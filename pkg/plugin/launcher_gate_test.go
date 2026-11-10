package plugin_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLauncherWaitsForLimitAdmission(t *testing.T) {
	path, _ := launcher(t)
	marker := filepath.Join(t.TempDir(), "must-not-run")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--await-admission", "/usr/bin/touch", marker)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	banner := make([]byte, len("lamp.plugin/gate/1\n"))
	if _, err := io.ReadFull(output, banner); err != nil {
		t.Fatalf("no startup gate: %v", err)
	}
	if string(banner) != "lamp.plugin/gate/1\n" {
		t.Fatalf("bad startup gate %q", banner)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("program ran before resource admission")
	}
	// Refusing admission (closing the pipe) must not execute the program.
	input.Close()
	if err := cmd.Wait(); err == nil {
		t.Fatal("refused admission reported success")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("program ran after refused admission")
	}
}
