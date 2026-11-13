package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lamp.eilmer.si/internal/localconfig"
	"lamp.eilmer.si/internal/surface"
)

// sandboxGit uses the public exec interface and returns a branch without
// checking out or overwriting the caller's files.
func sandboxGit(action string, args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("lamp sandbox "+action, flag.ContinueOnError)
	f.SetOutput(stderr)
	socket := f.String("socket", localconfig.Socket(), "daemon socket")
	origin := f.String("api-url", os.Getenv("LAMP_API_URL"), "API origin")
	tokenFile := f.String("token-file", "", "private token file")
	session := f.String("session-id", "", "session to return (required when the sandbox has several)")
	checkout := f.String("checkout", ".", "local git checkout receiving the branch")
	asJSON := f.Bool("json", false, "JSON output")
	if len(args) == 0 {
		return 2
	}
	sandbox := args[0]
	if f.Parse(args[1:]) != nil {
		return 2
	}
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	token, err := localconfig.Token(*tokenFile, *socket, *origin)
	if err != nil {
		return fail(err)
	}
	c := &surface.Client{Socket: *socket, URL: *origin, Token: token}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	page, err := surface.Invoke(ctx, c, "session_list", map[string]any{"limit": uint64(1000)})
	if err != nil {
		return fail(err)
	}
	var selected map[string]any
	for _, v := range page["items"].([]any) {
		item := v.(map[string]any)
		if item["sandbox_id"] != sandbox || item["state"] != "active" {
			continue
		}
		if *session != "" && item["id"] != *session {
			continue
		}
		if selected != nil {
			return fail(fmt.Errorf("select --session-id: this sandbox has several active sessions"))
		}
		selected = item
	}
	if selected == nil {
		return fail(fmt.Errorf("no active session in sandbox"))
	}
	id := selected["id"].(string)
	cwd := fmt.Sprintf("/tmp/lamp-sessions/%s.e%v/workspace", id, selected["location_epoch"])
	if shared, ok := selected["shared_session_id"].(string); ok && shared != "" {
		cwd = fmt.Sprintf("/tmp/lamp-sessions/%s.e%v/workspace", shared, selected["shared_epoch"])
	}
	gitArgs := []string{"git", "-c", "core.hooksPath=/dev/null", "-C", cwd}
	if action == "diff" {
		// Resolve the recorded base in the guest; no host shell or private worker API.
		script := `import pathlib,subprocess,sys
p=pathlib.Path(sys.argv[1]);base=(p/'.git/lamp-base').read_text().strip()
sys.exit(subprocess.call(['git','-c','core.hooksPath=/dev/null','-C',str(p),'diff','--binary',base]))`
		return quickstart([]string{"sandbox", "exec", sandbox, "--socket", *socket, "--api-url", *origin, "--token-file", *tokenFile, "--", "python3", "-c", script, cwd}, stdout, stderr)
	}
	script := `import base64,os,subprocess,sys,tempfile
fd,p=tempfile.mkstemp();os.close(fd);os.unlink(p)
try:
 subprocess.run(sys.argv[1:]+['bundle','create',p,'HEAD'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 print(base64.b64encode(open(p,'rb').read()).decode())
finally:
 if os.path.exists(p):os.unlink(p)`
	var output bytes.Buffer
	command := append([]string{"sandbox", "exec", sandbox, "--socket", *socket, "--api-url", *origin, "--token-file", *tokenFile, "--", "python3", "-c", script}, gitArgs...)
	if code := quickstart(command, &output, stderr); code != 0 {
		return code
	}
	bundle, err := base64.StdEncoding.DecodeString(strings.TrimSpace(output.String()))
	if err != nil {
		return fail(err)
	}
	temp, err := os.CreateTemp("", "lamp-changes-*.bundle")
	if err != nil {
		return fail(err)
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(bundle); err != nil {
		temp.Close()
		return fail(err)
	}
	if err = temp.Close(); err != nil {
		return fail(err)
	}
	branch := "lamp/" + id
	directory, err := filepath.Abs(*checkout)
	if err != nil {
		return fail(err)
	}
	cmd := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "-C", directory, "fetch", "--no-tags", "--", temp.Name(), "HEAD:refs/heads/"+branch)
	cmd.Stdout, cmd.Stderr = stderr, stderr
	if err = cmd.Run(); err != nil {
		return fail(err)
	}
	if *asJSON {
		json.NewEncoder(stdout).Encode(map[string]any{"branch": branch, "session_id": id, "checkout": directory})
	} else {
		fmt.Fprintln(stdout, "Returned changes on branch", branch)
	}
	return 0
}

// teleportArgs resolves a friendly destination using reads only. Mutation still
// goes through the canonical session operation and its revision check.
func teleportArgs(args, globals []string) ([]string, error) {
	var to, sandbox, host string
	filtered := append([]string{}, args[:2]...)
	for i := 2; i < len(args); i++ {
		key, val, has := strings.Cut(args[i], "=")
		if key != "--to" && key != "--sandbox-id" && key != "--host-id" {
			filtered = append(filtered, args[i])
			continue
		}
		if !has {
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("%s requires a value", key)
			}
			val = args[i]
		}
		switch key {
		case "--to":
			to = val
		case "--sandbox-id":
			sandbox = val
		case "--host-id":
			host = val
		}
	}
	if to == "" {
		return args, nil
	}
	location := map[string]any{}
	if to == "local" || strings.HasPrefix(to, "host:") {
		if strings.HasPrefix(to, "host:") {
			host = strings.TrimPrefix(to, "host:")
		}
		if host == "" {
			host = "hs_local"
		}
		location = map[string]any{"kind": "host", "host_id": host}
	} else {
		if strings.HasPrefix(to, "sandbox:") {
			sandbox = strings.TrimPrefix(to, "sandbox:")
		} else if to != "sandbox" {
			return nil, fmt.Errorf("use --to local, host:<id>, sandbox --sandbox-id <id>, or sandbox:<id>")
		}
		if sandbox == "" {
			return nil, fmt.Errorf("--sandbox-id is required")
		}
		f := flag.NewFlagSet("connection", flag.ContinueOnError)
		f.SetOutput(io.Discard)
		socket := f.String("socket", localconfig.Socket(), "")
		origin := f.String("api-url", os.Getenv("LAMP_API_URL"), "")
		tokenFile := f.String("token-file", "", "")
		f.Bool("json", false, "")
		if err := f.Parse(globals); err != nil {
			return nil, err
		}
		token, err := localconfig.Token(*tokenFile, *socket, *origin)
		if err != nil {
			return nil, err
		}
		client := &surface.Client{Socket: *socket, URL: *origin, Token: token}
		defer client.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, err := surface.Invoke(ctx, client, "sandbox_get", map[string]any{"sandbox_id": sandbox})
		if err != nil {
			return nil, err
		}
		resource := response["data"].(map[string]any)
		location = map[string]any{"kind": "sandbox", "sandbox_id": sandbox, "host_id": resource["host_id"]}
	}
	raw, _ := json.Marshal(location)
	return append(filtered, "--location", string(raw)), nil
}
