package workerpb

import (
	"bytes"
	"encoding/binary"
	"google.golang.org/protobuf/proto"
	"io"
	"strings"
	"testing"
)

func TestRoundTripAndLimits(t *testing.T) {
	hello := &Hello{Protocol: Protocol, WorkerId: "worker", BootId: "boot", MaxControlBytes: MaxControlBytes}
	frame := &Frame{Payload: &Frame_Hello{Hello: hello}}
	var b bytes.Buffer
	if err := WriteFrame(&b, frame); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&b)
	if err != nil || !proto.Equal(got, frame) {
		t.Fatalf("round trip: %v", err)
	}
	for _, n := range []uint32{0, MaxControlBytes + 1, ^uint32(0)} {
		var p [4]byte
		binary.BigEndian.PutUint32(p[:], n)
		r := bytes.NewReader(p[:])
		if _, err := ReadFrame(r); err == nil || r.Len() != 0 {
			t.Fatal("length accepted", n)
		}
	}
	hello.WorkerId = strings.Repeat("x", MaxControlBytes-64)
	hello.WorkerId += strings.Repeat("x", MaxControlBytes-proto.Size(frame))
	if proto.Size(frame) != MaxControlBytes {
		t.Fatal("bad exact boundary fixture")
	}
	if err := WriteFrame(&b, frame); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFrame(&b); err != nil {
		t.Fatal(err)
	}
	hello.WorkerId += "x"
	if err := WriteFrame(&b, frame); err == nil || b.Len() != 0 {
		t.Fatal("oversize emitted")
	}
}
func TestTruncatedAndInvalid(t *testing.T) {
	for _, data := range [][]byte{{0, 0}, {0, 0, 0, 2, 255}, {0, 0, 0, 1, 255}, {0, 0, 0, 2, 0x20, 1}} {
		if _, err := ReadFrame(bytes.NewReader(data)); err == nil {
			t.Fatal("invalid accepted", data)
		}
	}
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > 3 {
		p = p[:3]
	}
	return w.Buffer.Write(p)
}

type zeroWriter struct{}

func (zeroWriter) Write(p []byte) (int, error) { return 0, nil }
func TestShortWrites(t *testing.T) {
	f := &Frame{Payload: &Frame_Hello{Hello: &Hello{Protocol: Protocol}}}
	w := new(shortWriter)
	if err := WriteFrame(w, f); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFrame(&w.Buffer); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(zeroWriter{}, f); err != io.ErrShortWrite {
		t.Fatal(err)
	}
}
