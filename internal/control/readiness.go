package control

import "github.com/JohanSardar/lamp/pkg/api"

func coreReadiness(ready bool, at *string) *api.Readiness {
	checks := []api.ReadinessCheck{}
	for _, name := range []string{"template", "engine", "identity", "network", "settings", "kits", "auth", "connections", "command", "agent"} {
		state := api.ReadinessCheckStateSkipped
		switch name {
		case "template", "engine", "identity", "command":
			state = "pending"
			if ready {
				state = "passed"
			}
		}
		checks = append(checks, api.ReadinessCheck{Name: api.ReadinessCheckName(name), State: state})
	}
	return &api.Readiness{CommandStartedAt: at, Checks: &checks}
}
