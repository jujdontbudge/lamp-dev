package control

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Owner data recovery (architecture §7). Every backup gets a fresh random
// data key. It is wrapped once for this host's protected backup key and once
// for the owner's offline recovery public key (X25519 + HKDF-SHA256 +
// AES-256-GCM), so a clean replacement OS holding only the owner's recovery
// material can restore the archive with the original host and its key store
// gone. The recovery private key never enters the API, journal or archive.

const backupMagicV2 = "LAMPBK2\n"
const recoveryPublicPrefix = "lamp-recovery-v1 "
const recoveryKeyFormat = "lamp.recovery-key/v1"
const wrapHost = "A256GCM"
const wrapRecovery = "X25519-HKDF-SHA256-A256GCM"

// RecoveryKey is the owner's offline recovery material file.
type RecoveryKey struct {
	Format     string `json:"format"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

type keyEnvelope struct {
	RecipientKind string `json:"recipient_kind"`
	RecipientID   string `json:"recipient_id"`
	KeyID         string `json:"key_id"`
	Algorithm     string `json:"wrapping_algorithm"`
	Ephemeral     string `json:"ephemeral_public_key,omitempty"`
	Nonce         string `json:"nonce"`
	Wrapped       string `json:"wrapped_key"`
}

type envelopeSet struct {
	Format    string        `json:"format"`
	BackupID  string        `json:"backup_id"`
	Envelopes []keyEnvelope `json:"envelopes"`
}

// NewRecoveryKey generates owner recovery material and its public line.
func NewRecoveryKey() (RecoveryKey, string, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return RecoveryKey{}, "", err
	}
	pub := base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())
	return RecoveryKey{Format: recoveryKeyFormat, PrivateKey: base64.StdEncoding.EncodeToString(priv.Bytes()), PublicKey: pub}, recoveryPublicPrefix + pub + "\n", nil
}

// RecoveryFingerprint is SHA-256 of the canonical public-key bytes, base64.
func RecoveryFingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func readRecoveryPublic(path string) (*ecdh.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, recoveryPublicPrefix) {
		return nil, errors.New("recovery public key file is not lamp-recovery-v1")
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, recoveryPublicPrefix))
	if err != nil {
		return nil, err
	}
	return ecdh.X25519().NewPublicKey(b)
}

func readRecoveryPrivate(path string) (*ecdh.PrivateKey, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0077 != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("recovery key file must be a private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var k RecoveryKey
	if json.Unmarshal(raw, &k) != nil || k.Format != recoveryKeyFormat {
		return nil, errors.New("not a lamp recovery key")
	}
	b, err := base64.StdEncoding.DecodeString(k.PrivateKey)
	if err != nil {
		return nil, errors.New("not a lamp recovery key")
	}
	return ecdh.X25519().NewPrivateKey(b)
}

func hostKeyID(key []byte) string {
	sum := sha256.Sum256(append([]byte("lamp host backup key\n"), key...))
	return hex.EncodeToString(sum[:8])
}

func wrapAAD(backupID string, e keyEnvelope) []byte {
	return []byte("lamp-backup-dek/v1\n" + backupID + "\n" + e.RecipientKind + "\n" + e.KeyID)
}

func gcmFor(key []byte) cipher.AEAD {
	block, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(block)
	return g
}

func recoveryKEK(shared, ephemeral, recipient []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, shared, append(append([]byte{}, ephemeral...), recipient...), "lamp backup recovery v1", 32)
}

func (s *Service) hostEnvelope(backupID string, dek []byte) (keyEnvelope, error) {
	key, err := s.backupKey()
	if err != nil {
		return keyEnvelope{}, err
	}
	e := keyEnvelope{RecipientKind: "host", RecipientID: s.hostID(), KeyID: hostKeyID(key), Algorithm: wrapHost}
	nonce := make([]byte, 12)
	rand.Read(nonce)
	e.Nonce = base64.StdEncoding.EncodeToString(nonce)
	e.Wrapped = base64.StdEncoding.EncodeToString(gcmFor(key).Seal(nil, nonce, dek, wrapAAD(backupID, e)))
	return e, nil
}

func recoveryEnvelope(backupID string, dek []byte, pub *ecdh.PublicKey) (keyEnvelope, error) {
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return keyEnvelope{}, err
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		return keyEnvelope{}, err
	}
	kek, err := recoveryKEK(shared, eph.PublicKey().Bytes(), pub.Bytes())
	if err != nil {
		return keyEnvelope{}, err
	}
	fp := RecoveryFingerprint(pub.Bytes())
	e := keyEnvelope{RecipientKind: "owner_recovery", RecipientID: "owner", KeyID: fp, Algorithm: wrapRecovery, Ephemeral: base64.StdEncoding.EncodeToString(eph.PublicKey().Bytes())}
	nonce := make([]byte, 12)
	rand.Read(nonce)
	e.Nonce = base64.StdEncoding.EncodeToString(nonce)
	e.Wrapped = base64.StdEncoding.EncodeToString(gcmFor(kek).Seal(nil, nonce, dek, wrapAAD(backupID, e)))
	return e, nil
}

func unwrapEnvelope(backupID string, e keyEnvelope, hostKey []byte, recovery *ecdh.PrivateKey) ([]byte, error) {
	nonce, err1 := base64.StdEncoding.DecodeString(e.Nonce)
	wrapped, err2 := base64.StdEncoding.DecodeString(e.Wrapped)
	if err1 != nil || err2 != nil || len(nonce) != 12 {
		return nil, errors.New("envelope damaged")
	}
	var kek []byte
	switch {
	case e.RecipientKind == "host" && e.Algorithm == wrapHost && hostKey != nil && e.KeyID == hostKeyID(hostKey):
		kek = hostKey
	case e.RecipientKind == "owner_recovery" && e.Algorithm == wrapRecovery && recovery != nil && e.KeyID == RecoveryFingerprint(recovery.PublicKey().Bytes()):
		eph, err := base64.StdEncoding.DecodeString(e.Ephemeral)
		if err != nil {
			return nil, errors.New("envelope damaged")
		}
		pub, err := ecdh.X25519().NewPublicKey(eph)
		if err != nil {
			return nil, errors.New("envelope damaged")
		}
		shared, err := recovery.ECDH(pub)
		if err != nil {
			return nil, err
		}
		if kek, err = recoveryKEK(shared, eph, recovery.PublicKey().Bytes()); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("no key for this envelope")
	}
	dek, err := gcmFor(kek).Open(nil, nonce, wrapped, wrapAAD(backupID, e))
	if err != nil || len(dek) != 32 {
		return nil, errors.New("envelope authentication failed")
	}
	return dek, nil
}

// archiveEnvelopes seals a fresh data key to this host and, when enrolled,
// to the owner's recovery key. It returns the data key and archive preamble.
func (s *Service) archiveEnvelopes(backupID string) ([]byte, []byte, []keyEnvelope, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, nil, nil, err
	}
	host, err := s.hostEnvelope(backupID, dek)
	if err != nil {
		return nil, nil, nil, err
	}
	envs := []keyEnvelope{host}
	if s.recoveryPublic != nil {
		rec, err := recoveryEnvelope(backupID, dek, s.recoveryPublic)
		if err != nil {
			return nil, nil, nil, err
		}
		envs = append(envs, rec)
	}
	return dek, encodePreamble(backupID, envs), envs, nil
}

func encodePreamble(backupID string, envs []keyEnvelope) []byte {
	raw, _ := json.Marshal(envelopeSet{Format: "lamp.backup-envelopes/v1", BackupID: backupID, Envelopes: envs})
	var b bytes.Buffer
	b.WriteString(backupMagicV2)
	binary.Write(&b, binary.BigEndian, uint32(len(raw)))
	b.Write(raw)
	return b.Bytes()
}

// splitArchive returns the envelopes (nil for v1) and the chunk body that
// starts with the 4-byte nonce prefix.
func splitArchive(raw []byte) (*envelopeSet, []byte, error) {
	if bytes.HasPrefix(raw, []byte(backupMagic)) {
		return nil, raw[len(backupMagic):], nil
	}
	if !bytes.HasPrefix(raw, []byte(backupMagicV2)) || len(raw) < len(backupMagicV2)+4 {
		return nil, nil, errors.New("not a sealed archive")
	}
	rest := raw[len(backupMagicV2):]
	n := binary.BigEndian.Uint32(rest)
	if uint64(n) > uint64(len(rest)-4) || n > 1<<20 {
		return nil, nil, errors.New("archive truncated")
	}
	var set envelopeSet
	if json.Unmarshal(rest[4:4+n], &set) != nil || set.Format != "lamp.backup-envelopes/v1" {
		return nil, nil, errors.New("envelopes damaged")
	}
	return &set, rest[4+n:], nil
}

// archiveKey selects the data key this host can unwrap.
func (s *Service) archiveKey(id string, set *envelopeSet, recovery *ecdh.PrivateKey) ([]byte, error) {
	hostKey, err := s.backupKey()
	if err != nil {
		return nil, errors.New("backup key unavailable")
	}
	if set == nil {
		return hostKey, nil
	}
	if set.BackupID != id {
		return nil, errors.New("envelopes belong to another backup")
	}
	for _, e := range set.Envelopes {
		if dek, err := unwrapEnvelope(id, e, hostKey, recovery); err == nil {
			return dek, nil
		}
	}
	return nil, errors.New("no envelope opens with this host's key")
}

func resourceEnvelopes(envs []keyEnvelope) []any {
	out := []any{}
	for _, e := range envs {
		raw, _ := json.Marshal(e)
		sum := sha256.Sum256(raw)
		out = append(out, map[string]any{"recipient_kind": e.RecipientKind, "recipient_id": e.RecipientID, "key_id": e.KeyID, "wrapping_algorithm": e.Algorithm, "envelope_digest": "sha256:" + hex.EncodeToString(sum[:])})
	}
	return out
}

func recoveryState(envs []keyEnvelope) string {
	for _, e := range envs {
		if e.RecipientKind == "owner_recovery" {
			return "available"
		}
	}
	// No independent key path: visibly nonrecoverable if this host is lost.
	return "unavailable"
}

// importRecovered adopts sealed archives on a replacement host. Each one is
// opened with the owner's recovery key and verified completely, then its data
// key is rewrapped to this host's new key and it becomes an ordinary ready
// backup. backup_restore then restores it as usual. Runs before serving.
func (s *Service) importRecovered(keyFile, dir string) error {
	priv, err := readRecoveryPrivate(keyFile)
	if err != nil {
		return fmt.Errorf("recovery key: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	changed := false
	for _, ent := range entries {
		if !strings.HasSuffix(ent.Name(), ".lbk") {
			continue
		}
		id := strings.TrimSuffix(ent.Name(), ".lbk")
		if _, ok := s.life.Backups[id]; ok {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			return err
		}
		set, body, err := splitArchive(raw)
		if err != nil || set == nil {
			return fmt.Errorf("%s: %v", ent.Name(), errors.Join(err, errors.New("no recovery envelope")))
		}
		var rec *keyEnvelope
		for i := range set.Envelopes {
			if set.Envelopes[i].RecipientKind == "owner_recovery" {
				rec = &set.Envelopes[i]
			}
		}
		if rec == nil {
			return fmt.Errorf("%s: no owner recovery envelope", ent.Name())
		}
		dek, err := unwrapEnvelope(id, *rec, nil, priv)
		if err != nil {
			return fmt.Errorf("%s: %v", ent.Name(), err)
		}
		header, digest, err := decryptBody(id, body, dek)
		if err != nil {
			return fmt.Errorf("%s: %v", ent.Name(), err)
		}
		host, err := s.hostEnvelope(id, dek)
		if err != nil {
			return err
		}
		envs := []keyEnvelope{host, *rec}
		file := filepath.Join(s.dir, "backups", id+".lbk")
		if err := writeDurable(file, append(encodePreamble(id, envs), body...)); err != nil {
			return err
		}
		m := newResource("backup", s.journal.Control.OwnerID)
		m["id"] = id
		m["state"] = "ready"
		m["resource_kind"] = header.Kind
		m["resource_id"] = textField(header.Resource, "id")
		m["target_host_ids"] = []any{s.hostID()}
		m["custody_host_ids"] = []any{s.hostID()}
		m["captured_at"] = header.CapturedAt
		m["consistency"] = "application"
		m["manifest_digest"] = digest
		m["authority_epoch"] = jsonNumber(header.Epoch)
		m["desired_replicas"] = jsonNumber(1)
		m["durability"] = "backup"
		m["key_envelopes"] = resourceEnvelopes(envs)
		m["recovery_state"] = "available"
		m["labels"] = map[string]any{"lamp.recovered": "owner-recovery-key"}
		s.life.Backups[id] = &lifeBackup{R: m, File: file}
		changed = true
	}
	if changed {
		return s.saveLife()
	}
	return nil
}

func writeDurable(file string, data []byte) error {
	f, err := os.OpenFile(file+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(file + ".tmp")
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := fullSync(f); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if err := os.Rename(file+".tmp", file); err != nil {
		return err
	}
	return syncDir(filepath.Dir(file))
}
