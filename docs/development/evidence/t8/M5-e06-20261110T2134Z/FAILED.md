# Failed: the driver's counter process never started

This run used the re-provisioned guest (helper `d854b148…`) and the
machine-id fix. Every capacity step passed, along with the memory-fork
refusal and the A2 disk-only fork. The suspend and resume themselves worked:
the same boot session, the same machine id `832837c9…`, and the trial marker
was read back after resume. The trial still fails, with `counter:[0,0,0]` and
`process_alive:false`.

Cause, found afterwards in a hand-booted macOS sandbox: the driver started
its RAM-only counter with `nohup`. macOS `nohup` exits 127 with
"can't detach from console: Inappropriate ioctl for device" when it runs
under a daemon with no console, so the counter never ran. A plain
background job runs. This is a driver defect, not a suspend/resume one. It is
fixed in the driver, and the rerun is a separate run directory.
