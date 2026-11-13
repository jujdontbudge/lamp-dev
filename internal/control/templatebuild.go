package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

type templateCreateKey struct{}

func init() {
	lifeHandlers["template_create"] = (*Service).planTemplateCreate
	lifeHandlers["template_build"] = (*Service).planTemplateBuild
}

func (s *Service) templateCall(ctx context.Context, action, definition string) (*pb.Receipt, error) {
	r := s.workerRequest(ctx, "template", "sb_catalog", 1)
	r.Command = &pb.Request_Template{Template: &pb.Template{Action: action, Definition: definition}}
	return s.worker.call(ctx, r)
}

// Refresh outside the metadata lock: the worker is the authority for installed
// engine templates. Discovered records are private to this daemon's owner.
func (s *Service) refreshTemplates(ctx context.Context) error {
	reply, err := s.templateCall(ctx, "list", "")
	if err != nil {
		return err
	}
	var items []struct {
		ID        string        `json:"id"`
		Name      string        `json:"name"`
		Arch      string        `json:"arch"`
		GuestOS   string        `json:"guest_os"`
		Resources api.Resources `json:"resources"`
		Minimum   api.Resources `json:"minimum_resources"`
		Fixed     bool          `json:"fixed_resources"`
		SeedRef   string        `json:"seed_ref"`
	}
	if err := json.Unmarshal([]byte(reply.Detail["items"]), &items); err != nil {
		return err
	}
	s.readLock()
	defer s.mu.Unlock()
	for _, item := range items {
		if s.life.Templates[item.ID] != nil {
			continue
		}
		item.Resources.PidsMax = 4096
		item.Minimum.PidsMax = 1
		m := s.lifeResource("tp", "template", s.journal.Control.OwnerID)
		m["id"], m["name"], m["state"], m["arch"], m["guest_os"] = item.ID, item.Name, "ready", item.Arch, item.GuestOS
		m["default_resources"], m["minimum_resources"] = object(item.Resources), object(item.Minimum)
		m["durability"] = "local"
		m["labels"] = map[string]any{"seed_ref": item.SeedRef, "fixed_resources": fmt.Sprint(item.Fixed)}
		raw, _ := json.Marshal(item.Resources)
		s.life.Templates[item.ID] = &lifeTemplate{R: m, Capture: item.ID, Resources: raw, Flexible: !item.Fixed, Engine: true}
	}
	return nil
}

func (s *Service) planTemplateCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := onlyFields(in, "name definition_blob_id"); err != nil {
		return lifePlan{}, err
	}
	if textField(in, "name") == "" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "name required")
	}
	blob, err := s.readyBlob(a, textField(in, "definition_blob_id"))
	if err != nil {
		return lifePlan{}, err
	}
	m := s.lifeResource("tp", "template", a.Principal)
	m["name"], m["definition_blob_id"], m["state"] = in["name"], textField(blob.R, "id"), "building"
	applyLabels(m, in)
	id := textField(m, "id")
	s.life.Templates[id] = &lifeTemplate{R: m}
	return lifePlan{ref: lifeRef("template", id), result: map[string]any{"template_id": id}}, nil
}

func (s *Service) planTemplateBuild(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "template", textField(in, "template_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if s.life.Templates[textField(m, "id")].Building || textField(m, "state") == "ready" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "template is already building or ready")
	}
	blob, err := s.readyBlob(a, textField(in, "definition_blob_id"))
	if err != nil {
		return lifePlan{}, err
	}
	raw, err := s.readObject(textField(blob.R, "sha256"))
	if err != nil {
		return lifePlan{}, err
	}
	if len(raw) > 256<<10 {
		return lifePlan{}, reject("INVALID_ARGUMENT", "template definition exceeds 256 KiB")
	}
	// Refuse container inputs before an accepted build operation.
	first := strings.TrimSpace(string(raw))
	if strings.HasPrefix(strings.ToUpper(first), "FROM ") || strings.HasPrefix(first, "docker://") || strings.HasPrefix(first, "oci://") {
		return lifePlan{}, reject("INVALID_ARGUMENT", "Dockerfile/OCI definitions are unsupported; Docker is only available as a kit")
	}
	id := textField(m, "id")
	s.life.Templates[id].Building = true
	m["state"], m["definition_blob_id"] = "building", textField(blob.R, "id")
	m["labels"] = map[string]any{"build_log": "validating definition; preparing confined build VM"}
	editResource(m)
	var receipt *pb.Receipt
	return lifePlan{ref: lifeRef("template", id), timeout: 30 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			progress, stop := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() {
				defer close(done)
				ticker := time.NewTicker(500 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-progress.Done():
						return
					case <-ticker.C:
					}
					reply, err := s.templateCall(progress, "log", string(raw))
					if err != nil {
						continue
					}
					if log := reply.Detail["log"]; log != "" {
						s.readLock()
						m["labels"] = map[string]any{"build_log": log}
						s.mu.Unlock()
					}
				}
			}()
			var err error
			receipt, err = s.templateCall(ctx, "build", string(raw))
			stop()
			<-done
			return map[string]any{"template_id": id}, err
		},
		finish: func(_ map[string]any, buildErr error) error {
			s.life.Templates[id].Building = false
			if buildErr != nil {
				m["state"] = "failed"
				m["labels"] = map[string]any{"build_log": buildErr.Error()}
				editResource(m)
				return buildErr
			}
			t := s.life.Templates[id]
			t.Capture = receipt.CommittedHandleIds[0]
			t.Flexible = true
			if strings.Contains(receipt.Detail["fixed_resources"], "true") {
				t.Flexible = false
			}
			if raw := receipt.Detail["resources"]; raw != "" {
				var res api.Resources
				json.Unmarshal([]byte(raw), &res)
				res.PidsMax = 4096
				t.Resources, _ = json.Marshal(res)
				m["default_resources"] = object(res)
				minimum := res
				if t.Flexible {
					minimum.Vcpus, minimum.MemoryMib, minimum.PidsMax = 1, 256, 1
				}
				m["minimum_resources"] = object(minimum)
			}
			// Resolve resources and architecture from the engine's signed inventory.
			m["state"] = "ready"
			m["arch"], m["guest_os"] = receipt.Detail["arch"], receipt.Detail["guest_os"]
			m["labels"] = map[string]any{"build_log": receipt.Detail["log"], "engine_template_id": t.Capture}
			editResource(m)
			return nil
		}}, nil
}

func (s *Service) templateInventoryHTTP(w http.ResponseWriter, r *http.Request, id string, op api.OperationDescriptor) bool {
	if op.Name != "template_list" && op.Name != "template_get" && op.Name != "sandbox_create" {
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.refreshTemplates(ctx); err != nil {
		// Existing durable templates remain usable if discovery is temporarily down.
		s.mu.Lock()
		count := len(s.life.Templates)
		s.mu.Unlock()
		if count == 0 && op.Name != "sandbox_create" {
			writeError(w, id, "HOST_UNAVAILABLE", err.Error(), nil)
			return false
		}
	}
	return true
}
