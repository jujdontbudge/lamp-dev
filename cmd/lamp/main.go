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
	"strconv"
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
		fmt.Fprintln(stderr, "usage: lamp sandbox {create|get|fork|delete} | exec {create|get|watch} | operation get [flags] [-- argv...]")
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
	var mode, durability, resourcesJSON, argvJSON, cursor *string
	var limit *uint64
	mutation := action != "get" && action != "watch"
	switch noun + "_" + action {
	case "sandbox_create":
		templateID = flags.String("template-id", "", "immutable template ID")
		engineID = flags.String("engine-id", "", "enrolled engine ID")
		vcpus = flags.Uint64("vcpus", 2, "vCPUs")
		memory = flags.Uint64("memory-mib", 4096, "memory MiB")
		disk = flags.Uint64("disk-gib", 20, "disk GiB")
		pids = flags.Uint64("pids-max", 4096, "process limit")
		resourcesJSON = flags.String("resources-json", "", "canonical resources JSON")
		durability = flags.String("durability", "volatile", "volatile or local")
	case "sandbox_fork", "sandbox_delete":
		sandboxID = flags.String("sandbox-id", "", "source sandbox ID")
		revision = flags.Uint64("expected-revision", 0, "required source revision")
		if action == "fork" {
			mode = flags.String("fork-mode", "memory", "memory only for demo")
			durability = flags.String("durability", "volatile", "volatile or local")
		}
	case "sandbox_get":
		sandboxID = flags.String("sandbox-id", "", "sandbox ID")
	case "exec_create":
		sandboxID = flags.String("sandbox-id", "", "sandbox ID")
		cwd = flags.String("cwd", "/", "guest working directory")
		argvJSON = flags.String("argv-json", "", "command argv JSON")
	case "exec_get", "exec_watch":
		execID = flags.String("exec-id", "", "exec ID")
		if action == "watch" {
			cursor = flags.String("cursor", "", "output cursor")
			limit = flags.Uint64("limit", 100, "frame limit")
			wait = flags.Uint64("wait-ms", 0, "output wait")
		}
	case "operation_get":
		operationID = flags.String("operation-id", "", "operation ID")
	default:
		fmt.Fprintln(stderr, "unsupported engine demo command")
		return 2
	}
	if mutation {
		key = flags.String("idempotency-key", "", "retry key (generated if omitted)")
		wait = flags.Uint64("wait-ms", 0, "bounded wait, 0 returns acceptance")
	}
	parseArgs := args[2:]
	positional := ""
	if action != "create" && len(parseArgs) > 0 && !strings.HasPrefix(parseArgs[0], "-") {
		positional, parseArgs = parseArgs[0], parseArgs[1:]
	}
	if err := flags.Parse(parseArgs); err != nil {
		return 2
	}
	if positional != "" {
		var id *string
		switch noun {
		case "sandbox":
			id = sandboxID
		case "exec":
			id = execID
		case "operation":
			id = operationID
		}
		if id == nil || (*id != "" && *id != positional) {
			fmt.Fprintln(stderr, "conflicting resource IDs")
			return 2
		}
		*id = positional
	}
	if mutation && *key == "" {
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
		du := api.SandboxCreateInputDurability(*durability)
		in.Durability = &du
		if *resourcesJSON != "" {
			var resources api.Resources
			d := json.NewDecoder(strings.NewReader(*resourcesJSON))
			d.DisallowUnknownFields()
			if err := d.Decode(&resources); err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
			if err := d.Decode(new(any)); err != io.EOF {
				fmt.Fprintln(stderr, "expected one resources JSON object")
				return 2
			}
			in.Resources = &resources
		}
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
	case "sandbox_delete":
		if !require(sandboxID) || *revision == 0 {
			fmt.Fprintln(stderr, "sandbox ID and --expected-revision required")
			return 2
		}
		input = api.SandboxDeleteInput{SandboxId: *sandboxID, ExpectedRevision: *revision, IdempotencyKey: *key, WaitMs: wait}
		method, path = "DELETE", "/v1/sandboxes/"+url.PathEscape(*sandboxID)
	case "exec_create":
		argv := flags.Args()
		if *argvJSON != "" {
			if len(argv) != 0 || json.Unmarshal([]byte(*argvJSON), &argv) != nil {
				fmt.Fprintln(stderr, "invalid or conflicting argv")
				return 2
			}
		}
		if !require(sandboxID) || len(argv) == 0 {
			fmt.Fprintln(stderr, "--sandbox-id and -- argv... are required")
			return 2
		}
		input = api.ExecCreateInput{SandboxId: *sandboxID, Argv: argv, Cwd: cwd, IdempotencyKey: *key, WaitMs: wait}
		method, path = "POST", "/v1/execs"
	case "sandbox_get":
		if !require(sandboxID) {
			fmt.Fprintln(stderr, "--sandbox-id is required")
			return 2
		}
		path = "/v1/sandboxes/" + url.PathEscape(*sandboxID)
	case "exec_get", "exec_watch":
		if !require(execID) {
			fmt.Fprintln(stderr, "--exec-id is required")
			return 2
		}
		path = "/v1/execs/" + url.PathEscape(*execID)
		if action == "watch" {
			q := url.Values{"limit": {strconv.FormatUint(*limit, 10)}, "wait_ms": {strconv.FormatUint(*wait, 10)}}
			if *cursor != "" {
				q.Set("cursor", *cursor)
			}
			path += "/output?" + q.Encode()
		}
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
	if mutation {
		// Generated inputs are transport-neutral. REST carries authority selectors
		// in headers, never as mutation body fields.
		var body map[string]json.RawMessage
		if err := json.Unmarshal(data, &body); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		delete(body, "idempotency_key")
		delete(body, "expected_revision")
		if noun == "sandbox" && (action == "fork" || action == "delete") {
			delete(body, "sandbox_id")
		}
		data, err = json.Marshal(body)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
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
	if key != nil {
		request.Header.Set("Idempotency-Key", *key)
	}
	if revision != nil {
		request.Header.Set("If-Match", strconv.FormatUint(*revision, 10))
	}
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
