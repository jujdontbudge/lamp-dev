package control

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	pb "lamp.eilmer.si/pkg/workerpb"
)

//go:embed sandbox_kits.py
var sandboxKits string

var builtinKits = map[string]string{
	"browser": "Headless Chromium (throwaway guest profile)",
	"desktop": "Virtual desktop, input, screenshots and recording",
	"docker":  "Docker daemon inside the guest",
	"python":  "Python with pip, matplotlib and persistent interpreter",
	"node":    "Node.js and npm", "go": "Go", "rust": "Rust and Cargo", "c": "C/C++ and make",
	"lsp": "TypeScript language server",
}

func init() {
	for _, name := range []string{"sandbox_kit", "sandbox_run_code", "sandbox_lsp", "sandbox_browser", "sandbox_desktop"} {
		component := name[len("sandbox_"):]
		lifeHandlers[name] = func(s *Service, a caller, in map[string]any) (lifePlan, error) {
			in["component"] = component
			return s.planSandboxKit(a, in)
		}
	}
}

func kitNames(names []string) error {
	for _, name := range names {
		if _, ok := builtinKits[name]; !ok {
			return reject("INVALID_ARGUMENT", "unknown built-in kit: "+name)
		}
	}
	return nil
}

func (s *Service) planSandboxKit(a caller, in map[string]any) (lifePlan, error) {
	sb, err := s.lifeSandboxFor(a, textField(in, "sandbox_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := s.accountAdmission(a, "exec_create", sb); err != nil {
		return lifePlan{}, err
	}
	if sb.State != "ready" || s.busy[sb.Id] {
		return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox is not ready")
	}
	if value(sb.Boundary) != "vm" || s.privateBrowserDenied(a, sb) || value(sb.CaptureTrust) == "owner_private" {
		return lifePlan{}, reject("SCOPE_DENIED", "kits require an accessible task VM")
	}
	// The operation is journaled before the relay performs any guest effect.
	request := object(in)
	delete(request, "idempotency_key")
	delete(request, "sandbox_id")
	return lifePlan{ref: lifeRef("sandbox", sb.Id), timeout: 20 * time.Minute, run: func(ctx context.Context) (map[string]any, error) {
		s.readLock()
		current := s.journal.Sandboxes[sb.Id]
		if !s.current(a) || s.accountAdmission(a, "exec_create", current) != nil {
			s.mu.Unlock()
			return nil, reject("SCOPE_DENIED", "kit authority or task budget changed")
		}
		if current.State != "ready" || s.busy[sb.Id] || value(current.Generation) != value(sb.Generation) {
			s.mu.Unlock()
			return nil, reject("RESOURCE_IN_USE", "sandbox changed before kit execution")
		}
		s.starting[sb.Id]++
		s.mu.Unlock()
		defer func() { s.mu.Lock(); s.starting[sb.Id]--; s.mu.Unlock() }()
		return s.sandboxKitCall(ctx, sb.Id, request)
	}}, nil
}

func (s *Service) sandboxKitCall(ctx context.Context, sandbox string, in map[string]any) (map[string]any, error) {
	// The existing audited session bridge owns long-lived children. Nothing runs
	// on the host and a daemon restart reconnects to the guest-held contexts.
	request, _ := json.Marshal(in)
	program := "import base64;SOURCE=base64.b64decode(" + fmt.Sprintf("%q", base64.StdEncoding.EncodeToString([]byte(sandboxKits))) + ").decode();exec(compile(SOURCE,'<lamp-kits>','exec'))"
	argv := []string{"python3", "-c", program, string(request)}

	result, err := s.kitRelayExec(ctx, sandbox, argv)
	if err != nil {
		return nil, err
	}
	job := textField(result, "job")
	if len(job) != 32 {
		return nil, reject("OUTCOME_UNKNOWN", "guest kit job not acknowledged")
	}
	for _, c := range job {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return nil, reject("DATA_UNAVAILABLE", "invalid guest job identity")
		}
	}
	poll := `import pathlib,sys
p=pathlib.Path('/tmp/lamp-sandbox-kits')/(sys.argv[1]+'.result')
print(p.read_text() if p.exists() else '{"pending":true}')`
	for {
		result, err = s.kitRelayExec(ctx, sandbox, []string{"python3", "-c", poll, job})
		if err != nil {
			return nil, err
		}
		if pending, _ := result["pending"].(bool); !pending {
			return result, nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, &workerFailure{code: "OUTCOME_UNKNOWN", message: "guest kit job may still be running: " + job, unknown: true}
		case <-timer.C:
		}
	}
}

func (s *Service) kitRelayExec(ctx context.Context, sandbox string, argv []string) (map[string]any, error) {
	data, _ := json.Marshal(map[string]any{"action": "exec", "argv": argv, "timeout_ms": 5000})
	r := s.workerRequest(ctx, "kit", sandbox, s.sandboxGeneration(sandbox))
	r.Command = &pb.Request_SessionCall{SessionCall: &pb.SessionCall{RequestJson: data, BridgeSource: sessionRunner}}
	receipt, err := s.worker.call(ctx, r)
	if err != nil {
		return nil, err
	}
	raw := []byte(receipt.Detail["response_json"])
	var reply struct {
		Exit      int    `json:"exit_code"`
		Stdout    string `json:"stdout_base64"`
		Truncated bool   `json:"truncated"`
		Error     string `json:"error"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.Error != "" || reply.Truncated {
		return nil, &workerFailure{code: "OUTCOME_UNKNOWN", message: "guest kit reply unavailable; inspect operation before retrying", unknown: true}
	}
	output, err := base64.StdEncoding.DecodeString(reply.Stdout)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if json.Unmarshal(output, &result) != nil {
		return nil, fmt.Errorf("guest kit failed (exit %d)", reply.Exit)
	}
	if code := textField(result, "error"); code != "" {
		return nil, reject("INVALID_ARGUMENT", code+": "+textField(result, "message"))
	}
	return result, nil
}
