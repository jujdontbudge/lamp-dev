package network

import (
	"bytes"
	"encoding/json"
	"io"
)

// DecodeConfig rejects duplicate/null/conflicting selectors before typed
// decoding. Host network config vocabulary is closed lower-case ASCII snake
// case; this also rejects encoding/json's case-insensitive field aliases.
func DecodeConfig(reader io.Reader, out any) error {
	data, err := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return ErrDenied
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value func(int) error
	value = func(depth int) error {
		if depth > 32 {
			return ErrDenied
		}
		token, err := decoder.Token()
		if err != nil || token == nil {
			return ErrDenied
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				seen := map[string]bool{}
				for decoder.More() {
					key, err := decoder.Token()
					if err != nil {
						return ErrDenied
					}
					name, ok := key.(string)
					if !ok || name == "" || seen[name] {
						return ErrDenied
					}
					seen[name] = true
					for _, c := range name {
						if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
							return ErrDenied
						}
					}
					if err := value(depth + 1); err != nil {
						return err
					}
				}
				end, err := decoder.Token()
				if err != nil || end != json.Delim('}') {
					return ErrDenied
				}
			case '[':
				for decoder.More() {
					if err := value(depth + 1); err != nil {
						return err
					}
				}
				end, err := decoder.Token()
				if err != nil || end != json.Delim(']') {
					return ErrDenied
				}
			default:
				return ErrDenied
			}
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrDenied
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return ErrDenied
	}
	return nil
}
