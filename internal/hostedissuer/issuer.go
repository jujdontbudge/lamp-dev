// SPDX-License-Identifier: Apache-2.0
// Package hostedissuer runs separately from the relay. It receives CSRs, never
// fleet TLS private keys, and owns the ACME account and constrained DNS helper.
package hostedissuer

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mholt/acmez/v3"
	"github.com/mholt/acmez/v3/acme"
	"github.com/miekg/dns"
	"golang.org/x/sys/unix"
)

const production = "https://acme-v02.api.letsencrypt.org/directory"
const staging = "https://acme-staging-v02.api.letsencrypt.org/directory"

var fleetPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

type Config struct {
	StateDir         string   `json:"state_dir"`
	Directory        string   `json:"directory"`
	Email            string   `json:"email"`
	DNSHelper        string   `json:"dns_helper"`
	DNSHelperSHA256  string   `json:"dns_helper_sha256"`
	AuthoritativeDNS []string `json:"authoritative_dns"`
	// This is the slice of shared-domain capacity available AFTER reserving
	// other products' usage and repair capacity. Zero means deferred, not ready.
	WeeklyCapacity int               `json:"weekly_capacity"`
	Fleets         map[string]string `json:"fleet_roots"` // base64 Ed25519 public keys
}
type nonce struct {
	Value   string    `json:"value"`
	Expires time.Time `json:"expires"`
}
type ledger struct {
	Version      int               `json:"version"`
	Account      acme.Account      `json:"account"`
	Nonces       map[string]nonce  `json:"nonces"`
	Attempts     []time.Time       `json:"attempts"`
	Certificates map[string][]byte `json:"certificates"`
}
type Issuer struct {
	config Config
	state  ledger
	lock   *os.File
	key    crypto.Signer
}
type Request struct {
	Fleet     string `json:"fleet"`
	Nonce     string `json:"nonce"`
	CSR       []byte `json:"csr"`
	Signature []byte `json:"signature"`
}
type proof struct {
	Fleet   string `json:"fleet"`
	Nonce   string `json:"nonce"`
	CSRHash string `json:"csr_sha256"`
}

func Proof(r Request) []byte {
	sum := sha256.Sum256(r.CSR)
	raw, _ := json.Marshal(proof{r.Fleet, r.Nonce, hex.EncodeToString(sum[:])})
	return raw
}
func ReadPrivate(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	a, e := f.Stat()
	if e != nil {
		return nil, e
	}
	b, e := os.Lstat(path)
	if e != nil || !a.Mode().IsRegular() || b.Mode()&os.ModeSymlink != 0 || a.Mode().Perm()&0077 != 0 || a.Size() > 4<<20 {
		return nil, errors.New("private regular file required")
	}
	return io.ReadAll(io.LimitReader(f, (4<<20)+1))
}
func Open(path string) (*Issuer, error) {
	raw, e := ReadPrivate(path)
	if e != nil {
		return nil, e
	}
	var c Config
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return nil, e
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("one config required")
	}
	if !filepath.IsAbs(c.StateDir) || (c.Directory != production && c.Directory != staging) || c.WeeklyCapacity < 0 || c.WeeklyCapacity > 50 || len(c.AuthoritativeDNS) == 0 || !filepath.IsAbs(c.DNSHelper) {
		return nil, errors.New("invalid issuer configuration")
	}
	for _, address := range c.AuthoritativeDNS {
		h, p, e := net.SplitHostPort(address)
		if e != nil || net.ParseIP(h) == nil || p != "53" {
			return nil, errors.New("numeric authoritative DNS addresses on port 53 required")
		}
	}
	for fleet, key := range c.Fleets {
		b, e := base64.StdEncoding.DecodeString(key)
		if !fleetPattern.MatchString(fleet) || e != nil || len(b) != ed25519.PublicKeySize {
			return nil, errors.New("invalid enrolled fleet root")
		}
	}
	if e = os.MkdirAll(c.StateDir, 0700); e != nil {
		return nil, e
	}
	info, e := os.Lstat(c.StateDir)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private issuer directory required")
	}
	lock, e := os.OpenFile(filepath.Join(c.StateDir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		lock.Close()
		return nil, errors.New("issuer already in use")
	}
	i := &Issuer{config: c, lock: lock, state: ledger{Version: 1, Nonces: map[string]nonce{}, Certificates: map[string][]byte{}}}
	raw, e = ReadPrivate(filepath.Join(c.StateDir, "ledger.json"))
	if e == nil {
		e = json.Unmarshal(raw, &i.state)
		if i.state.Version != 1 || i.state.Nonces == nil || i.state.Certificates == nil {
			e = errors.New("unsupported issuer ledger")
		}
	}
	if e != nil && !os.IsNotExist(e) {
		i.Close()
		return nil, e
	}
	raw, e = ReadPrivate(filepath.Join(c.StateDir, "account.pem"))
	if e == nil {
		block, _ := pem.Decode(raw)
		if block == nil {
			e = errors.New("invalid ACME key")
		} else {
			var key any
			key, e = x509.ParsePKCS8PrivateKey(block.Bytes)
			if e == nil {
				var ok bool
				i.key, ok = key.(crypto.Signer)
				if !ok {
					e = errors.New("invalid ACME signer")
				}
			}
		}
	}
	if e != nil && !os.IsNotExist(e) {
		i.Close()
		return nil, e
	}
	return i, nil
}
func (i *Issuer) Close() { _ = unix.Flock(int(i.lock.Fd()), unix.LOCK_UN); _ = i.lock.Close() }
func atomicFile(path string, raw []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".issuer-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(raw); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (i *Issuer) save() error {
	raw, e := json.Marshal(i.state)
	if e != nil {
		return e
	}
	return atomicFile(filepath.Join(i.config.StateDir, "ledger.json"), raw)
}
func (i *Issuer) InitAccount(ctx context.Context) error {
	if i.state.Account.Location != "" {
		return errors.New("ACME account already registered")
	}
	if i.key == nil {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return e
		}
		der, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return e
		}
		if e = atomicFile(filepath.Join(i.config.StateDir, "account.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); e != nil {
			return e
		}
		i.key = key
	}
	client := &acme.Client{Directory: i.config.Directory}
	var contact []string
	if i.config.Email != "" {
		contact = []string{"mailto:" + i.config.Email}
	}
	account, e := client.NewAccount(ctx, acme.Account{Contact: contact, TermsOfServiceAgreed: true, PrivateKey: i.key})
	if e != nil {
		return errors.New("ACME account registration failed")
	}
	i.state.Account = account
	return i.save()
}
func (i *Issuer) Challenge(fleet string) (string, error) {
	if i.config.Fleets[fleet] == "" {
		return "", errors.New("fleet root not enrolled")
	}
	var random [32]byte
	if _, e := rand.Read(random[:]); e != nil {
		return "", e
	}
	value := hex.EncodeToString(random[:])
	i.state.Nonces[fleet] = nonce{value, time.Now().Add(time.Minute)}
	return value, i.save()
}
func (i *Issuer) Issue(ctx context.Context, r Request) ([]byte, error) {
	root, e := base64.StdEncoding.DecodeString(i.config.Fleets[r.Fleet])
	if e != nil || len(root) != 32 || !ed25519.Verify(root, Proof(r), r.Signature) {
		return nil, errors.New("fleet root proof required")
	}
	n := i.state.Nonces[r.Fleet]
	if n.Value == "" || n.Value != r.Nonce || !time.Now().Before(n.Expires) {
		return nil, errors.New("issuer challenge expired or consumed")
	}
	csr, e := x509.ParseCertificateRequest(r.CSR)
	name := "*." + r.Fleet + ".fleet.eilmer.si"
	if e != nil || csr.CheckSignature() != nil || len(csr.DNSNames) != 1 || csr.DNSNames[0] != name || len(csr.IPAddresses) != 0 || len(csr.EmailAddresses) != 0 || len(csr.URIs) != 0 || (csr.Subject.CommonName != "" && csr.Subject.CommonName != name) {
		return nil, errors.New("CSR must name exactly the allocated fleet wildcard")
	}
	// Reject arbitrary requested extensions; SAN is the only permitted extension.
	for _, ext := range csr.Extensions {
		if ext.Id.String() != "2.5.29.17" {
			return nil, errors.New("unapproved CSR extension")
		}
	}
	if i.key == nil || i.state.Account.Location == "" {
		return nil, errors.New("separate ACME account not registered")
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	recent := []time.Time{}
	for _, t := range i.state.Attempts {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= i.config.WeeklyCapacity {
		return nil, errors.New("CERTIFICATE_ISSUANCE_DEFERRED: no allocated shared-domain capacity")
	}
	if e = i.CheckCAA(ctx, r.Fleet); e != nil {
		return nil, e
	}
	helper, e := os.ReadFile(i.config.DNSHelper)
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256(helper)
	if hex.EncodeToString(sum[:]) != i.config.DNSHelperSHA256 {
		return nil, errors.New("DNS helper digest mismatch")
	}
	delete(i.state.Nonces, r.Fleet)
	i.state.Attempts = append(recent, time.Now())
	if e = i.save(); e != nil {
		return nil, e
	}
	account := i.state.Account
	account.PrivateKey = i.key
	solver := dnsSolver{i: i, fleet: r.Fleet}
	client := &acmez.Client{Client: &acme.Client{Directory: i.config.Directory}, ChallengeSolvers: map[string]acmez.Solver{acme.ChallengeTypeDNS01: solver}}
	certs, e := client.ObtainCertificate(ctx, acmez.OrderParameters{Account: account, Identifiers: []acme.Identifier{{Type: "dns", Value: name}}, CSR: acmez.StaticCSR(csr)})
	if e != nil || len(certs) == 0 {
		return nil, errors.New("ACME issuance failed; capacity reservation retained")
	}
	chain := certs[0].ChainPEM
	block, _ := pem.Decode(chain)
	if block == nil {
		return nil, errors.New("CA returned invalid chain")
	}
	cert, e := x509.ParseCertificate(block.Bytes)
	if e != nil || len(cert.DNSNames) != 1 || cert.DNSNames[0] != name || !bytes.Equal(cert.RawSubjectPublicKeyInfo, csr.RawSubjectPublicKeyInfo) {
		return nil, errors.New("CA certificate does not match authorized CSR")
	}
	i.state.Certificates[r.Fleet] = chain
	if e = i.save(); e != nil {
		return nil, e
	}
	return chain, nil
}
func (i *Issuer) Certificate(fleet string) ([]byte, error) {
	if len(i.state.Certificates[fleet]) == 0 {
		return nil, errors.New("no issued certificate")
	}
	return i.state.Certificates[fleet], nil
}
func (i *Issuer) AccountURI() string { return i.state.Account.Location }
func (i *Issuer) query(ctx context.Context, address, name string, kind uint16) ([]dns.RR, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), kind)
	m.RecursionDesired = false
	client := &dns.Client{Net: "tcp", Timeout: 5 * time.Second}
	answer, _, e := client.ExchangeContext(ctx, m, address)
	if e != nil || answer.Rcode != dns.RcodeSuccess || !answer.Authoritative {
		return nil, errors.New("authoritative DNS policy unavailable")
	}
	for _, rr := range answer.Answer {
		if rr.Header().Rrtype == dns.TypeCNAME || rr.Header().Rrtype == dns.TypeDNAME {
			return nil, errors.New("DNS aliases not permitted in issuer subtree")
		}
	}
	return answer.Answer, nil
}
func (i *Issuer) CheckCAA(ctx context.Context, fleet string) error {
	if !fleetPattern.MatchString(fleet) || i.state.Account.Location == "" {
		return errors.New("registered account and fleet required")
	}
	policy := "letsencrypt.org; accounturi=" + i.state.Account.Location + "; validationmethods=dns-01"
	for _, server := range i.config.AuthoritativeDNS {
		for _, name := range []string{"fleet.eilmer.si", fleet + ".fleet.eilmer.si"} {
			records, e := i.query(ctx, server, name, dns.TypeCAA)
			if e != nil {
				return e
			}
			ordinary, wild := 0, 0
			for _, rr := range records {
				c, ok := rr.(*dns.CAA)
				if !ok {
					return errors.New("unexpected CAA answer")
				}
				if c.Hdr.Name != dns.Fqdn(name) {
					return errors.New("CAA at wrong owner")
				}
				switch c.Tag {
				case "issue":
					ordinary++
					if c.Value != policy {
						return errors.New("permissive CAA issue policy")
					}
				case "issuewild":
					wild++
					if c.Value != policy {
						return errors.New("permissive CAA wildcard policy")
					}
				case "iodef":
				default:
					return errors.New("unrecognized CAA policy")
				}
			}
			if ordinary != 1 || wild != 1 {
				return errors.New("account-bound DNS-01 issue and issuewild CAA required")
			}
		}
	}
	return nil
}

type dnsSolver struct {
	i     *Issuer
	fleet string
}

func (s dnsSolver) run(ctx context.Context, action string, c acme.Challenge) error {
	expected := "_acme-challenge." + s.fleet + ".fleet.eilmer.si"
	if strings.TrimSuffix(c.DNS01TXTRecordName(), ".") != expected {
		return errors.New("DNS challenge outside fleet allocation")
	}
	raw, _ := json.Marshal(map[string]string{"action": action, "name": expected, "value": c.DNS01KeyAuthorization()})
	command := exec.CommandContext(ctx, s.i.config.DNSHelper)
	command.Stdin = bytes.NewReader(raw)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if command.Run() != nil {
		return errors.New("constrained DNS helper failed")
	}
	return nil
}
func (s dnsSolver) Present(ctx context.Context, c acme.Challenge) error {
	return s.run(ctx, "present", c)
}
func (s dnsSolver) CleanUp(ctx context.Context, c acme.Challenge) error {
	return s.run(ctx, "cleanup", c)
}
func (s dnsSolver) Wait(ctx context.Context, c acme.Challenge) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		ready := true
		for _, server := range s.i.config.AuthoritativeDNS {
			records, e := s.i.query(ctx, server, c.DNS01TXTRecordName(), dns.TypeTXT)
			found := false
			for _, rr := range records {
				if t, ok := rr.(*dns.TXT); ok && strings.Join(t.Txt, "") == c.DNS01KeyAuthorization() {
					found = true
				}
			}
			if e != nil || !found {
				ready = false
			}
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
