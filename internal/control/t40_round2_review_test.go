package control

import (
	"os"
	"path/filepath"
	"testing"
)

func TestT40SecondAttachmentPreservesEmptiedBackingRoot(t *testing.T) {
	s, err := Open(storeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data := []byte("saved contents")
	sum, err := s.putObject(data)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := tree{"saved.txt": {SHA256: sum, Size: int64(len(data)), Mode: 0644}}
	const resource = "vo_shared"
	const quota = 1 << 20
	if err := s.populateStorage(resource, snapshot, quota); err != nil {
		t.Fatal(err)
	}
	// The first attachment's 9P server holds this root across guest writes.
	root, err := os.OpenRoot(filepath.Join(s.dir, "storage", resource))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	before, err := root.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	// A guest removes the last file; the stored snapshot stays unchanged
	// until detach. Attaching a second guest must preserve this live state.
	if err := root.Remove("saved.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.populateStorage(resource, snapshot, quota); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(s.dir, "storage", resource)
	after, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("second attachment replaced the first attachment's live backing root")
	}
	if _, err := os.Stat(filepath.Join(dest, "saved.txt")); !os.IsNotExist(err) {
		t.Errorf("second attachment resurrected deleted saved.txt: stat error = %v", err)
	}
	if err := root.WriteFile("new.txt", []byte("live write"), 0644); err != nil {
		t.Errorf("first attachment cannot create files after second attach: %v", err)
	}
}
