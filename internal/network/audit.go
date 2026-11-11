package network

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event intentionally has no payload, headers, auth URL, provider key or bearer
// field. Host dial admission must be durable before a provider sees traffic.
type Event struct {
	Kind         string        `json:"kind"`
	Subject      Subject       `json:"subject"`
	Revision     uint64        `json:"revision"`
	Destination  *Destination  `json:"destination,omitempty"`
	Decision     string        `json:"decision"`
	Reason       string        `json:"reason"`
	PolicySHA256 string        `json:"policy_sha256,omitempty"`
	Protocol     string        `json:"protocol,omitempty"`
	MemberPort   uint16        `json:"member_port,omitempty"`
	GuestPort    uint16        `json:"guest_port,omitempty"`
	Packets      uint64        `json:"packets,omitempty"`
	Peer         *PeerIdentity `json:"peer,omitempty"`
	LinkID       string        `json:"link_id,omitempty"`
	Target       *Subject      `json:"target,omitempty"`
	PortID       string        `json:"port_id,omitempty"`
	GrantID      string        `json:"grant_id,omitempty"`
	DNSAddress   *netip.Addr   `json:"dns_address,omitempty"`
}
type Audit interface{ Record(Event) error }

// FileAudit is a bounded, create-new, host-permission-protected demo spool, not
// T23's encrypted/signed external ledger. A failed barrier permanently poisons it.
type FileAudit struct {
	mu          sync.Mutex
	file        *os.File
	used, limit int64
	sequence    uint64
	broken      bool
}

func NewFileAudit(path string, limit int64) (*FileAudit, error) {
	if limit < 4096 || limit > 1<<30 {
		return nil, ErrUnavailable
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return nil, ErrUnavailable
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*FileAudit, error) { _ = f.Close(); return nil, err }
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fail(err)
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil || closeErr != nil {
		return fail(errors.Join(err, closeErr))
	}
	return &FileAudit{file: f, limit: limit}, nil
}
func (a *FileAudit) Record(event Event) (err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.broken || a.sequence == ^uint64(0) {
		return ErrUnavailable
	}
	defer func() {
		if err != nil {
			a.broken = true
		}
	}()
	bytes, err := json.Marshal(struct {
		Sequence uint64    `json:"sequence"`
		At       time.Time `json:"observed_at"`
		Event    Event     `json:"event"`
	}{a.sequence + 1, time.Now().UTC(), event})
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	if len(bytes) > 64<<10 || a.used+int64(len(bytes)) > a.limit {
		return ErrUnavailable
	}
	if _, err = a.file.Write(bytes); err != nil {
		return err
	}
	if err = a.file.Sync(); err != nil {
		return err
	}
	a.used += int64(len(bytes))
	a.sequence++
	return nil
}
func (a *FileAudit) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.broken = true
	return a.file.Close()
}
