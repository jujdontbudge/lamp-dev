package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/JohanSardar/lamp/pkg/plugin"
)

func send(m plugin.Message) { raw, _ := json.Marshal(m); _ = plugin.WriteFrame(os.Stdout, raw) }

var allocated []byte

func main() {
	for {
		raw, err := plugin.ReadFrame(os.Stdin)
		if err != nil {
			return
		}
		var m plugin.Message
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		if m.Method == "initialize" {
			send(plugin.Message{JSONRPC: "2.0", ID: m.ID, Result: m.Params})
			continue
		}
		var call plugin.Invocation
		if json.Unmarshal(m.Params, &call) != nil {
			return
		}
		var input map[string]string
		_ = json.Unmarshal(call.Input, &input)
		switch input["mode"] {
		case "crash":
			os.Exit(79)
		case "hang":
			for {
				time.Sleep(time.Hour)
			}
		case "oversize":
			_, _ = os.Stdout.Write([]byte{0, 16, 0, 1})
			for {
				time.Sleep(time.Hour)
			}
		case "memory":
			allocated = make([]byte, 128<<20)
			for i := 0; i < len(allocated); i += 4096 {
				allocated[i] = 1
			}
			for {
				time.Sleep(time.Hour)
			}
		case "stderr":
			_, _ = os.Stderr.Write(make([]byte, 2<<20))
			for {
				time.Sleep(time.Hour)
			}
		case "malformed":
			_, _ = os.Stdout.Write([]byte{0, 0, 0, 2, '{', 'x'})
			for {
				time.Sleep(time.Hour)
			}
		case "flood":
			for {
				send(plugin.Message{JSONRPC: "2.0", ID: "unsolicited", Result: json.RawMessage(`{}`)})
			}
		case "replay", "forge":
			e := plugin.Effect{CallID: m.ID, GrantID: call.Grant.ID, Kind: call.Grant.Kind, ResourceID: call.Grant.ResourceID, Binding: call.Binding, Input: call.Input}
			if input["mode"] == "forge" {
				e.ResourceID = "neighbor"
				e.Kind = "ram"
			}
			params, _ := json.Marshal(e)
			request := plugin.Message{JSONRPC: "2.0", ID: "p-duplicate", Method: "host.effect", Params: params}
			send(request)
			if _, err := plugin.ReadFrame(os.Stdin); err != nil {
				return
			}
			if input["mode"] == "replay" {
				send(request)
				if _, err := plugin.ReadFrame(os.Stdin); err != nil {
					return
				}
			}
			send(plugin.Message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage(`{}`)})
		case "probe":
			found := map[string]bool{}
			_, err := os.ReadFile(input["path"])
			found["file"] = err == nil
			c, err := net.DialTimeout("tcp", input["tcp"], 100*time.Millisecond)
			found["tcp"] = err == nil
			if c != nil {
				c.Close()
			}
			c, err = net.DialTimeout("unix", input["unix"], 100*time.Millisecond)
			found["unix"] = err == nil
			if c != nil {
				c.Close()
			}
			found["env"] = os.Getenv("LAMP_TEST_OWNER_SECRET") != ""
			fd, _ := strconv.Atoi(input["fd"])
			f := os.NewFile(uintptr(fd), "probe")
			found["fd"] = false
			if info, err := f.Stat(); err == nil && info.Mode().IsRegular() {
				buf := make([]byte, 16)
				_, _ = f.Read(buf)
				found["fd"] = string(buf) == "inherited-secret"
			}
			found["subprocess"] = exec.Command("/bin/sh", "-c", "exit 0").Run() == nil
			result, _ := json.Marshal(found)
			send(plugin.Message{JSONRPC: "2.0", ID: m.ID, Result: result})
		}
	}
}
