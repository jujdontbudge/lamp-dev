package control

// Lifecycle, lasting storage and saved templates (T19) keep their metadata in
// one private, fsynced state file beside the operation journal, and their
// bytes in a content-addressed object store under the same state directory.
// Operations, idempotency and sandbox state stay in the journal; this file is
// written before the journal records an operation's outcome, so a crash can
// leave an orphaned object (collected later) but never a published resource
// whose bytes were not durable.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

const lifeStateFile = "lifecycle.json"

// lifeState is every T19 resource. Public resources are kept as their wire
// objects (map form) so reads return exactly what was committed; private
// fields sit beside them and are never serialized to callers.
type lifeState struct {
	Jobs      map[string]*ciJob      `json:"jobs,omitempty"`
	AuthFlows map[string]*ciAuthFlow `json:"auth_flows,omitempty"`
	Version   int                    `json:"version"`
	// HighWater is the latest host time this service has observed. Deadlines
	// are compared against max(now, HighWater): a clock rollback can never
	// extend a retention or expiry deadline.
	HighWater   string                     `json:"high_water"`
	Blobs       map[string]*lifeBlob       `json:"blobs"`
	Caches      map[string]*lifeCache      `json:"caches"`
	Volumes     map[string]*lifeVolume     `json:"volumes"`
	Attachments map[string]*lifeAttachment `json:"attachments"`
	Artifacts   map[string]*lifeArtifact   `json:"artifacts"`
	Backups     map[string]*lifeBackup     `json:"backups"`
	Templates   map[string]*lifeTemplate   `json:"templates"`
	Recipes     map[string]*lifeRecipe     `json:"recipes"`
	// RecipeNames maps owner + "\n" + name to the current immutable version.
	RecipeNames map[string]string         `json:"recipe_names"`
	WarmPools   map[string]*lifePool      `json:"warm_pools"`
	ChangeSets  map[string]*lifeChangeSet `json:"change_sets"`
	Sandboxes   map[string]*lifeSandbox   `json:"sandboxes"`
	// Discards are engine captures waiting for deletion through the
	// supervisor's state administration (retention expiry, deleted templates).
	Discards map[string]string `json:"discards,omitempty"`
}

type lifeBlob struct {
	R map[string]any `json:"r"`
	// Source names the sandbox whose guest bytes produced this blob, if any.
	Source string `json:"source,omitempty"`
	// Trust is the trust class of Source when the blob was produced.
	Trust string `json:"trust,omitempty"`
}

// tree is a directory snapshot: guest path (relative) → content object.
type treeEntry struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   int64  `json:"mode"`
	Link   string `json:"link,omitempty"`
	Dir    bool   `json:"dir,omitempty"`
}
type tree map[string]treeEntry

type cacheGeneration struct {
	Digest  string `json:"digest"`
	Tree    tree   `json:"tree"`
	Bytes   int64  `json:"bytes"`
	Created string `json:"created"`
}
type lifeCache struct {
	R           map[string]any    `json:"r"`
	Generations []cacheGeneration `json:"generations"`
}
type lifeVolume struct {
	R          map[string]any `json:"r"`
	Generation uint64         `json:"generation"`
	Digest     string         `json:"digest"`
	Tree       tree           `json:"tree"`
	// History keeps each generation's tree so a shared-files writer can be
	// merged against the generation it started from.
	History map[string]tree `json:"history"`
}
type lifeAttachment struct {
	R map[string]any `json:"r"`
	// Base is the generation digest materialized into the guest at attach.
	Base string `json:"base"`
}
type lifeArtifact struct {
	R map[string]any `json:"r"`
}
type lifeBackup struct {
	R    map[string]any `json:"r"`
	File string         `json:"file"`
}
type lifeTemplate struct {
	R map[string]any `json:"r"`
	// Capture is the engine's durable capture that new sandboxes restore.
	Capture string `json:"capture"`
	// Resources must match the capture exactly.
	Resources json.RawMessage `json:"resources"`
	Base      string          `json:"base"`
}
type lifeRecipe struct {
	R      map[string]any `json:"r"`
	Digest string         `json:"digest"`
}
type lifePool struct {
	R      map[string]any `json:"r"`
	Recipe string         `json:"recipe"`
	Digest string         `json:"digest"`
	// Entries are never-used sandboxes waiting to be claimed (id → state).
	Entries map[string]string `json:"entries"`
}
type lifeChangeSet struct {
	R          map[string]any `json:"r"`
	Source     string         `json:"source"`
	Target     string         `json:"target"`
	Repository string         `json:"repository"`
	Base       string         `json:"base"`
	TargetHead string         `json:"target_head"`
}

// lifeSandbox is per-sandbox lifecycle state that is not a public field.
type lifeSandbox struct {
	Capture     string   `json:"capture,omitempty"`
	SuspendedAt string   `json:"suspended_at,omitempty"`
	Taint       []string `json:"taint,omitempty"`
	Trust       string   `json:"trust,omitempty"`
	Pool        string   `json:"pool,omitempty"`
	Used        bool     `json:"used,omitempty"`
	// Resumed is the generation a resume last brought up. Its guest has no
	// create command, so restart reconciliation probes it instead.
	Resumed uint64 `json:"resumed_generation,omitempty"`
	// Activity is the last task session activity Lamp itself mediated
	// (an exec admission or completion). It is in-memory only.
	activity time.Time
	idleFrom time.Time
	waking   chan struct{}
}

func (s *Service) lifePath() string { return filepath.Join(s.dir, lifeStateFile) }

func newLifeState() *lifeState {
	return &lifeState{Jobs: map[string]*ciJob{}, AuthFlows: map[string]*ciAuthFlow{}, Version: 1, Blobs: map[string]*lifeBlob{}, Caches: map[string]*lifeCache{}, Volumes: map[string]*lifeVolume{},
		Attachments: map[string]*lifeAttachment{}, Artifacts: map[string]*lifeArtifact{}, Backups: map[string]*lifeBackup{},
		Templates: map[string]*lifeTemplate{}, Recipes: map[string]*lifeRecipe{}, RecipeNames: map[string]string{},
		WarmPools: map[string]*lifePool{}, ChangeSets: map[string]*lifeChangeSet{}, Sandboxes: map[string]*lifeSandbox{}, Discards: map[string]string{}}
}

func (s *Service) loadLife() error {
	st := newLifeState()
	data, err := os.ReadFile(s.lifePath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := decodeNumbers(data, st); err != nil {
			return fmt.Errorf("invalid lifecycle state: %w", err)
		}
		if st.Version != 1 {
			return errors.New("unsupported lifecycle state version")
		}
		fresh := newLifeState()
		// Older files may lack newer maps.
		if st.Discards == nil {
			st.Discards = fresh.Discards
		}
	}
	for _, d := range []string{"objects", "objects/tmp", "backups"} {
		if err := os.MkdirAll(filepath.Join(s.dir, d), 0700); err != nil {
			return err
		}
	}
	s.life = st
	return nil
}

// saveLife runs under mu: write, fsync, rename, fsync directory.
func (s *Service) saveLife() error {
	if now := time.Now().UTC(); now.After(s.lifeNow().Add(-time.Second)) {
		s.life.HighWater = now.Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(s.life)
	if err != nil {
		return err
	}
	tmp := s.lifePath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := fullSync(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.lifePath()); err != nil {
		return err
	}
	return syncDir(s.dir)
}

// lifeNow is host-authoritative time that never moves backwards.
func (s *Service) lifeNow() time.Time {
	now := time.Now().UTC()
	if s.life != nil {
		if hw, err := time.Parse(time.RFC3339Nano, s.life.HighWater); err == nil && hw.After(now) {
			return hw
		}
	}
	return now
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return fullSync(d)
}

// fullSync is fsync; Go's File.Sync issues F_FULLFSYNC on macOS, so the
// bytes reach the device rather than its volatile cache.
func fullSync(f *os.File) error { return f.Sync() }

// --- content-addressed objects ---------------------------------------------

func validSHA(h string) bool {
	if len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}
func (s *Service) objectPath(sum string) string {
	return filepath.Join(s.dir, "objects", sum[:2], sum)
}
func (s *Service) hasObject(sum string) bool {
	if !validSHA(sum) {
		return false
	}
	_, err := os.Stat(s.objectPath(sum))
	return err == nil
}

// putObject stores bytes durably and returns their SHA-256. It does not need
// mu: objects are immutable and named by content.
func (s *Service) putObject(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	if s.hasObject(h) {
		return h, nil
	}
	tmp, err := os.CreateTemp(filepath.Join(s.dir, "objects", "tmp"), "put-")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	return h, s.publishObject(tmp, h)
}

// publishObject fsyncs an open temporary file and links it at its content
// address, then fsyncs the directory. The caller verified (or computed) h.
func (s *Service) publishObject(tmp *os.File, h string) error {
	if err := fullSync(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	dir := filepath.Dir(s.objectPath(h))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.objectPath(h)); err != nil {
		return err
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	return syncDir(filepath.Join(s.dir, "objects"))
}

// readObject returns verified bytes or DATA_UNAVAILABLE; a missing or
// damaged object is never replaced by empty content.
func (s *Service) readObject(sum string) ([]byte, error) {
	if !validSHA(sum) {
		return nil, reject("DATA_UNAVAILABLE", "invalid object reference")
	}
	data, err := os.ReadFile(s.objectPath(sum))
	if err != nil {
		return nil, reject("DATA_UNAVAILABLE", "stored object "+sum[:12]+" is missing")
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != sum {
		return nil, reject("DATA_UNAVAILABLE", "stored object "+sum[:12]+" is damaged")
	}
	return data, nil
}

func (s *Service) readObjectRange(sum string, offset, length int64) ([]byte, int64, error) {
	f, err := os.Open(s.objectPath(sum))
	if err != nil {
		return nil, 0, reject("DATA_UNAVAILABLE", "stored object is missing")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if offset > info.Size() {
		return nil, info.Size(), reject("INVALID_ARGUMENT", "offset beyond end")
	}
	buf := make([]byte, min(length, info.Size()-offset))
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return nil, 0, err
	}
	return buf, info.Size(), nil
}

// referencedObjects lists every object still reachable from published state.
func (s *Service) referencedObjects() map[string]bool {
	refs := map[string]bool{}
	addTree := func(t tree) {
		for _, e := range t {
			if e.SHA256 != "" {
				refs[e.SHA256] = true
			}
		}
	}
	for _, b := range s.life.Blobs {
		if h := textField(b.R, "sha256"); h != "" && textField(b.R, "state") != "deleted" {
			refs[h] = true
		}
	}
	for _, c := range s.life.Caches {
		if textField(c.R, "state") == "deleted" {
			continue
		}
		for _, g := range c.Generations {
			addTree(g.Tree)
		}
	}
	for _, v := range s.life.Volumes {
		if textField(v.R, "state") == "deleted" {
			continue
		}
		addTree(v.Tree)
		for _, t := range v.History {
			addTree(t)
		}
	}
	return refs
}

// collectObjects removes unreferenced objects exactly once. Called under mu.
func (s *Service) collectObjects() (released int64) {
	refs := s.referencedObjects()
	root := filepath.Join(s.dir, "objects")
	entries, _ := os.ReadDir(root)
	for _, d := range entries {
		if !d.IsDir() || len(d.Name()) != 2 {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(root, d.Name()))
		for _, f := range files {
			if refs[f.Name()] {
				continue
			}
			p := filepath.Join(root, d.Name(), f.Name())
			if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) > time.Minute {
				if os.Remove(p) == nil {
					released += info.Size()
				}
			}
		}
	}
	return released
}

func jsonNumber(v uint64) json.Number { return json.Number(strconv.FormatUint(v, 10)) }

// decodeNumbers unmarshals with json.Number for numbers, so resource maps
// keep exact integers (float64 would print 1 GiB as 1.073741824e+09).
func decodeNumbers(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	return d.Decode(v)
}
