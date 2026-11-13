# T34 real VM evidence

The public HTTP API demonstration uses real Firecracker, a disposable template
built from the public third-party seed, disposable identity and offline recovery
keys. Authentication and private keys are not recorded. The script checks the
shared VM and available-memory limits before starting a guest.

The evidence is one development demonstration with failed attempts and a
continuation from the completed encrypted archives. It is not a benchmark.

- `attempt-05.txt` shows suspend/restart/resume with advancing counter, busy
  process eligibility, snapshot list/restore after source deletion, explicit
  snapshot deletion, volume backup, a memory fork during a 400 MB write with
  the source still ready/readable, and automatic exec wake.
- `attempt-10.txt` shows successful encrypted suspended-sandbox backup. Its
  later stop-counter command failed because task PIDs belong to each exec's
  private PID namespace. This did not prove that the counter disappeared.
- `attempt-14.txt` completes fresh-prefix offline-key recovery of that archive:
  counter 108→118, then 128→139 after another daemon restart; restored volume
  contains `volume-survives`. An independent idle guest keeps exactly the same
  `fires_at` across restart, suspends, then is deleted at retention expiry.
  A 1999 budget deadline is refused. A busy guest receives its budget warning
  then is deleted and its task stopped. Maximum age deletes a guest despite
  idle action `keep`. The run finishes `DONE` and reports zero Firecracker VMs.

The final continuation ran the rebased T33/T38 implementation plus the sparse
extraction fix, using the same built guard as the supervisor. Mac and Ubuntu
`make build` passed. The final T41 rebase also passed `make build`, targeted
Go vet, both reviewer regressions, and direct human/JSON CLI help checks;
lifecycle defaults remain visible alongside T41 discovery and resource help.
The only new automated tests are the reviewer's two
regressions, both reproduced failing before their fixes and passing afterward.

Run on the enrolled Ubuntu host, after building:

```sh
T34_STORE=/var/lib/lamp/<new-disposable-store> \
python3 docs/development/evidence/t34/demonstration.py <source> <new-private-root>
```

`T34_TEMPLATE` can reuse this demonstration's public template in its own store.
`T34_SKIP_FORK=1` avoids the second live slot when that fork was already observed.
`T34_RECOVERY_FROM=<prior-private-root>` continues from completed archives and
its offline key; keys stay private on the execution host.

## Preserved failures

Attempts 1 and 2 failed on worker custody setup and a demonstration argument
collision. Attempt 3 failed after the first wake output with exit 70 (see the
audit investigation). Attempts 4, 7 and 8 refused shared capacity. Attempts 5
and 6 exposed export using the template's 4 GiB base for a 1 GiB sandbox; export
now uses the same size-aware base as capture and resume. Attempt 9 read the
counter before its first file existed; the script now waits for it. Attempt 10
completed backup, then used an invalid cross-namespace kill. Attempt 11 lacked
T33's newly required audit-release configuration; the script now configures the
trusted release command against this disposable store. Attempt 12 lost its HTTP
connection during dense disk extraction. Attempt 13 was stopped deliberately
with a stack dump to diagnose the same I/O blockage. None of these failures is
relabeled as a successful run.
