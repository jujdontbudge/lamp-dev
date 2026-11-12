// lamp-network runs host-only network provider fixtures. It has no guest RPC,
// credential flags, public listener or unguarded fallback. Product control/engine
// integration owns admission, identities, descriptors and process supervision.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lamp.eilmer.si/internal/network"
)

type config struct {
	Subject  network.Subject `json:"subject"`
	Revision uint64          `json:"revision"`
	Policy   network.Policy  `json:"policy"`
	Provider struct {
		Type     string         `json:"type"`
		Endpoint netip.AddrPort `json:"endpoint"`
	} `json:"provider"`
	Audit          string              `json:"audit"`
	AuditBytes     int64               `json:"audit_bytes"`
	Listen         string              `json:"listen"`
	Reflector      string              `json:"reflector"`
	ExpectedExitIP netip.Addr          `json:"expected_exit_ip"`
	Target         network.Destination `json:"target"`
	Member         *struct {
		StateDir    string `json:"state_dir"`
		Hostname    string `json:"hostname"`
		ControlURL  string `json:"control_url"`
		AuthURLFile string `json:"auth_url_file"`
		AuthFD      int    `json:"auth_fd"`
	} `json:"member"`
	PortMaps []network.PortMap `json:"port_maps"`
	Bridge   *struct {
		Transport network.BridgeConfig `json:"transport"`
		Namespace string               `json:"namespace"`
		Interface string               `json:"interface"`
		FrameFD   int                  `json:"frame_fd"`
		GuardUID  int                  `json:"guard_uid"`
	} `json:"bridge"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lamp-network:", err)
		os.Exit(70)
	}
}
func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("usage: lamp-network serve|serve-exit|verify-exit|probe|probe-session|member|bridge CONFIG")
	}
	if os.Args[1] == "serve" {
		return runService(os.Args[2])
	}
	switch os.Args[1] {
	case "serve-exit", "verify-exit", "probe", "probe-session", "member", "bridge":
	default:
		return fmt.Errorf("unsupported host network command")
	}
	file, err := os.Open(os.Args[2])
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4<<20 {
		return fmt.Errorf("private regular host config required")
	}
	var c config
	if err := network.DecodeConfig(file, &c); err != nil {
		return fmt.Errorf("invalid host network configuration")
	}
	if c.Member != nil && c.Member.AuthFD != 0 && (c.Member.AuthFD < 3 || (c.Bridge != nil && c.Member.AuthFD == c.Bridge.FrameFD)) {
		return network.ErrDenied
	}
	addresses, prefixes, err := network.Inventory()
	if err != nil {
		return err
	}
	c.Policy.HostAddresses = append(c.Policy.HostAddresses, addresses...)
	c.Policy.LANPrefixes = append(c.Policy.LANPrefixes, prefixes...)
	if err := c.Policy.Validate(); err != nil {
		return err
	}
	if os.Args[1] == "serve-exit" && c.Provider.Type != "direct" {
		return fmt.Errorf("owned exit service requires a direct numeric upstream")
	}
	audit, err := network.NewFileAudit(c.Audit, c.AuditBytes)
	if err != nil {
		return err
	}
	defer audit.Close()
	var frames *os.File
	if os.Args[1] == "bridge" {
		if c.Bridge == nil || c.Bridge.Transport.Subject != c.Subject || c.Bridge.GuardUID < 65536 {
			return network.ErrDenied
		}
		if c.Bridge.FrameFD != 0 {
			if c.Bridge.Namespace != "" || c.Bridge.Interface != "" {
				return network.ErrDenied
			}
			frames, err = network.AdoptEthernetFD(c.Bridge.FrameFD)
		} else {
			frames, err = network.OpenPrivateTAP(c.Bridge.Namespace, c.Bridge.Interface)
		}
		if err != nil {
			return err
		}
		defer frames.Close()
		if err := network.DropGuardPrivileges(c.Bridge.GuardUID); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var provider network.Provider
	var member *network.Member
	switch c.Provider.Type {
	case "direct":
		provider = network.NewDirectProvider(nil)
	case "ssh_connect":
		provider, err = network.NewConnectProvider(c.Provider.Endpoint)
	case "private_member":
		if c.Member == nil {
			return fmt.Errorf("subject-bound private member state required")
		}
		authKey := ""
		if c.Member.AuthFD != 0 {
			if c.Member.AuthFD < 3 {
				return fmt.Errorf("dedicated host auth descriptor required")
			}
			secret := os.NewFile(uintptr(c.Member.AuthFD), "host-member-auth")
			if secret == nil {
				return fmt.Errorf("host auth descriptor unavailable")
			}
			data, readErr := io.ReadAll(io.LimitReader(secret, 4097))
			closeErr := secret.Close()
			if readErr != nil || closeErr != nil || len(data) > 4096 {
				return fmt.Errorf("invalid host auth input")
			}
			authKey = strings.TrimSpace(string(data))
			clear(data)
		}
		member, err = network.NewMemberWithControl(c.Subject, c.Member.StateDir, c.Member.Hostname, authKey, c.Member.ControlURL)
		if err != nil {
			return err
		}
		defer member.Close()
		ownedURL := false
		defer func() {
			if ownedURL {
				_ = os.Remove(c.Member.AuthURLFile)
			}
		}()
		authSink := func(authURL string) error {
			if c.Member.AuthURLFile == "" {
				return network.ErrUnavailable
			}
			parent, e := os.Stat(filepath.Dir(c.Member.AuthURLFile))
			if e != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
				return network.ErrDenied
			}
			f, e := os.OpenFile(c.Member.AuthURLFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return network.ErrUnavailable
			}
			ownedURL = true
			_, e = io.WriteString(f, authURL)
			if e == nil {
				e = f.Sync()
			}
			closeErr := f.Close()
			if e != nil || closeErr != nil {
				return network.ErrUnavailable
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "needs_auth", "auth_url_file": c.Member.AuthURLFile})
		}
		approval, done := context.WithTimeout(ctx, 10*time.Minute)
		err = member.Start(approval, authSink)
		done()
		if err != nil {
			return err
		}
		if ownedURL {
			_ = os.Remove(c.Member.AuthURLFile)
			ownedURL = false
		}
		provider = member
	default:
		return fmt.Errorf("unsupported selected provider")
	}
	if err != nil {
		return err
	}
	director, err := network.NewDirector(c.Subject, c.Revision, c.Policy, provider, audit)
	if err != nil {
		provider.Close()
		return err
	}
	defer director.Close()
	switch os.Args[1] {
	case "bridge":
		bridge, err := network.NewFrameBridge(ctx, c.Bridge.Transport, frames, director)
		if err != nil {
			return err
		}
		defer bridge.Close()
		if len(c.PortMaps) > 0 {
			if member == nil {
				return fmt.Errorf("inbound maps require a real private member")
			}
			ports, err := member.MapPorts(ctx, c.PortMaps, bridge, c.Revision, audit)
			if err != nil {
				return err
			}
			defer ports.Close()
		}
		ready := map[string]any{"state": "ready", "sandbox_id": c.Subject.SandboxID, "generation": c.Subject.Generation, "transport": "private_ethernet", "pid": os.Getpid()}
		if member != nil {
			ready["member_addresses"] = member.Addresses()
		}
		if json.NewEncoder(os.Stdout).Encode(ready) != nil {
			return network.ErrUnavailable
		}
		select {
		case <-ctx.Done():
			return nil
		case <-bridge.Done():
			return network.ErrUnavailable
		}
	case "member":
		if member == nil {
			return fmt.Errorf("private_member provider required")
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "ready", "member_addresses": member.Addresses(), "sandbox_id": c.Subject.SandboxID, "generation": c.Subject.Generation}); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	case "serve-exit":
		if c.Provider.Type != "direct" {
			return fmt.Errorf("owned exit service requires a direct numeric upstream")
		}
		address, err := netip.ParseAddrPort(c.Listen)
		if err != nil || !address.Addr().IsLoopback() {
			return fmt.Errorf("owned exit listener must be loopback")
		}
		listener, err := net.Listen("tcp", address.String())
		if err != nil {
			return err
		}
		defer listener.Close()
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "ready", "listener": listener.Addr().String(), "pid": os.Getpid()}); err != nil {
			return err
		}
		return network.ServeConnect(ctx, listener, director)
	case "verify-exit":
		observed, err := director.VerifyExit(ctx, c.Reflector, c.ExpectedExitIP)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "verified", "observed_exit_ip": observed.String(), "expected_exit_ip": c.ExpectedExitIP.String(), "provider": c.Provider.Type})
	case "probe", "probe-session":
		permit, err := director.Authorize(c.Subject, c.Target)
		if err != nil {
			return err
		}
		conn, err := director.Dial(ctx, permit)
		if err != nil {
			return err
		}
		defer conn.Close()
		digest, err := probe(conn, c.Target.Protocol)
		if err != nil {
			return err
		}
		if os.Args[1] == "probe-session" {
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "ready", "response_sha256": digest}); err != nil {
				return err
			}
			var action struct {
				Operation string `json:"operation"`
			}
			decoder := json.NewDecoder(io.LimitReader(os.Stdin, 256))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&action) != nil || action.Operation != "probe" {
				return fmt.Errorf("probe action required")
			}
			_, err := probe(conn, c.Target.Protocol)
			state := "allowed"
			if err != nil {
				state = "blocked"
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"state": state})
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "verified", "protocol": c.Target.Protocol, "response_sha256": digest})
	default:
		return fmt.Errorf("unsupported host network command")
	}
}

func probe(conn net.Conn, protocol string) (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	if n, err := conn.Write(data); err != nil || n != len(data) {
		return "", fmt.Errorf("probe write failed")
	}
	received := make([]byte, len(data))
	if protocol == "udp" {
		n, err := conn.Read(received)
		if err != nil || n != len(data) {
			return "", fmt.Errorf("datagram probe failed")
		}
	} else {
		if _, err := io.ReadFull(conn, received); err != nil {
			return "", fmt.Errorf("stream probe failed")
		}
	}
	expected := sha256.Sum256(data)
	actual := sha256.Sum256(received)
	if actual != expected {
		return "", fmt.Errorf("independent echo bytes differ")
	}
	return hex.EncodeToString(actual[:]), nil
}
