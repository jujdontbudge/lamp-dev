package control

import (
	"context"
	"path"
	"regexp"
	"strings"
	"time"
)

// Change sets (LIF-006) carry a winning fork's work back through git. The
// source's commits since the common base become one binary diff blob; apply
// runs a three-way merge in the target's repository. A conflict leaves the
// target exactly at its own committed state and reports the paths; a target
// that moved since the change set was made is refused as stale. Until the
// session subsystem is installed, a session field may name a sandbox, which
// stands for that sandbox's own workspace.

func init() {
	lifeHandlers["change_set_create"] = (*Service).planChangeSetCreate
	lifeHandlers["change_set_apply"] = (*Service).planChangeSetApply
	lifeHandlers["change_set_reject"] = (*Service).planChangeSetReject
	lifeHandlers["change_set_delete"] = (*Service).planChangeSetDelete
}

var gitSHA = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

const defaultRepository = "/workspace"

func (s *Service) sessionSandbox(a caller, id string) (string, error) {
	if !strings.HasPrefix(id, "sb_") {
		return "", reject("NOT_FOUND", "session not found")
	}
	sb, err := s.lifeSandboxFor(a, id)
	if err != nil {
		return "", err
	}
	if sb.State != "ready" && sb.State != "suspended" {
		return "", reject("RESOURCE_IN_USE", "sandbox "+id+" is not ready")
	}
	return id, nil
}

func (s *Service) git(ctx context.Context, sandbox, repo string, args ...string) (string, error) {
	res, err := s.guestRun(ctx, sandbox, append([]string{"git", "-C", repo, "-c", "user.name=Lamp", "-c", "user.email=lamp@localhost", "-c", "safe.directory=*"}, args...)...)
	if err != nil {
		return "", err
	}
	if res.Exit != 0 {
		return string(res.Stdout), reject("INVALID_ARGUMENT", "git "+args[0]+": "+strings.TrimSpace(string(res.Stderr)))
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func (s *Service) planChangeSetCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := onlyFields(in, "source_session_id target_session_id base_tree_digest selected_paths"); err != nil {
		return lifePlan{}, err
	}
	source, err := s.sessionSandbox(a, textField(in, "source_session_id"))
	if err != nil {
		return lifePlan{}, err
	}
	target, err := s.sessionSandbox(a, textField(in, "target_session_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if source == target {
		return lifePlan{}, reject("INVALID_ARGUMENT", "source and target must differ")
	}
	repo := defaultRepository
	if labels, ok := in["labels"].(map[string]any); ok && textField(labels, "repository") != "" {
		repo = textField(labels, "repository")
	}
	if !strings.HasPrefix(repo, "/") || path.Clean(repo) != repo {
		return lifePlan{}, reject("INVALID_ARGUMENT", "repository must be a clean absolute guest path")
	}
	paths := stringsField(in, "selected_paths")
	for _, p := range paths {
		if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") || path.Clean(p) != p || strings.HasPrefix(p, "..") {
			return lifePlan{}, reject("INVALID_ARGUMENT", "selected paths are clean repository-relative paths")
		}
	}
	base := strings.TrimPrefix(textField(in, "base_tree_digest"), "git:")
	if base != "" && !gitSHA.MatchString(base) {
		return lifePlan{}, reject("INVALID_ARGUMENT", "base_tree_digest must be git:<commit>")
	}
	m := s.lifeResource("cs", "change_set", a.Principal)
	id := textField(m, "id")
	m["state"] = "proposed"
	m["source_session_id"] = source
	m["target_session_id"] = target
	m["selected_paths"] = paths
	m["conflicts"] = []any{}
	applyLabels(m, in)
	cs := &lifeChangeSet{R: m, Source: source, Target: target, Repository: repo}
	s.life.ChangeSets[id] = cs
	owner := a.Principal
	var patch []byte
	var sum string
	var commits []string
	var targetHead string
	return lifePlan{ref: lifeRef("change_set", id), timeout: 10 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			for _, sb := range []string{source, target} {
				if err := s.Wake(ctx, caller{Principal: owner}, sb); err != nil {
					return nil, err
				}
			}
			var err error
			if targetHead, err = s.git(ctx, target, repo, "rev-parse", "HEAD"); err != nil {
				return nil, err
			}
			if base == "" {
				// The newest source commit the target also has.
				theirs, err := s.git(ctx, target, repo, "rev-list", "--max-count=4000", "HEAD")
				if err != nil {
					return nil, err
				}
				have := map[string]bool{}
				for _, c := range strings.Fields(theirs) {
					have[c] = true
				}
				mine, err := s.git(ctx, source, repo, "rev-list", "--max-count=4000", "HEAD")
				if err != nil {
					return nil, err
				}
				for _, c := range strings.Fields(mine) {
					if have[c] {
						base = c
						break
					}
				}
				if base == "" {
					return nil, reject("INVALID_ARGUMENT", "source and target share no history")
				}
			}
			out, err := s.git(ctx, source, repo, "rev-list", "--reverse", base+"..HEAD")
			if err != nil {
				return nil, err
			}
			commits = strings.Fields(out)
			file := "/tmp/lamp-" + id + ".patch"
			script := `f=$0 r=$1 b=$2; shift 2; git -C "$r" -c safe.directory='*' diff --binary --full-index "$b" HEAD -- "$@" > "$f"`
			args := append([]string{"sh", "-c", script, file, repo, base}, paths...)
			res, err := s.guestRun(ctx, source, args...)
			if err != nil {
				return nil, err
			}
			if res.Exit != 0 {
				return nil, reject("INVALID_ARGUMENT", "git diff: "+strings.TrimSpace(string(res.Stderr)))
			}
			_, _, size, err := s.guestStat(ctx, source, file)
			if err != nil {
				return nil, err
			}
			patch, err = s.guestReadFile(ctx, source, file, size, maxTransferBytes)
			s.guestRun(context.WithoutCancel(ctx), source, "rm", "-f", file)
			if err != nil {
				return nil, err
			}
			sum, err = s.putObject(patch)
			return map[string]any{"change_set_id": id}, err
		},
		finish: func(_ map[string]any, err error) error {
			if err != nil {
				m["state"] = "rejected"
				editResource(m)
				return err
			}
			b := s.newReadyBlob(owner, sum, int64(len(patch)), "text/x-diff", source)
			m["patch_blob_id"] = textField(b, "id")
			m["base_tree_digest"] = "git:" + base
			ids := make([]any, len(commits))
			for i, c := range commits {
				ids[i] = c
			}
			m["commit_ids"] = ids
			cs.Base, cs.TargetHead = base, targetHead
			editResource(m)
			return nil
		}}, nil
}

func (s *Service) planChangeSetApply(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "change_set", textField(in, "change_set_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if textField(in, "approval_id") != "" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "approval-gated apply is not configured for change sets")
	}
	if st := textField(m, "state"); st != "proposed" && st != "conflicted" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "change set is "+st)
	}
	id := textField(m, "id")
	cs := s.life.ChangeSets[id]
	allowed := map[string]bool{}
	for _, p := range stringsField(m, "selected_paths") {
		allowed[p] = true
	}
	selected := stringsField(in, "selected_paths")
	for _, p := range selected {
		if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") || path.Clean(p) != p {
			return lifePlan{}, reject("INVALID_ARGUMENT", "selected paths are clean repository-relative paths")
		}
		if len(allowed) > 0 && !allowed[p] {
			covered := false
			for q := range allowed {
				if strings.HasPrefix(p, q+"/") {
					covered = true
				}
			}
			if !covered {
				return lifePlan{}, reject("INVALID_ARGUMENT", p+" is not part of this change set")
			}
		}
	}
	b := s.life.Blobs[textField(m, "patch_blob_id")]
	if b == nil {
		return lifePlan{}, reject("DATA_UNAVAILABLE", "change set patch is missing")
	}
	patch, err := s.readObject(textField(b.R, "sha256"))
	if err != nil {
		return lifePlan{}, err
	}
	target, repo := cs.Target, cs.Repository
	if _, err := s.sessionSandbox(a, target); err != nil {
		return lifePlan{}, err
	}
	var conflicts []string
	var applied string
	return lifePlan{ref: lifeRef("change_set", id), timeout: 10 * time.Minute,
		run: func(ctx context.Context) (map[string]any, error) {
			if err := s.Wake(ctx, a, target); err != nil {
				return nil, err
			}
			head, err := s.git(ctx, target, repo, "rev-parse", "HEAD")
			if err != nil {
				return nil, err
			}
			if head != cs.TargetHead {
				return nil, reject("REVISION_CONFLICT", "the target moved since this change set was made; create a new change set")
			}
			if status, err := s.git(ctx, target, repo, "status", "--porcelain", "--untracked-files=no"); err != nil {
				return nil, err
			} else if status != "" {
				return nil, reject("RESOURCE_IN_USE", "the target has uncommitted changes; nothing was overwritten")
			}
			if len(patch) == 0 {
				applied = head
				return map[string]any{}, nil
			}
			file := "/tmp/lamp-" + id + ".patch"
			if err := s.guestWriteFile(ctx, target, file, patch); err != nil {
				return nil, err
			}
			defer s.guestRun(context.WithoutCancel(ctx), target, "rm", "-f", file)
			args := []string{"apply", "--3way", "--index", "--whitespace=nowarn"}
			for _, p := range selected {
				args = append(args, "--include="+p, "--include="+p+"/*")
			}
			if _, err := s.git(ctx, target, repo, append(args, file)...); err != nil {
				out, _ := s.git(ctx, target, repo, "diff", "--name-only", "--diff-filter=U")
				conflicts = strings.Fields(out)
				// Restore exactly the target's committed state; it was clean.
				if _, rerr := s.git(ctx, target, repo, "reset", "--hard", "--quiet", head); rerr != nil {
					return nil, rerr
				}
				if len(conflicts) == 0 {
					return nil, err
				}
				return map[string]any{}, nil
			}
			message := "Apply change set " + id + " from " + cs.Source
			if _, err := s.git(ctx, target, repo, "commit", "--quiet", "--allow-empty", "-m", message); err != nil {
				return nil, err
			}
			applied, err = s.git(ctx, target, repo, "rev-parse", "HEAD")
			return map[string]any{}, err
		},
		finish: func(_ map[string]any, err error) error {
			if err != nil {
				return err
			}
			if len(conflicts) > 0 {
				m["state"] = "conflicted"
				c := make([]any, len(conflicts))
				for i, p := range conflicts {
					c[i] = p
				}
				m["conflicts"] = c
			} else {
				m["state"] = "applied"
				m["conflicts"] = []any{}
				labels, _ := m["labels"].(map[string]any)
				out := map[string]any{}
				for k, v := range labels {
					out[k] = v
				}
				out["applied_commit"] = applied
				m["labels"] = out
				if len(selected) > 0 {
					m["selected_paths"] = selected
				}
				cs.TargetHead = applied
			}
			editResource(m)
			return nil
		}}, nil
}

func (s *Service) planChangeSetReject(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "change_set", textField(in, "change_set_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	if textField(m, "state") == "applied" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "change set is applied")
	}
	m["state"] = "rejected"
	editResource(m)
	return lifePlan{ref: lifeRef("change_set", textField(m, "id")), result: map[string]any{"change_set": m}}, nil
}

func (s *Service) planChangeSetDelete(a caller, in map[string]any) (lifePlan, error) {
	m, err := s.ownedOrMissing(a, "change_set", textField(in, "change_set_id"))
	if err != nil {
		return lifePlan{}, err
	}
	if err := lifeRevision(m, in); err != nil {
		return lifePlan{}, err
	}
	m["state"] = "rejected"
	editResource(m)
	if b := s.life.Blobs[textField(m, "patch_blob_id")]; b != nil {
		b.R["state"] = "deleted"
		editResource(b.R)
	}
	return lifePlan{ref: lifeRef("change_set", textField(m, "id")), result: map[string]any{}}, nil
}
