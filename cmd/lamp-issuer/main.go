// SPDX-License-Identifier: Apache-2.0
// lamp-issuer is an owner-operated job, not a relay endpoint. Never copy its
// configuration, ACME key, state or DNS helper credentials onto the relay.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"lamp.eilmer.si/internal/hostedissuer"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "lamp-issuer:", e)
		os.Exit(1)
	}
}
func writeNew(path string, raw []byte) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if _, e = f.Write(raw); e != nil {
		return e
	}
	return f.Sync()
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("use init-account, challenge, request, issue, certificate, or check-caa")
	}
	flags := flag.NewFlagSet("lamp-issuer", flag.ContinueOnError)
	config := flags.String("config", "", "private issuer configuration (never on relay)")
	fleet := flags.String("fleet", "", "enrolled fleet ID")
	nonce := flags.String("nonce", "", "public one-time issuer challenge")
	csr := flags.String("csr", "", "host-generated PEM CSR file")
	root := flags.String("root-key", "", "private host fleet-root Ed25519 PKCS8 PEM key")
	request := flags.String("request", "", "fleet-root-signed request JSON")
	out := flags.String("out", "", "new private output file")
	if e := flags.Parse(os.Args[2:]); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if os.Args[1] == "request" {
		raw, e := hostedissuer.ReadPrivate(*csr)
		if e != nil {
			return e
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			return errors.New("PEM CSR required")
		}
		r := hostedissuer.Request{Fleet: *fleet, Nonce: *nonce, CSR: block.Bytes}
		raw, e = hostedissuer.ReadPrivate(*root)
		if e != nil {
			return e
		}
		block, _ = pem.Decode(raw)
		if block == nil {
			return errors.New("PEM root key required")
		}
		key, e := x509.ParsePKCS8PrivateKey(block.Bytes)
		if e != nil {
			return e
		}
		signer, ok := key.(ed25519.PrivateKey)
		if !ok {
			return errors.New("Ed25519 root required")
		}
		r.Signature = ed25519.Sign(signer, hostedissuer.Proof(r))
		raw, _ = json.Marshal(r)
		return writeNew(*out, append(raw, '\n'))
	}
	i, e := hostedissuer.Open(*config)
	if e != nil {
		return e
	}
	defer i.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	switch os.Args[1] {
	case "init-account":
		if e = i.InitAccount(ctx); e != nil {
			return e
		}
		fmt.Println(i.AccountURI())
		return nil
	case "challenge":
		n, e := i.Challenge(*fleet)
		if e != nil {
			return e
		}
		fmt.Println(n)
		return nil
	case "check-caa":
		return i.CheckCAA(ctx, *fleet)
	case "certificate":
		chain, e := i.Certificate(*fleet)
		if e != nil {
			return e
		}
		return writeNew(*out, chain)
	case "issue":
		raw, e := hostedissuer.ReadPrivate(*request)
		if e != nil {
			return e
		}
		var r hostedissuer.Request
		if json.Unmarshal(raw, &r) != nil {
			return errors.New("invalid signed request")
		}
		chain, e := i.Issue(ctx, r)
		if e != nil {
			return e
		}
		return writeNew(*out, chain)
	default:
		return errors.New("unknown command")
	}
}
