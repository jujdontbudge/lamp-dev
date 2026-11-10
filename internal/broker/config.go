// Package broker keeps owner credentials on the host. Its task interface accepts
// typed operations, never URLs, executable names, headers, or signing oracles.
package broker

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const limit = 2 << 20

type Config struct {
	UpstreamCAFiles []string                  `json:"upstream_ca_files,omitempty"`
	Connections     map[string]Connection     `json:"connections"`
	Settings        map[string]SettingsSource `json:"settings"`
}
type Connection struct {
	Kind     string `json:"kind"`   // model, mcp, cli, service, git_sign
	Access   string `json:"access"` // off, read_only, ask_first, full
	Endpoint string `json:"endpoint,omitempty"`
	// CredentialCommand is a host-only credential-store reader. Its stdout must
	// contain only the bearer. No shell is used and stderr is never forwarded.
	CredentialCommand []string             `json:"credential_command,omitempty"`
	Binary            string               `json:"binary,omitempty"`
	Adapter           string               `json:"adapter,omitempty"`  // gh, stripe, aws
	Resource          string               `json:"resource,omitempty"` // repository or bucket
	Region            string               `json:"region,omitempty"`
	KeyFile           string               `json:"key_file,omitempty"`
	Models            []string             `json:"models,omitempty"`
	Operations        map[string]Operation `json:"operations"`
}
type Operation struct {
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"`
	Write  bool   `json:"write"`
	// SemanticRead is a trusted adapter assertion, not a task-supplied HTTP verb.
	// Service read_only requires an explicitly reviewed semantic read operation.
	SemanticRead bool              `json:"semantic_read,omitempty"`
	Fields       map[string]string `json:"fields,omitempty"` // required string, number, boolean, object, array; suffix ? means optional
}
type SettingsSource struct {
	Root  string   `json:"root"`
	Paths []string `json:"paths"`
}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)
var repoPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)

func Load(path string) (Config, error) {
	var c Config
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return c, errors.New("config must be a private regular file (0600)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return c, errors.New("invalid broker config")
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	for id, v := range c.Connections {
		if !namePattern.MatchString(id) || len(v.Operations) == 0 {
			return errors.New("connection requires a name and explicit operations")
		}
		switch v.Access {
		case "off", "read_only", "ask_first", "full":
		default:
			return errors.New("invalid access mode")
		}
		if len(v.CredentialCommand) > 0 && !filepath.IsAbs(v.CredentialCommand[0]) {
			return errors.New("credential reader must be an absolute executable")
		}
		switch v.Kind {
		case "model", "mcp", "service":
			u, err := url.Parse(v.Endpoint)
			if err != nil || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Host == "" {
				return errors.New("invalid pinned endpoint")
			}
			if u.Scheme != "https" && !(v.Kind == "service" && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
				return errors.New("upstreams require HTTPS; local services may use loopback HTTP")
			}
			if v.Kind != "service" && len(v.CredentialCommand) == 0 {
				return errors.New("authenticated upstream requires host credential reader")
			}
			if v.Kind == "model" && len(v.Models) == 0 {
				return errors.New("model allowlist required")
			}
		case "cli":
			if !filepath.IsAbs(v.Binary) {
				return errors.New("CLI binary must be absolute")
			}
			if v.Adapter != "gh" && v.Adapter != "stripe" && v.Adapter != "aws" {
				return errors.New("unsupported CLI adapter")
			}
			if v.Adapter == "gh" && !repoPattern.MatchString(v.Resource) {
				return errors.New("GitHub repository must be pinned")
			}
			if v.Adapter == "aws" && (!namePattern.MatchString(v.Resource) || !namePattern.MatchString(v.Region)) {
				return errors.New("AWS bucket and region must be pinned")
			}
		case "git_sign":
			if !filepath.IsAbs(v.KeyFile) || !filepath.IsAbs(v.Binary) || !repoPattern.MatchString(v.Resource) || v.Access != "ask_first" {
				return errors.New("git signing requires host key, ssh-keygen, repository and ask_first")
			}
		default:
			return errors.New("unsupported connection kind")
		}
		for name, op := range v.Operations {
			if !namePattern.MatchString(name) {
				return errors.New("invalid operation name")
			}
			for field, typ := range op.Fields {
				if !namePattern.MatchString(field) {
					return errors.New("invalid field name")
				}
				switch strings.TrimSuffix(typ, "?") {
				case "string", "number", "boolean", "object", "array":
				default:
					return errors.New("invalid field type")
				}
			}
			if v.Kind == "service" {
				if op.Method != "GET" && op.Method != "POST" && op.Method != "PUT" && op.Method != "DELETE" {
					return errors.New("service requires pinned method")
				}
				if !strings.HasPrefix(op.Path, "/") || strings.ContainsAny(op.Path, "?#\\") || strings.Contains(op.Path, "..") || strings.HasPrefix(op.Path, "//") {
					return errors.New("service requires exact origin path")
				}
				if !op.Write && !op.SemanticRead {
					return errors.New("service read operations require semantic_read assertion")
				}
			}
			if v.Kind == "cli" {
				write, ok := cliOperation(v.Adapter, name)
				if !ok || op.Write != write {
					return errors.New("CLI operation or write classification unsupported")
				}
			}
			if v.Kind == "git_sign" && (name != "sign_commit" || !op.Write) {
				return errors.New("only approved commit signing is supported")
			}
			if v.Kind == "model" && (name != "chat" || !op.Write) {
				return errors.New("model supports only chat (write)")
			}
		}
	}
	for id, s := range c.Settings {
		if !namePattern.MatchString(id) || !filepath.IsAbs(s.Root) || len(s.Paths) == 0 {
			return errors.New("settings requires name, absolute root and paths")
		}
		seen := map[string]bool{}
		for _, p := range s.Paths {
			if !safePath(p) || seen[p] {
				return errors.New("invalid or duplicate settings path")
			}
			seen[p] = true
		}
	}
	return nil
}
func safePath(p string) bool {
	return filepath.IsLocal(p) && p != "." && filepath.Clean(p) == p && !strings.Contains(p, "\\")
}
func cliOperation(adapter, operation string) (bool, bool) {
	switch adapter + ":" + operation {
	case "gh:repo_view", "gh:issue_list", "stripe:balance_get", "stripe:customers_list", "aws:identity", "aws:head_object":
		return false, true
	case "gh:issue_create", "stripe:customer_create":
		return true, true
	}
	return false, false
}
func validateFields(args map[string]any, fields map[string]string) error {
	for k := range args {
		if _, ok := fields[k]; !ok {
			return errors.New("SCOPE_DENIED")
		}
	}
	for k, typ := range fields {
		v, ok := args[k]
		if !ok && strings.HasSuffix(typ, "?") {
			continue
		}
		if !ok {
			return errors.New("INVALID_ARGUMENT")
		}
		good := false
		switch strings.TrimSuffix(typ, "?") {
		case "string":
			s, ok := v.(string)
			good = ok && len(s) <= 65536
		case "number":
			_, good = v.(float64)
		case "boolean":
			_, good = v.(bool)
		case "object":
			_, good = v.(map[string]any)
		case "array":
			_, good = v.([]any)
		}
		if !good {
			return errors.New("INVALID_ARGUMENT")
		}
	}
	return nil
}
