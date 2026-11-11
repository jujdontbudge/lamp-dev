package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"time"
)

// The owner pins this Unix socket. No task supplies accounting destinations or
// budget IDs. Each sandbox's task and its immutable home select the budget.
func (b *Broker) accounting(ctx context.Context, operation string, value any) (map[string]any, error) {
	raw, _ := json.Marshal(value)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", b.config.AccountabilitySocket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect denied") }}
	r, err := http.NewRequestWithContext(ctx, "POST", "http://localhost/internal/accountability/"+operation, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	response, err := client.Do(r)
	if err != nil {
		return nil, errors.New("ACCOUNTING_UNAVAILABLE")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if response.StatusCode == 429 {
			return nil, errors.New("BUDGET_EXHAUSTED")
		}
		return nil, errors.New("ACCOUNTING_UNAVAILABLE")
	}
	var reply map[string]any
	if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&reply) != nil {
		return nil, errors.New("ACCOUNTING_UNAVAILABLE")
	}
	return reply, nil
}
func reserveTokens(ctx context.Context, c Connection, in Invocation) (uint64, error) {
	if c.Kind != "model" {
		return 0, nil
	}
	output, err := modelOutputLimit(in.Arguments["max_tokens"])
	if err != nil {
		return 0, err
	}
	if len(c.TokenizerCommand) > 0 {
		raw, _ := json.Marshal(in.Arguments)
		out, err := command(ctx, c.TokenizerCommand, raw)
		if err != nil {
			return 0, errors.New("ACCOUNTING_UNAVAILABLE")
		}
		var counted struct {
			Prompt uint64 `json:"prompt_tokens"`
		}
		if json.Unmarshal(out, &counted) != nil || counted.Prompt > 1<<40 {
			return 0, errors.New("ACCOUNTING_UNAVAILABLE")
		}
		return counted.Prompt + output, nil
	}
	// Chat tokenization is vendor-specific. A configured vendor tokenizer must
	// provide an exact prompt count; otherwise reserve a conservative UTF-8 byte
	// bound plus the maximum output and report best-effort token visibility.
	messages, _ := json.Marshal(in.Arguments["messages"])
	return uint64(len(messages)) + output + 1024, nil
}

// Validation and reservation must interpret JSON numbers identically, including
// integral decimal and exponent spellings such as 1000.0 and 1e3.
func modelOutputLimit(v any) (uint64, error) {
	encoded, ok := v.(json.Number)
	n, err := encoded.Float64()
	if !ok || err != nil || !(n >= 1 && n <= 4096) || n != math.Trunc(n) {
		return 0, errors.New("SCOPE_DENIED")
	}
	return uint64(n), nil
}
func modelUsage(result []byte) *uint64 {
	var reply struct {
		Usage struct {
			Total *uint64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(result, &reply) != nil {
		return nil
	}
	return reply.Usage.Total
}
