package control

import (
	"context"
	"net/http"
	"time"

	"lamp.eilmer.si/internal/buildversion"
)

func (s *Service) versionHTTP(w http.ResponseWriter, id string) {
	engines := []map[string]any{}
	worker := &s.worker
	worker.mu.Lock()
	if worker.config.Socket != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := worker.connect(ctx)
		cancel()
		engine := map[string]any{"worker_id": worker.config.WorkerID, "connected": err == nil, "version": nil}
		if err == nil && worker.version != "" {
			engine["version"] = worker.version
		}
		engines = append(engines, engine)
	}
	worker.mu.Unlock()
	prepareJSON(w, 200, map[string]any{"version": buildversion.Current(), "api_version": "v1", "protocol": "lamp.worker/2", "engines": engines, "request_id": id})()
}
