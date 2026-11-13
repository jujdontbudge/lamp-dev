# T32 implementation evidence

A signed candidate was installed into a disposable prefix on the owner's
Ubuntu amd64 host, then its public CLI created a real Firecracker VM and ran
Python in it. The candidate archive included the engine, Firecracker/jailer
and compressed Linux seed. A loopback HTTP origin stood in for the release
server; the install script's digest and signature checks ran normally with a
disposable signing key. This was a fresh prefix, **not a freshly provisioned
OS**: the host already had its virtualization dependencies. No public release
was published, and the public URL still serves the older 0.1.1 path.

The run printed:

```text
hello from the signed Lamp candidate
x86_64
42
install to code and cleanup seconds: 165.473
```

That observation exceeds the ticket's two-minute target. It is reported as
observed on the shared host, not replaced with another timing run.

The default template listed as ready with 2 vCPUs, 2048 MiB and a 20 GiB disk.
A subsequent zero-argument `sandbox create` succeeded. Rebuilt CLI fixes were
then checked against this same installed daemon and VM: separate stdout
`out`, stderr `err`, exit 7; code after daemon restart; positional IDs and global
credential flags. The final credential and cleanup check also demonstrated
MCP's automatic local credential discovery, `LAMP_TOKEN_FILE`, sandbox deletion,
and removal of the disposable engine. Remaining Firecracker count was 1,
matching the other sandbox present before this installation. No owner engine,
Docker object or visible app was changed.

The signed candidate's CLI exposed additional bugs during that continuation;
the source fixes were exercised with the rebuilt CLI using `LAMP_PREFIX` to
point at the installed daemon. These later CLI binaries were not re-published
or described as matching the earlier archive's signed digest.

## Preserved failures and fixes

- `01-long-socket-failure.log`: engine/template setup worked, but creating the
  sandbox hit the jail audit socket length limit. Engine paths were shortened.
- `02-admission-wait-failure.log`: the sandbox became ready, but the CLI treated
  exec admission as completion. It now follows public output cursors until
  completion and then reads the exit status.
- `03-signed-install.log`: the install-to-code flow worked; the extra stream
  assertion then found the CLI reading `stream` instead of the API's `channel`.
- `04-streams-restart-and-flags.log`: the stream correction and daemon restart
  worked; the global-flag check then found argument reordering ahead of a
  positional ID. Global flags now remain after positional IDs and before `--`.
- `05-credentials-and-cleanup.log`: the corrected argument path, MCP, token
  override, deletion and engine removal succeeded.

Transient demonstration-script errors (a local HTTP readiness race and a
Python indentation error) did not reach product operations. No historical
failure above was relabeled as a passing result.

## Checks

`make build` passed: C launcher, `go build ./...`, SDK generation consistency
(no SDK implementation changes), and `cargo build --workspace --locked`.
`build.log` records the final cached build. Go vet passed for the changed CLI,
installer, control, surface, release and localconfig packages. The existing
`TestCLIUsesCanonicalMutationHeaders` smoke passed. Public generation checks,
Python compilation, shell syntax checks, helper `--help`, rejection of an
unknown helper option, and `git diff --check` passed. No tests were added by
the implementation agent.

The two reviewer-authored HTTP regressions were subsequently reproduced
failing and retained after the fixes; both now pass. The ledger is in
`sol-review.md`. `cargo fmt --all --check` also reported preexisting formatting
in `crates/lamp-engine/src/engine.rs` and `crates/lamp-guard/src/audit.rs`;
those unrelated lines were not changed. The changed VZ CLI file passed its
targeted `rustfmt --check`.

## Additional platform demonstrations

The coordinator requested the same signed install-to-code path on the M5,
the 8 GB M2 Air and Linux arm64. The owner authorized only noninteractive
`sudo -n` for disposable Mac installations and cleanup, including removing
their isolated worker accounts. No system launch items were bootstrapped,
no Keychain writes or visible app actions occurred, and existing Lamp
installations were left alone. Both Mac installations used process services.

`06-m5-signed-install.log` and `07-arm64-signed-install.log` record initial
signed candidates from `30d6893`: fresh prefixes `/tmp/l32m5` and `/tmp/l32la`,
ready default templates, real Linux `aarch64` execution and Python `42`,
then sandbox deletion and removal of their disposable engine installations.
Linux arm64 used the already-provisioned `lamp-la` Lima VM on the M5 with
nested KVM, matching the earlier tester's execution location. It had zero
Firecracker processes and 6675 MiB available before the install. Its sole
guest used 2048 MiB and was released; the final Firecracker count was zero.
These are fresh installation prefixes, not newly provisioned operating systems.

The Air's first attempt is preserved in `08-air-capacity-failure.log`.
It refused a 2048 MiB guest plus 256 MiB VMM allowance with only 1183 MiB
available. The engine's 2048 MiB physical reserve caps total VM allocations;
it is not an additional charge against available pages. No sandbox was
accepted. That attempt exposed failed-setup cleanup leaving disposable
worker accounts and engine files; those leftovers were explicitly removed.

The revised installer uses the engine's own memory observation to select a
2048, 1024, 512 or 256 MiB Mac default, accounting for the VMM allowance and
leaving one quarter of currently available pages for host activity. The
engine's admission checks and physical reserve are unchanged. The selected
resources are recorded in the installed template and used by CLI/MCP/HTTP.
`09-m5-adaptive-signed-install.log` shows this revised candidate, built from
`857b2e0`, selecting 2048 MiB and completing the full M5 flow again.

`10-mac-failed-setup-cleanup.log` demonstrates cleanup against a deliberately
created disposable group collision: setup creates its first account, refuses
the second account's preexisting group, then removes only its own account,
group and engine paths. The collision group remains until the demonstration
removes it. No owner account was involved.

`12-air-save-failure.log` preserves the next genuine Air failure: the installer
selected a 512 MiB guest with 1302 MiB available (1288 MiB at preparation),
then VZ's saved-state operation failed from the SSH bootstrap context. The
existing T8 evidence documents why: VZ needs the console login session's
Secure Enclave keys. The installer now invokes template preparation and the
supervisor through `sudo -n launchctl asuser <caller-uid>`, retaining the
isolated worker accounts. This adopts the login context without bootstrapping
a launch item or interacting with the screen. The failed attempt's accounts
and engine directory were automatically removed by the cleanup fix.

`13-air-signed-install.log` records the corrected candidate from `c0c0146`.
With the owner's everyday apps left open, the Air reported physical 8192 MiB,
available 1176 MiB at preflight and 1161 MiB at preparation, and a physical
reserve of 2048 MiB. The installer selected a 512 MiB guest with a 256 MiB
VMM allowance. Template preparation completed in the login context; the
installed CLI created a real VM and printed `Linux aarch64` and Python `42`.
Its default template and sandbox records both report 512 MiB. The sandbox
was deleted and the installation removed; both worker users and groups,
the engine directory and the runtime directory were confirmed absent.
No everyday app was stopped, and the engine's memory checks were unchanged.

| Host | Signed install through code and sandbox cleanup | Default guest memory |
| --- | ---: | ---: |
| Ubuntu amd64 | 165.473 s (original observation) | 2048 MiB |
| M5 | 38.113 s initially; 49.316 s after adaptive-default change | 2048 MiB |
| Linux arm64 nested-KVM VM | 46.805 s | 2048 MiB |
| 8 GB M2 Air, apps open | 103.468 s after memory and login-context fixes | 512 MiB |

The M5 rerun demonstrated the changed default selection, not another timing
trial. These observations use local HTTP origins and exclude publishing and
release assembly; they do not replace the original Ubuntu missed target.

Candidate archive SHA-256 values (disposable Ed25519 release signing key):

- Initial Mac: `f317a94dbc0b19e4e00e15cc34c0c5c9fcd5c82639334f9709639aae8b39019e`
- Linux arm64: `8d3a03a7c463dbd7cb018aa80a3f6707f0bbc5918561289e9ccc085ae2b3d5f2`
- Adaptive M5: `22447d4bc597c4c72bea073c5694cc47b1c2adfd45e8e664142dcf329f0c4dde`
- Final Air: `c8242d7d11d7c41f1fc090ea37de3d46c03f26fd9ed9189e72ac7d29051ea116`

The candidate archives include compressed seeds, not tester-side kernel
builds. The Mac prepared seed is Ubuntu arm64; the Linux arm64 seed uses the
earlier prepared Firecracker kernel/rootfs in its own signed seed format.
The Mac VZ worker is ad-hoc signed with its Virtualization entitlement for
these local candidates; this does not establish production notarization.
An Air transfer optimization reassembled the same signed payload locally.
AppleDouble sidecars introduced by that temporary overlay tar were correctly
refused as unsigned files (`11-air-sidecar-refusal.log`); the corrected archive
omits them. This was a candidate assembly error, not a signature bypass.

## Limits

This evidence does not establish Mac DNS/HTTPS behavior or automatic gVisor
enrollment on hosts without KVM; that enrollment remains unavailable and the
installer explicitly refuses those hosts. Fresh-host apt dependency
installation was not exercised because the Ubuntu hosts had dependencies.
The Ubuntu two-minute target remains missed as originally observed.

The public URL still needs complete platform archive publication. The
coordinator explicitly accepted branch-built signed candidates for T32's
installation demonstrations; publication is not required for this ticket.
