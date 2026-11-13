# T44 follow-up: audit stalls and arm64

The coordinator requested a cause/fix for the network failures and actual kit
runs on the M5 Linux guest and the existing `lamp-la` Linux arm64 Lima VM.

## Network failure and fix

The fresh Ubuntu reproduction captured the previously missing cause:

```
network audit fence: watchdog previous_age=1.049012643s
network audit fence: event=heartbeat write_elapsed=2.247715677s previous_age=2.446825275s error=selected network route unavailable
```

The audit write completed slowly during package installation. The director
converted a successful storage barrier into a permanent route failure solely
because it exceeded one second. The watchdog also killed the route while the
write was in progress. Suspend/resume replaced that director, explaining why
manual recovery appeared to help.

Preallocation plus `fdatasync` did not fix this: a later write took 980 ms,
which together with the 250 ms heartbeat interval crossed the cutoff. Moving
audit files outside the VM store also did not fix it: a write took 1.27 s.
Those attempted changes are not claimed as fixes and were not retained.
The corrected demo keeps the installer's normal layout with audit files outside
the loop-backed VM store; both locations ultimately share the owner's disk.

The fix removes the one-second timing-based disconnect. Every admitted DNS
answer, dial and policy change still waits for its durable audit write. Slow
storage applies backpressure; an actual barrier error, full spool or lost helper
still closes the route. Previously admitted connections already have their
admission recorded durably. No audit write, quota or policy check is bypassed.
Actual audit errors now produce a diagnostic in the helper log.

`ubuntu.json` records Python state (42 then 43), Chromium example.com navigation
and screenshot, Alpine pull and `DIND_OK`, TypeScript symbols/completions,
1024x768 desktop input/PNG/MP4, and a final HTTPS 200. It is one generation-1 VM
with no suspend/resume or manual recovery. Its saved audit contains 1,129
records, sequences 1 through 1,129, and an observation gap of 3.133659 seconds;
the managed helper log is empty. Sandbox deletion completed and Firecracker
count returned to zero. The filesystem was shared with normal owner activity;
Chrome, Beeper and the T3 server remained running.

File-read payloads are replaced in transcripts by byte counts and SHA-256;
large LSP arrays retain returned counts, total/truncation and samples. The PNGs
and MP4 are attached separately. `workload.py` is the manual demo's kit sequence.

## Platform preparation findings

The Mac installer omitted `lamp-network` from its installed engine components,
although the release contained it. The new startup diagnostic showed:

```
sandbox-exec: execvp() of '/private/var/lamp-i/3dc3e704/lib/0.1.2/lamp-network' failed: No such file or directory
```

The installer now copies that component on Mac as well as Linux. The Mac
engine includes the helper's error in startup failures, so failed-install
cleanup no longer erases the only explanation.

The initial disposable arm64 image was inconsistent after preparation and
was discarded. Temporary image copies exhausted the Mac disk during that
iteration; `lamp-la` subsequently reported I/O errors and a read-only journal.
Task artifacts were removed and the idle Lima VM was restarted for journal
recovery. Its root filesystem returned to read/write mode; no live filesystem
repair command was run. Owner desktop apps and the T3 server stayed running. The corrected seed is a disposable copy of the public Ubuntu
seed, mounted solely to install this branch's init/helper/guard and then
unmounted. `e2fsck -fn` passed all five phases before packaging. Its hashes are:

- SHA-256: `46d788482d40e5bfb611cfbc8d999e3bf6dd2b3e6fb30cebb00e35f53029c0b5`
- sha256-c4m: `99b04b8e85470c252d636f0926b3cccc1dfbe05cbd17a2f2623c804e51b2b026`

## M5 Mac engine

`m5-install.log`, `m5-demo.log` and `m5.json` show a signed disposable branch
candidate installed with process services at `/tmp/t44m4`. The default Linux
guest boots as `aarch64`. Python returns 42 then 43; Chromium navigates to
example.com and returns its title and PNG; Docker pulls native Alpine and
prints `DIND_OK`; the LSP returns `greet` and completions; the desktop accepts
input and returns PNG and MP4. The final guest HTTPS request returns 200.
All run in the same sandbox, with no suspend/resume or network recovery.

The sandbox was deleted and the install uninstalled with data deletion. The
engine path and both `_lampvm72310`/`_lampvm72311` users and groups are absent.
No system launch item, host Docker resource or visible owner UI was used.

Linux arm64 results are recorded after its run completes.
