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

Linux arm64 results are split across the browser run and a fresh disposable run for the remaining kits, described below.

## Linux arm64 relay diagnosis

The first valid-seed run returned `outcome_unknown` during Python installation.
A diagnostic reproduction retained the VM instead of immediately cleaning up.
`la-prior-failure.json` records an acknowledged Python job whose status poll took
6.273647 seconds against the relay's 5-second timeout. The runtime remained alive
and its saved job result was `{"kit":"python","state":"ready","boundary":"vm"}`.
This was a lost status read, not a failed installation. The daemon now retries
only the read-only result poll for that acknowledged job, within the existing
operation deadline and Task/lifecycle checks. It never resubmits installation.
A result explicitly marked `outcome_unknown` by the runtime remains terminal.

The disposable host receiver diagnostic also captured `Resource temporarily
unavailable (os error 11)`: its 500 ms read timeout closed otherwise healthy
idle streams, consuming new audit segments on reconnect. The receive command
now waits for data or EOF. Guest collectors retain their durable-ACK deadline;
actual storage errors and full spools still close the receiver, and sandbox
teardown still kills it. The failed VM was deleted before the next demonstration.
The signed disposable install was retained, with only its daemon and host guard
replaced by branch builds before creating a fresh generation-1 sandbox.


A later fresh run passed Python but the shared session bridge exited when a
status caller disconnected before its late reply. `bridge-disconnect.py` runs
the actual bridge subprocess: before the fix the bridge exited 0 and its next
call got `ConnectionRefusedError`; after the fix it stayed alive and the next
exec printed 42. The server now handles receive failures and lost replies per
connection. That VM was deleted before the next run.

`la-prior-startup.json` records the next initial session readiness read returning
`DATA_UNAVAILABLE` after the guest helper's five-second startup wait. No kit job
had been submitted. Readiness reads now use the same bounded, authority-checked
retry path as result polling, so a delayed bridge can become ready without
manual intervention. Installation submission remains non-retryable.


The next run kept healthy networking and a live bridge through slow polls,
passed Python, and then hit the relay's 1,100-second limit during Playwright's
full-browser installation. Its archive grew from 73 MB to 205 MB and its full
Chromium cache appeared. `la-prior-download.json` preserves that failed
operation rather than calling it a pass; `la-slow-relays.log` preserves observed
status calls exceeding five seconds that the new poll path tolerated.

The ticket's browser is headless and launches Playwright without a channel
override. Installation now requests `--only-shell`, the executable that launch
actually uses, skipping the unnecessary full interactive Chromium download.
The [official Playwright documentation](https://playwright.dev/python/docs/browsers#chromium-headless-shell)
describes this mode. No audit, Task or timeout enforcement was relaxed. The
failed VM was deleted, and the final fresh run uses `workload-la.py` to exercise
Chromium first, followed by Python, Docker, LSP and desktop.


The headless-only kit installed successfully in `sb_cad942bca52a60a477f6d29f227f7be9`.
Its first cold navigation hit Playwright's default 30-second page wait, while
network audit continued with no helper error. Subsequent CLI evaluation returned
`ready=complete`, `title=Example Domain`, and the expected HTTPS URL; Python
HTTPS returned 200. CLI title and PNG capture then succeeded. The navigation
wait is now bounded at two minutes, retaining `domcontentloaded`.

The resident guest runtime retains the source with which it started. To verify
the new wait without mutating that runtime, `cold-browser-probe.py` sends the
unmodified current `Browser` implementation through public `exec` in the same
VM, launching a fresh Chromium process and saving its own PNG. This is distinct
from the CLI browser action; no VM restart, suspend/resume or network recovery
is performed. Remaining kits continue through normal kit CLI calls.


## Linux arm64 storage incident

After the successful cold Chromium probe, the host Mac had only 1.2 GiB free.
Lima logged a virtual-disk WRITE I/O error, an aborted EXT4 journal and a
read-only root remount. A safe write inside this ticket's install failed with
`Read-only file system`; the control journal then correctly became unavailable.
This run is not claimed as an uninterrupted five-kit pass. No network fence
or suspend/resume recovery was involved.

The browser transcript and both PNGs were copied out before cleanup. This
worktree's generated Rust build directory was removed to free space. With no
live task VM, Lima was stopped and restarted; no live filesystem repair or
forced remount was attempted. The affected disposable install was uninstalled
with data deletion. After discard reclaimed its storage, the Mac had 7.1 GiB
free. A new signed disposable candidate includes the current daemon, the branch
host guard and the same validated public seed. The remaining kits run in a
fresh sandbox under `/home/johansardar.guest/t44-install7`.

`la-browser.json` contains the CLI browser actions and the separate current-source
cold probe. Long encoded source arguments are represented by byte count and
SHA-256. `la-browser-run.log`, `la-cold-browser.txt`, `la-browser.png` and
`la-cold-browser.png` preserve the actual output and artifacts.


The first remaining-kit attempt (`t44-install7`) also exhausted host storage
before Python completed. Stopping the idle Lima instance recovered several GiB;
Mac swap usage was about 8 GiB. Lima's memory allocation was temporarily reduced
from 8 GiB to 4 GiB for the final disposable attempt, `t44-install8`. Its previous
setting is restored after cleanup. This is host-resource recovery, not a kit or
network pass; no successful result is inferred from either interrupted run.


The coordinator then cleared the Mac Go build cache and old build outputs,
recovering 38 GiB, and restarted Lima with a writable root. The requested single
retry uses `t44-install9`; the failed install8 store and mirror were removed
before starting it. Reducing memory had not solved the storage problem, so it
is not described as a fix. The coordinator's cleanup supplied adequate space.


## Final single retry after disk cleanup

`t44-install9` installed successfully from the signed current branch candidate
and created generation-1 sandbox `sb_bd1e8b1091fdec1ba0615ee6598e5eca`.
The Mac stayed at 37–40 GiB free, above the coordinator's 20 GiB floor, and
Lima's root stayed writable. The Python kit operation remained `accepted`
without a returned result. A diagnostic public exec failed with exactly:

```json
{"code":"HOST_UNAVAILABLE","message":"guest channel refused: ","retryable":true}
```

`la-retry-operations.json` preserves both the pending kit and failed exec;
`la-retry-worker.log` records the associated worker calls. No kit deadline
failure is claimed: after this unresponsive attempt the sandbox was explicitly
deleted for cleanup, and deletion succeeded. The root cause of this guest
channel failure is not established. It is separate from the earlier disk-full
failures; no runtime/package failure or unsupported arm64 architecture is
inferred from it.

Linux arm64 evidence therefore covers stateful Python in the prior run and
Chromium title/PNG plus the fresh current-source cold probe in the browser run.
**Docker, LSP and desktop remain unverified on Linux arm64.** All five kits did
pass on the M5 Mac engine's aarch64 Linux guest and on Ubuntu, without network
recovery. The coordinator's full requested Linux arm64 demonstration is not
complete, despite the approved behavior changes.

`la-retry-install.log` records successful installation. `la-retry-cleanup.log`
records uninstall with data deletion and zero Firecracker processes. The staging
release, release mirrors, failed stores, install prefixes and disposable signing
keys were removed. Lima's original 8 GiB memory setting was restored after the
run. No existing Docker resources or owner desktop apps were used.
