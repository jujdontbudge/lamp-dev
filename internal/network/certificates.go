package network

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Authority belongs to the host ingress, never the app/guest. Callers install
// only PublicPEM in their private-network client trust store. Verification is
// never disabled and no public certificate provider/trial is required.
type Authority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	PublicPEM   []byte
}

func NewAuthority(directory string) (*Authority, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrDenied
	}
	certPath, keyPath := filepath.Join(directory, "ingress-ca.pem"), filepath.Join(directory, "ingress-ca-key.pem")
	certInfo, certErr := os.Lstat(certPath)
	keyInfo, keyErr := os.Lstat(keyPath)
	if certErr == nil || keyErr == nil {
		if certErr != nil || keyErr != nil || !certInfo.Mode().IsRegular() || !keyInfo.Mode().IsRegular() || keyInfo.Mode().Perm()&0077 != 0 {
			return nil, ErrDenied
		}
		certPEM, err := os.ReadFile(certPath)
		if err != nil {
			return nil, ErrUnavailable
		}
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, ErrUnavailable
		}
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		clear(keyPEM)
		if err != nil || len(pair.Certificate) != 1 {
			return nil, ErrDenied
		}
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil || !cert.IsCA || !time.Now().Before(cert.NotAfter) {
			return nil, ErrDenied
		}
		key, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
		if !ok {
			return nil, ErrDenied
		}
		return &Authority{cert, key, certPEM}, nil
	}
	if !os.IsNotExist(certErr) || !os.IsNotExist(keyErr) {
		return nil, ErrUnavailable
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Lamp private ingress"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(30 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	clear(private)
	write := func(path string, data []byte) error {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	if err := write(keyPath, keyPEM); err != nil {
		clear(keyPEM)
		return nil, err
	}
	clear(keyPEM)
	if err := write(certPath, certPEM); err != nil {
		return nil, err
	}
	directoryFD, err := os.Open(directory)
	if err != nil {
		return nil, err
	}
	err = directoryFD.Sync()
	directoryFD.Close()
	if err != nil {
		return nil, err
	}
	return &Authority{certificate, key, certPEM}, nil
}
func (a *Authority) certificateFor(host string) (*tls.Certificate, error) {
	if host == "" || len(host) > 253 {
		return nil, ErrDenied
	}
	now := time.Now()
	// Leaf renewal cannot silently extend or replace the client's CA trust.
	if now.Before(a.certificate.NotBefore) || !now.Before(a.certificate.NotAfter) {
		return nil, ErrUnavailable
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	until := now.Add(24 * time.Hour)
	if a.certificate.NotAfter.Before(until) {
		until = a.certificate.NotAfter
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: host}, DNSNames: []string{host}, NotBefore: now.Add(-time.Minute), NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, a.certificate, &key.PublicKey, a.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der, a.certificate.Raw}, PrivateKey: key, Leaf: leaf}, nil
}
