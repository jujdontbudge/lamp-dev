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
starts the supervisor and runs `t29_demo_test.go`. The harness is built only
with the `t29demo` tag, so it stays out of `./...`. It is copied into
`internal/control` and run as `go test -tags t29demo ./internal/control -run
'^TestT29Demonstration$'`. The GitLab half is driven
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
- **Fleet job result** ([27](evidence/t29/logs/demonstration-27-fleet.log)).
  The one fleet-scheduled job ended `failed` because sandbox admission returned
  `429 NO_CAPACITY: local reserved slice exhausted`. The demonstration's two
  fleet hosts have no VM capacity of their own. It was not the guard timeout.
- **Trusted vs public policy** (trusted
  [25](evidence/t29/logs/demonstration-25-trusted.log) with its native
  [Gitea log](evidence/t29/logs/gitea-run-10-native.log), public
  [24](evidence/t29/logs/demonstration-24-public.log)). These come from the
  live VM's root-owned network attachment, the broker's own audit and the job
  records:

  | | Trusted (Gitea, `trusted`) | Public (Woodpecker, `public_pr`) |
  | --- | --- | --- |
  | Model or broker secret | grant issued; the job saw `lamp model grant - present` | none issued; the broker audit is empty |
  | Network exceptions | 8: the private forge ports plus broker `10.219.29.1:3936` | 7: the forge ports only, no broker |
  | Retention (`retain_stopped=true`), successful job | VM **kept** (`suspended`), then inspected and deleted by the owner | VM **deleted**: public jobs are never retained |

- **Guard heartbeat fix, and one job over 3 minutes.** The guard's collector
  sends a heartbeat every 250 ms. The host used to write each one into the
  command's 256 KiB audit spool. The spool filled after about 2.7 minutes, the
  guard exited 70, and the command was killed. That is what failed trusted run
  23: the runner's spool `process-000004.ndjson` stood at 261,690 bytes, and
  gitea-runner died without a native result. The host receiver
  (`crates/lamp-guard/src/audit.rs`) now ACKs heartbeats and keeps one a minute,
  with the count it stands for. Every other record is still durable before its
  ACK, and a full spool still kills the command. Run 25's trusted job then ran
  3 min 34 s, 22:16:37 to 22:20:11 UTC, with a step that took 201 s. Every step
  succeeded.
- **Netlink failures.** Run 18's `ENOBUFS` and run 19's `NFQUEUE
  poll/liveness failure` are the guard netlink receive-buffer issue that PR #32
  (T25) fixes. This PR does not change `netlink.rs`.

## Review fixes (2716db2)

Shown in one short real run each:

- **Replay with a fresh delivery ID**
  ([28](evidence/t29/logs/demonstration-28-replay.log)). Delivery-ID headers
  are not signed, so deduplication now keys on signed content only:
  - Linear: `webhookId` plus the body hash;
  - Lamp scheme: the hash of `timestamp.body`;
  - forge scheme (`X-Hub-Signature-256`): the body hash.

  For each scheme, a delivery ran once on a fresh VM. The same captured body
  was then resent with a new `Linear-Delivery`, `X-Lamp-Delivery` or
  `X-Gitea-Delivery` value. Each resend returned `replayed=true` with the same
  operation, and each trigger had exactly 1 job.
- **Fleeting secret off argv**
  ([30](evidence/t29/logs/demonstration-30-gitlab.log)). The bootstrap exec's
  argv now holds only a one-time boot nonce and the public SSH key. The plugin
  accepts the nonce once, then sends the instance secret over the callback. The
  guest agent receives it in its environment and removes it at start. The
  Custom-executor and Fleeting jobs both succeeded. The driver collected the
  instance secret from the plugin's own state file. It then searched the Lamp
  store (journal and exec records) and the guard's audit spools for it: 0 files
  contained it. The boot nonce had already been consumed before the driver's
  first poll, so it was not searched.
- **Fleet-wide overlap**
  ([29](evidence/t29/logs/demonstration-29-fleet-skip.log)). The fleet state
  now replicates the active occurrence of each `fleet_key` schedule. A claim
  compare-and-sets it:
  - `skip` refuses while another host's occurrence is live.
  - `cancel_previous` supersedes it, and the previous host cancels its job when
    it sees that.
  - The host that ran the job clears the entry once the job ends.
  - An entry older than 7 h counts as stale (the host was lost).

  Two hosts ran one `skip` schedule, every minute, with a 150 s job. The 22:53
  occurrence ran on `hs_t29a` from 22:53:00 to 22:55:30. Midway, the replicated
  active entry named `hs_t29a`. Neither host claimed or started 22:54 or 22:55.
  The job ran to `completed/success`.
- **Code changes only**, noted rather than demonstrated:
  - GitLab `pipeline` and `parent_pipeline` sources are now public, because a
    child pipeline may descend from a fork's MR pipeline, which is not traced
    upstream.
  - Jenkins `UpstreamCause` is now public.
  - CI retention requires `trust_class == "trusted"` directly at the keep
    decision, so public work is never retained, whatever `Retain` holds.
  - The guard coalesces only frames that are exactly the collector's
    `{"sequence", "details": {"type":"heartbeat"}}`. Any other frame is
    persisted. Records are still durable before their ACK, and a full spool
    still kills the command.

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
