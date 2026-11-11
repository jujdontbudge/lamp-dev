# T19 lifecycle, lasting storage and saved templates — evidence, 2026-11-11

Owner rule of 2026-11-10: the evidence is one real end-to-end demonstration of
each feature on the owner's machines, no trial or benchmark loops, almost no
tests. The E-numbered gates, thresholds and timing targets in the ticket are
background only; nothing here measures time or claims a gate. `docs/spec`,
`docs/tickets`, the Makefile, CI and existing tests are unchanged.

Everything below ran on lane LX (the Ubuntu laptop), through Lamp's public HTTP
API on real Firecracker sandboxes. The supervisor ran from a private store
(`/var/lib/lamp/t19-private`, its own memory/netns directories and UID base)
beside the other tickets' supervisors, which were left alone. Before every
sandbox create, the driver checked that fewer than two Firecracker processes
were running and at least 12 GiB was available; when another ticket held
sandboxes, it waited. No owner service was stopped, Docker was not touched, and
every run ended with zero Firecracker processes of ours.

The driver is [`evidence/t19/demo.py`](evidence/t19/demo.py), with its
environment helper [`lamp_env.py`](evidence/t19/lamp_env.py). It creates a
fresh lampd state per part, enrolls the owner (and, in part D, a team member)
with bearer credentials, and writes every request and response to a
`transcript.jsonl`. Credentials, the identity file and private state are not
retained. The transcripts are in `evidence/t19/<part>/transcript.jsonl`, and
each one pins the `lampd` and `lamp-supervisor` SHA-256 it ran against.

All five parts (A–E) ran against one build of the final tree (after merging
`main`, including T9 and the sandbox budgets ticket): `lampd` SHA-256 `4b4c81ea…5d1714` and `lamp-supervisor`
`8446a963…80a278`. Every run ends with the supervisor's remaining captures
listed after lampd stopped; in all five that list is empty. Part A is run
v9; B–E are run v10 (v9's part B hit the guard collector's exit 70 in one
task command, the T10 issue below, and was run again unchanged; that run is
[kept](evidence/t19/B-exit70-run/transcript.jsonl)).

## A — idle suspend, wake, overrides, expiry (LIF-001/002/003)

[Transcript](evidence/t19/A/transcript.jsonl), sandbox
`sb_8874684d…` created with `lifecycle {idle_action: suspend, idle_grace_seconds: 600}`.

* A stale `If-Match` on `sandbox_update` returned `REVISION_CONFLICT`, and an
  unsupported field returned `INVALID_ARGUMENT`.
* **The owner's idle definition (LIF-002).** One workload at a time, each judged
  by the first monitor observation started after the workload and after the
  exec's own session activity had aged out:

  | Workload in the guest | Eligible | Reasons |
  |---|---|---|
  | nothing running | yes | — |
  | sleeping background process | no | `task_process` |
  | dev server listening on :8000 | no | `task_process`, `task_connection` |
  | busy build loop | no | `task_process` |
  | harness parked on a person message | yes | — |
  | parked harness plus a sleeping process | no | `task_process` |
  | parked harness alone again | yes | — |

  The harness is a real process blocked reading a FIFO, holding a 16-byte
  random nonce only in memory. It marks itself parked with a `flock`ed marker.
* **Automatic suspend (LIF-001).** Lowering the grace to 15 s with
  `sandbox_update` let the monitor suspend the parked sandbox by itself:
  `state=suspended`, snapshot `sn_1ce652d4…`, and no Firecracker process of
  ours left running. `snapshot_get` returns the suspend's record: `ready`,
  `preserves_memory: true`, generation 1, `durability: local`,
  `consistency: application`. After the resume below, the same record reads
  `deleted`.
* **A full restart while suspended.** lampd and the root supervisor were both
  stopped and started again, with a new control grant. The sandbox read back as
  `suspended`.
* **Wake by exec.** An ordinary `exec_create` wrote the person's message into
  the FIFO. Lamp resumed the sandbox first, as generation 2. The harness
  (still pid 7) answered with `nonce_from_memory = d714b27c…2b1df1`, exactly the
  nonce it had shown before the suspend. The guest's uptime read 102 s, so this
  was a memory restore, not a cold boot.
* **Retention (LIF-003).** With `suspended_retention_seconds: 20`, a suspended
  sandbox was deleted by the monitor. Its saved states (`cp_1ce652d4…` from the
  earlier suspend and `cp_87d29354…` from this one) were discarded from the
  supervisor; `captures_left` is empty.
* **Hard expiry.** A second sandbox with `idle_action: keep` and an `expires_at`
  40 s ahead was suspended at its deadline. `sandbox_resume` then returned
  `TASK_AUTHORITY_EXPIRED`: saved work after a deadline needs new authority.

## B — fork alternatives, keep the winner, merge through git (LIF-006)

[Transcript](evidence/t19/B/transcript.jsonl). Parent P holds a git repository
(`app.py`, `README.md`, commit `base`).

* Fork C1 committed "alternative A" (`return "hello (alternative A)"`) and was
  suspended. Fork C2 committed "alternative B" plus a test. Meanwhile P kept
  working and committed a README change.
* `change_set_create` C2 → P found base `0806e4b9…`, commits `[e3d5efcb…]` and
  stored the binary patch as a blob. `change_set_apply` applied it with a git
  3-way apply and committed it in P (`eca65c1c…`). P's log then
  read `Apply change set … / parent docs / base`, `app.py` held B's version,
  P's README edit was kept, and B's test passed inside P.
* C2 was deleted. `change_set_create` C1 → P **woke the suspended C1**
  (journal: suspended → resuming → ready) to read its commit `caab7bf5…`. Its
  apply hit the winner's edit to the same line. The change set became
  `conflicted` with `conflicts: ["app.py"]`. P's HEAD was still `eca65c1c…`
  with a clean worktree and B's `app.py`; nothing was overwritten. The loser was
  then rejected.

## C — saved templates, recipes, warm pools (TPL-002, TPL-003, LIF-005)

[Transcript](evidence/t19/C/transcript.jsonl).

* **Save installed tools.** A sandbox installed `/var/tmp/tools/bin/greet` and
  also wrote an unselected file. `sandbox_save_template` with
  `selected_paths ["/var/tmp/tools"]` built `tp_60c8c9da…` (`build:
  clean_copy`, root object `cp_60c8c9da…`). It ran in a fresh builder sandbox
  from the base template, which was released afterwards.
* **Tainted source.** The source then got a planted `bin/canary` and
  `capture_trust: rebuild_required`. A copy-save returned
  `CLEAN_REBUILD_REQUIRED`. A reconstruction plan (the tool's bytes uploaded as a
  pinned blob plus one command) built `tp_dee229f8…` (`build: reconstructed`)
  in a clean builder from the plan alone.
* **Recipes.** `recipe_create greet` → `rc_f90683ed…` (revision 1). An update
  with a wrong revision returned `REVISION_CONFLICT`; the right one created a new
  immutable version `rc_1ac04707…` (revision 2, new digest). Both versions are
  listed unchanged.
* A sandbox created from the recipe ran `greet-tool v1: hello, person` and
  `rebuilt-from-pinned-inputs` with no install step. Its tool directory listed
  only `greet` (no canary), and the unselected file was absent. The recipe's
  lifecycle was applied to it.
* **Warm pool.** A pool with `target_size 1` for the recipe filled to
  `ready_count 1`. `sandbox_create {recipe_id}` handed out that never-used entry:
  `warm_pool_id` set, `auth_mode: none`, no token-like variables in its
  environment, and the tool ran. The pool refilled to `ready_count 1`. Deleting
  the recipe while that sandbox used it returned `RESOURCE_IN_USE`. After
  cleanup, both template captures were discarded from the supervisor.

## D — lasting storage (STO-001..005)

[Transcript](evidence/t19/D/transcript.jsonl), two sandboxes A and B.

* **Shared cache (STO-001).** A wrote a 300 KB wheel and an index into an
  attached cache. `cache_publish` with the empty generation as `base_digest`
  produced generation `a3c8b6e4…`; a second publish on the old base returned
  `REVISION_CONFLICT`. B attached the cache read-only and read identical SHA-256s
  (`b6f99635…`, `2d27fbdf…`).
* **Named volume with several attachments (STO-002).** A and B both attached
  `shared_files` volume `notes` read-write, wrote different files and
  detached. On reattach both files were present. When both edited
  `shared.txt`, the second detach returned `RESOURCE_IN_USE` ("changed on both
  sides since attach") and kept the attachment, so nothing was overwritten. An
  `exclusive_block` volume refused a second writer with `RESOURCE_IN_USE`.
* **CI output outlives its job (STO-003).** A 1 MiB random file was transferred
  out of A to a blob, and `artifact_create {job_id: job_ci_1}` published it as
  `complete`. A was deleted, then the artifact downloaded in verified chunks with
  the guest's SHA-256 `f66d2c51…`. A manifest naming one missing entry produced
  an `incomplete` artifact (`present_bytes 11` of `expected_bytes 4107`).
* **Owner-only (STO-004).** The fleet was switched to team mode, and a real
  member principal was enrolled with its own bearer credential. It listed zero
  caches, volumes, artifacts, blobs and backups while the owner had rows. Its
  `get` of the owner's artifact, volume and cache returned `NOT_FOUND`, as did
  its download. Creating a `shared` volume returned `SCOPE_DENIED`.
* **Encrypted backup and restore (STO-005).** `backup_create` sealed the volume
  (`bk_aa26edd2…`, AES-256-GCM, manifest digest recorded). The volume was
  deleted, and `backup_restore` with `new_authority_epoch 2` recreated it under
  its id. Attached to B, it read `from-a / from-b / edit-from-a`. A second
  backup had one byte flipped on disk, and its restore returned
  `DATA_UNAVAILABLE` ("authentication failed").

An earlier attempt of part C is
[kept here](evidence/t19/C-exit70-run/transcript.jsonl). It stopped because the
in-guest guard collector exited 70 during one task command; its audit stream
ends without `collector_complete`, matching the intermittent collector failure
T10 investigated, not T19 code. Part C was run again; that attempt's two
template captures were deleted with the supervisor's state administration.

## E — review fixes (PR #27 review)

[Transcript](evidence/t19/E/transcript.jsonl). Each fix shown once, through the
public API on real sandboxes.

* **Writes are never dropped on detach (fix 3).** A `shared_files` volume was
  attached read-write and a file written into it. `sandbox_delete` was then
  refused with `RESOURCE_IN_USE` ("read-write volume attachment … is still
  attached; detach it first so its files are written back"). The sandbox was
  suspended, and `attachment_delete` woke it (generation 1 suspended →
  generation 2 ready) and wrote the file back. Lamp's own retention and idle
  deletes detach (write back) first and keep the sandbox if that fails; a
  detach from a sandbox that is already gone returns `DATA_UNAVAILABLE` and
  stays attached instead of pretending.
* **Restart after resume (fix 7).** lampd alone was stopped and started again
  with the generation-2 guest running. Reconciliation probed that generation
  and the sandbox read `ready`; a read-only reattach read
  `written-before-suspend` back from the volume.
* **SSH attach wakes (LIF-001).** The sandbox was suspended again with a nonce
  written only to guest RAM (`/dev/shm`). T18's `lamp-access` console, on a
  loopback SSH listener with a throwaway key and the owner's API credential,
  ran `ssh … shell <sandbox>`. It resumed the sandbox before the prompt
  (generation 2 suspended → generation 3 ready) and printed the same nonce,
  `bd3c9678…d8ec1b`.
* **Hard expiry of a busy sandbox (fix 8).** A sandbox with `expires_at` 40 s
  ahead ran `while :; do sleep 1; done` as a task exec. It was suspended at the
  deadline anyway (about 4 s after `expires_at`, on the next monitor tick). The exec went from `running` to
  `outcome_unknown`, and `sandbox_resume` returned `TASK_AUTHORITY_EXPIRED`.
* **Shared download blob (fix 4).** Artifact 1 published blob X; artifact 2's
  manifest listed X and a second blob Y. Deleting artifact 1 left X `ready`, and
  artifact 2 still downloaded X byte for byte.
* **Two-output backup (fix 5).** Artifact 2 was backed up, deleted and
  restored. The restored manifest names new blob ids for both outputs, and both
  downloaded with their original SHA-256s (`f7a4c96d…`, `738b2dfd…`).
* **Warm-pool entries stay never-used (fix 6).** With a pool of one ready
  entry, `sandbox_file_transfer` (upload of a token-like file) into the entry
  returned `RESOURCE_IN_USE` ("unclaimed warm-pool sandbox"). `sandbox_create
  {recipe_id}` then handed out that same entry, and the file was absent.

Fixes 1 and 2 are code changes without a separate run: a rejected lifecycle
request now restores the state in place, so objects and resource maps that a
running operation (such as a cache publish) still holds keep its later writes;
and change-set applies into one target repository run one at a time, so a
second apply sees the first one's commit and stops with `REVISION_CONFLICT`,
and a conflicting apply resets only to the current `HEAD`.

## Code

* `internal/control/life*.go`: the lifecycle monitor, the suspend/resume/wake
  paths, idle observation, blobs, caches, volumes, attachments, artifacts,
  encrypted backups, saved templates, recipes, warm pools and change sets.
  Every handler runs through the existing operation journal. A rejected plan
  restores the staged state exactly.
* `internal/surface/lifecycle_names.go` installs those contract operations.
* `authority.go` routes them; `exec_create` first wakes a suspended sandbox and
  `sandbox_create` resolves recipes, saved templates and warm-pool claims.
* `lampd --state-admin '<json argv>'` names the supervisor's root-only state
  administration, which Lamp uses to discard saved state it no longer
  references (resumed, deleted or expired). Without it, every capture is kept.
* Merged with `main` at `a530c12` (T9 and the budgets ticket). T9 owns `sandbox_suspend`/`resume`,
  snapshots, `template_get` and blob reads. A suspend writes T9's snapshot
  record for its capture and retires it on resume or delete. Clean-export
  templates and blobs T9 created are read through T9's handlers.
* `lamp-engine`: a resumed sandbox allocates again under its existing custody
  record instead of failing with "custody record already exists". Restoring a
  sandbox's own capture (it is recorded as that capture's parent, durably, so
  this holds after a supervisor restart) authorizes it in place. Before this,
  it was inherited and made the sandbox its own capture's grandparent, which
  the supervisor rejects at startup as a cyclic custody store. One such cycle
  from a run before the fix was removed from this ticket's private store copy
  by hand (backup kept beside it).
* Activity wake for the separate fronts: `surface.WakeSandbox` resumes a
  suspended sandbox through the public API (`sandbox_get`, `sandbox_resume`)
  and waits for `ready`. T18's console calls it on `shell <sandbox>`. T12's
  port ingress calls it after a request is authorized and audited, when
  `lamp-network` is given `wake_api_socket` and `wake_token_file`. Message wake
  belongs to T21's sessions, which call `Service.Wake`.
* `lamp-supervisor grant-install --max-generation N`: the control grant may
  authorize a generation range. A resumed sandbox runs as a new generation,
  with its own guard audit spool and identity barrier; nothing bound to the
  earlier run carries over. Without the flag, grants keep the exact match.

## Limits of what is shown

* Volumes and caches have copy semantics. An attachment materializes the
  current tree into the guest; a read-write detach (or `cache_publish`) collects
  it back by content address. There is no live shared mount. A read-only
  attachment is a copy marked `a-w`; the task's namespace root can still write
  its own copy, which is never collected.
* Guest tasks run in a user namespace and can write only `/tmp`, `/var/tmp`
  and `/dev/shm` in this image, so the demonstration's paths live under
  `/var/tmp`. Change sets default to `/workspace` (`labels.repository`
  overrides it).
* Each task exec runs in its own PID namespace, so the idle observer cannot see
  other execs' processes directly. It counts them in the guest-wide task
  cgroup, reads the shared run queue and socket table, and accepts a parked
  harness only through a marker that its live process holds `flock` on. The
  per-exec process counts it relies on (2 for the observer's exec, 3 plus the
  task's own for any other) were measured on this guard.
* Sessions, Tasks, continuation and messages belong to other tickets;
  `Service.Wake` is their hook. Exec, SSH `shell` and allowed dev requests are
  wired here. The dev-request wake is not demonstrated end to end: the network
  service on this lane is not attached to lampd's sandboxes, and a published
  port is bound to one sandbox generation, so a resumed generation needs its
  port published again (T12).
* An expired, suspended sandbox with a read-write volume attached cannot be
  woken to write back, so Lamp keeps it instead of deleting it. A change set's `sb_` id stands in for a session id.
  `sandbox_resume` with continuation fields returns `CONTINUATION_UNSUPPORTED`.
* Replicated durability, remote backup hosts and auth modes other than `none`
  return `QUORUM_REQUIRED`, `HOST_NOT_TRUSTED` or `INVALID_ARGUMENT`. They need
  hosts and custody this lane does not have.

## Verification

`make bootstrap verify` passed once on the final tree, on the M5 Mac:
[`make-verify.log`](evidence/t19/make-verify.log). No tests were added.
