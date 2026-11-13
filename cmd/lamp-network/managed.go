package main

// The engine starts one managed data plane for a generation. The inherited
// private stream is its only management authority; losing it closes the NIC,
// previews, links and active egress. No guest chooses host endpoints.
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"time"

	"lamp.eilmer.si/internal/network"
)

type managedConfig struct {
	ControlFD int    `json:"control_fd"`
	GuardUID  int    `json:"guard_uid"`
	StateDir  string `json:"state_dir"`
	Sandbox   config `json:"sandbox"`
}
type managedAction struct {
	Operation string          `json:"operation"`
	Port      uint16          `json:"port"`
	PortID    string          `json:"port_id"`
	Public    bool            `json:"public"`
	GrantID   string          `json:"grant_id"`
	Key       string          `json:"key"`
	Expires   time.Time       `json:"expires_at"`
	Expected  uint64          `json:"expected_revision"`
	Revision  uint64          `json:"revision"`
	Layers    []network.Layer `json:"policy_layers"`
	Target    network.Subject `json:"target"`
	Address   string          `json:"address"`
	Token     string          `json:"token"`
	Alias     netip.Addr      `json:"alias"`
	Provider  struct {
		Type     string         `json:"type"`
		Endpoint netip.AddrPort `json:"endpoint"`
	} `json:"provider"`
	Access       string `json:"access"`
	ConnectionID string `json:"connection_id"`
}
type remoteGuest struct {
	subject        network.Subject
	address, token string
	port           uint16
}

func (g *remoteGuest) Subject() network.Subject { return g.subject }
func (g *remoteGuest) DialGuest(ctx context.Context, protocol string, port uint16) (net.Conn, error) {
	if protocol != "tcp" || port != g.port {
		return nil, network.ErrDenied
	}
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", g.address)
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	_ = c.SetDeadline(deadline)
	if err = json.NewEncoder(c).Encode(map[string]string{"token": g.token}); err != nil {
		c.Close()
		return nil, err
	}
	var ack [1]byte
	if _, err = io.ReadFull(c, ack[:]); err != nil || ack[0] != 1 {
		c.Close()
		return nil, network.ErrDenied
	}
	_ = c.SetDeadline(time.Time{})
	return c, nil
}

type guestGrant struct {
	net.Listener
	cancel context.CancelFunc
}

func (g *guestGrant) Close() error { g.cancel(); return g.Listener.Close() }

func managed(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return network.ErrDenied
	}
	var plan managedConfig
	if err = network.DecodeConfig(f, &plan); err != nil {
		return fmt.Errorf("managed plan: %w", err)
	}
	if plan.ControlFD < 3 || plan.Sandbox.Bridge == nil {
		return fmt.Errorf("managed descriptors: %w", network.ErrDenied)
	}
	controlFile := os.NewFile(uintptr(plan.ControlFD), "engine-network-control")
	control, err := net.FileConn(controlFile)
	controlFile.Close()
	if err != nil {
		return fmt.Errorf("control descriptor: %w", err)
	}
	defer control.Close()
	if _, ok := control.(*net.UnixConn); !ok {
		return network.ErrDenied
	}
	c := plan.Sandbox
	addresses, prefixes, err := network.Inventory()
	if err != nil {
		return err
	}
	c.Policy.HostAddresses = append(c.Policy.HostAddresses, addresses...)
	c.Policy.LANPrefixes = append(c.Policy.LANPrefixes, prefixes...)
	frames, err := network.OpenPrivateTAP(c.Bridge.Namespace, c.Bridge.Interface)
	if err != nil {
		return fmt.Errorf("private TAP: %w", err)
	}
	defer frames.Close()
	audit, err := network.NewFileAudit(c.Audit, c.AuditBytes)
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	defer audit.Close()
	authority, err := network.NewAuthority(plan.StateDir)
	if err != nil {
		return fmt.Errorf("authority: %w", err)
	}
	ports, err := network.NewPortManager(plan.StateDir, "localhost", 443, authority, func(context.Context, *http.Request) (network.PeerIdentity, error) {
		return network.PeerIdentity{}, network.ErrDenied
	}, audit, nil)
	if err != nil {
		return err
	}
	defer ports.Close()
	if err = network.DropGuardPrivileges(plan.GuardUID); err != nil {
		return fmt.Errorf("drop privileges: %w", err)
	}
	provider := network.NewDirectProvider(nil)
	director, err := network.NewDirector(c.Subject, c.Revision, c.Policy, provider, audit)
	if err != nil {
		return fmt.Errorf("director: %w", err)
	}
	defer director.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge, err := network.NewFrameBridge(ctx, c.Bridge.Transport, frames, director)
	if err != nil {
		return fmt.Errorf("frame bridge: %w", err)
	}
	defer bridge.Close()
	if err = json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "ready"}); err != nil {
		return err
	}
	links := map[string]*network.SandboxLink{}
	guests := map[string]*guestGrant{}
	defer func() {
		for _, l := range links {
			l.Close()
		}
		for _, l := range guests {
			l.Close()
		}
	}()
	decoder := json.NewDecoder(control)
	encoder := json.NewEncoder(control)
	for {
		var a managedAction
		if decoder.Decode(&a) != nil {
			return network.ErrUnavailable
		}
		var result any
		var actionErr error
		switch a.Operation {
		case "publish_port":
			result, actionErr = ports.PublishLoopback(bridge, a.Port, a.Public)
			if actionErr == nil && !a.Expires.IsZero() {
				id := result.(network.PublishedPort).ID
				time.AfterFunc(time.Until(a.Expires), func() { _ = ports.Unpublish(id) })
			}
		case "share_port":
			result, actionErr = ports.Share(a.PortID, a.Key, "", a.Expires)
		case "revoke_share":
			actionErr = ports.RevokeShare(a.GrantID)
		case "unpublish_port":
			actionErr = ports.Unpublish(a.PortID)
		case "public_port":
			actionErr = ports.SetPublic(a.PortID, a.Public)
		case "select_exit":
			var p network.Provider
			switch a.Provider.Type {
			case "direct":
				p = network.NewDirectProvider(nil)
			case "ssh_connect":
				p, actionErr = network.NewConnectProvider(a.Provider.Endpoint)
			default:
				actionErr = network.ErrDenied
			}
			if actionErr == nil {
				actionErr = director.SelectProvider(p)
			}
		case "host_service":
			address, e := netip.ParseAddrPort(a.Address)
			if e != nil || !address.Addr().IsLoopback() {
				actionErr = network.ErrDenied
				break
			}
			service := &network.HostService{ID: network.Subject{SandboxID: "sb_service_" + a.ConnectionID, Generation: 1}, Endpoint: address, Access: a.Access}
			link, e := director.AttachLink(service, a.Alias, "tcp", a.Port, a.Expires)
			actionErr = e
			if e == nil {
				links[a.ConnectionID] = link
			}
		case "policy_update":
			actionErr = director.Update(a.Expected, a.Revision, a.Layers)
		case "guest_grant":
			if a.Port == 0 || a.Expires.IsZero() || !a.Expires.After(time.Now()) || len(guests) >= 64 {
				actionErr = network.ErrDenied
				break
			}
			listener, e := net.Listen("tcp4", "127.0.0.1:0")
			if e != nil {
				actionErr = e
				break
			}
			// The token arrives only over the supervisor's private stream and never
			// becomes a URL, audit value or public resource attribute.
			if len(a.Token) < 32 {
				listener.Close()
				actionErr = network.ErrDenied
				break
			}
			guestCtx, guestCancel := context.WithDeadline(ctx, a.Expires)
			guests[a.ConnectionID] = &guestGrant{listener, guestCancel}
			context.AfterFunc(guestCtx, func() { listener.Close() })
			deadline := a.Expires
			port := a.Port
			hash := sha256.Sum256([]byte(a.Token))
			go func() {
				timer := time.AfterFunc(time.Until(deadline), func() { listener.Close() })
				defer timer.Stop()
				for {
					raw, e := listener.Accept()
					if e != nil {
						return
					}
					go func() {
						defer raw.Close()
						stop := context.AfterFunc(guestCtx, func() { raw.Close() })
						defer stop()
						_ = raw.SetDeadline(time.Now().Add(3 * time.Second))
						var hello struct {
							Token string `json:"token"`
						}
						if json.NewDecoder(io.LimitReader(raw, 512)).Decode(&hello) != nil || sha256.Sum256([]byte(hello.Token)) != hash {
							return
						}
						upstream, e := bridge.DialGuest(ctx, "tcp", port)
						if e != nil {
							return
						}
						defer upstream.Close()
						if _, e = raw.Write([]byte{1}); e != nil {
							return
						}
						_ = raw.SetDeadline(deadline)
						done := make(chan struct{})
						go func() { _, _ = io.Copy(upstream, raw); raw.Close(); upstream.Close(); close(done) }()
						_, _ = io.Copy(raw, upstream)
						raw.Close()
						upstream.Close()
						<-done
					}()
				}
			}()
			result = map[string]any{"address": listener.Addr().String(), "target": c.Subject}
		case "sandbox_link":
			addr, e := netip.ParseAddrPort(a.Address)
			if e != nil || !addr.Addr().IsLoopback() {
				actionErr = network.ErrDenied
				break
			}
			guest := &remoteGuest{a.Target, a.Address, a.Token, a.Port}
			link, e := director.AttachLink(guest, a.Alias, "tcp", a.Port, a.Expires)
			actionErr = e
			if e == nil {
				links[a.ConnectionID] = link
			}
		case "revoke_link":
			if l := links[a.ConnectionID]; l != nil {
				actionErr = l.Close()
			} else if l := guests[a.ConnectionID]; l != nil {
				actionErr = l.Close()
			} else {
				actionErr = network.ErrDenied
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
