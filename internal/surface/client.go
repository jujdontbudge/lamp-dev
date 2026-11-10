package surface

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/JohanSardar/lamp/pkg/api"
)

type Client struct {
	mu                 sync.Mutex
	Socket, URL, Token string
	HTTP               *http.Client
}

func (c *Client) httpClient() (*http.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.HTTP != nil {
		return c.HTTP, nil
	}
	transport := &http.Transport{}
	if c.URL == "" {
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
		}
	} else {
		u, err := url.Parse(c.URL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
			return nil, fmt.Errorf("API URL must be an origin without credentials")
		}
		host := u.Hostname()
		ip := net.ParseIP(host)
		loopback := host == "localhost" || ip != nil && ip.IsLoopback()
		if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
			return nil, fmt.Errorf("remote API requires HTTPS")
		}
	}
	c.HTTP = &http.Client{Transport: transport, Timeout: 65 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("API redirects are not permitted") }}
	return c.HTTP, nil
}
func (c *Client) Close() {
	c.mu.Lock()
	client := c.HTTP
	c.mu.Unlock()
	if client != nil {
		client.CloseIdleConnections()
	}
}
func (c *Client) Open(ctx context.Context, op api.OperationDescriptor, input map[string]any, stream bool) (*http.Response, error) {
	path, headers, data, err := Request(op, input)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(c.URL, "/")
	if base == "" {
		base = "http://lamp"
	}
	r, err := http.NewRequestWithContext(ctx, strings.ToUpper(op.Method), base+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	if stream {
		r.Header.Set("Accept", "text/event-stream")
	}
	if c.Token != "" {
		r.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	client, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	if stream {
		streamClient := *client
		streamClient.Timeout = 0 // Follow lives until its caller cancels.
		client = &streamClient
	}
	return client.Do(r)
}
func (c *Client) Call(ctx context.Context, op api.OperationDescriptor, input map[string]any) ([]byte, int, error) {
	response, err := c.Open(ctx, op, input, false)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil {
		return nil, 0, err
	}
	if len(data) > 4<<20 {
		return nil, 0, fmt.Errorf("API response exceeds 4 MiB")
	}
	return data, response.StatusCode, nil
}

// InputSchema expands local references for MCP clients without remote resolution.
func InputSchema(name string) any { return expand(definitions[name], map[string]bool{}) }
func expand(v any, seen map[string]bool) any {
	switch v := v.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/$defs/")
			if seen[name] {
				return map[string]any{}
			}
			next := map[string]bool{}
			for k, b := range seen {
				next[k] = b
			}
			next[name] = true
			return expand(definitions[name], next)
		}
		out := map[string]any{}
		for k, x := range v {
			out[k] = expand(x, seen)
		}
		return out
	case []any:
		out := []any{}
		for _, x := range v {
			out = append(out, expand(x, seen))
		}
		return out
	default:
		return v
	}
}
