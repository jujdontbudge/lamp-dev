// contractprobe is an unprivileged contract-test process, never a Lamp worker.
package main

import (
	"encoding/json"
	"fmt"
	"github.com/JohanSardar/lamp/pkg/api"
	w "github.com/JohanSardar/lamp/pkg/workerpb"
	"io"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		panic("mode required")
	}
	var err error
	switch os.Args[1] {
	case "worker-client":
		ctx := &w.Context{RequestId: "request-fixture", OperationId: "operation-fixture", DeadlineMs: 18446744073709551615, SandboxId: "sandbox-fixture", Generation: 18446744073709551615, GrantId: "grant-fixture", PolicyRevision: 18446744073709551615}
		f := &w.Frame{Payload: &w.Frame_Request{Request: &w.Request{Context: ctx, Command: &w.Request_Inspect{Inspect: &w.Inspect{}}}}}
		if err = w.WriteFrame(os.Stdout, f); err != nil {
			break
		}
		var reply *w.Frame
		reply, err = w.ReadFrame(os.Stdin)
		if err == nil && (reply.GetResponse() == nil || reply.GetResponse().GetReceipt().GetState() != "accepted" || reply.GetResponse().GetContext().GetGeneration() != ctx.Generation) {
			err = fmt.Errorf("invalid reply: %v", reply)
		}
	case "input-json":
		var value api.SessionInputInput
		dec := json.NewDecoder(os.Stdin)
		dec.DisallowUnknownFields()
		if err = dec.Decode(&value); err == nil {
			err = json.NewEncoder(os.Stdout).Encode(value)
		}
	case "scope-json":
		var value api.Scope
		dec := json.NewDecoder(os.Stdin)
		dec.DisallowUnknownFields()
		if err = dec.Decode(&value); err == nil {
			err = json.NewEncoder(os.Stdout).Encode(value)
		}
	case "metadata":
		err = json.NewEncoder(os.Stdout).Encode(api.Operations)
	default:
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
