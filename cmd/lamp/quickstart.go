package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lamp.eilmer.si/internal/localconfig"
	"lamp.eilmer.si/internal/surface"
)

// Global connection flags work before or after the command, including --x=y.
func globalArgs(args []string) []string {
	var globals, rest, tail []string
	for i := 0; i < len(args); i++ {
		name := strings.SplitN(args[i], "=", 2)[0]
		switch name {
		case "--socket", "--api-url", "--token-file":
			globals = append(globals, args[i])
			if name == args[i] && i+1 < len(args) {
				i++
				globals = append(globals, args[i])
			}
		case "--json":
			globals = append(globals, args[i])
		case "--":
			tail = args[i:]
			i = len(args)
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) == 0 {
		return args
	}
	out := append(rest, globals...)
	return append(out, tail...)
}

func quickCall(ctx context.Context, c *surface.Client, name string, in map[string]any) (map[string]any, error) {
	data, status, err := c.Call(ctx, surface.ByName[name], in)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err = json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if status >= 400 {
		return out, fmt.Errorf("%s", strings.TrimSpace(string(data)))
	}
	return out, nil
}
func resourceID(out map[string]any) string {
	refs, _ := out["resource_ids"].([]any)
	if len(refs) > 0 {
		ref, _ := refs[0].(map[string]any)
		id, _ := ref["id"].(string)
		return id
	}
	return ""
}
func quickKey() string { return fmt.Sprintf("cli-quick-%d", time.Now().UnixNano()) }

func quickstart(args []string, stdout, stderr io.Writer) (exit int) {
	verb := args[1]
	for _, arg := range args[2:] {
		if arg == "--" {
			break
		}
		if arg == "--help" {
			fmt.Fprintln(stdout, "usage: lamp sandbox run [--template-id ID] -- COMMAND [ARG...]\n       lamp sandbox exec ID -- COMMAND [ARG...]\nrun creates a private 2 vCPU / 2048 MiB / 20 GiB VM, waits until ready, runs code, then deletes it.\nexec uses an existing sandbox. Exit status and stdout/stderr come from the guest.\nPersistent create defaults: idle suspend after 300 s; suspended retention 7 days; previews private.")
			return 0
		}
	}
	flags := flag.NewFlagSet("sandbox "+verb, flag.ContinueOnError)
	flags.SetOutput(stderr)
	socket := flags.String("socket", localconfig.Socket(), "daemon socket")
	url := flags.String("api-url", os.Getenv("LAMP_API_URL"), "API URL")
	tokenFile := flags.String("token-file", "", "private owner token file")
	template := flags.String("template-id", "", "template (default: installed default)")
	asJSON := flags.Bool("json", false, "print exec metadata as JSON")
	rest := args[2:]
	id := ""
	// globalArgs may have placed connection flags before the positional id.
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--" {
			break
		}
		if strings.HasPrefix(rest[i], "-") {
			if !strings.Contains(rest[i], "=") && rest[i] != "--json" {
				i++
			}
			continue
		}
		if verb == "exec" {
			id = rest[i]
			rest = append(append([]string{}, rest[:i]...), rest[i+1:]...)
		}
		break
	}
	if err := flags.Parse(rest); err != nil {
		return 2
	}
	argv := flags.Args()
	if len(argv) == 0 || verb == "exec" && id == "" {
		fmt.Fprintln(stderr, "lamp: use lamp sandbox", verb, "--help")
		return 2
	}
	token, err := localconfig.Token(*tokenFile, *socket, *url)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	c := &surface.Client{Socket: *socket, URL: *url, Token: token}
	defer c.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fail := func(err error) int { fmt.Fprintln(stderr, "lamp:", err); return 1 }
	if verb == "run" {
		in := map[string]any{"idempotency_key": quickKey(), "wait_ms": uint64(60000)}
		if *template != "" {
			in["template_id"] = *template
		}
		if err := c.CreateDefaults(ctx, in); err != nil {
			return fail(err)
		}
		out, e := quickCall(ctx, c, "sandbox_create", in)
		id = resourceID(out)
		if id == "" && e != nil {
			detail, _ := out["error"].(map[string]any)
			if opID, ok := detail["operation_id"].(string); ok {
				if op, err := quickCall(context.Background(), c, "operation_get", map[string]any{"operation_id": opID}); err == nil {
					data, _ := op["data"].(map[string]any)
					id = resourceID(data)
				}
			}
		}
		if id != "" {
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 65*time.Second)
				defer cancel()
				current, e := quickCall(cleanup, c, "sandbox_get", map[string]any{"sandbox_id": id})
				if e != nil {
					fmt.Fprintln(stderr, "lamp: cleanup failed:", e)
					if exit == 0 {
						exit = 1
					}
					return
				}
				data, _ := current["data"].(map[string]any)
				_, e = quickCall(cleanup, c, "sandbox_delete", map[string]any{"sandbox_id": id, "expected_revision": data["revision"], "idempotency_key": quickKey(), "wait_ms": uint64(60000)})
				if e != nil {
					fmt.Fprintln(stderr, "lamp: cleanup failed; delete sandbox", id, ":", e)
					if exit == 0 {
						exit = 1
					}
				}
			}()
		}
		if e != nil {
			return fail(e)
		}
		if id == "" {
			return fail(fmt.Errorf("create returned no sandbox"))
		}
		state, e := quickCall(ctx, c, "sandbox_get", map[string]any{"sandbox_id": id})
		if e != nil {
			return fail(e)
		}
		data, _ := state["data"].(map[string]any)
		if data["state"] != "ready" {
			return fail(fmt.Errorf("sandbox %s is %v; inspect with lamp sandbox get %s", id, data["state"], id))
		}
	}
	out, err := quickCall(ctx, c, "exec_create", map[string]any{"sandbox_id": id, "argv": argv, "idempotency_key": quickKey(), "wait_ms": uint64(60000)})
	if err != nil {
		return fail(err)
	}
	execID := resourceID(out)
	if execID == "" {
		return fail(fmt.Errorf("exec returned no identifier"))
	}
	waitCtx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	query := map[string]any{"exec_id": execID, "wait_ms": uint64(60000), "limit": uint64(1000)}
	all := []any{}
	for {
		page, err := quickCall(waitCtx, c, "exec_watch", query)
		if err != nil {
			return fail(err)
		}
		items, _ := page["items"].([]any)
		for _, v := range items {
			if *asJSON {
				all = append(all, v)
				continue
			}
			f, _ := v.(map[string]any)
			encoded, _ := f["data_base64"].(string)
			b, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return fail(err)
			}
			dst := stdout
			if f["channel"] == "stderr" {
				dst = stderr
			}
			if _, err := dst.Write(b); err != nil {
				return fail(err)
			}
		}
		cursor, _ := page["next_cursor"].(string)
		if cursor == "" {
			break
		}
		query["cursor"] = cursor
	}
	state, err := quickCall(ctx, c, "exec_get", map[string]any{"exec_id": execID})
	if err != nil {
		return fail(err)
	}
	data, _ := state["data"].(map[string]any)
	if data["exit_code"] == nil {
		return fail(fmt.Errorf("exec %s is %v; inspect with lamp exec get %s", execID, data["state"], execID))
	}
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(map[string]any{"exec": data, "output": all}); err != nil {
			return fail(err)
		}
	}
	code, _ := data["exit_code"].(float64)
	return int(code)
}
