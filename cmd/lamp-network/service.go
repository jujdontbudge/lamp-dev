package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/JohanSardar/lamp/internal/network"
	"github.com/JohanSardar/lamp/internal/surface"
)

const maxControlActionBytes = 1 << 20

type serviceConfig struct {
	GuardUID         int      `json:"guard_uid"`
	ControlFD        int      `json:"control_fd"`
	StateDir         string   `json:"state_dir"`
	Domain           string   `json:"domain"`
	IngressSandboxID string   `json:"ingress_sandbox_id"`
	IngressPort      uint16   `json:"ingress_port"`
	PrivateViewers   []string `json:"private_viewers"`
	// WakeAPISocket and WakeTokenFile, when set, let an allowed dev request
	// resume a suspended sandbox through Lamp's public API.
	WakeAPISocket string   `json:"wake_api_socket"`
	WakeTokenFile string   `json:"wake_token_file"`
	Sandboxes     []config `json:"sandboxes"`
}
type runningSandbox struct {
	director *network.Director
	member   *network.Member
	bridge   *network.FrameBridge
}
type controlAction struct {
	Operation        string          `json:"operation"`
	SandboxID        string          `json:"sandbox_id"`
	TargetID         string          `json:"target_id"`
	Port             uint16          `json:"port"`
	Protocol         string          `json:"protocol"`
	Alias            string          `json:"alias"`
	PortID           string          `json:"port_id"`
	GrantID          string          `json:"grant_id"`
	ConnectionID     string          `json:"connection_id"`
	IdempotencyKey   string          `json:"idempotency_key"`
	Recipient        string          `json:"recipient"`
	ExpiresAt        time.Time       `json:"expires_at"`
	ExpectedRevision uint64          `json:"expected_revision"`
	Revision         uint64          `json:"revision"`
	Layers           []network.Layer `json:"policy_layers"`
}

// runService is the host supervisor's descriptor-based integration boundary.
// Control replies, including transient share URLs, go ONLY to the inherited
// private Unix stream, never stdout, an audit record, guest channel or listener.
func runService(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return network.ErrDenied
	}
	var plan serviceConfig
	if network.DecodeConfig(file, &plan) != nil || plan.GuardUID < 65536 || plan.ControlFD < 3 || len(plan.Sandboxes) == 0 || len(plan.Sandboxes) > 64 {
		return network.ErrDenied
	}
	roles := map[int]bool{plan.ControlFD: true}
	for _, c := range plan.Sandboxes {
		for _, fd := range []int{func() int {
			if c.Bridge != nil {
				return c.Bridge.FrameFD
			}
			return 0
		}(), func() int {
			if c.Member != nil {
				return c.Member.AuthFD
			}
			return 0
		}()} {
			if fd != 0 {
				if fd < 3 || roles[fd] {
					return network.ErrDenied
				}
				roles[fd] = true
			}
		}
	}
	controllerFile := os.NewFile(uintptr(plan.ControlFD), "supervisor-network-control")
	if controllerFile == nil {
		return network.ErrDenied
	}
	controller, err := net.FileConn(controllerFile)
	controllerFile.Close()
	if err != nil {
		return network.ErrDenied
	}
	defer controller.Close()
	if _, ok := controller.(*net.UnixConn); !ok {
		return network.ErrDenied
	}
	addresses, prefixes, err := network.Inventory()
	if err != nil {
		return err
	}
	frames := make([]*os.File, len(plan.Sandboxes))
	audits := make([]*network.FileAudit, len(plan.Sandboxes))
	keys := make([]string, len(plan.Sandboxes))
	defer func() {
		for _, fd := range frames {
			if fd != nil {
				fd.Close()
			}
		}
		for _, audit := range audits {
			if audit != nil {
				audit.Close()
			}
		}
	}()
	seen := map[string]bool{}
	for i, c := range plan.Sandboxes {
		if seen[c.Subject.SandboxID] || c.Bridge == nil || c.Subject != c.Bridge.Transport.Subject {
			return network.ErrDenied
		}
		seen[c.Subject.SandboxID] = true
		c.Policy.HostAddresses = append(c.Policy.HostAddresses, addresses...)
		c.Policy.LANPrefixes = append(c.Policy.LANPrefixes, prefixes...)
		if c.Policy.Validate() != nil {
			return network.ErrDenied
		}
		plan.Sandboxes[i] = c
		audits[i], err = network.NewFileAudit(c.Audit, c.AuditBytes)
		if err != nil {
			return err
		}
		if c.Bridge.FrameFD != 0 {
			if c.Bridge.Namespace != "" || c.Bridge.Interface != "" {
				return network.ErrDenied
			}
			frames[i], err = network.AdoptEthernetFD(c.Bridge.FrameFD)
		} else {
			frames[i], err = network.OpenPrivateTAP(c.Bridge.Namespace, c.Bridge.Interface)
		}
		if err != nil {
			return err
		}
		if c.Member != nil && c.Member.AuthFD != 0 {
			if c.Member.AuthFD < 3 {
				return network.ErrDenied
			}
			secret := os.NewFile(uintptr(c.Member.AuthFD), "host-member-key")
			if secret == nil {
				return network.ErrDenied
			}
			data, readErr := io.ReadAll(io.LimitReader(secret, 4097))
			secret.Close()
			if readErr != nil || len(data) > 4096 {
				return network.ErrDenied
			}
			keys[i] = strings.TrimSpace(string(data))
			clear(data)
		}
	}
	if err := network.DropGuardPrivileges(plan.GuardUID); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	running := map[string]*runningSandbox{}
	defer func() {
		for _, sb := range running {
			sb.bridge.Close()
			if sb.member != nil {
				sb.member.Close()
			}
		}
	}()
	var observations []map[string]any
	for i, c := range plan.Sandboxes {
		var provider network.Provider
		var member *network.Member
		if c.Member != nil {
			member, err = network.NewMemberWithControl(c.Subject, c.Member.StateDir, c.Member.Hostname, keys[i], c.Member.ControlURL)
			keys[i] = ""
			if err != nil {
				return err
			}
			approval, cancel := context.WithTimeout(ctx, time.Minute)
			err = member.Start(approval, nil)
			cancel()
			if err != nil {
				member.Close()
				return err
			}
		}
		switch c.Provider.Type {
		case "direct":
			provider = network.NewDirectProvider(nil)
		case "ssh_connect":
			provider, err = network.NewConnectProvider(c.Provider.Endpoint)
		case "private_member":
			if member == nil {
				return network.ErrDenied
			}
			provider = member
		default:
			if member != nil {
				member.Close()
			}
			return network.ErrDenied
		}
		if err == nil && member != nil && c.Provider.Type != "private_member" {
			provider, err = network.AttachMember(provider, member, []string{plan.Domain})
		}
		if err != nil {
			if provider != nil {
				provider.Close()
			}
			if member != nil {
				member.Close()
			}
			return err
		}
		director, err := network.NewDirector(c.Subject, c.Revision, c.Policy, provider, audits[i])
		if err != nil {
			provider.Close()
			return err
		}
		bridge, err := network.NewFrameBridge(ctx, c.Bridge.Transport, frames[i], director)
		if err != nil {
			director.Close()
			return err
		}
		running[c.Subject.SandboxID] = &runningSandbox{director, member, bridge}
		if len(c.PortMaps) > 0 {
			if member == nil {
				return network.ErrDenied
			}
			if _, err := member.MapPorts(ctx, c.PortMaps, bridge, c.Revision, audits[i]); err != nil {
				return err
			}
		}
		observation := map[string]any{"sandbox_id": c.Subject.SandboxID, "generation": c.Subject.Generation}
		if member != nil {
			observation["member_addresses"] = member.Addresses()
		}
		observations = append(observations, observation)
	}
	ingress := running[plan.IngressSandboxID]
	if ingress == nil || ingress.member == nil {
		return network.ErrDenied
	}
	authority, err := network.NewAuthority(plan.StateDir)
	if err != nil {
		return err
	}
	manager, err := network.NewPortManager(plan.StateDir, plan.Domain, plan.IngressPort, authority, func(ctx context.Context, r *http.Request) (network.PeerIdentity, error) {
		return ingress.member.WhoIs(ctx, "tcp", r.RemoteAddr)
	}, audits[0], plan.PrivateViewers)
	if err != nil {
		return err
	}
	defer manager.Close()
	if plan.WakeAPISocket != "" && plan.WakeTokenFile != "" {
		manager.SetWake(func(ctx context.Context, sandboxID string) error {
			raw, err := os.ReadFile(plan.WakeTokenFile)
			if err != nil {
				return err
			}
			client := &surface.Client{Socket: plan.WakeAPISocket, Token: strings.TrimSpace(string(raw))}
			defer client.Close()
			return surface.WakeSandbox(ctx, client, sandboxID)
		})
	}
	listener, err := ingress.member.ListenTCP(plan.IngressPort)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: manager, TLSConfig: manager.TLSConfig(), ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	defer server.Close()
	go func() { _ = server.ServeTLS(listener, "", "") }()
	if json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "ready", "sandboxes": observations, "ingress_ca_pem": string(authority.PublicPEM)}) != nil {
		return network.ErrUnavailable
	}
	links := map[string]*network.SandboxLink{}
	defer func() {
		for _, link := range links {
			link.Close()
		}
	}()
	actions := make(chan controlAction)
	go func() {
		defer close(actions)
		limited := &io.LimitedReader{R: controller}
		decoder := json.NewDecoder(limited)
		decoder.DisallowUnknownFields()
		for {
			// Renew the byte budget per JSON value, accounting for decoder
			// read-ahead. Keep the same decoder so concatenated values and
			// buffered bytes survive; there is no cumulative stream limit.
			buffered, err := io.Copy(io.Discard, decoder.Buffered())
			if err != nil || buffered > maxControlActionBytes {
				return
			}
			limited.N = maxControlActionBytes + 1 - buffered
			start := decoder.InputOffset()
			var action controlAction
			if err := decoder.Decode(&action); err != nil || decoder.InputOffset()-start > maxControlActionBytes {
				return
			}
			select {
			case actions <- action:
			case <-ctx.Done():
				return
			}
		}
	}()
	encoder := json.NewEncoder(controller)
	for {
		select {
		case <-ctx.Done():
			return nil
		case action, ok := <-actions:
			if !ok {
				return network.ErrUnavailable
			}
			var result any
			var actionErr error
			sb := running[action.SandboxID]
			switch action.Operation {
			case "publish_port":
				if sb == nil {
					actionErr = network.ErrDenied
				} else {
					result, actionErr = manager.Publish(sb.bridge, action.Port)
				}
			case "share_port":
				result, actionErr = manager.Share(action.PortID, action.IdempotencyKey, action.Recipient, action.ExpiresAt)
			case "revoke_share":
				actionErr = manager.RevokeShare(action.GrantID)
			case "unpublish_port":
				actionErr = manager.Unpublish(action.PortID)
			case "sandbox_link":
				target := running[action.TargetID]
				alias, parseErr := netip.ParseAddr(action.Alias)
				if sb == nil || target == nil || parseErr != nil {
					actionErr = network.ErrDenied
					break
				}
				expires := action.ExpiresAt
				if expires.IsZero() {
					expires = time.Now().Add(time.Hour)
				}
				link, err := sb.director.AttachLink(target.bridge, alias, action.Protocol, action.Port, expires)
				actionErr = err
				if err == nil {
					links[link.ID] = link
					result = map[string]any{"connection_id": link.ID, "source_sandbox_id": action.SandboxID, "target_sandbox_id": action.TargetID, "alias": alias.String(), "protocol": action.Protocol, "port": action.Port}
				}
			case "revoke_link":
				if link := links[action.ConnectionID]; link != nil {
					actionErr = link.Close()
				} else {
					actionErr = network.ErrDenied
				}
			case "policy_update":
				if sb == nil {
					actionErr = network.ErrDenied
				} else {
					actionErr = sb.director.Update(action.ExpectedRevision, action.Revision, action.Layers)
				}
			default:
				actionErr = network.ErrDenied
			}
			response := map[string]any{"state": "applied", "data": result}
			if actionErr != nil {
				response = map[string]any{"state": "rejected", "error": fmt.Sprint(actionErr)}
			}
			if encoder.Encode(response) != nil {
				return network.ErrUnavailable
			}
		}
	}
}
