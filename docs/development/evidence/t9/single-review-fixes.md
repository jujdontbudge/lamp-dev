# PR #10: single review fixes

All six findings were confirmed and addressed. No tests were added or extended,
no sandbox/sign-in trial was run, and no further review was requested.

1. **Quiescence.** The protected collector now attaches with `PTRACE_SEIZE`
   before releasing the child's admission pipe. It forwards real stop signals
   and uses `PTRACE_LISTEN` for job-control stops, preserving SIGCONT handling.
   Only then does it publish a root-owned acknowledgement bound to the PID
   namespace, thread ID and process start time. The wrapper requires an
   acknowledgement for every thread of each frozen process; temporary ptrace
   stops no longer count. Acknowledgements are revoked before a TID can resume,
   and the wrapper waits for their revocation before another operation.

2. **Resume audit spools.** Every network attachment gets an exclusively created
   nonce-suffixed audit directory. Sandbox identity and generation stay intact,
   while fresh network, proxy and process spool files cannot collide with a
   previous attachment's `create_new` files. Previous audit evidence remains.

3. **Failed command admission.** All send, response-read and guest rejection
   errors after `begin_run` durably finish the failed lease before returning.
   This does not depend on a command record being created. Failure retains the
   existing uncertain-lineage policy: a fresh vendor sign-in can recover it;
   stale inference/forks are not silently accepted.

4. **Control restart.** Startup retains a durably committed `suspended` state
   instead of rewriting it as `unavailable`. Resume still validates the capture
   with the worker and does not treat an old readiness command as proof that
   a stopped guest is running.

5. **Cancellation.** Custody dispatch checks for an intact, accepted operation
   under the journal lock before committing `running` or making worker calls.
   A canceled share cannot rebuild or activate; its source reservation and
   completion waiter are released without overwriting cancellation.

6. **Clean reconstruction.** The builder's crate-private boot-probe restore
   bypasses subscription registration only for a fresh template in a newly
   allocated sandbox. It rejects memory/durable captures. Ordinary restores
   still apply registrations by both template ID and name. This is not a
   caller-selectable public API flag.

## Storage merge

Main `09899fd704a0cfd02112bd677e692931ad562a0d` was merged, not rebased.
The merge retains T7's disk backend, thin-pool, producer-enrollment and memory
options, durable capture handling and resource-aware builder probes alongside
the custody controls. Durable restore sources also consult custody before
restore. T7's capture-spare option and T9's subscription restriction both apply.

The merged implementation is
`9febad2042c7d976de3a414d8070b973b88095ac`.
`docs/spec`, `docs/tickets` and the guard seccomp profile are unchanged relative
to that main. The protected-process fix changes the tracer, not its task or
host-helper syscall allowlists.

## Verification

Linux-target `cargo check --workspace --all-targets --locked` passed with
`--target x86_64-unknown-linux-gnu`, including after the storage merge.
Linux-target `cargo clippy --workspace --all-targets --locked -- -D warnings`
also passed with that target. These are static checks, not a Linux runtime trial.

The one requested `make verify` invocation is retained in
`verify-review-main-09899fd-01.log`; it passed with `verification_exit_code=0`.
No trial run or new subscription evidence is claimed. E07 and the signed-in
fork demonstration remain incomplete.
