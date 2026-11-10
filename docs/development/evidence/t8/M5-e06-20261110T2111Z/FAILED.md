# Failed: stopped after 2 of 100 suspend/resume trials

Every capacity step passed (see `trials.jsonl`): two running, a third refused
with `NO_CAPACITY`, Linux takes no macOS slot, retry after delete, a race with
exactly one winner, undersized refused, resume of A, memory fork refused with
`DEVICE_NOT_FORKABLE`, and the A2 disk-only fork. Both trials then failed
(`ok:false`), and the driver was stopped with SIGTERM. The two failures had
different causes:

1. **The counter process was dead** (`counter:[0,0,0]`, `process_alive:false`)
   before any suspend. The macOS guest helper's timeout thread killed the
   command's process group once the exec timeout had passed, even when the
   command had already exited normally. That killed the background job the
   driver had started. This is a defect in the helper (fixed in 5c59d82), not
   in suspend/resume.
2. **The machine id changed on each resume.** The engine applied a fresh
   machine id at resume. A resumed sandbox must keep its own identity. Fixed
   in 5c59d82: the suspend record carries the machine id.

This run is kept as failed. The rerun used the re-provisioned guest
(helper sha256 `d854b148…`) and is a separate run directory.
