package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"lamp.eilmer.si/internal/localconfig"
	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
)

const exitHelp = "Errors: JSON on stderr with --json. Exit: 0 success, 1 API/operation failure, 2 usage, 3 unauthenticated, 4 scope denied, 5 conflict/expired, 6 transport/timeout."

func help(out io.Writer, words []string, asJSON bool, globals []string) int {
	ops := surface.InstalledOperations(strings.HasPrefix(os.Getenv("LAMP_API_URL"), "lamp+fleet:"))
	if asJSON {
		flags := flag.NewFlagSet("help", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		socket := flags.String("socket", localconfig.Socket(), "")
		url := flags.String("api-url", os.Getenv("LAMP_API_URL"), "")
		file := flags.String("token-file", "", "")
		flags.Bool("json", false, "")
		if flags.Parse(globals) == nil {
			token, err := localconfig.Token(*file, *socket, *url)
			if err == nil {
				client := &surface.Client{Socket: *socket, URL: *url, Token: token}
				ops = client.Operations(context.Background())
				client.Close()
			}
		}
	}
	selected := []api.OperationDescriptor{}
	prefix := strings.ReplaceAll(strings.Join(words, "_"), "-", "_")
	for _, op := range ops {
		if prefix == "" || op.Name == prefix || strings.HasPrefix(op.Name, prefix+"_") {
			selected = append(selected, op)
		}
	}
	if asJSON {
		json.NewEncoder(out).Encode(map[string]any{"commands": selected, "usage": "lamp <resource> <action> [id] [flags]", "errors": exitHelp})
		return 0
	}
	fmt.Fprintln(out, "Lamp — isolated workspaces for agents")
	if len(words) == 0 {
		fmt.Fprintln(out, "Usage: lamp <resource> <action> [id] [flags]\nCommands:")
		groups := map[string]bool{}
		for _, op := range ops {
			group, _, _ := strings.Cut(strings.TrimPrefix(op.CLI, "lamp "), " ")
			groups[group] = true
		}
		names := []string{}
		for group := range groups {
			names = append(names, group)
		}
		sort.Strings(names)
		fmt.Fprintln(out, "  "+strings.Join(names, ", "))
		fmt.Fprintln(out, "\nStart: lamp sandbox run -- python3 -c 'print(6 * 7)'\n       lamp sandbox create\n       lamp sandbox exec <id> -- uname -a\nExample: lamp sandbox create --help")
	} else if len(words) == 1 {
		fmt.Fprintf(out, "Usage: lamp %s <action> [id] [flags]\nCommands:\n", words[0])
		for _, op := range selected {
			fmt.Fprintf(out, "  %-24s %s\n", strings.TrimPrefix(op.CLI, "lamp "+words[0]+" "), describe(op))
		}
		if words[0] == "sandbox" {
			fmt.Fprintln(out, "  run                      Create a default VM, run COMMAND, then delete it\n  exec                     Run COMMAND in an existing sandbox\n  wait                     Wait for a sandbox state (default ready)")
		}
		fmt.Fprintf(out, "Example: lamp %s list --json\n", words[0])
	} else if prefix == "sandbox_wait" {
		fmt.Fprintln(out, "Usage: lamp sandbox wait [id] --sandbox-id ID [--state ready] [--timeout 60s]\nWait until the sandbox reaches the state; fail on failed/deleted/unavailable.\nExample: lamp sandbox wait sb_example --timeout 60s")
	} else if op, ok := surface.ByName[prefix]; ok {
		fmt.Fprintf(out, "Usage: %s [id] [flags]\n%s; required scope: %s\nFlags (objects and arrays take JSON):\n", op.CLI, describe(op), op.Scope)
		props, _ := surface.Definition(op.InputSchema)["properties"].(map[string]any)
		required := map[string]bool{}
		if values, ok := surface.Definition(op.InputSchema)["required"].([]any); ok {
			for _, field := range values {
				required[field.(string)] = true
			}
		}
		fields := []string{}
		for field := range props {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		for _, field := range fields {
			p := props[field].(map[string]any)
			typ, _ := p["type"].(string)
			if typ == "" {
				typ = "JSON"
			}
			detail := fmt.Sprint(p["description"])
			if detail == "<nil>" {
				detail = typ
			}
			if field == "expected_revision" {
				detail = "revision; fetched automatically if omitted"
			}
			if field == "wait_ms" {
				detail = "milliseconds to wait (0–60000; create default 60000, otherwise 0)"
			}
			if field == "timeout_ms" {
				detail = "milliseconds to wait (0–60000)"
			}
			if required[field] && field != "idempotency_key" && field != "expected_revision" && !(prefix == "sandbox_create" && field == "template_id") {
				detail += "; required"
			}
			fmt.Fprintf(w, "  --%s\t%s\n", strings.ReplaceAll(field, "_", "-"), detail)
		}
		w.Flush()
		if prefix == "sandbox_create" {
			fmt.Fprintln(out, "Installed defaults: 2 CPUs, up to 2048 MiB memory sized for the host, 20 GiB disk, 4096 PIDs.\nUse lamp template list for the installed resources. Create waits up to 60 s for readiness.\nShorthands: --vcpus, --memory-mib, --disk-gib, --pids-max; --resources takes JSON.")
		}
		fmt.Fprintf(out, "Example: %s", op.CLI)
		if prefix == "key_create" {
			fmt.Fprint(out, ` --scope '{"actions":["sandbox.get","exec.create"],"sandbox_ids":["sb_example"]}' --expires-at <UTC>`)
		} else if prefix == "sandbox_create" {
			fmt.Fprint(out, " --template-id <id> --wait-ms 60000")
		} else if prefix == "exec_create" {
			fmt.Fprint(out, ` --sandbox-id <id> --argv '["uname"]' --wait-ms 60000`)
		} else if strings.Contains(op.Path, "{") {
			fmt.Fprint(out, " <id>")
		}
		fmt.Fprintln(out, " --json")
	}
	fmt.Fprintln(out, "\nGlobal flags (before or after command): --socket PATH, --api-url ORIGIN, --token-file PATH, --json.\nAuthentication: LAMP_TOKEN_FILE or LAMP_TOKEN; endpoint: LAMP_SOCKET or LAMP_API_URL.\nUse --input FILE (or - for stdin) for private or nested JSON; --help --json for the catalogue.")
	fmt.Fprintln(out, exitHelp)
	return 0
}
func describe(op api.OperationDescriptor) string {
	kind, verb, _ := strings.Cut(op.Scope, ".")
	return strings.ToUpper(verb[:1]) + verb[1:] + " " + strings.ReplaceAll(kind, "_", " ")
}

func suggestion(value string, choices []string) string {
	best, distance := "", 4
	for _, candidate := range choices {
		prev := make([]int, len(candidate)+1)
		for i := range prev {
			prev[i] = i
		}
		for i := range value {
			next := make([]int, len(candidate)+1)
			next[0] = i + 1
			for j := range candidate {
				cost := 1
				if value[i] == candidate[j] {
					cost = 0
				}
				next[j+1] = min(next[j]+1, prev[j+1]+1, prev[j]+cost)
			}
			prev = next
		}
		if d := prev[len(candidate)]; d < distance {
			best, distance = candidate, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf("; did you mean %q?", best)
}

func humanResult(out io.Writer, raw []byte) {
	m, err := surface.Decode(raw)
	if err != nil {
		fmt.Fprintln(out, string(raw))
		return
	}
	if e, ok := m["error"].(map[string]any); ok {
		fmt.Fprintf(out, "lamp: %v: %v\n", e["code"], e["message"])
		return
	}
	if items, ok := m["items"].([]any); ok {
		if len(items) == 0 {
			fmt.Fprintln(out, "No resources found. Use lamp <resource> create --help to get started.")
			return
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tTEMPLATE\tSTATE\tCPUS\tMEMORY MiB\tLABELS")
		for _, item := range items {
			row, _ := item.(map[string]any)
			res, _ := row["resources"].(map[string]any)
			labels, _ := json.Marshal(row["labels"])
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", display(row["id"]), display(row["template_id"]), display(row["state"]), display(res["vcpus"]), display(res["memory_mib"]), labels)
		}
		w.Flush()
		return
	}
	if op, ok := m["operation"].(map[string]any); ok {
		fmt.Fprintf(out, "Operation %v: %v\n", m["operation_id"], op["state"])
		if e, ok := op["error"].(map[string]any); ok {
			fmt.Fprintf(out, "%v: %v\n", e["code"], e["message"])
		}
		refs, _ := m["resource_ids"].([]any)
		for _, ref := range refs {
			r := ref.(map[string]any)
			fmt.Fprintf(out, "%v %v\n", r["kind"], r["id"])
		}
		if result, ok := m["result"]; ok && result != nil {
			formatted, _ := json.MarshalIndent(result, "", "  ")
			fmt.Fprintln(out, string(formatted))
		}
		return
	}
	var pretty map[string]any
	json.Unmarshal(raw, &pretty)
	b, _ := json.MarshalIndent(pretty, "", "  ")
	fmt.Fprintln(out, string(b))
}
func display(v any) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(v)
}

// CLI convenience for sandbox-bound scopes; REST uses ResourceRef objects.
func scopeShorthand(v any) error {
	scope, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	refs, _ := scope["resource_ids"].([]any)
	for i, ref := range refs {
		if id, ok := ref.(string); ok {
			if !strings.HasPrefix(id, "sb_") {
				return fmt.Errorf("scope.resource_ids[%d]: use {kind,id} for a non-sandbox resource", i)
			}
			refs[i] = map[string]any{"kind": "sandbox", "id": id}
		}
	}
	return nil
}
