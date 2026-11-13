package control

import (
	"context"
	"encoding/json"
	"time"

	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

// Read the enrolled worker, rather than guessing support from the daemon OS.
func (s *Service) workerEngine() (*pb.Receipt, []string, error) {
	ctx, cancel := context.WithTimeout(s.context(), 2*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	receipt, err := s.worker.call(ctx, &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: newID("op"), DeadlineMs: uint64(deadline.UnixMilli()), GrantId: s.worker.config.GrantID, PolicyRevision: 1}, Command: &pb.Request_Inspect{Inspect: &pb.Inspect{}}})
	if err != nil {
		return nil, nil, err
	}
	var supported []string
	if json.Unmarshal([]byte(receipt.Detail["request_devices"]), &supported) != nil {
		return nil, nil, reject("DATA_UNAVAILABLE", "worker does not advertise device support; update the worker")
	}
	return receipt, supported, nil
}

func (s *Service) localEngine() (map[string]any, []string, error) {
	receipt, supported, err := s.workerEngine()
	if err != nil {
		return nil, nil, err
	}
	devices := []api.Device{}
	for _, kind := range supported {
		devices = append(devices, api.Device{Id: kind, Type: api.DeviceType(kind)})
	}
	now := timestamp()
	item := map[string]any{"id": receipt.Detail["engine"], "kind": "engine", "fleet_id": "fl_local", "owner_id": s.journal.Control.OwnerID, "revision": 1, "created_at": now, "updated_at": now, "state": "active", "labels": map[string]string{"device_requests": "only listed devices are accepted; Mac optional devices come from template configuration"}, "visibility": "private", "host_id": s.hostID(), "boundary": "vm", "devices": devices, "digest": receipt.Detail["engine_digest"]}
	return item, supported, nil
}

func (s *Service) admitDevices(devices *[]api.Device) error {
	if devices == nil || len(*devices) == 0 {
		return nil
	}
	_, supported, err := s.workerEngine()
	if err != nil {
		return err
	}
	for _, device := range *devices {
		found := false
		for _, kind := range supported {
			found = found || string(device.Type) == kind
		}
		if !found {
			return reject("DEVICE_UNAVAILABLE", "the enrolled engine cannot attach requested device "+string(device.Type)+"; see lamp engine list")
		}
	}
	return nil
}
