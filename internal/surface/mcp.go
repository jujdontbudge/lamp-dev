package surface

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/JohanSardar/lamp/pkg/api"
)

// ServeMCP uses MCP's newline-delimited JSON-RPC stdio transport. Concurrent
// calls are bounded so an event wait cannot block another caller's tool.
func ServeMCP(ctx context.Context, input io.Reader, output io.Writer, client *Client) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 96<<10)
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 32)
	send := func(v any) { mu.Lock(); defer mu.Unlock(); json.NewEncoder(output).Encode(v) }
	defer wg.Wait()
	for scanner.Scan() {
		raw := append([]byte{}, scanner.Bytes()...)
		m, err := Decode(raw)
		if err != nil {
			send(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "invalid JSON"}})
			continue
		}
		id, hasID := m["id"]
		method, _ := m["method"].(string)
		if !hasID {
			continue
		}
		if m["jsonrpc"] != "2.0" {
			send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32600, "message": "invalid JSON-RPC"}})
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			response := map[string]any{"jsonrpc": "2.0", "id": id}
			rpcError := func(code int, message string) { response["error"] = map[string]any{"code": code, "message": message} }
			switch method {
			case "initialize":
				response["result"] = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "lamp", "version": "0.1.0-dev"}, "instructions": "Tools use the same caller credential as HTTP and CLI. Supply stable idempotency_key for mutations. Events use bounded event_list waits."}
			case "ping":
				response["result"] = map[string]any{}
			case "tools/list":
				list := []any{}
				for _, op := range api.Operations {
					if !CoreNames[op.Name] && !(strings.HasPrefix(client.URL, "lamp+fleet:") && (op.Name == "host_get" || op.Name == "host_list")) {
						continue
					}
					list = append(list, map[string]any{"name": op.Name, "description": op.CLI + "; required scope " + op.Scope, "inputSchema": InputSchema(op.InputSchema)})
				}
				response["result"] = map[string]any{"tools": list}
			case "tools/call":
				params, ok := m["params"].(map[string]any)
				if !ok {
					rpcError(-32602, "params required")
					break
				}
				name, _ := params["name"].(string)
				op, ok := ByName[name]
				if !ok {
					rpcError(-32602, "unknown tool")
					break
				}
				args, ok := params["arguments"].(map[string]any)
				if !ok {
					rpcError(-32602, "arguments object required")
					break
				}
				if _, _, _, err := Request(op, args); err != nil {
					response["result"] = toolError("INVALID_ARGUMENT", err.Error())
					break
				}
				data, status, err := client.Call(ctx, op, args)
				if err != nil {
					response["result"] = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf(`{"error":{"code":"DATA_UNAVAILABLE","message":%q}}`, err.Error())}}}
					break
				}
				if !json.Valid(data) {
					rpcError(-32603, "invalid upstream JSON")
					break
				}
				structured, _ := Decode(data)
				response["result"] = map[string]any{"isError": status >= 400, "structuredContent": structured, "content": []any{map[string]any{"type": "text", "text": string(data)}}}
			default:
				rpcError(-32601, "method not found")
			}
			send(response)
		}()
	}
	return scanner.Err()
}

func toolError(code, message string) map[string]any {
	raw, _ := json.Marshal(api.ErrorEnvelope{RequestId: "mcp", Error: api.ErrorDetail{Code: api.ErrorCode(code), Message: message}})
	return map[string]any{"isError": true, "structuredContent": json.RawMessage(raw), "content": []any{map[string]any{"type": "text", "text": string(raw)}}}
}
