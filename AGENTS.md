# Verification and independent benchmark harness

Run `make bootstrap verify` for workspace, contracts, interoperability, lint and
the independent harness tests. For harness-only iteration use
`python3 -m unittest discover -s tests/benchmarks -v`. Python uses only its
standard library; the workloads additionally need `cc` for the fixed build row.

The measuring side lives in `benchmarks/`, not engine/control crates. Run with
`python3 -m benchmarks.run --help`. It imports no Lamp runtime, generated API
bindings, serializer or storage implementation. Public wire vocabulary is read
from the agreed contracts when maintaining the handwritten adapter; do not add
private worker RPCs to that adapter.

## Running on a qualified lane

Install the exact `benchmarks/workload.py` as
`/opt/lamp/bench-workload.py` in the prepared template before the timing run.
Do not upload/build/prepare a template in the timed path. The runner verifies
the guest workload digest and compares native/secured Python and compiler
identities. Use `--workload`/`--guest-python` to name different installed paths.
No command starts/stops owner services or touches Docker.

Run from the machine being measured. Supply `--metadata <json>` with the actual
§22 observations: `source_hashes`, `dependency_hashes`, `artifacts` are nonempty
maps of artifact names to SHA-256 hex; `compiled_features` is the actual enabled
feature set; `hardware`, `os`, `filesystem`, `storage`, `policy`, `resources`,
`power`, `thermal` are structured measured records. Include CPU model/core
assignment, filesystem/mount options, physical storage, power/governor and
thermal samples rather than placeholders. Also supply `durability`, `lane`,
`topology`, `access_route` and `controls` with `guard`, `audit`, `cow`,
`capture_tracking` all true. `os.kernel` is `Linux` or `Darwin`. Missing data
cannot produce a passing report. Only native LX/Linux carries binding speed
evidence; other lanes remain diagnostic.

For example, with an authenticated local API and an already prepared template:

```sh
python3 -m benchmarks.run --suite E01 --api-url "$LAMP_API_URL" \
  --template tp_bench --metadata "$RUN_METADATA" --output "$NEW_RUN_DIRECTORY" \
  --observer '["/path/to/independent-collector"]'
```

Alternatively use `--cli /absolute/path/to/lamp`. Authentication is inherited
by the CLI or read from `LAMP_BENCH_TOKEN` for API calls; never put credentials
in URLs or evidence metadata. API redirects are rejected. Remote authenticated
APIs require HTTPS; a local SSH tunnel may use loopback HTTP.

`--observer` is a JSON argv, not shell text. It receives one JSON object on
stdin with `operation`, `sandbox_id`, `source_sandbox_id`, and returns JSON
`rss_bytes`, `disk_bytes`, `pressure`, plus `source_pause_ms` for forks.
Missing independent samples leave the run incomplete. Sample collection is
outside the caller-to-useful-command timer.

E01 fixes all nine B1 dirty/durability rows, counts, strict boundaries and
failures; `--extra-rows <json>` adds explicitly labeled nonbinding timing rows,
never replaces B1. Extra row fields are `id`, `resources`, `dirty_mib`,
`durability`, `case`. Include additional admitted memory/device configurations,
pressure and full-dirty cases there. The runner currently executes
prepared-template create and live memory fork; a label must not be used to
claim a cold build, warm-pool assignment or suspend/resume measurement.
Full memory/disk checksums and independent writes follow the independently
witnessed first useful commands, outside the timing endpoint.

For E02 also supply `--directory` on the actual private disk in both environments,
`--native-cpus 0,1` (use the lane's actual matched CPUs), `--network-fixture <json>`
and `--proxy <url>` for the secured proxy path. Baseline work runs directly on
the native host, without the secured proxy or artificially inserted overhead.
The fixture JSON contains `host`, `tcp_port`, `udp_port`, `http_url`, pointing
to the same independent remote fixture for both halves. Start the owned remote
echo fixture explicitly with `python3 benchmarks/workload.py serve-network
--host <address> --tcp <port> --udp <port> --http <port>`; it prints bound ports.
No rented service is needed. Every measure has ten randomized pairs; publish
individual ratios and the seeded paired-bootstrap one-sided 95% lower bound.
The confidence objective is provisional, not a replacement binding threshold.

## Storage fixture integration (T7)

`--suite E04 --model` runs the disposable block-model control, never qualifies
a real filesystem or power-loss barrier. `--smoke` reduces counts and can never
pass any gate. A full model run still performs 1,000 process-crash cases, all
before/after boundaries, three omitted-flush controls, and a validly hashed
unauthorized-producer control. Model reordering changes recovery order and
discards simulated volatile writes; it is not a real block integration fault.

Real integration is supplied with `--storage-fixture '["/path/to/fixture"]'`.
That trusted fixture must use only disposable volumes and Lamp's public CLI/API
for capture/restore/guest observations, not implementation serializers. The
harness appends `--root <new temporary directory>` and `--request <json>`.
The directory contains `lamp-disposable-fixture`; refuse an absent marker and
never interpret a live disk as this fixture.

Requests choose external `nonce`, `point`, `side`, `phases`, `fault`
(`discard`/`reorder`), `iteration`, optional `omit`
(`object_flush`/`directory_flush`/`journal_flush`) or `unauthorized`.
Each scenario has a root generation `0` and child generation `1`, 8192 pattern
RAM bytes and 16384 pattern disk bytes. The public workload's pattern consists
of 4096-byte blocks: SHA-256 of UTF-8
`nonce:generation:ram-or-disk:block-index`, repeated 128 times (truncate the last
block). The oracle independently derives these hashes from externally selected
inputs, not returned checksums/serialization.

The line-delimited stdout protocol is:

1. `{"event":"ack","value":{"id":"0","parent":null,"nonce":"...",
   "generation":0,"memory_bytes":8192,"disk_bytes":16384}}` and similarly
   generation 1 with parent `"0"`. Wait for stdin
   `{"acknowledged":"0"}` (or `"1"`). The harness fsyncs the external history
   before releasing that acknowledgement. Never acknowledge a generation that
   has not crossed the declared durability boundary.
2. Abruptly terminate/restart the disposable writer at the requested boundary;
   report normalized `crash_exit:79` (or `0` for `point:none`).
3. `{"event":"recovered","crash_exit":79,"value":{"objects":{...},
   "qualification":"real-filesystem-block-barrier"}}`.
   Each recovered object contains `nonce`, `generation`, `parent`, `producer`,
   `memory_sha256`, `disk_sha256`, `ticks`. Hash actual independently read bytes.
   `"authorized"` is the positive fixture producer. Preserve ancestors.
   Retain public-call transcripts, actual filesystem/block traces and qualified
   barrier evidence in the run's artifacts; a string claiming qualification
   does not replace those real T7 experiments.
4. The unauthorized-producer positive rejection case must report
   `unauthorized_capture_rejected:true`, acknowledge only root 0, and publish no
   child 1. Merely detecting an unauthorized object after accepting it cannot
   pass real E04. The model intentionally accepts it to test oracle detection.

The driver fsyncs every chained raw observation, refuses existing output
directories, records cleanup failures separately, and never overwrites failed
runs. Keep all run directories, including failed/incomplete ones. Hard run
thresholds are constants in `benchmarks/protocol.py`, not CLI overrides.
T6/T7 must supply real engine, independent collector and filesystem evidence;
successful deterministic tests alone are not engine or power-loss certification.
