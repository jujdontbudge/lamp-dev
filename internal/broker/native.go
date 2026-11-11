package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
)

func normalizeNative(in Invocation) error {
	key := "max_tokens"
	if in.Operation == "responses" {
		key = "max_output_tokens"
	}
	if in.Operation == "chat_completions" {
		if _, exists := in.Arguments["max_completion_tokens"]; exists {
			key = "max_completion_tokens"
		}
		// Streamed chat reports usage only when asked; budgets need it.
		if stream, _ := in.Arguments["stream"].(bool); stream {
			in.Arguments["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	raw, exists := in.Arguments[key]
	if !exists {
		in.Arguments[key] = json.Number("4096")
		return nil
	}
	number, ok := raw.(json.Number)
	n, err := number.Float64()
	if !ok || err != nil || n < 1 || math.IsInf(n, 0) || math.IsNaN(n) || n != math.Trunc(n) {
		return errors.New("INVALID_ARGUMENT")
	}
	if n > 4096 {
		in.Arguments[key] = json.Number("4096")
	}
	return nil
}

func (b *Broker) nativeModel(ctx context.Context, c Connection, in Invocation) ([]byte, string, error) {
	credential, err := command(ctx, c.CredentialCommand, nil)
	secret := bytes.TrimSpace(credential)
	if err != nil || len(secret) < 8 || len(secret) > 16384 || bytes.ContainsAny(secret, "\r\n") {
		return nil, "", errors.New("CREDENTIAL_UNAVAILABLE")
	}
	body := in.Arguments
	if c.FreeOnly {
		if !strings.HasPrefix(c.Endpoint, "https://openrouter.ai/") {
			return nil, "", errors.New("SCOPE_DENIED")
		}
		body["provider"] = map[string]any{"max_price": map[string]any{"prompt": 0, "completion": 0}}
	}
	headers := map[string]string{}
	if in.Operation == "messages" {
		headers["anthropic-version"] = "2023-06-01"
	}
	result, responseHeaders, err := b.request(ctx, c.Endpoint, "POST", body, secret, headers)
	if err != nil {
		return nil, "", err
	}
	content := responseHeaders.Get("Content-Type")
	if !strings.HasPrefix(content, "application/json") && !strings.HasPrefix(content, "text/event-stream") {
		return nil, "", errors.New("UPSTREAM_UNAVAILABLE")
	}
	return result, content, nil
}

func nativeModelUsage(data []byte) *uint64 {
	type usage struct {
		Total    *uint64 `json:"total_tokens"`
		Prompt   *uint64 `json:"prompt_tokens"`
		Complete *uint64 `json:"completion_tokens"`
		Input    *uint64 `json:"input_tokens"`
		Output   *uint64 `json:"output_tokens"`
		Read     uint64  `json:"cache_read_input_tokens"`
		Creation uint64  `json:"cache_creation_input_tokens"`
	}
	var input, output, total *uint64
	observe := func(u *usage) {
		if u == nil {
			return
		}
		if u.Total != nil {
			total = u.Total
		}
		if u.Input != nil && *u.Input <= 1<<40 && u.Read <= 1<<40 && u.Creation <= 1<<40 {
			n := *u.Input + u.Read + u.Creation
			input = &n
		}
		if u.Output != nil {
			output = u.Output
		}
		if u.Prompt != nil && u.Complete != nil && u.Total == nil {
			n := *u.Prompt + *u.Complete
			total = &n
		}
	}
	frames := [][]byte{data}
	if !json.Valid(data) {
		frames = bytes.Split(data, []byte("\n"))
	}
	for _, frame := range frames {
		frame = bytes.TrimSpace(bytes.TrimPrefix(frame, []byte("data:")))
		var v struct {
			Type    string `json:"type"`
			Usage   *usage `json:"usage"`
			Message struct {
				Usage *usage `json:"usage"`
			} `json:"message"`
			Response struct {
				Usage *usage `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal(frame, &v) == nil {
			if v.Type == "error" || v.Type == "response.failed" || v.Type == "response.incomplete" {
				return nil
			}
			observe(v.Usage)
			observe(v.Message.Usage)
			observe(v.Response.Usage)
		}
	}
	if total != nil && *total <= 1<<42 {
		return total
	}
	if input == nil || output == nil || *input > 1<<42 || *output > 1<<42 {
		return nil
	}
	result := *input + *output
	return &result
}
