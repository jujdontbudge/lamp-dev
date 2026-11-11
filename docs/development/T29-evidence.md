# T29 evidence: native CI plugins and triggers

All runs used the owner's Ubuntu laptop, real Firecracker VMs and a private
Lamp store (`/var/lib/lamp/t29-private`). Before creating a VM, every run
checked that fewer than 2 VMs were live and at least 12 GiB was free. The
forges are disposable local installs that run as plain processes on a private
bridge address (`10.219.29.1`), never in Docker:

| Service | Install |
| --- | --- |
| Forgejo 17 | port 3929 |
| Gitea 28.3 | port 3930 |
| Jenkins | `jenkins.war`, port 3931 |
| Woodpecker 3.20.1 | ports 3932 and 3933 |
| GitLab CE 19.5.4 | throwaway chroot install, port 3934 |

The project in every case is a copy of public zlib. Each VM's network is
deny-by-default, with exact exceptions for those ports only.

Each run is driven by `docs/development/evidence/t29/run_demo.py`, which
starts the supervisor and runs `t29_demo_test.go`. The GitLab half is driven
by `gitlab_demo.py`. The seed image comes from `prepare_seed2.py`. The logs
below are the unedited run output, except that colour codes are stripped and
token-like values are redacted.

## Results

| System | Plugin | Real job | Log |
| --- | --- | --- | --- |
| Forgejo | `pg_forgejo` | success; Lamp cancel shows as native `cancelled`; controller lost mid-job, then recovery cancelled natively, deregistered the runner and deleted the VM | [02](evidence/t29/logs/demonstration-02.log) |
| Gitea | `pg_gitea` | success, native log captured (6.5 KB), VM deleted, 0 runners left | [09](evidence/t29/logs/demonstration-09-gitea.log) |
| Jenkins | `pg_jenkins` | success, archived `libz.a` collected (`archive.zip`, 66 KB) | [08](evidence/t29/logs/demonstration-08-systems.log) |
| Woodpecker | `pg_woodpecker` | success; public repo classed `public_pr` (public policy) | [08](evidence/t29/logs/demonstration-08-systems.log) |
| GitLab, Custom executor | `cmd/lamp-gitlab-executor` | success with artifacts, in its own fresh sandbox | [16](evidence/t29/logs/demonstration-16-gitlab.log) |
| GitLab, Fleeting | `cmd/lamp-fleeting` (+ `lamp-fleeting-guest`) | success with artifacts, in its own instance; the instance is pruned after one use | [14](evidence/t29/logs/demonstration-14-gitlab.log), [16](evidence/t29/logs/demonstration-16-gitlab.log) |
| Buildkite, CircleCI, Azure Pipelines, Bitbucket | adapters in `internal/ci/systems.go` | **not run**: the owner dropped them (2026-11-11); adapters kept as built. Earlier signups were blocked, see below | none |

Every run ended with 0 sandboxes left undeleted, 0 forge runners still
registered, and no Firecracker process left by this ticket.

## Triggers and schedules

All from [08](evidence/t29/logs/demonstration-08-systems.log):

- **Webhook.** A signed delivery returns 202. A vendor retry of the same
  delivery is replayed as the same operation. A forged signature returns 401
  `TRIGGER_UNAUTHORIZED`. Three deliveries created exactly one job. That job ran
  to `completed/success` on a fresh VM, and the event reached the agent
  (`agent-output.log`).
- **Slack.** A v0-signed event ran to `completed/success` on a fresh VM. The
  same event replayed with an hour-old timestamp returns 401.
- **Schedule.** `* * * * *` in America/New_York fired by timer and ran to
  `completed/success` on a fresh VM. `overlap_policy=skip` skipped 3
  occurrences while that run was live.
- **Daylight saving.** On the spring-forward day, a nonexistent 02:30 shifts
  to 03:30 EDT (or is skipped under `skip`). On the fall-back day, 01:30 runs
  once.

## Linear, trigger cancel, fleet schedules and policy

- **Linear** ([17](evidence/t29/logs/demonstration-17-extra.log)). A
  `Linear-Signature` HMAC delivery returns 202. A redelivery is replayed as the
  same operation. A forged signature returns 401 `Linear signature mismatch`,
  and a stale timestamp returns 401. Four deliveries created one job. It ran to
  `completed/success` on a fresh VM, and that VM was deleted.
- **Trigger cancel** ([17](evidence/t29/logs/demonstration-17-extra.log)). A
  live trigger-started job was cancelled through Lamp. It ended
  `canceled/cancelled`, its VM was deleted, 0 execs were left in it, and no
  Firecracker process was left on the host. The guard audit shows
  `released=false`, only because this demonstration configures no
  `--audit-release`. No other authority was left.
- **Schedule fleet coordination**
  ([20](evidence/t29/logs/demonstration-20-fleet.log)). Two lampd hosts
  (`hs_t29a`, `hs_t29b`) formed one fleet over mTLS Raft. The same schedule
  (`fleet_key=t29-nightly`, every minute) was enrolled on both. Each occurrence
  has one replicated claim. Occurrence 21:06Z ran once fleet-wide, on
  `hs_t29a`. The other host skipped the occurrences it did not claim (1 on
  `hs_t29a`, 2 on `hs_t29b`). The one job that ran ended `failed`. That does
  not affect coordination, which is the point of this observation.
- **Trusted vs public policy** (trusted
  [23](evidence/t29/logs/demonstration-23-trusted.log), public
  [24](evidence/t29/logs/demonstration-24-public.log)). These come from the
  live VM's root-owned network attachment and the broker's own audit:

  | | Trusted (Gitea, `trusted`) | Public (Woodpecker, `public_pr`) |
  | --- | --- | --- |
  | Model or broker secret | grant issued (broker audit: `issued` for its VM) | none issued; the broker audit is empty |
  | Network exceptions | 8: the private forge ports plus broker `10.219.29.1:3936` | 7: the forge ports only, no broker |
  | Retention (`retain_stopped=true`) | VM deleted, because the job failed and only successful trusted jobs are kept | VM deleted even though the job succeeded: public jobs are never retained |

  The trusted job failed in each of three attempts: in runs 18 and 23, and run
  21 timed out waiting for a dispatch. So a retained trusted VM was **not
  observed**. The rule is `retain = retain_stopped && !public`, applied to
  successful jobs. The public side shows that rule really deleting a successful
  job's VM. In run 23, gitea-runner exited without a native result (`native
  job result unavailable after runner exit`), and the guard log is clean. In
  run 18, the guard died with `ENOBUFS` on its netlink queue. Run 19, a public
  run, shows `NFQUEUE poll/liveness failure`. These are not yet explained.
- **Guard limit found.** The per-process guard spool is 256 KiB, and heartbeats
  are written every 250 ms. A command that runs longer than about 2.7 minutes
  fills the spool, and the guard exits 70. The same limit is on `main`.

## Not run

Buildkite, CircleCI, Azure Pipelines and Bitbucket need hosted accounts. The
adapters exist, but no real job ran, because every headless signup with
Playwright's own headless Chromium stopped at a bot check:

- **Buildkite:** at the final step of the email signup form (`/signup`),
  "Bot detection failed; please try again".
- **CircleCI:** `/signup` is behind Google reCAPTCHA.
- **Bitbucket:** Atlassian ID signup is behind reCAPTCHA Enterprise.
- **Azure Pipelines:** Microsoft account creation shows a HUMAN
  press-and-hold challenge at the email step.

On 2026-11-11 the owner dropped real-job runs for these four. Their adapters
stay as built, and no further signups were attempted.

The generic GitLab `run-single` forge adapter (`pg_gitlab`) was also not run.
GitLab was demonstrated through the Custom executor and the Fleeting plugin,
which is how GitLab Runner hosts plug in.

## Fixes found by the real runs

- Trigger work admission raced, so it is now serialized.
- Trigger work cleanup did not wait for the sandbox delete operation, so jobs
  stayed in `cleanup_failed`.
- Gitea reports bare workflow names, and gitea-runner v6 needs `HOME`.
- Jenkins answers form posts with a 302 redirect.
- Woodpecker's local backend fetched its clone plugin from GitHub. It is now
  `skip_clone` with a clone from the private forge.
- Fleeting guests run unprivileged, with no writable `/run`. The guest agent
  now serves SSH itself.
- The guard audits argv. Bulk upload through argv used up the sandbox's audit
  reservation and killed every later exec with exit 70. The guest agent is now
  fetched over the authenticated callback and checked against its sha256.
- The GitLab executor had polled operations with an unsupported query
  parameter and looped on fully read output.
