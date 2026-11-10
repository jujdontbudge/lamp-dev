// SPDX-License-Identifier: AGPL-3.0-only
package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type enrollment struct {
	Hash    string    `json:"hash"`
	Expires time.Time `json:"expires"`
}
type database struct {
	Version     int                   `json:"version"`
	Hosts       map[string]string     `json:"hosts"`
	Enrollments map[string]enrollment `json:"enrollments"`
}
type store struct {
	mu   sync.Mutex
	path string
	db   database
	lock *os.File
}

func openStore(path string) (*store, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	info, e := os.Lstat(filepath.Dir(path))
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private state directory required")
	}
	lock, e := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		lock.Close()
		return nil, errors.New("relay state already in use")
	}
	s := &store{path: path, lock: lock, db: database{1, map[string]string{}, map[string]enrollment{}}}
	raw, e := PrivateRead(path)
	if e == nil {
		e = decode(raw, &s.db)
		if e == nil && (s.db.Version != 1 || s.db.Hosts == nil || s.db.Enrollments == nil) {
			e = errors.New("unsupported relay state")
		}
	}
	if e != nil && !os.IsNotExist(e) {
		s.close()
		return nil, e
	}
	return s, nil
}
func (s *store) close() { _ = unix.Flock(int(s.lock.Fd()), unix.LOCK_UN); _ = s.lock.Close() }
func (s *store) update(fn func(*database) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.Marshal(s.db)
	var next database
	_ = json.Unmarshal(raw, &next)
	if e := fn(&next); e != nil {
		return e
	}
	raw, e := json.Marshal(next)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".relay-state-")
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
	if e = os.Rename(f.Name(), s.path); e != nil {
		return e
	}
	// Once renamed, keep memory consistent even when directory fsync reports failure.
	s.db = next
	dir, e := os.Open(filepath.Dir(s.path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func secret() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic("system random source unavailable")
	}
	return hex.EncodeToString(b[:])
}
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == hex.EncodeToString(b)
}
func (s *store) host(fleet, credential string) bool {
	if !validHash(credential) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Hosts[fleet] != "" && s.db.Hosts[fleet] == hash(credential)
}
