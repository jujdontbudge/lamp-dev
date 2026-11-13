package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lamp.eilmer.si/internal/surface"
)

func init() {
	surface.CoreNames["auth_flow_create"] = true
	surface.CoreNames["auth_flow_complete"] = true
	lifeHandlers["auth_flow_create"] = (*Service).planAgentAuth
}

// OpenRouter's PKCE sign-in exchanges the browser's one-use code on the host.
// No authorization code, verifier or returned provider key enters the journal.
func (s *Service) planAgentAuth(a caller, in map[string]any) (lifePlan, error) {
	if !s.agentOwner(a) {
		return lifePlan{}, reject("SCOPE_DENIED", "provider sign-in requires owner credential")
	}
	if textField(in, "identity_provider_id") == "codex" && textField(in, "purpose") == "provider_login" {
		return s.planCodexCustody(a)
	}
	if textField(in, "identity_provider_id") != "openrouter" || textField(in, "purpose") != "provider_login" {
		return lifePlan{}, reject("AUTH_TRANSPORT_UNSUPPORTED", "release sign-in supports OpenRouter PKCE for opencode; subscription custody still requires its vendor adapter")
	}
	pending := 0
	for _, flow := range s.life.Networks {
		if textField(flow.R, "kind") == "auth_flow" && textField(flow.R, "state") == "pending" && !sessionExpired(flow.R) {
			pending++
		}
	}
	if pending >= 16 {
		return lifePlan{}, reject("CONTROL_QUOTA_EXCEEDED", "too many pending sign-in flows")
	}
	n := s.addNetwork("af", "auth_flow", a.Principal)
	id := textField(n.R, "id")
	verifier := newID("pkce") + newID("pkce")
	nonce := newID("state")
	sum := sha256.Sum256([]byte(verifier))
	n.R["state"], n.R["purpose"], n.R["identity_provider_id"], n.R["challenge"], n.R["expires_at"] = "pending", "provider_login", "openrouter", nonce, time.Now().Add(10*time.Minute).UTC().Format(time.RFC3339Nano)
	return lifePlan{ref: lifeRef("auth_flow", id), run: func(ctx context.Context) (map[string]any, error) {
		if err := PrivateDir(filepath.Join(s.dir, "agent-auth")); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(map[string]string{"verifier": verifier, "state": nonce})
		p := filepath.Join(s.dir, "agent-auth", id)
		if err := os.WriteFile(p, raw, 0600); err != nil {
			return nil, err
		}
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		callback := "http://" + listener.Addr().String() + "/callback?state=" + url.QueryEscape(nonce)
		authorization := "https://openrouter.ai/auth?" + url.Values{"callback_url": {callback}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}.Encode()
		server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			if r.URL.Path != "/callback" || r.Method != "GET" || r.URL.Query().Get("state") != nonce {
				http.Error(w, "Sign-in state mismatch", 403)
				return
			}
			if err := s.completeAgentAuth(r.Context(), id, r.URL.Query().Get("code"), nonce); err != nil {
				http.Error(w, "Sign-in failed. Start a new Lamp auth flow.", 400)
				return
			}
			io.WriteString(w, "Signed in. The model key stays on the Lamp host. You can close this page.")
		})}
		stop := context.AfterFunc(s.context(), func() { server.Close() })
		timer := time.AfterFunc(10*time.Minute, func() { stop(); server.Close(); os.Remove(p) })
		go func() { server.Serve(listener); timer.Stop(); stop() }()
		return map[string]any{"auth_flow_id": id, "authorization_url": authorization}, nil
	}, finish: func(result map[string]any, err error) error {
		if err == nil {
			n.R["authorization_url"] = result["authorization_url"]
			delete(result, "authorization_url")
		} else {
			n.R["state"] = "failed"
		}
		return err
	}}, nil
}
func (s *Service) completeAgentAuth(ctx context.Context, id, code, state string) error {
	if code == "" || len(code) > 4096 {
		return reject("INVALID_ARGUMENT", "one-use provider code required")
	}
	s.readLock()
	n := s.life.Networks[id]
	valid := n != nil && textField(n.R, "state") == "pending" && textField(n.R, "challenge") == state && !sessionExpired(n.R)
	s.mu.Unlock()
	if !valid {
		return reject("SCOPE_DENIED", "flow expired, completed or state mismatch")
	}
	p := filepath.Join(s.dir, "agent-auth", id)
	raw, err := os.ReadFile(p)
	if err != nil {
		return reject("SCOPE_DENIED", "flow verifier unavailable")
	}
	var private map[string]string
	if json.Unmarshal(raw, &private) != nil || private["state"] != state {
		return reject("SCOPE_DENIED", "flow state mismatch")
	}
	// Consume before the exchange; failure requires a fresh flow, never replay.
	if err = os.Remove(p); err != nil {
		return reject("SCOPE_DENIED", "flow was already consumed")
	}
	defer func() {
		s.readLock()
		defer s.mu.Unlock()
		if textField(n.R, "state") == "pending" {
			n.R["state"] = "failed"
			editResource(n.R)
			if s.saveLife() != nil {
				s.broken = true
			}
		}
	}()
	body, _ := json.Marshal(map[string]string{"code": code, "code_verifier": private["verifier"], "code_challenge_method": "S256"})
	exchangeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(exchangeCtx, "POST", "https://openrouter.ai/api/v1/auth/keys", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return reject("DATA_UNAVAILABLE", "provider exchange failed")
	}
	defer response.Body.Close()
	var key struct {
		Key string `json:"key"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 20000)).Decode(&key) != nil || len(key.Key) < 8 || strings.ContainsAny(key.Key, "\r\n\x00") {
		return reject("DATA_UNAVAILABLE", "provider exchange rejected")
	}
	if err = os.WriteFile(filepath.Join(s.dir, "credentials", id), []byte(key.Key), 0600); err != nil {
		return err
	}
	s.readLock()
	defer s.mu.Unlock()
	source := s.addNetwork("cr", "credential_source", s.journal.Control.OwnerID)
	source.R["state"], source.R["provider"], source.R["account_ref"] = "ready", "openrouter", id
	n.R["state"] = "completed"
	n.R["labels"] = map[string]any{"lamp.credential_source_id": source.R["id"]}
	editResource(n.R)
	if err = s.saveLife(); err != nil {
		s.broken = true
		return err
	}
	return nil
}
func (s *Service) agentAuthCompleteHTTP(w http.ResponseWriter, r *http.Request, id, flow string) {
	s.readLock()
	allowed := s.agentOwner(requestCaller(r))
	s.mu.Unlock()
	if !allowed {
		writeError(w, id, "SCOPE_DENIED", "owner sign-in required", nil)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8000))
	in, e := surface.Decode(raw)
	if err != nil || e != nil {
		writeError(w, id, "INVALID_ARGUMENT", "invalid sign-in input", nil)
		return
	}
	if err = s.completeAgentAuth(r.Context(), flow, textField(in, "code"), textField(in, "state")); err != nil {
		writeError(w, id, "SCOPE_DENIED", err.Error(), nil)
		return
	}
	prepareJSON(w, 200, map[string]any{"result": map[string]any{"secret_available": false}, "request_id": id})()
}

// Import is explicit and host-only: the owner's original is never changed.
func (s *Service) planCodexCustody(a caller) (lifePlan, error) {
	n := s.addNetwork("af", "auth_flow", a.Principal)
	n.R["state"], n.R["purpose"], n.R["identity_provider_id"] = "pending", "provider_login", "codex"
	id := textField(n.R, "id")
	return lifePlan{ref: lifeRef("auth_flow", id), run: func(ctx context.Context) (map[string]any, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		p := filepath.Join(home, ".codex", "auth.json")
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() {
			return nil, reject("CREDENTIAL_UNAVAILABLE", "sign in with Codex CLI on this host first")
		}
		raw, err := os.ReadFile(p)
		if err != nil || len(raw) > 65536 {
			return nil, reject("CREDENTIAL_UNAVAILABLE", "cannot read Codex host sign-in")
		}
		var auth struct {
			Tokens struct {
				Access  string `json:"access_token"`
				Account string `json:"account_id"`
			} `json:"tokens"`
		}
		if json.Unmarshal(raw, &auth) != nil || auth.Tokens.Access == "" || auth.Tokens.Account == "" {
			return nil, reject("CREDENTIAL_UNAVAILABLE", "Codex ChatGPT sign-in required")
		}
		if err = os.WriteFile(filepath.Join(s.dir, "credentials", id), raw, 0600); err != nil {
			return nil, err
		}
		return map[string]any{"auth_flow_id": id}, nil
	}, finish: func(result map[string]any, err error) error {
		if err != nil {
			n.R["state"] = "failed"
			return err
		}
		source := s.addNetwork("cr", "credential_source", a.Principal)
		source.R["state"], source.R["provider"], source.R["account_ref"] = "ready", "codex", id
		n.R["state"] = "completed"
		n.R["labels"] = map[string]any{"lamp.credential_source_id": source.R["id"]}
		result["credential_source_id"] = source.R["id"]
		return nil
	}}, nil
}
