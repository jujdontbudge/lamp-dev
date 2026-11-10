# Stopped: complete at the revised floor (owner decision, 2026-11-10 20:50Z)

The run was started for 1,000 captures. It was stopped right after trial 158
was written, following the owner's decision that the E05 floor is ≥100
captures per certified row. The driver and its shell were ended with SIGTERM,
which is why there is no `exit` or `finished` file. The source sandbox was then
released. `summary.json` is empty because the driver was stopped before it
printed its summary. `stats.txt` is computed from `trials.jsonl` by
`infra/mac/e05-stats.py`.

Result: 159 trials, 5 warmup and 154 measured. All 154 are `ok`, with 0
state, 0 clock and 0 isolation errors and 0 lost acknowledged records. Every
trial is a memory fork with both sides continuing.

Timing note: the Air (8 GiB) was under memory pressure for the whole run.
Swap was 3.2–3.6 of 4 GiB used, and kernel_task was compressing memory. The
copy of the 21 GiB macOS guest disk from the M5 was also writing to the Air's
disk from about trial 30 onward. The guest's 768 MiB working set was largely
compressed or swapped by macOS, which explains the low RSS (`ps`, MiB), and
VZ had to fault it back in to save it. That is why the source pause is about
3 s. The times are published as measured; no non-Linux speed target exists
yet (E05: publish before setting one).
