# T23 budgets and audit evidence — 2026-11-10

The implementation runs through the public Lamp CLI/API on the owner's M5,
Ubuntu laptop and M2 Air. Compute demonstrations use disposable Firecracker
sandboxes on Ubuntu; the Macs run only this ticket's headless fleet processes.
No visible app/window, existing owner service or Docker resource was changed.
No paid provider was contacted. This is feature evidence, not a benchmark,
soak run, attack suite or E-numbered qualification claim.

## Real Ubuntu journey

[Transcript](evidence/t23/lx-journey.jsonl),
[exported audit](evidence/t23/lx-audit-export.json), and
[binary identities](evidence/t23/lx-artifacts.txt).

Run from the M5 over `ssh ubuntu-server`:

```sh
python3 /home/johan/lamp-t23/accountability_demo.py \
  --root /home/johan/lamp-t23/demo-3 \
  --store /var/lib/lamp/t23 \
  --policy /home/johan/lamp-t23/policy.json \
  --template tp_8a6922170222418fabafdc0c95 \
  --binary-dir /home/johan/lamp-t23
```

The prepared disposable template contains public Ubuntu/Python software. The
store uses copied immutable template material and a disposable producer key,
with its own supervisor, UID range, runtime directories, grants and audit root.
It does not use the owner's projects or vault items.

Observed results:

- A parent and child task used the same budget and deadline. A real sandbox
  attached to the child reached `ready` and executed Python, a child process and
  a public TCP connection. Parent stop released that descendant sandbox.
- A second allocation was rejected by the one-sandbox/4 GiB admission policy.
  Admitted work continued and produced its audit export. Both actual sandbox
  allocations checked global `pgrep -c firecracker < 2` and at least 12 GiB
  available immediately before creation; the first was released before the
  second was created.
- The real host broker invoked a local HTTPS model fixture using a host-only
  fixture credential and a fixture-specific exact tokenizer. The provider saw
  1,100 tokens durably reserved before its effect. Its actual response reported
  five tokens; settlement left five used and zero reserved. Another outstanding
  reservation made an over-budget request fail with HTTP 429. Fixture token
  counts are not a claim of compatibility with a commercial provider tokenizer.
- The exported artifact contains 47 records, including protected process
  collector events, the host guard's actual `1.1.1.1:443` connection, control
  records and mediated service/model requests. Its SHA-256 is
  `7288417ff6d64b0051676401f47297972bb85eb5f4447b6a371dd38b305beb8c`.
  No request bodies, credentials or command environment were exported.
- Optional telemetry initially returned an empty sample list. After opt-in, an
  actual broker invocation yielded numeric timestamp/count/duration fields.
  Purge returned an empty list again and compacted the journal. Retention is
  fixed at 90 days; the one added unit test checks that elapsed-time boundary.
- Revoking the broker grant caused its next invocation to fail with HTTP 403.
- A real `/bin/sleep 120` command in a sandbox with a 15-second opaque-route
  budget was force-stopped and the sandbox became `deleted`. This is an actual
  time-budget demonstration, not a speed target or benchmark measurement.

## Fleet key revocation and offline expiry

[Fleet transcript](evidence/t23/fleet-key.jsonl).

`tools/accountability_fleet_demo.py` launched an isolated three-host mTLS fleet
with direct pairwise SSH transport over Tailscale. M5 management created a parent
key, a child key and a task, then used the child key to start real Ubuntu work.
Only this demo's Air daemon was paused with `SIGSTOP`, leaving its owner apps and
OS running. The surviving quorum committed parent-key revocation.

Ubuntu released the child-key sandbox. The child bearer became unauthenticated.
The operation receipt showed M5 and Ubuntu acknowledged, with Air explicitly
pending until `2026-11-11T23:51:45.640261Z`, including a residual
`offline_authority` capability. After `SIGCONT`, Air projected revocation and the
receipt reported all three hosts acknowledged and `complete: true`.

That journey completed in the retained run
`/tmp/lamp-t23-fleet-evidence-6`. The following member-role setup in that same
process stopped on an identity-propagation readiness issue; it did not allocate
another sandbox. The separate role journey below addresses that case.

## Role removal

[Role transcript](evidence/t23/fleet-role.jsonl).

The role-only journey enrolls a disposable subject, admits it as a team member,
and waits for all hosts to recognize that authority. The member starts a real
Ubuntu command; the owner changes its membership to viewer. The supervisor
releases the sandbox, while the viewer can inspect the final state and cannot
start replacement work.

## Retained-resource integration

After T9 merged, the capture/share/suspend routes were connected to the same
budget, audit and retained-byte ledger. A separate
[real retained-quota journey](evidence/t23/retained-quota.jsonl) created one
Ubuntu sandbox under the 20 GiB disk policy, rejected a private memory/disk
snapshot before allocating retained storage, then successfully started admitted
work in the existing sandbox. Cleanup released that sandbox. The run is retained
at `/home/johan/lamp-t23/retained-quota-1`.

## Verification and boundaries

`make bootstrap` prepared the existing Python verification dependencies.
`make verify` ran once and passed: [log](evidence/t23/make-verify.log).
The one added retention-boundary unit test passed. After rebasing onto the
newly merged network/access changes, `go build ./...` and
`cargo build --locked -p lamp-supervisor` passed:
[build log](evidence/t23/rebase-build.log).
The later T9 integration also built successfully:
[final integration build](evidence/t23/final-integration-build.log), with
[final code/artifact hashes](evidence/t23/final-integration-artifacts.json).
[Source and artifact hashes](evidence/t23/artifacts.json) identify the demonstrated
builds. No full-suite, benchmark or trial-count loop was run.

Earlier debugging runs remain on the owner's machines, including failed and
capacity-refused attempts. They were not overwritten or deleted. Initial fleet
attempts exposed an omitted demo key expiry, propagation readiness, and the
Unix socket path length limit; one attempt correctly refused allocation when
other tickets already used both global sandbox slots. Ticket supervisors were
terminated and their grants revoked after each journey, releasing their guests.

Exact mediated reservation requires the configured provider tokenizer. Opaque
subscriptions/external routes remain visibly opaque and their total token use
cannot be certified from mediated counts. The watchdog runs in `lampd` and
requires a reachable supervisor to confirm stop; failures remain unavailable and
charged. Budgets and descendants stay on one immutable home. This work does not
claim commercial-provider compatibility, a signed/tamper-proof ledger, Mac VM
audit qualification, or a payment integration. See
[implementation details](T23-budgets-and-audit.md).
