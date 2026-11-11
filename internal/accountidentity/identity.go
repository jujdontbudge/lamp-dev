// SPDX-License-Identifier: Apache-2.0
// Package accountidentity verifies immutable OIDC token identities. It neither
// enrolls principals nor matches email addresses.
package accountidentity

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
	"sync"
	"time"
)

// Identity is an immutable provider identity. No email address participates in authorization.
type Identity struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}
type Claims struct {
	Issuer    string          `json:"iss"`
	Subject   string          `json:"sub"`
	Expires   int64           `json:"exp"`
	NotBefore int64           `json:"nbf"`
	Issued    int64           `json:"iat"`
	Party     string          `json:"azp"`
	Audience  json.RawMessage `json:"aud"`
}
type Verifier struct {
	Issuer, Audience string
	Parties          []string
	JWKS             string
	Client           *http.Client
	mu               sync.Mutex
	keys             map[string]*rsa.PublicKey
	until            time.Time
}

func (v *Verifier) Verify(ctx context.Context, token string) (Identity, time.Time, error) {
	denied := errors.New("invalid identity credential")
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 16384 {
		return Identity{}, time.Time{}, denied
	}
	var header struct {
		Algorithm string   `json:"alg"`
		Key       string   `json:"kid"`
		Critical  []string `json:"crit"`
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil || json.Unmarshal(raw, &header) != nil || header.Algorithm != "RS256" || header.Key == "" || len(header.Critical) != 0 {
		return Identity{}, time.Time{}, denied
	}
	key, e := v.key(ctx, header.Key)
	if e != nil {
		return Identity{}, time.Time{}, denied
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		return Identity{}, time.Time{}, denied
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], signature) != nil {
		return Identity{}, time.Time{}, denied
	}
	var c Claims
	raw, e = base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || json.Unmarshal(raw, &c) != nil {
		return Identity{}, time.Time{}, denied
	}
	now := time.Now().Unix()
	if c.Issuer != v.Issuer || c.Subject == "" || c.Expires <= now || c.NotBefore > now || c.Issued > now+30 || c.Issued <= 0 || c.Expires > now+3600 || c.Expires-c.Issued > 3600 {
		return Identity{}, time.Time{}, denied
	}
	party := false
	for _, p := range v.Parties {
		if p == c.Party {
			party = true
		}
	}
	if !party {
		return Identity{}, time.Time{}, denied
	}
	if v.Audience != "" {
		var one string
		var many []string
		if json.Unmarshal(c.Audience, &one) == nil {
			many = []string{one}
		} else if json.Unmarshal(c.Audience, &many) != nil {
			return Identity{}, time.Time{}, denied
		}
		found := false
		for _, a := range many {
			if a == v.Audience {
				found = true
			}
		}
		if !found {
			return Identity{}, time.Time{}, denied
		}
	}
	return Identity{c.Issuer, c.Subject}, time.Unix(c.Expires, 0), nil
}
func (v *Verifier) key(ctx context.Context, id string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Now().Before(v.until) {
		if k := v.keys[id]; k != nil {
			return k, nil
		}
		return nil, errors.New("unknown signing key")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", v.JWKS, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "Lamp-Identity/1.0 (+https://eilmer.si)")
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("signing keys unavailable")
	}
	var set struct {
		Keys []struct {
			ID        string `json:"kid"`
			Type      string `json:"kty"`
			Algorithm string `json:"alg"`
			Use       string `json:"use"`
			N         string `json:"n"`
			E         string `json:"e"`
		} `json:"keys"`
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &set) != nil || len(set.Keys) > 100 {
		return nil, errors.New("invalid signing keys")
	}
	keys := map[string]*rsa.PublicKey{}
	for _, j := range set.Keys {
		if j.Type != "RSA" || (j.Algorithm != "" && j.Algorithm != "RS256") || (j.Use != "" && j.Use != "sig") {
			continue
		}
		n, e1 := base64.RawURLEncoding.DecodeString(j.N)
		exponent, e2 := base64.RawURLEncoding.DecodeString(j.E)
		if e1 != nil || e2 != nil || len(n) < 256 || len(n) > 512 || len(exponent) > 4 {
			continue
		}
		exp := new(big.Int).SetBytes(exponent).Int64()
		if exp < 3 || exp > 2147483647 || exp%2 == 0 {
			continue
		}
		if keys[j.ID] != nil {
			return nil, errors.New("duplicate signing key")
		}
		keys[j.ID] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exp)}
	}
	v.keys = keys
	v.until = time.Now().Add(5 * time.Minute)
	if keys[id] == nil {
		return nil, errors.New("unknown signing key")
	}
	return keys[id], nil
}
