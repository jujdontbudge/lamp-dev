package control

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"lamp.eilmer.si/pkg/api"
)

// Installed templates are host enrollment, not user-created saved captures.
// Their signed seed was verified before the engine built and probed them.
func (s *Service) loadInstalledTemplate() error {
	if s.installDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(s.installDir, "config", "default-template.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var t api.Template
	if err := json.Unmarshal(raw, &t); err != nil {
		return err
	}
	if t.Id == "" || t.State != "ready" || t.DefaultResources == nil {
		return fmt.Errorf("invalid installed default template")
	}
	s.installedTemplate = &t
	return nil
}
