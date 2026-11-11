// network-demo-peer is a real, independent private-network caller. It imports
// Tailscale's SDK, not Lamp's runtime, policy, engine or control implementation.
// Enrollment keys enter only on a host FD. HTTPS grants enter only on stdin;
// neither keys, URLs with fragments nor cookies are printed.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/net/dns/dnsmessage"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"tailscale.com/envknob"
	"tailscale.com/tsnet"
)

type config struct {
	Dir        string `json:"dir"`
	Hostname   string `json:"hostname"`
	ControlURL string `json:"control_url"`
	AuthFD     int    `json:"auth_fd"`
	Port       uint16 `json:"port"`
}
type action struct {
	Operation string `json:"operation"`
	Target    string `json:"target"`
	Protocol  string `json:"protocol"`
	Marker    string `json:"marker"`
	URL       string `json:"url"`
	CAPEM     string `json:"ca_pem"`
	Token     string `json:"token"`
	StreamID  string `json:"stream_id"`
}

var output sync.Mutex

func emit(value any) {
	output.Lock()
	defer output.Unlock()
	_ = json.NewEncoder(os.Stdout).Encode(value)
}
func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "network demo peer unavailable")
		os.Exit(70)
	}
}
func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("private config required")
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	defer f.Close()
	var c config
	decoder := json.NewDecoder(io.LimitReader(f, 16384))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil || c.AuthFD < 3 || c.Port == 0 || c.Port == 65535 {
		return fmt.Errorf("invalid peer config")
	}
	secret := os.NewFile(uintptr(c.AuthFD), "host-peer-enrollment")
	if secret == nil {
		return fmt.Errorf("key descriptor required")
	}
	key, err := io.ReadAll(io.LimitReader(secret, 4097))
	secret.Close()
	if err != nil || len(key) > 4096 {
		return fmt.Errorf("key input")
	}
	envknob.SetNoLogsNoSupport()
	discard := func(string, ...any) {}
	server := &tsnet.Server{Dir: c.Dir, Hostname: c.Hostname, ControlURL: c.ControlURL, AuthKey: strings.TrimSpace(string(key)), Ephemeral: true, Logf: discard, UserLogf: discard}
	clear(key)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	approval, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	status, err := server.Up(approval)
	if err != nil {
		return err
	}
	defer server.Close()
	client, err := server.LocalClient()
	if err != nil {
		return err
	}
	observed := func(protocol string, remote net.Addr, data []byte) {
		who, err := client.WhoIsProto(ctx, protocol, remote.String())
		if err != nil || who.Node == nil {
			return
		}
		ap, err := netip.ParseAddrPort(remote.String())
		if err != nil {
			return
		}
		sum := sha256.Sum256(data)
		emit(map[string]any{"event": "observed", "protocol": protocol, "source_ip": ap.Addr().String(), "node_id": strconv.FormatInt(int64(who.Node.ID), 10), "hostname": strings.TrimSuffix(who.Node.Name, "."), "sha256": hex.EncodeToString(sum[:])})
	}
	var closers []io.Closer
	defer func() {
		for _, closer := range closers {
			closer.Close()
		}
	}()
	for _, port := range []uint16{c.Port, c.Port + 1} {
		listener, err := server.Listen("tcp", ":"+strconv.Itoa(int(port)))
		if err != nil {
			return err
		}
		closers = append(closers, listener)
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				go func() {
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
					buffer := make([]byte, 65535)
					for {
						n, err := conn.Read(buffer)
						if n > 0 {
							observed("tcp", conn.RemoteAddr(), buffer[:n])
							if _, e := conn.Write(buffer[:n]); e != nil {
								return
							}
						}
						if err != nil {
							return
						}
					}
				}()
			}
		}()
		for _, ip := range status.TailscaleIPs {
			packet, err := server.ListenPacket("udp", netip.AddrPortFrom(ip, port).String())
			if err != nil {
				return err
			}
			closers = append(closers, packet)
			go func() {
				buffer := make([]byte, 65535)
				for {
					n, remote, err := packet.ReadFrom(buffer)
					if err != nil {
						return
					}
					observed("udp", remote, buffer[:n])
					_, _ = packet.WriteTo(buffer[:n], remote)
				}
			}()
		}
	}
	jar, _ := cookiejar.New(nil)
	emit(map[string]any{"event": "ready", "addresses": status.TailscaleIPs, "port": c.Port})
	actions := make(chan action)
	go func() {
		defer close(actions)
		decoder := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
		decoder.DisallowUnknownFields()
		for {
			var a action
			if decoder.Decode(&a) != nil {
				return
			}
			select {
			case actions <- a:
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case a, ok := <-actions:
			if !ok {
				<-ctx.Done()
				return nil
			}
			if a.Operation == "https" || a.Operation == "exchange" || a.Operation == "stream" {
				roots := x509.NewCertPool()
				if !roots.AppendCertsFromPEM([]byte(a.CAPEM)) {
					return fmt.Errorf("private CA required")
				}
				transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					host, port, err := net.SplitHostPort(address)
					if err != nil {
						return nil, err
					}
					if _, err := netip.ParseAddr(host); err != nil {
						wire, _, err := client.QueryDNS(ctx, host, "A")
						if err != nil {
							return nil, err
						}
						var answer dnsmessage.Message
						if answer.Unpack(wire) != nil || answer.Header.RCode != dnsmessage.RCodeSuccess {
							return nil, fmt.Errorf("private DNS refused")
						}
						host = ""
						for _, record := range answer.Answers {
							if a, ok := record.Body.(*dnsmessage.AResource); ok {
								host = netip.AddrFrom4(a.A).String()
								break
							}
						}
						if host == "" {
							return nil, fmt.Errorf("private DNS empty")
						}
					}
					return server.Dial(ctx, network, net.JoinHostPort(host, port))
				}, ForceAttemptHTTP2: true, DisableKeepAlives: true}
				httpClient := &http.Client{Transport: transport, Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				method := http.MethodGet
				target := a.URL
				var body io.Reader
				if a.Operation == "exchange" {
					method = http.MethodPost
					data, _ := json.Marshal(map[string]string{"token": a.Token})
					body = bytes.NewReader(data)
					target = strings.TrimSuffix(target, "/") + "/_lamp/exchange"
				}
				request, err := http.NewRequestWithContext(ctx, method, target, body)
				if err != nil {
					return fmt.Errorf("invalid HTTPS action")
				}
				if body != nil {
					request.Header.Set("Content-Type", "application/json")
				}
				request.Header.Set("Authorization", "Bearer lamp-demo-not-a-credential")
				request.Header.Set("X-Lamp-Principal", "untrusted-header")
				response, err := httpClient.Do(request)
				if err != nil {
					cause := fmt.Sprintf("%T", err)
					var u *url.Error
					if errors.As(err, &u) {
						cause = fmt.Sprintf("%T", u.Err)
						var op *net.OpError
						if errors.As(u.Err, &op) {
							cause += "/" + fmt.Sprintf("%T", op.Err)
						}
					}
					emit(map[string]any{"event": "https", "state": "unavailable", "cause": cause})
					transport.CloseIdleConnections()
					continue
				}
				if a.Operation == "stream" && response.StatusCode == 200 {
					buffer := make([]byte, 1)
					_, err := io.ReadFull(response.Body, buffer)
					if err != nil {
						response.Body.Close()
						return fmt.Errorf("stream did not open")
					}
					emit(map[string]any{"event": "stream_open", "stream_id": a.StreamID})
					go func() {
						_, _ = io.Copy(io.Discard, response.Body)
						response.Body.Close()
						transport.CloseIdleConnections()
						emit(map[string]any{"event": "stream_closed", "stream_id": a.StreamID})
					}()
					continue
				}
				bytes, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
				response.Body.Close()
				transport.CloseIdleConnections()
				result := map[string]any{"event": "https", "status": response.StatusCode, "body_bytes": len(bytes), "complete": readErr == nil}
				var app struct {
					App           string `json:"app"`
					Cookie        bool   `json:"cookie"`
					Authorization bool   `json:"authorization"`
					Identity      bool   `json:"identity"`
				}
				if json.Unmarshal(bytes, &app) == nil && app.App == "lamp-dev" {
					result["app"] = app.App
					result["cookie_visible_to_app"] = app.Cookie
					result["authorization_visible_to_app"] = app.Authorization
					result["identity_visible_to_app"] = app.Identity
				}
				// No response body, Set-Cookie or bearer URL is emitted.
				emit(result)
				continue
			}
			ap, err := netip.ParseAddrPort(a.Target)
			if err != nil || (a.Protocol != "tcp" && a.Protocol != "udp") {
				return fmt.Errorf("literal service required")
			}
			dial, cancel := context.WithTimeout(ctx, 2*time.Second)
			conn, err := server.Dial(dial, a.Protocol, ap.String())
			cancel()
			state := "blocked"
			sum := ""
			if err == nil {
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				data := []byte(a.Marker)
				if _, e := conn.Write(data); e == nil {
					received := make([]byte, len(data))
					var n int
					if a.Protocol == "tcp" {
						n, err = io.ReadFull(conn, received)
					} else {
						n, err = conn.Read(received)
					}
					if err == nil && n == len(data) && string(received) == a.Marker {
						state = "allowed"
						hash := sha256.Sum256(received)
						sum = hex.EncodeToString(hash[:])
					}
				}
				conn.Close()
			}
			emit(map[string]any{"event": "action", "state": state, "protocol": a.Protocol, "response_sha256": sum})
		}
	}
}
