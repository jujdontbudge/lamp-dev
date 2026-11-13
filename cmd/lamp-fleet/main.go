// lamp-fleet provisions private host bundles for a new enrolled fleet.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lamp.eilmer.si/internal/control"
)

type bundle struct {
	HostID    string              `json:"host_id"`
	Hosts     []control.FleetHost `json:"hosts"`
	PublicAPI string              `json:"public_api_listen"`
	Bootstrap bool                `json:"bootstrap"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lamp-fleet:", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("use init --hosts hosts.json --out NEW_DIRECTORY, or join --bundle DIRECTORY --root PRIVATE_DIRECTORY [-- lampd flags]")
	}
	flags := flag.NewFlagSet("lamp-fleet "+os.Args[1], flag.ContinueOnError)
	switch os.Args[1] {
	case "init":
		hosts := flags.String("hosts", "", "JSON array of enrolled hosts (id, api, raft, os, arch, engines, capacity)")
		out := flags.String("out", "", "new private bundle directory")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		return initialize(*hosts, *out)
	case "join":
		from := flags.String("bundle", "", "private host bundle, transferred securely from init")
		root := flags.String("root", "", "absolute private host directory; retain for restarts")
		prepare := flags.Bool("prepare-only", false, "write enrollment files for lamp-installer without starting lampd")
		daemon := flags.String("lampd", "", "optional lampd binary (default: beside lamp-fleet)")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		return join(*from, *root, *daemon, *prepare, flags.Args())
	default:
		return errors.New("use init or join")
	}
}
func write(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	return ce
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return write(path, append(b, '\n'))
}
func key() (*ecdsa.PrivateKey, error) { return ecdsa.GenerateKey(elliptic.P256(), rand.Reader) }
func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	return n
}
func certificate(t, parent *x509.Certificate, pub any, signer *ecdsa.PrivateKey) ([]byte, error) {
	b, err := x509.CreateCertificate(rand.Reader, t, parent, pub, signer)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b}), nil
}
func initialize(hostFile, out string) error {
	if out == "" {
		return errors.New("--out required")
	}
	data, err := os.ReadFile(hostFile)
	if err != nil {
		return err
	}
	var hosts []control.FleetHost
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&hosts); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("expected one host array")
	}
	if len(hosts) == 0 {
		return errors.New("at least one host required")
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].ID < hosts[j].ID })
	seen := map[string]bool{}
	for _, h := range hosts {
		if len(h.ID) > 20 || !strings.HasPrefix(h.ID, "hs_") || strings.Trim(h.ID, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" || seen[h.ID] {
			return errors.New("unique hs_ host IDs of at most 20 lowercase letters, digits and underscores required")
		}
		seen[h.ID] = true
		u, e := url.Parse(h.API)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("host api must be a bare HTTPS origin")
		}
		if _, _, e = net.SplitHostPort(u.Host); e != nil {
			return e
		}
		if _, _, e = net.SplitHostPort(h.Raft); e != nil {
			return e
		}
	}
	if err = os.Mkdir(out, 0700); err != nil {
		return err
	}
	caKey, err := key()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	ca := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Lamp fleet"}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(5, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caPEM, err := certificate(ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	// The CA signing key never enters a host bundle.
	caDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		return err
	}
	if err = write(filepath.Join(out, "ca.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caDER})); err != nil {
		return err
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return err
	}
	bearer := hex.EncodeToString(token)
	hash := sha256.Sum256([]byte(bearer))
	if err = write(filepath.Join(out, "owner.token"), []byte(bearer+"\n")); err != nil {
		return err
	}
	ingresses := []map[string]string{}
	for _, h := range hosts {
		// Public ingress binds a separate port; the host peer API remains mTLS-only.
		u, _ := url.Parse(h.API)
		address, port, _ := net.SplitHostPort(u.Host)
		peerPort, e := strconv.Atoi(port)
		if e != nil || peerPort < 1 || peerPort >= 65535 {
			return errors.New("peer API port must be 1..65534")
		}
		public := net.JoinHostPort(address, fmt.Sprint(peerPort+1))
		dir := filepath.Join(out, h.ID)
		if err = os.Mkdir(dir, 0700); err != nil {
			return err
		}
		k, e := key()
		if e != nil {
			return e
		}
		leaf := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: h.ID}, DNSNames: []string{h.ID}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		cert, e := certificate(leaf, ca, &k.PublicKey, caKey)
		if e != nil {
			return e
		}
		der, e := x509.MarshalECPrivateKey(k)
		if e != nil {
			return e
		}
		for name, b := range map[string][]byte{"ca.crt": caPEM, "host.crt": cert, "host.key": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})} {
			if e = write(filepath.Join(dir, name), b); e != nil {
				return e
			}
		}
		if e = writeJSON(filepath.Join(dir, "identity.json"), map[string]string{"owner_id": "pr_fleet_owner", "bootstrap_sha256": hex.EncodeToString(hash[:])}); e != nil {
			return e
		}
		if e = writeJSON(filepath.Join(dir, "bundle.json"), bundle{h.ID, hosts, public, h.ID == hosts[0].ID}); e != nil {
			return e
		}
		ingresses = append(ingresses, map[string]string{"url": "https://" + public, "server_name": h.ID})
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if err = write(filepath.Join(out, "ca.crt"), caPEM); err != nil {
		return err
	}
	if err = writeJSON(filepath.Join(out, "discovery.json"), map[string]any{"ca_file": filepath.Join(abs, "ca.crt"), "ingresses": ingresses}); err != nil {
		return err
	}
	fmt.Println("Fleet bundles created. Transfer only each host's directory securely. Keep owner.token and ca.key private; retain the CA offline. Host certificates expire in one year.")
	return nil
}
func join(from, root, daemon string, prepare bool, extra []string) error {
	if !filepath.IsAbs(root) {
		return errors.New("--root must be an absolute private directory")
	}
	if err := control.PrivateDir(root); err != nil {
		return err
	}
	read := func(name string) ([]byte, error) { return readPrivate(filepath.Join(from, name)) }
	raw, err := read("bundle.json")
	if err != nil {
		return err
	}
	var b bundle
	if err = json.Unmarshal(raw, &b); err != nil {
		return err
	}
	for _, name := range []string{"ca.crt", "host.crt", "host.key", "identity.json"} {
		data, e := read(name)
		if e != nil {
			return e
		}
		path := filepath.Join(root, name)
		if old, e := os.ReadFile(path); e == nil {
			if string(old) != string(data) {
				return errors.New("join root already belongs to a different bundle")
			}
		} else if !os.IsNotExist(e) {
			return e
		} else if e = write(path, data); e != nil {
			return e
		}
	}
	c := control.FleetConfig{HostID: b.HostID, Hosts: b.Hosts, PublicAPIListen: b.PublicAPI, Bootstrap: b.Bootstrap, CAFile: filepath.Join(root, "ca.crt"), CertFile: filepath.Join(root, "host.crt"), KeyFile: filepath.Join(root, "host.key"), GuardDir: filepath.Join(root, "guard")}
	config := filepath.Join(root, "fleet.json")
	encoded, _ := json.Marshal(c)
	if old, e := os.ReadFile(config); e == nil {
		if string(old) != string(encoded) {
			return errors.New("fleet configuration changed; use the original bundle")
		}
	} else if !os.IsNotExist(e) {
		return e
	} else if e = write(config, encoded); e != nil {
		return e
	}
	if prepare {
		fmt.Println(config)
		return nil
	}
	if daemon == "" {
		self, e := os.Executable()
		if e != nil {
			return e
		}
		daemon = filepath.Join(filepath.Dir(self), "lampd")
	}
	daemon, err = exec.LookPath(daemon)
	if err != nil {
		return err
	}
	// Enrollment paths cannot be overridden through pass-through worker/update flags.
	for _, a := range extra {
		for _, reserved := range []string{"fleet-config", "identity-file", "state-dir", "socket"} {
			if a == "--"+reserved || a == "-"+reserved || strings.HasPrefix(a, "--"+reserved+"=") || strings.HasPrefix(a, "-"+reserved+"=") {
				return fmt.Errorf("%s is managed by join", reserved)
			}
		}
	}
	args := append([]string{daemon, "--fleet-config", config, "--identity-file", filepath.Join(root, "identity.json"), "--state-dir", filepath.Join(root, "state"), "--socket", filepath.Join(root, "api.sock")}, extra...)
	return syscall.Exec(daemon, args, os.Environ())
}

func readPrivate(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	link, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || link.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return nil, errors.New("private regular bundle file required")
	}
	return io.ReadAll(io.LimitReader(f, 1<<20))
}
