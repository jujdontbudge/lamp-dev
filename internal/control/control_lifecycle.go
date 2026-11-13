package control

import "time"

func (s *Service) planJobDelete(a caller, in map[string]any) (lifePlan, error) {
	if err := onlyFields(in, "job_id"); err != nil {
		return lifePlan{}, err
	}
	m, err := s.ownedOrMissing(a, "job", textField(in, "job_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if !ciTerminal(textField(m, "state")) {
		return lifePlan{}, reject("RESOURCE_IN_USE", "cancel or reconcile the active job before deletion")
	}
	if sb := textField(m, "sandbox_id"); sb != "" && s.journal.Sandboxes[sb].State != "deleted" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "delete the job sandbox before deleting its record")
	}
	delete(s.life.Jobs, textField(m, "id"))
	return lifePlan{ref: lifeRef("job", textField(m, "id")), result: map[string]any{}}, nil
}
func (s *Service) planArtifactUpdate(a caller, in map[string]any) (lifePlan, error) {
	if err := onlyFields(in, "artifact_id expires_at"); err != nil {
		return lifePlan{}, err
	}
	m, err := s.ownedOrMissing(a, "artifact", textField(in, "artifact_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if expiry, ok := in["expires_at"]; ok {
		when, err := time.Parse(time.RFC3339Nano, expiry.(string))
		if err != nil || !time.Now().Before(when) {
			return lifePlan{}, reject("INVALID_ARGUMENT", "artifact expiry must be in the future")
		}
		m["expires_at"] = expiry
	}
	applyLabels(m, in)
	editResource(m)
	return lifePlan{ref: lifeRef("artifact", textField(m, "id")), result: map[string]any{}}, nil
}
