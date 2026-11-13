// lamp binds every command to the canonical HTTP API; it has no engine path.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lamp.eilmer.si/internal/localconfig"
	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	args = globalArgs(args)
	if len(args) >= 2 && args[0] == "sandbox" && (args[1] == "exec" || args[1] == "run") {
		return quickstart(args, stdout, stderr)
	}
	preview := len(args) >= 2 && ((args[0] == "sandbox" && args[1] == "preview") || (args[0] == "port" && args[1] == "expose"))
	if preview {
		if args[0] == "sandbox" {
			if len(args) < 4 {
				return 2
			}
			args = append([]string{"port", "create", "--sandbox-id", args[2], "--port", args[3]}, args[4:]...)
		} else {
			args[1] = "create"
		}
	}
	asJSON := false
	for _, s := range args {
		if s == "--" {
			break
		}
		if s == "--json" {
			asJSON = true
		}
	}
	usage := func(message string) int { cliError(stderr, "INVALID_ARGUMENT", message, asJSON); return 2 }
	// Pull connection flags out before resolving resource and action.
	globals := []string{}
	local := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			local = append(local, args[i:]...)
			break
		}
		key := strings.SplitN(arg, "=", 2)[0]
		if key == "--json" || key == "--socket" || key == "--api-url" || key == "--token-file" {
			globals = append(globals, arg)
			if key != "--json" && !strings.Contains(arg, "=") {
				i++
				if i >= len(args) {
					return usage(key + " requires a value")
				}
				globals = append(globals, args[i])
			}
		} else {
			local = append(local, arg)
		}
	}
	args = local
	if len(args) == 0 {
		return help(stdout, nil, asJSON, globals)
	}
	helpWords := []string{}
	wantsHelp := args[0] == "help"
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" {
			wantsHelp = true
		} else if arg != "help" {
			helpWords = append(helpWords, arg)
		}
	}
	if wantsHelp {
		return help(stdout, helpWords, asJSON, globals)
	}
	if args[0] == "--version" {
		args[0] = "version"
	}
	if strings.HasPrefix(args[0], "-") {
		return usage("unknown flag " + strconv.Quote(args[0]) + suggestion(args[0], []string{"--version", "--help", "--socket", "--token-file", "--api-url", "--json"}))
	}
	if len(args) > 0 && args[0] == "version" {
		if len(args) > 2 || len(args) == 2 && args[1] != "--json" {
			return usage("usage: lamp version [--json]")
		}
		json.NewEncoder(stdout).Encode(map[string]string{"version": "0.1.0-dev", "protocol": "lamp.worker/2"})
		return 0
	}
	if len(args) > 0 && args[0] == "quota" {
		args = append([]string{"metric", "list"}, args[1:]...)
	}
	if len(args) < 2 {
		return usage("usage: lamp <resource> <action> [flags]")
	}
	name := strings.ReplaceAll(args[0], "-", "_") + "_" + strings.ReplaceAll(args[1], "-", "_")
	waitingSandbox := name == "sandbox_wait"
	lookup := name
	if waitingSandbox {
		lookup = "sandbox_get"
	}
	op, ok := surface.ByName[lookup]
	if !ok {
		choices := []string{}
		for _, candidate := range surface.InstalledOperations(false) {
			choices = append(choices, strings.TrimPrefix(candidate.CLI, "lamp "))
		}
		return usage("unknown command " + strconv.Quote(strings.Join(args[:2], " ")) + suggestion(strings.Join(args[:2], " "), choices))
	}
	for _, arg := range args[2:] {
		if name == "browser_action" && strings.SplitN(strings.TrimLeft(arg, "-"), "=", 2)[0] == "arguments-json" {
			return usage("browser arguments require --input file or private --input -; not argv")
		}

	}
	flags := flag.NewFlagSet(op.CLI, flag.ContinueOnError)
	var parseLog bytes.Buffer
	flags.SetOutput(&parseLog)
	flags.Usage = func() {}
	socket := localconfig.Socket()
	flags.StringVar(&socket, "socket", socket, "lampd Unix socket")
	apiURL := flags.String("api-url", os.Getenv("LAMP_API_URL"), "HTTP API origin or lamp+fleet:///absolute/discovery.json")
	tokenFile := flags.String("token-file", "", "private bearer file (or LAMP_TOKEN)")
	jsonFlag := flags.Bool("json", false, "machine-readable result")
	inputFile := flags.String("input", "", "canonical JSON input file; - reads private stdin")
	privatePNG := flags.String("private-png", "", "new private screenshot file; image data is never printed")
	follow := flags.Bool("follow", false, "NDJSON events until interrupted")
	desiredState := "ready"
	timeout := 60 * time.Second
	if waitingSandbox {
		flags.StringVar(&desiredState, "state", desiredState, "sandbox wait target state")
	}
	if waitingSandbox || name == "operation_wait" {
		flags.DurationVar(&timeout, "timeout", timeout, "wait timeout (e.g. 60s)")
	}
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
		setter := func(raw string) error {
			if name == "browser_action" && field == "arguments" {
				return fmt.Errorf("browser arguments require --input file or private --input -; not argv")
			}
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
			if field == "scope" {
				if err := scopeShorthand(v); err != nil {
					return err
				}
			}
			in[field] = v
			return nil
		}
		flags.Func(flagName, "canonical "+field, setter)
		if typ == "object" || typ == "array" {
			flags.Func(strings.TrimSuffix(flagName, "-json"), "canonical "+field+" (JSON)", setter)
		}
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
	remaining = append(append([]string{}, globals...), remaining...)
	if err := flags.Parse(remaining); err != nil {
		message := strings.TrimSpace(parseLog.String())
		if strings.Contains(message, "flag provided but not defined:") {
			bad := strings.TrimSpace(strings.TrimPrefix(message, "flag provided but not defined:"))
			choices := []string{}
			flags.VisitAll(func(f *flag.Flag) { choices = append(choices, "-"+f.Name) })
			message += suggestion(bad, choices)
		}
		return usage(message)
	}
	asJSON = *jsonFlag
	if *inputFile != "" {
		var data []byte
		var e error
		if *inputFile == "-" {
			data, e = io.ReadAll(io.LimitReader(os.Stdin, (96<<10)+1))
		} else {
			data, e = os.ReadFile(*inputFile)
		}
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
			if k == "scope" {
				if err := scopeShorthand(v); err != nil {
					return usage(err.Error())
				}
			}
			in[k] = v
		}
	}
	if positional != "" {
		field := strings.ReplaceAll(args[0], "-", "_") + "_id"
		if _, exists := props[field]; !exists {
			return usage("unexpected resource ID")
		}
		if existing, exists := in[field]; exists && existing != positional {
			return usage("positional ID conflicts with " + field + " in --input or flags")
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
		shorthand := false
		flags.Visit(func(f *flag.Flag) {
			if _, ok := dimensions[f.Name]; ok {
				shorthand = true
			}
		})
		if _, supplied := in["resources"]; supplied && shorthand {
			return usage("--resources cannot be combined with --vcpus, --memory-mib, --disk-gib or --pids-max")
		}
	}
	if preview {
		if _, ok := in["wait_ms"]; !ok {
			in["wait_ms"] = json.Number("60000")
		}
	}
	automaticKey := false
	if op.Mutation {
		if _, ok := in["idempotency_key"]; !ok {
			automaticKey = true
			b := make([]byte, 32)
			if _, e := rand.Read(b); e != nil {
				return usage("random source unavailable")
			}
			in["idempotency_key"] = "cli-" + hex.EncodeToString(b)
		}
	}
	token, tokenErr := localconfig.Token(*tokenFile, socket, *apiURL)
	if tokenErr != nil {
		return usage(tokenErr.Error())
	}
	client := &surface.Client{Socket: socket, URL: *apiURL, Token: token}
	defer client.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if waitingSandbox || name == "operation_wait" {
		if _, _, _, err := surface.Request(op, in); err != nil {
			return usage(err.Error())
		}
		return waitFor(ctx, client, op, in, desiredState, timeout, stdout, stderr, asJSON)
	}
	// Explicit revisions are preserved. Omitted revisions use a current read,
	// with no automatic retry on conflict (so concurrent edits remain visible).
	if op.Mutation {
		if _, needed := props["expected_revision"]; needed {
			if _, given := in["expected_revision"]; !given {
				in["expected_revision"] = json.Number("0")
				_, _, _, err := surface.Request(op, in)
				delete(in, "expected_revision")
				if err != nil {
					return usage(err.Error())
				}
				kind, _, _ := strings.Cut(op.Scope, ".")
				get, exists := surface.ByName[kind+"_get"]
				if !exists {
					return usage("--expected-revision is required for this command")
				}
				readInput := map[string]any{}
				readProps, _ := surface.Definition(get.InputSchema)["properties"].(map[string]any)
				for field := range readProps {
					if value, exists := in[field]; exists {
						readInput[field] = value
					}
				}
				raw, status, err := client.Call(ctx, get, readInput)
				if err != nil {
					cliError(stderr, "DATA_UNAVAILABLE", err.Error(), asJSON)
					return 6
				}
				if status >= 400 {
					emit(stdout, stderr, raw, status, asJSON)
					return statusExitCode(status)
				}
				reply, err := surface.Decode(raw)
				if err != nil {
					return usage("cannot read current revision")
				}
				resource, _ := reply["data"].(map[string]any)
				revision, exists := resource["revision"]
				if !exists {
					return usage("current resource has no revision; specify --expected-revision")
				}
				in["expected_revision"] = revision
			}
		}
	}
	if name == "sandbox_create" {
		if err := client.CreateDefaults(ctx, in); err != nil {
			cliError(stderr, "INVALID_ARGUMENT", err.Error(), asJSON)
			return 2
		}
		if in["wait_ms"] == nil {
			in["wait_ms"] = uint64(60000)
		}
		shorthand := false
		flags.Visit(func(f *flag.Flag) {
			if _, ok := dimensions[f.Name]; ok {
				shorthand = true
			}
		})
		r, _ := in["resources"].(map[string]any)
		if r == nil && shorthand {
			r = map[string]any{"vcpus": uint64(2), "memory_mib": uint64(4096), "disk_gib": uint64(20), "pids_max": uint64(4096)}
		}
		flags.Visit(func(f *flag.Flag) {
			if v, ok := dimensions[f.Name]; ok {
				r[strings.ReplaceAll(f.Name, "-", "_")] = *v
			}
		})
		if r != nil {
			in["resources"] = r
		}
	}
	// Validate before transport so malformed local requests always exit 2.
	if _, _, _, err := surface.Request(op, in); err != nil {
		return usage(err.Error())
	}
	if automaticKey && name == "sandbox_create" {
		in["idempotency_key"] = client.CreateRetryKey(ctx, in, strings.TrimPrefix(in["idempotency_key"].(string), "cli-"))
	}
	if *follow {
		if name != "event_list" {
			return usage("--follow is supported by event list")
		}
		return followEvents(ctx, client, op, in, stdout, stderr, asJSON)
	}
	data, status, err := callProgress(ctx, client, op, in, stderr, asJSON)
	if err != nil {
		message := err.Error()
		if op.Mutation {
			message += "; outcome unknown; retry only with the same idempotency_key=" + fmt.Sprint(in["idempotency_key"])
		}
		cliError(stderr, "OUTCOME_UNKNOWN", message, asJSON)
		return 6
	}
	if preview && err == nil && status < 400 {
		var reply map[string]any
		if json.Unmarshal(data, &reply) == nil {
			refs, _ := reply["resource_ids"].([]any)
			if len(refs) > 0 {
				ref, _ := refs[0].(map[string]any)
				id, _ := ref["id"].(string)
				if id != "" {
					data, status, err = client.Call(ctx, surface.ByName["port_get"], map[string]any{"port_id": id})
					if err != nil {
						cliError(stderr, "DATA_UNAVAILABLE", err.Error(), asJSON)
						return 6
					}
					var out map[string]any
					if json.Unmarshal(data, &out) == nil {
						if port, ok := out["data"].(map[string]any); ok {
							port["url"] = port["origin"]
						}
						data, _ = json.Marshal(out)
					}
				}
			}
		}
	}
	if !json.Valid(data) {
		cliError(stderr, "DATA_UNAVAILABLE", "invalid API JSON", asJSON)
		return 6
	}
	if name == "browser_action" && fmt.Sprint(in["action"]) == "screenshot" && status < 400 && !operationFailed(data) {
		var reply struct {
			Result struct {
				Observations struct {
					PNG string `json:"png"`
				} `json:"observations"`
			} `json:"result"`
		}
		if *privatePNG == "" || json.Unmarshal(data, &reply) != nil || reply.Result.Observations.PNG == "" {
			return usage("private screenshot requires --private-png and a current owner screenshot response")
		}
		image, err := base64.StdEncoding.DecodeString(reply.Result.Observations.PNG)
		if err != nil {
			return usage("invalid private screenshot")
		}
		file, err := os.OpenFile(*privatePNG, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return usage("cannot create new private screenshot file")
		}
		_, err = file.Write(image)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return usage("cannot save private screenshot")
		}
		data = []byte("{\"private_png_saved\":true}\n")
	}
	emit(stdout, stderr, data, status, asJSON)
	if status < 400 && operationFailed(data) {
		return 1
	}
	if status >= 400 {
		return statusExitCode(status)
	}
	if surface.MutationFailed(data) {
		return 1
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
	if _, ok := in["wait_ms"]; !ok {
		in["wait_ms"] = uint64(25000)
	}
	for {
		r, e := c.Open(ctx, op, in, true)
		if e != nil {
			if ctx.Err() != nil {
				return 0
			}
			cliError(errs, "DATA_UNAVAILABLE", e.Error(), asJSON)
			return 6
		}
		if r.StatusCode != 200 {
			data, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
			r.Body.Close()
			emit(out, errs, data, r.StatusCode, asJSON)
			return statusExitCode(r.StatusCode)
		}
		scanner := bufio.NewScanner(r.Body)
		scanner.Buffer(make([]byte, 4096), 96<<10)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, ": cursor ") {
				in["cursor"] = strings.TrimPrefix(line, ": cursor ")
			}
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				event, err := surface.Decode([]byte(data))
				if err != nil {
					r.Body.Close()
					cliError(errs, "DATA_UNAVAILABLE", "invalid event", asJSON)
					return 6
				}
				if cursor, ok := event["cursor"].(string); ok {
					in["cursor"] = cursor
				}
				if _, e := fmt.Fprintln(out, data); e != nil {
					r.Body.Close()
					return 6
				}
			}
		}
		r.Body.Close()
		if ctx.Err() != nil {
			return 0
		}
		if e := scanner.Err(); e != nil {
			cliError(errs, "DATA_UNAVAILABLE", e.Error(), asJSON)
			return 6
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(100 * time.Millisecond):
		}
	}
}
