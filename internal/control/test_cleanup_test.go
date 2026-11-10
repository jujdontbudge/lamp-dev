package control_test

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A separate session watches a pipe held only by the test parent. Even SIGKILL
// or Go's timeout panic closes that pipe; the watcher then removes the build
// directory. It owns only the uniquely created directory passed by its parent.
func guardTestDirectory(dir string) (func(), error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		reader.Close()
		writer.Close()
		return nil, err
	}
	child := exec.Command(executable)
	child.Env = append(os.Environ(), "LAMP_TEST_CLEANUP_DIR="+dir, "LAMP_TEST_CLEANUP_PROBE=")
	child.Stdin = reader
	child.Stderr = os.Stderr
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = child.Start()
	reader.Close()
	if err != nil {
		writer.Close()
		return nil, err
	}
	return func() { writer.Close(); child.Wait() }, nil
}

func testCleanupChild() bool {
	if dir := os.Getenv("LAMP_TEST_CLEANUP_DIR"); dir != "" {
		io.Copy(io.Discard, os.Stdin)
		if err := os.RemoveAll(dir); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("LAMP_TEST_CLEANUP_PROBE") != "" {
		dir, err := os.MkdirTemp("", "lamp-control-test-")
		if err != nil {
			panic(err)
		}
		cleanup, err := guardTestDirectory(dir)
		if err != nil {
			os.RemoveAll(dir)
			panic(err)
		}
		defer cleanup()
		if err := os.WriteFile(filepath.Join(dir, "binary"), []byte("test"), 0600); err != nil {
			panic(err)
		}
		json.NewEncoder(os.Stdout).Encode(dir)
		for {
			time.Sleep(time.Hour)
		}
	}
	return false
}

func TestControlBinaryDirectoryCleanedAfterParentKill(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable)
	child.Env = append(os.Environ(), "LAMP_TEST_CLEANUP_PROBE=1")
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	paths := make(chan string, 1)
	go func() { var dir string; json.NewDecoder(output).Decode(&dir); paths <- dir }()
	var dir string
	select {
	case dir = <-paths:
	case <-time.After(10 * time.Second):
		t.Fatal("cleanup probe did not start")
	}
	if dir == "" {
		t.Fatal("missing cleanup directory")
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if _, err := os.Stat(filepath.Join(dir, "binary")); err != nil {
		t.Fatal(err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	child.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := os.Stat(dir)
		if os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("killed test left directory %s: %v", dir, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
