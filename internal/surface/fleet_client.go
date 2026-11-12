package surface

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"lamp.eilmer.si/pkg/api"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// A fleet URI names one caller-owned discovery file. Entries are interchangeable
// public ingresses, not retry homes. Retrying a lost transport always preserves
// the exact method, body, bearer and key; lampd still routes to the original home.
type FleetAddress struct {
	CAFile    string         `json:"ca_file"`
	Ingresses []FleetIngress `json:"ingresses"`
}
type FleetIngress struct {
	URL        string `json:"url"`
	ServerName string `json:"server_name"`
}
type fleetTransport struct {
	ingresses  []FleetIngress
	transports []*http.Transport
}

func newFleetTransport(address string) (http.RoundTripper, error) {
	uri, err := url.Parse(address)
	if err != nil || uri.Scheme != "lamp+fleet" || uri.Host != "" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" || uri.Path == "" || uri.Path[0] != '/' {
		return nil, errors.New("fleet address must be lamp+fleet:///absolute/discovery.json")
	}
	raw, err := os.ReadFile(uri.Path)
	if err != nil {
		return nil, err
	}
	var config FleetAddress
	if err = json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	if len(config.Ingresses) == 0 || len(config.Ingresses) > 32 {
		return nil, errors.New("fleet address needs 1–32 public ingresses")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if config.CAFile != "" {
		raw, err = os.ReadFile(config.CAFile)
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM(raw) {
			return nil, errors.New("invalid discovery CA")
		}
	}
	transport := &fleetTransport{ingresses: config.Ingresses}
	for _, ingress := range config.Ingresses {
		origin, err := url.Parse(ingress.URL)
		if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
			return nil, errors.New("fleet ingress must be an HTTPS origin")
		}
		transport.transports = append(transport.transports, &http.Transport{DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, TLSHandshakeTimeout: 2 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: ingress.ServerName}})
	}
	return transport, nil
}
func (t *fleetTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		defer request.Body.Close()
	}
	var failures []error
	for index, ingress := range t.ingresses {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		origin, _ := url.Parse(ingress.URL)
		attempt := request.Clone(request.Context())
		attempt.URL.Scheme = origin.Scheme
		attempt.URL.Host = origin.Host
		attempt.Host = origin.Host
		if request.Body != nil && request.Body != http.NoBody {
			if request.GetBody == nil {
				return nil, errors.New("fleet request must have a replayable body")
			}
			var err error
			attempt.Body, err = request.GetBody()
			if err != nil {
				return nil, err
			}
		}
		response, err := t.transports[index].RoundTrip(attempt)
		if err == nil {
			return response, nil
		}
		failures = append(failures, err)
	}
	return nil, fmt.Errorf("fleet ingresses unavailable: %w", errors.Join(failures...))
}
func (t *fleetTransport) CloseIdleConnections() {
	for _, transport := range t.transports {
		transport.CloseIdleConnections()
	}
}

// CreateRetryKey chooses a fresh structured identity from authenticated discovery
// before sending a create. An explicit caller key is never changed. Capacity is
// advisory here; the selected home performs the atomic admission check.
func (c *Client) CreateRetryKey(ctx context.Context, input map[string]any, nonce string) string {
	fallback := "cli-" + nonce
	if !strings.HasPrefix(c.URL, "lamp+fleet:") {
		return fallback
	}
	raw, status, err := c.Call(ctx, ByName["fleet_get"], map[string]any{"fleet_id": "fl_local"})
	var fleet struct {
		Data api.Fleet `json:"data"`
	}
	if err != nil || status != 200 || json.Unmarshal(raw, &fleet) != nil || fleet.Data.AuthorityEpoch == nil {
		return fallback
	}
	raw, status, err = c.Call(ctx, ByName["host_list"], map[string]any{})
	var hosts struct {
		Items []api.Host `json:"items"`
	}
	if err != nil || status != 200 || json.Unmarshal(raw, &hosts) != nil {
		return fallback
	}
	pin, _ := input["host_id"].(string)
	engine, _ := input["engine_id"].(string)
	encoded, _ := json.Marshal(input["resources"])
	var request api.Resources
	if json.Unmarshal(encoded, &request) != nil {
		return fallback
	}
	number := func(v *uint64) uint64 {
		if v == nil {
			return 0
		}
		return *v
	}
	fits := func(total, used *uint64, want uint64) bool {
		return number(total) >= number(used) && want <= number(total)-number(used)
	}
	for _, host := range hosts.Items {
		if host.State != "active" || pin != "" && pin != host.Id || host.Capacity == nil || host.EngineIds == nil || len(*host.EngineIds) == 0 {
			continue
		}
		compatible := engine == ""
		for _, id := range *host.EngineIds {
			if id == engine {
				compatible = true
			}
		}
		if !compatible {
			continue
		}
		used := host.Reserved
		if used == nil {
			used = &api.Capacity{}
		}
		if !fits(host.Capacity.Vcpus, used.Vcpus, request.Vcpus) || !fits(host.Capacity.MemoryMib, used.MemoryMib, request.MemoryMib) || !fits(host.Capacity.DiskGib, used.DiskGib, request.DiskGib) || !fits(host.Capacity.PidsMax, used.PidsMax, request.PidsMax) {
			continue
		}
		return fmt.Sprintf("lk1.%d.%s.%s", *fleet.Data.AuthorityEpoch, host.Id, nonce)
	}
	return fallback
}
