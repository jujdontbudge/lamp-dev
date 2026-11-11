package control

import (
	"embed"
	"net/http"
	"strings"
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
