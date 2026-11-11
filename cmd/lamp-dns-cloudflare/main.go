// SPDX-License-Identifier: Apache-2.0
// lamp-dns-cloudflare is a separate DNS custodian, not relay code. Run it under
// an OS identity the relay and certificate issuer cannot read or modify. An
// issuer can invoke it through an SSH forced command with a dedicated key.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JohanSardar/lamp/internal/hostedissuer"
)

type config struct {
	ZoneID    string   `json:"zone_id"`
	TokenFile string   `json:"token_file"`
	Records   []string `json:"records"`
}
type effect struct {
	Action string `json:"action"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}
type record struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}
type response struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Info    struct {
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

var identifier = regexp.MustCompile(`^[a-f0-9]{32}$`)
var challengeName = regexp.MustCompile(`^_acme-challenge\.([a-z][a-z0-9-]{0,47}\.fleet\.|relay\.|account\.)eilmer\.si$`)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "lamp-dns-cloudflare:", e)
		os.Exit(1)
	}
}
func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("one JSON value required")
	}
	return nil
}
func run() error {
	path := flag.String("config", "", "private DNS-custodian configuration")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	raw, e := hostedissuer.ReadPrivate(*path)
	if e != nil {
		return e
	}
	var c config
	if decode(raw, &c) != nil || !identifier.MatchString(c.ZoneID) || !filepath.IsAbs(c.TokenFile) || len(c.Records) == 0 {
		return errors.New("invalid DNS custodian configuration")
	}
	allowed := map[string]bool{}
	for _, name := range c.Records {
		if !challengeName.MatchString(name) || allowed[name] {
			return errors.New("explicit unique allocated eilmer.si challenge names required")
		}
		allowed[name] = true
	}
	raw, e = io.ReadAll(io.LimitReader(os.Stdin, 8193))
	if e != nil || len(raw) > 8192 {
		return errors.New("bounded DNS effect required")
	}
	var in effect
	if decode(raw, &in) != nil || (in.Action != "present" && in.Action != "cleanup") || !allowed[in.Name] {
		return errors.New("DNS effect outside allocated challenge records")
	}
	value, e := base64.RawURLEncoding.DecodeString(in.Value)
	if e != nil || len(value) != 32 {
		return errors.New("DNS-01 SHA-256 challenge value required")
	}
	// Check effect authority before opening the API credential or making a request.
	raw, e = hostedissuer.ReadPrivate(c.TokenFile)
	if e != nil {
		return e
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, " \r\n\t") {
		return errors.New("invalid DNS API credential")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	api := func(method, path string, body any) (response, error) {
		var result response
		var payload io.Reader
		if body != nil {
			b, e := json.Marshal(body)
			if e != nil {
				return result, e
			}
			payload = bytes.NewReader(b)
		}
		req, e := http.NewRequestWithContext(ctx, method, "https://api.cloudflare.com/client/v4/zones/"+c.ZoneID+"/dns_records"+path, payload)
		if e != nil {
			return result, errors.New("DNS request could not be constructed")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		if e != nil {
			return result, errors.New("DNS API unavailable")
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		if e != nil || len(b) > 1<<20 || json.Unmarshal(b, &result) != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.Success {
			return result, errors.New("DNS API effect denied or unavailable")
		}
		return result, nil
	}
	result, e := api("GET", "?type=TXT&name="+url.QueryEscape(in.Name)+"&per_page=100", nil)
	if e != nil {
		return e
	}
	var records []record
	if json.Unmarshal(result.Result, &records) != nil || len(records) > 100 || result.Info.TotalPages > 1 {
		return errors.New("DNS result requires operator inspection")
	}
	matched := []record{}
	for _, r := range records {
		if r.Type != "TXT" || r.Name != in.Name || !identifier.MatchString(r.ID) {
			return errors.New("DNS response outside requested record authority")
		}
		if r.Content == in.Value {
			matched = append(matched, r)
		}
	}
	changed := false
	if in.Action == "present" && len(matched) == 0 {
		result, e = api("POST", "", map[string]any{"type": "TXT", "name": in.Name, "content": in.Value, "ttl": 60, "comment": "Lamp constrained DNS-01; exact value cleanup only"})
		if e != nil {
			return e
		}
		var created record
		if json.Unmarshal(result.Result, &created) != nil || created.Type != "TXT" || created.Name != in.Name || created.Content != in.Value || !identifier.MatchString(created.ID) {
			return errors.New("DNS API did not confirm the authorized effect")
		}
		changed = true
	}
	if in.Action == "cleanup" {
		for _, r := range matched {
			if _, e = api("DELETE", "/"+r.ID, nil); e != nil {
				return e
			}
			changed = true
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]bool{"changed": changed})
}
