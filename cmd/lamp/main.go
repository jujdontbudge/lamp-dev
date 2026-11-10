// lamp binds every command to the canonical HTTP API; it has no engine path.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/JohanSardar/lamp/internal/surface"
	"github.com/JohanSardar/lamp/pkg/api"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	for _, s := range args {
		if s == "--json" {
			asJSON = true
		}
	}
	usage := func(message string) int { cliError(stderr, "INVALID_ARGUMENT", message, asJSON); return 2 }
	if len(args) > 0 && (args[0] == "--help" || args[0] == "help") {
		json.NewEncoder(stdout).Encode(map[string]any{"commands": api.Operations, "usage": "lamp <resource> <action> [id] [--input file.json] [flags] [--json]", "authentication": "LAMP_TOKEN or --token-file; LAMP_SOCKET or LAMP_API_URL", "errors": "JSON errors on stderr; exit 0 success, 1 API failure, 2 usage, 3 unauthenticated, 4 scope denied, 5 conflict, 6 transport"})
		return 0
	}
	if len(args) > 0 && args[0] == "version" {
		if len(args) > 2 || len(args) == 2 && args[1] != "--json" {
			return usage("usage: lamp version [--json]")
		}
		json.NewEncoder(stdout).Encode(map[string]string{"version": "0.1.0-dev", "protocol": "lamp.worker/1"})
		return 0
	}
	if len(args) < 2 {
		return usage("usage: lamp <resource> <action> [flags]")
	}
	name := strings.ReplaceAll(args[0], "-", "_") + "_" + strings.ReplaceAll(args[1], "-", "_")
	op, ok := surface.ByName[name]
	if !ok {
		return usage("unknown operation")
	}
	for _, arg := range args[2:] {
		if arg == "--help" {
			json.NewEncoder(stdout).Encode(map[string]any{"command": op.CLI, "input_schema": surface.InputSchema(op.InputSchema), "scope": op.Scope})
			return 0
		}
	}
	flags := flag.NewFlagSet(op.CLI, flag.ContinueOnError)
	var parseLog bytes.Buffer
	flags.SetOutput(&parseLog)
	home, _ := os.UserHomeDir()
	socket := os.Getenv("LAMP_SOCKET")
	if socket == "" {
		socket = filepath.Join(home, ".local", "state", "lamp-demo", "api.sock")
	}
	flags.StringVar(&socket, "socket", socket, "lampd Unix socket")
	apiURL := flags.String("api-url", os.Getenv("LAMP_API_URL"), "HTTP API origin")
	tokenFile := flags.String("token-file", "", "private bearer file (or LAMP_TOKEN)")
	jsonFlag := flags.Bool("json", false, "machine-readable result")
	inputFile := flags.String("input", "", "canonical JSON input file")
	follow := flags.Bool("follow", false, "NDJSON events until interrupted")
	in := map[string]any{}
	props := surface.Definition(op.InputSchema)["properties"].(map[string]any)
	for field, p := range props {
		field, p := field, p.(map[string]any)
		typ, _ := p["type"].(string)
		if typ == "" {
			typ = "object"
		}
		flagName := strings.ReplaceAll(field, "_", "-")
		if typ == "object" || typ == "array" {
			flagName += "-json"
		}
		flags.Func(flagName, "canonical "+field, func(raw string) error {
			if _, exists := in[field]; exists {
				return fmt.Errorf("duplicate %s", field)
			}
			var v any
			switch typ {
			case "string":
				v = raw
			case "integer":
				if _, e := strconv.ParseUint(raw, 10, 64); e != nil {
					return e
				}
				v = json.Number(raw)
			case "boolean":
				x, e := strconv.ParseBool(raw)
				if e != nil {
					return e
				}
				v = x
			default:
				d := json.NewDecoder(strings.NewReader(raw))
				d.UseNumber()
				if e := d.Decode(&v); e != nil {
					return e
				}
				if d.Decode(new(any)) != io.EOF {
					return fmt.Errorf("expected one JSON value")
				}
			}
			in[field] = v
			return nil
		})
	}
	// Resource shorthand remains compatible with the engine benchmark caller.
	dimensions := map[string]*uint64{}
	if name == "sandbox_create" {
		for _, d := range []struct {
			name  string
			value uint64
		}{{"vcpus", 2}, {"memory-mib", 4096}, {"disk-gib", 20}, {"pids-max", 4096}} {
			dimensions[d.name] = flags.Uint64(d.name, d.value, "sandbox resource limit")
		}
	}
	remaining := args[2:]
	positional := ""
	if len(remaining) > 0 && !strings.HasPrefix(remaining[0], "-") {
		positional = remaining[0]
		remaining = remaining[1:]
	}
	if err := flags.Parse(remaining); err != nil {
		return usage(strings.TrimSpace(parseLog.String()))
	}
	asJSON = *jsonFlag
	if *inputFile != "" {
		data, e := os.ReadFile(*inputFile)
		if e != nil {
			return usage(e.Error())
		}
		if len(data) > 96<<10 {
			return usage("input exceeds 96 KiB")
		}
		body, e := surface.Decode(data)
		if e != nil {
			return usage(e.Error())
		}
		for k, v := range body {
			if _, ok := in[k]; ok {
				return usage("duplicate input field: " + k)
			}
			in[k] = v
		}
	}
	if positional != "" {
		field := strings.ReplaceAll(args[0], "-", "_") + "_id"
		if _, exists := props[field]; !exists {
			return usage("unexpected resource ID")
		}
		if _, exists := in[field]; exists {
			return usage("duplicate resource ID")
		}
		in[field] = positional
	}
	if flags.NArg() > 0 {
		if name != "exec_create" {
			return usage("unexpected arguments")
		}
		if _, exists := in["argv"]; exists {
			return usage("conflicting argv")
		}
		in["argv"] = flags.Args()
	}
	if name == "sandbox_create" {
		if _, ok := in["resources"]; !ok {
			r := map[string]any{}
			for k, v := range dimensions {
				r[strings.ReplaceAll(k, "-", "_")] = *v
			}
			in["resources"] = r
		}
	}
	if op.Mutation {
		if _, ok := in["idempotency_key"]; !ok {
			b := make([]byte, 32)
			if _, e := rand.Read(b); e != nil {
				return usage("random source unavailable")
			}
			in["idempotency_key"] = "cli-" + hex.EncodeToString(b)
		}
	}
	token := os.Getenv("LAMP_TOKEN")
	if *tokenFile != "" {
		data, e := os.ReadFile(*tokenFile)
		if e != nil {
			return usage(e.Error())
		}
		token = strings.TrimSpace(string(data))
	}
	client := &surface.Client{Socket: socket, URL: *apiURL, Token: token}
	defer client.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Validate before transport so malformed local requests always exit 2.
	if _, _, _, err := surface.Request(op, in); err != nil {
		return usage(err.Error())
	}
	if *follow {
		if name != "event_list" {
			return usage("--follow is supported by event list")
		}
		return followEvents(ctx, client, op, in, stdout, stderr, asJSON)
	}
	data, status, err := client.Call(ctx, op, in)
	if err != nil {
		message := err.Error()
		if op.Mutation {
			message += "; outcome unknown; retry only with the same idempotency_key=" + fmt.Sprint(in["idempotency_key"])
		}
		cliError(stderr, "OUTCOME_UNKNOWN", message, asJSON)
		return 6
	}
	if !json.Valid(data) {
		cliError(stderr, "DATA_UNAVAILABLE", "invalid API JSON", asJSON)
		return 6
	}
	target := stdout
	if status >= 400 {
		target = stderr
	}
	if asJSON {
		target.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			fmt.Fprintln(target)
		}
	} else {
		var pretty bytes.Buffer
		json.Indent(&pretty, data, "", "  ")
		fmt.Fprintln(target, pretty.String())
	}
	if status >= 400 {
		return statusExitCode(status)
	}
	return 0
}
func statusExitCode(status int) int {
	switch status {
	case 401:
		return 3
	case 403:
		return 4
	case 409, 410:
		return 5
	default:
		return 1
	}
}
func cliError(w io.Writer, code, message string, asJSON bool) {
	if asJSON {
		json.NewEncoder(w).Encode(api.ErrorEnvelope{RequestId: "cli", Error: api.ErrorDetail{Code: api.ErrorCode(code), Message: message}})
	} else {
		fmt.Fprintln(w, "lamp:", message)
	}
}
func followEvents(ctx context.Context, c *surface.Client, op api.OperationDescriptor, in map[string]any, out, errs io.Writer, asJSON bool) int {
	r, e := c.Open(ctx, op, in, true)
	if e != nil {
		cliError(errs, "DATA_UNAVAILABLE", e.Error(), asJSON)
		return 6
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		errs.Write(data)
		return statusExitCode(r.StatusCode)
	}
	scanner := bufio.NewScanner(r.Body)
	scanner.Buffer(make([]byte, 4096), 96<<10)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if !json.Valid([]byte(data)) {
				cliError(errs, "DATA_UNAVAILABLE", "invalid event", asJSON)
				return 6
			}
			if _, e := fmt.Fprintln(out, data); e != nil {
				return 6
			}
		}
	}
	if ctx.Err() != nil {
		return 0
	}
	if e := scanner.Err(); e != nil {
		cliError(errs, "DATA_UNAVAILABLE", e.Error(), asJSON)
		return 6
	}
	return 0
}
