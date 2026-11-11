package access

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
)

// InitCA is an explicit owner operation. Serving never silently creates a new fleet CA.
func InitCA(path string) error {
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return e
	}
	p, e := ssh.MarshalPrivateKey(key, "Lamp fleet CA")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(pem.EncodeToMemory(p))
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	s, _ := ssh.NewSignerFromKey(key)
	pub, e := os.OpenFile(path+".pub", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if e != nil {
		return e
	}
	_, e = pub.Write(ssh.MarshalAuthorizedKey(s.PublicKey()))
	ce = pub.Close()
	if e == nil {
		e = ce
	}
	return e
}
func loadSigner(path string) (ssh.Signer, error) {
	b, e := PrivateRead(path)
	if e != nil {
		return nil, e
	}
	return ssh.ParsePrivateKey(b)
}
func signedCertificate(key ssh.PublicKey, ca ssh.Signer, kind uint32, names []string, lifetime time.Duration) (*ssh.Certificate, error) {
	var b [8]byte
	if _, e := rand.Read(b[:]); e != nil {
		return nil, e
	}
	now := time.Now()
	c := &ssh.Certificate{Key: key, Serial: binary.BigEndian.Uint64(b[:]), CertType: kind, KeyId: "lamp", ValidPrincipals: names, ValidAfter: uint64(now.Add(-30 * time.Second).Unix()), ValidBefore: uint64(now.Add(lifetime).Unix()), Permissions: ssh.Permissions{Extensions: map[string]string{}}}
	if kind == ssh.UserCert {
		c.Extensions["permit-pty"] = ""
	}
	if e := c.SignCert(rand.Reader, ca); e != nil {
		return nil, e
	}
	return c, nil
}
func hostSigner(c Config) (ssh.Signer, error) {
	path := filepath.Join(c.StateDir, "ssh-host")
	if _, e := os.Lstat(path); os.IsNotExist(e) {
		if e = InitCA(path); e != nil {
			return nil, e
		}
	}
	key, e := loadSigner(path)
	if e != nil {
		return nil, e
	}
	ca, e := loadSigner(c.HostCAFile)
	if e != nil {
		return nil, e
	}
	cert, e := signedCertificate(key.PublicKey(), ca, ssh.HostCert, c.Hostnames, 24*time.Hour)
	if e != nil {
		return nil, e
	}
	return ssh.NewCertSigner(cert, key)
}
