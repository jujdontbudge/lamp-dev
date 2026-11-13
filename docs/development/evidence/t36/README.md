# T36 real VM evidence, 2026-11-12

These are observations from disposable Firecracker sandboxes on the owner's
Ubuntu laptop, using a public Ubuntu seed, disposable credentials and random
binary data. Both sandboxes were deleted after the demonstration. VM admission
checked the shared two-VM limit and at least 12 GiB available immediately before
creation. No existing containers, owner repositories or browser profiles were used.

`exec-files.json` records HTTP exec and file calls, one-step CLI copy, and a
lampd restart. Both streams returned exactly 8,388,608 bytes without truncation;
a five-second command finished under a three-second worker request timeout.
Timeout, interrupt, binary stdin, environment (including an empty value), cwd,
exit 7, execution after `unshare -m`, and a 16 MiB random binary round trip
worked. The binary SHA-256 matched before and after. Unicode read/list/stat,
content search, NOT_FOUND and invalid-NUL-path errors worked. Completed output
survived daemon restart.

That first run's PTY output includes an actual exit-2 job-control error. It is
preserved here. The fix acquires the controlling terminal inside the task PID
namespace, before entering its user namespace. `ssh-files.json` records the
fixed PTY: 24x80, resize to 40x100, Ctrl-C, RECONNECTED, persistent `/tmp`, and
exit 0. It also records stock OpenSSH, scp, rsync, a binary local-forward response
from guest loopback, file watch, rename and delete. The minimal seed lacks rsync;
the demo copied the public Ubuntu rsync executable and missing libraries into
`/tmp`, with a disposable wrapper setting LD_LIBRARY_PATH.

Earlier iteration failures remain failures: an empty environment value caused
the worker's wire validator to reject Go's explicit empty map value; startup
waited too briefly for template verification; two attempts declined VM creation
because both shared slots were occupied; an interrupted demo left a stale local
socket; and an earlier concurrent long command returned outcome_unknown while
the flood completed. The latter run did not retain the underlying reason. The
fresh isolated run records the later successful observation without relabeling
the earlier result. Subsequent failures exposed rsync-path dispatch and missing
seed libraries, both addressed before the recorded successful SSH run.

Local checks: `make build`, targeted `go vet` for cmd/lamp and internal
control/access/surface, and `cargo clippy --locked -p lamp-init -p lamp-engine
-p lamp-guard -p lamp-supervisor -- -D warnings`. Public and SDK generated
bindings were checked for consistency. No new automated tests were added for
these demonstrations. The pinned protobuf generators were invoked directly
when the wrapper could not fetch Go metadata during the network outage.

Limits of this evidence: runtime observations are Linux x86_64. Mac paths were
compiled on the M5, not demonstrated in a VM. No 500 MB transfer, full-disk run,
ten-minute command, GUI terminal editor, or release latency benchmark is claimed.
Q3 measurement remains a release task. SDK convenience work remains deferred;
generated artifacts stay consistent with the canonical contract.

Sol review found that the CLI and SSH shells omitted TERM. The retained reviewer
regression test failed before the fix and passed afterward. `terminal-type.json`
records a new disposable VM observation: both CLI and stock SSH passed vt100
through to the guest, ran `tput clear` successfully and exited 0. The VM was
deleted after that demonstration.
