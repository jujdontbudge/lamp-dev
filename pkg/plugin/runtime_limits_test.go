package plugin

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func controllerFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, value := range map[string]string{"memory.max": "67108864", "memory.swap.max": "0", "pids.max": "32", "cpu.max": "25000 100000"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
func TestKernelLimitsRequireActualControllerFiles(t *testing.T) {
	for _, tc := range []struct {
		name, file, value string
		missing           bool
	}{
		{name: "no memory delegation", file: "memory.max", missing: true},
		{name: "no CPU delegation", file: "cpu.max", missing: true},
		{name: "unlimited memory", file: "memory.max", value: "max"},
		{name: "unlimited CPU", file: "cpu.max", value: "max 100000"},
		{name: "memory above ceiling", file: "memory.max", value: "134217728"},
		{name: "CPU above ceiling", file: "cpu.max", value: "25001 100000"},
		{name: "CPU overflow", file: "cpu.max", value: "18446744073709551615 100000"},
		{name: "no swap accounting", file: "memory.swap.max", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := controllerFixture(t)
			if tc.missing {
				if err := os.Remove(filepath.Join(dir, tc.file)); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := verifyCgroupLimits(dir, DefaultLimits()); err == nil {
				t.Fatal("unconfirmed kernel limit accepted")
			}
		})
	}
	if err := verifyCgroupLimits(controllerFixture(t), DefaultLimits()); err != nil {
		t.Fatal(err)
	}
}

func TestMissingControllersKeepStartupGateClosed(t *testing.T) {
	for _, missing := range []string{"memory.max", "cpu.max"} {
		t.Run(missing, func(t *testing.T) {
			// Fault injection is at the cgroup-observation seam only. The bootstrap,
			// pipes and denied execution are real; this is not OS qualification evidence.
			dir := t.TempDir()
			helper := filepath.Join(dir, "launcher")
			marker := filepath.Join(dir, "untrusted-program-ran")
			if out, err := exec.Command("cc", "-O2", "-Wall", "-Wextra", "-Werror", "-o", helper, "../../tools/plugin-launcher.c").CombinedOutput(); err != nil {
				t.Fatalf("bootstrap: %s %v", out, err)
			}
			cgroup := controllerFixture(t)
			if err := os.Remove(filepath.Join(cgroup, missing)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, helper, "--await-admission", "/usr/bin/touch", marker)
			input, _ := cmd.StdinPipe()
			output, _ := cmd.StdoutPipe()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			w := &Worker{pkg: &Package{manifest: Manifest{Limits: DefaultLimits()}}, stdin: input, stdout: output, limits: runtimeProof{qualify: func(context.Context) error { return verifyCgroupLimits(cgroup, DefaultLimits()) }}}
			if err := w.admitRuntime(ctx); err == nil {
				t.Fatal("startup admitted without required controller")
			}
			input.Close()
			_, _ = io.Copy(io.Discard, output)
			if err := cmd.Wait(); err == nil {
				t.Fatal("unqualified bootstrap returned success")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("untrusted program ran without confirmed limits")
			}
		})
	}
}
