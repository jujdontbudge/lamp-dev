# T36 arm64 runtime follow-up

The coordinator requested one runtime demonstration on the M5 and on Linux
arm64, where the original F37 mixed-stream/flood failure was found. These runs
use fresh, disposable installation prefixes on the existing hosts, signed local
candidates, and copies of T32's public Ubuntu arm64 seeds updated with T36's
built guest init/helper/collector. They are not fresh operating systems or a
published release. Credentials and the disposable release key stay outside
this evidence. No Docker objects, existing Lamp installations or visible apps
were changed.

The M5 candidate is from `5ec4827`; its Linux guest runs under VZ. `m5.json`
records one sandbox and all five requested checks:

- The public CLI returned exactly 8,388,608 `x` bytes on stdout and 8,388,608
  `y` bytes on stderr, with exact expected SHA-256 values and exit 0.
- An additional 8,388,608-byte stderr-only flood returned with empty stdout;
  the next exec printed `AFTER_STDERR_FLOOD` and exited 0.
- The HTTP API honored a named environment variable, an explicitly empty
  variable, `/tmp` cwd, and 14 bytes of binary live stdin including NUL/0xff;
  stderr remained separate and the command exited 0.
- Public CLI `sandbox cp` moved 16,777,216 random bytes into and out of the
  guest. The source and returned SHA-256 matched.
- Public CLI `sandbox shell`, attached to a local PTY, accepted interactive
  input, reported 24x80 then 40x100 after resize, recovered from Ctrl-C during
  sleep, retained `/tmp` cwd, and exited 0. The transcript is retained.

`m5-install.log` records template preparation and Linux aarch64 execution.
`m5-uninstall.log` and `m5-cleanup.json` record sandbox deletion, uninstall,
and absence of both disposable worker users/groups, the engine directory,
the runtime directory and the prefix. Process services were used, with only
noninteractive `sudo -n`; no system launch items were bootstrapped and no
Keychain writes occurred.

## Preserved setup failures

No runtime check is claimed for a failed installation:

1. The demonstration script initially inherited a restrictive 077 umask. Root
   engine-library directories became unreadable to the installer's unprivileged
   digest check, which rejected the copy. The first occurrence did not retain
   its subprocess output; a retry against that partial prefix then refused an
   existing mirror file. `01-la-restrictive-umask.log` retains the same digest
   refusal at a fresh prefix. Subsequent runs use the normal 022 umask. This
   restrictive-umask installation limitation remains; it is outside T36 exec.
2. My conversion of the Mac-format seed metadata retained the unsupported
   top-level `page_size` field. The Linux seed importer correctly refused it
   (`02-la-seed-format.log`). The candidate was rebuilt with Linux seed metadata.
3. Template boot then exposed the T32/T39 integration defect in
   `03-la-missing-network.log`: the installer omitted `lamp-network` from the
   root-owned component set, although T39 starts it beside `lamp-guard`.
   The product fix adds it to the Linux component list. The normal release
   builder already includes this binary in its signed payload.

## Linux arm64 after the installer fix

`linux-arm64.json` records the same five checks, once, from candidate `5580cfd`
in the existing `lamp-la` nested-KVM Lima VM on the M5. Both output streams
contain exactly 8 MiB with the same expected hashes as the M5; the stderr-only
flood is followed by another successful exec. Environment/empty variable/cwd
and binary stdin pass, the 16 MiB file hashes match, and the terminal transcript
shows input, 24x80 to 40x100 resize, Ctrl-C recovery, cwd and exit 0.

`linux-arm64-install.log` records successful seed import/template preparation.
`linux-arm64-uninstall.log` and `linux-arm64-cleanup.json` record deletion of the
sandbox and installation, absence of its engine/runtime/UID reservation and
all attempted disposable prefixes, and zero Firecracker processes afterward.
Before setup the Lima VM had zero Firecracker processes and about 6.5 GiB
available out of its 8 GiB allocation; one 2048 MiB guest was used at a time.
The shared Ubuntu amd64 laptop was not used for this follow-up.

`candidates.json` identifies the signed manifests and guest binaries. Both
installs used source-built candidates; the M5 has an ad-hoc VZ entitlement
signature for this local demonstration. Production signing/notarization or
public release publication is not claimed. Guest Linux execution under VZ
is now demonstrated; the separate macOS-guest helper is still compile-only.

Validation after the one-line installer fix: `make build` and
`go vet ./cmd/lamp-installer` passed. No tests were added. Earlier Sol-approved
exec/PTY behavior is unchanged; the installer delta receives a separate scoped
Sol review recorded in `../sol-loop.md`.
