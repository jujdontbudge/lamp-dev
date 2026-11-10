// Package plugin hosts separately executed lamp.plugin/1 workers. It does not
// implement public API authorization or private engine RPCs.
package plugin

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const Protocol = "lamp.plugin/1"
const MaxFrameBytes = 1 << 20

// StrictJSON rejects duplicate keys (at every depth), invalid UTF-8, excessive
// nesting and trailing data rather than letting different peers interpret it differently.
func StrictJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return errors.New("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 64 {
			return errors.New("JSON nesting limit")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return err
				}
				s, ok := k.(string)
				if !ok || keys[s] {
					return errors.New("duplicate JSON key")
				}
				keys[s] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func ReadFrame(r io.Reader) (json.RawMessage, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > MaxFrameBytes {
		return nil, errors.New("plugin frame length out of bounds")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	if err := StrictJSON(b); err != nil {
		return nil, err
	}
	return b, nil
}
func WriteFrame(w io.Writer, raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > MaxFrameBytes {
		return errors.New("plugin frame length out of bounds")
	}
	if err := StrictJSON(raw); err != nil {
		return err
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(raw)))
	for _, b := range [][]byte{prefix[:], raw} {
		for len(b) > 0 {
			n, err := w.Write(b)
			if err != nil {
				return err
			}
			if n <= 0 {
				return io.ErrShortWrite
			}
			b = b[n:]
		}
	}
	return nil
}

func requiredFields(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	if err := StrictJSON(raw); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("required object missing")
	}
	for _, key := range keys {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("required field missing: " + key)
		}
	}
	return fields, nil
}

func decode(raw []byte, v any) error {
	if err := StrictJSON(raw); err != nil {
		return err
	}
	if err := checkExactFields(raw, v); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
