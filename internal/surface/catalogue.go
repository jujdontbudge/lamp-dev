package surface

import (
	"context"
	"encoding/json"
	"fmt"

	"lamp.eilmer.si/pkg/api"
)

// Catalogue projects the canonical descriptors onto executable handlers.
func Catalogue(installed map[string]bool) []api.OperationDescriptor {
	out := []api.OperationDescriptor{}
	for _, op := range api.Operations {
		if installed[op.Name] {
			out = append(out, op)
		}
	}
	return out
}

// Installed discovers the connected daemon's handlers, including fleet additions.
func (c *Client) Installed(ctx context.Context) (map[string]bool, error) {
	raw, status, err := c.Call(ctx, ByName["docs_read"], map[string]any{"topic": "operations"})
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("operation discovery failed (HTTP %d)", status)
	}
	var result struct {
		Installed map[string]bool `json:"installed_operations"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Installed == nil {
		return nil, fmt.Errorf("daemon did not return installed operations")
	}
	return result.Installed, nil
}

// InstalledOpenAPI retains the common schemas while removing unavailable paths.
func InstalledOpenAPI(raw []byte, installed map[string]bool) []byte {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	paths := doc["paths"].(map[string]any)
	for path, value := range paths {
		methods := value.(map[string]any)
		for method, value := range methods {
			operation, ok := value.(map[string]any)
			if !ok {
				continue
			}
			name, ok := operation["operationId"].(string)
			if ok && !installed[name] {
				delete(methods, method)
			}
		}
		if len(methods) == 0 {
			delete(paths, path)
		}
	}
	data, _ := json.Marshal(doc)
	return data
}
