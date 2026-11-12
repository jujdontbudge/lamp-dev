// lamp-plugin-reference demonstrates the public pipe protocol for all ten types.
// It has no ambient effects: it asks the core to exercise its assigned typed
// grant, and returns the core's receipt. External systems belong behind core's
// current-policy authorizer, never behind a plugin's self-reported authority.
package main

import (
	"encoding/json"
	"os"

	"lamp.eilmer.si/pkg/plugin"
)

func send(m plugin.Message) bool {
	b, err := json.Marshal(m)
	return err == nil && plugin.WriteFrame(os.Stdout, b) == nil
}
func main() {
	var hello plugin.Hello
	pending := map[string]string{}
	for {
		raw, err := plugin.ReadFrame(os.Stdin)
		if err != nil {
			return
		}
		var m plugin.Message
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		switch m.Method {
		case "initialize":
			if json.Unmarshal(m.Params, &hello) != nil || hello.ProtocolVersion != plugin.Protocol {
				return
			}
			raw, _ := json.Marshal(hello)
			if !send(plugin.Message{JSONRPC: "2.0", ID: m.ID, Result: raw}) {
				return
			}
		case "run":
			var call plugin.Invocation
			if json.Unmarshal(m.Params, &call) != nil {
				return
			}
			e := plugin.Effect{CallID: m.ID, GrantID: call.Grant.ID, Kind: call.Grant.Kind, ResourceID: call.Grant.ResourceID, Binding: call.Binding, Input: call.Input}
			raw, _ := json.Marshal(e)
			id := "p-" + m.ID
			if len(pending) >= 16 {
				return
			}
			pending[id] = m.ID
			if !send(plugin.Message{JSONRPC: "2.0", ID: id, Method: "host.effect", Params: raw}) {
				return
			}
		case "":
			id, ok := pending[m.ID]
			if !ok {
				return
			}
			delete(pending, m.ID)
			if !send(plugin.Message{JSONRPC: "2.0", ID: id, Result: m.Result, Error: m.Error}) {
				return
			}
		case "cancel":
			return
		default:
			if !send(plugin.Message{JSONRPC: "2.0", ID: m.ID, Error: &plugin.RPCError{Code: -32601, Message: "unknown method"}}) {
				return
			}
		}
	}
}
