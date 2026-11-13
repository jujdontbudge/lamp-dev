package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

// Telemetry is best-effort observation, kept separately from the authority journal.
// Counters are guest kernel values, never requested resource limits.
type observations struct {
	Samples    map[string][]metricSample `json:"samples"`
	EngineLogs map[string]string         `json:"engine_logs"`
}

type metricSample struct {
	ObservedAt string            `json:"observed_at"`
	Generation uint64            `json:"generation"`
	BootID     string            `json:"boot_id,omitempty"`
	Counters   map[string]uint64 `json:"counters"`
	CPUPercent *float64          `json:"cpu_percent,omitempty"`
	Error      string            `json:"error,omitempty"`
}

func (s *Service) observeSandboxes() {
	defer s.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		s.readLock()
		sandboxes := []api.Sandbox{}
		for _, sb := range s.journal.Sandboxes {
			if sb.State == "ready" {
				sandboxes = append(sandboxes, sb)
			}
		}
		s.mu.Unlock()
		for _, sb := range sandboxes {
			ctx, cancel := context.WithTimeout(s.context(), 3*time.Second)
			deadline, _ := ctx.Deadline()
			receipt, err := s.worker.call(ctx, &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: newID("op"), DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: sb.Id, Generation: value(sb.Generation), GrantId: s.worker.config.GrantID, PolicyRevision: 1}, Command: &pb.Request_Inspect{Inspect: &pb.Inspect{}}})
			cancel()
			sample := metricSample{ObservedAt: timestamp(), Generation: value(sb.Generation), Counters: map[string]uint64{}}
			if err != nil {
				sample.Error = "engine metrics unavailable"
			} else {
				sample.BootID = receipt.Detail["metrics_boot_id"]
				if json.Unmarshal([]byte(receipt.Detail["metrics"]), &sample.Counters) != nil || len(sample.Counters) == 0 {
					sample.Error = "guest metrics unavailable; update the guest helper/template"
				}
			}
			s.readLock()
			history := s.metrics[sb.Id]
			if len(history) > 0 && sample.Error == "" {
				prev := history[len(history)-1]
				now, _ := time.Parse(time.RFC3339Nano, sample.ObservedAt)
				then, _ := time.Parse(time.RFC3339Nano, prev.ObservedAt)
				current, ok := sample.Counters["cpu_usage_usec"]
				old, had := prev.Counters["cpu_usage_usec"]
				if ok && had && sample.BootID == prev.BootID && sample.Generation == prev.Generation && current >= old && now.After(then) {
					percent := float64(current-old) / float64(now.Sub(then).Microseconds()) * 100
					sample.CPUPercent = &percent // 100% is one fully busy core.
				}
			}
			history = append(history, sample)
			if len(history) > 720 {
				history = history[len(history)-720:]
			}
			s.metrics[sb.Id] = history
			if receipt != nil && receipt.Detail["boot_log"] != "" {
				s.engineLogs[sb.Id] = receipt.Detail["boot_log"]
			}
			s.mu.Unlock()
		}
		s.readLock()
		cutoff := time.Now().Add(-time.Hour)
		for id, history := range s.metrics {
			first := 0
			for first < len(history) {
				at, _ := time.Parse(time.RFC3339Nano, history[first].ObservedAt)
				if at.After(cutoff) {
					break
				}
				first++
			}
			if first == len(history) {
				delete(s.metrics, id)
				delete(s.engineLogs, id)
			} else {
				s.metrics[id] = history[first:]
			}
		}
		raw, err := json.Marshal(observations{Samples: s.metrics, EngineLogs: s.engineLogs})
		if err == nil { // Atomic best-effort replacement; an interrupted sample never damages authority.
			tmp := filepath.Join(s.dir, "metrics.json.tmp")
			if os.WriteFile(tmp, raw, 0600) == nil {
				_ = os.Rename(tmp, filepath.Join(s.dir, "metrics.json"))
			}
		}
		s.mu.Unlock()
		select {
		case <-s.context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) observabilityHTTP(w http.ResponseWriter, r *http.Request, id string, op api.OperationDescriptor, in map[string]any) {
	q, err := parseQuery(r)
	if err != nil || surface.Query(op, q, in) != nil {
		writeError(w, id, "INVALID_ARGUMENT", "invalid query parameters", nil)
		return
	}
	s.readLock()
	var send func()
	defer func() {
		s.mu.Unlock()
		if send != nil {
			send()
		}
	}()
	if s.broken {
		writeError(w, id, "DATA_UNAVAILABLE", "journal unavailable", nil)
		return
	}
	a := requestCaller(r)
	sb, ok := s.journal.Sandboxes[textField(in, "sandbox_id")]
	if !ok || !s.can(a, op.Scope) || !s.sandboxAllowed(a, sb) {
		writeError(w, id, "NOT_FOUND", "sandbox not found", nil)
		return
	}
	limit := uintField(in, "limit")
	if limit == 0 {
		limit = 60
	}
	if limit > 720 {
		writeError(w, id, "INVALID_ARGUMENT", "limit must be 1..720", nil)
		return
	}
	if op.Name == "sandbox_metrics" {
		history := s.metrics[sb.Id]
		if len(history) > int(limit) {
			history = history[len(history)-int(limit):]
		}
		if history == nil {
			history = []metricSample{}
		}
		send = prepareJSON(w, 200, map[string]any{"items": history, "request_id": id})
		return
	}
	logs := []map[string]any{}
	if text := s.engineLogs[sb.Id]; text != "" {
		logs = append(logs, map[string]any{"occurred_at": sb.CreatedAt, "source": "boot/engine", "message": text})
	}
	for _, stored := range s.journal.Events {
		e := stored.Event
		include := e.ResourceKind == "sandbox" && e.ResourceId == sb.Id
		if e.ResourceKind == "exec" {
			ex, exists := s.journal.Execs[e.ResourceId]
			include = exists && value(ex.SandboxId) == sb.Id && s.execAllowed(a, ex)
		}
		if e.ResourceKind == "operation" {
			rec := s.journal.Operations[e.ResourceId]
			include = rec != nil && rec.Resource.Kind == "sandbox" && rec.Resource.Id == sb.Id && s.operationAllowed(a, rec)
		}
		if include {
			logs = append(logs, map[string]any{"occurred_at": e.OccurredAt, "source": e.ResourceKind, "resource_id": e.ResourceId, "message": string(e.Type), "data": e.Data})
		}
	}
	for _, rec := range s.journal.Operations {
		if rec.Resource.Kind == "sandbox" && rec.Resource.Id == sb.Id && rec.Operation.Error != nil && s.operationAllowed(a, rec) {
			logs = append(logs, map[string]any{"occurred_at": rec.Operation.UpdatedAt, "source": "engine", "message": rec.Operation.Error.Message})
		}
	}
	if s.can(a, "exec.watch") {
		for exID, ex := range s.journal.Execs {
			if value(ex.SandboxId) != sb.Id || !s.execAllowed(a, ex) {
				continue
			}
			for _, frame := range s.journal.Outputs[exID] {
				data, _ := base64.StdEncoding.DecodeString(frame.DataBase64)
				logs = append(logs, map[string]any{"occurred_at": ex.UpdatedAt, "source": frame.Channel, "resource_id": exID, "message": string(data)})
			}
		}
	}
	sort.SliceStable(logs, func(i, j int) bool { return logs[i]["occurred_at"].(string) < logs[j]["occurred_at"].(string) })
	if len(logs) > int(limit) {
		logs = logs[len(logs)-int(limit):]
	}
	// Keep bounded tails inside the CLI's bounded response reader, even when
	// many commands each produced a full output tail.
	first, encoded := len(logs), 0
	for first > 0 {
		raw, _ := json.Marshal(logs[first-1])
		if encoded+len(raw) > 512<<10 {
			break
		}
		encoded += len(raw)
		first--
	}
	logs = logs[first:]
	send = prepareJSON(w, 200, map[string]any{"items": logs, "request_id": id})
}
