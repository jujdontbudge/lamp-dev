package plugin

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
)

const EngineWarning = "This engine can read and alter its assigned sandbox RAM. Trust applies only to this exact executable and manifest digest, never neighbors or broker keys."
const Minor = 1

var types = []string{"engine", "network", "credential_source", "harness", "kit", "ci", "trigger", "console", "access", "notification_sink"}
var name = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
var semver = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?$`)

type Limits struct {
	MemoryBytes     uint64 `json:"memory_bytes"`
	CPUPercent      int    `json:"cpu_percent"`
	Concurrent      int    `json:"concurrent"`
	DeadlineMS      int    `json:"deadline_ms"`
	FramesPerSecond int    `json:"frames_per_second"`
	StderrBytes     int    `json:"stderr_bytes"`
}

func DefaultLimits() Limits {
	return Limits{MemoryBytes: 64 << 20, CPUPercent: 25, Concurrent: 16, DeadlineMS: 10000, FramesPerSecond: 128, StderrBytes: 64 << 10}
}

type Compatibility struct {
	APIMajor int `json:"api_major"`
	MinMinor int `json:"min_minor"`
	MaxMinor int `json:"max_minor"`
}
type Method struct {
	Input  Schema `json:"input"`
	Result Schema `json:"result"`
}
type Manifest struct {
	Type             string            `json:"type"`
	Publisher        string            `json:"publisher"`
	Version          string            `json:"version"`
	Digest           string            `json:"digest"`
	ProtocolVersion  string            `json:"protocol_version"`
	TrustClass       string            `json:"trust_class"`
	Permissions      []string          `json:"permissions"`
	Limits           Limits            `json:"limits"`
	ConfigSchema     Schema            `json:"config_schema"`
	Compatibility    Compatibility     `json:"compatibility"`
	RequiredFeatures []string          `json:"required_features,omitempty"`
	Methods          map[string]Method `json:"methods"`
}

// Approval is supplied only by authenticated owner admission in the control core,
// never decoded from plugin RPC. Exact manifest approval includes all permissions,
// schemas, limits, provenance and executable digest. Updates require fresh approval.
type Approval struct{ OwnerID, ManifestDigest, EngineWarning string }
type Package struct {
	manifest Manifest
	raw      []byte
	digest   string
	owner    string
}

func Digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func ValidDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && s == hex.EncodeToString(b)
}
func Admit(raw, executable []byte, a Approval) (*Package, error) {
	if len(raw) > MaxFrameBytes {
		return nil, errors.New("manifest too large")
	}
	fields, err := requiredFields(raw, "type", "publisher", "version", "digest", "protocol_version", "trust_class", "permissions", "limits", "config_schema", "compatibility", "methods")
	if err != nil {
		return nil, err
	}
	if _, err := requiredFields(fields["limits"], "memory_bytes", "cpu_percent", "concurrent", "deadline_ms", "frames_per_second", "stderr_bytes"); err != nil {
		return nil, err
	}
	if _, err := requiredFields(fields["compatibility"], "api_major", "min_minor", "max_minor"); err != nil {
		return nil, err
	}
	var m Manifest
	if err := decode(raw, &m); err != nil {
		return nil, err
	}
	if a.OwnerID == "" || a.ManifestDigest != Digest(raw) {
		return nil, errors.New("exact owner permission review required")
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	if Digest(executable) != m.Digest {
		return nil, errors.New("executable digest mismatch")
	}
	if m.TrustClass == "engine_trusted" && a.EngineWarning != EngineWarning {
		return nil, errors.New("explicit owner engine warning required")
	}
	return &Package{manifest: m, raw: slices.Clone(raw), digest: Digest(raw), owner: a.OwnerID}, nil
}

// VerifySignature checks provenance over exact manifest bytes. It does not admit
// authority; callers must still use Admit and an independent owner approval.
func VerifySignature(raw []byte, key ed25519.PublicKey, signature []byte) error {
	if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, raw, signature) {
		return errors.New("invalid publisher signature")
	}
	return nil
}
func (p *Package) Manifest() Manifest     { var m Manifest; _ = json.Unmarshal(p.raw, &m); return m }
func (p *Package) ManifestDigest() string { return p.digest }
func (m Manifest) validate() error {
	if !slices.Contains(types, m.Type) || !name.MatchString(m.Publisher) || !semver.MatchString(m.Version) || !ValidDigest(m.Digest) {
		return errors.New("invalid type, publisher, version or digest")
	}
	if m.ProtocolVersion != Protocol || m.Compatibility.APIMajor != 1 || m.Compatibility.MinMinor < 0 || m.Compatibility.MaxMinor < m.Compatibility.MinMinor || m.Compatibility.MinMinor > Minor || m.Compatibility.MaxMinor < Minor-1 {
		return errors.New("incompatible plugin protocol/API")
	}
	switch m.TrustClass {
	case "confined", "credential", "platform", "engine_trusted":
	default:
		return errors.New("unknown trust class")
	}
	if (m.Type == "engine") != (m.TrustClass == "engine_trusted") {
		return errors.New("engine type requires engine trust and vice versa")
	}
	if m.TrustClass == "credential" && m.Type != "credential_source" {
		return errors.New("credential class only for credential sources")
	}
	if m.TrustClass == "platform" && m.Type != "network" && m.Type != "access" {
		return errors.New("platform class only for network/access")
	}
	seen := map[string]bool{}
	for _, p := range m.Permissions {
		if !name.MatchString(p) || seen[p] || p != m.Type+".use" {
			return fmt.Errorf("unsupported or duplicate permission %q", p)
		}
		seen[p] = true
	}
	l := m.Limits
	if l.MemoryBytes < 8<<20 || l.MemoryBytes > 1<<30 || l.CPUPercent < 1 || l.CPUPercent > 100 || l.Concurrent < 1 || l.Concurrent > 16 || l.DeadlineMS < 1 || l.DeadlineMS > 10000 || l.FramesPerSecond < 1 || l.FramesPerSecond > 1024 || l.StderrBytes < 0 || l.StderrBytes > 1<<20 {
		return errors.New("invalid plugin limits")
	}
	features := map[string]bool{}
	for _, f := range m.RequiredFeatures {
		if features[f] || (f != "typed-grants" && f != "cancel") {
			return errors.New("unsupported or duplicate required feature")
		}
		features[f] = true
	}
	if err := m.ConfigSchema.check(0); err != nil {
		return err
	}
	if len(m.Methods) == 0 || len(m.Methods) > 64 {
		return errors.New("methods required and bounded")
	}
	for n, m := range m.Methods {
		if !name.MatchString(n) || n == "initialize" || n == "host.effect" || n == "cancel" {
			return errors.New("invalid declared method")
		}
		if err := m.Input.check(0); err != nil {
			return err
		}
		if err := m.Result.check(0); err != nil {
			return err
		}
	}
	return nil
}
