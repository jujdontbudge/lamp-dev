// SPDX-License-Identifier: Apache-2.0
// lamp-ingress terminates fleet TLS on approved ingress hosts, never on the relay.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lamp.eilmer.si/internal/hostedissuer"
	"lamp.eilmer.si/internal/hostedlogin"
)

type user struct {
	Identity  hostedlogin.Identity `json:"identity"`
	TokenFile string               `json:"token_file,omitempty"`
}
type host struct {
	Origin   string `json:"origin"`
	Socket   string `json:"api_socket,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Users    []user `json:"users"`
}
type config struct {
	Listen        string `json:"listen"`
	Certificate   string `json:"certificate"`
	Key           string `json:"key"`
	AccountOrigin string `json:"account_origin"`
	Hosts         []host `json:"hosts"`
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "lamp-ingress:", e)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "", "private fleet ingress config")
	flag.Parse()
	raw, e := hostedissuer.ReadPrivate(*path)
	if e != nil {
		return e
	}
	var c config
	if json.Unmarshal(raw, &c) != nil || !hostedlogin.ValidOrigin(c.AccountOrigin, "account.eilmer.si") || len(c.Hosts) == 0 {
		return errors.New("invalid fleet ingress config")
	}
	if _, e = hostedissuer.ReadPrivate(c.Key); e != nil {
		return e
	}
	handlers := map[string]http.Handler{}
	hostnames := map[string]bool{}
	for _, h := range c.Hosts {
		u, e := url.Parse(h.Origin)
		if e != nil || !hostedlogin.ValidOrigin(h.Origin, u.Hostname()) || !strings.HasSuffix(u.Hostname(), ".fleet.eilmer.si") || len(h.Users) == 0 || hostnames[strings.ToLower(u.Hostname())] {
			return errors.New("distinct private fleet hostnames and identities required: cookies ignore ports")
		}
		hostnames[strings.ToLower(u.Hostname())] = true
		login, e := hostedlogin.New(h.Origin, c.AccountOrigin)
		if e != nil {
			return e
		}
		if h.Upstream != "" {
			login.SessionCookieName = "__Host-lamp_port"
		}
		var target *url.URL
		transport := &http.Transport{}
		if h.Socket != "" {
			if !filepath.IsAbs(h.Socket) || h.Upstream != "" {
				return errors.New("choose API socket or app upstream")
			}
			target = &url.URL{Scheme: "http", Host: "lamp"}
			socket := h.Socket
			transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			}
		} else {
			target, e = url.Parse(h.Upstream)
			if e != nil || target.Scheme != "http" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || net.ParseIP(target.Hostname()) == nil || !net.ParseIP(target.Hostname()).IsLoopback() {
				return errors.New("numeric loopback app upstream required")
			}
		}
		credentials := map[hostedlogin.Identity]string{}
		for _, person := range h.Users {
			if person.Identity.Issuer != "https://clerk.eilmer.si" || person.Identity.Subject == "" {
				return errors.New("immutable production Eilmer identities required")
			}
			token := ""
			if h.Socket != "" {
				b, e := hostedissuer.ReadPrivate(person.TokenFile)
				if e != nil {
					return e
				}
				token = strings.TrimSpace(string(b))
				if token == "" || strings.ContainsAny(token, " \r\n\t") {
					return errors.New("principal token file required")
				}
			}
			credentials[person.Identity] = token
		}
		api := h.Socket != ""
		proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
			grant, _ := login.Authenticate(p.In)
			p.SetURL(target)
			p.Out.Host = p.In.Host
			p.Out.Header.Del("Cookie")
			p.Out.Header.Del("Authorization")
			for key := range p.Out.Header {
				lower := strings.ToLower(key)
				if strings.HasPrefix(lower, "x-lamp-") || strings.HasPrefix(lower, "cf-") || lower == "forwarded" || strings.HasPrefix(lower, "x-forwarded-") {
					p.Out.Header.Del(key)
				}
			}
			if api {
				p.Out.Header.Set("Authorization", "Bearer "+credentials[grant.Identity])
			}
		}, ModifyResponse: func(r *http.Response) error {
			cookies := r.Cookies()
			r.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				if cookie.Domain == "" && !strings.HasPrefix(cookie.Name, "__Host-lamp_") && !strings.HasPrefix(cookie.Name, "__Host-eilmer_") {
					r.Header.Add("Set-Cookie", cookie.String())
				}
			}
			r.Header.Del("Access-Control-Allow-Origin")
			r.Header.Del("Access-Control-Allow-Credentials")
			return nil
		}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "private upstream unavailable", 503)
		}}
		origin := h.Origin
		hostname := u.Hostname()
		handlers[u.Host] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hostedlogin.Security(w)
			if r.TLS == nil || r.TLS.ServerName != hostname {
				http.Error(w, "wrong fleet origin", 421)
				return
			}
			if login.Handle(w, r) {
				return
			}
			if r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin {
				http.Error(w, "sibling origin denied", 403)
				return
			}
			grant, ok := login.Authenticate(r)
			if !ok {
				login.Required(w, r)
				return
			}
			if _, ok = credentials[grant.Identity]; !ok {
				http.Error(w, "fleet principal not enrolled", 403)
				return
			}
			if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != origin {
				http.Error(w, "mutation origin required", 403)
				return
			}
			if api && r.URL.Path == "/" {
				hostedlogin.HTML(w, "Private Lamp fleet", `<p>Account {{.Subject}} authenticated inside fleet TLS.</p><a href=/v1/fleets/fl_local>Read the authenticated fleet API</a>`, grant.Identity)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
			proxy.ServeHTTP(w, r)
		})
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := handlers[r.Host]
		if h == nil {
			http.Error(w, "unallocated fleet origin", 421)
			return
		}
		h.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: c.Listen, Handler: handler, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384, ErrorLog: log.New(io.Discard, "", 0)}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	e = server.ListenAndServeTLS(c.Certificate, c.Key)
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
