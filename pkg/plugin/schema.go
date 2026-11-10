package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// Schema is the deliberately closed lamp.plugin/1 schema vocabulary, not an
// unrestricted JSON Schema interpreter. Unknown keywords fail manifest decoding.
// Object schemas always reject additional properties; no external references.
type Schema struct {
	Type       string            `json:"type"`
	Properties map[string]Schema `json:"properties,omitempty"`
	Required   []string          `json:"required,omitempty"`
	Items      *Schema           `json:"items,omitempty"`
	Enum       []json.RawMessage `json:"enum,omitempty"`
	MaxLength  int               `json:"maxLength,omitempty"`
	MaxItems   int               `json:"maxItems,omitempty"`
	Minimum    *float64          `json:"minimum,omitempty"`
	Maximum    *float64          `json:"maximum,omitempty"`
}

func (s *Schema) UnmarshalJSON(raw []byte) error {
	type plain Schema
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return errors.New("schema must be an object")
	}
	for key, raw := range fields {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("null schema keyword: %s", key)
		}
	}
	*s = Schema(value)
	return nil
}

func (s Schema) check(depth int) error {
	if depth > 16 {
		return errors.New("schema nesting limit")
	}
	switch s.Type {
	case "object", "array", "string", "boolean", "integer", "number", "null":
	default:
		return errors.New("unsupported schema type")
	}
	if s.Enum != nil && len(s.Enum) == 0 {
		return errors.New("enum must contain at least one value")
	}
	if s.MaxLength < 0 || s.MaxItems < 0 {
		return errors.New("negative schema limit")
	}
	if (s.Minimum != nil && (math.IsNaN(*s.Minimum) || math.IsInf(*s.Minimum, 0))) || (s.Maximum != nil && (math.IsNaN(*s.Maximum) || math.IsInf(*s.Maximum, 0))) {
		return errors.New("invalid numeric bound")
	}
	if s.Minimum != nil && s.Maximum != nil && *s.Minimum > *s.Maximum {
		return errors.New("inverted numeric bounds")
	}
	if s.Type == "array" && s.Items == nil {
		return errors.New("array requires items")
	}
	seen := map[string]bool{}
	for _, k := range s.Required {
		if _, ok := s.Properties[k]; !ok || seen[k] {
			return errors.New("invalid required property")
		}
		seen[k] = true
	}
	for _, v := range s.Properties {
		if err := v.check(depth + 1); err != nil {
			return err
		}
	}
	if s.Items != nil {
		if err := s.Items.check(depth + 1); err != nil {
			return err
		}
	}
	for _, v := range s.Enum {
		if err := StrictJSON(v); err != nil {
			return err
		}
	}
	return nil
}
func (s Schema) Validate(raw json.RawMessage) error {
	if len(raw) > MaxFrameBytes {
		return errors.New("schema value too large")
	}
	if err := s.check(0); err != nil {
		return err
	}
	if err := StrictJSON(raw); err != nil {
		return err
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return err
	}
	return s.value(value)
}
func (s Schema) value(v any) error {
	if len(s.Enum) > 0 {
		matched := false
		b, _ := json.Marshal(v)
		for _, e := range s.Enum {
			var x any
			d := json.NewDecoder(bytes.NewReader(e))
			d.UseNumber()
			if d.Decode(&x) == nil {
				c, _ := json.Marshal(x)
				matched = matched || bytes.Equal(b, c)
			}
		}
		if !matched {
			return errors.New("value not in enum")
		}
	}
	bad := func() error { return fmt.Errorf("value does not match %s schema", s.Type) }
	switch s.Type {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return bad()
		}
		for _, k := range s.Required {
			if _, ok := m[k]; !ok {
				return bad()
			}
		}
		for k, v := range m {
			child, ok := s.Properties[k]
			if !ok {
				return bad()
			}
			if err := child.value(v); err != nil {
				return err
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok || s.Items == nil || (s.MaxItems > 0 && len(a) > s.MaxItems) {
			return bad()
		}
		for _, x := range a {
			if err := s.Items.value(x); err != nil {
				return err
			}
		}
	case "string":
		x, ok := v.(string)
		if !ok || (s.MaxLength > 0 && utf8.RuneCountInString(x) > s.MaxLength) {
			return bad()
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return bad()
		}
	case "null":
		if v != nil {
			return bad()
		}
	case "integer", "number":
		x, ok := v.(json.Number)
		if !ok {
			return bad()
		}
		n, err := x.Float64()
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) || (s.Type == "integer" && math.Trunc(n) != n) || (s.Minimum != nil && n < *s.Minimum) || (s.Maximum != nil && n > *s.Maximum) {
			return bad()
		}
	}
	return nil
}
