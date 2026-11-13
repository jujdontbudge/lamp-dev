package relayclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
)

// Config enrolls an outbound ciphertext carrier. Reconnect credentials remain
// in a private host file and are never exposed through the public API.
type Config struct {
	Origin         string `json:"origin"`
	CredentialFile string `json:"credential_file"`
	SSHTarget      string `json:"ssh_target"`
	HTTPSTarget    string `json:"https_target,omitempty"`
}

func LoadConfig(path string) (*Config, error) {
	raw, err := privateRead(path)
	if err != nil {
		return nil, err
	}
	var c Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&c); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("expected one relay configuration")
	}
	if _, err = endpoint(c.Origin, "", false); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(c.CredentialFile) || c.SSHTarget == "" {
		return nil, errors.New("absolute credential file and loopback SSH target required")
	}
	for _, a := range []string{c.SSHTarget, c.HTTPSTarget} {
		if a == "" {
			continue
		}
		h, _, e := net.SplitHostPort(a)
		ip := net.ParseIP(h)
		if e != nil || ip == nil || !ip.IsLoopback() {
			return nil, errors.New("relay targets must be numeric loopback")
		}
	}
	raw, err = privateRead(c.CredentialFile)
	if err != nil {
		return nil, err
	}
	var credential Credential
	if json.Unmarshal(raw, &credential) != nil || !validFleet(credential.Fleet) || len(credential.Secret) != 64 {
		return nil, errors.New("invalid host credential")
	}
	return &c, nil
}
