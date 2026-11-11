// Command lamp-fleeting is a GitLab Runner Fleeting plugin: each autoscaler
// instance is a fresh Lamp sandbox, created and deleted through Lamp's public
// API only. With `max_use_count = 1` every GitLab job gets its own VM.
//
//	[runners.autoscaler]
//	  plugin = "/usr/local/bin/lamp-fleeting"
//	  capacity_per_instance = 1
//	  max_use_count = 1
//	  max_instances = 2
//	  [runners.autoscaler.plugin_config]
//	    recipe_id     = "rc_..."           # trusted recipe with openssh-server
//	    callback_addr = "10.219.29.1:3935" # enrolled private endpoint, guest-reachable
//	    state_dir     = "/var/lib/lamp-fleeting"
//	  [runners.autoscaler.connector_config]
//	    use_static_credentials = false   # the plugin reports the guest user and key
//
// Lamp authority comes from LAMP_SOCKET or LAMP_API_URL with LAMP_TOKEN or
// LAMP_TOKEN_FILE in the runner's environment.
//
// SSH transport: guests accept no inbound connections, so a small agent in
// each guest dials the plugin's callback address (an exact enrolled network
// exception) and splices each requested stream to the guest's loopback sshd.
// The runner connects to a per-instance loopback port on the runner host.
// A per-instance random secret authenticates the agent's connections.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"gitlab.com/gitlab-org/fleeting/fleeting/plugin"
	"gitlab.com/gitlab-org/fleeting/fleeting/provider"
	"golang.org/x/crypto/ssh"
)

type instance struct {
	Sandbox string `json:"sandbox"`
	Secret  string `json:"secret"`
	Exec    string `json:"exec,omitempty"`
	User    string `json:"user,omitempty"`
	port    int
	ctl     net.Conn
	pending chan net.Conn
}

type Group struct {
	RecipeID     string `json:"recipe_id"`
	CallbackAddr string `json:"callback_addr"`
	StateDir     string `json:"state_dir"`
	MaxSize      int    `json:"max_size"`

	log      hclog.Logger
	api      *client
	settings provider.Settings
	key      []byte
	pub      string
	mu       sync.Mutex
	items    map[string]*instance
	cb       net.Listener
}

var _ provider.InstanceGroup = (*Group)(nil)

func (g *Group) Init(ctx context.Context, log hclog.Logger, settings provider.Settings) (provider.ProviderInfo, error) {
	g.log, g.settings, g.items = log, settings, map[string]*instance{}
	if g.RecipeID == "" || g.CallbackAddr == "" || g.StateDir == "" {
		return provider.ProviderInfo{}, errors.New("recipe_id, callback_addr and state_dir are required")
	}
	if g.MaxSize == 0 {
		g.MaxSize = 2
	}
	var err error
	if g.api, err = newClient(); err != nil {
		return provider.ProviderInfo{}, err
	}
	os.MkdirAll(g.StateDir, 0700)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, _ := ssh.MarshalPrivateKey(priv, "lamp-fleeting")
	g.key = pem.EncodeToMemory(block)
	sp, _ := ssh.NewPublicKey(pub)
	g.pub = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp)))
	// Instances from an earlier plugin process are adopted for Update and
	// deletion, never reused: their agents held the old process's key.
	if raw, e := os.ReadFile(filepath.Join(g.StateDir, "instances.json")); e == nil {
		json.Unmarshal(raw, &g.items)
	}
	if g.cb, err = net.Listen("tcp", g.CallbackAddr); err != nil {
		return provider.ProviderInfo{}, fmt.Errorf("callback listener: %w", err)
	}
	go g.acceptAgents()
	return provider.ProviderInfo{ID: "lamp/" + g.RecipeID, MaxSize: g.MaxSize, Version: "1"}, nil
}

func (g *Group) save() {
	raw, _ := json.Marshal(g.items)
	os.WriteFile(filepath.Join(g.StateDir, "instances.json"), raw, 0600)
}

func (g *Group) Update(ctx context.Context, fn func(string, provider.State)) error {
	g.mu.Lock()
	ids := make([]string, 0, len(g.items))
	for id := range g.items {
		ids = append(ids, id)
	}
	g.mu.Unlock()
	for _, id := range ids {
		g.mu.Lock()
		it := g.items[id]
		g.mu.Unlock()
		out, err := g.api.do("GET", "/v1/sandboxes/"+it.Sandbox, nil, nil)
		state := provider.StateCreating
		if err != nil && strings.Contains(err.Error(), "HTTP 404") {
			state = provider.StateDeleted
		} else if d, _ := out["data"].(map[string]any); d != nil {
			switch d["state"] {
			case "ready":
				state = provider.StateCreating
				g.mu.Lock()
				if it.ctl != nil {
					state = provider.StateRunning
				}
				g.mu.Unlock()
				if it.Exec == "" {
					g.startAgent(id, it)
				}
			case "deleting", "stopping":
				state = provider.StateDeleting
			case "deleted", "failed":
				state = provider.StateDeleted
			}
		}
		if state == provider.StateDeleted {
			g.mu.Lock()
			delete(g.items, id)
			g.save()
			g.mu.Unlock()
		}
		fn(id, state)
	}
	return nil
}

func (g *Group) Increase(ctx context.Context, n int) (int, error) {
	done := 0
	for i := 0; i < n; i++ {
		id := "lf-" + randHex(8)
		out, err := g.api.do("POST", "/v1/sandboxes", map[string]any{"recipe_id": g.RecipeID}, map[string]string{"Idempotency-Key": "fleeting:" + id})
		if err != nil {
			return done, err
		}
		sandbox := ""
		if refs, ok := out["resource_ids"].([]any); ok && len(refs) > 0 {
			sandbox, _ = refs[0].(map[string]any)["id"].(string)
		}
		g.mu.Lock()
		g.items[id] = &instance{Sandbox: sandbox, Secret: randHex(32)}
		g.save()
		g.mu.Unlock()
		done++
	}
	return done, nil
}

// startAgent installs this process's public key and runs sshd plus the reverse
// agent as one long-lived command; deleting the sandbox ends both.
func (g *Group) startAgent(id string, it *instance) {
	agent := `import os,socket,sys,threading
cb,iid,sec,user=sys.argv[1],sys.argv[2],sys.argv[3],sys.argv[4]
h,p=cb.rsplit(':',1)
def pipe(a,b):
  try:
    while True:
      d=a.recv(65536)
      if not d: break
      b.sendall(d)
  except OSError: pass
  for s in (a,b):
    try: s.shutdown(socket.SHUT_RDWR)
    except OSError: pass
def stream():
  up=socket.create_connection((h,int(p)));up.sendall(('data %s %s\n'%(iid,sec)).encode())
  down=socket.create_connection(('127.0.0.1',2222))
  threading.Thread(target=pipe,args=(up,down),daemon=True).start();pipe(down,up)
ctl=socket.create_connection((h,int(p)));ctl.sendall(('ctl %s %s %s\n'%(iid,sec,user)).encode())
for line in ctl.makefile():
  if line.strip()=='open': threading.Thread(target=stream,daemon=True).start()
`
	// Sandbox commands run unprivileged: sshd runs as that user on loopback
	// with its own host key, and the runner logs in as the same user.
	script := `set -e
D=/var/tmp/lamp-fleeting
mkdir -p "$D" && chmod 700 "$D"
printf '%s\n' "$1" > "$D/authorized_keys" && chmod 600 "$D/authorized_keys"
[ -f "$D/hostkey" ] || ssh-keygen -q -t ed25519 -N '' -f "$D/hostkey"
/usr/sbin/sshd -f /dev/null -o ListenAddress=127.0.0.1 -o Port=2222 -o HostKey="$D/hostkey" -o AuthorizedKeysFile="$D/authorized_keys" \
  -o StrictModes=no -o UsePAM=no -o PasswordAuthentication=no -o PidFile="$D/sshd.pid" -o "SetEnv=PATH=/opt/gitlab-runner:/usr/local/bin:/usr/bin:/bin HOME=$D"
exec python3 -c "$2" "$3" "$4" "$5" "$(id -un)"`
	out, err := g.api.do("POST", "/v1/execs", map[string]any{"sandbox_id": it.Sandbox, "argv": []string{"sh", "-c", script, "lamp", g.pub, agent, g.CallbackAddr, id, it.Secret}},
		map[string]string{"Idempotency-Key": "fleeting-agent:" + id})
	if err != nil {
		g.log.Error("agent start failed", "instance", id, "err", err)
		return
	}
	if refs, ok := out["resource_ids"].([]any); ok && len(refs) > 0 {
		g.mu.Lock()
		it.Exec, _ = refs[0].(map[string]any)["id"].(string)
		g.save()
		g.mu.Unlock()
	}
}

func (g *Group) acceptAgents() {
	for {
		c, err := g.cb.Accept()
		if err != nil {
			return
		}
		go func() {
			c.SetReadDeadline(time.Now().Add(10 * time.Second))
			r := bufio.NewReader(c)
			line, err := r.ReadString('\n')
			f := strings.Fields(line)
			if err != nil || len(f) < 3 || len(f) > 4 {
				c.Close()
				return
			}
			c.SetReadDeadline(time.Time{})
			g.mu.Lock()
			it := g.items[f[1]]
			ok := it != nil && subtle.ConstantTimeCompare([]byte(it.Secret), []byte(f[2])) == 1
			if ok && f[0] == "ctl" {
				if it.ctl != nil {
					it.ctl.Close()
				}
				it.ctl, it.pending = c, make(chan net.Conn, 4)
				if len(f) == 4 {
					it.User = f[3]
				}
			}
			pending := (chan net.Conn)(nil)
			if ok {
				pending = it.pending
			}
			g.mu.Unlock()
			switch {
			case !ok:
				c.Close()
			case f[0] == "data" && pending != nil:
				pending <- &bufConn{Conn: c, r: r}
			case f[0] != "ctl":
				c.Close()
			}
		}()
	}
}

type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// ConnectInfo exposes the instance on a loopback port of the runner host;
// each accepted connection asks the guest agent for a fresh stream to sshd.
func (g *Group) ConnectInfo(ctx context.Context, id string) (provider.ConnectInfo, error) {
	g.mu.Lock()
	it := g.items[id]
	g.mu.Unlock()
	if it == nil || it.ctl == nil {
		return provider.ConnectInfo{}, provider.ErrInstanceUnhealthy
	}
	if it.port == 0 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return provider.ConnectInfo{}, err
		}
		it.port = l.Addr().(*net.TCPAddr).Port
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go g.bridge(it, c, l)
			}
		}()
	}
	info := provider.ConnectInfo{ConnectorConfig: g.settings.ConnectorConfig, ID: id, ExternalAddr: "127.0.0.1:" + strconv.Itoa(it.port), InternalAddr: "127.0.0.1:" + strconv.Itoa(it.port)}
	info.OS, info.Arch, info.Protocol, info.ProtocolPort = "linux", "amd64", provider.ProtocolSSH, it.port
	info.Username, info.Key, info.UseStaticCredentials = it.User, g.key, true
	if info.Username == "" {
		info.Username = "root"
	}
	return info, nil
}

func (g *Group) bridge(it *instance, c net.Conn, l net.Listener) {
	defer c.Close()
	g.mu.Lock()
	ctl, pending := it.ctl, it.pending
	g.mu.Unlock()
	if ctl == nil {
		return
	}
	if _, err := io.WriteString(ctl, "open\n"); err != nil {
		return
	}
	select {
	case up := <-pending:
		defer up.Close()
		go io.Copy(up, c)
		io.Copy(c, up)
	case <-time.After(20 * time.Second):
	}
}

func (g *Group) Decrease(ctx context.Context, ids []string) ([]string, error) {
	var done []string
	for _, id := range ids {
		g.mu.Lock()
		it := g.items[id]
		g.mu.Unlock()
		if it == nil {
			done = append(done, id)
			continue
		}
		if err := g.api.deleteSandbox(it.Sandbox, "fleeting-decrease"); err != nil {
			g.log.Error("delete failed", "instance", id, "err", err)
			continue
		}
		g.mu.Lock()
		if it.ctl != nil {
			it.ctl.Close()
		}
		delete(g.items, id)
		g.save()
		g.mu.Unlock()
		done = append(done, id)
	}
	return done, nil
}

func (g *Group) Heartbeat(ctx context.Context, id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if it := g.items[id]; it == nil || it.ctl == nil {
		return provider.ErrInstanceUnhealthy
	}
	return nil
}
func (g *Group) Suspend(context.Context, []string) ([]string, error) {
	return nil, provider.ErrSuspendResumeNotSupported
}
func (g *Group) Resume(context.Context, []string) ([]string, error) {
	return nil, provider.ErrSuspendResumeNotSupported
}

// Shutdown deletes every instance: none outlives the runner's autoscaler.
func (g *Group) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	ids := make([]string, 0, len(g.items))
	for id := range g.items {
		ids = append(ids, id)
	}
	g.mu.Unlock()
	_, err := g.Decrease(ctx, ids)
	if g.cb != nil {
		g.cb.Close()
	}
	return err
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func main() { plugin.Main(&Group{}, plugin.VersionInfo{Name: "lamp-fleeting", Version: "1"}) }

// ----------------------------------------------------------- Lamp client --

type client struct {
	http  *http.Client
	base  string
	token string
}

func newClient() (*client, error) {
	c := &client{http: &http.Client{Timeout: 90 * time.Second}, base: strings.TrimSuffix(os.Getenv("LAMP_API_URL"), "/")}
	if c.base == "" {
		socket := os.Getenv("LAMP_SOCKET")
		if socket == "" {
			return nil, errors.New("LAMP_SOCKET or LAMP_API_URL required")
		}
		c.base = "http://lamp"
		c.http.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}
	}
	c.token = os.Getenv("LAMP_TOKEN")
	if f := os.Getenv("LAMP_TOKEN_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, errors.New("LAMP_TOKEN_FILE unreadable")
		}
		c.token = strings.TrimSpace(string(b))
	}
	return c, nil
}

func (c *client) do(method, path string, body any, headers map[string]string) (map[string]any, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("Lamp API unavailable")
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&out)
	if res.StatusCode >= 300 {
		return out, fmt.Errorf("Lamp %s %s: HTTP %d", method, path, res.StatusCode)
	}
	return out, nil
}

func (c *client) deleteSandbox(sandbox, why string) error {
	out, err := c.do("GET", "/v1/sandboxes/"+sandbox, nil, nil)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return nil
		}
		return err
	}
	d, _ := out["data"].(map[string]any)
	if d["state"] == "deleted" {
		return nil
	}
	rev := fmt.Sprint(d["revision"])
	_, err = c.do("DELETE", "/v1/sandboxes/"+sandbox, nil, map[string]string{"Idempotency-Key": why + ":" + sandbox + ":" + rev, "If-Match": rev})
	return err
}

var _ = base64.StdEncoding
