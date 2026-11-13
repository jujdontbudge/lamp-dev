package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
)

type sessionTransfer struct {
	InFlight                                     bool `json:"-"`
	Kind, Phase, OperationID, SourceID, TargetID string
	Caller                                       caller
	Source, Target                               map[string]any
	SourceEpoch, TargetEpoch                     uint64
	Offset, UploadOffset, Size                   uint64
	FinalOutput, FinalEvents                     uint64
	ArchiveFile                                  string
	Archive, SHA256                              string
	Paths                                        []string
	Metadata                                     map[string]any
	// Carried holds acknowledged inputs the fenced source never delivered;
	// they are replayed once on the target after its start.
	Carried []sessionWork `json:",omitempty"`
}

type sessionChangeSet struct {
	Resource    map[string]any `json:"resource"`
	Archive     string         `json:"archive,omitempty"`
	ArchiveFile string         `json:"archive_file,omitempty"`
	SHA256      string         `json:"sha256"`
}

func (s *Service) transferLocation(a caller, location map[string]any) error {
	switch textField(location, "kind") {
	case "host":
		host, exists := s.sessions.Hosts[textField(location, "host_id")]
		if !exists || host.OwnerID != a.Principal {
			return reject("SCOPE_DENIED", "target host is not enrolled for this principal")
		}
	case "sandbox":
		sb, exists := s.journal.Sandboxes[textField(location, "sandbox_id")]
		if !exists || !s.sandboxAllowed(a, sb) || sandboxHost(sb) != textField(location, "host_id") {
			return reject("NOT_FOUND", "target sandbox not found")
		}
		if sb.State != "ready" || s.sandboxMustStop(sb) {
			return reject("RESOURCE_IN_USE", "target sandbox must be ready and within its live authority and budget")
		}
		if value(sb.CaptureTrust) == "owner_private" {
			return reject("AUTH_TRANSPORT_UNSUPPORTED", "native custody requires its protected continuation adapter")
		}
	default:
		return reject("INVALID_ARGUMENT", "a host or sandbox location is required")
	}
	return nil
}

// Checking a move never fences a runner, changes a revision or journals work.
func (s *Service) checkSessionTeleport(a caller, work *managedSession, target map[string]any) error {
	m := work.Resource
	if textField(m, "state") != "active" || work.GitBusy {
		return reject("RESOURCE_IN_USE", "session must be active and outside another transfer")
	}
	if s.sessions.Harnesses[textField(m, "harness_id")].Native == nil {
		return reject("AUTH_TRANSPORT_UNSUPPORTED", "harness has no native continuation adapter")
	}
	if textField(m, "shared_session_id") != "" {
		return reject("RESOURCE_IN_USE", "side chats share their main session's location; end the side chat before moving the main session")
	}
	for _, child := range s.journal.Control.Sessions {
		if textField(child.Resource, "shared_session_id") == textField(m, "id") && textField(child.Resource, "state") != "ended" && textField(child.Resource, "state") != "failed" {
			return reject("RESOURCE_IN_USE", "end shared-file side chats before moving this session")
		}
	}
	return s.transferLocation(a, target)
}

func (s *Service) sessionTransferMutation(a caller, op api.OperationDescriptor, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	c := s.journal.Control
	if c.SessionTransfers == nil {
		c.SessionTransfers = map[string]*sessionTransfer{}
	}
	if c.ChangeSets == nil {
		c.ChangeSets = map[string]*sessionChangeSet{}
	}
	kind, verb, _ := strings.Cut(op.Scope, ".")
	ref := api.ResourceRef{Kind: api.ResourceKind(kind)}
	fail := func(code, message string) (api.ResourceRef, map[string]any, *string, []string, error) {
		return ref, nil, nil, nil, reject(code, message)
	}
	if kind == "change_set" {
		if verb == "create" {
			if err := acceptFields(in, "source_session_id target_session_id selected_paths"); err != nil {
				return ref, nil, nil, nil, err
			}
			source, target := c.Sessions[textField(in, "source_session_id")], c.Sessions[textField(in, "target_session_id")]
			if source == nil || target == nil || textField(source.Resource, "owner_id") != a.Principal || textField(target.Resource, "owner_id") != a.Principal || !s.sessionAllowed(a, source.Resource, "session.get") || !s.sessionAllowed(a, target.Resource, "session.get") {
				return fail("NOT_FOUND", "source and target sessions must both be owned and authorized")
			}
			if textField(source.Resource, "state") != "active" || textField(target.Resource, "state") != "active" {
				return fail("RESOURCE_IN_USE", "git return requires active source and target sessions")
			}
			if uint64(len(c.ChangeSets)) >= s.quotas.Records {
				return fail("CONTROL_QUOTA_EXCEEDED", "retained change-set quota exceeded")
			}
			m := newResource("change_set", a.Principal)
			m["id"], m["state"] = s.resourceID("cs"), "proposed"
			m["source_session_id"], m["target_session_id"] = source.Resource["id"], target.Resource["id"]
			m["selected_paths"] = sessionTransferPaths(in["selected_paths"])
			ref.Id = textField(m, "id")
			c.ChangeSets[ref.Id] = &sessionChangeSet{Resource: m}
			t := s.newSessionTransfer("change_set", a, source.Resource, target.Resource)
			t.Paths = sessionTransferPaths(in["selected_paths"])
			c.SessionTransfers[ref.Id] = t
			return ref, map[string]any{"change_set_id": ref.Id}, nil, nil, nil
		}
		change := c.ChangeSets[textField(in, "change_set_id")]
		if change == nil || textField(change.Resource, "owner_id") != a.Principal {
			return fail("NOT_FOUND", "change set not found")
		}
		ref.Id = textField(change.Resource, "id")
		if uintField(change.Resource, "revision") != uintField(in, "expected_revision") {
			return fail("REVISION_CONFLICT", "change-set revision changed")
		}
		if t := c.SessionTransfers[ref.Id]; t != nil && t.Phase != "done" {
			return fail("RESOURCE_IN_USE", "change set is still being prepared or applied")
		}
		if verb == "reject" || verb == "delete" {
			change.Resource["state"] = "rejected"
			change.Archive, change.ArchiveFile = "", ""
			if transfer := c.SessionTransfers[ref.Id]; transfer != nil {
				transfer.Archive, transfer.ArchiveFile = "", ""
			}
			editResource(change.Resource)
			return ref, map[string]any{}, nil, nil, nil
		}
		if verb != "apply" || (change.Archive == "" && change.ArchiveFile == "") || textField(change.Resource, "state") != "proposed" {
			return fail("RESOURCE_IN_USE", "a prepared proposed change set is required")
		}
		if _, selected := in["selected_paths"]; selected {
			return fail("INVALID_ARGUMENT", "select paths when creating the change set")
		}
		source, target := c.Sessions[textField(change.Resource, "source_session_id")], c.Sessions[textField(change.Resource, "target_session_id")]
		if source == nil || target == nil || !s.sessionAllowed(a, source.Resource, "session.get") || !s.sessionAllowed(a, target.Resource, "session.input") {
			return fail("SCOPE_DENIED", "git return session authority changed")
		}
		if target.GitBusy || textField(target.Resource, "state") != "active" {
			return fail("RESOURCE_IN_USE", "target session is busy")
		}
		target.GitBusy = true
		t := s.newSessionTransfer("apply", a, source.Resource, target.Resource)
		t.Phase, t.Archive, t.SHA256, t.ArchiveFile = "upload", change.Archive, change.SHA256, change.ArchiveFile
		c.SessionTransfers[ref.Id] = t
		return ref, map[string]any{}, nil, nil, nil
	}
	source := c.Sessions[textField(in, "session_id")]
	if source == nil || textField(source.Resource, "owner_id") != a.Principal || !s.sessionAllowed(a, source.Resource, op.Scope) {
		return fail("NOT_FOUND", "session not found")
	}
	m := source.Resource
	ref.Id = textField(m, "id")
	if uintField(m, "revision") != uintField(in, "expected_revision") {
		return fail("REVISION_CONFLICT", "session revision changed")
	}
	if textField(m, "state") != "active" || source.GitBusy {
		return fail("RESOURCE_IN_USE", "session must be active and outside another transfer")
	}
	if t := c.SessionTransfers[ref.Id]; t != nil && t.Phase != "done" {
		return fail("RESOURCE_IN_USE", "session transfer is already in progress")
	}
	if verb == "spawn" {
		if err := acceptFields(in, "session_id sandbox_mode recipe_id selected_paths harness_id prompt side"); err != nil {
			return ref, nil, nil, nil, err
		}
		recipe, exists := s.sessions.Recipes[textField(in, "recipe_id")]
		if !exists {
			if textField(in, "recipe_id") != "" {
				return fail("NOT_FOUND", "session recipe not found")
			}
			recipe = SessionRecipe{HarnessID: textField(m, "harness_id"), AuthMode: textField(m, "auth_mode")}
			if harness := textField(in, "harness_id"); harness != "" {
				recipe.HarnessID, recipe.AuthMode = harness, ""
			}
		}
		location := object(m["location"])
		mode := textField(in, "sandbox_mode")
		if id, ok := strings.CutPrefix(mode, "sandbox:"); ok {
			sb := s.journal.Sandboxes[id]
			location = map[string]any{"kind": "sandbox", "host_id": sandboxHost(sb), "sandbox_id": id}
		} else if id, ok := strings.CutPrefix(mode, "host:"); ok {
			location = map[string]any{"kind": "host", "host_id": id}
		} else if mode != "" && mode != "same" {
			return fail("INVALID_ARGUMENT", "choose same, sandbox:<id>, or host:<id>")
		}
		if err := s.transferLocation(a, location); err != nil {
			return ref, nil, nil, nil, err
		}
		sbID, agentMode := "", "inside"
		if textField(location, "kind") == "sandbox" {
			sbID, agentMode = textField(location, "sandbox_id"), "inside"
		}
		side, _ := in["side"].(bool)
		if side && mode != "" && mode != "same" {
			return fail("INVALID_ARGUMENT", "side chats share the parent's location and files")
		}
		child := map[string]any{"harness_id": recipe.HarnessID, "auth_mode": recipe.AuthMode, "parent_session_id": ref.Id, "sandbox_id": sbID, "agent_mode": agentMode, "location": location}
		child["prompt"], child["task_id"] = in["prompt"], m["task_id"]
		childRef, result, _, changed, err := s.sessionMutation(a, api.OperationDescriptor{Scope: "session.create"}, child)
		if err != nil {
			return ref, nil, nil, nil, err
		}
		if side {
			shared := textField(m, "shared_session_id")
			if shared == "" {
				shared = ref.Id
			}
			c.Sessions[childRef.Id].Resource["shared_session_id"] = shared
			c.Sessions[childRef.Id].Resource["shared_epoch"] = c.Sessions[shared].Resource["location_epoch"]
			return childRef, result, nil, changed, nil
		}
		// Keep the initial prompt, but start only after the workspace restore.
		c.Sessions[childRef.Id].Pending = c.Sessions[childRef.Id].Pending[1:]
		t := s.newSessionTransfer("spawn", a, m, c.Sessions[childRef.Id].Resource)
		if value, exists := in["selected_paths"]; exists {
			t.Paths = sessionTransferPaths(value)
		}
		c.SessionTransfers[childRef.Id] = t
		return childRef, result, nil, changed, nil
	}
	if verb != "teleport" && verb != "checkpoint" {
		return fail("INVALID_ARGUMENT", "unsupported session transfer")
	}
	if s.sessions.Harnesses[textField(m, "harness_id")].Native == nil {
		return fail("AUTH_TRANSPORT_UNSUPPORTED", "this harness does not expose native continuation state")
	}
	if err := acceptFields(in, "session_id location"); err != nil {
		return ref, nil, nil, nil, err
	}
	target := object(m["location"])
	if verb == "teleport" {
		target = object(in["location"])
		if err := s.checkSessionTeleport(a, source, target); err != nil {
			return ref, nil, nil, nil, err
		}
	}
	t := s.newSessionTransfer(verb, a, m, m)
	t.Target = target
	if verb == "teleport" {
		if t.SourceEpoch == ^uint64(0) {
			return fail("CONTROL_QUOTA_EXCEEDED", "location epoch exhausted")
		}
		t.TargetEpoch++
		m["location_epoch"] = t.TargetEpoch
		for _, lease := range c.InputLeases {
			if textField(lease, "resource_id") == ref.Id && textField(lease, "state") == "active" {
				lease["state"] = "revoked"
				editResource(lease)
			}
		}
	}
	m["state"] = "moving"
	m["transfer"] = map[string]any{"phase": "snapshot", "bytes_transferred": uint64(0), "artifact_bytes": uint64(0), "safe_to_close_source": false}
	editResource(m)
	c.SessionTransfers[ref.Id] = t
	return ref, map[string]any{}, nil, nil, nil
}

func (s *Service) newSessionTransfer(kind string, a caller, source, target map[string]any) *sessionTransfer {
	return &sessionTransfer{Kind: kind, Phase: "snapshot", Caller: a, SourceID: textField(source, "id"), TargetID: textField(target, "id"), Source: object(source["location"]), Target: object(target["location"]), SourceEpoch: uintField(source, "location_epoch"), TargetEpoch: uintField(target, "location_epoch")}
}

func sessionTransferPaths(value any) []string {
	var document struct {
		Paths []string `json:"paths"`
	}
	assign(map[string]any{"paths": value}, &document)
	if document.Paths == nil {
		return []string{}
	}
	return document.Paths
}

func (s *Service) armSessionTransfer(id, operation string) bool {
	t := s.journal.Control.SessionTransfers[id]
	if t == nil || t.OperationID != "" {
		return false
	}
	t.OperationID = operation
	return true
}

func (s *Service) transferCall(ctx context.Context, t *sessionTransfer, source bool, request map[string]any) (map[string]any, error) {
	id, location, epoch := t.TargetID, t.Target, t.TargetEpoch
	if source {
		id, location, epoch = t.SourceID, t.Source, t.SourceEpoch
	}
	s.readLock()
	work := s.journal.Control.Sessions[id]
	if work == nil {
		s.mu.Unlock()
		return nil, errors.New("session unavailable")
	}
	harness := s.sessions.Harnesses[textField(work.Resource, "harness_id")]
	host := s.sessions.Hosts[textField(location, "host_id")]
	sb := s.journal.Sandboxes[textField(location, "sandbox_id")]
	s.mu.Unlock()
	request["id"], request["epoch"] = id, epoch
	request["root"] = "/tmp/lamp-sessions"
	request["cwd"], request["env"] = harness.Cwd, harness.Env
	if textField(location, "kind") == "host" {
		request["root"] = host.Root
	}
	raw, _ := json.Marshal(request)
	if textField(location, "kind") == "host" && host.Socket != "" {
		connection, err := (&net.Dialer{}).DialContext(ctx, "unix", host.Socket)
		if err != nil {
			return nil, err
		}
		defer connection.Close()
		connection.SetDeadline(time.Now().Add(10 * time.Second))
		if err := json.NewEncoder(connection).Encode(request); err != nil {
			return nil, err
		}
		var reply map[string]any
		decoder := json.NewDecoder(io.LimitReader(connection, 128<<10))
		decoder.UseNumber()
		err = decoder.Decode(&reply)
		return reply, err
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	var data []byte
	var err error
	if textField(location, "kind") == "host" {
		argv := append(append([]string{}, host.Command[1:]...), "-c", sessionRunner, encoded)
		data, err = exec.CommandContext(ctx, host.Command[0], argv...).Output()
	} else {
		data, err = s.sessionGuestCall(ctx, sb, []string{"python3", "-c", sessionRunner, encoded})
	}
	if err != nil {
		return nil, err
	}
	return surface.Decode(data)
}

// Large code/continuation archives live in private files. Journaling every
// base64 chunk in the control document otherwise retains gigabytes for a
// modest repository. The receipt offset selects the acknowledged file prefix
// after a crash; atomic replacement happens before the receipt is committed.
func (s *Service) transferArchive(t *sessionTransfer) (string, error) {
	if t.ArchiveFile == "" {
		return t.Archive, nil
	}
	if filepath.Base(t.ArchiveFile) != t.ArchiveFile {
		return "", errors.New("invalid transfer artifact")
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, "transfer-artifacts", t.ArchiveFile))
	if len(raw) > (16<<20)*4/3+4 {
		return "", errors.New("transfer artifact exceeds quota")
	}
	return string(raw), err
}
func (s *Service) appendTransferArchive(t *sessionTransfer, chunk string) error {
	raw, err := s.transferArchive(t)
	if err != nil {
		return err
	}
	prefix := base64.StdEncoding.EncodedLen(int(t.Offset))
	if prefix > len(raw) || prefix+len(chunk) > (16<<20)*4/3+4 {
		return errors.New("invalid transfer artifact offset")
	}
	directory := filepath.Join(s.dir, "transfer-artifacts")
	if err := PrivateDir(directory); err != nil {
		return err
	}
	name := t.OperationID + ".b64"
	if err := atomicSecret(filepath.Join(directory, name), []byte(raw[:prefix]+chunk)); err != nil {
		return err
	}
	t.ArchiveFile, t.Archive = name, ""
	return nil
}

func (s *Service) failSessionTransfer(id, phase string) {
	s.readLock()
	defer s.mu.Unlock()
	t := s.journal.Control.SessionTransfers[id]
	if t == nil || t.Phase != phase {
		return
	}
	rec := s.journal.Operations[t.OperationID]
	if rec == nil {
		return
	}
	target := s.journal.Control.Sessions[t.TargetID]
	target.GitBusy = false
	if t.Kind == "teleport" && phase == "snapshot" {
		target.Resource["state"], target.Resource["location_epoch"] = "active", t.SourceEpoch
	} else if t.Kind == "spawn" || t.Kind == "teleport" {
		target.Resource["state"], target.Resource["status_reason"] = "failed", "transfer failed; source files and continuation retained at source location"
	}
	editResource(target.Resource)
	t.Phase = "done"
	rec.Operation.State, rec.Operation.CompletedAt = "failed", pointer(timestamp())
	rec.Operation.Error = &api.ErrorDetail{Code: "DATA_UNAVAILABLE", Message: "session transfer failed during " + phase + "; source files retained"}
	rec.Operation.Revision++
	rec.Operation.UpdatedAt = timestamp()
	if t.Kind == "teleport" {
		target.Resource["transfer"] = map[string]any{"phase": "failed", "bytes_transferred": min(t.UploadOffset*3/4, t.Size), "artifact_bytes": t.Size, "safe_to_close_source": false}
		editResource(target.Resource)
	}
	if err := s.save("$control", t.OperationID); err != nil {
		s.broken = true
		return
	}
	if ch := s.done[t.OperationID]; ch != nil {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func (s *Service) driveSessionTransfer(id string) {
	s.readLock()
	t := s.journal.Control.SessionTransfers[id]
	if t == nil || t.Phase == "done" || !s.current(t.Caller) || s.broken {
		s.mu.Unlock()
		return
	}
	if t.InFlight {
		s.mu.Unlock()
		return
	}
	t.InFlight = true
	defer func() {
		s.readLock()
		if current := s.journal.Control.SessionTransfers[id]; current != nil {
			current.InFlight = false
		}
		s.mu.Unlock()
	}()
	copy := *t
	t = &copy
	source, target := s.journal.Control.Sessions[t.SourceID], s.journal.Control.Sessions[t.TargetID]
	if source == nil || target == nil {
		s.mu.Unlock()
		return
	}
	if t.Phase == "snapshot" && t.Kind != "spawn" && t.Kind != "change_set" && len(source.Pending) != 0 {
		s.mu.Unlock()
		return
	}
	if t.Phase == "drain" && (source.Offset < t.FinalOutput || source.EventOffset < t.FinalEvents) {
		s.mu.Unlock()
		return
	}
	if t.Phase == "start" && textField(target.Resource, "state") != "active" {
		s.mu.Unlock()
		return
	}
	if t.Kind == "apply" && len(target.Pending) != 0 {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	request := map[string]any{"transfer_id": id, "artifact_sha256": t.SHA256}
	useSource := false
	switch t.Phase {
	case "snapshot":
		useSource = true
		request["action"], request["include_native"], request["fence"] = "snapshot", t.Kind == "teleport" || t.Kind == "checkpoint", t.Kind == "teleport"
		if len(t.Paths) > 0 {
			request["selected_paths"] = t.Paths
		}
	case "export":
		useSource = true
		request["action"], request["artifact_offset"] = "export", t.Offset
	case "upload":
		archive, err := s.transferArchive(t)
		if err != nil {
			return
		}
		end := t.UploadOffset + 32768
		if end > uint64(len(archive)) {
			end = uint64(len(archive))
		}
		request["action"], request["artifact_offset"], request["artifact_data"] = "upload", t.UploadOffset, archive[t.UploadOffset:end]
	case "restore":
		request["action"], request["include_native"] = "restore", t.Kind == "teleport"
	case "apply":
		request["action"] = "git_apply"
	}
	reply := map[string]any{}
	var callErr error
	if _, call := request["action"]; call {
		ctx, cancel := context.WithTimeout(s.context(), 10*time.Second)
		reply, callErr = s.transferCall(ctx, t, useSource, request)
		cancel()
		if reply["error"] != nil {
			s.failSessionTransfer(id, t.Phase)
			return
		}
		if callErr != nil || reply["wait"] == true {
			return
		}
	}
	s.readLock()
	defer s.mu.Unlock()
	current := s.journal.Control.SessionTransfers[id]
	if current == nil || current.Phase != t.Phase || current.OperationID != t.OperationID || !s.current(t.Caller) {
		return
	}
	before := &controlState{}
	raw, _ := json.Marshal(s.journal.Control)
	json.Unmarshal(raw, before)
	rec := s.journal.Operations[t.OperationID]
	oldRecord := *rec
	t = current
	source, target = s.journal.Control.Sessions[t.SourceID], s.journal.Control.Sessions[t.TargetID]
	done := false
	switch t.Phase {
	case "snapshot":
		t.SHA256, t.Size = textField(reply, "artifact_sha256"), uintField(reply, "artifact_size")
		t.Metadata = object(reply["artifact_info"])
		t.Phase = "export"
		if t.Kind == "teleport" {
			if textField(reply, "state") != "fenced" {
				return
			}
			t.FinalOutput, t.FinalEvents, t.Phase = uintField(reply, "size"), uintField(reply, "event_offset"), "drain"
			// Settle what is still queued against what the source runner
			// actually delivered before the fence. Delivered inputs are part of
			// the native snapshot and must not run again; undelivered ones are
			// carried to the target; anything else is dropped on the record.
			inputs := object(reply["inputs"])
			delivered, dropped := 0, 0
			for _, work := range source.Pending {
				if work.Action != "input" {
					dropped++
					continue
				}
				seen := object(inputs[work.Lease])
				if (textField(seen, "state") == "delivered" && uintField(seen, "sequence") >= work.Sequence) || uintField(seen, "sequence") > work.Sequence {
					delivered++
				} else {
					t.Carried = append(t.Carried, work)
				}
			}
			source.Pending = nil
			source.Resource["transfer_inputs"] = map[string]any{"delivered_at_source": delivered, "carried_to_target": len(t.Carried), "dropped_non_input": dropped, "location_epoch": t.TargetEpoch}
		}
	case "drain":
		t.Phase = "export"
	case "export":
		chunk := textField(reply, "artifact_data")
		if uintField(reply, "artifact_offset") <= t.Offset {
			return
		}
		if err := s.appendTransferArchive(t, chunk); err != nil {
			return
		}
		t.Offset = uintField(reply, "artifact_offset")
		if t.Offset == t.Size {
			archive, err := s.transferArchive(t)
			if err != nil {
				return
			}
			artifact, err := base64.StdEncoding.DecodeString(archive)
			hash := sha256.Sum256(artifact)
			if err != nil || hex.EncodeToString(hash[:]) != t.SHA256 {
				s.journal.Control = before
				return
			}
			if t.Kind == "change_set" {
				change := s.journal.Control.ChangeSets[id]
				change.Archive, change.SHA256, change.ArchiveFile = t.Archive, t.SHA256, t.ArchiveFile
				change.Resource["base_tree_digest"], change.Resource["commit_ids"] = t.Metadata["base_tree"], t.Metadata["commits"]
				editResource(change.Resource)
				t.Archive, done = "", true
			} else if t.Kind == "checkpoint" {
				source.Checkpoint = archive
				source.Resource["state"] = "active"
				editResource(source.Resource)
				t.Archive, done = "", true
			} else {
				t.Phase = "upload"
			}
		}
	case "upload":
		t.UploadOffset = uintField(reply, "artifact_offset")
		if archive, err := s.transferArchive(t); err == nil && t.UploadOffset == uint64(len(archive)) {
			t.Phase = "restore"
			if t.Kind == "apply" {
				t.Phase = "apply"
			}
		}
	case "restore":
		if reply["restored"] != true {
			return
		}
		target.Resource["location"], target.Resource["location_epoch"] = t.Target, t.TargetEpoch
		target.Resource["agent_mode"] = "inside"
		if textField(t.Target, "kind") == "sandbox" {
			target.Resource["sandbox_id"], target.Resource["agent_mode"] = t.Target["sandbox_id"], "inside"
		}
		target.Resource["state"], target.Resource["observation_state"] = "starting", "stale"
		target.Offset, target.EventOffset = 0, 0
		target.Pending = append([]sessionWork{{ID: newID("dispatch"), Action: "start"}}, target.Pending...)
		// The target runner starts with a fresh input ledger, so carried inputs
		// are re-keyed to one per-epoch lease and numbered from zero.
		carry := "carry@" + strconv.FormatUint(t.TargetEpoch, 10)
		for i, work := range t.Carried {
			work.ID, work.Lease, work.Sequence = newID("dispatch"), carry, uint64(i)
			target.Pending = append(target.Pending, work)
		}
		t.Carried = nil
		editResource(target.Resource)
		t.Phase = "start"
	case "start":
		t.Archive, done = "", true
	case "apply":
		change := s.journal.Control.ChangeSets[id]
		change.Resource["state"] = "applied"
		if conflicts, ok := reply["conflicts"].([]any); ok && len(conflicts) > 0 {
			change.Resource["state"], change.Resource["conflicts"] = "conflicted", conflicts
		}
		editResource(change.Resource)
		target.GitBusy = false
		t.Archive, done = "", true
	}
	if done {
		t.Phase = "done"
		rec.Operation.State, rec.Operation.CompletedAt = "succeeded", pointer(timestamp())
		rec.Operation.Revision++
		rec.Operation.UpdatedAt = timestamp()
	}
	if t.Kind == "teleport" {
		target.Resource["transfer"] = map[string]any{"phase": t.Phase, "bytes_transferred": min(t.UploadOffset*3/4, t.Size), "artifact_bytes": t.Size, "safe_to_close_source": done}
		editResource(target.Resource)
	}
	if err := s.save("$control", t.OperationID); err != nil {
		if errors.Is(err, errAuthorityConflict) {
			s.journal.Control = before
			*rec = oldRecord
		} else {
			s.broken = true
		}
		return
	}
	if done {
		if t.Kind != "change_set" && t.ArchiveFile != "" {
			_ = os.Remove(filepath.Join(s.dir, "transfer-artifacts", t.ArchiveFile))
		}
		if ch := s.done[t.OperationID]; ch != nil {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	}
}
