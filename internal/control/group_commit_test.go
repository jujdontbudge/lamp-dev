package control

import (
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
)

func TestGroupedCommitHidesStagedPrefixAndRecoversEveryAcknowledgement(t *testing.T) {
	config := storeConfig(t)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	original := writeRecordAt
	var once sync.Once
	writeRecordAt = func(f *os.File, data []byte, off int64) (int, error) {
		once.Do(func() { close(entered); <-release })
		return original(f, data, off)
	}
	defer func() { writeRecordAt = original }()
	saved := make(chan error, 32)
	for i := 0; i < 32; i++ {
		go func(i int) {
			s.mu.Lock()
			id := string(rune('A' + i))
			s.journal.Operations[id] = &record{Key: id, Operation: api.Operation{Id: id, State: "succeeded"}}
			err := s.saveConcurrent(id)
			s.mu.Unlock()
			saved <- err
		}(i)
	}
	<-entered
	read := make(chan struct{})
	go func() { s.readLock(); s.mu.Unlock(); close(read) }()
	select {
	case <-read:
		t.Fatal("reader saw staged state")
	case <-saved:
		t.Fatal("save acknowledged before data flush")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 32; i++ {
		if err := <-saved; err != nil {
			t.Fatal(err)
		}
	}
	<-read
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	writeRecordAt = original
	restored, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if len(restored.journal.Operations) != 32 {
		t.Fatal("lost acknowledged records")
	}
}

func TestGroupedCommitFailureBreaksAllPublication(t *testing.T) {
	s, err := Open(storeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	original := writeRecordAt
	writeRecordAt = func(*os.File, []byte, int64) (int, error) { return 0, errors.New("injected storage failure") }
	defer func() { writeRecordAt = original }()
	s.mu.Lock()
	s.journal.Operations["op"] = &record{Operation: api.Operation{Id: "op", State: "accepted"}}
	err = s.saveConcurrent("op")
	s.mu.Unlock()
	if err == nil {
		t.Fatal("failed group acknowledged")
	}
	s.readLock()
	defer s.mu.Unlock()
	if !s.broken {
		t.Fatal("failed prefix can be published")
	}
}

func TestReplyUsesOwnDurableSnapshotWithoutUnrelatedFlush(t *testing.T) {
	s, err := Open(storeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.journal.Operations["op"] = &record{Operation: api.Operation{Id: "op", State: "accepted"}}
	if err := s.save("op"); err != nil {
		t.Fatal(err)
	}
	// This newer terminal value has not been saved and must never escape.
	s.journal.Operations["op"].Operation.State = "succeeded"
	entered, release := make(chan struct{}), make(chan struct{})
	original := writeRecordAt
	writeRecordAt = func(f *os.File, data []byte, off int64) (int, error) {
		close(entered)
		<-release
		return original(f, data, off)
	}
	defer func() { writeRecordAt = original }()
	saved := make(chan error, 1)
	go func() {
		s.mu.Lock()
		s.journal.Operations["other"] = &record{Operation: api.Operation{Id: "other", State: "accepted"}}
		err := s.saveConcurrent("other")
		s.mu.Unlock()
		saved <- err
	}()
	<-entered
	response := httptest.NewRecorder()
	read := make(chan struct{})
	go func() { s.respond(response, httptest.NewRequest("GET", "/", nil), "req", "op", true, nil); close(read) }()
	select {
	case <-read:
	case <-time.After(time.Second):
		close(release)
		<-saved
		t.Fatal("reply waited on unrelated output flush")
	}
	close(release)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if response.Code != 202 || !strings.Contains(response.Body.String(), `"state":"accepted"`) {
		t.Fatal("staged state escaped", response.Body.String())
	}
}
