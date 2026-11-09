package control

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/JohanSardar/lamp/pkg/workerpb"
)

func TestWorkerRejectsUnassignedPeerAndBadProtocol(t *testing.T) {
	for _, name := range []string{"uid", "pid", "protocol", "worker", "boot", "zero-max", "large-max", "context", "negotiated-size", "no-receipt", "unknown"} {
		t.Run(name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "lamp-peer-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			socket := filepath.Join(dir, "w.sock")
			l, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				c, err := l.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(time.Second))
				if _, err := pb.ReadFrame(c); err != nil {
					return
				}
				h := &pb.Hello{Protocol: pb.Protocol, WorkerId: "assigned", BootId: "boot", MaxControlBytes: pb.MaxControlBytes}
				switch name {
				case "protocol":
					h.Protocol = "lamp.worker/2"
				case "worker":
					h.WorkerId = "other"
				case "boot":
					h.BootId = "other"
				case "zero-max":
					h.MaxControlBytes = 0
				case "large-max":
					h.MaxControlBytes = pb.MaxControlBytes + 1
				case "negotiated-size":
					h.MaxControlBytes = 1024
				}
				if pb.WriteFrame(c, &pb.Frame{Payload: &pb.Frame_Hello{Hello: h}}) != nil {
					return
				}
				f, err := pb.ReadFrame(c)
				if err != nil {
					return
				}
				if name == "negotiated-size" {
					var p [4]byte
					binary.BigEndian.PutUint32(p[:], 1025)
					c.Write(p[:])
					return
				}
				r := &pb.Response{Context: f.GetRequest().Context, Result: &pb.Response_Receipt{Receipt: &pb.Receipt{State: "command_started"}}}
				switch name {
				case "context":
					r.Context = &pb.Context{RequestId: "wrong"}
				case "no-receipt":
					r.Result = nil
				case "unknown":
					r.Result = &pb.Response_Receipt{Receipt: &pb.Receipt{State: "command_started", OutcomeUnknown: true}}
				}
				pb.WriteFrame(c, &pb.Frame{Payload: &pb.Frame_Response{Response: r}})
			}()
			config := WorkerConfig{Socket: socket, WorkerID: "assigned", BootID: "boot", UID: os.Geteuid(), PID: os.Getpid(), GrantID: "grant"}
			if name == "uid" {
				config.UID++
			}
			if name == "pid" {
				config.PID++
			}
			w := workerClient{config: config}
			defer w.close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err = w.call(ctx, &pb.Request{Context: &pb.Context{RequestId: "r", OperationId: "o"}, Command: &pb.Request_Inspect{Inspect: &pb.Inspect{}}})
			if err == nil {
				t.Fatal("untrusted response was accepted")
			}
			<-finished
		})
	}
}
func TestWorkerHandshakeHonorsCancellation(t *testing.T) {
	dir, err := os.MkdirTemp("", "lamp-cancel-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "w.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	hello := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		pb.ReadFrame(c)
		close(hello)
		<-release
	}()
	w := workerClient{config: WorkerConfig{Socket: socket, WorkerID: "w", BootID: "b", UID: os.Geteuid(), PID: os.Getpid()}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := w.call(ctx, &pb.Request{Command: &pb.Request_Inspect{Inspect: &pb.Inspect{}}})
		done <- err
	}()
	<-hello
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled handshake succeeded")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("canceled handshake stayed blocked until the original deadline")
		<-done
	}
	w.close()
}
func TestWorkerDoesNotSendOversizeNegotiatedRequest(t *testing.T) {
	dir, err := os.MkdirTemp("", "lamp-size-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "w.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	noRequest := make(chan bool, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(time.Second))
		pb.ReadFrame(c)
		pb.WriteFrame(c, &pb.Frame{Payload: &pb.Frame_Hello{Hello: &pb.Hello{Protocol: pb.Protocol, WorkerId: "w", BootId: "b", MaxControlBytes: 16}}})
		var b [1]byte
		n, _ := c.Read(b[:])
		noRequest <- n == 0
	}()
	w := workerClient{config: WorkerConfig{Socket: socket, WorkerID: "w", BootID: "b", UID: os.Geteuid(), PID: os.Getpid()}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = w.call(ctx, &pb.Request{Context: &pb.Context{RequestId: "long-enough-to-exceed-sixteen-bytes"}, Command: &pb.Request_Inspect{Inspect: &pb.Inspect{}}})
	if err == nil {
		t.Fatal("oversized request accepted")
	}
	w.close()
	if !<-noRequest {
		t.Fatal("oversize frame wrote bytes")
	}
}
