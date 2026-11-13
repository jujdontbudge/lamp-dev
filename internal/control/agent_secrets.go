package control

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lamp.eilmer.si/internal/broker"
	"lamp.eilmer.si/internal/surface"
)

func init() { surface.CoreNames["secret_request"] = true }
func validSecretHosts(hosts []string) bool {
	if len(hosts) == 0 || len(hosts) > 16 {
		return false
	}
	for _, h := range hosts {
		u, err := url.Parse("https://" + h)
		if err != nil || u.Host != h || u.Hostname() != h || u.User != nil || h != strings.ToLower(h) || strings.ContainsAny(h, "/?#\\ \t\r\n") {
			return false
		}
		if ip := net.ParseIP(h); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
			return false
		}
	}
	return true
}
func (s *Service) storeSecret(name, value string, hosts []string) error {
	s.secretMu.Lock()
	defer s.secretMu.Unlock()
	if !agentName.MatchString(name) || len(value) < 8 || len(value) > 16384 || strings.ContainsAny(value, "\r\n\x00") {
		return reject("INVALID_ARGUMENT", "invalid secret name or value")
	}
	if len(hosts) > 0 && !validSecretHosts(hosts) {
		return reject("INVALID_ARGUMENT", "secret hosts must be exact public lowercase DNS names")
	}
	for _, dir := range []string{"credentials", "secret-scopes"} {
		if err := PrivateDir(filepath.Join(s.dir, dir)); err != nil {
			return err
		}
	}
	// First revoke the old scope, so a partial replacement cannot broaden access.
	scope := filepath.Join(s.dir, "secret-scopes", name)
	if err := atomicSecret(scope, []byte("[]")); err != nil {
		return err
	}
	if err := atomicSecret(filepath.Join(s.dir, "credentials", name), []byte(value)); err != nil {
		return err
	}
	raw, _ := json.Marshal(hosts)
	return atomicSecret(scope, raw)
}
func atomicSecret(path string, value []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".entry-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(value)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err == nil {
		err = syncDirectory(filepath.Dir(path))
	}
	return err
}
func secretPlaceholder(rt *agentRuntime, name string) string {
	h := hmac.New(sha256.New, []byte(rt.token))
	h.Write([]byte(name))
	return "lamp_secret_" + hex.EncodeToString(h.Sum(nil))
}

func (s *Service) secretRequestHTTP(w http.ResponseWriter, r *http.Request, id string, rt *agentRuntime) {
	if rt == nil {
		s.readLock()
		allowed := s.agentOwner(requestCaller(r))
		s.mu.Unlock()
		if !allowed {
			writeError(w, id, "SCOPE_DENIED", "secret entry requests require owner or an enrolled agent capability", nil)
			return
		}
	}
	var in struct {
		Name  string   `json:"name"`
		Hosts []string `json:"hosts"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8000)).Decode(&in) != nil || !agentName.MatchString(in.Name) || !validSecretHosts(in.Hosts) {
		writeError(w, id, "INVALID_ARGUMENT", "name and exact allowed public hosts are required", nil)
		return
	}
	if rt != nil {
		found := false
		for _, name := range rt.enrollment.Secrets {
			found = found || name == in.Name
		}
		if !found {
			writeError(w, id, "SCOPE_DENIED", "secret name is not enrolled for this agent", nil)
			return
		}
	}
	select {
	case s.agentEntries <- struct{}{}:
	default:
		writeError(w, id, "CONTROL_QUOTA_EXCEEDED", "too many open secret entry forms", nil)
		return
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		<-s.agentEntries
		writeError(w, id, "HOST_UNAVAILABLE", "secret entry listener unavailable", nil)
		return
	}
	nonce := newID("entry")
	host := listener.Addr().String()
	used := make(chan struct{}, 1)
	page := template.Must(template.New("entry").Parse(`<!doctype html><meta name="referrer" content="no-referrer"><title>Lamp secret entry</title><h1>Enter {{.Name}}</h1><p>This value stays on the Lamp host. Allowed hosts: {{.Hosts}}</p><form method="post"><input type="hidden" name="nonce" value="{{.Nonce}}"><label>Secret <input type="password" name="value" required autocomplete="off"></label><button>Store secret</button></form>`))
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(out http.ResponseWriter, request *http.Request) {
		out.Header().Set("Cache-Control", "no-store")
		out.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'")
		out.Header().Set("Referrer-Policy", "no-referrer")
		if request.Host != host || request.URL.Path != "/" {
			http.Error(out, "Unavailable", 404)
			return
		}
		if request.Method == "GET" {
			page.Execute(out, map[string]string{"Name": in.Name, "Hosts": strings.Join(in.Hosts, ", "), "Nonce": nonce})
			return
		}
		if request.Method != "POST" || request.Header.Get("Origin") != "http://"+host {
			http.Error(out, "Origin mismatch", 403)
			return
		}
		request.Body = http.MaxBytesReader(out, request.Body, 20000)
		if request.ParseForm() != nil || request.Form.Get("nonce") != nonce {
			http.Error(out, "Entry mismatch", 403)
			return
		}
		select {
		case used <- struct{}{}:
		default:
			http.Error(out, "Entry already used", 409)
			return
		}
		if err := s.storeSecret(in.Name, request.Form.Get("value"), in.Hosts); err != nil {
			http.Error(out, "Secret could not be stored", 400)
			return
		}
		io.WriteString(out, "Secret stored on the Lamp host. You can close this page.")
	})}
	stop := context.AfterFunc(s.context(), func() { server.Close() })
	timer := time.AfterFunc(10*time.Minute, func() { stop(); server.Close() })
	go func() { defer func() { <-s.agentEntries }(); server.Serve(listener); timer.Stop(); stop() }()
	prepareJSON(w, 200, map[string]any{"result": map[string]any{"entry_url": "http://" + host + "/"}, "request_id": id})()
}

// Explicit host proxy: HTTPS terminates at the host, substitutes only enrolled
// placeholders for the approved origin, then redacts credentials in responses.
func (s *Service) secretUse(rt *agentRuntime, w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&in) != nil {
		writeError(w, "", "INVALID_ARGUMENT", "invalid secret request", nil)
		return
	}
	u, err := url.Parse(in.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Host == "" || u.Port() != "" && u.Port() != "443" {
		writeError(w, "", "SCOPE_DENIED", "secret proxy requires a public HTTPS origin", nil)
		return
	}
	if in.Method == "" {
		in.Method = "GET"
	}
	if in.Method != "GET" && in.Method != "POST" {
		writeError(w, "", "SCOPE_DENIED", "secret proxy permits GET or POST", nil)
		return
	}
	redactions := [][]byte{}
	s.secretMu.RLock()
	for _, name := range rt.enrollment.Secrets {
		secret, err := s.readAgentSecret(name)
		if err != nil {
			continue
		}
		redactions = append(redactions, secret)
		var hosts []string
		scope, err := os.ReadFile(filepath.Join(s.dir, "secret-scopes", name))
		if err != nil || json.Unmarshal(scope, &hosts) != nil {
			continue
		}
		allowed := false
		for _, host := range hosts {
			allowed = allowed || host == u.Hostname()
		}
		if allowed {
			for header, value := range in.Headers {
				in.Headers[header] = strings.ReplaceAll(value, secretPlaceholder(rt, name), string(secret))
			}
			in.Body = strings.ReplaceAll(in.Body, secretPlaceholder(rt, name), string(secret))
		}
	}
	s.secretMu.RUnlock()
	// Never send even an opaque capability marker to an unapproved origin.
	for _, name := range rt.enrollment.Secrets {
		marker := secretPlaceholder(rt, name)
		remaining := strings.Contains(in.Body, marker)
		for _, value := range in.Headers {
			remaining = remaining || strings.Contains(value, marker)
		}
		if remaining {
			writeError(w, "", "SCOPE_DENIED", "secret host is not approved", nil)
			return
		}
	}
	request, err := http.NewRequestWithContext(r.Context(), in.Method, u.String(), strings.NewReader(in.Body))
	if err != nil {
		writeError(w, "", "INVALID_ARGUMENT", "invalid upstream request", nil)
		return
	}
	for name, value := range in.Headers {
		if strings.EqualFold(name, "Host") || strings.EqualFold(name, "Proxy-Authorization") {
			writeError(w, "", "SCOPE_DENIED", "header not permitted", nil)
			return
		}
		request.Header.Set(name, value)
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, a := range addresses {
			if !a.IP.IsGlobalUnicast() || a.IP.IsPrivate() || a.IP.IsLoopback() || a.IP.IsLinkLocalUnicast() {
				continue
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
		}
		return nil, reject("SCOPE_DENIED", "private upstream refused")
	}}
	client := &http.Client{Transport: transport, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		writeError(w, "", "DATA_UNAVAILABLE", "upstream secret request failed", nil)
		return
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		writeError(w, "", "DATA_UNAVAILABLE", "upstream response too large", nil)
		return
	}
	raw = broker.RedactSecrets(raw, redactions)
	prepareJSON(w, 200, map[string]any{"status": response.StatusCode, "body": string(raw)})()
}
