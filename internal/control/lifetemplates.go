package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"time"

	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
)

func init() {
	lifeHandlers["sandbox_save_template"] = (*Service).planSaveTemplate
	lifeHandlers["template_delete"] = (*Service).planTemplateDelete
	lifeHandlers["recipe_create"] = (*Service).planRecipeCreate
	lifeHandlers["recipe_update"] = (*Service).planRecipeUpdate
	lifeHandlers["recipe_delete"] = (*Service).planRecipeDelete
	lifeHandlers["warm_pool_create"] = (*Service).planPoolCreate
	lifeHandlers["warm_pool_update"] = (*Service).planPoolUpdate
	lifeHandlers["warm_pool_delete"] = (*Service).planPoolDelete
}

// --- saved templates (TPL-002) ---------------------------------------------

// reconstructionPlan rebuilds tools from verified nonsecret inputs only:
// pinned blobs written to paths, then commands, in a clean sandbox.
type reconstructionPlan struct {
	Files []struct {
		Path   string `json:"path"`
		BlobID string `json:"blob_id"`
		SHA256 string `json:"sha256"`
		Mode   string `json:"mode"`
	} `json:"files"`
	Commands [][]string `json:"commands"`
}

// engineSource resolves a template reference to what the worker restores and
// the resources it fixes (nil when the engine template decides).
func (s *Service) engineSource(owner, ref string) (string, *api.Resources, error) {
	t, ok := s.life.Templates[ref]
	if !ok {
		return ref, nil, nil
	}
	if textField(t.R, "owner_id") != owner || textField(t.R, "state") != "ready" {
		return "", nil, reject("NOT_FOUND", "template not found")
	}
	var r api.Resources
	json.Unmarshal(t.Resources, &r)
	if t.Flexible {
		return t.Capture, nil, nil
	}
	return t.Capture, &r, nil
}

func (s *Service) planSaveTemplate(a caller, in map[string]any) (lifePlan, error) {
	id := textField(in, "sandbox_id")
	sb, err := s.lifeSandboxFor(a, id)
	if err != nil {
		return lifePlan{}, err
	}
	if sb.Revision != uintField(in, "expected_revision") {
		return lifePlan{}, reject("REVISION_CONFLICT", "sandbox revision is "+strconv.FormatUint(sb.Revision, 10))
	}
	if sb.State != "ready" || s.busy[id] {
		return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox is not ready")
	}
	name := textField(in, "name")
	if name == "" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "name required")
	}
	paths := stringsField(in, "selected_paths")
	for _, p := range paths {
		if !strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "/" {
			return lifePlan{}, reject("INVALID_ARGUMENT", "selected paths must be clean absolute guest paths")
		}
		for _, at := range s.life.Attachments {
			mount := textField(at.R, "mount_path")
			if textField(at.R, "sandbox_id") == id && textField(at.R, "state") == "ready" && (p == mount || strings.HasPrefix(p, mount+"/") || strings.HasPrefix(mount, p+"/")) {
				return lifePlan{}, reject("INVALID_ARGUMENT", "attached volumes and caches are never absorbed into a template: "+mount)
			}
		}
	}
	taint := s.taintOf(id)
	planBlob := textField(in, "reconstruction_blob_id")
	var plan reconstructionPlan
	var files [][]byte
	if planBlob != "" {
		b, err := s.readyBlob(a, planBlob)
		if err != nil {
			return lifePlan{}, err
		}
		raw, err := s.readObject(textField(b.R, "sha256"))
		if err != nil {
			return lifePlan{}, err
		}
		if json.Unmarshal(raw, &plan) != nil || (len(plan.Files) == 0 && len(plan.Commands) == 0) {
			return lifePlan{}, reject("INVALID_ARGUMENT", "reconstruction plan must list files or commands")
		}
		for _, f := range plan.Files {
			fb, err := s.readyBlob(a, f.BlobID)
			if err != nil {
				return lifePlan{}, err
			}
			if textField(fb.R, "sha256") != trimDigest(f.SHA256) {
				return lifePlan{}, reject("CLEAN_REBUILD_REQUIRED", "reconstruction input "+f.BlobID+" does not match its pinned digest")
			}
			if fb.Source != "" && len(s.taintOf(fb.Source)) > 0 {
				return lifePlan{}, reject("CLEAN_REBUILD_REQUIRED", "reconstruction input "+f.BlobID+" came from tainted guest bytes")
			}
			if !strings.HasPrefix(f.Path, "/") || path.Clean(f.Path) != f.Path {
				return lifePlan{}, reject("INVALID_ARGUMENT", "plan paths must be clean absolute guest paths")
			}
			data, err := s.readObject(textField(fb.R, "sha256"))
			if err != nil {
				return lifePlan{}, err
			}
			files = append(files, data)
		}
	} else if len(taint) > 0 {
		return lifePlan{}, reject("CLEAN_REBUILD_REQUIRED", "Verified reconstruction inputs required (tainted by "+strings.Join(taint, ",")+")")
	} else if len(paths) == 0 {
		return lifePlan{}, reject("CLEAN_REBUILD_REQUIRED", "selected_paths or reconstruction_blob_id required")
	}
	base, fixed, err := s.engineSource(a.Principal, value(sb.TemplateId))
	if err != nil {
		return lifePlan{}, err
	}
	resources := sb.Resources
	if fixed != nil {
		resources = fixed
	}
	m := s.lifeResource("tp", "template", a.Principal)
	tp := textField(m, "id")
	m["state"] = "building"
	m["name"] = name
	m["default_resources"] = object(resources)
	m["minimum_resources"] = object(resources)
	m["durability"] = "local"
	m["custody_host_ids"] = []any{s.hostID()}
	how := "clean_copy"
	if planBlob != "" {
		how = "reconstructed"
	}
	m["labels"] = map[string]any{"source_sandbox_id": id, "build": how, "base_template_id": value(sb.TemplateId)}
	rawRes, _ := json.Marshal(resources)
	s.life.Templates[tp] = &lifeTemplate{R: m, Resources: rawRes, Base: value(sb.TemplateId)}
	var archiveObject string
	if len(paths) == 0 {
		return lifePlan{}, reject("CLEAN_REBUILD_REQUIRED", "selected_paths must identify the clean rebuild outputs")
	}
	var builder string
	return lifePlan{ref: lifeRef("template", tp), timeout: 20 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			var archive []byte
			if how == "clean_copy" {
				var err error
				// Only the selected paths leave the source; nothing else does.
				if archive, err = s.guestArchive(ctx, id, 1<<30, paths...); err != nil {
					return nil, err
				}
			}
			if how == "clean_copy" {
				archiveObject, err = s.putObject(archive)
				return map[string]any{"template_id": tp}, err
			}
			var err error
			builder, err = s.internalCreate(ctx, a, "lamp-template-build:"+tp, base, resources)
			if err != nil {
				return nil, err
			}
			defer s.internalRelease(context.WithoutCancel(ctx), a, builder)
			if how == "clean_copy" {
				if err := s.guestExtract(ctx, builder, archive, "/", false); err != nil {
					return nil, err
				}
			} else {
				for i, f := range plan.Files {
					if err := s.guestWriteFile(ctx, builder, f.Path, files[i]); err != nil {
						return nil, err
					}
					mode := f.Mode
					if mode == "" {
						mode = "0644"
					}
					if res, err := s.guestRun(ctx, builder, "chmod", mode, f.Path); err != nil || res.Exit != 0 {
						return nil, reject("CLEAN_REBUILD_REQUIRED", "cannot set mode of "+f.Path)
					}
				}
				for _, argv := range plan.Commands {
					res, err := s.guestRun(ctx, builder, argv...)
					if err != nil {
						return nil, err
					}
					if res.Exit != 0 {
						return nil, reject("CLEAN_REBUILD_REQUIRED", "reconstruction command failed: "+strings.Join(argv, " ")+": "+strings.TrimSpace(string(res.Stderr)))
					}
				}
			}
			if _, err := s.guestRun(ctx, builder, "sync"); err != nil {
				return nil, err
			}
			archive, err = s.guestArchive(ctx, builder, 1<<30, paths...)
			if err != nil {
				return nil, err
			}
			archiveObject, err = s.putObject(archive)
			if err != nil {
				return nil, err
			}
			return map[string]any{"template_id": tp}, nil
		},
		finish: func(_ map[string]any, err error) error {
			t := s.life.Templates[tp]
			if err != nil {
				m["state"] = "failed"
			} else {
				m["state"] = "ready"
				t.Capture = base
				t.Archive = archiveObject
				m["root_objects"] = []any{archiveObject}
			}
			editResource(m)
			return err
		}}, nil
}

func (s *Service) planTemplateDelete(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "template", textField(in, "template_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	id := textField(m, "id")
	if s.installedTemplate != nil && id == s.installedTemplate.Id {
		return lifePlan{}, reject("RESOURCE_IN_USE", "installed default template is managed by the installer")
	}
	for _, sb := range s.journal.Sandboxes {
		if value(sb.TemplateId) == id && sb.State != "deleted" && sb.State != "failed" {
			return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox "+sb.Id+" uses this template")
		}
	}
	for _, t := range s.life.Templates {
		if t.Base == id && textField(t.R, "state") != "deleted" {
			return lifePlan{}, reject("RESOURCE_IN_USE", "template "+textField(t.R, "id")+" derives from this base")
		}
	}
	for _, rc := range s.life.Recipes {
		if textField(rc.R, "template_id") == id && textField(rc.R, "state") != "deleted" {
			return lifePlan{}, reject("RESOURCE_IN_USE", "recipe "+textField(rc.R, "id")+" uses this template")
		}
	}
	m["state"] = "deleted"
	editResource(m)
	if t := s.life.Templates[id]; t.Capture != "" && !t.Engine && t.Archive == "" {
		s.life.Discards[t.Capture] = "template deleted"
	}
	return lifePlan{ref: lifeRef("template", id), result: map[string]any{}}, nil
}

// internalCreate makes a sandbox through the ordinary create path and waits
// until it is ready.
func (s *Service) internalCreate(ctx context.Context, a caller, key, template string, resources *api.Resources) (string, error) {
	body := map[string]any{"template_id": template}
	if resources != nil {
		body["resources"] = resources
	}
	code, out := s.internalHTTP(a, "POST", "/v1/sandboxes", map[string]string{"Idempotency-Key": key}, body)
	if code >= 400 {
		e, _ := out["error"].(map[string]any)
		return "", reject(textField(e, "code"), "sandbox create: "+textField(e, "message"))
	}
	opID := textField(out, "operation_id")
	refs, _ := out["resource_ids"].([]any)
	if len(refs) == 0 {
		return "", reject("DATA_UNAVAILABLE", "sandbox create returned no sandbox")
	}
	id := textField(refs[0].(map[string]any), "id")
	if err := s.lifeWait(ctx, opID); err != nil {
		return id, err
	}
	return id, nil
}

// internalRelease deletes a Lamp-made sandbox (stop and release).
func (s *Service) internalRelease(ctx context.Context, a caller, id string) {
	s.mu.Lock()
	sb := s.journal.Sandboxes[id]
	s.mu.Unlock()
	if sb.Id == "" || sb.State == "deleted" {
		return
	}
	code, out := s.internalHTTP(a, "DELETE", "/v1/sandboxes/"+id, map[string]string{"Idempotency-Key": "lamp-release:" + id, "If-Match": strconv.FormatUint(sb.Revision, 10)}, nil)
	if code < 400 {
		s.lifeWait(ctx, textField(out, "operation_id"))
	}
}

// --- recipes (TPL-003) -----------------------------------------------------

var recipeUnsupported = map[string]string{"harness_id": "the harness catalogue", "kit_ids": "kits", "devices": "devices",
	"network_policy_id": "network policy", "network_plans": "network policy", "connection_ids": "connections",
	"settings_source_ids": "settings sources", "budget_id": "budgets"}

func recipeDigest(m map[string]any) string {
	return digest(map[string]any{"template_id": m["template_id"], "resources": m["resources"], "lifecycle": m["lifecycle"], "auth_mode": m["auth_mode"]})
}

func (s *Service) recipeFields(m, in map[string]any) error {
	for k := range in {
		switch k {
		case "recipe_id", "name", "template_id", "resources", "lifecycle", "auth_mode", "labels", "idempotency_key", "expected_revision":
		default:
			if why, ok := recipeUnsupported[k]; ok {
				if raw, isList := in[k].([]any); isList && len(raw) == 0 {
					continue
				}
				return reject("INVALID_ARGUMENT", k+" needs "+why+", which this host has not installed")
			}
			return reject("INVALID_ARGUMENT", k+" is not a recipe field")
		}
	}
	if v := textField(in, "auth_mode"); v != "" && v != "none" {
		return reject("INVALID_ARGUMENT", "auth_mode "+v+" needs credential custody, which this host has not installed")
	}
	for _, k := range []string{"name", "template_id", "resources", "lifecycle", "auth_mode", "labels"} {
		if v, ok := in[k]; ok {
			m[k] = v
		}
	}
	if textField(m, "name") == "" || textField(m, "template_id") == "" {
		return reject("INVALID_ARGUMENT", "name and template_id required")
	}
	if t, ok := s.life.Templates[textField(m, "template_id")]; ok && !t.Flexible {
		if textField(t.R, "owner_id") != textField(m, "owner_id") || textField(t.R, "state") != "ready" {
			return reject("NOT_FOUND", "template not found")
		}
		var fixed api.Resources
		json.Unmarshal(t.Resources, &fixed)
		if _, ok := m["resources"]; !ok {
			m["resources"] = object(fixed)
		} else if digest(m["resources"]) != digest(object(fixed)) {
			return reject("INVALID_ARGUMENT", "a saved template fixes its resources")
		}
	}
	return nil
}

func (s *Service) planRecipeCreate(a caller, in map[string]any) (lifePlan, error) {
	m := s.lifeResource("rc", "recipe", a.Principal)
	if err := s.recipeFields(m, in); err != nil {
		return lifePlan{}, err
	}
	nameKey := a.Principal + "\n" + textField(m, "name")
	if s.life.RecipeNames[nameKey] != "" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "recipe name exists; update it to move the name")
	}
	d := recipeDigest(m)
	m["digest"] = d
	id := textField(m, "id")
	s.life.Recipes[id] = &lifeRecipe{R: m, Digest: d}
	s.life.RecipeNames[nameKey] = id
	return lifePlan{ref: lifeRef("recipe", id), result: map[string]any{"recipe_id": id}}, nil
}

// recipe_update makes a new immutable version and moves the name to it with
// compare-and-swap: only an update of the current version can win.
func (s *Service) planRecipeUpdate(a caller, in map[string]any) (lifePlan, error) {
	old, err := s.ownedOrMissing(a, "recipe", textField(in, "recipe_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(old, in); err != nil {
		return lifePlan{}, err
	}
	oldKey := a.Principal + "\n" + textField(old, "name")
	if s.life.RecipeNames[oldKey] != textField(old, "id") {
		return lifePlan{}, reject("REVISION_CONFLICT", "a newer version of this recipe exists")
	}
	m := s.lifeResource("rc", "recipe", a.Principal)
	for k, v := range old {
		switch k {
		case "id", "created_at", "updated_at", "revision":
		default:
			m[k] = v
		}
	}
	m["revision"] = jsonNumber(uintField(old, "revision") + 1)
	if err := s.recipeFields(m, in); err != nil {
		return lifePlan{}, err
	}
	newKey := a.Principal + "\n" + textField(m, "name")
	if newKey != oldKey && s.life.RecipeNames[newKey] != "" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "recipe name exists")
	}
	d := recipeDigest(m)
	m["digest"] = d
	id := textField(m, "id")
	s.life.Recipes[id] = &lifeRecipe{R: m, Digest: d}
	delete(s.life.RecipeNames, oldKey)
	s.life.RecipeNames[newKey] = id
	return lifePlan{ref: lifeRef("recipe", id), result: map[string]any{"recipe_id": id}}, nil
}

func (s *Service) planRecipeDelete(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "recipe", textField(in, "recipe_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	id := textField(m, "id")
	for _, sb := range s.journal.Sandboxes {
		if value(sb.RecipeId) == id && sb.State != "deleted" && sb.State != "failed" {
			return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox "+sb.Id+" uses this recipe version")
		}
	}
	for _, p := range s.life.WarmPools {
		if p.Recipe == id && textField(p.R, "state") != "failed" {
			return lifePlan{}, reject("RESOURCE_IN_USE", "warm pool "+textField(p.R, "id")+" uses this recipe version")
		}
	}
	m["state"] = "deleted"
	editResource(m)
	key := a.Principal + "\n" + textField(m, "name")
	if s.life.RecipeNames[key] == id {
		delete(s.life.RecipeNames, key)
	}
	return lifePlan{ref: lifeRef("recipe", id), result: map[string]any{}}, nil
}

// --- sandbox create from a recipe or saved template ------------------------

// lifeCreate handles sandbox_create inputs this subsystem owns: recipe_id
// (claiming a matching warm-pool entry when one is ready), a saved template
// and lifecycle overrides. Returns false for an ordinary create.
func (s *Service) lifeCreate(w http.ResponseWriter, r *http.Request, requestID string) bool {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, requestID, "INVALID_ARGUMENT", "body exceeds limit", nil)
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	body, err := surface.Decode(data)
	if err != nil {
		writeError(w, requestID, "INVALID_ARGUMENT", err.Error(), nil)
		return true
	}
	if _, exists := body["idempotency_key"]; exists {
		writeError(w, requestID, "INVALID_ARGUMENT", "idempotency_key: use the Idempotency-Key header", nil)
		return true
	}
	validation := map[string]any{}
	for k, v := range body {
		validation[k] = v
	}
	validation["idempotency_key"] = r.Header.Get("Idempotency-Key")
	if textField(body, "template_id") != "" && textField(body, "recipe_id") != "" {
		writeError(w, requestID, "INVALID_ARGUMENT", "give template_id or recipe_id, not both", nil)
		return true
	}
	if err := surface.Validate("SandboxCreateInput", validation); err != nil {
		writeError(w, requestID, "INVALID_ARGUMENT", err.Error(), nil)
		return true
	}
	recipeID, template := textField(body, "recipe_id"), textField(body, "template_id")
	lifecycle, hasLifecycle := body["lifecycle"]
	a := requestCaller(r)
	s.readLock()
	if recipeID == "" && body["template_id"] == nil && s.installedTemplate != nil {
		template = s.installedTemplate.Id
	}
	if _, ok := s.life.Templates[template]; !ok && template != "" {
		if found := s.lifeFind(a, "template", template); found != nil {
			template = textField(found, "id")
			body["template_id"] = template
		}
	}
	_, saved := s.life.Templates[template]
	if recipeID == "" && !saved {
		s.mu.Unlock()
		return false
	}
	fail := func(code, message string) bool {
		s.mu.Unlock()
		writeError(w, requestID, code, message, nil)
		return true
	}
	var recipe map[string]any
	if recipeID != "" {
		recipe = s.lifeFind(a, "recipe", recipeID)
		if recipe == nil || textField(recipe, "state") != "active" {
			return fail("NOT_FOUND", "recipe not found")
		}
		recipeID = textField(recipe, "id")
		if template != "" && template != textField(recipe, "template_id") {
			return fail("INVALID_ARGUMENT", "template_id differs from the recipe")
		}
		template = textField(recipe, "template_id")
		if _, ok := body["resources"]; !ok && recipe["resources"] != nil {
			body["resources"] = recipe["resources"]
		}
		if !hasLifecycle && recipe["lifecycle"] != nil {
			lifecycle, hasLifecycle = recipe["lifecycle"], true
		}
		if hasLifecycle {
			var l api.Lifecycle
			raw, _ := json.Marshal(lifecycle)
			if json.Unmarshal(raw, &l) != nil {
				return fail("INVALID_ARGUMENT", "invalid lifecycle")
			}
			if err := s.validateLifecycle(a, &l); err != nil {
				e := err.(*coreError)
				return fail(e.code, e.message)
			}
		}
		// A warm-pool hit hands out one never-used entry, exactly once. A
		// retried claim replays its original operation without claiming again.
		key := r.Header.Get("Idempotency-Key")
		in := map[string]any{"recipe_id": recipeID, "idempotency_key": key}
		rawIn, _ := json.Marshal(in)
		old, retried := s.keys[a.Principal+"\nsandbox_create\n"+key]
		if retried && s.journal.Operations[old].Hash == bearerHash("sandbox_create\n"+string(rawIn)) {
			s.mu.Unlock()
			s.lifeRespond(w, r, requestID, old, true, 0)
			return true
		}
		// A retried miss takes the same deterministic rewrite below.
		claimed := ""
		if !retried && body["budget_id"] == nil && body["task_id"] == nil && sameJSON(body["resources"], recipe["resources"]) {
			claimed = s.claimPoolEntry(a, recipeID)
		}
		if claimed != "" {
			s.mu.Unlock()
			opID, replayed, err := s.lifeStartWith(a, "sandbox_create", in, func(s *Service, a caller, in map[string]any) (lifePlan, error) {
				s.finishClaim(claimed, recipeID, lifecycle)
				return lifePlan{ref: lifeRef("sandbox", claimed), ids: []string{claimed}, result: map[string]any{"sandbox_id": claimed}}, nil
			})
			if err != nil {
				e := err.(*coreError)
				writeError(w, requestID, e.code, e.message, nil)
				return true
			}
			s.lifeRespond(w, r, requestID, opID, replayed, 0)
			return true
		}
	}
	source, fixed, err := s.engineSource(a.Principal, template)
	if err != nil {
		return fail("NOT_FOUND", "template not found")
	}
	if _, explicit := body["resources"]; !explicit {
		if t := s.life.Templates[template]; t != nil && len(t.Resources) > 0 {
			var defaults api.Resources
			if json.Unmarshal(t.Resources, &defaults) == nil {
				body["resources"] = defaults
			}
		}
	}
	s.mu.Unlock()
	if fixed != nil {
		if res, ok := body["resources"]; ok && digest(res) != digest(object(fixed)) {
			writeError(w, requestID, "INVALID_ARGUMENT", "a saved template fixes its resources", nil)
			return true
		}
		body["resources"] = fixed
	}
	var parsed api.Lifecycle
	if hasLifecycle {
		raw, _ := json.Marshal(lifecycle)
		if json.Unmarshal(raw, &parsed) != nil {
			writeError(w, requestID, "INVALID_ARGUMENT", "invalid lifecycle", nil)
			return true
		}
	}
	delete(body, "recipe_id")
	if hasLifecycle {
		body["lifecycle"] = lifecycle
	}
	body["template_id"] = source
	raw, _ := json.Marshal(body)
	inner := r.Clone(context.WithValue(r.Context(), templateCreateKey{}, template))
	inner.Body = io.NopCloser(bytes.NewReader(raw))
	inner.ContentLength = int64(len(raw))
	rec := httptest.NewRecorder()
	s.engineHTTP(rec, inner)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if refs, ok := out["resource_ids"].([]any); ok && len(refs) > 0 && rec.Code < 400 {
		id := textField(refs[0].(map[string]any), "id")
		opID := textField(out, "operation_id")
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			// Patch the declared inputs once the create's own writes end.
			s.mu.Lock()
			done := s.done[opID]
			s.mu.Unlock()
			if done != nil {
				<-done
			}
			s.readLock()
			defer s.mu.Unlock()
			sb, ok := s.journal.Sandboxes[id]
			if !ok || s.broken {
				return
			}
			if template != source {
				sb.TemplateId = pointer(template)
			}
			if recipeID != "" {
				sb.RecipeId = pointer(recipeID)
				// Recipes on this host are signed out (auth_mode none).
				sb.AuthMode = pointer(api.SandboxAuthModeNone)
			}
			s.journal.Sandboxes[id] = sb
			if s.save(id) != nil {
				s.broken = true
			}
		}()
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	w.Write(rec.Body.Bytes())
	return true
}

// --- warm pools (LIF-005) --------------------------------------------------

func (s *Service) planPoolCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := onlyFields(in, "recipe_id target_size max_size match_digest"); err != nil {
		return lifePlan{}, err
	}
	rc := s.lifeFind(a, "recipe", textField(in, "recipe_id"))
	if rc == nil || textField(rc, "state") != "active" {
		return lifePlan{}, reject("NOT_FOUND", "recipe not found")
	}
	target, max := uintField(in, "target_size"), uintField(in, "max_size")
	if _, ok := in["max_size"]; !ok {
		max = target
	}
	if target > max {
		return lifePlan{}, reject("INVALID_ARGUMENT", "target_size exceeds max_size")
	}
	if max > 8 {
		return lifePlan{}, reject("NO_CAPACITY", "a host holds at most 8 warm entries")
	}
	rcID := textField(rc, "id")
	d := s.life.Recipes[rcID].Digest
	if want := textField(in, "match_digest"); want != "" && want != d {
		return lifePlan{}, reject("INVALID_ARGUMENT", "match_digest differs from the recipe's immutable digest")
	}
	m := s.lifeResource("wp", "warm_pool", a.Principal)
	m["recipe_id"] = rcID
	m["target_size"] = jsonNumber(target)
	m["max_size"] = jsonNumber(max)
	m["match_digest"] = d
	applyLabels(m, in)
	s.life.WarmPools[textField(m, "id")] = &lifePool{R: m, Recipe: rcID, Digest: d, Entries: map[string]string{}}
	return lifePlan{ref: lifeRef("warm_pool", textField(m, "id")), result: map[string]any{"warm_pool_id": textField(m, "id")}}, nil
}

func (s *Service) planPoolUpdate(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "warm_pool", textField(in, "warm_pool_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if err := onlyFields(in, "warm_pool_id target_size max_size"); err != nil {
		return lifePlan{}, err
	}
	target, max := uintField(m, "target_size"), uintField(m, "max_size")
	if _, ok := in["target_size"]; ok {
		target = uintField(in, "target_size")
	}
	if _, ok := in["max_size"]; ok {
		max = uintField(in, "max_size")
	}
	if target > max || max > 8 {
		return lifePlan{}, reject("INVALID_ARGUMENT", "target_size ≤ max_size ≤ 8")
	}
	m["target_size"], m["max_size"] = jsonNumber(target), jsonNumber(max)
	editResource(m)
	return lifePlan{ref: lifeRef("warm_pool", textField(m, "id")), result: map[string]any{}}, nil
}

func (s *Service) planPoolDelete(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "warm_pool", textField(in, "warm_pool_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	p := s.life.WarmPools[textField(m, "id")]
	// Paused while its entries are released, then removed.
	m["state"] = "paused"
	m["target_size"] = jsonNumber(0)
	editResource(m)
	var entries []string
	for id := range p.Entries {
		entries = append(entries, id)
	}
	return lifePlan{ref: lifeRef("warm_pool", textField(m, "id")), timeout: 5 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			for _, id := range entries {
				s.internalRelease(ctx, a, id)
			}
			return map[string]any{}, nil
		},
		finish: func(_ map[string]any, err error) error {
			for _, id := range entries {
				delete(p.Entries, id)
				if l := s.life.Sandboxes[id]; l != nil {
					l.Pool = ""
				}
			}
			if err == nil {
				delete(s.life.WarmPools, textField(m, "id"))
			}
			editResource(m)
			return err
		}}, nil
}

// claimPoolEntry takes one ready, never-used entry. Under mu.
func (s *Service) claimPoolEntry(a caller, recipeID string) string {
	for _, p := range s.life.WarmPools {
		if textField(p.R, "owner_id") != a.Principal || p.Recipe != recipeID || textField(p.R, "state") != "active" || p.Digest != s.life.Recipes[recipeID].Digest {
			continue
		}
		for id, state := range p.Entries {
			l := s.life.Sandboxes[id]
			sb := s.journal.Sandboxes[id]
			if state != "ready" || sb.State != "ready" || l == nil || l.Used || s.busy[id] || s.starting[id] > 0 {
				continue
			}
			delete(p.Entries, id)
			l.Pool = ""
			l.Used = true
			return id
		}
	}
	return ""
}

func (s *Service) finishClaim(id, recipeID string, lifecycle any) {
	sb := s.journal.Sandboxes[id]
	sb.RecipeId = pointer(recipeID)
	sb.AuthMode = pointer(api.SandboxAuthModeNone)
	if lifecycle != nil {
		var parsed api.Lifecycle
		raw, _ := json.Marshal(lifecycle)
		if json.Unmarshal(raw, &parsed) == nil {
			sb.Lifecycle = &parsed
		}
	} else {
		sb.Lifecycle = nil
	}
	sb.Revision++
	sb.UpdatedAt = timestamp()
	s.journal.Sandboxes[id] = sb
	s.lifeSB(id).Activity = time.Now()
}

// refillPools creates at most one entry per pool per tick, never beyond max.
func (s *Service) refillPools(ctx context.Context) {
	type fill struct {
		pool, owner, template string
		resources             *api.Resources
		n                     int
	}
	var fills []fill
	s.readLock()
	for id, p := range s.life.WarmPools {
		if textField(p.R, "state") != "active" {
			continue
		}
		live := 0
		for sb, state := range p.Entries {
			current := s.journal.Sandboxes[sb]
			if current.State == "failed" || current.State == "deleted" || current.State == "unavailable" {
				delete(p.Entries, sb)
				if l := s.life.Sandboxes[sb]; l != nil {
					l.Pool = ""
				}
				continue
			}
			if state == "filling" && current.State == "ready" {
				p.Entries[sb] = "ready"
			}
			live++
		}
		if uint64(live) >= uintField(p.R, "target_size") || uint64(live) >= uintField(p.R, "max_size") {
			continue
		}
		rc := s.life.Recipes[p.Recipe]
		var res *api.Resources
		if rc.R["resources"] != nil {
			res = &api.Resources{}
			assign(rc.R["resources"].(map[string]any), res)
		}
		_, fixed, err := s.engineSource(textField(p.R, "owner_id"), textField(rc.R, "template_id"))
		if err != nil {
			continue
		}
		if fixed != nil {
			res = fixed
		}
		fills = append(fills, fill{pool: id, owner: textField(p.R, "owner_id"), template: textField(rc.R, "template_id"), resources: res, n: len(p.Entries)})
	}
	s.mu.Unlock()
	for _, f := range fills {
		a := caller{Principal: f.owner}
		body := map[string]any{"template_id": f.template}
		if f.resources != nil {
			body["resources"] = f.resources
		}
		code, out := s.internalHTTP(a, "POST", "/v1/sandboxes", map[string]string{"Idempotency-Key": "lamp-pool:" + f.pool + ":" + newID("e")}, body)
		refs, _ := out["resource_ids"].([]any)
		if code >= 400 || len(refs) == 0 {
			continue
		}
		id := textField(refs[0].(map[string]any), "id")
		s.readLock()
		p := s.life.WarmPools[f.pool]
		p.Entries[id] = "filling"
		l := s.lifeSB(id)
		l.Pool = f.pool
		if s.saveLife() != nil {
			s.broken = true
		}
		s.mu.Unlock()
		opID := textField(out, "operation_id")
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.lifeWait(s.context(), opID)
			s.readLock()
			defer s.mu.Unlock()
			sb := s.journal.Sandboxes[id]
			if sb.State == "ready" {
				sb.WarmPoolId = pointer(f.pool)
				sb.RecipeId = pointer(s.life.WarmPools[f.pool].Recipe)
				sb.AuthMode = pointer(api.SandboxAuthModeNone)
				sb.Lifecycle = &api.Lifecycle{IdleAction: pointer(api.LifecycleIdleActionKeep)}
				s.journal.Sandboxes[id] = sb
				if p := s.life.WarmPools[f.pool]; p.Entries[id] == "filling" {
					p.Entries[id] = "ready"
				}
				if s.saveLife() != nil || s.save(id) != nil {
					s.broken = true
				}
			}
		}()
	}
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
