package control

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JohanSardar/lamp/internal/release"
	"github.com/JohanSardar/lamp/pkg/api"
	"github.com/hashicorp/raft"
)

// Rolling updates (HST-005). Update resources and the fleet update_policy
// live in the quorum-replicated control state, so every host sees the same
// rollout. A rollout moves strictly one host at a time: only the host named
// current_host_id acts. Its lampd hands the release to the local updater
// agent (lamp-installer agent, a separate service so stopping lampd never
// stops the updater), which verifies, drains, applies, checks saved and
// active work and rolls back on failure. The restarted lampd reads the
// agent's result and commits it: completed advances to the next host,
// rolled_back or failed stops the rollout. Automatic updates start disabled;
// when the owner enables them, the coordinator creates an Update for the
// newest verified release on the policy channel.

var updateTick = 2 * time.Second

type installInfo struct {
	Current        string `json:"current"`
	HighestVersion string `json:"highest_version"`
}

type updateRequest struct {
	UpdateID      string `json:"update_id"`
	ReleaseDigest string `json:"release_digest"`
	Version       string `json:"version"`
	Rollback      bool   `json:"rollback,omitempty"`
}

type updateResult struct {
	UpdateID string `json:"update_id"`
	State    string `json:"state"`
	Version  string `json:"version"`
	Detail   string `json:"detail,omitempty"`
}

func (s *Service) installed() (installInfo, error) {
	var in installInfo
	raw, err := os.ReadFile(filepath.Join(s.installDir, "config", "install.json"))
	if err != nil {
		return in, err
	}
	return in, json.Unmarshal(raw, &in)
}

func updateActive(u api.Update) bool {
	return u.State == "pending" || u.State == "draining" || u.State == "applying" || u.State == "checking"
}

func defaultUpdatePolicy() *api.UpdatePolicy {
	return &api.UpdatePolicy{Automatic: pointer(false), MaxUnavailable: pointer(uint64(1)), Channel: pointer("stable"), DrainTimeoutMs: pointer(uint64(60000))}
}

func (c *controlState) updatePolicy() api.UpdatePolicy {
	p := *defaultUpdatePolicy()
	if u := c.Fleet.UpdatePolicy; u != nil {
		if u.Automatic != nil {
			p.Automatic = u.Automatic
		}
		if u.Channel != nil {
			p.Channel = u.Channel
		}
		if u.DrainTimeoutMs != nil {
			p.DrainTimeoutMs = u.DrainTimeoutMs
		}
	}
	return p
}

// setUpdatePolicy validates a fleet_update update_policy.
func setUpdatePolicy(c *controlState, raw any) error {
	data, _ := json.Marshal(raw)
	var p api.UpdatePolicy
	if err := json.Unmarshal(data, &p); err != nil {
		return reject("INVALID_ARGUMENT", "invalid update_policy")
	}
	if p.MaxUnavailable != nil && *p.MaxUnavailable != 1 {
		return reject("INVALID_ARGUMENT", "updates roll one host at a time: max_unavailable must be 1")
	}
	if p.Channel != nil && *p.Channel == "" {
		return reject("INVALID_ARGUMENT", "update channel required")
	}
	merged := c.updatePolicy()
	if p.Automatic != nil {
		merged.Automatic = p.Automatic
	}
	if p.Channel != nil {
		merged.Channel = p.Channel
	}
	if p.DrainTimeoutMs != nil {
		merged.DrainTimeoutMs = p.DrainTimeoutMs
	}
	c.Fleet.UpdatePolicy = &merged
	return nil
}

func (s *Service) enrolledHosts() []string {
	if s.fleet == nil {
		return []string{s.hostID()}
	}
	out := []string{}
	for _, h := range s.fleet.config.Hosts {
		out = append(out, h.ID)
	}
	return out
}

// newUpdate verifies the release by digest before anything is applied.
func (s *Service) newUpdate(owner, digest string, hosts []string) (api.Update, error) {
	var u api.Update
	c := s.journal.Control
	if s.installDir == "" {
		return u, reject("NOT_FOUND", "this host was not installed by lamp-installer; no release mirror")
	}
	for _, v := range c.Updates {
		if updateActive(v) {
			return u, reject("RESOURCE_IN_USE", "update "+v.Id+" is still rolling out")
		}
	}
	trust, err := release.ReadTrust(filepath.Join(s.installDir, "config", "release-trust.pub"))
	if err != nil {
		return u, reject("INVALID_ARGUMENT", "no pinned release key")
	}
	dir, ix, err := release.FindByDigest(filepath.Join(s.installDir, "mirror"), trust, digest)
	if err != nil {
		return u, reject("INVALID_ARGUMENT", "release trust: "+err.Error())
	}
	if _, _, _, err := release.VerifyPlatform(dir, trust); err != nil {
		return u, reject("INVALID_ARGUMENT", "release trust: "+err.Error())
	}
	policy := c.updatePolicy()
	if ix.Channel != value(policy.Channel) {
		return u, reject("INVALID_ARGUMENT", "release channel "+ix.Channel+" is not the fleet channel "+value(policy.Channel))
	}
	info, err := s.installed()
	if err != nil {
		return u, reject("DATA_UNAVAILABLE", "installation state unreadable")
	}
	if !release.Newer(ix.Version, info.HighestVersion) {
		return u, reject("INVALID_ARGUMENT", "anti-rollback: "+ix.Version+" is not newer than installed "+info.HighestVersion)
	}
	enrolled := map[string]bool{}
	for _, h := range s.enrolledHosts() {
		enrolled[h] = true
	}
	if len(hosts) == 0 {
		hosts = s.enrolledHosts()
	}
	seen := map[string]bool{}
	for _, h := range hosts {
		if !enrolled[h] || seen[h] {
			return u, reject("INVALID_ARGUMENT", "host_ids must be distinct enrolled hosts")
		}
		seen[h] = true
	}
	m := newResource("update", owner)
	m["id"] = s.resourceID("up")
	assign(m, &u)
	revocations := []api.SecurityRevocation{}
	for _, r := range ix.Revocations {
		revocations = append(revocations, api.SecurityRevocation{Digest: pointer(r.Digest), EffectiveAt: pointer(r.EffectiveAt), Reason: pointer(r.Reason), MetadataVersion: pointer(ix.MetadataVersion)})
	}
	mv := ix.MetadataVersion
	u.State = "pending"
	u.ReleaseDigest = pointer(digest)
	u.MetadataVersions = &api.MetadataVersions{Root: pointer(uint64(release.RootVersion)), Targets: pointer(mv), Snapshot: pointer(mv), Timestamp: pointer(mv)}
	u.SecurityRevocations = &revocations
	phase := api.MigrationPhase(ix.Migration.Phase)
	u.Migration = &api.Migration{LogVersion: pointer(ix.Migration.LogVersion), SnapshotVersion: pointer(ix.Migration.SnapshotVersion), ProjectionVersion: pointer(ix.Migration.ProjectionVersion), Phase: &phase, RollbackSafe: pointer(ix.Migration.RollbackSafe)}
	u.HostIds = &hosts
	u.CurrentHostId = pointer(hosts[0])
	u.RollbackRef = pointer(info.Current)
	u.Labels = api.StringMap{"lamp.version": ix.Version}
	return u, nil
}

// updateMutation handles update_create and update_delete inside the staged
// management transaction of coreHTTP.
func (s *Service) updateMutation(a caller, verb string, in map[string]any) (api.ResourceRef, map[string]any, *string, []string, error) {
	c := s.journal.Control
	fail := func(err error) (api.ResourceRef, map[string]any, *string, []string, error) {
		return api.ResourceRef{}, nil, nil, nil, err
	}
	if s.role(a) != "owner" {
		return fail(reject("SCOPE_DENIED", "updates need owner authority"))
	}
	if c.Updates == nil {
		c.Updates = map[string]api.Update{}
	}
	var u api.Update
	switch verb {
	case "create":
		if err := acceptFields(in, "release_digest host_ids labels"); err != nil {
			return fail(err)
		}
		if textField(in, "release_digest") == "" {
			return fail(reject("INVALID_ARGUMENT", "release_digest required"))
		}
		var err error
		if u, err = s.newUpdate(c.OwnerID, textField(in, "release_digest"), stringsField(in, "host_ids")); err != nil {
			return fail(err)
		}
	case "delete":
		old, ok := c.Updates[textField(in, "update_id")]
		if !ok {
			return fail(reject("NOT_FOUND", "update not found"))
		}
		if updateActive(old) {
			return fail(reject("RESOURCE_IN_USE", "update is still rolling out"))
		}
		delete(c.Updates, old.Id)
		ref := api.ResourceRef{Kind: "update", Id: old.Id}
		return ref, map[string]any{"update_id": old.Id}, nil, nil, nil
	case "rollback":
		old, ok := c.Updates[textField(in, "update_id")]
		if !ok {
			return fail(reject("NOT_FOUND", "update not found"))
		}
		if _, ok := in["expected_revision"]; ok && uintField(in, "expected_revision") != old.Revision {
			return fail(reject("REVISION_CONFLICT", "update revision is "+strconv.FormatUint(old.Revision, 10)))
		}
		if updateActive(old) {
			return fail(reject("RESOURCE_IN_USE", "update is still rolling out"))
		}
		for _, v := range c.Updates {
			if updateActive(v) {
				return fail(reject("RESOURCE_IN_USE", "update "+v.Id+" is still rolling out"))
			}
		}
		if old.Labels["lamp.phase"] == "rollback" {
			return fail(reject("INVALID_ARGUMENT", "update was already rolled back"))
		}
		// Hosts that completed this release go back, last updated first.
		hosts := value(old.HostIds)
		back := []string{}
		for i := len(hosts) - 1; i >= 0; i-- {
			if strings.HasPrefix(old.Labels["lamp.host."+hosts[i]], "completed ") {
				back = append(back, hosts[i])
			}
		}
		if len(back) == 0 || value(old.RollbackRef) == "" {
			return fail(reject("INVALID_ARGUMENT", "no host completed this update; nothing to roll back"))
		}
		u = old
		u.Labels["lamp.phase"] = "rollback"
		u.Labels["lamp.rollback.hosts"] = strings.Join(back, ",")
		u.CurrentHostId = pointer(back[0])
		u.State = "pending"
		u.Revision++
		u.UpdatedAt = timestamp()
	default:
		return fail(reject("NOT_FOUND", "operation not installed"))
	}
	c.Updates[u.Id] = u
	ref := api.ResourceRef{Kind: "update", Id: u.Id}
	return ref, map[string]any{"update_id": u.Id}, nil, nil, nil
}

// commitControl stages a control-state edit, replicates it and keeps the old
// state if the commit is refused. Called with s.mu held.
func (s *Service) commitControlEdit(edit func(c *controlState)) error {
	old := s.journal.Control
	clone := &controlState{}
	raw, _ := json.Marshal(old)
	json.Unmarshal(raw, clone)
	if clone.Updates == nil {
		clone.Updates = map[string]api.Update{}
	}
	edit(clone)
	s.journal.Control = clone
	if err := s.save("$control"); err != nil {
		s.journal.Control = old
		return err
	}
	return nil
}

func (s *Service) updateDriver() {
	defer s.wg.Done()
	t := time.NewTicker(updateTick)
	defer t.Stop()
	for {
		select {
		case <-s.rootContext.Done():
			return
		case <-t.C:
			s.updateStep()
		}
	}
}

func readJSON(path string, v any) bool {
	raw, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(raw, v) == nil
}

func (s *Service) updateStep() {
	if s.installDir == "" || s.wantFleet && s.fleet == nil {
		return
	}
	if s.fleet != nil {
		if s.fleet.project() != nil {
			return
		}
	}
	s.readLock()
	defer s.mu.Unlock()
	if s.broken {
		return
	}
	c := s.journal.Control
	me := s.hostID()
	reqPath := filepath.Join(s.installDir, "config", "update-request.json")
	resPath := filepath.Join(s.installDir, "config", "update-result.json")
	for id, u := range c.Updates {
		if !updateActive(u) || value(u.CurrentHostId) != me {
			continue
		}
		var res updateResult
		if readJSON(resPath, &res) && res.UpdateID == id {
			rollback := u.Labels["lamp.phase"] == "rollback"
			err := s.commitControlEdit(func(c *controlState) {
				v := c.Updates[id]
				if rollback {
					v.Labels["lamp.rollback."+me] = res.State + " " + res.Version
					hosts := strings.Split(v.Labels["lamp.rollback.hosts"], ",")
					next := ""
					for i, h := range hosts {
						if h == me && i+1 < len(hosts) {
							next = hosts[i+1]
						}
					}
					switch {
					case res.State != "completed":
						v.State = "failed"
					case next == "":
						v.State = "rolled_back"
					default:
						v.CurrentHostId = pointer(next)
						v.State = "pending"
					}
					if res.Detail != "" {
						v.Labels["lamp.detail."+me] = truncate(res.Detail, 200)
					}
					v.Revision++
					v.UpdatedAt = timestamp()
					c.Updates[id] = v
					return
				}
				v.Labels["lamp.host."+me] = res.State + " " + res.Version
				if res.Detail != "" {
					v.Labels["lamp.detail."+me] = truncate(res.Detail, 200)
				}
				switch res.State {
				case "completed":
					hosts := value(v.HostIds)
					next := ""
					for i, h := range hosts {
						if h == me && i+1 < len(hosts) {
							next = hosts[i+1]
						}
					}
					if next == "" {
						v.State = "completed"
					} else {
						v.CurrentHostId = pointer(next)
						v.State = "pending"
					}
				case "rolled_back":
					v.State = "rolled_back"
				default:
					v.State = "failed"
				}
				v.Revision++
				v.UpdatedAt = timestamp()
				c.Updates[id] = v
			})
			if err == nil {
				os.Remove(resPath)
			}
			return
		}
		if _, err := os.Stat(reqPath); err == nil {
			return // the agent is working on it
		}
		if u.State != "pending" && u.State != "draining" {
			return
		}
		req := updateRequest{UpdateID: id, ReleaseDigest: value(u.ReleaseDigest), Version: u.Labels["lamp.version"]}
		if u.Labels["lamp.phase"] == "rollback" {
			req.Version, req.Rollback = value(u.RollbackRef), true
		}
		raw, _ := json.Marshal(req)
		// Commit draining before handing over, so the fleet sees this host
		// leave service before it actually does.
		if u.State == "pending" {
			if err := s.commitControlEdit(func(c *controlState) {
				v := c.Updates[id]
				v.State = "draining"
				v.Revision++
				v.UpdatedAt = timestamp()
				c.Updates[id] = v
			}); err != nil {
				return
			}
		}
		os.WriteFile(reqPath+".tmp", raw, 0600)
		os.Rename(reqPath+".tmp", reqPath)
		return
	}
	// Automatic creation on the coordinator only.
	if s.fleet != nil && s.fleet.raft.State() != raft.Leader {
		return
	}
	policy := c.updatePolicy()
	if !value(policy.Automatic) {
		return
	}
	for _, v := range c.Updates {
		if updateActive(v) {
			return
		}
	}
	trust, err := release.ReadTrust(filepath.Join(s.installDir, "config", "release-trust.pub"))
	if err != nil {
		return
	}
	_, ix, digest, ok := release.Newest(filepath.Join(s.installDir, "mirror"), trust, value(policy.Channel))
	info, err := s.installed()
	if !ok || err != nil || !release.Newer(ix.Version, info.HighestVersion) {
		return
	}
	for _, v := range c.Updates {
		if value(v.ReleaseDigest) == digest {
			return // already tried; a rolled-back release is not retried
		}
	}
	u, err := s.newUpdate(c.OwnerID, digest, nil)
	if err != nil {
		return
	}
	u.Labels["lamp.created_by"] = "automatic"
	s.commitControlEdit(func(c *controlState) { c.Updates[u.Id] = u })
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

var _ = errors.New
var _ = strconv.Itoa
