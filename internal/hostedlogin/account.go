// SPDX-License-Identifier: Apache-2.0
package hostedlogin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"lamp.eilmer.si/internal/accountidentity"
)

type AccountConfig struct {
	Listen      string     `json:"listen"`
	Origin      string     `json:"origin"`
	Certificate string     `json:"certificate"`
	Key         string     `json:"key"`
	Clients     []string   `json:"clients"`
	Subjects    []Identity `json:"subjects"`
}
type destination struct{ Audience, State, Challenge string }
type accountFlow struct {
	mu           sync.Mutex
	CSRF, SignIn string
	Client       *http.Client
	Expires      time.Time
	Destination  *destination
	Attempts     int
}
type accountSession struct {
	Identity  Identity
	Client    *http.Client
	SessionID string
	Expires   time.Time
}
type code struct {
	Grant               Grant
	Audience, Challenge string
	Expires             time.Time
}
type Account struct {
	Config   AccountConfig
	verifier *accountidentity.Verifier
	mu       sync.Mutex
	flows    map[string]*accountFlow
	sessions map[string]*accountSession
	codes    map[string]code
}

func NewAccount(c AccountConfig) (*Account, error) {
	if !ValidOrigin(c.Origin, "account.eilmer.si") || len(c.Subjects) == 0 || len(c.Clients) == 0 {
		return nil, errors.New("account origin, private subjects and exact clients required")
	}
	hostnames := map[string]bool{}
	for _, origin := range c.Clients {
		u, e := url.Parse(origin)
		if e != nil || !ValidOrigin(origin, u.Hostname()) || (u.Hostname() != "relay.eilmer.si" && !strings.HasSuffix(u.Hostname(), ".fleet.eilmer.si")) || hostnames[strings.ToLower(u.Hostname())] {
			return nil, errors.New("distinct registered eilmer.si client hostnames required: cookies ignore ports")
		}
		hostnames[strings.ToLower(u.Hostname())] = true
	}
	for _, id := range c.Subjects {
		if id.Issuer != "https://clerk.eilmer.si" || id.Subject == "" {
			return nil, errors.New("shared production Eilmer issuer/subject allowlist required")
		}
	}
	return &Account{Config: c, verifier: &accountidentity.Verifier{Issuer: "https://clerk.eilmer.si", Parties: []string{"https://accounts.eilmer.si"}, JWKS: "https://clerk.eilmer.si/.well-known/jwks.json"}, flows: map[string]*accountFlow{}, sessions: map[string]*accountSession{}, codes: map[string]code{}}, nil
}
func (a *Account) prune() {
	now := time.Now()
	for k, v := range a.flows {
		if now.After(v.Expires) {
			delete(a.flows, k)
		}
	}
	for k, v := range a.sessions {
		if now.After(v.Expires) {
			delete(a.sessions, k)
		}
	}
	for k, v := range a.codes {
		if now.After(v.Expires) {
			delete(a.codes, k)
		}
	}
}
func (a *Account) allowed(id Identity) bool {
	for _, x := range a.Config.Subjects {
		if x == id {
			return true
		}
	}
	return false
}
func (a *Account) registered(origin string) bool {
	for _, x := range a.Config.Clients {
		if x == origin {
			return true
		}
	}
	return false
}
func (a *Account) newFlow(w http.ResponseWriter, d *destination) (*accountFlow, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prune()
	if len(a.flows) >= 64 {
		http.Error(w, "sign-in capacity unavailable", 503)
		return nil, false
	}
	jar, _ := cookiejar.New(nil)
	f := &accountFlow{CSRF: Random(), Destination: d, Expires: time.Now().Add(5 * time.Minute), Client: &http.Client{Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	raw := Random()
	a.flows[Digest(raw)] = f
	SetCookie(w, "__Host-eilmer_flow", raw, 300)
	return f, true
}
func (a *Account) signIn(w http.ResponseWriter, f *accountFlow) {
	HTML(w, "Eilmer account sign-in", `<p>Sign in to the existing shared Eilmer account. No public sign-up is available here.</p><form method=post action=/signin><input type=hidden name=csrf value="{{.}}"><label>Email <input type=email name=email required autocomplete=username></label><button>Send sign-in code</button></form>`, f.CSRF)
}
func (a *Account) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	Security(w)
	w.Header().Set("Connection", "close")
	u, _ := url.Parse(a.Config.Origin)
	if r.TLS == nil || r.TLS.ServerName != u.Hostname() || r.Host != u.Host || r.URL.RawQuery != "" {
		http.Error(w, "wrong account origin", 421)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/closed" {
		HTML(w, "Private Eilmer account", `<p>Public sign-up is disabled. Contact your fleet owner for enrollment.</p>`, nil)
		return
	}
	if r.URL.Path == "/redeem" {
		a.redeem(w, r)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/" {
		if raw, ok := Cookie(r, "__Host-eilmer_account"); ok {
			a.mu.Lock()
			a.prune()
			s := a.sessions[Digest(raw)]
			a.mu.Unlock()
			if s != nil {
				HTML(w, "Eilmer account", `<p>Signed in as {{.Issuer}} / {{.Subject}}.</p>`, s.Identity)
				return
			}
		}
		if f, ok := a.newFlow(w, nil); ok {
			a.signIn(w, f)
		}
		return
	}
	if r.Method != "POST" || !Form(w, r) {
		http.Error(w, "account POST required", 405)
		return
	}
	if r.URL.Path == "/authorize" {
		origin := r.Header.Get("Origin")
		d := &destination{Audience: r.PostForm.Get("audience"), State: r.PostForm.Get("state"), Challenge: r.PostForm.Get("challenge")}
		if !a.registered(origin) || d.Audience != origin || len(d.State) != 64 || len(d.Challenge) != 43 {
			http.Error(w, "unregistered account client", 403)
			return
		}
		if raw, ok := Cookie(r, "__Host-eilmer_account"); ok {
			a.mu.Lock()
			a.prune()
			s := a.sessions[Digest(raw)]
			a.mu.Unlock()
			if s != nil && a.refresh(r.Context(), s) == nil {
				a.authorize(w, s.Identity, d)
				return
			}
		}
		if f, ok := a.newFlow(w, d); ok {
			a.signIn(w, f)
		}
		return
	}
	if r.Header.Get("Origin") != a.Config.Origin {
		http.Error(w, "account origin required", 403)
		return
	}
	raw, ok := Cookie(r, "__Host-eilmer_flow")
	a.mu.Lock()
	a.prune()
	f := a.flows[Digest(raw)]
	if !ok || f == nil || r.PostForm.Get("csrf") != f.CSRF || f.Attempts >= 5 {
		a.mu.Unlock()
		http.Error(w, "account flow denied", 403)
		return
	}
	f.Attempts++
	a.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/signin":
		if f.SignIn != "" {
			http.Error(w, "code already requested", 409)
			return
		}
		email := r.PostForm.Get("email")
		if len(email) > 254 || !strings.Contains(email, "@") {
			http.Error(w, "email required", 400)
			return
		}
		if _, e := frontend(r.Context(), f.Client, "GET", "/v1/environment", nil); e != nil {
			http.Error(w, "sign-in unavailable", 503)
			return
		}
		if _, e := frontend(r.Context(), f.Client, "GET", "/v1/client", nil); e != nil {
			http.Error(w, "sign-in unavailable", 503)
			return
		}
		j, e := frontend(r.Context(), f.Client, "POST", "/v1/client/sign_ins", url.Values{"identifier": {email}})
		if e != nil {
			http.Error(w, "sign-in denied or unavailable", 403)
			return
		}
		response := object(j, "response")
		id, _ := response["id"].(string)
		factors, _ := response["supported_first_factors"].([]any)
		address := ""
		for _, v := range factors {
			m, _ := v.(map[string]any)
			if m["strategy"] == "email_code" {
				address, _ = m["email_address_id"].(string)
			}
		}
		if !validID(id, "sia_") || address == "" {
			http.Error(w, "email-code sign-in unavailable", 403)
			return
		}
		if _, e = frontend(r.Context(), f.Client, "POST", "/v1/client/sign_ins/"+id+"/prepare_first_factor", url.Values{"strategy": {"email_code"}, "email_address_id": {address}}); e != nil {
			http.Error(w, "sign-in code unavailable", 403)
			return
		}
		a.mu.Lock()
		f.SignIn = id
		a.mu.Unlock()
		HTML(w, "Check your email", `<form method=post action=/verify><input type=hidden name=csrf value="{{.}}"><label>Sign-in code <input name=code autocomplete=one-time-code required></label><button>Verify Eilmer account</button></form>`, f.CSRF)
	case "/verify":
		if !validID(f.SignIn, "sia_") {
			http.Error(w, "sign-in flow required", 403)
			return
		}
		j, e := frontend(r.Context(), f.Client, "POST", "/v1/client/sign_ins/"+f.SignIn+"/attempt_first_factor", url.Values{"strategy": {"email_code"}, "code": {r.PostForm.Get("code")}})
		response := object(j, "response")
		sid, _ := response["created_session_id"].(string)
		if e != nil || response["status"] != "complete" || !validID(sid, "sess_") {
			http.Error(w, "sign-in code denied", 403)
			return
		}
		s := &accountSession{Client: f.Client, SessionID: sid, Expires: time.Now().Add(24 * time.Hour)}
		if e := a.refresh(r.Context(), s); e != nil {
			// Errors contain fixed categories, never provider tokens, OTPs or bodies.
			log.Printf("account refresh denied: %s", e)
			http.Error(w, "account not enrolled for this private service", 403)
			return
		}
		token := Random()
		a.mu.Lock()
		a.prune()
		if len(a.sessions) >= 512 {
			a.mu.Unlock()
			http.Error(w, "account capacity unavailable", 503)
			return
		}
		a.sessions[Digest(token)] = s
		delete(a.flows, Digest(raw))
		a.mu.Unlock()
		SetCookie(w, "__Host-eilmer_flow", "", -1)
		SetCookie(w, "__Host-eilmer_account", token, 86400)
		if f.Destination != nil {
			a.authorize(w, s.Identity, f.Destination)
		} else {
			http.Redirect(w, r, "/", 303)
		}
	default:
		http.NotFound(w, r)
	}
}
func (a *Account) refresh(ctx context.Context, s *accountSession) error {
	j, e := frontend(ctx, s.Client, "POST", "/v1/client/sessions/"+s.SessionID+"/tokens", url.Values{})
	if e != nil {
		return e
	}
	jwt, _ := object(j, "response")["jwt"].(string)
	if jwt == "" {
		jwt, _ = j["jwt"].(string)
	}
	if jwt == "" {
		return errors.New("Clerk session-token response missing JWT")
	}
	id, _, e := a.verifier.Verify(ctx, jwt)
	if e != nil || (s.Identity.Subject != "" && s.Identity != id) {
		return errors.New("Clerk identity verification failed")
	}
	if !a.allowed(id) {
		return errors.New("Clerk identity not enrolled")
	}
	if s.Identity.Subject == "" {
		s.Identity = id
	}
	return nil
}
func (a *Account) authorize(w http.ResponseWriter, id Identity, d *destination) {
	raw := Random()
	a.mu.Lock()
	a.prune()
	if len(a.codes) >= 128 {
		a.mu.Unlock()
		http.Error(w, "account grant capacity unavailable", 503)
		return
	}
	a.codes[Digest(raw)] = code{Grant{Identity: id, Expires: time.Now().Add(15 * time.Minute)}, d.Audience, d.Challenge, time.Now().Add(time.Minute)}
	a.mu.Unlock()
	Post(w, d.Audience+"/account/callback", map[string]string{"code": raw, "state": d.State})
}
func (a *Account) redeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
		http.Error(w, "server-side account exchange required", 403)
		return
	}
	var in struct {
		Code     string `json:"code"`
		Verifier string `json:"verifier"`
		Audience string `json:"audience"`
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e != nil || d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || len(in.Code) != 64 || len(in.Verifier) != 64 {
		http.Error(w, "invalid code exchange", 403)
		return
	}
	a.mu.Lock()
	a.prune()
	c, ok := a.codes[Digest(in.Code)]
	if !ok || c.Audience != in.Audience || Challenge(in.Verifier) != c.Challenge {
		a.mu.Unlock()
		http.Error(w, "code expired, consumed or proof denied", 403)
		return
	}
	delete(a.codes, Digest(in.Code))
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c.Grant)
}
func object(j map[string]any, key string) map[string]any { m, _ := j[key].(map[string]any); return m }
func validID(s, prefix string) bool {
	if !strings.HasPrefix(s, prefix) || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}
func frontend(ctx context.Context, client *http.Client, method, path string, values url.Values) (map[string]any, error) {
	var result map[string]any
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://clerk.eilmer.si"+path, body)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Origin", "https://accounts.eilmer.si")
	req.Header.Set("User-Agent", "Lamp-Account/1.0 (+https://account.eilmer.si)")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := client.Do(req)
	if e != nil {
		return nil, errors.New("Clerk unavailable")
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 || resp.StatusCode != 200 || json.Unmarshal(raw, &result) != nil {
		return nil, errors.New("Clerk effect denied")
	}
	return result, nil
}
