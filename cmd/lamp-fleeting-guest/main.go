// Command lamp-fleeting-guest runs inside a Lamp sandbox for the lamp-fleeting
// GitLab Fleeting plugin. Guests accept no inbound connections, so it dials
// the plugin's callback address (an exact enrolled network exception) and
// serves SSH itself on each stream the plugin asks for. It runs as the
// sandbox's unprivileged user and needs no system sshd or writable /run.
//
//	LAMP_FLEETING_SECRET=<secret> lamp-fleeting-guest <callback host:port> <instance id> <authorized public key>
//
// The secret comes by environment, never argv, so no exec record or argv
// audit holds it; the agent removes it from its own environment at start.
package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"
)

func main() {
	secret := os.Getenv("LAMP_FLEETING_SECRET")
	os.Unsetenv("LAMP_FLEETING_SECRET")
	if len(os.Args) != 4 || secret == "" {
		fmt.Fprintln(os.Stderr, "usage: LAMP_FLEETING_SECRET=... lamp-fleeting-guest <callback> <instance> <public key>")
		os.Exit(2)
	}
	cb, id := os.Args[1], os.Args[2]
	if os.Getenv("HOME") == "" {
		os.Setenv("HOME", "/var/tmp/lamp-fleeting")
	}
	os.MkdirAll(os.Getenv("HOME"), 0700)
	// Artifact and cache steps call the runner helper by name.
	os.Setenv("PATH", "/opt/gitlab-runner:/usr/local/bin:/usr/bin:/bin")
	allowed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(os.Args[3]))
	if err != nil {
		fail(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if bytes.Equal(k.Marshal(), allowed.Marshal()) {
			return nil, nil
		}
		return nil, fmt.Errorf("unknown key")
	}}
	cfg.AddHostKey(signer)
	name := "lamp"
	if u, e := user.Current(); e == nil {
		name = u.Username
	}
	ctl, err := net.Dial("tcp", cb)
	if err != nil {
		fail(err)
	}
	fmt.Fprintf(ctl, "ctl %s %s %s\n", id, secret, name)
	lines := bufio.NewScanner(ctl)
	for lines.Scan() {
		if strings.TrimSpace(lines.Text()) != "open" {
			continue
		}
		go func() {
			c, err := net.Dial("tcp", cb)
			if err != nil {
				return
			}
			fmt.Fprintf(c, "data %s %s\n", id, secret)
			serve(c, cfg)
		}()
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "lamp-fleeting-guest:", err)
	os.Exit(1)
}

func serve(c net.Conn, cfg *ssh.ServerConfig) {
	defer c.Close()
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, in, err := nc.Accept()
		if err != nil {
			continue
		}
		go session(ch, in)
	}
}

// session runs one exec (or a login shell) with the requested environment.
func session(ch ssh.Channel, in <-chan *ssh.Request) {
	defer ch.Close()
	env := os.Environ()
	for req := range in {
		switch req.Type {
		case "env":
			var kv struct{ K, V string }
			if ssh.Unmarshal(req.Payload, &kv) == nil {
				env = append(env, kv.K+"="+kv.V)
			}
			req.Reply(true, nil)
		case "pty-req", "window-change":
			req.Reply(true, nil)
		case "exec", "shell":
			argv := []string{"bash", "-l"}
			if req.Type == "exec" {
				var p struct{ Cmd string }
				ssh.Unmarshal(req.Payload, &p)
				argv = []string{"sh", "-c", p.Cmd}
			}
			req.Reply(true, nil)
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Env, cmd.Stdout, cmd.Stderr = env, ch, ch.Stderr()
			if h, e := os.UserHomeDir(); e == nil {
				cmd.Dir = h
			}
			stdin, _ := cmd.StdinPipe()
			go func() { io.Copy(stdin, ch); stdin.Close() }()
			code := uint32(0)
			if err := cmd.Run(); err != nil {
				code = 255
				if x, ok := err.(*exec.ExitError); ok {
					if ws, ok := x.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
						code = 128 + uint32(ws.Signal())
					} else {
						code = uint32(x.ExitCode())
					}
				}
			}
			b := make([]byte, 4)
			binary.BigEndian.PutUint32(b, code)
			ch.SendRequest("exit-status", false, b)
			return
		default:
			req.Reply(false, nil)
		}
	}
}
