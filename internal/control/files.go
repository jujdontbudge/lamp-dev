package control

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

//go:embed guest_files.py
var pyFiles string

func guestPath(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", reject("INVALID_ARGUMENT", "guest path must be nonempty and contain no NUL")
	}
	return path.Clean("/" + p), nil
}
func init() {
	lifeHandlers["sandbox_file_delete"] = (*Service).planFileEdit
	lifeHandlers["sandbox_file_rename"] = (*Service).planFileEdit
	lifeHandlers["sandbox_file_write"] = (*Service).planFileWrite
}
func (s *Service) fileCall(ctx context.Context, sandbox string, in map[string]any) (map[string]any, error) {
	raw, _ := json.Marshal(in)
	data, err := s.guestPython(ctx, sandbox, pyFiles, string(raw))
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err = decodeNumbers(data, &result); err != nil {
		return nil, err
	}
	if code := textField(result, "error"); code != "" {
		return nil, reject(code, textField(result, "message"))
	}
	return result, nil
}
func (s *Service) fileReadHTTP(w http.ResponseWriter, r *http.Request, id string, op api.OperationDescriptor, in map[string]any) {
	var body map[string]any
	if _, err := decodeInput(w, r, &body, "sandbox_id path offset_bytes length_bytes cursor limit query contents wait_ms"); err != nil {
		writeError(w, id, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	for k, v := range body {
		if old, ok := in[k]; ok && old != v {
			writeError(w, id, "INVALID_ARGUMENT", "conflicting sandbox selector", nil)
			return
		}
		in[k] = v
	}
	if err := surface.Validate(op.InputSchema, in); err != nil {
		writeError(w, id, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	p, err := guestPath(textField(in, "path"))
	if err != nil {
		writeError(w, id, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	a := requestCaller(r)
	s.readLock()
	sb, err := s.lifeSandboxFor(a, textField(in, "sandbox_id"))
	if err == nil && (sb.State != "ready" || s.busy[sb.Id]) {
		err = reject("RESOURCE_IN_USE", "sandbox is not ready")
	}
	if err == nil && value(sb.CaptureTrust) == "owner_private" {
		err = reject("SCOPE_DENIED", "private custody files require the protected continuation adapter")
	}
	s.mu.Unlock()
	if err != nil {
		writeError(w, id, errorCode(err), err.Error(), nil)
		return
	}
	in["path"], in["action"] = p, strings.TrimPrefix(op.Name, "sandbox_file_")
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	result, err := s.fileCall(ctx, sb.Id, in)
	if err != nil {
		writeError(w, id, errorCode(err), err.Error(), nil)
		return
	}
	result["request_id"] = id
	prepareJSON(w, 200, result)()
}
func (s *Service) planFileEdit(a caller, in map[string]any) (lifePlan, error) {
	sb, err := s.lifeSandboxFor(a, textField(in, "sandbox_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if sb.Revision != uintField(in, "expected_revision") {
		return lifePlan{}, reject("REVISION_CONFLICT", "sandbox revision changed")
	}
	if sb.State != "ready" || s.busy[sb.Id] {
		return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox is not ready")
	}
	if value(sb.CaptureTrust) == "owner_private" {
		return lifePlan{}, reject("SCOPE_DENIED", "private custody files require the protected continuation adapter")
	}
	p, err := guestPath(textField(in, "path"))
	if err != nil {
		return lifePlan{}, err
	}
	in["path"] = p
	action := "delete"
	if textField(in, "destination") != "" {
		action = "rename"
		dest, err := guestPath(textField(in, "destination"))
		if err != nil {
			return lifePlan{}, err
		}
		in["destination"] = dest
	}
	in["action"] = action
	return lifePlan{ref: lifeRef("sandbox", sb.Id), run: func(ctx context.Context) (map[string]any, error) { return s.fileCall(ctx, sb.Id, in) }}, nil
}
func (s *Service) planFileWrite(a caller, in map[string]any) (lifePlan, error) {
	mode := textField(in, "mode")
	if mode != "" {
		n, err := strconv.ParseUint(mode, 8, 32)
		if err != nil || n > 0777 {
			return lifePlan{}, reject("INVALID_ARGUMENT", "mode must be octal permissions 0000..0777")
		}
	}
	if h := textField(in, "expected_sha256"); h != "" {
		b, err := s.readyBlob(a, textField(in, "blob_id"))
		if err != nil {
			return lifePlan{}, err
		}
		if trimDigest(h) != textField(b.R, "sha256") {
			return lifePlan{}, reject("REVISION_CONFLICT", "blob SHA-256 differs from expected_sha256")
		}
	}
	in["direction"] = "upload"
	plan, err := s.planFileTransfer(a, in)
	if err != nil {
		return lifePlan{}, err
	}
	run := plan.run
	plan.run = func(ctx context.Context) (map[string]any, error) {
		_, err := run(ctx)
		if err == nil && mode != "" {
			_, err = s.guestPython(ctx, textField(in, "sandbox_id"), `import os,sys;os.chmod(sys.argv[1],int(sys.argv[2],8))`, textField(in, "path"), mode)
		}
		return map[string]any{}, err
	}
	return plan, nil
}

func errorCode(err error) string {
	var core *coreError
	if errors.As(err, &core) {
		return core.code
	}
	var worker *workerFailure
	if errors.As(err, &worker) {
		return worker.code
	}
	return "DATA_UNAVAILABLE"
}
