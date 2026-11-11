package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/JohanSardar/lamp/internal/ci"
)

// Only the API-verified trusted workflow can select a host-enrolled model
// channel. Public/PR jobs still require zero private-service exceptions.
func (s *Service) ciModelPolicy(id string, r ci.Repository) *ci.ModelBroker {
	j := s.ciSnapshot(id)
	if textField(j.R, "trust_class") != "trusted" || r.ModelBroker == nil || len(r.ModelBroker.Workflows[j.Workflow]) == 0 {
		return nil
	}
	return r.ModelBroker
}
func (s *Service) ciBrokerNetwork(id string, r ci.Repository, exceptions []any) bool {
	b := s.ciModelPolicy(id, r)
	if b == nil {
		return len(exceptions) == 0
	}
	if len(exceptions) != 1 {
		return false
	}
	u, _ := url.Parse(b.Endpoint)
	port, _ := strconv.Atoi(u.Port())
	e, ok := exceptions[0].(map[string]any)
	return ok && len(e) == 3 && textField(e, "address") == u.Hostname() && textField(e, "transport") == "tcp" && uintField(e, "port") == uint64(port)
}
func ciBrokerCall(ctx context.Context, socket, path string, input, out any) error {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer tr.CloseIdleConnections()
	raw, _ := json.Marshal(input)
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://localhost"+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("enrolled CI model broker unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("CI model broker refused scoped authority")
	}
	if out != nil && json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(out) != nil {
		return errors.New("CI model broker response unavailable")
	}
	return nil
}
func (s *Service) ciModelEnvironment(ctx context.Context, id string, r ci.Repository) (map[string]string, error) {
	b := s.ciModelPolicy(id, r)
	if b == nil {
		return nil, nil
	}
	j := s.ciSnapshot(id)
	sandbox := textField(j.R, "sandbox_id")
	ca, err := os.ReadFile(b.CAFile)
	if err != nil || len(ca) > 65536 {
		return nil, errors.New("enrolled model CA unavailable")
	}
	// Public CA only; no upstream key or bearer enters a public exec request.
	_, err = s.guestPython(ctx, sandbox, `import os,sys,base64
p='/var/tmp/lamp-ci-model';os.makedirs(p,exist_ok=True)
with open('/etc/ssl/certs/ca-certificates.crt','rb') as f:roots=f.read()
with open(p+'/ca.pem','wb') as f:f.write(roots+b'\n'+base64.b64decode(sys.argv[1]))
os.chmod(p+'/ca.pem',0o644)
`, base64.StdEncoding.EncodeToString(ca))
	if err != nil {
		return nil, err
	}
	var grant struct {
		Token string `json:"token"`
		ID    string `json:"grant_id"`
	}
	err = ciBrokerCall(ctx, b.OwnerSocket, "/owner/grants", map[string]any{"sandbox_id": sandbox, "generation": s.sandboxGeneration(sandbox), "peer_ip": b.PeerIP, "ttl_seconds": 300, "connections": b.Workflows[j.Workflow], "settings": []string{}}, &grant)
	if err != nil {
		return nil, err
	}
	if grant.Token == "" || grant.ID == "" {
		return nil, errors.New("CI model grant incomplete")
	}
	if err = s.ciUpdate(id, func(j *ciJob) { j.BrokerGrant = grant.ID }); err != nil {
		ciBrokerCall(ctx, b.OwnerSocket, "/owner/revoke", map[string]string{"grant_id": grant.ID}, nil)
		return nil, err
	}
	// Like the native JIT token, this short-lived transport token goes only into
	// the private worker envelope. Durable records contain the revocation ID only.
	return map[string]string{"LAMP_MODEL_TOKEN": grant.Token, "LAMP_MODEL_URL": b.Endpoint, "LAMP_MODEL_CA": "/var/tmp/lamp-ci-model/ca.pem", "NODE_EXTRA_CA_CERTS": "/var/tmp/lamp-ci-model/ca.pem", "SSL_CERT_FILE": "/var/tmp/lamp-ci-model/ca.pem", "CODEX_CA_CERTIFICATE": "/var/tmp/lamp-ci-model/ca.pem"}, nil
}
func (s *Service) ciRevokeModel(ctx context.Context, id string, r ci.Repository) error {
	j := s.ciSnapshot(id)
	if j.BrokerGrant == "" {
		return nil
	}
	if r.ModelBroker == nil {
		return errors.New("CI model grant needs enrolled revocation channel")
	}
	if err := ciBrokerCall(ctx, r.ModelBroker.OwnerSocket, "/owner/revoke", map[string]string{"grant_id": j.BrokerGrant}, nil); err != nil {
		return err
	}
	return s.ciUpdate(id, func(j *ciJob) { j.BrokerGrant = "" })
}

// ciKeepModelGrant renews the job's broker grant while the job runs, so a long
// job keeps model access for its whole lifetime. The broker still caps each
// extension at one TTL, so a stopped or crashed controller lets the grant lapse.
// Cleanup revokes the grant when the job ends; stop must run before that.
func (s *Service) ciKeepModelGrant(ctx context.Context, id string, r ci.Repository) (stop func()) {
	grant := s.ciSnapshot(id).BrokerGrant
	if grant == "" || r.ModelBroker == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(60 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				call, end := context.WithTimeout(ctx, 10*time.Second)
				ciBrokerCall(call, r.ModelBroker.OwnerSocket, "/owner/renew", map[string]any{"grant_id": grant, "ttl_seconds": 300}, nil)
				end()
			}
		}
	}()
	return func() { cancel(); <-done }
}
