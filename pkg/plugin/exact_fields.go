package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// encoding/json intentionally accepts case-folded struct field names. The wire
// contract does not. Check exact tags before invoking it, including nested and
// embedded records. Map keys and RawMessage contents remain application data.
func checkExactFields(raw []byte, target any) error {
	return exactFields(raw, reflect.TypeOf(target))
}
func exactFields(raw []byte, t reflect.Type) error {
	if t == nil {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeFor[json.RawMessage]() || t == reflect.TypeFor[time.Time]() {
		return nil
	}
	raw = bytes.TrimSpace(raw)
	switch t.Kind() {
	case reflect.Struct:
		if len(raw) == 0 || raw[0] != '{' {
			return nil
		} // ordinary decoding checks the value type
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		fields := map[string]reflect.Type{}
		jsonFields(t, fields)
		for name, value := range object {
			field, ok := fields[name]
			if !ok {
				return fmt.Errorf("unknown case-exact JSON field %q", name)
			}
			if err := exactFields(value, field); err != nil {
				return err
			}
		}
	case reflect.Map:
		if len(raw) == 0 || raw[0] != '{' {
			return nil
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		for _, value := range object {
			if err := exactFields(value, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Array, reflect.Slice:
		if len(raw) == 0 || raw[0] != '[' {
			return nil
		}
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := exactFields(value, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
func jsonFields(t reflect.Type, fields map[string]reflect.Type) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		child := f.Type
		for child.Kind() == reflect.Pointer {
			child = child.Elem()
		}
		if name == "" && f.Anonymous && child.Kind() == reflect.Struct {
			jsonFields(child, fields)
			continue
		}
		if name == "" {
			name = f.Name
		}
		fields[name] = f.Type
	}
}
