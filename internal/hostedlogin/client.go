// SPDX-License-Identifier: Apache-2.0
// Package hostedlogin exchanges one-use account codes into origin-local sessions.
// It never accepts email as identity or shares Clerk cookies between products.
package hostedlogin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/JohanSardar/lamp/internal/accountidentity"
)

type Identity = accountidentity.Identity
type Grant struct {
	Identity Identity  `json:"identity"`
	Expires  time.Time `json:"expires"`
}
type flow struct {
	State, Verifier, CSRF string
	Expires               time.Time
	Begun                 bool
}
type session struct {
	Grant
	CSRF string
}
type Client struct {
	Origin, AccountOrigin string
	SessionCookieName     string
	mu                    sync.Mutex
	flows                 map[string]flow
	sessions              map[string]session
}

func Random() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic("system randomness unavailable")
	}
	return hex.EncodeToString(b[:])
}
func Digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func Challenge(s string) string {
	b := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func ValidOrigin(origin, hostname string) bool {
	u, e := url.Parse(origin)
	return e == nil && u.Scheme == "https" && u.Hostname() == hostname && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}
func New(origin, account string) (*Client, error) {
	u, e := url.Parse(origin)
	if e != nil || !ValidOrigin(origin, u.Hostname()) || (u.Hostname() != "relay.eilmer.si" && !strings.HasSuffix(u.Hostname(), ".fleet.eilmer.si")) || !ValidOrigin(account, "account.eilmer.si") {
		return nil, errors.New("exact eilmer.si product/account origins required")
	}
	return &Client{Origin: origin, AccountOrigin: account, flows: map[string]flow{}, sessions: map[string]session{}}, nil
}
func Cookie(r *http.Request, name string) (string, bool) {
	v := ""
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			v = c.Value
			count++
		}
	}
	return v, count == 1 && len(v) == 64
}
func SetCookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: age})
}
func Security(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	// Form POSTs must retain their Origin for exact-origin CSRF checks. No
	// credentials are carried in URLs; strict-origin sends no path or query.
	w.Header().Set("Referrer-Policy", "strict-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000")
}
func HTML(w http.ResponseWriter, title, body string, data any) {
	Security(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	t := template.Must(template.New("page").Parse("<!doctype html><meta charset=utf-8><title>" + template.HTMLEscapeString(title) + "</title><h1>" + template.HTMLEscapeString(title) + "</h1>" + body))
	_ = t.Execute(w, data)
}
func Post(w http.ResponseWriter, target string, fields map[string]string) {
	Security(w)
	nonce := Random()
	u, e := url.Parse(target)
	if e != nil {
		http.Error(w, "invalid account handoff", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action "+u.Scheme+"://"+u.Host+"; script-src 'nonce-"+nonce+"'")
	t := template.Must(template.New("handoff").Parse(`<!doctype html><meta charset=utf-8><title>Eilmer sign-in</title><form method=post action="{{.Target}}">{{range $k,$v:=.Fields}}<input type=hidden name="{{$k}}" value="{{$v}}">{{end}}<button>Continue</button></form><script nonce="{{.Nonce}}">document.forms[0].submit()</script>`))
	_ = t.Execute(w, struct {
		Target string
		Fields map[string]string
		Nonce  string
	}{target, fields, nonce})
}
func Form(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if r.ParseForm() != nil || r.URL.RawQuery != "" {
		http.Error(w, "invalid form", 400)
		return false
	}
	for _, v := range r.PostForm {
		if len(v) != 1 {
			http.Error(w, "ambiguous form", 400)
			return false
		}
	}
	return true
}
func (c *Client) cookieName() string {
	if c.SessionCookieName == "__Host-lamp_port" {
		return c.SessionCookieName
	}
	return "__Host-lamp_session"
}
func (c *Client) prune() {
	now := time.Now()
	for k, v := range c.flows {
		if now.After(v.Expires) {
			delete(c.flows, k)
		}
	}
	for k, v := range c.sessions {
		if now.After(v.Expires) {
			delete(c.sessions, k)
		}
	}
}

// Authenticate accepts a local opaque bearer, or exactly one host-prefixed
// cookie. A sibling's parent-domain cookies are never account credentials.
func (c *Client) Authenticate(r *http.Request) (Grant, bool) {
	values := r.Header.Values("Authorization")
	raw := ""
	if len(values) == 1 && strings.HasPrefix(values[0], "Bearer ") {
		raw = strings.TrimPrefix(values[0], "Bearer ")
	} else if len(values) > 0 {
		return Grant{}, false
	} else {
		var ok bool
		raw, ok = Cookie(r, c.cookieName())
		if !ok {
			return Grant{}, false
		}
		if r.Header.Get("Origin") != "" && r.Header.Get("Origin") != c.Origin {
			return Grant{}, false
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	s, ok := c.sessions[Digest(raw)]
	return s.Grant, ok
}
func (c *Client) Required(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	if len(c.flows) >= 64 {
		http.Error(w, "sign-in capacity unavailable", 503)
		return
	}
	raw := Random()
	f := flow{State: Random(), Verifier: Random(), CSRF: Random(), Expires: time.Now().Add(5 * time.Minute)}
	c.flows[Digest(raw)] = f
	SetCookie(w, "__Host-lamp_login", raw, 300)
	HTML(w, "Eilmer account required", `<p>This private fleet requires the shared Eilmer account.</p><form method=post action=/account/login><input type=hidden name=csrf value="{{.}}"><button>Sign in with Eilmer</button></form>`, f.CSRF)
}
func (c *Client) Handle(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/account/login" && r.URL.Path != "/account/callback" && r.URL.Path != "/account/token" && r.URL.Path != "/account/logout" {
		return false
	}
	Security(w)
	if r.Method != "POST" || !Form(w, r) {
		http.Error(w, "POST form required", 405)
		return true
	}
	if r.URL.Path == "/account/token" || r.URL.Path == "/account/logout" {
		raw, ok := Cookie(r, c.cookieName())
		c.mu.Lock()
		c.prune()
		s, found := c.sessions[Digest(raw)]
		if !ok || !found || r.Header.Get("Origin") != c.Origin || r.PostForm.Get("csrf") != s.CSRF {
			c.mu.Unlock()
			http.Error(w, "session proof required", 403)
			return true
		}
		if r.URL.Path == "/account/logout" {
			delete(c.sessions, Digest(raw))
			c.mu.Unlock()
			SetCookie(w, c.cookieName(), "", -1)
			http.Redirect(w, r, "/", 303)
			return true
		}
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="relay-account-token"`)
		_, _ = io.WriteString(w, raw)
		return true
	}
	raw, ok := Cookie(r, "__Host-lamp_login")
	if !ok {
		http.Error(w, "login proof required", 403)
		return true
	}
	c.mu.Lock()
	c.prune()
	f, found := c.flows[Digest(raw)]
	if !found {
		c.mu.Unlock()
		http.Error(w, "login expired", 403)
		return true
	}
	if r.URL.Path == "/account/login" {
		if r.Header.Get("Origin") != c.Origin || r.PostForm.Get("csrf") != f.CSRF || f.Begun {
			c.mu.Unlock()
			http.Error(w, "login origin/state denied", 403)
			return true
		}
		f.Begun = true
		c.flows[Digest(raw)] = f
		c.mu.Unlock()
		Post(w, c.AccountOrigin+"/authorize", map[string]string{"audience": c.Origin, "state": f.State, "challenge": Challenge(f.Verifier)})
		return true
	}
	if r.Header.Get("Origin") != c.AccountOrigin || !f.Begun || r.PostForm.Get("state") != f.State {
		c.mu.Unlock()
		http.Error(w, "account callback denied", 403)
		return true
	}
	delete(c.flows, Digest(raw))
	c.mu.Unlock()
	grant, e := redeem(r.Context(), c.AccountOrigin, r.PostForm.Get("code"), f.Verifier, c.Origin)
	if e != nil {
		http.Error(w, "account exchange denied", 403)
		return true
	}
	token := Random()
	c.mu.Lock()
	c.prune()
	if len(c.sessions) >= 512 {
		c.mu.Unlock()
		http.Error(w, "session capacity unavailable", 503)
		return true
	}
	c.sessions[Digest(token)] = session{grant, Random()}
	c.mu.Unlock()
	SetCookie(w, "__Host-lamp_login", "", -1)
	SetCookie(w, c.cookieName(), token, int(time.Until(grant.Expires).Seconds()))
	http.Redirect(w, r, "/", 303)
	return true
}
func (c *Client) Status(w http.ResponseWriter, r *http.Request) {
	grant, ok := c.Authenticate(r)
	if !ok {
		c.Required(w, r)
		return
	}
	raw, _ := Cookie(r, c.cookieName())
	c.mu.Lock()
	s := c.sessions[Digest(raw)]
	c.mu.Unlock()
	HTML(w, "Eilmer private relay", `<p>Signed in with issuer {{.Grant.Identity.Issuer}}, subject {{.Grant.Identity.Subject}}.</p><form method=post action=/account/token><input type=hidden name=csrf value="{{.CSRF}}"><button>Download private CLI account token</button></form><form method=post action=/account/logout><input type=hidden name=csrf value="{{.CSRF}}"><button>Sign out of this product</button></form>`, struct {
		Grant Grant
		CSRF  string
	}{grant, s.CSRF})
}
func redeem(ctx context.Context, account, code, verifier, audience string) (Grant, error) {
	var grant Grant
	b, _ := json.Marshal(map[string]string{"code": code, "verifier": verifier, "audience": audience})
	req, e := http.NewRequestWithContext(ctx, "POST", account+"/redeem", bytes.NewReader(b))
	if e != nil {
		return grant, e
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		return grant, errors.New("account exchange unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&grant) != nil || grant.Identity.Issuer != "https://clerk.eilmer.si" || grant.Identity.Subject == "" || !grant.Expires.After(time.Now()) || grant.Expires.After(time.Now().Add(time.Hour)) {
		return Grant{}, errors.New("invalid account exchange")
	}
	return grant, nil
}
