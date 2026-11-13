package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
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

// nativeStallLimit ends a native stream only when the upstream sends nothing
// for this long; a long reply that keeps arriving is never cut off.
const nativeStallLimit = 90 * time.Second

// nativeStream forwards a native model reply to the harness as it arrives.
// The provider credential stays here. Known credential forms are scanned on
// the stream with a held-back tail, so a value split across chunks is still
// caught before any of it is written. Usage frames are kept for settlement.
// streamed reports whether response bytes were already sent to the harness.
func (b *Broker) nativeStream(ctx context.Context, c Connection, in Invocation, w http.ResponseWriter, stop context.CancelFunc) ([]byte, bool, error) {
	credential, err := command(ctx, c.CredentialCommand, nil)
	secret := bytes.TrimSpace(credential)
	account := ""
	if c.CodexSubscription {
		var auth struct {
			Tokens struct {
				Access  string `json:"access_token"`
				Account string `json:"account_id"`
			} `json:"tokens"`
		}
		if json.Unmarshal(credential, &auth) != nil {
			return nil, false, errors.New("CREDENTIAL_UNAVAILABLE")
		}
		secret = []byte(auth.Tokens.Access)
		account = auth.Tokens.Account
		if account == "" {
			return nil, false, errors.New("CREDENTIAL_UNAVAILABLE")
		}
	}
	if err != nil || len(secret) < 8 || len(secret) > 16384 || bytes.ContainsAny(secret, "\r\n") {
		return nil, false, errors.New("CREDENTIAL_UNAVAILABLE")
	}
	body := in.Arguments
	if c.FreeOnly {
		if !strings.HasPrefix(c.Endpoint, "https://openrouter.ai/") {
			return nil, false, errors.New("SCOPE_DENIED")
		}
		body["provider"] = map[string]any{"max_price": map[string]any{"prompt": 0, "completion": 0}}
	}
	if c.CodexSubscription {
		delete(body, "max_output_tokens")
		body["store"] = false
		body["stream"] = true
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, false, errors.New("INVALID_ARGUMENT")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, false, errors.New("UPSTREAM_UNAVAILABLE")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+string(secret))
	if c.CodexSubscription {
		req.Header.Set("ChatGPT-Account-Id", account)
		req.Header.Set("OpenAI-Beta", "responses=experimental")
		req.Header.Set("originator", "codex_cli_rs")
	}
	if in.Operation == "messages" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	// The watchdog also bounds the wait for response headers.
	stall := time.AfterFunc(nativeStallLimit, stop)
	defer stall.Stop()
	response, err := b.stream.Do(req)
	if err != nil {
		return nil, false, errors.New("OUTCOME_UNKNOWN")
	}
	defer response.Body.Close()
	content := response.Header.Get("Content-Type")
	// The pinned ChatGPT subscription endpoint can omit Content-Type for SSE.
	if c.CodexSubscription && content == "" {
		content = "text/event-stream"
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !strings.HasPrefix(content, "application/json") && !strings.HasPrefix(content, "text/event-stream") {
		log.Printf("lamp-broker: native upstream response rejected (status=%d)", response.StatusCode)
		return nil, false, errors.New("UPSTREAM_UNAVAILABLE")
	}
	forms := secretForms(secret)
	if c.CodexSubscription {
		// Also prevent echoes of refresh/id tokens or the entire custody document.
		var auth struct {
			Tokens map[string]any `json:"tokens"`
		}
		json.Unmarshal(credential, &auth)
		for _, v := range auth.Tokens {
			if value, ok := v.(string); ok && len(value) >= 8 {
				forms = append(forms, secretForms([]byte(value))...)
			}
		}
	}
	hold := 0
	for _, f := range forms {
		if len(f) > hold {
			hold = len(f)
		}
	}
	hold--
	flusher, _ := w.(http.Flusher)
	started := false
	var pending, usage, line []byte
	keep := func(chunk []byte) {
		// Only lines naming usage are retained, bounded, for settlement.
		for _, c := range chunk {
			line = append(line, c)
			if c == '\n' || len(line) > 4<<20 {
				if bytes.Contains(line, []byte(`"usage"`)) && len(usage)+len(line) <= 8<<20 {
					usage = append(usage, line...)
				}
				line = line[:0]
			}
		}
	}
	send := func(data []byte) error {
		if !started {
			w.Header().Set("Content-Type", content)
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(200)
			started = true
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			stall.Reset(nativeStallLimit)
			pending = append(pending, buffer[:n]...)
			for _, f := range forms {
				if bytes.Contains(pending, []byte(f)) {
					return usage, started, errors.New("OUTPUT_BLOCKED")
				}
			}
			if cut := len(pending) - hold; cut > 0 {
				keep(pending[:cut])
				if send(pending[:cut]) != nil {
					return usage, started, errors.New("OUTCOME_UNKNOWN")
				}
				pending = append(pending[:0], pending[cut:]...)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			// Stalled (watchdog), budget stop, deadline, or upstream reset.
			return usage, started, errors.New("OUTCOME_UNKNOWN")
		}
	}
	keep(pending)
	keep([]byte("\n"))
	if len(pending) > 0 || !started {
		if send(pending) != nil {
			return usage, true, errors.New("OUTCOME_UNKNOWN")
		}
	}
	return usage, true, nil
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
