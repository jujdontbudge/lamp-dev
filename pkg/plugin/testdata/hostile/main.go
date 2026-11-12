package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"lamp.eilmer.si/pkg/plugin"
)

func send(m plugin.Message) { raw, _ := json.Marshal(m); _ = plugin.WriteFrame(os.Stdout, raw) }

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
