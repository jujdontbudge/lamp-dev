package plugin_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/JohanSardar/lamp/pkg/plugin"
)

func TestWireBoundary(t *testing.T) {
	var b bytes.Buffer
	want := json.RawMessage(`{"jsonrpc":"2.0","id":"1","result":{"ok":true}}`)
	if err := plugin.WriteFrame(&b, want); err != nil {
		t.Fatal(err)
	}
	got, err := plugin.ReadFrame(&b)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("roundtrip: %s %v", got, err)
	}
	for _, n := range []uint32{0, plugin.MaxFrameBytes + 1, ^uint32(0)} {
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], n)
		if _, err := plugin.ReadFrame(bytes.NewReader(prefix[:])); err == nil {
			t.Fatalf("accepted size %d", n)
		}
	}
	if _, err := plugin.ReadFrame(bytes.NewReader([]byte{0, 0, 0, 5, '{'})); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncation: %v", err)
	}
	for _, raw := range []string{`{`, `{"id":1,"id":2}`, `{"nested":{"x":1,"x":2}}`, `{"n":NaN}`, `{"x":1} {"x":2}`} {
		b.Reset()
		if err := plugin.WriteFrame(&b, json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted ambiguous JSON %s", raw)
		}
	}
}

func TestExactFrameCeiling(t *testing.T) {
	raw := json.RawMessage(`"` + strings.Repeat("x", plugin.MaxFrameBytes-2) + `"`)
	var b bytes.Buffer
	if err := plugin.WriteFrame(&b, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.ReadFrame(&b); err != nil {
		t.Fatal(err)
	}
	raw = append(raw[:len(raw)-1], 'x', '"')
	if err := plugin.WriteFrame(&b, raw); err == nil || b.Len() != 0 {
		t.Fatal("oversize frame partially written")
	}
}
func FuzzReadFrame(f *testing.F) {
	f.Add([]byte{0, 0, 0, 2, '{', '}'})
	f.Add([]byte{0, 16, 0, 1})
	f.Add([]byte("bad"))
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = plugin.ReadFrame(bytes.NewReader(data)) })
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(b []byte) (int, error) {
	if len(b) > 3 {
		b = b[:3]
	}
	return w.Buffer.Write(b)
}
func TestFrameShortWrites(t *testing.T) {
	w := new(shortWriter)
	if err := plugin.WriteFrame(w, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.ReadFrame(&w.Buffer); err != nil {
		t.Fatal(err)
	}
}
