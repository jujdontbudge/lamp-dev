package control

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// The catalogue describes each official harness integration route and its
// actual vendor limitations. It is a description, not a certification.
//
//go:embed harness_catalogue.json
var harnessCatalogueJSON []byte

type catalogueEntry struct {
	ID                string            `json:"id"`
	Kind              string            `json:"kind"`
	Name              string            `json:"name"`
	AuthModes         []string          `json:"auth_modes"`
	Routes            map[string]string `json:"routes"`
	PermissionSources []string          `json:"permission_sources"`
	Endpoints         []string          `json:"endpoints"`
	Trust             string            `json:"trust"`
	Continuation      string            `json:"continuation"`
	NativeSubagents   string            `json:"native_subagents"`
	GuestSupport      []string          `json:"guest_support"`
	Limitations       []string          `json:"limitations"`
}

var harnessCatalogue = func() []catalogueEntry {
	var entries []catalogueEntry
	if err := json.Unmarshal(harnessCatalogueJSON, &entries); err != nil {
		panic(err)
	}
	return entries
}()

func catalogueByKind(kind string) *catalogueEntry {
	for i := range harnessCatalogue {
		if harnessCatalogue[i].Kind == kind {
			return &harnessCatalogue[i]
		}
	}
	return nil
}

// binaryDigest identifies the exact enrolled official binary (after symlinks).
func binaryDigest(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, 1<<30)); err != nil {
		return ""
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// describeHarness fills the public Harness resource from the catalogue.
func describeHarness(m map[string]any, entry *catalogueEntry, modes []string) {
	labels := map[string]any{"lamp.catalogue_id": entry.ID, "lamp.name": entry.Name, "lamp.continuation": entry.Continuation, "lamp.native_subagents": entry.NativeSubagents, "lamp.trust": entry.Trust, "lamp.endpoints": strings.Join(entry.Endpoints, ", ")}
	for i, limit := range entry.Limitations {
		labels["lamp.limitation."+strconv.Itoa(i+1)] = limit
	}
	for mode, route := range entry.Routes {
		labels["lamp.route."+mode] = route
	}
	sources := []string{}
	for _, url := range entry.PermissionSources {
		sources = append(sources, url+" (checked 2026-11-11)")
	}
	m["labels"], m["plugin_id"], m["auth_modes"], m["permission_sources"], m["guest_support"] = labels, entry.ID, modes, sources, entry.GuestSupport
	m["hooks"] = []map[string]any{{"kind": "continuation"}, {"kind": "capture"}, {"kind": "spawn"}, {"kind": "refresh"}}
	m["certification"], m["evidence_ref"] = "described", "docs/development/T25-evidence.md#"+entry.ID
}

func (s *Service) harnessResources(a caller) []map[string]any {
	c := s.journal.Control
	items := []map[string]any{}
	enrolled := map[string]bool{}
	for id, h := range s.sessions.Harnesses {
		if h.OwnerID != a.Principal {
			continue
		}
		m := newResource("harness", h.OwnerID)
		m["id"], m["auth_modes"], m["version"] = id, h.AuthModes, "enrolled"
		if h.Native != nil {
			if entry := catalogueByKind(h.Native.Kind); entry != nil {
				describeHarness(m, entry, h.AuthModes)
				enrolled[entry.ID] = true
			}
			if digest := binaryDigest(h.Native.Binary); digest != "" {
				m["binary_digest"] = digest
			}
		}
		m["created_at"], m["updated_at"] = c.Fleet.CreatedAt, c.Fleet.CreatedAt
		items = append(items, m)
	}
	// Catalogue routes not yet enrolled by this owner are listed as disabled
	// so people can see which harness they could bring and on which route.
	for i := range harnessCatalogue {
		entry := &harnessCatalogue[i]
		if enrolled[entry.ID] {
			continue
		}
		if _, taken := s.sessions.Harnesses[entry.ID]; taken {
			continue
		}
		m := newResource("harness", a.Principal)
		m["id"], m["state"], m["version"] = entry.ID, "disabled", "catalogue"
		describeHarness(m, entry, entry.AuthModes)
		m["created_at"], m["updated_at"] = c.Fleet.CreatedAt, c.Fleet.CreatedAt
		items = append(items, m)
	}
	sort.Slice(items, func(i, j int) bool { return textField(items[i], "id") < textField(items[j], "id") })
	return items
}
