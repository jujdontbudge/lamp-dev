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

All four parts ran against one build of the final tree (after merging
`main` with T9): `lampd` SHA-256 `9f0e11e3…735fb9` and `lamp-supervisor`
`620d3aab…afaa839`. Every run ends with the supervisor's remaining captures
listed after lampd stopped; in all four that list is empty.

## A — idle suspend, wake, overrides, expiry (LIF-001/002/003)

[Transcript](evidence/t19/A/transcript.jsonl), sandbox
`sb_1066bf98…` created with `lifecycle {idle_action: suspend, idle_grace_seconds: 600}`.

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
  `state=suspended`, snapshot `sn_71a87194…`, and no Firecracker process of
  ours left running. `snapshot_get` returns the suspend's record: `ready`,
  `preserves_memory: true`, generation 1, `durability: local`,
  `consistency: application`. After the resume below, the same record reads
  `deleted`.
* **A full restart while suspended.** lampd and the root supervisor were both
  stopped and started again, with a new control grant. The sandbox read back as
  `suspended`.
* **Wake by exec.** An ordinary `exec_create` wrote the person's message into
  the FIFO. Lamp resumed the sandbox first, as generation 2. The harness
  (still pid 7) answered with `nonce_from_memory = 38386d3e…0f4b2f`, exactly the
  nonce it had shown before the suspend. The guest's uptime read 97 s, so this
  was a memory restore, not a cold boot.
* **Retention (LIF-003).** With `suspended_retention_seconds: 20`, a suspended
  sandbox was deleted by the monitor. Its saved states (`cp_71a87194…` from the
  earlier suspend and `cp_8fa2a9fd…` from this one) were discarded from the
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
* `change_set_create` C2 → P found base `112ef204…`, commits `[072dbd11…]` and
  stored the binary patch as a blob. `change_set_apply` applied it with a git
  3-way apply and committed it in P (`1ea1a4d6…`). P's log then
  read `Apply change set … / parent docs / base`, `app.py` held B's version,
  P's README edit was kept, and B's test passed inside P.
* C2 was deleted. `change_set_create` C1 → P **woke the suspended C1**
  (journal: suspended → resuming → ready) to read its commit `7f716110…`. Its
  apply hit the winner's edit to the same line. The change set became
  `conflicted` with `conflicts: ["app.py"]`. P's HEAD was still `1ea1a4d6…`
  with a clean worktree and B's `app.py`; nothing was overwritten. The loser was
  then rejected.

## C — saved templates, recipes, warm pools (TPL-002, TPL-003, LIF-005)

[Transcript](evidence/t19/C/transcript.jsonl).

* **Save installed tools.** A sandbox installed `/var/tmp/tools/bin/greet` and
  also wrote an unselected file. `sandbox_save_template` with
  `selected_paths ["/var/tmp/tools"]` built `tp_c37da052…` (`build:
  clean_copy`, root object `cp_c37da052…`). It ran in a fresh builder sandbox
  from the base template, which was released afterwards.
* **Tainted source.** The source then got a planted `bin/canary` and
  `capture_trust: rebuild_required`. A copy-save returned
  `CLEAN_REBUILD_REQUIRED`. A reconstruction plan (the tool's bytes uploaded as a
  pinned blob plus one command) built `tp_61524f70…` (`build: reconstructed`)
  in a clean builder from the plan alone.
* **Recipes.** `recipe_create greet` → `rc_8d95ae46…` (revision 1). An update
  with a wrong revision returned `REVISION_CONFLICT`; the right one created a new
  immutable version `rc_4f4e0ad5…` (revision 2, new digest). Both versions are
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
  produced generation `a338ae3a…`; a second publish on the old base returned
  `REVISION_CONFLICT`. B attached the cache read-only and read identical SHA-256s
  (`2771cb6d…`, `2d27fbdf…`).
* **Named volume with several attachments (STO-002).** A and B both attached
  `shared_files` volume `notes` read-write, wrote different files and
  detached. On reattach both files were present. When both edited
  `shared.txt`, the second detach returned `RESOURCE_IN_USE` ("changed on both
  sides since attach") and kept the attachment, so nothing was overwritten. An
  `exclusive_block` volume refused a second writer with `RESOURCE_IN_USE`.
* **CI output outlives its job (STO-003).** A 1 MiB random file was transferred
  out of A to a blob, and `artifact_create {job_id: job_ci_1}` published it as
  `complete`. A was deleted, then the artifact downloaded in verified chunks with
  the guest's SHA-256 `b258aa46…`. A manifest naming one missing entry produced
  an `incomplete` artifact (`present_bytes 11` of `expected_bytes 4107`).
* **Owner-only (STO-004).** The fleet was switched to team mode, and a real
  member principal was enrolled with its own bearer credential. It listed zero
  caches, volumes, artifacts, blobs and backups while the owner had rows. Its
  `get` of the owner's artifact, volume and cache returned `NOT_FOUND`, as did
  its download. Creating a `shared` volume returned `SCOPE_DENIED`.
* **Encrypted backup and restore (STO-005).** `backup_create` sealed the volume
  (`bk_52ad5ced…`, AES-256-GCM, manifest digest recorded). The volume was
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
* Merged with T9 (`main` at `a3f5723`). T9 owns `sandbox_suspend`/`resume`,
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
* Sessions, Tasks, continuation, messages, SSH attach and dev requests belong
  to other tickets. `Service.Wake` is the exported hook they call; this ticket
  wires the exec path. A change set's `sb_` id stands in for a session id.
  `sandbox_resume` with continuation fields returns `CONTINUATION_UNSUPPORTED`.
* Replicated durability, remote backup hosts and auth modes other than `none`
  return `QUORUM_REQUIRED`, `HOST_NOT_TRUSTED` or `INVALID_ARGUMENT`. They need
  hosts and custody this lane does not have.

## Verification

`make bootstrap verify` passed once on the final tree, on the M5 Mac:
[`make-verify.log`](evidence/t19/make-verify.log). No tests were added.
