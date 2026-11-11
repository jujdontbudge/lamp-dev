// Package release verifies signed Lamp releases.
//
// A release version is a directory holding index.json and index.sig (Ed25519
// over the index bytes by the release key) and one subdirectory per platform
// ("linux-amd64", "darwin-arm64") with its own signed manifest.json,
// manifest.sig and payload/. The index names every platform manifest by
// SHA-256, so its digest identifies the whole multi-platform release; that is
// the Update resource's release_digest.
package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const ManifestFormat = "lamp.release/v1"
const IndexFormat = "lamp.release-index/v1"
const TrustPrefix = "lamp-release-ed25519 "

// RootVersion is the generation of the pinned release key.
const RootVersion = 1

type FileEntry struct {
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
}

type Component struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Sum     string `json:"sum,omitempty"`
}

type Manifest struct {
	Format     string                 `json:"format"`
	Version    string                 `json:"version"`
	Channel    string                 `json:"channel"`
	OS         string                 `json:"os"`
	Arch       string                 `json:"arch"`
	CreatedAt  string                 `json:"created_at"`
	Files      map[string]FileEntry   `json:"files"`
	Components map[string][]Component `json:"components"`
	// Engine identifies the VM engine the release runs (worker and VMM
	// binary digests, snapshot format). Saved machine state is bound to it,
	// so the updater refuses an engine change while such state exists.
	Engine map[string]string `json:"engine,omitempty"`
}

type Revocation struct {
	Digest      string `json:"digest"`
	EffectiveAt string `json:"effective_at"`
	Reason      string `json:"reason"`
}

// Migration describes the release's state formats, as shipped and signed.
type Migration struct {
	LogVersion        uint64 `json:"log_version"`
	SnapshotVersion   uint64 `json:"snapshot_version"`
	ProjectionVersion uint64 `json:"projection_version"`
	Phase             string `json:"phase"`
	RollbackSafe      bool   `json:"rollback_safe"`
}

type Index struct {
	Format          string            `json:"format"`
	Version         string            `json:"version"`
	Channel         string            `json:"channel"`
	MetadataVersion uint64            `json:"metadata_version"`
	CreatedAt       string            `json:"created_at"`
	Platforms       map[string]string `json:"platforms"` // "os-arch" -> manifest SHA-256
	Revocations     []Revocation      `json:"security_revocations"`
	Migration       Migration         `json:"migration"`
}

func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

func ReadTrust(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, TrustPrefix) {
		return nil, errors.New("not a lamp release trust file")
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, TrustPrefix))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("not a lamp release trust file")
	}
	return b, nil
}

func ParseVersion(v string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("version %q is not a.b.c", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("version %q is not a.b.c", v)
		}
		out[i] = n
	}
	return out, nil
}

// Newer reports whether a is a later version than b.
func Newer(a, b string) bool {
	x, _ := ParseVersion(a)
	y, _ := ParseVersion(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func verifySig(trust ed25519.PublicKey, raw []byte, sigPath string) error {
	sigRaw, err := os.ReadFile(sigPath)
	if err != nil {
		return errors.New("release is unsigned")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
	if err != nil || !ed25519.Verify(trust, raw, sig) {
		return errors.New("release signature does not verify against the pinned release key")
	}
	return nil
}

// VerifyIndex checks a release version directory's signed index and returns
// it with its digest.
func VerifyIndex(dir string, trust ed25519.PublicKey) (Index, string, error) {
	var ix Index
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return ix, "", err
	}
	if err := verifySig(trust, raw, filepath.Join(dir, "index.sig")); err != nil {
		return ix, "", err
	}
	if err := json.Unmarshal(raw, &ix); err != nil || ix.Format != IndexFormat {
		return ix, "", errors.New("unknown release index format")
	}
	if _, err := ParseVersion(ix.Version); err != nil {
		return ix, "", err
	}
	return ix, Digest(raw), nil
}

// VerifyPlatform checks this host's platform release inside a version
// directory: index trust, the manifest digest named by the index, the manifest
// signature and every payload file.
func VerifyPlatform(dir string, trust ed25519.PublicKey) (Index, Manifest, string, error) {
	ix, digest, err := VerifyIndex(dir, trust)
	if err != nil {
		return ix, Manifest{}, "", err
	}
	want, ok := ix.Platforms[Platform()]
	if !ok {
		return ix, Manifest{}, "", fmt.Errorf("release %s has no %s build", ix.Version, Platform())
	}
	m, err := VerifyManifest(filepath.Join(dir, Platform()), trust, want)
	if err == nil && (m.Version != ix.Version || m.Channel != ix.Channel) {
		err = errors.New("platform manifest does not match its index")
	}
	return ix, m, digest, err
}

// VerifyManifest checks one platform directory. want, when set, is the
// manifest digest the index promised.
func VerifyManifest(dir string, trust ed25519.PublicKey, want string) (Manifest, error) {
	return verifyManifestAt(dir, filepath.Join(dir, "payload"), trust, want)
}

// VerifyStaged checks an installed release directory (<prefix>/releases/<v>):
// its signed manifest and every staged file, which sit beside the manifest
// rather than under payload/.
func VerifyStaged(dir string, trust ed25519.PublicKey, want string) (Manifest, error) {
	return verifyManifestAt(dir, dir, trust, want)
}

// ManifestDigest is the digest an index names for the manifest in dir.
func ManifestDigest(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", err
	}
	return Digest(raw), nil
}

// FindByVersion returns the mirrored release of a version whose signed index
// verifies.
func FindByVersion(mirror string, trust ed25519.PublicKey, version string) (Index, string, error) {
	entries, _ := os.ReadDir(mirror)
	for _, e := range entries {
		ix, digest, err := VerifyIndex(filepath.Join(mirror, e.Name()), trust)
		if err == nil && ix.Version == version {
			return ix, digest, nil
		}
	}
	return Index{}, "", fmt.Errorf("no signed release %s in the host's release mirror", version)
}

// Revocations gathers the security revocations of every verified index in
// the mirror: a later release can withdraw an earlier one.
func Revocations(mirror string, trust ed25519.PublicKey) map[string]Revocation {
	out := map[string]Revocation{}
	entries, _ := os.ReadDir(mirror)
	for _, e := range entries {
		ix, _, err := VerifyIndex(filepath.Join(mirror, e.Name()), trust)
		if err != nil {
			continue
		}
		for _, r := range ix.Revocations {
			out[r.Digest] = r
		}
	}
	return out
}

// CheckRevoked refuses a release digest that any verified index revokes.
func CheckRevoked(mirror string, trust ed25519.PublicKey, digest string) error {
	if r, ok := Revocations(mirror, trust)[digest]; ok {
		return fmt.Errorf("release %s is revoked (effective %s): %s", digest, r.EffectiveAt, r.Reason)
	}
	return nil
}

func verifyManifestAt(dir, payload string, trust ed25519.PublicKey, want string) (Manifest, error) {
	var m Manifest
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, err
	}
	if want != "" && Digest(raw) != want {
		return m, errors.New("platform manifest differs from the signed index")
	}
	if err := verifySig(trust, raw, filepath.Join(dir, "manifest.sig")); err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil || m.Format != ManifestFormat {
		return m, errors.New("unknown release manifest format")
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return m, err
	}
	if m.OS != runtime.GOOS || m.Arch != runtime.GOARCH {
		return m, fmt.Errorf("release is for %s/%s, host is %s/%s", m.OS, m.Arch, runtime.GOOS, runtime.GOARCH)
	}
	seen := 0
	err = filepath.WalkDir(payload, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(payload, p)
		if payload == dir && (rel == "manifest.json" || rel == "manifest.sig") {
			return nil
		}
		e, ok := m.Files[rel]
		if !ok || !d.Type().IsRegular() {
			return fmt.Errorf("payload file %s is not in the signed manifest", rel)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != e.SHA256 {
			return fmt.Errorf("payload file %s does not match its signed digest", rel)
		}
		seen++
		return nil
	})
	if err != nil {
		return m, err
	}
	if seen != len(m.Files) {
		return m, errors.New("payload is missing signed files")
	}
	return m, nil
}

// FindByDigest returns the version directory under mirror whose signed index
// has the given digest.
func FindByDigest(mirror string, trust ed25519.PublicKey, digest string) (string, Index, error) {
	entries, _ := os.ReadDir(mirror)
	for _, e := range entries {
		dir := filepath.Join(mirror, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
		if err != nil || Digest(raw) != digest {
			continue
		}
		ix, _, err := VerifyIndex(dir, trust)
		return dir, ix, err
	}
	return "", Index{}, errors.New("no release with that digest in the host's release mirror")
}

// Newest returns the newest verified release on channel in mirror.
func Newest(mirror string, trust ed25519.PublicKey, channel string) (string, Index, string, bool) {
	var best Index
	var bestDir, bestDigest string
	entries, _ := os.ReadDir(mirror)
	for _, e := range entries {
		dir := filepath.Join(mirror, e.Name())
		ix, digest, err := VerifyIndex(dir, trust)
		if err != nil || ix.Channel != channel || CheckRevoked(mirror, trust, digest) != nil {
			continue
		}
		if bestDir == "" || Newer(ix.Version, best.Version) {
			best, bestDir, bestDigest = ix, dir, digest
		}
	}
	return bestDir, best, bestDigest, bestDir != ""
}
