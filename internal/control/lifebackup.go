package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	"lamp.eilmer.si/pkg/api"
)

// Backups (STO-005) are sealed archives: AES-256-GCM in 1 MiB chunks under
// a per-backup data key wrapped for this host and the owner recovery key (recovery.go), each chunk bound to the backup ID, its index and whether
// it is last, so truncation, reordering or substitution fail decryption.
// A backup restores only through its own capture point, and a missing or
// damaged object fails the restore instead of producing empty content.

const backupMagic = "LAMPBK1\n"
const backupChunk = 1 << 20

type backupHeader struct {
	Format     string           `json:"format"`
	BackupID   string           `json:"backup_id"`
	Kind       string           `json:"kind"`
	Resource   map[string]any   `json:"resource"`
	Tree       tree             `json:"tree,omitempty"`
	Blobs      []map[string]any `json:"blobs,omitempty"`
	Objects    []string         `json:"objects"`
	CapturedAt string           `json:"captured_at"`
	Epoch      uint64           `json:"authority_epoch"`
}

func (s *Service) backupKey() ([]byte, error) {
	p := filepath.Join(s.dir, "backup.key")
	if key, err := os.ReadFile(p); err == nil {
		if len(key) != 32 {
			return nil, errors.New("invalid backup key")
		}
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err == nil {
		err = fullSync(f)
	}
	f.Close()
	if err != nil {
		return nil, err
	}
	return key, syncDir(s.dir)
}

func (s *Service) planBackupCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := onlyFields(in, "resource_kind resource_id target_host_ids consistency desired_replicas durability"); err != nil {
		return lifePlan{}, err
	}
	for _, h := range stringsField(in, "target_host_ids") {
		if h != s.hostID() {
			return lifePlan{}, reject("HOST_NOT_TRUSTED", "backup target "+h+" is not an enrolled storage host here")
		}
	}
	if uintField(in, "desired_replicas") > 1 {
		return lifePlan{}, reject("QUORUM_REQUIRED", "more than one replica needs a second storage host")
	}
	kind := textField(in, "resource_kind")
	res, err := s.ownedOrMissing(a, kind, textField(in, "resource_id"))
	if kind != "volume" && kind != "cache" && kind != "artifact" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "backups cover volumes, caches and artifacts")
	}
	if err != nil {
		return lifePlan{}, err
	}
	resID := textField(res, "id")
	m := s.lifeResource("bk", "backup", a.Principal)
	id := textField(m, "id")
	h := backupHeader{Format: "lamp.backup/v1", BackupID: id, Kind: kind, CapturedAt: timestamp(), Epoch: 1}
	raw, _ := json.Marshal(res)
	decodeNumbers(raw, &h.Resource)
	switch kind {
	case "volume":
		// The last completed save is the volume's committed tree; a live
		// writer's unsaved guest changes are not part of it.
		h.Tree = s.life.Volumes[resID].Tree
	case "cache":
		h.Tree = s.life.Caches[resID].generation(textField(res, "generation_digest")).Tree
	case "artifact":
		// The manifest and every output it lists.
		for _, blob := range s.artifactBlobIDs(res) {
			if b := s.life.Blobs[blob]; b != nil && textField(b.R, "state") == "ready" {
				h.Blobs = append(h.Blobs, b.R)
				h.Objects = append(h.Objects, textField(b.R, "sha256"))
			}
		}
	}
	for _, e := range h.Tree {
		if e.SHA256 != "" {
			h.Objects = append(h.Objects, e.SHA256)
		}
	}
	m["state"] = "capturing"
	m["resource_kind"] = kind
	m["resource_id"] = resID
	m["target_host_ids"] = []any{s.hostID()}
	m["captured_at"] = h.CapturedAt
	consistency := textField(in, "consistency")
	if consistency == "" {
		consistency = "application"
	}
	m["consistency"] = consistency
	m["authority_epoch"] = jsonNumber(1)
	m["desired_replicas"] = jsonNumber(1)
	m["durability"] = "backup"
	m["custody_host_ids"] = []any{s.hostID()}
	applyLabels(m, in)
	file := filepath.Join(s.dir, "backups", id+".lbk")
	s.life.Backups[id] = &lifeBackup{R: m, File: file}
	if kind == "volume" {
		res["backup_id"] = id
	}
	var digest string
	var envs []keyEnvelope
	return lifePlan{ref: lifeRef("backup", id), timeout: 30 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			var err error
			digest, envs, err = s.sealBackup(h, file)
			return map[string]any{"backup_id": id}, err
		},
		finish: func(_ map[string]any, err error) error {
			if err != nil {
				m["state"] = "failed"
			} else {
				m["state"] = "ready"
				m["manifest_digest"] = digest
				m["key_envelopes"] = resourceEnvelopes(envs)
				m["recovery_state"] = recoveryState(envs)
			}
			editResource(m)
			return err
		}}, nil
}

// sealBackup writes envelopes + header + objects as authenticated encrypted
// chunks under a fresh data key and returns the manifest digest. The file is
// durable before it is reported.
func (s *Service) sealBackup(h backupHeader, file string) (string, []keyEnvelope, error) {
	key, preamble, envs, err := s.archiveEnvelopes(h.BackupID)
	if err != nil {
		return "", nil, err
	}
	header, _ := json.Marshal(h)
	var plain bytes.Buffer
	binary.Write(&plain, binary.BigEndian, uint32(len(header)))
	plain.Write(header)
	for _, sum := range h.Objects {
		data, err := s.readObject(sum)
		if err != nil {
			return "", nil, err
		}
		raw, _ := hex.DecodeString(sum)
		plain.Write(raw)
		binary.Write(&plain, binary.BigEndian, uint64(len(data)))
		plain.Write(data)
	}
	gcm := gcmFor(key)
	prefix := make([]byte, 4)
	rand.Read(prefix)
	var out bytes.Buffer
	out.Write(preamble)
	out.Write(prefix)
	data := plain.Bytes()
	for i := 0; ; i++ {
		end := min((i+1)*backupChunk, len(data))
		last := end == len(data)
		sealed := gcm.Seal(nil, chunkNonce(prefix, i), data[i*backupChunk:end], chunkAAD(h.BackupID, i, last))
		binary.Write(&out, binary.BigEndian, uint32(len(sealed)))
		out.Write(sealed)
		if last {
			break
		}
	}
	if err := writeDurable(file, out.Bytes()); err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(header)
	return "sha256:" + hex.EncodeToString(sum[:]), envs, nil
}

func chunkNonce(prefix []byte, i int) []byte {
	n := make([]byte, 12)
	copy(n, prefix)
	binary.BigEndian.PutUint64(n[4:], uint64(i))
	return n
}
func chunkAAD(id string, i int, last bool) []byte {
	b := []byte(id)
	b = binary.BigEndian.AppendUint64(b, uint64(i))
	if last {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	return b
}

// openBackup decrypts and verifies a sealed backup completely.
func (s *Service) openBackup(id, file, wantDigest string) (backupHeader, map[string][]byte, error) {
	fail := func(why string) (backupHeader, map[string][]byte, error) {
		return backupHeader{}, nil, reject("DATA_UNAVAILABLE", "backup cannot be restored: "+why)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return fail("sealed archive is missing")
	}
	set, body, err := splitArchive(raw)
	if err != nil {
		return fail(err.Error())
	}
	key, err := s.archiveKey(id, set, nil)
	if err != nil {
		return fail(err.Error())
	}
	h, digest, objects, err := decryptArchive(id, body, key)
	if err != nil {
		return fail(err.Error())
	}
	if digest != wantDigest {
		return fail("manifest digest differs")
	}
	return h, objects, nil
}

func decryptBody(id string, body, key []byte) (backupHeader, string, error) {
	h, digest, _, err := decryptArchive(id, body, key)
	return h, digest, err
}

// decryptArchive authenticates every chunk and object under the data key.
func decryptArchive(id string, body, key []byte) (backupHeader, string, map[string][]byte, error) {
	var h backupHeader
	fail := func(why string) (backupHeader, string, map[string][]byte, error) {
		return h, "", nil, errors.New(why)
	}
	if len(body) < 4 {
		return fail("archive truncated")
	}
	prefix := body[:4]
	r := bytes.NewReader(body[4:])
	gcm := gcmFor(key)
	var plain bytes.Buffer
	for i := 0; ; i++ {
		var n uint32
		if err := binary.Read(r, binary.BigEndian, &n); err != nil {
			return fail("archive truncated")
		}
		sealed := make([]byte, n)
		if _, err := io.ReadFull(r, sealed); err != nil {
			return fail("archive truncated")
		}
		last := r.Len() == 0
		chunk, err := gcm.Open(nil, chunkNonce(prefix, i), sealed, chunkAAD(id, i, last))
		if err != nil {
			return fail("authentication failed")
		}
		plain.Write(chunk)
		if last {
			break
		}
	}
	var hl uint32
	if binary.Read(&plain, binary.BigEndian, &hl) != nil || int(hl) > plain.Len() {
		return fail("header damaged")
	}
	header := plain.Next(int(hl))
	sum := sha256.Sum256(header)
	if decodeNumbers(header, &h) != nil || h.BackupID != id {
		return fail("header damaged")
	}
	objects := map[string][]byte{}
	for plain.Len() > 0 {
		digest := hex.EncodeToString(plain.Next(32))
		var n uint64
		if binary.Read(&plain, binary.BigEndian, &n) != nil || n > uint64(plain.Len()) {
			return fail("object framing damaged")
		}
		data := append([]byte(nil), plain.Next(int(n))...)
		got := sha256.Sum256(data)
		if hex.EncodeToString(got[:]) != digest {
			return fail("object " + digest[:12] + " damaged")
		}
		objects[digest] = data
	}
	for _, want := range h.Objects {
		if _, ok := objects[want]; !ok {
			return fail("object " + want[:12] + " missing")
		}
	}
	return h, "sha256:" + hex.EncodeToString(sum[:]), objects, nil
}

func (s *Service) planBackupRestore(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "backup", textField(in, "backup_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if t := textField(in, "target_host_id"); t != "" && t != s.hostID() {
		return lifePlan{}, reject("HOST_NOT_TRUSTED", "restore target is not this host")
	}
	if textField(in, "recovery_credential_source_id") != "" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "recovered archives are imported with the owner's recovery key at lampd start; no recovery credential is needed here")
	}
	if textField(m, "state") != "ready" {
		return lifePlan{}, reject("DATA_UNAVAILABLE", "backup is not ready")
	}
	epoch := uintField(m, "authority_epoch")
	next := epoch + 1
	if _, ok := in["new_authority_epoch"]; ok {
		next = uintField(in, "new_authority_epoch")
		if next <= epoch {
			return lifePlan{}, reject("REVISION_CONFLICT", "new_authority_epoch must exceed the backup's epoch")
		}
	}
	id := textField(m, "id")
	file := s.life.Backups[id].File
	digest := textField(m, "manifest_digest")
	var h backupHeader
	var objects map[string][]byte
	var restored api.ResourceRef
	plan := lifePlan{ref: lifeRef("backup", id), timeout: 30 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			var err error
			h, objects, err = s.openBackup(id, file, digest)
			if err != nil {
				return nil, err
			}
			for sum, data := range objects {
				if got, err := s.putObject(data); err != nil || got != sum {
					return nil, reject("DATA_UNAVAILABLE", "cannot store restored object")
				}
			}
			return map[string]any{}, nil
		},
		finish: func(_ map[string]any, err error) error {
			if err != nil {
				return err
			}
			restored = s.restoreResource(a, h, id, next)
			m["authority_epoch"] = jsonNumber(next)
			editResource(m)
			return nil
		}}
	plan.more = func() []api.ResourceRef {
		if restored.Id == "" {
			return nil
		}
		return []api.ResourceRef{restored}
	}
	return plan, nil
}

// restoreResource republishes a verified backup as a resource. The original
// ID is reused only when that resource is deleted; otherwise a new one is made.
func (s *Service) restoreResource(a caller, h backupHeader, backupID string, epoch uint64) api.ResourceRef {
	m := h.Resource
	orig := textField(m, "id")
	now := timestamp()
	reuse := false
	switch h.Kind {
	case "volume":
		v := s.life.Volumes[orig]
		reuse = v == nil || textField(v.R, "state") == "deleted"
	case "cache":
		c := s.life.Caches[orig]
		reuse = c == nil || textField(c.R, "state") == "deleted"
	case "artifact":
		ar := s.life.Artifacts[orig]
		reuse = ar == nil || textField(ar.R, "state") == "deleted"
	}
	prefix := map[string]string{"volume": "vo", "cache": "ca", "artifact": "ar"}[h.Kind]
	id := orig
	if !reuse {
		id = s.resourceID(prefix)
	}
	m["id"] = id
	m["owner_id"] = a.Principal
	m["revision"] = jsonNumber(1)
	m["created_at"] = now
	m["updated_at"] = now
	switch h.Kind {
	case "volume":
		m["state"] = "ready"
		m["attachment_ids"] = []any{}
		m["backup_id"] = backupID
		d := treeDigest(h.Tree)
		s.life.Volumes[id] = &lifeVolume{R: m, Generation: 1, Digest: d, Tree: h.Tree, History: map[string]tree{d: h.Tree}}
	case "cache":
		m["state"] = "active"
		d := treeDigest(h.Tree)
		m["generation_digest"] = d
		s.life.Caches[id] = &lifeCache{R: m, Generations: []cacheGeneration{{Digest: d, Tree: h.Tree, Bytes: treeBytes(h.Tree), Created: now}}}
	case "artifact":
		ids := map[string]string{}
		manifest := textField(m, "manifest_blob_id")
		for _, b := range h.Blobs {
			if textField(b, "id") == manifest {
				continue
			}
			nb := s.newReadyBlob(a.Principal, textField(b, "sha256"), int64(uintField(b, "size_bytes")), textField(b, "media_type"), "")
			ids[textField(b, "id")] = textField(nb, "id")
		}
		// The manifest names its outputs by blob id; republish it with the
		// restored blobs' ids.
		rewritten := false
		for _, b := range h.Blobs {
			if textField(b, "id") != manifest {
				continue
			}
			raw, err := s.readObject(textField(b, "sha256"))
			var man map[string]any
			if err != nil || decodeNumbers(raw, &man) != nil {
				break
			}
			entries, _ := man["entries"].([]any)
			for _, e := range entries {
				if em, ok := e.(map[string]any); ok {
					if n, ok := ids[textField(em, "blob_id")]; ok {
						em["blob_id"] = n
					}
				}
			}
			out, _ := json.Marshal(man)
			if sum, err := s.putObject(out); err == nil {
				nb := s.newReadyBlob(a.Principal, sum, int64(len(out)), "application/json", "")
				ids[manifest] = textField(nb, "id")
				rewritten = true
			}
		}
		for _, k := range []string{"download_blob_id", "manifest_blob_id"} {
			if n, ok := ids[textField(m, k)]; ok {
				m[k] = n
			}
		}
		if !rewritten && manifest != "" {
			m["state"] = "incomplete"
		} else if textField(m, "state") == "deleted" {
			m["state"] = "complete"
		}
		m["expires_at"] = s.lifeNow().Add(defaultArtifactRetain).Format(time.RFC3339Nano)
		s.life.Artifacts[id] = &lifeArtifact{R: m}
	}
	return lifeRef(h.Kind, id)
}

func (s *Service) planBackupDelete(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "backup", textField(in, "backup_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	m["state"] = "deleted"
	editResource(m)
	os.Remove(s.life.Backups[textField(m, "id")].File)
	syncDir(filepath.Join(s.dir, "backups"))
	return lifePlan{ref: lifeRef("backup", textField(m, "id")), result: map[string]any{}}, nil
}
