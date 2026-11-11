package ci

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// VerifyIdentity fetches keys from the fixed issuer, never a JWT-controlled URL.
// No JWT or response body is included in errors. There is no algorithm fallback.
func (g *GitHub) VerifyIdentity(ctx context.Context, jwt, audience string) (map[string]any, error) {
	denied := errors.New("workload identity rejected")
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 || len(jwt) > 32768 {
		return nil, denied
	}
	decode := func(s string, out any) error {
		b, e := base64.RawURLEncoding.DecodeString(s)
		if e != nil {
			return e
		}
		return json.Unmarshal(b, out)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if decode(parts[0], &header) != nil || header.Alg != "RS256" || header.Kid == "" {
		return nil, denied
	}
	var claims map[string]any
	if decode(parts[1], &claims) != nil || claims["iss"] != GitHubIssuer || claims["aud"] != audience {
		return nil, denied
	}
	now := float64(time.Now().Unix())
	exp, eok := claims["exp"].(float64)
	nbf, nok := claims["nbf"].(float64)
	iat, iok := claims["iat"].(float64)
	if !eok || !nok || !iok || exp <= now || nbf > now || iat > now || now-iat > 600 || exp-iat > 600 {
		return nil, denied
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", GitHubIssuer+"/.well-known/jwks", nil)
	res, err := g.HTTP.Do(req)
	if err != nil {
		return nil, denied
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, denied
	}
	var keys struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&keys) != nil {
		return nil, denied
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, denied
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	verified := false
	for _, k := range keys.Keys {
		if k.Kid != header.Kid || k.Kty != "RSA" || k.Alg != "RS256" {
			continue
		}
		n, ne := base64.RawURLEncoding.DecodeString(k.N)
		e, ee := base64.RawURLEncoding.DecodeString(k.E)
		if ne != nil || ee != nil || len(e) > 4 || len(n) < 256 {
			continue
		}
		exponent := 0
		for _, v := range e {
			exponent = exponent*256 + int(v)
		}
		if exponent < 3 {
			continue
		}
		if rsa.VerifyPKCS1v15(&rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}, crypto.SHA256, digest[:], sig) == nil {
			verified = true
			break
		}
	}
	if !verified {
		return nil, denied
	}
	// Visibility is issuer evidence, not bootstrap input. A PR or an unknown
	// visibility can never receive a Lamp key, even with a trusted base ref.
	if claims["repository_visibility"] != "private" || claims["event_name"] == "pull_request" || claims["event_name"] == "pull_request_target" {
		return nil, denied
	}
	return claims, nil
}
func (g *GitHub) IdentityPolicy(c map[string]any) *OIDCPolicy {
	for _, p := range g.Config.OIDC {
		if c["repository_id"] == p.RepositoryID && c["repository_owner_id"] == p.OwnerID && c["workflow_ref"] == p.WorkflowRef && c["workflow_sha"] == p.WorkflowSHA && c["ref"] == p.Ref && c["event_name"] == p.Event {
			copy := p
			return &copy
		}
	}
	return nil
}
