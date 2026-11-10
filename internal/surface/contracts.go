// Package surface binds transports to the same canonical operation inventory.
package surface

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	public "github.com/JohanSardar/lamp/contracts/public"
	"github.com/JohanSardar/lamp/pkg/api"
)

var definitions map[string]map[string]any
var ByName = map[string]api.OperationDescriptor{}
var exactRoutes = map[string]api.OperationDescriptor{}
var patternedRoutes = map[string][]api.OperationDescriptor{}

func init() {
	var doc struct {
		Defs map[string]map[string]any `json:"$defs"`
	}
	d := json.NewDecoder(bytes.NewReader(public.Schema))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil {
		panic(err)
	}
	definitions = doc.Defs
	for _, op := range api.Operations {
		ByName[op.Name] = op
	}
	for _, op := range api.Operations {
		method := strings.ToLower(op.Method)
		if !strings.Contains(op.Path, "{") {
			exactRoutes[method+" "+op.Path] = op
		} else {
			parts := strings.Split(op.Path, "/")
			key := method + " " + parts[2]
			patternedRoutes[key] = append(patternedRoutes[key], op)
		}
	}

}

func Definition(name string) map[string]any { return definitions[name] }
func Decode(data []byte) (map[string]any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := unique(d); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("expected one JSON object")
	}
	var v map[string]any
	d = json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	err := d.Decode(&v)
	if v == nil && err == nil {
		err = fmt.Errorf("expected JSON object")
	}
	return v, err
}
func unique(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	if t == nil {
		return nil
	}
	if v, ok := t.(json.Delim); ok {
		seen := map[string]bool{}
		for d.More() {
			if v == '{' {
				k, e := d.Token()
				if e != nil {
					return e
				}
				s, ok := k.(string)
				if !ok || seen[s] {
					return fmt.Errorf("duplicate field")
				}
				seen[s] = true
			}
			if e := unique(d); e != nil {
				return e
			}
		}
		_, e = d.Token()
		return e
	}
	return nil
}
func Validate(name string, v any) error { return validate(definitions[name], v, name) }
func validate(s map[string]any, v any, path string) error {
	if branches, ok := s["allOf"].([]any); ok {
		for _, branch := range branches {
			if err := validate(branch.(map[string]any), v, path); err != nil {
				return err
			}
		}
	}
	if condition, ok := s["if"].(map[string]any); ok {
		branch := "else"
		if validate(condition, v, path) == nil {
			branch = "then"
		}
		if rule, ok := s[branch].(map[string]any); ok {
			if err := validate(rule, v, path); err != nil {
				return err
			}
		}
	}
	if s["type"] == nil && s["properties"] != nil {
		copy := map[string]any{}
		for k, v := range s {
			copy[k] = v
		}
		copy["type"] = "object"
		return validate(copy, v, path)
	}

	if req, ok := s["required"].([]any); ok {
		if m, ok := v.(map[string]any); ok {
			for _, k := range req {
				if _, ok := m[k.(string)]; !ok {
					return fmt.Errorf("%s.%s is required", path, k)
				}
			}
		}
	}
	if neg, ok := s["not"].(map[string]any); ok && validate(neg, v, path) == nil {
		return fmt.Errorf("%s: forbidden combination", path)
	}
	if variants, ok := s["anyOf"].([]any); ok {
		found := false
		for _, x := range variants {
			if validate(x.(map[string]any), v, path) == nil {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s: no matching variant", path)
		}
	}
	if ref, ok := s["$ref"].(string); ok {
		return validate(definitions[strings.TrimPrefix(ref, "#/$defs/")], v, path)
	}
	if variants, ok := s["oneOf"].([]any); ok {
		matches := 0
		for _, x := range variants {
			if validate(x.(map[string]any), v, path) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s must match one variant", path)
		}
	}
	if c, ok := s["const"]; ok && !reflect.DeepEqual(c, v) {
		return fmt.Errorf("%s: invalid constant", path)
	}
	if choices, ok := s["enum"].([]any); ok {
		found := false
		for _, c := range choices {
			if reflect.DeepEqual(c, v) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s: invalid value", path)
		}
	}
	fail := func() error { return fmt.Errorf("%s: invalid %v", path, s["type"]) }
	switch s["type"] {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fail()
		}
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, k := range req {
				if _, ok := m[k.(string)]; !ok {
					return fmt.Errorf("%s.%s is required", path, k)
				}
			}
		}

		if dependencies, ok := s["dependentRequired"].(map[string]any); ok {
			for k, deps := range dependencies {
				if _, ok := m[k]; ok {
					for _, dep := range deps.([]any) {
						if _, ok := m[dep.(string)]; !ok {
							return fmt.Errorf("%s.%s requires %s", path, k, dep)
						}
					}
				}
			}
		}
		if max, ok := s["maxProperties"].(json.Number); ok {
			n, _ := max.Int64()
			if int64(len(m)) > n {
				return fail()
			}
		}
		for k, x := range m {
			if p, ok := props[k].(map[string]any); ok {
				if err := validate(p, x, path+"."+k); err != nil {
					return err
				}
			} else if a, ok := s["additionalProperties"].(map[string]any); ok {
				if err := validate(a, x, path+"."+k); err != nil {
					return err
				}
			} else if s["additionalProperties"] == false {
				return fmt.Errorf("%s.%s: unknown field", path, k)
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return fail()
		}
		if max, ok := s["maxItems"].(json.Number); ok {
			n, _ := max.Int64()
			if int64(len(a)) > n {
				return fail()
			}
		}
		for _, x := range a {
			if err := validate(s["items"].(map[string]any), x, path); err != nil {
				return err
			}
		}
	case "string":
		x, ok := v.(string)
		if !ok {
			return fail()
		}
		for _, bound := range []string{"minLength", "maxLength"} {
			if b, ok := s[bound].(json.Number); ok {
				n, _ := b.Int64()
				l := int64(len([]rune(x)))
				if bound == "minLength" && l < n || bound == "maxLength" && l > n {
					return fail()
				}
			}
		}
		if s["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, x); err != nil {
				return fail()
			}
		}
		if p, ok := s["pattern"].(string); ok {
			match, _ := regexp.MatchString(p, x)
			if !match {
				return fail()
			}
		}
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return fail()
		}
		r, ok := new(big.Rat).SetString(n.String())
		if !ok || !r.IsInt() {
			return fail()
		}
		for _, bound := range []string{"minimum", "maximum"} {
			if b, ok := s[bound].(json.Number); ok {
				br, _ := new(big.Rat).SetString(b.String())
				if bound == "minimum" && r.Cmp(br) < 0 || bound == "maximum" && r.Cmp(br) > 0 {
					return fail()
				}
			}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fail()
		}
	}
	return nil
}

// Request maps transport-neutral inputs to canonical headers, path and query.
// No transport may send authority selectors in a REST body.
func Request(op api.OperationDescriptor, in map[string]any) (string, map[string]string, []byte, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return "", nil, nil, err
	}
	in, err = Decode(data)
	if err != nil {
		return "", nil, nil, err
	}
	if err = Validate(op.InputSchema, in); err != nil {
		return "", nil, nil, err
	}
	path := op.Path
	headers := map[string]string{}
	for k, v := range in {
		placeholder := "{" + k + "}"
		if strings.Contains(path, placeholder) {
			s, ok := v.(string)
			if !ok || s == "" || strings.ContainsAny(s, "/\x00") {
				return "", nil, nil, fmt.Errorf("invalid path selector")
			}
			path = strings.ReplaceAll(path, placeholder, url.PathEscape(s))
			delete(in, k)
		}
	}
	if strings.Contains(path, "{") {
		return "", nil, nil, fmt.Errorf("missing resource ID")
	}
	if op.Mutation {
		for k, h := range map[string]string{"idempotency_key": "Idempotency-Key", "expected_revision": "If-Match"} {
			if v, ok := in[k]; ok {
				headers[h] = fmt.Sprint(v)
				delete(in, k)
			}
		}
	}
	if strings.EqualFold(op.Method, "get") {
		q := url.Values{}
		for k, v := range in {
			q.Set(k, fmt.Sprint(v))
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		return path, headers, nil, nil
	}
	data, err = json.Marshal(in)
	return path, headers, data, err
}
func Match(method, path string) (api.OperationDescriptor, map[string]any, bool) {
	method = strings.ToLower(method)
	if op, ok := exactRoutes[method+" "+path]; ok {
		return op, map[string]any{}, true
	}
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return api.OperationDescriptor{}, nil, false
	}
	candidates := patternedRoutes[method+" "+parts[2]]

	// Exact routes precede placeholder routes.
	for _, op := range candidates {
		if !strings.EqualFold(method, op.Method) {
			continue
		}
		a, b := strings.Split(path, "/"), strings.Split(op.Path, "/")
		if len(a) != len(b) {
			continue
		}
		in := map[string]any{}
		match := true
		for i, part := range b {
			if start := strings.IndexByte(part, '{'); start >= 0 {
				end := strings.IndexByte(part, '}')
				prefix, suffix := part[:start], part[end+1:]
				if !strings.HasPrefix(a[i], prefix) || !strings.HasSuffix(a[i], suffix) || len(a[i]) <= len(prefix)+len(suffix) {
					match = false
					break
				}
				in[part[start+1:end]] = a[i][len(prefix) : len(a[i])-len(suffix)]
			} else if a[i] != part {
				match = false
				break
			}
		}
		if match {
			return op, in, true
		}
	}
	return api.OperationDescriptor{}, nil, false
}
func Query(op api.OperationDescriptor, q url.Values, in map[string]any) error {
	props, _ := Definition(op.InputSchema)["properties"].(map[string]any)
	for k, values := range q {
		p, ok := props[k].(map[string]any)
		if !ok || len(values) != 1 {
			return fmt.Errorf("invalid query field %s", k)
		}
		if _, exists := in[k]; exists {
			return fmt.Errorf("duplicate selector")
		}
		if p["type"] == "integer" {
			if _, err := strconv.ParseUint(values[0], 10, 64); err != nil {
				return err
			}
			in[k] = json.Number(values[0])
		} else {
			in[k] = values[0]
		}
	}
	return Validate(op.InputSchema, in)
}
