# Stopped: complete at the revised floor (owner decision, 2026-11-10 20:50Z)

The run was started for 1,000 captures. At 20:50Z on 2026-11-10 the owner
set the E05 floor at ≥100 full-dirty, in-flight-I/O captures per certified row
(the 1,000 came from the spec rounds, not from the owner). The driver was then
stopped with SIGTERM right after trial 771 was written (`stopped`, exit 143),
and the source sandbox was released. No trial was cut short, so no trial is
missing from `trials.jsonl`. `summary.json` is empty because the driver was
stopped before it printed its summary. `stats.txt` is computed from
`trials.jsonl` by `infra/mac/e05-stats.py`.

Result: 772 trials, 5 warmup and 767 measured. All 767 are `ok`, with 0 state,
0 clock and 0 isolation errors and 0 lost acknowledged records. Every trial is
a memory fork with both sides continuing.

Timing note: forks from about trial 400 to the stop were slower (p50 about
1.8 s, against about 0.6 s before). That period overlaps the copy of the
21 GiB macOS guest disk from the M5 to the Air (a sparse read plus ssh, which
started at about trial 370) and a `cargo check` around trial 640. The M5 was
also screen-locked for part of the run. While it was locked the driver waited
before each fork, and the wait is outside `fork_ms`. The times are published
as measured.
