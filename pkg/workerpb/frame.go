package workerpb

import (
	"encoding/binary"
	"errors"
	"io"

	"google.golang.org/protobuf/proto"
)

const Protocol = "lamp.worker/1"
const MaxControlBytes = 1 << 20

// ReadFrame rejects an oversized length before allocating or consuming its body.
// The caller owns peer authentication, deadlines and handle/grant validation.
func ReadFrame(r io.Reader) (*Frame, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > MaxControlBytes {
		return nil, errors.New("invalid worker frame length")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	f := new(Frame)
	if err := proto.Unmarshal(data, f); err != nil {
		return nil, err
	}
	if f.Payload == nil {
		return nil, errors.New("missing worker payload")
	}
	return f, nil
}

// WriteFrame never emits a partial oversize frame; the whole protobuf size is
// checked before any bytes are written. Connections must serialize writers.
func WriteFrame(w io.Writer, f *Frame) error {
	if f == nil || f.Payload == nil {
		return errors.New("missing worker payload")
	}
	n := proto.Size(f)
	if n == 0 || n > MaxControlBytes {
		return errors.New("invalid worker frame length")
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(f)
	if err != nil {
		return err
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(n))
	return writeAll(w, append(prefix[:], data...))
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
