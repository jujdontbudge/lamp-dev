package surface

import (
	"context"
	"encoding/json"
	"fmt"
)

// CreateDefaults resolves only the host-enrolled default, through the public
// template API. Explicit template/recipe selections retain their own defaults.
func (c *Client) CreateDefaults(ctx context.Context, in map[string]any) error {
	if in["template_id"] != nil || in["recipe_id"] != nil {
		return nil
	}
	data, status, err := c.Call(ctx, ByName["template_list"], map[string]any{})
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("cannot discover default template: %s", data)
	}
	var reply struct {
		Items []struct {
			ID        string            `json:"id"`
			Labels    map[string]string `json:"labels"`
			Resources map[string]any    `json:"default_resources"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return err
	}
	for _, t := range reply.Items {
		if t.Labels["lamp.default"] == "true" {
			in["template_id"] = t.ID
			if in["resources"] == nil {
				in["resources"] = t.Resources
			}
			return nil
		}
	}
	return fmt.Errorf("template_id required; run lamp template list; install the default engine and seed first")
}
