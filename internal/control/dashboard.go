package control

import (
	"context"
	"crypto/subtle"
	"embed"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The host-served dashboard (OPS-001) is static HTML/CSS/JavaScript that
// talks to the same public API with the viewer's own credential. The page
// itself carries no data: everything shown is what that caller may read.
//
//go:embed dashboard/index.html dashboard/app.js dashboard/style.css
var dashboardFiles embed.FS

var dashboardTypes = map[string]string{".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8"}

// serveDashboard reports whether the request was a dashboard asset.
func serveDashboard(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != "GET" && r.Method != "HEAD" {
		return false
	}
	name := ""
	switch r.URL.Path {
	case "/", "/dashboard", "/dashboard/":
		name = "index.html"
	case "/dashboard/app.js":
		name = "app.js"
	case "/dashboard/style.css":
		name = "style.css"
	default:
		return false
	}
	data, err := dashboardFiles.ReadFile("dashboard/" + name)
	if err != nil {
		return false
	}
	h := w.Header()
	h.Set("Content-Type", dashboardTypes[name[strings.LastIndex(name, "."):]])
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(200)
	if r.Method == "GET" {
		w.Write(data)
	}
	return true
}

// RequireBearer wraps a handler for a network dashboard listener: unlike the
// owner's peer-checked Unix socket, a TCP caller is never implicitly the
// owner, so every API request must carry its own bearer credential.
func RequireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveDashboard(w, r) {
			return
		}
		if v := r.Header.Values("Authorization"); len(v) != 1 || !strings.HasPrefix(v[0], "Bearer ") || len(v[0]) < 8 {
			writeError(w, newID("req"), "UNAUTHENTICATED", "sign in: a bearer credential is required on the dashboard listener", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// The sign-in file is owner-only. Its one-use code is exchanged by POST,
// never put in a URL, HTTP log, or installer output. Opening the file again
// uses the replacement code. Browser sessions expire after eight hours and
// daemon restart; bearer authentication remains available for remote clients.
type dashboardOwnerKey struct{}
type localDashboard struct {
	mu                        sync.Mutex
	origin, file, code, owner string
	sessions                  map[string]time.Time
}

func (s *Service) LocalDashboard(next http.Handler, address, directory string) (http.Handler, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, fmt.Errorf("automatic dashboard sign-in requires a loopback address")
	}
	d := &localDashboard{origin: "http://" + address, file: filepath.Join(directory, "dashboard.html"), owner: s.journal.Control.OwnerID, sessions: map[string]time.Time{}}
	if err := os.WriteFile(filepath.Join(directory, "dashboard-url"), []byte(d.origin+"/dashboard\n"), 0600); err != nil {
		return nil, err
	}
	if err := d.rotate(); err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != address {
			http.Error(w, "unexpected dashboard host", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/dashboard/sign-in" && r.Method == "POST" {
			r.Body = http.MaxBytesReader(w, r.Body, 1024)
			if r.ParseForm() != nil {
				http.Error(w, "invalid sign-in", 400)
				return
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("code")), []byte(d.code)) != 1 {
				http.Error(w, "sign-in expired; reopen the private dashboard file", 401)
				return
			}
			if err := d.rotate(); err != nil {
				http.Error(w, "cannot renew sign-in file", 503)
				return
			}
			session := newID("web") + newID("key")
			now := time.Now()
			for key, expiry := range d.sessions {
				if !now.Before(expiry) {
					delete(d.sessions, key)
				}
			}
			if len(d.sessions) >= 32 { // Bound disposable browser sessions, retiring the oldest.
				oldest := ""
				at := now.Add(24 * time.Hour)
				for key, expiry := range d.sessions {
					if expiry.Before(at) {
						oldest, at = key, expiry
					}
				}
				delete(d.sessions, oldest)
			}
			d.sessions[bearerHash(session)] = now.Add(8 * time.Hour)
			http.SetCookie(w, &http.Cookie{Name: "lamp_dashboard", Value: session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 3600})
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		if r.URL.Path == "/dashboard/sign-out" && r.Method == "POST" {
			if r.Header.Get("Origin") != d.origin {
				http.Error(w, "dashboard origin required", 403)
				return
			}
			cookie, _ := r.Cookie("lamp_dashboard")
			d.mu.Lock()
			delete(d.sessions, bearerHash(cookieValue(cookie)))
			d.mu.Unlock()
			http.SetCookie(w, &http.Cookie{Name: "lamp_dashboard", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if serveDashboard(w, r) {
			return
		}
		if r.Header.Get("Authorization") == "" {
			cookie, err := r.Cookie("lamp_dashboard")
			d.mu.Lock()
			valid := err == nil && time.Now().Before(d.sessions[bearerHash(cookieValue(cookie))])
			d.mu.Unlock()
			if valid {
				if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != d.origin {
					http.Error(w, "dashboard origin required", 403)
					return
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), dashboardOwnerKey{}, d.owner)))
				return
			}
		}
		RequireBearer(next).ServeHTTP(w, r)
	}), nil
}
func cookieValue(cookie *http.Cookie) string {
	if cookie == nil {
		return ""
	}
	return cookie.Value
}
func (d *localDashboard) rotate() error {
	code := newID("signin") + newID("key")
	body := `<!doctype html><meta charset="utf-8"><meta name="referrer" content="no-referrer"><title>Lamp dashboard sign-in</title><form method="post" action="` + html.EscapeString(d.origin) + `/dashboard/sign-in"><input type="hidden" name="code" value="` + code + `"><button>Open Lamp dashboard</button></form><script>document.forms[0].submit()</script>`
	tmp := d.file + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, d.file); err != nil {
		return err
	}
	d.code = code
	return nil
}
