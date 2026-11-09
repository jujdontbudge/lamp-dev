// lamp uses the same local HTTP API as engine gate callers; it has no engine path.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 2 && args[0] == "version" && args[1] == "--json" {
		json.NewEncoder(stdout).Encode(map[string]string{"version": "0.1.0-dev", "protocol": "lamp.worker/1"})
		return 0
	}
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: lamp sandbox {create|get|fork} | exec {create|get} | operation get [flags] [-- argv...]")
		return 2
	}
	noun, action := args[0], args[1]
	flags := flag.NewFlagSet("lamp "+noun+" "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	home, _ := os.UserHomeDir()
	defaultSocket := os.Getenv("LAMP_SOCKET")
	if defaultSocket == "" {
		defaultSocket = filepath.Join(home, ".local", "state", "lamp-demo", "api.sock")
	}
	socket := flags.String("socket", defaultSocket, "lampd Unix socket (or LAMP_SOCKET)")
	asJSON := flags.Bool("json", false, "emit the canonical API response")
	var templateID, engineID, sandboxID, operationID, execID, key, cwd *string
	var wait, revision, vcpus, memory, disk, pids *uint64
	var mode, durability *string
	switch noun + "_" + action {
	case "sandbox_create":
		templateID = flags.String("template-id", "", "immutable template ID")
		engineID = flags.String("engine-id", "", "enrolled engine ID")
		vcpus = flags.Uint64("vcpus", 2, "vCPUs")
		memory = flags.Uint64("memory-mib", 4096, "memory MiB")
		disk = flags.Uint64("disk-gib", 20, "disk GiB")
		pids = flags.Uint64("pids-max", 1024, "process limit")
	case "sandbox_fork":
		sandboxID = flags.String("sandbox-id", "", "source sandbox ID")
		revision = flags.Uint64("expected-revision", 0, "required source revision")
		mode = flags.String("fork-mode", "memory", "memory only for demo")
		durability = flags.String("durability", "volatile", "volatile only for demo")
	case "sandbox_get":
		sandboxID = flags.String("sandbox-id", "", "sandbox ID")
	case "exec_create":
		sandboxID = flags.String("sandbox-id", "", "sandbox ID")
		cwd = flags.String("cwd", "/", "guest working directory")
	case "exec_get":
		execID = flags.String("exec-id", "", "exec ID")
	case "operation_get":
		operationID = flags.String("operation-id", "", "operation ID")
	default:
		fmt.Fprintln(stderr, "unsupported engine demo command")
		return 2
	}
	if action != "get" {
		key = flags.String("idempotency-key", "", "retry key (generated if omitted)")
		wait = flags.Uint64("wait-ms", 0, "bounded wait, 0 returns acceptance")
	}
	if err := flags.Parse(args[2:]); err != nil {
		return 2
	}
	if action != "get" && *key == "" {
		*key = fmt.Sprintf("cli-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	method, path := "GET", ""
	var input any
	require := func(v *string) bool { return v != nil && *v != "" && !strings.ContainsAny(*v, "/\x00") }
	switch noun + "_" + action {
	case "sandbox_create":
		if !require(templateID) {
			fmt.Fprintln(stderr, "--template-id is required")
			return 2
		}
		in := api.SandboxCreateInput{TemplateId: templateID, IdempotencyKey: *key, WaitMs: wait, Resources: &api.Resources{Vcpus: *vcpus, MemoryMib: *memory, DiskGib: *disk, PidsMax: *pids}}
		if *engineID != "" {
			in.EngineId = engineID
		}
		input = in
		method, path = "POST", "/v1/sandboxes"
	case "sandbox_fork":
		if !require(sandboxID) || *revision == 0 {
			fmt.Fprintln(stderr, "--sandbox-id and --expected-revision are required")
			return 2
		}
		fm := api.SandboxForkInputForkMode(*mode)
		du := api.SandboxForkInputDurability(*durability)
		input = api.SandboxForkInput{SandboxId: *sandboxID, ExpectedRevision: *revision, ForkMode: &fm, Durability: &du, IdempotencyKey: *key, WaitMs: wait}
		method, path = "POST", "/v1/sandboxes/"+url.PathEscape(*sandboxID)+":fork"
	case "exec_create":
		if !require(sandboxID) || len(flags.Args()) == 0 {
			fmt.Fprintln(stderr, "--sandbox-id and -- argv... are required")
			return 2
		}
		input = api.ExecCreateInput{SandboxId: *sandboxID, Argv: flags.Args(), Cwd: cwd, IdempotencyKey: *key, WaitMs: wait}
		method, path = "POST", "/v1/execs"
	case "sandbox_get":
		if !require(sandboxID) {
			fmt.Fprintln(stderr, "--sandbox-id is required")
			return 2
		}
		path = "/v1/sandboxes/" + url.PathEscape(*sandboxID)
	case "exec_get":
		if !require(execID) {
			fmt.Fprintln(stderr, "--exec-id is required")
			return 2
		}
		path = "/v1/execs/" + url.PathEscape(*execID)
	case "operation_get":
		if !require(operationID) {
			fmt.Fprintln(stderr, "--operation-id is required")
			return 2
		}
		path = "/v1/operations/" + url.PathEscape(*operationID)
	}
	if noun+"_"+action != "exec_create" && flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	data, err := json.Marshal(input)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", *socket)
	}}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, method, "http://lamp"+path, bytes.NewReader(data))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		fmt.Fprintln(stderr, "lamp:", err)
		return 1
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 || !json.Valid(body) {
		fmt.Fprintln(stderr, "lamp: invalid or oversized API response")
		return 1
	}
	target := stdout
	if response.StatusCode >= 400 {
		target = stderr
	}
	if *asJSON {
		_, err = target.Write(body)
	} else {
		var pretty bytes.Buffer
		err = json.Indent(&pretty, body, "", "  ")
		if err == nil {
			_, err = target.Write(append(pretty.Bytes(), '\n'))
		}
	}
	if err != nil || response.StatusCode >= 400 {
		return 1
	}
	return 0
}
