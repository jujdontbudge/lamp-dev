# Fleet control

`lampd --fleet-config /private/fleet.json --identity-file /private/owner.json`
adds an enrolled fleet to the existing control core. The private Unix socket
continues to work. A control-only host can omit worker flags; a compute host
must enroll its actual local supervisor using the existing worker flags.

The operator provisions a private CA and one TLS certificate/key per host. Each
certificate's DNS SAN is that immutable `hs_…` host ID. IDs contain lowercase
ASCII letters, digits and underscores, at most 20 characters. Both peer HTTP
and Raft use TLS 1.3 and require certificates from enrolled hosts. The public
HTTPS listener accepts caller bearers, without giving callers host certificates.
Only the peer listener exposes the internal consensus and observation routes.

An example two-host manifest (use the actual addresses and measured capacity):

```json
{
  "host_id": "hs_lx",
  "bootstrap": true,
  "ca_file": "/private/ca.crt",
  "cert_file": "/private/hs_lx.crt",
  "key_file": "/private/hs_lx.key",
  "guard_dir": "/private/lamp-recovery-guard",
  "public_api_listen": "100.80.96.36:7443",
  "hosts": [
    {
      "id": "hs_lx",
      "api": "https://100.80.96.36:7444",
      "raft": "100.80.96.36:7445",
      "os": "linux",
      "arch": "x86_64",
      "engines": ["firecracker-1.17.0"],
      "capacity": {"vcpus": 2, "memory_mib": 4096, "disk_gib": 20, "pids_max": 4096}
    },
    {
      "id": "hs_m5",
      "api": "https://100.127.45.76:7444",
      "raft": "100.127.45.76:7445",
      "os": "darwin",
      "arch": "arm64",
      "engines": [],
      "capacity": {"vcpus": 0, "memory_mib": 0, "disk_gib": 0, "pids_max": 0}
    }
  ]
}
```

Every host receives the same `hosts` manifest and owner enrollment. Change the
local `host_id`, key/certificate paths and public listen address on each host;
set `bootstrap` on exactly one initial host. The committed manifest is immutable
in this implementation: silently changing it on restart fails closed. Online
host invitations, removal and capacity redistribution are not implemented here.
`api_listen` and `raft_listen` optionally separate bind addresses from advertised
addresses. Pairwise SSH forwards over Tailscale also work; each host pair must
connect directly, without depending on a third host as a relay.

Callers configure one address:

```sh
export LAMP_API_URL=lamp+fleet:///private/fleet-discovery.json
# LAMP_TOKEN or lamp --token-file still supplies the caller's bearer.
lamp fleet get fl_local --json
lamp host list --json
lamp sandbox create --template-id tp_example --json
```

The discovery document contains public ingress addresses and public trust
material only; it never contains a bearer or host private key:

```json
{
  "ca_file": "/private/ca.crt",
  "ingresses": [
    {"url": "https://100.127.45.76:7443", "server_name": "hs_m5"},
    {"url": "https://100.80.96.36:7443", "server_name": "hs_lx"}
  ]
}
```

CLI and MCP use the same shared client. On a connection failure it tries the next
ingress with the exact original request, including its key. It does not retry an
HTTP error as a new operation. Redirects remain forbidden. Direct HTTP clients
can use any public ingress, or implement the same discovery policy.

Fleet/key/principal/membership mutations need a Raft quorum (one of one, both
of two, two of three). Their control projection and durable management receipts
replicate before a successful reply. Local worker output and guest bytes do not
enter consensus. Approval decisions/consumption also commit their authority
change before the existing enforcing adapter can dispatch; the adapter stays at
its original home. Uninstalled broker adapters still fail closed.

Each host's configured resource slice is committed with the fleet manifest.
Admission atomically reserves against that slice in the home journal. Failed or
unknown cleanup keeps its charge until Stop/Release confirms deletion. Slices
are never borrowed from an unavailable host. A finite key concurrency ceiling is
split deterministically among the enrolled host IDs. This can leave spare local
physical capacity unusable under a narrower principal ceiling.

Permits have a 24-hour horizon. Quorum renews the same slices before expiry;
isolated hosts cannot extend them. Linux uses CLOCK_BOOTTIME and macOS uses
CLOCK_MONOTONIC_RAW for admission deadlines, including system sleep. Key and
approval expiry also use the advancing authority clock during a running daemon.
A restarted daemon needs quorum activation. This is local admission authority;
it does not implement transferable VM, network or storage writer leases.

`lk1.<epoch>.<home_host_id>.<nonce>` retry keys keep their original home forever.
The CLI chooses a fresh structured create key from authenticated discovery when
no key is supplied through a fleet address. A supplied key is never rewritten.
Other clients can choose a structured key explicitly. Unknown opaque keys need
a quorum-committed home registration; equal retries return the original home
operation, even after deletion. A lost home returns
`IDEMPOTENCY_HOME_UNAVAILABLE`, rather than rerunning elsewhere. Strict host pins
never fall back to another host; incompatible engines and exhausted slices
reject. Requested setup/devices outside the existing worker subset still reject.

`host list/get` reports enrolled capacity and last observations. `sandbox list`
and `exec list` merge authorized metadata from peer journals. A missing host's
last sandbox remains visible as `unavailable`, retaining `host_id` and
`custody_host_ids`; `sandbox get` can return that cached metadata. Commands,
outputs and guest bytes still require the actual home. The cache is observational
and cannot create authority or promote missing private disks.

Keep `guard_dir` outside state-directory backups and never restore it backwards.
Its fsynced watermark advances before a home commit is acknowledged/dispatched.
A journal behind that watermark, or an existing journal missing its watermark,
is rejected at startup. Fleet state cannot be reopened as standalone state.
Restoring all independent guard and consensus copies backwards is not a
supported recovery procedure; there is no automatic fenced-epoch recovery tool
in this implementation.

The owned-machine demonstration and its limits are recorded in
[t16-evidence.md](t16-evidence.md). No performance qualification is claimed.
