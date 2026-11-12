// SPDX-License-Identifier: Apache-2.0
package access

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lamp.eilmer.si/internal/accountidentity"
)

func validateOIDC(c OIDC) error {
	issuer, e := url.Parse(c.Issuer)
	if e != nil || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return errors.New("exact OIDC issuer required")
	}
	jwks, e := url.Parse(c.JWKSURL)
	if e != nil || jwks.Host != issuer.Host || jwks.Scheme != issuer.Scheme || jwks.User != nil || jwks.RawQuery != "" || jwks.Fragment != "" {
		return errors.New("JWKS must share the configured issuer origin")
	}
	ip := net.ParseIP(issuer.Hostname())
	if issuer.Scheme != "https" && !(issuer.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return errors.New("OIDC requires HTTPS except numeric loopback development")
	}
	if c.Audience == "" || len(c.Parties) == 0 {
		return errors.New("OIDC audience and exact authorized parties required")
	}
	for _, p := range c.Parties {
		u, e := url.Parse(p)
		if e != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback())) {
			return errors.New("exact HTTPS authorized party origins required")
		}
	}
	return nil
}

// oidcCredential exchanges only a verified issuer/subject for the custodian's
// existing fleet credential. The OIDC bearer never reaches the control daemon.
// An unrecognized subject (including a colliding email) cannot select an owner.
func (s *Service) oidcCredential(ctx context.Context, bearer string) (string, time.Time, error) {
	id, expiry, e := s.oidc.Verify(ctx, bearer)
	if e != nil {
		return "", time.Time{}, e
	}
	for _, u := range s.config.Users {
		if u.Identity != nil && *u.Identity == id {
			t, e := token(u.TokenFile)
			return t, expiry, e
		}
	}
	return "", time.Time{}, errors.New("OIDC subject not enrolled")
}
func (s *Service) initOIDC() {
	if c := s.config.OIDC; c != nil {
		s.oidc = &accountidentity.Verifier{Issuer: c.Issuer, Audience: c.Audience, JWKS: c.JWKSURL, Parties: c.Parties}
	}
}
func (s *Service) withOIDC(w http.ResponseWriter, r *http.Request) (*http.Request, context.CancelFunc, bool) {
	credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if s.oidc == nil || strings.Count(credential, ".") != 2 {
		return r, func() {}, true
	}
	credential, expiry, e := s.oidcCredential(r.Context(), credential)
	if e != nil {
		http.Error(w, "OIDC credential denied", 401)
		return r, func() {}, false
	}
	ctx, cancel := context.WithDeadline(r.Context(), expiry)
	r = r.Clone(ctx)
	r.Header.Set("Authorization", "Bearer "+credential)
	return r, cancel, true
}
