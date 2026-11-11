package broker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

func validateInvocation(c Connection, in Invocation) error {
	invalid := errors.New("SCOPE_DENIED")
	switch c.Kind {
	case "model":
		model, _ := in.Arguments["model"].(string)
		if !contains(c.Models, model) {
			return invalid
		}
		if c.FreeOnly && model != "openrouter/free" && !strings.HasSuffix(model, ":free") {
			return invalid
		}
		if in.Operation == "messages" || in.Operation == "responses" {
			if tools, ok := in.Arguments["tools"].([]any); ok {
				for _, value := range tools {
					tool, ok := value.(map[string]any)
					if !ok {
						return invalid
					}
					if kind, exists := tool["type"]; exists && kind != "function" && kind != "custom" {
						nested, ok := tool["tools"].([]any)
						if kind != "namespace" || !ok {
							return invalid
						}
						for _, item := range nested {
							function, ok := item.(map[string]any)
							if !ok || function["type"] != "function" && function["type"] != "custom" {
								return invalid
							}
						}
					}
				}
			}
			if in.Operation == "messages" {
				messages, ok := in.Arguments["messages"].([]any)
				if !ok || len(messages) == 0 {
					return invalid
				}
			} else if _, ok := in.Arguments["input"]; !ok {
				return invalid
			}
			return nil
		}
		// Only a bounded, non-streaming chat shape is supported. Never silently
		// downgrade an unsupported client or hand it the credential instead.
		for k := range in.Arguments {
			if k != "model" && k != "messages" && k != "max_tokens" && k != "temperature" {
				return invalid
			}
		}
		messages, ok := in.Arguments["messages"].([]any)
		if !ok || len(messages) == 0 || len(messages) > 128 {
			return invalid
		}
		for _, m := range messages {
			msg, ok := m.(map[string]any)
			if !ok || len(msg) != 2 {
				return invalid
			}
			role, _ := msg["role"].(string)
			content, ok := msg["content"].(string)
			if !ok || len(content) > 65536 || !contains([]string{"system", "user", "assistant"}, role) {
				return invalid
			}
		}
		if _, err := modelOutputLimit(in.Arguments["max_tokens"]); err != nil {
			return invalid
		}
	case "cli":
		switch c.Adapter + ":" + in.Operation {
		case "gh:repo_view", "gh:issue_list", "stripe:balance_get", "stripe:customers_list", "aws:identity":
			if len(in.Arguments) != 0 {
				return invalid
			}
		case "gh:issue_create":
			if !onlyStrings(in.Arguments, "title", "body") {
				return invalid
			}
		case "stripe:customer_create":
			if !onlyStrings(in.Arguments, "description") {
				return invalid
			}
		case "aws:head_object":
			if !onlyStrings(in.Arguments, "key") {
				return invalid
			}
		default:
			return invalid
		}
	case "git_sign":
		if !onlyStrings(in.Arguments, "commit") {
			return invalid
		}
		s := in.Arguments["commit"].(string)
		// ssh-keygen is only a Git commit signer, never a generic SSH-agent oracle.
		if !commitPattern.MatchString(s) || strings.ContainsRune(s, 0) {
			return invalid
		}
	}
	return nil
}

var commitPattern = regexp.MustCompile(`(?s)^tree [0-9a-f]{40,64}\n(?:parent [0-9a-f]{40,64}\n)*author [^\n]+\ncommitter [^\n]+\n\n.+$`)

func onlyStrings(m map[string]any, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		s, ok := m[k].(string)
		if !ok || s == "" || len(s) > 65536 || strings.ContainsRune(s, 0) {
			return false
		}
	}
	return true
}

// cappedBuffer prevents a compromised upstream or CLI from consuming unbounded
// broker memory. Partial or oversized outputs are never sent to the task.
type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > limit {
		return 0, errors.New("output too large")
	}
	return b.Buffer.Write(p)
}
func command(ctx context.Context, argv []string, input []byte) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("CREDENTIAL_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// Retain only credential-store discovery environment, not arbitrary shell,
	// loader, proxy, alternate API endpoint or task-controlled overrides.
	for _, key := range []string{"HOME", "USER", "PATH", "XDG_CONFIG_HOME", "SSH_AUTH_SOCK", "AWS_PROFILE", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "GH_PROMPT_DISABLED=1", "GH_PAGER=cat", "AWS_PAGER=", "AWS_EC2_METADATA_DISABLED=true")
	cmd.Stdin = bytes.NewReader(input)
	var out cappedBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if cmd.Run() != nil {
		return nil, errors.New("UPSTREAM_UNAVAILABLE")
	}
	return out.Bytes(), nil
}
func (b *Broker) execute(ctx context.Context, c Connection, in Invocation) (json.RawMessage, error) {
	var secret []byte
	if len(c.CredentialCommand) > 0 {
		raw, err := command(ctx, c.CredentialCommand, nil)
		if err != nil {
			return nil, errors.New("CREDENTIAL_UNAVAILABLE")
		}
		secret = bytes.TrimSpace(raw)
		if len(secret) < 8 || len(secret) > 16384 || bytes.ContainsAny(secret, "\r\n") {
			return nil, errors.New("CREDENTIAL_UNAVAILABLE")
		}
	}
	var result []byte
	var err error
	switch c.Kind {
	case "model":
		result, _, err = b.request(ctx, c.Endpoint, "POST", in.Arguments, secret, nil)
	case "service":
		u, _ := url.Parse(c.Endpoint)
		u.Path = strings.TrimSuffix(u.Path, "/") + c.Operations[in.Operation].Path
		u.RawPath = ""
		method := c.Operations[in.Operation].Method
		var body any = in.Arguments
		if method == http.MethodGet {
			// GET inputs have an explicit query representation. Never discard a
			// validated resource selector or move a task value into the path.
			query := url.Values{}
			for name, value := range in.Arguments {
				if text, ok := value.(string); ok {
					query.Set(name, text)
				} else {
					raw, e := json.Marshal(value)
					if e != nil {
						return nil, errors.New("INVALID_ARGUMENT")
					}
					query.Set(name, string(raw))
				}
			}
			u.RawQuery = query.Encode()
			body = nil
		}
		result, _, err = b.request(ctx, u.String(), method, body, secret, nil)
	case "mcp":
		result, err = b.mcp(ctx, c, in, secret)
	case "cli":
		var argv []string
		switch c.Adapter + ":" + in.Operation {
		case "gh:repo_view":
			argv = []string{"api", "repos/" + c.Resource, "--jq", "{full_name,description,html_url,stargazers_count}"}
		case "gh:issue_list":
			argv = []string{"api", "repos/" + c.Resource + "/issues?per_page=5", "--jq", "[.[] | {number,title,state}]"}
		case "gh:issue_create":
			// JSON stdin avoids argv leaks or flag injection through issue text.
			argv = []string{"api", "--method", "POST", "repos/" + c.Resource + "/issues", "--input", "-", "--jq", "{number,title,state}"}
		case "stripe:balance_get":
			argv = []string{"balance", "retrieve"}
		case "stripe:customers_list":
			argv = []string{"customers", "list", "--limit", "3"}
		case "stripe:customer_create":
			argv = []string{"customers", "create", "--description", in.Arguments["description"].(string)}
		case "aws:identity":
			argv = []string{"sts", "get-caller-identity", "--output", "json", "--region", c.Region}
		case "aws:head_object":
			argv = []string{"s3api", "head-object", "--bucket", c.Resource, "--key", in.Arguments["key"].(string), "--output", "json", "--region", c.Region}
		}
		body, _ := json.Marshal(in.Arguments)
		result, err = command(ctx, append([]string{c.Binary}, argv...), body)
	case "git_sign":
		result, err = command(ctx, []string{c.Binary, "-Y", "sign", "-f", c.KeyFile, "-n", "git"}, []byte(in.Arguments["commit"].(string)))
		if err == nil {
			result, _ = json.Marshal(map[string]string{"repository": c.Resource, "signature": string(result)})
		}
	}
	if err != nil {
		return nil, err
	}
	// Buffer the complete response before release so chunk boundaries cannot
	// bypass known-value detection. Credential recipients themselves are trusted;
	// this is defense in depth, not a claim to decode arbitrary transformations.
	if leaks(result, secret) {
		return nil, errors.New("OUTPUT_BLOCKED")
	}
	if !json.Valid(result) {
		return nil, errors.New("UPSTREAM_UNAVAILABLE")
	}
	// Keep valid upstream JSON opaque through send(), including CLI and MCP
	// results. Decoding through any would silently coerce integer IDs to floats.
	return json.RawMessage(result), nil
}
func leaks(data, secret []byte) bool {
	if len(secret) == 0 {
		return false
	}
	forms := []string{string(secret), base64.StdEncoding.EncodeToString(secret), base64.RawStdEncoding.EncodeToString(secret), base64.URLEncoding.EncodeToString(secret), hex.EncodeToString(secret), url.QueryEscape(string(secret))}
	quoted, _ := json.Marshal(string(secret))
	forms = append(forms, string(quoted[1:len(quoted)-1]))
	// Also decode JSON escapes before scanning: a vendor may return \uXXXX.
	var decoded any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if d.Decode(&decoded) == nil {
		var walk func(any) bool
		walk = func(v any) bool {
			switch v := v.(type) {
			case string:
				for _, f := range forms {
					if strings.Contains(v, f) {
						return true
					}
				}
			case []any:
				for _, x := range v {
					if walk(x) {
						return true
					}
				}
			case map[string]any:
				for k, x := range v {
					if walk(k) || walk(x) {
						return true
					}
				}
			}
			return false
		}
		if walk(decoded) {
			return true
		}
	}
	for _, f := range forms {
		if bytes.Contains(data, []byte(f)) {
			return true
		}
	}
	return false
}
func (b *Broker) request(ctx context.Context, endpoint, method string, body any, secret []byte, headers map[string]string) ([]byte, http.Header, error) {
	// GET arguments must already be encoded by the service adapter. Reject an
	// accidental body rather than silently losing selectors in a future caller.
	if method == http.MethodGet && body != nil {
		return nil, nil, errors.New("INVALID_ARGUMENT")
	}
	var input io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, nil, errors.New("INVALID_ARGUMENT")
		}
		input = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, input)
	if err != nil {
		return nil, nil, errors.New("UPSTREAM_UNAVAILABLE")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if len(secret) > 0 {
		req.Header.Set("Authorization", "Bearer "+string(secret))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	response, err := b.client.Do(req)
	if err != nil {
		return nil, nil, errors.New("OUTCOME_UNKNOWN")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(data) > limit || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, nil, errors.New("UPSTREAM_UNAVAILABLE")
	}
	if leaks(data, secret) {
		return nil, nil, errors.New("OUTPUT_BLOCKED")
	}
	return data, response.Header, nil
}
func (b *Broker) mcp(ctx context.Context, c Connection, in Invocation, secret []byte) ([]byte, error) {
	rpc := func(id int, method string, params any) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	}
	init, headers, err := b.request(ctx, c.Endpoint, "POST", rpc(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "lamp-broker", "version": "0.1.0"}}), secret, nil)
	if err != nil {
		return nil, err
	}
	var initialized struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	init = rpcData(init)
	if json.Unmarshal(init, &initialized) != nil || initialized.Result.ProtocolVersion == "" {
		return nil, errors.New("UPSTREAM_UNAVAILABLE")
	}
	h := map[string]string{"MCP-Protocol-Version": initialized.Result.ProtocolVersion}
	if session := headers.Get("Mcp-Session-Id"); session != "" {
		h["Mcp-Session-Id"] = session
	}
	_, _, err = b.request(ctx, c.Endpoint, "POST", map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, secret, h)
	if err != nil {
		return nil, err
	}
	data, _, err := b.request(ctx, c.Endpoint, "POST", rpc(2, "tools/call", map[string]any{"name": in.Operation, "arguments": in.Arguments}), secret, h)
	if err != nil {
		return nil, err
	}
	data = rpcData(data)
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &reply) != nil || len(reply.Result) == 0 || len(reply.Error) > 0 {
		return nil, errors.New("UPSTREAM_UNAVAILABLE")
	}
	return reply.Result, nil
}

// Streamable HTTP's finite SSE response is buffered and only the JSON-RPC
// result is returned. Long-lived notifications, uploads and subscriptions are
// intentionally unsupported; no credentials are forwarded to the task.
func rpcData(data []byte) []byte {
	if json.Valid(data) {
		return data
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("data:")) {
			v := bytes.TrimSpace(line[5:])
			var m map[string]any
			if json.Unmarshal(v, &m) == nil && m["id"] != nil {
				return v
			}
		}
	}
	return nil
}
