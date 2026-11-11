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

## Not done or not run (stated plainly)

* **No Apple Developer ID signing, Installer package or notarization.** Creating
  the missing Developer ID Installer certificate needs keychain writes, and
  using the private key with `codesign` can show a keychain prompt on the
  owner's screen. Both are off limits here. Mac releases are ad-hoc built
  binaries trusted through Lamp's own Ed25519 release signature.
* **No launchd load on the Macs.** The `launchd` service mode writes a
  LaunchAgent and uses `launchctl bootstrap gui/<uid>`, but it was not run.
  Loading a login item shows a Background Items notification on the owner's
  screen, so the Macs used `process` mode.
* **No reboot test.** Other tickets' daemons were live on the Ubuntu laptop, so
  it was not rebooted. The unit is `enabled` for `multi-user.target`.
* **Only one Linux host.** Debian, Fedora and arm64 Linux were not run (no such
  hosts exist).
* **No live sandboxes during updates.** Updates preserved saved work: journal,
  artifacts, backups and blobs, checked through the API and by bytes. lampd's
  existing restart reconciliation for running sandboxes was not exercised,
  because no supervisor was enrolled on these demo installs.
* **No Update API resource.** Rollout is driven by `lamp-installer`. The
  `update` resource, `fleet_update` `update_policy` and dashboard consent flow
  from HST-005 are not wired into lampd.
* **No fleet membership revocation on uninstall.** These single-host installs
  had no fleet enrollment.
* **No timing measurements**, per the owner rule.
