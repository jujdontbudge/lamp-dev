package control

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

const execSlot = 24576 + 13

func (s *Service) execIO(ctx context.Context, sandbox string, generation uint64, command string, in map[string]any) (map[string]any, error) {
	in["sandbox_id"], in["generation"], in["exec_id"] = sandbox, generation, command
	bytes, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	request := s.workerRequest(ctx, "io", sandbox, generation)
	request.Command = &pb.Request_SessionCall{SessionCall: &pb.SessionCall{RequestJson: bytes, BridgeSource: "lamp-exec-io/1"}}
	receipt, err := s.worker.call(ctx, request)
	if err != nil {
		return nil, err
	}
	if receipt.State != "session_reply" {
		return nil, fmt.Errorf("command I/O unavailable")
	}
	var result map[string]any
	err = decodeNumbers([]byte(receipt.Detail["response_json"]), &result)
	return result, err
}

func (s *Service) execOutputPath(id string) (string, bool) {
	p := filepath.Join(s.dir, "exec-output", id)
	_, err := os.Stat(p)
	return p, err == nil
}
func (s *Service) openExecOutput(id string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Join(s.dir, "exec-output"), 0700); err != nil {
		return nil, err
	}
	p, _ := s.execOutputPath(id)
	return os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
}
func (s *Service) appendExecOutput(file *os.File, frame api.Frame) error {
	bytes, err := base64.StdEncoding.DecodeString(frame.DataBase64)
	if err != nil {
		return err
	}
	slot := make([]byte, execSlot)
	if frame.Channel == api.FrameChannelStderr {
		slot[0] = 1
	}
	offset, _ := strconv.ParseUint(frame.ByteOffset, 10, 64)
	binary.BigEndian.PutUint64(slot[1:9], offset)
	binary.BigEndian.PutUint32(slot[9:13], uint32(len(bytes)))
	copy(slot[13:], bytes)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = file.Write(slot)
	return err
}
func (s *Service) watchExecSpool(w http.ResponseWriter, r *http.Request, requestID, id string, offset, limit uint64, until time.Time) {
	// A bounded page keeps HTTP and MCP responses within their transport limit.
	if limit > 64 {
		limit = 64
	}
	for {
		s.readLock()
		ex, ok := s.journal.Execs[id]
		if !ok || !s.can(requestCaller(r), "exec.watch") || !s.execAllowed(requestCaller(r), ex) {
			s.mu.Unlock()
			writeError(w, requestID, "NOT_FOUND", "exec not found", nil)
			return
		}
		p, _ := s.execOutputPath(id)
		file, err := os.Open(p)
		if err != nil {
			s.mu.Unlock()
			writeError(w, requestID, "DATA_UNAVAILABLE", "exec output unavailable", nil)
			return
		}
		st, err := file.Stat()
		if err != nil {
			file.Close()
			s.mu.Unlock()
			writeError(w, requestID, "DATA_UNAVAILABLE", "exec output unavailable", nil)
			return
		}
		count := uint64(st.Size() / execSlot)
		more := ex.State == "running" || ex.State == "accepted"
		if offset > count {
			file.Close()
			s.mu.Unlock()
			writeError(w, requestID, "INVALID_ARGUMENT", "output cursor beyond end", nil)
			return
		}
		if more && offset == count && time.Now().Before(until) {
			file.Close()
			s.mu.Unlock()
			timer := time.NewTimer(min(20*time.Millisecond, time.Until(until)))
			select {
			case <-r.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		result := api.ExecWatchResult{Items: []api.Frame{}, RequestId: requestID}
		end := min(count, offset+limit)
		for i := offset; i < end; i++ {
			slot := make([]byte, execSlot)
			if _, err = file.ReadAt(slot, int64(i)*execSlot); err != nil {
				break
			}
			n := binary.BigEndian.Uint32(slot[9:13])
			if n > 24576 {
				err = io.ErrUnexpectedEOF
				break
			}
			channel := api.FrameChannelStdout
			if slot[0] == 1 {
				channel = api.FrameChannelStderr
			}
			result.Items = append(result.Items, api.Frame{StreamId: id + "." + string(channel), Channel: channel, Sequence: binary.BigEndian.Uint64(slot[1:9]), ByteOffset: strconv.FormatUint(binary.BigEndian.Uint64(slot[1:9]), 10), DataBase64: base64.StdEncoding.EncodeToString(slot[13 : 13+n]), LocationEpoch: 1})
		}
		file.Close()
		if more || end < count {
			result.NextCursor = pointer(id + ":" + strconv.FormatUint(end, 10))
		}
		s.mu.Unlock()
		if err != nil {
			writeError(w, requestID, "DATA_UNAVAILABLE", "exec output read failed", nil)
			return
		}
		prepareJSON(w, 200, result)()
		return
	}
}

func init() {
	lifeHandlers["exec_interrupt"] = (*Service).planExecIO
	lifeHandlers["exec_input"] = (*Service).planExecIO
	lifeHandlers["exec_resize"] = (*Service).planExecResize
}
func (s *Service) planExecResize(a caller, in map[string]any) (lifePlan, error) {
	if uintField(in, "rows") == 0 || uintField(in, "rows") > 65535 || uintField(in, "columns") == 0 || uintField(in, "columns") > 65535 {
		return lifePlan{}, reject("INVALID_ARGUMENT", "PTY resize requires rows and columns in 1..65535")
	}
	return s.planExecIO(a, in)
}

func (s *Service) planExecIO(a caller, in map[string]any) (lifePlan, error) {
	id := textField(in, "exec_id")
	ex, ok := s.journal.Execs[id]
	if !ok || !s.execAllowed(a, ex) {
		return lifePlan{}, reject("NOT_FOUND", "exec not found")
	}
	if ex.Revision != uintField(in, "expected_revision") {
		return lifePlan{}, reject("REVISION_CONFLICT", "exec revision changed")
	}
	if ex.State != "running" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "exec is not running")
	}
	sb := s.journal.Sandboxes[value(ex.SandboxId)]
	if sb.State != "ready" || s.busy[sb.Id] {
		return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox is not ready")
	}
	args := map[string]any{}
	if input, exists := in["data_base64"]; exists {
		data, err := base64.StdEncoding.DecodeString(fmt.Sprint(input))
		if err != nil || len(data) > 4096 || (len(data) == 0 && in["eof"] != true) {
			return lifePlan{}, reject("INVALID_ARGUMENT", "terminal input must be 1..4096 bytes of base64")
		}
		args["input_b64"] = input
		args["input_eof"] = in["eof"] == true
	} else if _, exists := in["rows"]; exists {
		args["rows"], args["columns"] = in["rows"], in["columns"]
	} else {
		signal := uintField(in, "signal")
		if signal == 0 {
			signal = 2
		}
		if signal != 2 && signal != 9 && signal != 15 {
			return lifePlan{}, reject("INVALID_ARGUMENT", "signal must be 2, 9 or 15")
		}
		args["signal"] = signal
	}
	command := ""
	for opID, record := range s.journal.Operations {
		if record.Resource.Id == id && value(record.Operation.OperationName) == "exec_create" {
			command = opID + ".start"
			break
		}
	}
	if command == "" {
		return lifePlan{}, reject("DATA_UNAVAILABLE", "command identity unavailable")
	}
	return lifePlan{ref: api.ResourceRef{Kind: "exec", Id: id}, run: func(ctx context.Context) (map[string]any, error) {
		_, err := s.execIO(ctx, sb.Id, value(sb.Generation), command, args)
		return map[string]any{}, err
	}}, nil
}
