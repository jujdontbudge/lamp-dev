# T4 review round 2: draining connections are not terminal

R2-1 changes only FIN/drain record semantics and their regression coverage.
`origin/main` through **0ee4b23** (T3 control path) is merged, and `make verify`
was rerun on that merged tree. Admission, batching, policy and fail-closed
restrictions are unchanged.

A FIN now writes a durable `checkpoint` with `closed:false` and
`ended_at_ms:null`. Subsequent draining counter checkpoints remain unterminated.
Only RST, idle expiry or SYN replacement retires the identity and emits a terminal
`end`. SYN replacement must therefore write the previous draining identity's end
even when no counters changed since its FIN checkpoint.

## Focused regression

**`fin_then_peer_data_guard_kill_remains_unterminated`** runs on the real kernel:

1. Establish TCP, then synchronize just after an audit batch.
2. Send FIN with `shutdown(SHUT_WR)` while retaining the receive side.
3. The peer sends additional data after receiving FIN; the client verifies it.
4. Stop the guard immediately and SIGKILL it before the next counter batch.
5. Assert that no record for this identity claims `end`, `closed:true` or an end
   timestamp. Also verify that the last durable receive count still equals the
   FIN checkpoint's count, proving the extra peer data was not batched first.

See source-port **19002**, flow **8**, in the actual
[LX spool](evidence/t4/review-r2/lx/network.ndjson) and
[LA spool](evidence/t4/review-r2/la/network.ndjson). Both retain an explicitly
unterminated FIN checkpoint after the crash.

The deterministic Rust test **`fin_then_peer_data_crash_remains_unterminated`**
also exercises a durable checkpoint *after* peer data during drain, then loses
another unbatched peer-data increment. Both the FIN and later checkpoints remain
unterminated. Existing FIN/SYN-reuse assertions account for the terminal record
now required when retiring the draining identity.

## Verification

- [LX](evidence/t4/review-r2/lx.log): **13 Rust tests**, strict clippy and all
  **14 real-kernel integration assertions pass**; disk-backed ext4 spools.
- [LA](evidence/t4/review-r2/la.log): the same checks pass on nested Linux arm64.
- [Merged-tree `make verify`](evidence/t4/review-r2/m5-verify.log): passes on M5,
  including T3's control-path tests.
- [Source hashes](evidence/t4/review-r2/source.sha256) and actual binary/test
  hashes in the [LX](evidence/t4/review-r2/lx/summary.json) and
  [LA](evidence/t4/review-r2/la/summary.json) summaries.

No performance claim is added. No owner service was stopped, no Docker object
or host firewall/sysctl was changed, and no paid service was used. The LX log
confirms the owner's Chrome service remains active.
