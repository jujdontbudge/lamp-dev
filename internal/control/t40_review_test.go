package control

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// A guest keeps directory fids across rename(2), including fids held by
// another sandbox attached to the same volume.
func TestT40RenamedDirectoryRemainsUsable(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "old"), 0755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	go (&p9server{root: root, quota: 1 << 20, mu: &sync.Mutex{}, fids: map[uint32]*p9fid{}}).serve(server)
	rpc := func(kind uint8, fields ...any) {
		t.Helper()
		var body, frame bytes.Buffer
		p9put(&body, fields...)
		p9put(&frame, uint32(body.Len()+7), kind, uint16(1))
		frame.Write(body.Bytes())
		if _, err := client.Write(frame.Bytes()); err != nil {
			t.Fatal(err)
		}
		var size uint32
		if err := binary.Read(client, binary.LittleEndian, &size); err != nil {
			t.Fatal(err)
		}
		reply := make([]byte, size-4)
		if _, err := io.ReadFull(client, reply); err != nil {
			t.Fatal(err)
		}
		if reply[0] == 7 {
			t.Fatalf("9P request %d failed: Linux errno %d", kind, binary.LittleEndian.Uint32(reply[3:]))
		}
	}
	rpc(100, uint32(262144), "9P2000.L")
	rpc(104, uint32(1), ^uint32(0), "", "", uint32(65537))
	rpc(110, uint32(1), uint32(2), uint16(1), "old")
	rpc(74, uint32(1), "old", uint32(1), "new")
	// mkdirat(dirfd, "child", 0755) must still work after its parent moved.
	rpc(72, uint32(2), "child", uint32(0755), uint32(65537))
	if _, err := os.Stat(filepath.Join(dir, "new", "child")); err != nil {
		t.Fatal(err)
	}
}

func TestT40InterruptedTemplateBuildCanRetryAfterRestart(t *testing.T) {
	config := storeConfig(t)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	owner := caller{Principal: s.journal.Control.OwnerID}
	raw := []byte(`format = "lamp.template/v1"
name = "tools"
guest_os = "linux"
arch = "x86_64"
seed_ref = "lamp-seed/ubuntu-26.04@sha256:0000000000000000000000000000000000000000000000000000000000000000"
network = "none"
`)
	sum, err := s.putObject(raw)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	blob := s.newReadyBlob(owner.Principal, sum, int64(len(raw)), "text/plain", "")
	blobID := textField(blob, "id")
	created, err := s.planTemplateCreate(owner, map[string]any{"name": "tools", "definition_blob_id": blobID})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	id := created.ref.Id
	if _, err := s.planTemplateBuild(owner, map[string]any{"template_id": id, "definition_blob_id": blobID, "expected_revision": jsonNumber(1)}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	// Persist the admission state, exactly as lifeStartWith does before the
	// build begins. A process loss here leaves no worker build to recover.
	if err := s.saveLife(); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rev := uintField(s.life.Templates[id].R, "revision")
	if _, err := s.planTemplateBuild(owner, map[string]any{"template_id": id, "definition_blob_id": blobID, "expected_revision": jsonNumber(rev)}); err != nil {
		t.Fatalf("interrupted template build must be recoverable after restart: %v", err)
	}
}
