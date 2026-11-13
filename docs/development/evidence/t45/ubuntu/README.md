# Ubuntu maintenance follow-up

One complete update → rollback → uninstall run on the owner's Ubuntu amd64
laptop, 2026-11-13 03:22–03:23 UTC. The candidates were built from T45 head
`dbb6c8be36229cb040df5a31212e06f5d29f3026`, stamped 0.1.1 and 0.1.2 and signed
with a disposable release key. These are local candidates, not public releases.

[maintenance.log](maintenance.log) contains the real CLI transcript.
[result.json](result.json) contains the capacity observations, before/after
existing-engine fingerprints and every cleanup result.

The installation used `--engine --service systemd --name t45-leazek` with a
private prefix under `/home/johan/t45-ubuntu-LEAZEK/p`. It created one VM and
executed the public six 1.16.0 module. Its source archive was fetched from PyPI
on the Mac, checked against PyPI's SHA-256, then supplied to the VM; no owner
project, account or vault data was used.

`lamp-installer update --prefix … --source http://127.0.0.1:<port>` downloaded
the newest signed platform candidate without `--release`, suspended the VM,
restarted the supervisor/daemon at 0.1.2 and resumed the same VM. The module's
SHA-256 still matched and it printed `1.16.0`. A repeated update printed
`already up to date (0.1.2)`. Rollback restored 0.1.1 and the same module again
passed its hash check and printed `1.16.0` inside that VM.

The VM was deleted promptly, then `uninstall --data delete --confirm-delete
t45-leazek` completed. All recorded cleanup checks were true:

| Installation-owned resource | Result after uninstall |
| --- | --- |
| `lamp-t45-leazek.service` and `lamp-t45-leazek-agent.service` | Unit files and enable links absent; both `LoadState=not-found`, `ActiveState=inactive` |
| `/var/lib/lamp-i/928e5a25` | Absent, including both engine binary versions, XFS store, sparse backing image and audit files |
| `/run/lamp-928e5a25` | Absent, including the VM's private network namespace and memory mount |
| Installation-derived UID reservation | Absent |
| Installation mounts, supervisor and network/engine processes | None remain |
| Host nft rules | Same semantic SHA-256 before/after |

The running default managed network used a private L2 namespace and userspace
network director; its `nft list tables` was empty. The namespace was observed
while running and disappeared on deletion. Host nft JSON was compared with
only counters, handles and metainfo excluded; the semantic digest was
`af7e8ed1fb07e082def62705307daeeb650f305bfa9d154defa34b6851cfce57` both times.
No host firewall rules or existing Docker resources were modified.

The existing engine was intentionally retained:

- `/usr/local/lib/lamp`: every regular engine file's SHA-256 matched before/after.
- `/etc/systemd/system/lamp-supervisor.service`: identical file SHA-256;
  service remained `active/running` at **PID 34842** throughout.
- `/var/lib/lamp`: same device/inode, owner/mode and existing
  `/dev/loop14` XFS mount. No owner store contents or credentials were read.

Before template preparation, `pgrep -c firecracker` returned 0 and `free -m`
reported **16,300 MiB available**. Immediately before the one sandbox creation,
the count was 0 and available memory was **16,824 MiB**. It was 1 before update
and rollback, and 0 after uninstall. The shared two-sandbox limit and 12 GiB
creation headroom were respected.

Two candidate-construction failures remain failures in the evidence:

1. [candidate-cgo-failure.log](candidate-cgo-failure.log) and its
   [JSON](candidate-cgo-failure.json): my initial native Go build enabled cgo;
   the privilege drop refused it. Candidates were rebuilt with the production
   release builder's `CGO_ENABLED=0` setting ([static-go-package.log](static-go-package.log)).
2. [candidate-stale-seed-failure.log](candidate-stale-seed-failure.log) and its
   [JSON](candidate-stale-seed-failure.json): the copied T35 public seed's old
   helper rejected T36's `stdin_path`. A task-owned sparse copy was updated
   offline with this checkout's static musl guest binaries, read back to verify
   their hashes, and signed into both candidates
   ([seed-preparation.log](seed-preparation.log)). The source seed was unchanged.

Neither attempt completed installation. Automatic failure cleanup removed
its privileged engine resources and left the existing engine/network intact;
its staged user prefix remained (`prefix_absent=false` in those JSON files)
and was explicitly removed before reconstructing the candidates. No product
code was changed to obtain the successful maintenance run. No tests added.

The follow-up was rebased onto main including T37; [post-rebase-build.log](post-rebase-build.log)
records the successful required `make build`. The T45 behavior diff is unchanged
from the one-round Sol approval; this follow-up adds evidence only.
