package broker

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type settingsChange struct {
	ID         string            `json:"id"`
	Source     string            `json:"source_id"`
	Base       string            `json:"base_revision"`
	Files      map[string]string `json:"files"`
	Before     map[string]string `json:"before"`
	Sandbox    string            `json:"sandbox_id"`
	Generation uint64            `json:"generation"`
	GrantID    string            `json:"-"`
	Expires    time.Time         `json:"expires_at"`
}

func openSource(s SettingsSource) (*os.Root, error) {
	info, err := os.Lstat(s.Root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("SCOPE_DENIED")
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return nil, errors.New("SCOPE_DENIED")
	}
	return root, nil
}
func regular(root *os.Root, path string) bool {
	parts := strings.Split(path, string(filepath.Separator))
	for i := range parts {
		st, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if i == len(parts)-1 && (!st.Mode().IsRegular() || st.Size() > 65536) {
			return false
		}
	}
	return true
}
func settingsSafe(s string) bool {
	upper := strings.ToUpper(s)
	return !strings.Contains(upper, "PRIVATE KEY") && !strings.Contains(s, "\x00") && !strings.Contains(s, "aws_secret_access_key")
}
func snapshot(root *os.Root, s SettingsSource) (map[string]string, error) {
	files := map[string]string{}
	for _, p := range s.Paths {
		if !regular(root, p) {
			return nil, errors.New("SCOPE_DENIED")
		}
		f, err := root.Open(p)
		if err != nil {
			return nil, errors.New("SCOPE_DENIED")
		}
		raw, err := io.ReadAll(io.LimitReader(f, 65537))
		_ = f.Close()
		if err != nil || len(raw) > 65536 || !settingsSafe(string(raw)) {
			return nil, errors.New("OUTPUT_BLOCKED")
		}
		files[p] = string(raw)
	}
	return files, nil
}
func (b *Broker) taskSettings(w http.ResponseWriter, r *http.Request, g Grant) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/settings/")
	id, action, ok := strings.Cut(path, "/")
	if !ok || !contains(g.Settings, id) {
		fail(w, "SCOPE_DENIED")
		return
	}
	source, ok := b.config.Settings[id]
	if !ok {
		fail(w, "SCOPE_DENIED")
		return
	}
	root, err := openSource(source)
	if err != nil {
		fail(w, err.Error())
		return
	}
	defer root.Close()
	files, err := snapshot(root, source)
	if err != nil {
		fail(w, err.Error())
		return
	}
	switch action {
	case "copy":
		var empty struct{}
		if decode(r, &empty) != nil {
			fail(w, "INVALID_ARGUMENT")
			return
		}
		send(w, 200, map[string]any{"source_id": id, "base_revision": digest(files), "files": files})
	case "propose":
		var in struct {
			Base  string            `json:"base_revision"`
			Files map[string]string `json:"files"`
		}
		if decode(r, &in) != nil || len(in.Files) == 0 || len(in.Files) > len(source.Paths) {
			fail(w, "INVALID_ARGUMENT")
			return
		}
		if in.Base != digest(files) {
			fail(w, "REVISION_CONFLICT")
			return
		}
		for p, data := range in.Files {
			if !contains(source.Paths, p) || !safePath(p) || len(data) > 65536 || !settingsSafe(data) {
				fail(w, "SCOPE_DENIED")
				return
			}
		}
		if len(b.changes) >= 128 {
			fail(w, "SCOPE_DENIED")
			return
		}
		change := &settingsChange{ID: randomID(), Source: id, Base: in.Base, Files: in.Files, Before: files, Sandbox: g.Sandbox, Generation: g.Generation, GrantID: g.id, Expires: g.expires}
		b.changes[change.ID] = change
		b.log("settings", g.Sandbox, "proposed")
		send(w, 200, map[string]string{"settings_change_id": change.ID, "state": "proposed", "base_revision": in.Base})
	default:
		fail(w, "SCOPE_DENIED")
	}
}
func (b *Broker) ownerSettings(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/owner/settings/")
	change, ok := b.changes[id]
	if !ok {
		fail(w, "SCOPE_DENIED")
		return
	}
	if r.Method == "GET" {
		send(w, 200, change)
		return
	}
	if r.Method != "POST" {
		fail(w, "SCOPE_DENIED")
		return
	}
	var in struct {
		Base  string   `json:"base_revision"`
		Paths []string `json:"selected_paths"`
	}
	if decode(r, &in) != nil || len(in.Paths) == 0 || in.Base != change.Base {
		fail(w, "INVALID_ARGUMENT")
		return
	}
	live := false
	for _, g := range b.grants {
		if g.id == change.GrantID {
			live = true
		}
	}
	if !live || !time.Now().Before(change.Expires) {
		fail(w, "SCOPE_DENIED")
		return
	}
	selected := map[string]bool{}
	for _, p := range in.Paths {
		if _, ok := change.Files[p]; !ok || selected[p] {
			fail(w, "SCOPE_DENIED")
			return
		}
		selected[p] = true
	}
	source := b.config.Settings[change.Source]
	root, err := openSource(source)
	if err != nil {
		fail(w, err.Error())
		return
	}
	defer root.Close()
	files, err := snapshot(root, source)
	if err != nil {
		fail(w, err.Error())
		return
	}
	if digest(files) != change.Base {
		fail(w, "REVISION_CONFLICT")
		return
	}
	// Stage only selected paths. Rename replaces hard links instead of modifying
	// their other names. os.Root confines all operations beneath the owner root.
	staged := map[string]string{}
	defer func() {
		for _, p := range staged {
			_ = root.Remove(p)
		}
	}()
	for p := range selected {
		temp := filepath.Join(filepath.Dir(p), ".lamp-"+randomID())
		f, e := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			fail(w, "OUTCOME_UNKNOWN")
			return
		}
		staged[p] = temp
		_, e = f.Write([]byte(change.Files[p]))
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			fail(w, "OUTCOME_UNKNOWN")
			return
		}
	}
	// Reject concurrent source edits observed since approval/staging. A settings
	// root must not have other writers during apply (see README).
	current, e := snapshot(root, source)
	if e != nil || digest(current) != change.Base {
		fail(w, "REVISION_CONFLICT")
		return
	}
	paths := append([]string{}, in.Paths...)
	sort.Strings(paths)
	delete(b.changes, id) // one-use approval, including an interrupted apply
	for _, p := range paths {
		if !regular(root, p) || root.Rename(staged[p], p) != nil {
			b.log("settings", change.Sandbox, "OUTCOME_UNKNOWN")
			fail(w, "OUTCOME_UNKNOWN")
			return
		}
		delete(staged, p)
		dir, e := root.Open(filepath.Dir(p))
		if e != nil {
			fail(w, "OUTCOME_UNKNOWN")
			return
		}
		e = dir.Sync()
		_ = dir.Close()
		if e != nil {
			fail(w, "OUTCOME_UNKNOWN")
			return
		}
	}
	after, err := snapshot(root, source)
	if err != nil {
		fail(w, "OUTCOME_UNKNOWN")
		return
	}
	// Confirm each selected effect, never advertise a partial result as success.
	for _, p := range paths {
		if !bytes.Equal([]byte(after[p]), []byte(change.Files[p])) {
			fail(w, "OUTCOME_UNKNOWN")
			return
		}
	}
	b.log("settings", change.Sandbox, "applied")
	send(w, 200, map[string]any{"state": "applied", "selected_paths": paths, "revision": digest(after)})
}
