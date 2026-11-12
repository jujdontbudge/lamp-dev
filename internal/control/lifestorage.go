package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lamp.eilmer.si/pkg/api"
)

const (
	maxBlobBytes          = 4 << 30
	maxTransferBytes      = 256 << 20
	defaultCacheQuota     = 1 << 30
	defaultVolumeQuota    = 1 << 30
	artifactCollection    = 120 * time.Second
	defaultArtifactRetain = 30 * 24 * time.Hour
	downloadChunk         = 1 << 20
)

func init() {
	lifeHandlers["blob_create"] = (*Service).planBlobCreate
	lifeHandlers["blob_upload"] = (*Service).planBlobUpload
	lifeHandlers["blob_finalize"] = (*Service).planBlobFinalize
	lifeHandlers["blob_delete"] = (*Service).planBlobDelete
	lifeHandlers["sandbox_file_transfer"] = (*Service).planFileTransfer
	lifeHandlers["cache_create"] = (*Service).planCacheCreate
	lifeHandlers["cache_publish"] = (*Service).planCachePublish
	lifeHandlers["cache_evict"] = (*Service).planCacheEvict
	lifeHandlers["cache_delete"] = (*Service).planCacheDelete
	lifeHandlers["volume_create"] = (*Service).planVolumeCreate
	lifeHandlers["volume_delete"] = (*Service).planVolumeDelete
	lifeHandlers["attachment_create"] = (*Service).planAttachmentCreate
	lifeHandlers["attachment_delete"] = (*Service).planAttachmentDelete
	lifeHandlers["artifact_create"] = (*Service).planArtifactCreate
	lifeHandlers["artifact_finalize"] = (*Service).planArtifactFinalize
	lifeHandlers["artifact_delete"] = (*Service).planArtifactDelete
	lifeHandlers["backup_create"] = (*Service).planBackupCreate
	lifeHandlers["backup_restore"] = (*Service).planBackupRestore
	lifeHandlers["backup_delete"] = (*Service).planBackupDelete
}

func onlyFields(in map[string]any, fields string) error {
	for k := range in {
		if k == "idempotency_key" || k == "expected_revision" || k == "labels" || k == "visibility" {
			continue
		}
		if !strings.Contains(" "+fields+" ", " "+k+" ") {
			return reject("INVALID_ARGUMENT", k+" is not supported here")
		}
	}
	if v := textField(in, "visibility"); v != "" && v != "private" {
		return reject("SCOPE_DENIED", "lasting storage is owner-only; sharing needs an explicit share")
	}
	return nil
}

func applyLabels(m, in map[string]any) {
	if l, ok := in["labels"].(map[string]any); ok {
		m["labels"] = l
	}
}

func (s *Service) ownedOrMissing(a caller, kind, id string) (map[string]any, error) {
	m := s.lifeFind(a, kind, id)
	if m == nil || textField(m, "state") == "deleted" {
		return nil, reject("NOT_FOUND", kind+" not found")
	}
	return m, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func trimDigest(d string) string { return strings.TrimPrefix(d, "sha256:") }

// --- blobs -----------------------------------------------------------------

func (s *Service) blobTemp(id string) string { return filepath.Join(s.dir, "objects", "tmp", id) }

func (s *Service) planBlobCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := onlyFields(in, "sha256 size_bytes media_type expires_at"); err != nil {
		return lifePlan{}, err
	}
	m := s.lifeResource("bl", "blob", a.Principal)
	m["state"] = "uploading"
	if h := trimDigest(textField(in, "sha256")); h != "" {
		if !validSHA(h) {
			return lifePlan{}, reject("INVALID_ARGUMENT", "sha256 must be 64 hex digits")
		}
		m["sha256"] = h
	}
	if _, ok := in["size_bytes"]; ok {
		if uintField(in, "size_bytes") > maxBlobBytes {
			return lifePlan{}, reject("NO_CAPACITY", "blob exceeds 4 GiB")
		}
		m["size_bytes"] = jsonNumber(uintField(in, "size_bytes"))
	}
	for _, k := range []string{"media_type", "expires_at"} {
		if v := textField(in, k); v != "" {
			m[k] = v
		}
	}
	m["uploaded_ranges"] = []any{}
	applyLabels(m, in)
	s.life.Blobs[textField(m, "id")] = &lifeBlob{R: m}
	return lifePlan{ref: lifeRef("blob", textField(m, "id")), result: map[string]any{"blob_id": textField(m, "id")}}, nil
}

func (s *Service) planBlobUpload(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "blob", textField(in, "blob_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if textField(m, "state") != "uploading" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "blob is not uploading")
	}
	data, err := base64.StdEncoding.DecodeString(textField(in, "data_base64"))
	if err != nil || len(data) > 1<<20 {
		return lifePlan{}, reject("INVALID_ARGUMENT", "data_base64 must hold at most 1 MiB")
	}
	if c := trimDigest(textField(in, "chunk_sha256")); c != "" && c != sha256Hex(data) {
		return lifePlan{}, reject("INVALID_ARGUMENT", "chunk_sha256 does not match the chunk")
	}
	offset := int64(uintField(in, "offset_bytes"))
	if offset+int64(len(data)) > maxBlobBytes {
		return lifePlan{}, reject("NO_CAPACITY", "blob exceeds 4 GiB")
	}
	f, err := os.OpenFile(s.blobTemp(textField(m, "id")), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return lifePlan{}, reject("DATA_UNAVAILABLE", "cannot stage upload")
	}
	_, err = f.WriteAt(data, offset)
	if err == nil {
		err = fullSync(f)
	}
	f.Close()
	if err != nil {
		return lifePlan{}, reject("DATA_UNAVAILABLE", "cannot stage upload")
	}
	ranges := stringsField(m, "uploaded_ranges")
	ranges = append(ranges, fmt.Sprintf("%d-%d", offset, offset+int64(len(data))))
	m["uploaded_ranges"] = ranges
	editResource(m)
	return lifePlan{ref: lifeRef("blob", textField(m, "id")), result: map[string]any{}}, nil
}

func (s *Service) planBlobFinalize(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "blob", textField(in, "blob_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if textField(m, "state") != "uploading" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "blob is not uploading")
	}
	id := textField(m, "id")
	data, err := os.ReadFile(s.blobTemp(id))
	if err != nil && !os.IsNotExist(err) {
		return lifePlan{}, reject("DATA_UNAVAILABLE", "staged upload unreadable")
	}
	got := sha256Hex(data)
	want := trimDigest(textField(in, "sha256"))
	if want == "" {
		want = textField(m, "sha256")
	}
	size := uintField(in, "size_bytes")
	if _, ok := in["size_bytes"]; !ok {
		size = uintField(m, "size_bytes")
	}
	if (want != "" && want != got) || uint64(len(data)) != size {
		// Corrupted input never becomes ready.
		m["state"] = "failed"
		editResource(m)
		os.Remove(s.blobTemp(id))
		return lifePlan{ref: lifeRef("blob", id), result: map[string]any{}}, nil
	}
	if _, err := s.putObject(data); err != nil {
		return lifePlan{}, reject("DATA_UNAVAILABLE", "cannot store blob")
	}
	os.Remove(s.blobTemp(id))
	m["state"] = "ready"
	m["sha256"] = got
	m["size_bytes"] = jsonNumber(uint64(len(data)))
	editResource(m)
	return lifePlan{ref: lifeRef("blob", id), result: map[string]any{}}, nil
}

func (s *Service) planBlobDelete(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "blob", textField(in, "blob_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	id := textField(m, "id")
	for _, ar := range s.life.Artifacts {
		if textField(ar.R, "state") != "deleted" && (textField(ar.R, "download_blob_id") == id || textField(ar.R, "manifest_blob_id") == id) {
			return lifePlan{}, reject("RESOURCE_IN_USE", "an artifact references this blob")
		}
	}
	m["state"] = "deleted"
	editResource(m)
	return lifePlan{ref: lifeRef("blob", id), result: map[string]any{}}, nil
}

// newReadyBlob records bytes produced by Lamp as a ready blob. Under mu.
func (s *Service) newReadyBlob(owner, sum string, size int64, media, source string) map[string]any {
	m := s.lifeResource("bl", "blob", owner)
	m["state"] = "ready"
	m["sha256"] = sum
	m["size_bytes"] = jsonNumber(uint64(size))
	if media != "" {
		m["media_type"] = media
	}
	trust := ""
	if source != "" {
		trust = s.sandboxTrust(source)
	}
	s.life.Blobs[textField(m, "id")] = &lifeBlob{R: m, Source: source, Trust: trust}
	return m
}

func (s *Service) sandboxTrust(id string) string {
	if l := s.life.Sandboxes[id]; l != nil && l.Trust != "" {
		return l.Trust
	}
	return "trusted"
}

func (s *Service) readyBlob(a caller, id string) (*lifeBlob, error) {
	m := s.lifeFind(a, "blob", id)
	if m == nil {
		return nil, reject("NOT_FOUND", "blob not found")
	}
	if textField(m, "state") != "ready" {
		return nil, reject("DATA_UNAVAILABLE", "blob is not ready")
	}
	return s.life.Blobs[id], nil
}

// downloadChunk serves blob_download and artifact_download as verified chunks.
func (s *Service) downloadChunk(w http.ResponseWriter, reqID string, a caller, name string, in map[string]any) func() {
	fail := func(code, message string) func() {
		return prepareError(w, reqID, api.ErrorDetail{Code: api.ErrorCode(code), Message: message})
	}
	blobID := textField(in, "blob_id")
	out := map[string]any{"request_id": reqID}
	if name == "artifact_download" {
		ar := s.lifeFind(a, "artifact", textField(in, "artifact_id"))
		if ar == nil || textField(ar, "state") == "deleted" {
			return fail("NOT_FOUND", "artifact not found")
		}
		blobID = textField(ar, "download_blob_id")
		if blobID == "" {
			return fail("DATA_UNAVAILABLE", "artifact has no collected content")
		}
		out["manifest_blob_id"] = textField(ar, "manifest_blob_id")
	}
	b, err := s.readyBlob(a, blobID)
	if err != nil {
		e := err.(*coreError)
		if name == "artifact_download" {
			return fail("DATA_UNAVAILABLE", "artifact content is unavailable")
		}
		return fail(e.code, e.message)
	}
	sum := textField(b.R, "sha256")
	offset := int64(uintField(in, "offset_bytes"))
	length := int64(downloadChunk)
	if _, ok := in["length_bytes"]; ok {
		length = min(int64(uintField(in, "length_bytes")), downloadChunk)
	}
	if offset == 0 && length >= int64(uintField(b.R, "size_bytes")) {
		// A whole-object read is verified against its content address.
		if _, err := s.readObject(sum); err != nil {
			return fail("DATA_UNAVAILABLE", err.Error())
		}
	}
	data, size, err := s.readObjectRange(sum, offset, length)
	if err != nil {
		e, ok := err.(*coreError)
		if !ok {
			return fail("DATA_UNAVAILABLE", "stored content unreadable")
		}
		return fail(e.code, e.message)
	}
	out["blob_id"] = blobID
	out["offset_bytes"] = jsonNumber(uint64(offset))
	out["data_base64"] = base64.StdEncoding.EncodeToString(data)
	out["chunk_sha256"] = sha256Hex(data)
	out["sha256"] = sum
	out["size_bytes"] = jsonNumber(uint64(size))
	out["eof"] = offset+int64(len(data)) >= size
	return prepareJSON(w, 200, out)
}

// --- file transfer ---------------------------------------------------------

// sandbox_file_transfer: "download" copies a guest file (or a directory, as
// a tar archive) out to a new ready blob; "upload" writes a ready blob into
// the guest at path.
func (s *Service) planFileTransfer(a caller, in map[string]any) (lifePlan, error) {
	id := textField(in, "sandbox_id")
	sb, err := s.lifeSandboxFor(a, id)
	if err != nil {
		return lifePlan{}, err
	}
	if sb.Revision != uintField(in, "expected_revision") {
		return lifePlan{}, reject("REVISION_CONFLICT", "sandbox revision changed")
	}
	if sb.State != "ready" || s.busy[id] {
		return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox is not ready")
	}
	p := textField(in, "path")
	if !strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) || path.Clean(p) != p {
		return lifePlan{}, reject("INVALID_ARGUMENT", "path must be a clean absolute guest path")
	}
	direction := textField(in, "direction")
	var data []byte
	if direction == "upload" {
		b, err := s.readyBlob(a, textField(in, "blob_id"))
		if err != nil {
			return lifePlan{}, err
		}
		if data, err = s.readObject(textField(b.R, "sha256")); err != nil {
			return lifePlan{}, err
		}
		if len(data) > maxTransferBytes {
			return lifePlan{}, reject("NO_CAPACITY", "transfer exceeds 256 MiB")
		}
	} else if textField(in, "blob_id") != "" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "download creates a new blob")
	}
	owner := a.Principal
	var produced string
	return lifePlan{ref: lifeRef("sandbox", id), timeout: 10 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			if direction == "upload" {
				return map[string]any{"blob_id": textField(in, "blob_id")}, s.guestWriteFile(ctx, id, p, data)
			}
			exists, dir, size, err := s.guestStat(ctx, id, p)
			if err != nil {
				return nil, err
			}
			if !exists {
				return nil, reject("NOT_FOUND", "guest path not found")
			}
			if dir {
				data, err = s.guestArchive(ctx, id, maxTransferBytes, p)
			} else {
				data, err = s.guestReadFile(ctx, id, p, size, maxTransferBytes)
			}
			if err != nil {
				return nil, err
			}
			produced, err = s.putObject(data)
			if err != nil {
				return nil, err
			}
			return map[string]any{}, nil
		},
		finish: func(result map[string]any, err error) error {
			if err == nil && direction == "download" {
				media := "application/octet-stream"
				if strings.HasSuffix(p, ".tar") {
					media = "application/x-tar"
				}
				b := s.newReadyBlob(owner, produced, int64(len(data)), media, id)
				result["blob_id"] = textField(b, "id")
			}
			return err
		}}, nil
}

// --- caches (STO-001) ------------------------------------------------------

func (s *Service) planCacheCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := onlyFields(in, "name trust_class arch toolchain_digest quota_bytes retention_seconds"); err != nil {
		return lifePlan{}, err
	}
	if textField(in, "name") == "" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "name required")
	}
	m := s.lifeResource("ca", "cache", a.Principal)
	trust := textField(in, "trust_class")
	if trust == "" {
		trust = "trusted"
	}
	m["trust_class"] = trust
	m["name"] = textField(in, "name")
	for _, k := range []string{"arch", "toolchain_digest"} {
		if v := textField(in, k); v != "" {
			m[k] = v
		}
	}
	quota := uint64(defaultCacheQuota)
	if _, ok := in["quota_bytes"]; ok {
		quota = uintField(in, "quota_bytes")
	}
	m["quota_bytes"] = jsonNumber(quota)
	if _, ok := in["retention_seconds"]; ok {
		m["retention_seconds"] = jsonNumber(uintField(in, "retention_seconds"))
	}
	empty := treeDigest(tree{})
	m["generation_digest"] = empty
	applyLabels(m, in)
	s.life.Caches[textField(m, "id")] = &lifeCache{R: m, Generations: []cacheGeneration{{Digest: empty, Tree: tree{}, Created: timestamp()}}}
	return lifePlan{ref: lifeRef("cache", textField(m, "id")), result: map[string]any{"cache_id": textField(m, "id")}}, nil
}

func (c *lifeCache) generation(digest string) *cacheGeneration {
	for i := range c.Generations {
		if c.Generations[i].Digest == digest {
			return &c.Generations[i]
		}
	}
	return nil
}

// cache_publish collects the one read-write attachment's directory as a new
// generation. base_digest must name the current generation (compare and
// swap); generation_digest, when given, must equal the collected content.
// Only a sandbox whose trust matches the cache's class can publish to it.
func (s *Service) planCachePublish(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "cache", textField(in, "cache_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	id := textField(m, "id")
	c := s.life.Caches[id]
	current := textField(m, "generation_digest")
	if base := textField(in, "base_digest"); base == "" || base != current {
		return lifePlan{}, reject("REVISION_CONFLICT", "base_digest is not the current generation "+current)
	}
	var writer *lifeAttachment
	for _, at := range s.life.Attachments {
		if textField(at.R, "resource_id") == id && textField(at.R, "state") == "ready" && textField(at.R, "mode") == "read_write" {
			if writer != nil {
				return lifePlan{}, reject("RESOURCE_IN_USE", "more than one writer is attached")
			}
			writer = at
		}
	}
	if writer == nil {
		return lifePlan{}, reject("RESOURCE_IN_USE", "publish needs one read-write attachment")
	}
	sandbox := textField(writer.R, "sandbox_id")
	if s.sandboxTrust(sandbox) != textField(m, "trust_class") {
		return lifePlan{}, reject("SCOPE_DENIED", "the writer's trust class differs from the cache's")
	}
	if sb := s.journal.Sandboxes[sandbox]; sb.State != "ready" || s.busy[sandbox] {
		return lifePlan{}, reject("RESOURCE_IN_USE", "writer sandbox is not ready")
	}
	mount := textField(writer.R, "mount_path")
	quota := int64(uintField(m, "quota_bytes"))
	want := textField(in, "generation_digest")
	var collected tree
	return lifePlan{ref: lifeRef("cache", id), timeout: 10 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			t, err := s.collectTree(ctx, sandbox, mount, quota)
			if err != nil {
				return nil, err
			}
			if d := treeDigest(t); want != "" && want != d {
				return nil, reject("INVALID_ARGUMENT", "generation_digest does not match collected content "+d)
			}
			collected = t
			return map[string]any{}, nil
		},
		finish: func(_ map[string]any, err error) error {
			if err != nil {
				return err
			}
			if textField(m, "generation_digest") != current {
				return reject("REVISION_CONFLICT", "a concurrent publish won; this one changed nothing")
			}
			d := treeDigest(collected)
			if c.generation(d) == nil {
				c.Generations = append(c.Generations, cacheGeneration{Digest: d, Tree: collected, Bytes: treeBytes(collected), Created: timestamp()})
			}
			m["generation_digest"] = d
			writer.Base = d
			editResource(m)
			return nil
		}}, nil
}

// cache_evict releases every generation that is neither current nor pinned
// by an attached reader; each object is released exactly once by GC.
func (s *Service) planCacheEvict(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "cache", textField(in, "cache_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	id := textField(m, "id")
	c := s.life.Caches[id]
	pinned := map[string]bool{textField(m, "generation_digest"): true}
	for _, at := range s.life.Attachments {
		if textField(at.R, "resource_id") == id && textField(at.R, "state") == "ready" {
			pinned[at.Base] = true
		}
	}
	kept := c.Generations[:0]
	evicted := 0
	for _, g := range c.Generations {
		if pinned[g.Digest] {
			kept = append(kept, g)
		} else {
			evicted++
		}
	}
	c.Generations = kept
	editResource(m)
	return lifePlan{ref: lifeRef("cache", id), result: map[string]any{}}, nil
}

func (s *Service) planCacheDelete(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "cache", textField(in, "cache_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if s.attached(textField(m, "id")) {
		return lifePlan{}, reject("RESOURCE_IN_USE", "cache is attached")
	}
	m["state"] = "deleted"
	editResource(m)
	return lifePlan{ref: lifeRef("cache", textField(m, "id")), result: map[string]any{}}, nil
}

func (s *Service) attached(resource string) bool {
	for _, at := range s.life.Attachments {
		if textField(at.R, "resource_id") == resource && (textField(at.R, "state") == "ready" || textField(at.R, "state") == "attaching") {
			return true
		}
	}
	return false
}

// --- volumes (STO-002) -----------------------------------------------------

func (s *Service) planVolumeCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := onlyFields(in, "name mode storage_host_id quota_bytes durability"); err != nil {
		return lifePlan{}, err
	}
	if textField(in, "name") == "" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "name required")
	}
	if h := textField(in, "storage_host_id"); h != "" && h != s.hostID() {
		return lifePlan{}, reject("HOST_UNAVAILABLE", "storage host is not this host")
	}
	switch d := textField(in, "durability"); d {
	case "", "local":
	case "replicated", "backup":
		return lifePlan{}, reject("QUORUM_REQUIRED", "replicated volumes need a second storage host")
	default:
		return lifePlan{}, reject("INVALID_ARGUMENT", "a volume is always durable")
	}
	mode := textField(in, "mode")
	if mode == "" {
		mode = "shared_files"
	}
	m := s.lifeResource("vo", "volume", a.Principal)
	m["state"] = "ready"
	m["name"] = textField(in, "name")
	m["mode"] = mode
	m["storage_host_id"] = s.hostID()
	quota := uint64(defaultVolumeQuota)
	if _, ok := in["quota_bytes"]; ok {
		quota = uintField(in, "quota_bytes")
	}
	m["quota_bytes"] = jsonNumber(quota)
	m["attachment_ids"] = []any{}
	m["durability"] = "local"
	m["custody_host_ids"] = []any{s.hostID()}
	applyLabels(m, in)
	empty := treeDigest(tree{})
	s.life.Volumes[textField(m, "id")] = &lifeVolume{R: m, Digest: empty, Tree: tree{}, History: map[string]tree{empty: {}}}
	return lifePlan{ref: lifeRef("volume", textField(m, "id")), result: map[string]any{"volume_id": textField(m, "id")}}, nil
}

func (s *Service) planVolumeDelete(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "volume", textField(in, "volume_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if s.attached(textField(m, "id")) {
		return lifePlan{}, reject("RESOURCE_IN_USE", "volume is attached")
	}
	m["state"] = "deleted"
	editResource(m)
	return lifePlan{ref: lifeRef("volume", textField(m, "id")), result: map[string]any{}}, nil
}

// mergeShared applies one writer's changes (base → mine) onto the current
// tree. A path changed on both sides since base is a conflict: nothing is
// overwritten blindly.
func mergeShared(base, current, mine tree) (tree, []string) {
	out := tree{}
	for k, v := range current {
		out[k] = v
	}
	var conflicts []string
	keys := map[string]bool{}
	for k := range base {
		keys[k] = true
	}
	for k := range mine {
		keys[k] = true
	}
	for k := range keys {
		b, inBase := base[k]
		m, inMine := mine[k]
		if inBase == inMine && b == m {
			continue // unchanged by this writer
		}
		c, inCur := current[k]
		if inCur != inBase || c != b {
			if !(inCur == inMine && c == m) {
				conflicts = append(conflicts, k)
			}
			continue
		}
		if inMine {
			out[k] = m
		} else {
			delete(out, k)
		}
	}
	return out, conflicts
}

// --- attachments -----------------------------------------------------------

func (s *Service) planAttachmentCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := onlyFields(in, "sandbox_id resource_kind resource_id mount_path mode fork_policy"); err != nil {
		return lifePlan{}, err
	}
	sbID := textField(in, "sandbox_id")
	sb, err := s.lifeSandboxFor(a, sbID)
	if err != nil {
		return lifePlan{}, err
	}
	if sb.State != "ready" || s.busy[sbID] {
		return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox is not ready")
	}
	kind := textField(in, "resource_kind")
	if kind != "cache" && kind != "volume" {
		return lifePlan{}, reject("NOT_FOUND", "operation not installed for "+kind+" attachments")
	}
	res, err := s.ownedOrMissing(a, kind, textField(in, "resource_id"))
	if err != nil {
		return lifePlan{}, err
	}
	resID := textField(res, "id")
	mount := textField(in, "mount_path")
	if !strings.HasPrefix(mount, "/") || path.Clean(mount) != mount || mount == "/" || strings.ContainsRune(mount, 0) {
		return lifePlan{}, reject("INVALID_ARGUMENT", "mount_path must be a clean absolute guest directory")
	}
	mode := textField(in, "mode")
	if mode == "" {
		mode = "read_only"
	}
	fork := textField(in, "fork_policy")
	if fork == "" {
		fork = "detach"
	}
	if fork != "detach" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "a fork never inherits a writer; fork_policy must be detach")
	}
	var t tree
	var base string
	switch kind {
	case "cache":
		c := s.life.Caches[resID]
		if mode == "read_write" && s.sandboxTrust(sbID) != textField(res, "trust_class") {
			return lifePlan{}, reject("SCOPE_DENIED", "only same-trust sandboxes may write this cache")
		}
		base = textField(res, "generation_digest")
		t = c.generation(base).Tree
	case "volume":
		v := s.life.Volumes[resID]
		if textField(res, "mode") == "exclusive_block" && mode == "read_write" {
			for _, at := range s.life.Attachments {
				if textField(at.R, "resource_id") == resID && textField(at.R, "mode") == "read_write" && (textField(at.R, "state") == "ready" || textField(at.R, "state") == "attaching") {
					return lifePlan{}, reject("RESOURCE_IN_USE", "an exclusive volume already has its writer")
				}
			}
		}
		base, t = v.Digest, v.Tree
	}
	m := s.lifeResource("at", "attachment", a.Principal)
	m["state"] = "attaching"
	m["sandbox_id"] = sbID
	m["resource_kind"] = kind
	m["resource_id"] = resID
	m["mount_path"] = mount
	m["mode"] = mode
	m["fork_policy"] = fork
	applyLabels(m, in)
	at := &lifeAttachment{R: m, Base: base}
	atID := textField(m, "id")
	s.life.Attachments[atID] = at
	if kind == "volume" {
		res["attachment_ids"] = append(stringsField(res, "attachment_ids"), atID)
		editResource(res)
	}
	return lifePlan{ref: lifeRef("attachment", atID), timeout: 10 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			if err := s.materializeTree(ctx, sbID, t, mount); err != nil {
				return nil, err
			}
			if mode == "read_only" {
				// Read-only within the guest's own permissions; writes are
				// never collected from a read-only attachment.
				s.guestRun(ctx, sbID, "chmod", "-R", "a-w", mount)
			}
			return map[string]any{"attachment_id": atID}, nil
		},
		finish: func(_ map[string]any, err error) error {
			if err != nil {
				m["state"] = "failed"
				s.detachFrom(res, atID)
			} else {
				m["state"] = "ready"
			}
			editResource(m)
			return err
		}}, nil
}

func (s *Service) detachFrom(res map[string]any, atID string) {
	if res["kind"] != "volume" {
		return
	}
	ids := stringsField(res, "attachment_ids")
	kept := []string{}
	for _, id := range ids {
		if id != atID {
			kept = append(kept, id)
		}
	}
	res["attachment_ids"] = kept
	editResource(res)
}

// attachment_delete detaches. A read-write volume attachment first writes its
// directory back: exclusive replaces, shared merges against its base.
func (s *Service) planAttachmentDelete(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "attachment", textField(in, "attachment_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if textField(m, "state") == "detached" {
		return lifePlan{}, reject("NOT_FOUND", "attachment not found")
	}
	atID := textField(m, "id")
	at := s.life.Attachments[atID]
	kind, resID, sbID := textField(m, "resource_kind"), textField(m, "resource_id"), textField(m, "sandbox_id")
	var res map[string]any
	if kind == "volume" {
		res = s.life.Volumes[resID].R
	} else {
		res = s.life.Caches[resID].R
	}
	sb := s.journal.Sandboxes[sbID]
	writable := kind == "volume" && textField(m, "mode") == "read_write" && textField(m, "state") == "ready"
	if writable && sb.State != "ready" && sb.State != "suspended" {
		// The guest that held this attachment's files is gone; never
		// pretend a write-back happened.
		return lifePlan{}, reject("DATA_UNAVAILABLE", "sandbox "+sbID+" is "+string(sb.State)+"; files written through this attachment cannot be written back, so it stays attached")
	}
	writeBack := writable
	if !writeBack {
		m["state"] = "detached"
		editResource(m)
		s.detachFrom(res, atID)
		return lifePlan{ref: lifeRef("attachment", atID), result: map[string]any{}}, nil
	}
	if s.busy[sbID] {
		return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox is busy")
	}
	quota := int64(uintField(res, "quota_bytes"))
	mount := textField(m, "mount_path")
	var collected tree
	return lifePlan{ref: lifeRef("attachment", atID), timeout: 10 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			// A suspended sandbox's files are in its saved state: wake it
			// so they are written back, never skipped.
			if err := s.Wake(ctx, a, sbID); err != nil {
				return nil, err
			}
			t, err := s.collectTree(ctx, sbID, mount, quota)
			collected = t
			return map[string]any{}, err
		},
		finish: func(_ map[string]any, err error) error {
			if err != nil {
				return err
			}
			v := s.life.Volumes[resID]
			next := collected
			if textField(res, "mode") == "shared_files" {
				var conflicts []string
				next, conflicts = mergeShared(v.History[at.Base], v.Tree, collected)
				if len(conflicts) > 0 {
					// The attachment stays attached and nothing is
					// overwritten; the writer resolves and detaches again.
					return reject("RESOURCE_IN_USE", "changed on both sides since attach: "+strings.Join(conflicts, ","))
				}
			}
			if treeBytes(next) > int64(quota) {
				return reject("NO_CAPACITY", "volume quota exceeded")
			}
			d := treeDigest(next)
			if d != v.Digest {
				v.Generation++
				v.Digest, v.Tree = d, next
				if v.History == nil {
					v.History = map[string]tree{}
				}
				v.History[d] = next
			}
			s.pruneVolumeHistory(resID)
			m["state"] = "detached"
			editResource(m)
			s.detachFrom(res, atID)
			return nil
		}}, nil
}

// writableAttachments lists a sandbox's attached read-write volume
// attachments (id → revision). Under mu.
func (s *Service) writableAttachments(sbID string) map[string]uint64 {
	out := map[string]uint64{}
	for id, at := range s.life.Attachments {
		m := at.R
		if textField(m, "sandbox_id") == sbID && textField(m, "resource_kind") == "volume" && textField(m, "mode") == "read_write" && textField(m, "state") == "ready" {
			out[id] = uintField(m, "revision")
		}
	}
	return out
}

// deleteKeepsWrites refuses sandbox_delete while a read-write volume is
// attached: deleting would drop its files. Detach first to write them back.
func (s *Service) deleteKeepsWrites(w http.ResponseWriter, requestID, sbID string) bool {
	s.mu.Lock()
	open := s.writableAttachments(sbID)
	s.mu.Unlock()
	for id := range open {
		writeError(w, requestID, "RESOURCE_IN_USE", "read-write volume attachment "+id+" is still attached; detach it first so its files are written back", nil)
		return false
	}
	return true
}

// detachForDelete writes back every read-write volume attachment before Lamp
// itself deletes a sandbox (retention or idle delete). It reports false when
// any write-back did not complete; the sandbox is then kept.
func (s *Service) detachForDelete(ctx context.Context, owner, sbID string) bool {
	s.mu.Lock()
	open := s.writableAttachments(sbID)
	s.mu.Unlock()
	for id, rev := range open {
		in := map[string]any{"attachment_id": id, "expected_revision": jsonNumber(rev), "idempotency_key": "lamp-delete-detach:" + id + ":" + strconv.FormatUint(rev, 10)}
		opID, _, err := s.lifeStart(caller{Principal: owner}, "attachment_delete", in)
		if err == nil {
			err = s.lifeWait(ctx, opID)
		}
		if err != nil {
			return false
		}
	}
	return true
}

// pruneVolumeHistory keeps only the current tree and attached writers' bases.
func (s *Service) pruneVolumeHistory(id string) {
	v := s.life.Volumes[id]
	keep := map[string]bool{v.Digest: true}
	for _, at := range s.life.Attachments {
		if textField(at.R, "resource_id") == id && textField(at.R, "state") != "detached" {
			keep[at.Base] = true
		}
	}
	for d := range v.History {
		if !keep[d] {
			delete(v.History, d)
		}
	}
}

// --- artifacts (STO-003) ---------------------------------------------------

type artifactManifest struct {
	Format  string `json:"format"`
	Entries []struct {
		BlobID string `json:"blob_id"`
		SHA256 string `json:"sha256"`
		Size   uint64 `json:"size_bytes"`
		Path   string `json:"path,omitempty"`
	} `json:"entries"`
}

func (s *Service) planArtifactCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := onlyFields(in, "job_id task_id manifest_blob_id download_blob_id expected_bytes expires_at durability"); err != nil {
		return lifePlan{}, err
	}
	if _, ok := in["present_bytes"]; ok {
		return lifePlan{}, reject("INVALID_ARGUMENT", "present_bytes is observed, not declared")
	}
	if d := textField(in, "durability"); d != "" && d != "local" {
		return lifePlan{}, reject("QUORUM_REQUIRED", "replicated artifacts need a second storage host")
	}
	m := s.lifeResource("ar", "artifact", a.Principal)
	m["state"] = "collecting"
	for _, k := range []string{"job_id", "task_id"} {
		if v := textField(in, k); v != "" {
			m[k] = v
		}
	}
	if _, ok := in["expected_bytes"]; ok {
		m["expected_bytes"] = jsonNumber(uintField(in, "expected_bytes"))
	}
	expires := s.lifeNow().Add(defaultArtifactRetain).Format(time.RFC3339Nano)
	if v := textField(in, "expires_at"); v != "" {
		expires = v
	}
	m["expires_at"] = expires
	m["present_bytes"] = jsonNumber(0)
	m["durability"] = "local"
	m["desired_replicas"] = jsonNumber(1)
	m["custody_host_ids"] = []any{s.hostID()}
	applyLabels(m, in)
	id := textField(m, "id")
	s.life.Artifacts[id] = &lifeArtifact{R: m}
	if blob := textField(in, "download_blob_id"); blob != "" {
		b, err := s.readyBlob(a, blob)
		if err != nil {
			return lifePlan{}, err
		}
		// A single collected object: Lamp writes its manifest.
		man := map[string]any{"format": "lamp.artifact-manifest/v1", "entries": []any{map[string]any{"blob_id": blob, "sha256": textField(b.R, "sha256"), "size_bytes": uintField(b.R, "size_bytes")}}}
		raw, _ := json.Marshal(man)
		sum, err := s.putObject(raw)
		if err != nil {
			return lifePlan{}, reject("DATA_UNAVAILABLE", "cannot store manifest")
		}
		mb := s.newReadyBlob(a.Principal, sum, int64(len(raw)), "application/json", "")
		if err := s.finalizeArtifact(a, m, textField(mb, "id")); err != nil {
			return lifePlan{}, err
		}
	} else if mb := textField(in, "manifest_blob_id"); mb != "" {
		if err := s.finalizeArtifact(a, m, mb); err != nil {
			return lifePlan{}, err
		}
	}
	return lifePlan{ref: lifeRef("artifact", id), result: map[string]any{"artifact_id": id}}, nil
}

// finalizeArtifact verifies every manifest entry against stored bytes and
// publishes complete only when every expected byte is present.
func (s *Service) finalizeArtifact(a caller, m map[string]any, manifestBlob string) error {
	b, err := s.readyBlob(a, manifestBlob)
	if err != nil {
		return err
	}
	raw, err := s.readObject(textField(b.R, "sha256"))
	if err != nil {
		return err
	}
	var man artifactManifest
	if json.Unmarshal(raw, &man) != nil || len(man.Entries) == 0 {
		return reject("INVALID_ARGUMENT", "manifest must list entries")
	}
	var present, declared uint64
	for _, e := range man.Entries {
		declared += e.Size
		eb := s.lifeFind(a, "blob", e.BlobID)
		if eb == nil || textField(eb, "state") != "ready" || textField(eb, "sha256") != trimDigest(e.SHA256) || uintField(eb, "size_bytes") != e.Size || !s.hasObject(trimDigest(e.SHA256)) {
			continue
		}
		present += e.Size
	}
	expected := declared
	if _, ok := m["expected_bytes"]; ok {
		expected = uintField(m, "expected_bytes")
	}
	m["expected_bytes"] = jsonNumber(expected)
	m["present_bytes"] = jsonNumber(present)
	m["manifest_blob_id"] = manifestBlob
	m["download_blob_id"] = man.Entries[0].BlobID
	if present == expected && present == declared {
		m["state"] = "complete"
	} else {
		m["state"] = "incomplete"
	}
	editResource(m)
	return nil
}

func (s *Service) planArtifactFinalize(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "artifact", textField(in, "artifact_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if textField(m, "state") != "collecting" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "artifact is already finalized")
	}
	if err := s.finalizeArtifact(a, m, textField(in, "manifest_blob_id")); err != nil {
		return lifePlan{}, err
	}
	return lifePlan{ref: lifeRef("artifact", textField(m, "id")), result: map[string]any{}}, nil
}

func (s *Service) planArtifactDelete(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "artifact", textField(in, "artifact_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	s.deleteArtifact(m)
	return lifePlan{ref: lifeRef("artifact", textField(m, "id")), result: map[string]any{}}, nil
}

// deleteArtifact deletes an artifact and the blobs only it holds; a blob that
// another live artifact also references stays ready. Under mu.
func (s *Service) deleteArtifact(m map[string]any) {
	m["state"] = "deleted"
	inUse := map[string]bool{}
	for id, other := range s.life.Artifacts {
		if id == textField(m, "id") || textField(other.R, "state") == "deleted" {
			continue
		}
		for _, blob := range s.artifactBlobIDs(other.R) {
			inUse[blob] = true
		}
	}
	for _, blob := range s.artifactBlobIDs(m) {
		if b := s.life.Blobs[blob]; b != nil && !inUse[blob] && textField(b.R, "state") != "deleted" {
			b.R["state"] = "deleted"
			editResource(b.R)
		}
	}
	editResource(m)
}

// artifactBlobIDs lists every blob an artifact holds: its manifest, its
// download blob and each manifest entry. Under mu.
func (s *Service) artifactBlobIDs(m map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	add(textField(m, "manifest_blob_id"))
	add(textField(m, "download_blob_id"))
	if b := s.life.Blobs[textField(m, "manifest_blob_id")]; b != nil {
		if raw, err := s.readObject(textField(b.R, "sha256")); err == nil {
			var man artifactManifest
			if json.Unmarshal(raw, &man) == nil {
				for _, e := range man.Entries {
					add(e.BlobID)
				}
			}
		}
	}
	return out
}

// sweepStorage applies collection deadlines and retention. Under mu.
func (s *Service) sweepStorage(now time.Time) {
	for _, ar := range s.life.Artifacts {
		m := ar.R
		switch textField(m, "state") {
		case "collecting":
			created, _ := time.Parse(time.RFC3339Nano, textField(m, "created_at"))
			if now.Sub(created) >= artifactCollection {
				// Teardown never waits on incomplete output.
				m["state"] = "incomplete"
				editResource(m)
			}
		case "complete", "incomplete":
			if exp, err := time.Parse(time.RFC3339Nano, textField(m, "expires_at")); err == nil && !now.Before(exp) {
				s.deleteArtifact(m)
			}
		}
	}
	for id, b := range s.life.Blobs {
		if textField(b.R, "state") == "uploading" {
			created, _ := time.Parse(time.RFC3339Nano, textField(b.R, "created_at"))
			if now.Sub(created) > 24*time.Hour {
				b.R["state"] = "failed"
				editResource(b.R)
				os.Remove(s.blobTemp(id))
			}
		}
		if exp, err := time.Parse(time.RFC3339Nano, textField(b.R, "expires_at")); err == nil && !now.Before(exp) && textField(b.R, "state") == "ready" {
			b.R["state"] = "deleted"
			editResource(b.R)
		}
	}
	s.collectObjects()
}
