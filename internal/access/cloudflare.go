package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/JohanSardar/lamp/pkg/plugin"
)

// The plugin is a confined planner. Only this owner-configured authority can
// start the pinned connector, and neither credentials nor executable arguments
// cross the plugin pipe.
type tunnelAuthority struct {
	mu            sync.Mutex
	ctx           context.Context
	config        Cloudflare
	origin, state string
	cmd           *exec.Cmd
	staged        string
	done          chan struct{}
	revoked       bool
}

func (a *tunnelAuthority) Effect(ctx context.Context, e plugin.Effect) (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.revoked || a.ctx.Err() != nil || ctx.Err() != nil || e.Kind != "access" || e.ResourceID != a.config.TunnelID {
		return nil, errors.New("access grant denied")
	}
	var in struct {
		Operation string `json:"operation"`
	}
	if json.Unmarshal(e.Input, &in) != nil || in.Operation != "start" || a.cmd != nil {
		return nil, errors.New("invalid access effect")
	}
	bin, err := os.ReadFile(a.config.Executable)
	if err != nil || plugin.Digest(bin) != a.config.ExecutableSHA256 {
		return nil, errors.New("cloudflared executable digest mismatch")
	}
	if _, err = PrivateRead(a.config.CredentialsFile); err != nil {
		return nil, err
	}
	// JSON is a YAML subset. The final rule prevents accidental catch-all exposure.
	cfg := map[string]any{"tunnel": a.config.TunnelID, "credentials-file": a.config.CredentialsFile, "ingress": []any{map[string]any{"hostname": a.config.Hostname, "service": "http://" + a.origin}, map[string]any{"service": "http_status:404"}}}
	raw, _ := json.Marshal(cfg)
	path := filepath.Join(a.state, "cloudflared.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return nil, err
	}
	// Execute the bytes we checked, rather than a path a package updater can replace.
	f, err := os.CreateTemp(a.state, "cloudflared-executable-")
	if err != nil {
		return nil, err
	}
	a.staged = f.Name()
	_, err = f.Write(bin)
	if err == nil {
		err = f.Chmod(0500)
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(a.staged)
		return nil, err
	}
	a.cmd = exec.CommandContext(a.ctx, a.staged, "--no-autoupdate", "--config", path, "tunnel", "run")
	a.cmd.Env = []string{"LANG=C", "HOME=" + a.state}
	// Connector logs can include account details. Keep them in a private file.
	log, err := os.OpenFile(filepath.Join(a.state, "cloudflared.log"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		os.Remove(a.staged)
		return nil, err
	}
	a.cmd.Stdout = log
	a.cmd.Stderr = log
	if err = a.cmd.Start(); err != nil {
		log.Close()
		os.Remove(a.staged)
		a.cmd = nil
		return nil, errors.New("cloudflared failed to start")
	}
	a.done = make(chan struct{})
	go func() { _ = a.cmd.Wait(); log.Close(); os.Remove(a.staged); close(a.done) }()
	return json.Marshal(map[string]any{"state": "started", "url": "https://" + a.config.Hostname})
}
func (a *tunnelAuthority) Revoke(string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.revoked = true
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
	}
}
func (a *tunnelAuthority) FailDependent(string, string) { a.Revoke("") }

func (s *Service) startCloudflare(ctx context.Context, c Cloudflare) (func(), <-chan struct{}, error) {
	raw, e := os.ReadFile(c.Manifest)
	if e != nil {
		return nil, nil, e
	}
	bin, e := os.ReadFile(c.Plugin)
	if e != nil {
		return nil, nil, e
	}
	pkg, e := plugin.Admit(raw, bin, plugin.Approval{OwnerID: "access-owner", ManifestDigest: c.ManifestSHA256})
	if e != nil {
		return nil, nil, e
	}
	if pkg.Manifest().Type != "access" {
		return nil, nil, errors.New("cloudflare requires an access plugin")
	}
	a := &tunnelAuthority{ctx: ctx, config: c, origin: s.config.HTTPListen, state: s.config.StateDir}
	w, e := plugin.Start(ctx, plugin.StartOptions{ID: "cloudflare", Package: pkg, Executable: c.Plugin, StateDir: s.config.StateDir, Launcher: c.Launcher, LauncherDigest: c.LauncherSHA256, DiagnosticSoftLimits: c.DiagnosticSoftLimits, Authority: a})
	if e != nil {
		return nil, nil, e
	}
	cleanup := func() {
		_ = w.Close()
		a.Revoke("")
		if a.done != nil {
			<-a.done
		}
	}
	b := plugin.Binding{OwnerID: "access-owner", TaskID: "access-route", OperationID: randomID(), Generation: 1, ParentGrant: "owner-config", Deadline: time.Now().Add(10 * time.Second)}
	g, e := w.Grant(plugin.GrantRequest{Kind: "access", ResourceID: c.TunnelID, Binding: b})
	if e == nil {
		_, e = w.Call(ctx, "run", b, g, json.RawMessage(`{"operation":"start"}`))
	}
	if e != nil {
		cleanup()
		return nil, nil, e
	}
	// Connector loss is a service failure, never an advertised healthy route.
	return cleanup, a.done, nil
}

func CloudflareManifest(executable []byte) ([]byte, error) {
	input := plugin.Schema{Type: "object", Properties: map[string]plugin.Schema{"operation": {Type: "string", Enum: []json.RawMessage{json.RawMessage(`"start"`)}}}, Required: []string{"operation"}}
	result := plugin.Schema{Type: "object", Properties: map[string]plugin.Schema{"state": {Type: "string"}, "url": {Type: "string"}}, Required: []string{"state", "url"}}
	m := plugin.Manifest{Type: "access", Publisher: "lamp.cloudflare", Version: "0.1.0", Digest: plugin.Digest(executable), ProtocolVersion: plugin.Protocol, TrustClass: "platform", Permissions: []string{"access.use"}, Limits: plugin.DefaultLimits(), ConfigSchema: plugin.Schema{Type: "object"}, Compatibility: plugin.Compatibility{APIMajor: 1, MinMinor: 1, MaxMinor: 1}, Methods: map[string]plugin.Method{"run": {Input: input, Result: result}}}
	return json.MarshalIndent(m, "", "  ")
}

// RunCloudflarePlugin implements lamp.plugin/1, with no network or credential access.
func RunCloudflarePlugin(in *os.File, out *os.File) error {
	send := func(m plugin.Message) error {
		m.JSONRPC = "2.0"
		b, e := json.Marshal(m)
		if e != nil {
			return e
		}
		return plugin.WriteFrame(out, b)
	}
	pending := ""
	for {
		raw, e := plugin.ReadFrame(in)
		if e != nil {
			return e
		}
		var m plugin.Message
		if e = json.Unmarshal(raw, &m); e != nil {
			return e
		}
		switch m.Method {
		case "initialize":
			var hello plugin.Hello
			if json.Unmarshal(m.Params, &hello) != nil || hello.ProtocolVersion != plugin.Protocol || hello.Type != "access" {
				return errors.New("invalid initialization")
			}
			if e = send(plugin.Message{ID: m.ID, Result: m.Params}); e != nil {
				return e
			}
		case "run":
			if pending != "" {
				return errors.New("call already active")
			}
			var call plugin.Invocation
			if json.Unmarshal(m.Params, &call) != nil || call.Grant.Kind != "access" {
				return errors.New("invalid invocation")
			}
			pending = m.ID
			effect, _ := json.Marshal(plugin.Effect{CallID: m.ID, GrantID: call.Grant.ID, Kind: "access", ResourceID: call.Grant.ResourceID, Binding: call.Binding, Input: call.Input})
			if e = send(plugin.Message{ID: "cloudflare-start", Method: "host.effect", Params: effect}); e != nil {
				return e
			}
		case "":
			if pending == "" || m.ID != "cloudflare-start" {
				return errors.New("unexpected response")
			}
			if e = send(plugin.Message{ID: pending, Result: m.Result, Error: m.Error}); e != nil {
				return e
			}
			pending = ""
		case "cancel":
			return nil
		default:
			return fmt.Errorf("unsupported plugin method")
		}
	}
}
