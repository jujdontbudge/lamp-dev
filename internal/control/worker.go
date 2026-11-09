package control

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	pb "github.com/JohanSardar/lamp/pkg/workerpb"
	"google.golang.org/protobuf/proto"
)

// AuthenticateLocal admits the one OS owner of the private public API socket.
func AuthenticateLocal(conn *net.UnixConn) error { return authenticatePeer(conn, os.Geteuid(), 0) }

// WorkerConfig pins the locally enrolled supervisor, not a caller-supplied address.
// GrantID names a supervisor-installed control grant; this client never opens RAM,
// disk, or hypervisor handles and cannot mint engine authority.
type WorkerConfig struct {
	Socket, WorkerID, BootID, GrantID string
	UID, PID                          int
	Timeout                           time.Duration
}
type workerClient struct {
	config  WorkerConfig
	mu      sync.Mutex
	conn    *net.UnixConn
	maximum uint32
}
type workerFailure struct {
	code, message string
	unknown       bool
	retryable     bool
}

func (e *workerFailure) Error() string { return e.message }
func (w *workerClient) close()         { w.mu.Lock(); defer w.mu.Unlock(); w.disconnect() }
func (w *workerClient) disconnect() {
	if w.conn != nil {
		w.conn.Close()
		w.conn = nil
	}
}
func readBounded(r io.Reader, maximum uint32) (*pb.Frame, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > maximum {
		return nil, errors.New("invalid negotiated worker frame length")
	}
	return pb.ReadFrame(io.MultiReader(bytes.NewReader(prefix[:]), r))
}
func (w *workerClient) connect(ctx context.Context) error {
	if w.conn != nil {
		return nil
	}
	c, err := (&net.Dialer{}).DialContext(ctx, "unix", w.config.Socket)
	if err != nil {
		return err
	}
	w.conn = c.(*net.UnixConn)
	conn := w.conn
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { conn.Close(); close(stopped) })
	defer func() {
		if !stop() {
			<-stopped
		}
	}()
	if err := authenticatePeer(w.conn, w.config.UID, w.config.PID); err != nil {
		w.disconnect()
		return err
	}
	deadline, _ := ctx.Deadline()
	w.conn.SetDeadline(deadline)
	h := &pb.Hello{Protocol: pb.Protocol, WorkerId: w.config.WorkerID, BootId: w.config.BootID, MaxControlBytes: pb.MaxControlBytes}
	if err := pb.WriteFrame(w.conn, &pb.Frame{Payload: &pb.Frame_Hello{Hello: h}}); err != nil {
		w.disconnect()
		return err
	}
	f, err := readBounded(w.conn, pb.MaxControlBytes)
	if err != nil {
		w.disconnect()
		return err
	}
	got := f.GetHello()
	if got == nil || got.Protocol != pb.Protocol || got.WorkerId != h.WorkerId || got.BootId != h.BootId || got.MaxControlBytes == 0 || got.MaxControlBytes > pb.MaxControlBytes {
		w.disconnect()
		return errors.New("unassigned worker or incompatible Hello")
	}
	w.maximum = got.MaxControlBytes
	return nil
}
func (w *workerClient) call(ctx context.Context, r *pb.Request) (*pb.Receipt, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, &workerFailure{code: "HOST_UNAVAILABLE", message: err.Error(), unknown: false}
	}
	if err := w.connect(ctx); err != nil {
		return nil, &workerFailure{code: "HOST_UNAVAILABLE", message: err.Error(), unknown: false}
	}
	deadline, _ := ctx.Deadline()
	w.conn.SetDeadline(deadline)
	conn := w.conn
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { conn.Close(); close(stopped) })
	defer func() {
		if !stop() {
			<-stopped
		}
	}()
	f := &pb.Frame{Payload: &pb.Frame_Request{Request: r}}
	if proto.Size(f) > int(w.maximum) {
		return nil, &workerFailure{code: "INVALID_ARGUMENT", message: "request exceeds negotiated control maximum", unknown: false}
	}
	// From the first attempted write onward, transport failure is uncertain. Never
	// reconnect and redeliver this effect under another request/operation ID.
	if err := pb.WriteFrame(conn, f); err != nil {
		w.disconnect()
		return nil, &workerFailure{code: "OUTCOME_UNKNOWN", message: err.Error(), unknown: true}
	}
	f, err := readBounded(conn, w.maximum)
	if err != nil {
		w.disconnect()
		return nil, &workerFailure{code: "OUTCOME_UNKNOWN", message: err.Error(), unknown: true}
	}
	response := f.GetResponse()
	if response == nil || !proto.Equal(response.Context, r.Context) {
		w.disconnect()
		return nil, &workerFailure{code: "OUTCOME_UNKNOWN", message: "worker response context mismatch", unknown: true}
	}
	if e := response.GetError(); e != nil {
		return nil, &workerFailure{code: e.Code, message: e.Message, unknown: e.Code == "OUTCOME_UNKNOWN", retryable: e.Retryable}
	}
	receipt := response.GetReceipt()
	if receipt == nil {
		w.disconnect()
		return nil, &workerFailure{code: "OUTCOME_UNKNOWN", message: "worker omitted receipt", unknown: true}
	}
	if receipt.OutcomeUnknown {
		return nil, &workerFailure{code: "OUTCOME_UNKNOWN", message: "worker reports uncertain effect", unknown: true}
	}
	return receipt, nil
}

// perform waits for the named evidence, never interpreting acceptance as success.
// Inspect is read-only, uses a new operation ID, and does not retry the effect.
func (w *workerClient) perform(ctx context.Context, r *pb.Request, want string) error {
	receipt, err := w.call(ctx, r)
	if err != nil {
		return err
	}
	for receipt.State != want {
		if receipt.State != "accepted" && receipt.State != "preparing" && receipt.State != "running" && receipt.State != "waiting" {
			return &workerFailure{code: "OUTCOME_UNKNOWN", message: fmt.Sprintf("unexpected worker receipt %q (want %s)", receipt.State, want), unknown: true}
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return &workerFailure{code: "OUTCOME_UNKNOWN", message: "deadline before worker evidence", unknown: true}
		case <-timer.C:
		}
		c := proto.Clone(r.Context).(*pb.Context)
		c.RequestId = newID("req")
		c.OperationId = newID("op")
		receipt, err = w.call(ctx, &pb.Request{Context: c, Command: &pb.Request_Inspect{Inspect: &pb.Inspect{}}})
		if err != nil {
			return &workerFailure{code: "OUTCOME_UNKNOWN", message: "lost terminal evidence after acceptance: " + err.Error(), unknown: true}
		}
	}
	return nil
}
