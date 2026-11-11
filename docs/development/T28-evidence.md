# T28 — install, update, recovery and SDKs: evidence

One end-to-end run on the owner's three machines, driven from the M5 by
[`tools/install_demo.py`](../../tools/install_demo.py) on 2026-11-11.
[Transcript](evidence/t28/transcript.jsonl) (one JSON line per step, with command, exit code and output).
Owner work on every host is the public `psf/requests` v2.32.3 source tarball
(SHA-256 `f665576f…`). It was uploaded through the generated Python SDK,
published as an artifact and backed up.

| Host | Service | Platform |
| --- | --- | --- |
| Ubuntu laptop | systemd unit `lamp-t28demo.service`, enabled | linux/amd64 |
| M5 | `process` mode (detached lampd, pidfile) | darwin/arm64 |
| MacBook Air | `process` mode | darwin/arm64 |

## What was shown

* **Signed install.** Release key and owner recovery key were generated in
  `~/.config/lamp/t28-demo/keys` (outside the repository, never printed). Each
  release is a directory holding `manifest.json`, an Ed25519 `manifest.sig` and
  `payload/`. Install verifies the signature, platform and every file digest,
  pins the release public key and starts lampd. The manifest lists the exact Go
  module graph of each binary (`components`) and ships `LICENSE`,
  `THIRD_PARTY.md` and the relay AGPL text, so licenses travel with the release.
* **Restart.** `systemctl restart` on Ubuntu came back active. The saved artifact
  downloaded with the same SHA-256.
* **Automatic updates start disabled.** `update-policy.json` starts as
  `{"automatic":false,"max_unavailable":1}`, and a timer-style `auto` run does
  nothing. After the owner opted in on the Air, `auto` applied a release, which
  still went through verification and rollback.
* **Release trust.** A copy of 0.2.0 with one payload byte flipped was refused
  before apply ("payload file bin/lamp does not match its signed digest").
  Installing an older signed 0.1.0 over 0.2.0 was refused by anti-rollback.
* **One host at a time.** `lamp-installer rollout` moved 0.2.0 through Ubuntu,
  then the M5, then the Air. Each remote host verified the release with its own
  installed, pinned-trust installer. Before stopping lampd (SIGTERM drain),
  each host's API inventory (sandboxes, volumes, caches, artifacts, backups,
  blobs) was compared with the inventory after the restart. Every artifact still
  verified byte for byte.
* **Stop and roll back.** 0.3.0 is signed correctly, but its lampd cannot start.
  Ubuntu failed its check and rolled back to 0.2.0 with all 4 saved items. The
  rollout stopped with `not_updated: [local, johansardars-macbook-air]`, and
  both Macs stayed on 0.2.0.
* **Uninstall choices.** Uninstall with no `--data` choice refused and changed
  nothing. `retain` (M5) kept the state directory. `export` (Air) wrote a
  verified copy of the owner data and also kept the original. `delete` needs
  `--confirm-delete <name>`. On Ubuntu it removed the prefix, the systemd unit,
  the state and the host backup key.
* **Recovery on a replacement OS.** Every new backup's data key is wrapped twice:
  once to the host key (`A256GCM`) and once to the owner's offline recovery
  public key (`X25519-HKDF-SHA256-A256GCM`). The backup reports
  `recovery_state: available` and both `key_envelopes`. A host without an
  enrolled recovery key reports `unavailable`. The Ubuntu backup archive was
  copied offsite before Ubuntu was deleted. A fresh install on a separate
  prefix on the Air then recovered it onto a different OS. With the wrong
  recovery key the import was refused, nothing was imported and the normal
  service restarted. With the owner's key, `lamp-installer recover` ran lampd
  once with the key, rewrapped the data key to the new host and imported the
  archive. `backup_restore` through the SDK then restored the artifact with the
  original SHA-256. Afterwards the key appeared in neither the service
  arguments nor the state.
* **SDKs.** `tools/sdkgen.py` generates `sdk/typescript` (ESM JS plus `.d.ts`,
  clean under `tsc --strict`) and `sdk/python` (stdlib-only client plus
  TypedDicts) from `contracts/public/schema.json`, with one method per canonical
  operation (328). The Python SDK did all the owner work above. The TypeScript
  SDK listed backups and created a blob on the M5. `make sdk-pack` builds
  `dist/lamp-sdk-0.1.0.tgz` and `dist/lamp_sdk-0.1.0-py3-none-any.whl`, each
  with the license. Nothing was published.

## Fleet updates through Lamp (run five, 2026-11-11)

`tools/fleet_update_demo.py` formed a three-host Lamp fleet: the Ubuntu laptop
(systemd), the M5 and the MacBook Air (user LaunchAgents in `gui/<uid>`). The
transcript is `docs/development/evidence/t28/fleet-update/transcript.jsonl`.

* **Mac service.** Both Macs loaded `si.eilmer.lamp.t28fleet` and its `-agent`
  with `launchctl bootstrap gui/<uid>`. No system daemon was used. The M5
  service restarted with `launchctl kickstart -k` and came back healthy.
  Uninstall booted both out and left no plists.
* **Off by default.** After 3.1.0 was published to every host's mirror, the
  fleet's `update_policy` showed `automatic: false` and Lamp created no Update.
* **Trust.** `update_create` with a tampered release returned
  `INVALID_ARGUMENT` ("release signature does not verify against the pinned
  release key").
* **Automatic rollout.** `lamp fleet update fl_local --update-policy-json
  '{"automatic":true,...}'` returned rc 0. The leader then created the Update
  itself. `update_get` showed it go `draining` on hs_air, then `pending` and
  `draining` on hs_lx, then on hs_m5, and finally `completed`. That is one host
  at a time. All three hosts reported 3.1.0 and healthy. The `update_policy` in
  that record reads `automatic: false`. The read went to a follower before it
  applied the commit; the demo now waits for the change to show up. The
  automatic Update itself, which Lamp creates only when the policy is on, shows
  the change took effect.
* **Active work kept.** A real VZ sandbox (Ubuntu 26.04) on the M5 ran a guest
  process that wrote a heartbeat to tmpfs. Across the M5 update it kept the same
  VM boot id and pid, beats went from 2 to 45 with no gap over 2 s, and the
  sandbox stayed `ready`. At the end it was still alive with 158 beats.
* **Broken release stops the rollout.** A signed 3.2.0 whose lampd fails its
  health check went `draining` on hs_air, failed, was rolled back and ended
  `rolled_back`. hs_lx and hs_m5 stayed on 3.1.0, and hs_air came back on 3.1.0
  healthy. Lamp created no new Update for that digest.
* `update_get` carries `metadata_versions`, `security_revocations` and
  `migration` from the signed index.
* Cleanup deleted the sandbox, uninstalled all three hosts, revoked the
  supervisor grant and closed the tunnels.

## Supervisor swap and `update_rollback` (2026-11-11)

Transcript: `docs/development/evidence/t28/fleet-update/supervisor-rollback.jsonl`
(`PHASE=supervisor tools/fleet_update_demo.py`). This run reused the fleet
setup above. It did not repeat steps that had already passed, such as the
tamper refusal, the broken release and the policy staying off by default.

* **The VZ supervisor is a release component.** Darwin releases now ship
  `bin/lamp-supervisor` and `bin/lamp-vz`. The installer copies a release's pair
  into a root-owned directory (`/usr/local/lib/lamp-<name>/<version>/`), checks
  the copies against the signed bytes and runs the supervisor as root with
  `sudo -n`. It also gives lampd the new supervisor PID, since lampd
  authenticates its worker by PID. Both releases were built from this code: 4.0.0
  ships supervisor `c4434e9a…` and 4.1.0 ships supervisor `b6daef46…`. The two
  builds differ only in their embedded `supervisor_release`. `lamp-vz` is the
  same build in both, ad-hoc signed with its virtualization entitlement.
* **Roll forward.** Automatic updates rolled 4.1.0 through hs_air, then hs_lx,
  then hs_m5. On the M5 the agent did the following:
  1. Suspended the running sandbox through Lamp's API (`sandbox_suspend`,
     durable).
  2. Stopped supervisor 4.0.0 and started supervisor 4.1.0.
  3. Started lampd 4.1.0.
  4. Resumed the sandbox (`sandbox_resumed`).
  5. Checked that saved and active work were unchanged.

  The update completed. The guest kept the same VM boot id and the same
  process (pid 141), and that process was alive afterwards.
* **Active work is suspended and resumed, not kept running.** VZ machines run
  inside their supervisor's worker processes, so they cannot outlive a
  supervisor restart. The sandbox's memory was saved to disk and restored. The
  guest's heartbeat shows the pause: its largest gap was 10 s across the
  roll forward and 11 s across the rollback.
* **`lamp update rollback <update>`** (`POST /v1/updates/{id}:rollback`) moved
  the completed Update to `lamp.phase=rollback`. It took the hosts back one at a
  time, last updated first: hs_m5, then hs_lx, then hs_air, ending
  `rolled_back`. Each host first re-checks the target release. Its staged files
  must match their signed manifest, and that manifest must match a signed index
  in the host's mirror. Then the host goes through the same transition and the
  same work check. On the M5 that meant suspend, swap back to supervisor
  4.0.0, resume. All three hosts ended on 4.0.0, and the M5 again ran
  supervisor `c4434e9a…`. The guest was the same process and still alive. A
  failed rollback check sends the host back to the release it was on and stops
  the rollback (`failed`).
* **Fixed while getting here:**
  * The updater now waits up to 10 minutes for a release to reach its own
    mirror. Before, an update the leader created faster than the release was
    published reached the Air before the release did, and the Air failed.
  * Draining waits up to the drain timeout for running commands, which
    block a suspend.
  * Suspend and resume are followed to the end of their operation, so a
    refusal fails the update quickly instead of hanging it.
  * `install` now mirrors its own signed release, so a rollback to it can
    verify it.
* **Templates are bound to the engine digest.** A Linux template's saved
  memory is bound to the `lamp-vz` digest. The supervisor installed under
  `/usr/local/lib/lamp` was too old for this code: it ignored `max_generation`,
  so it could not resume, and its `lamp-vz` rejected the current spec. So both
  releases ship this code's `lamp-vz`. An early try with that `lamp-vz` against
  the shared M5 store quarantined the shared template `tp_12cf…`. I moved it back
  unchanged, and the old supervisor verified both templates again. The demo then
  used a disposable store (`/private/var/lamp-t28`) holding a template
  qualified for this `lamp-vz`. That template was built from the shared
  template's own kernel and root filesystem. The store was removed afterwards.
  A release that changes `lamp-vz` bytes would make existing Linux templates
  unusable until they are prepared again; this demo's releases do not change
  them.

## Review fixes (2026-11-11)

All nine review findings are fixed in `cmd/lamp-installer`, `internal/release` and
`internal/control/updates.go`. #1, #8 and #9 were shown once each for real. The
others were built (`go build ./...`) but not run separately. No tests were
added.

1. **A failed supervisor start no longer leaves the host without a
   supervisor.** Stopping the supervisor records `version: ""`, and a transition
   swaps whenever no supervisor is alive. Shown on Ubuntu with signed release
   5.2.0, whose supervisor exits at start: `supervisor_start_failed`, then
   `supervisor_swapped from "" to 5.1.0`, `sandbox_resumed` and `rolled_back`.
   The guest process stayed alive.
2. **Revocations are enforced.** They are gathered from every verified index in
   the mirror. They are checked in `update_create`, automatic updates (a
   revoked release is never chosen as newest), `update_rollback` targets and on
   the host for updates and rollbacks.
3. **A failed or rolled-back release can be retried.** A staged release is
   reused only if it still passes `VerifyStaged`; otherwise it is removed and
   staged again.
4. **Only `ready` sandboxes are suspended, and they come back `ready`.** The
   schema has no `running` state for a sandbox.
5. **The work check covers this host only.** It looks at sandboxes and
   resources this host runs or holds a copy of, and it fails only if an
   earlier item is missing or its digest changed. The host id is now lampd's
   own: the fleet config's `host_id`, else `hs_local`. It is never a caller
   flag. An earlier run that passed a mismatching id skipped the suspend and
   lost that demo's VM. The work check caught it and failed the update, and
   the flag is gone.
6. **The channel the coordinator checked travels in `update-request.json`**,
   and the host checks the release against that channel.
7. **The agent passes the Update's release digest.** `applyUpdate` refuses if
   the digest from `VerifyPlatform` differs.
8. **An engine change cannot strand saved work.** Releases sign their engine
   identity in the manifest (`engine`: each shipped engine binary's digest and
   `snapshot_format`, required when an engine is shipped). The updater refuses
   an engine change while the store holds machine state made by the running
   engine: templates, captures, or running or suspended sandboxes. It refuses
   before anything is changed. Shown on the M5
   (`evidence/t28/review/engine-guard.txt`): 6.0.0 (`lamp-vz` cc0d0222…,
   format 1) to 6.1.0 (`lamp-vz` 7e3e1128…, format 2) was refused. The error
   names the sandbox and the template. Afterwards it was the same version, the
   same supervisor pid, and the sandbox was still `ready`. The other option,
   keeping the old engine for old snapshots, was not built.
9. **Linux swaps its supervisor and engine like the Macs.** The supervisor set
   is per OS: `lamp-vz` on macOS; `firecracker` and `jailer` on Linux. It runs
   from a root-owned release directory under both systemd and launchd hosts.
   Shown on the Ubuntu laptop (`evidence/t28/review/linux-supervisor-swap.txt`,
   script `tools/t28_linux_supervisor_demo.sh`). The demo used a disposable
   store (`/var/lib/lamp/t28`, own uid range and `/run/t28`) with a template
   built from a copy of a seed. One sandbox ran; the laptop had 0–1 other
   firecracker processes and about 26 GiB available. The steps:
   * Install 5.0.0 under systemd.
   * Update to 5.1.0: suspend, swap to supervisor 833b0b51…, resume, completed.
   * Update to 5.2.0: the broken supervisor is reverted as in #1.

   The guest's tmpfs heartbeat kept counting (2, then 4, then 5) and the process
   stayed alive. The guest boot id here comes from the template snapshot, so the
   surviving in-memory heartbeat is the evidence. Afterwards the store, units and
   mounts were removed.

## Not done or not run (stated plainly)

* **No Apple Developer ID signing, Installer package or notarization.** Creating
  the missing Developer ID Installer certificate needs keychain writes, and
  using the private key with `codesign` can show a keychain prompt on the
  owner's screen. Both are off limits here. Mac releases are ad-hoc built
  binaries trusted through Lamp's own Ed25519 release signature.
* The dashboard consent screen belongs to T27.
* **No reboot test.** Other tickets' daemons were live on the Ubuntu laptop, so
  it was not rebooted. The unit is `enabled` for `multi-user.target`.
* **Only one Linux host.** Debian, Fedora and arm64 Linux were not run (no such
  hosts exist).
* **No fleet membership revocation on uninstall.** These single-host installs
  had no fleet enrollment.
* **No timing measurements**, per the owner rule.
