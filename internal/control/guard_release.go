package control

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// A deleted sandbox's guard audit spools stay on the host until Lamp has
// captured their records into its own state directory and the trusted release
// command has removed them. Only then does the sandbox stop holding a retained
// spool reservation. The `.released` marker is written last, so a crash at any
// point keeps the slot held and the next cleanup repeats both steps.
const guardAuditDir = "guard-audit"

func (s *Service) loadReleasedGuardAudit() error {
	s.auditReleased = map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(s.dir, guardAuditDir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".released"); ok {
			s.auditReleased[id] = true
		}
	}
	return nil
}

// releaseGuardAudit captures then removes a deleted sandbox's guard spools.
// Without a configured release command spools are kept and the slot stays held.
func (s *Service) releaseGuardAudit(ctx context.Context, sandbox string) error {
	if len(s.auditRelease) == 0 {
		return nil
	}
	s.readLock()
	done := s.auditReleased[sandbox]
	sb, ok := s.journal.Sandboxes[sandbox]
	s.mu.Unlock()
	if done {
		return nil
	}
	if !ok || sb.State != "deleted" {
		return errors.New("guard audit is released only after sandbox deletion")
	}
	run := func(action string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		argv := append(append([]string{}, s.auditRelease...), action, sandbox)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		if out.Len() > 1<<30 {
			return nil, errors.New("guard audit capture too large")
		}
		return out.Bytes(), err
	}
	records, err := run("capture")
	if err != nil {
		return errors.New("guard audit capture failed")
	}
	dir := filepath.Join(s.dir, guardAuditDir)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err = writeDurable(dir, sandbox+".ndjson", records); err != nil {
		return err
	}
	if _, err = run("remove"); err != nil {
		return errors.New("guard audit spool removal failed")
	}
	if err = writeDurable(dir, sandbox+".released", nil); err != nil {
		return err
	}
	s.readLock()
	s.auditReleased[sandbox] = true
	s.mu.Unlock()
	return nil
}

// capturedGuardAudit returns a released sandbox's captured records.
func (s *Service) capturedGuardAudit(sandbox string) ([]byte, bool) {
	s.readLock()
	done := s.auditReleased[sandbox]
	s.mu.Unlock()
	if !done {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(s.dir, guardAuditDir, sandbox+".ndjson"))
	return data, err == nil
}

func writeDurable(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if e := tmp.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// sweepGuardAudit releases the spools of every deleted sandbox still holding a
// reservation, including sandboxes deleted before release was configured or
// outside CI. Failures are retried on the next pass.
func (s *Service) sweepGuardAudit() {
	defer s.wg.Done()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		var held []string
		s.readLock()
		for id, sb := range s.journal.Sandboxes {
			if sb.State == "deleted" && !s.auditReleased[id] {
				held = append(held, id)
			}
		}
		s.mu.Unlock()
		for _, id := range held {
			if s.context().Err() != nil {
				return
			}
			s.releaseGuardAudit(s.context(), id)
		}
		select {
		case <-s.context().Done():
			return
		case <-tick.C:
		}
	}
}
