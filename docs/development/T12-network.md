# T12 transport-policy foundation — networking ticket remains incomplete

This change implements the numeric IPv4 transport portion of NET-005 and its
host revision fence. **It does not complete T12, qualify E09, or unblock T26.**
No exit provider, private-member identity, dev-server authentication service or
sandbox-link control resource is manufactured from an endpoint label.

## Implemented boundary

`lamp-guard` and the existing numeric HTTP proxy share one decision function:

1. Explicit `safety_denies` are immutable destination ceilings. Exact private
   service exceptions cannot override them.
2. Explicit denies in **any** `policy_ceiling` or `policy_layers` entry win.
3. The existing special-use/private/LAN/metadata/host-inventory default blocks
   destinations unless an exact address/transport/port exception was installed.
4. Every default-deny layer must independently allow the destination/transport/
   port. A permit in one layer cannot widen a parent layer.

`policy_ceiling` is immutable for a sandbox generation; live revisions replace
only `policy_layers`. `require_allowlist` is also immutable and requires a
persistent default-deny layer. Trusted public/unknown-visibility forge intake
must set it; a guest cannot assert repository visibility. This is an integration
primitive, **not** completed forge intake or the stranger-PR safety policy.

Each layer uses public vocabulary (`filter_mode`, `default_egress`, `allow`,
`deny`); each rule requires `cidr`, `protocol` (`tcp`/`udp`) and explicit nonzero
`ports`. Empty/missing/ambiguous selectors, unknown fields, oversized rule sets,
IPv6 CIDRs, hostname rules and `strict_sites` fail admission. Limits are eight
immutable and eight mutable layers, 256 rules per layer and 64 ports per rule.
There is no pretending that an IP permit inspects an encrypted site.

The engine's host policy passes these fields into the generation's guard/proxy
config before task release. Existing T6 policies retain their previous default
when the optional fields are absent. The legacy `host_denies` inventory remains
a default that exact benchmark exceptions may lift; put explicit host/fleet
safety exclusions in `safety_denies` when they must never be lifted. A raw
exception is not a generation-bound NET-006 Connection.

Example additions to the existing host policy/config:

```json
{
  "require_allowlist": true,
  "safety_denies": ["169.254.0.0/16", "100.64.0.0/10"],
  "policy_ceiling": [{
    "filter_mode": "transport", "default_egress": "deny",
    "allow": [{"cidr": "8.8.8.8/32", "protocol": "tcp", "ports": [443]}]
  }],
  "policy_layers": [{
    "filter_mode": "transport", "default_egress": "public", "deny": []
  }]
}
```

These example addresses are fixture selectors, not an adequate CI allowlist or
host inventory. The compiler still refuses the built-in private ranges even if
an allow rule names them, absent an exact private-service exception.

## Live revision fence

A trusted supervisor can supply a preconnected **AF_UNIX SOCK_SEQPACKET** socket
as `lamp-guard run CONFIG control:FD`. This is an inherited host-only capability,
not a new public endpoint, plugin channel or worker RPC. Never hand either end
to a task/guest. No listener or additional post-confinement syscall is added.
The guard retains its subordinate UID, only namespace CAP_NET_ADMIN, seccomp,
no-new-privileges, and only its assigned descriptors.

One datagram (maximum 60 KiB) is one complete replacement:

```json
{"expected_revision":1,"revision":2,"policy_layers":[{"filter_mode":"transport","default_egress":"deny","allow":[],"deny":[]}]}
```

The revision must be exactly current+1. A stale/malformed/unsupported update
returns `state:rejected` with the unchanged `effective_policy_revision`.
A valid update durably flushes old counters/denials, records retirement of all
old flow identities and the new policy digest, swaps the in-memory decision,
and only then acknowledges `state:applied`. Both readiness and transitions bind
the decision inputs (including built-in special-use ranges) by SHA-256.
Subsequent audit attribution uses the new revision. Durability or ACK failure
terminates the guard; disconnect/truncation also terminates it. The installed
NFQUEUE rules have no bypass, so established and new traffic drop.

All old TCP admissions are conservatively fenced, including still-permitted
flows; applications reconnect with a fresh SYN. Newly denied TCP/UDP cannot
continue using a cached admission. This is packet fencing, not injected TCP RST
or completed application-level WebSocket/HTTP2 stream cancellation. A permitted
UDP tuple may be freshly admitted under the new revision. Already-forwarded
packets are not recalled.

**Proxy limitation:** live revision admission is rejected when `proxy` is
configured. The existing blocking proxy does not yet have an active-stream
fence. The T6 engine always configures that proxy and therefore only consumes
the **initial** policy additions; it does not expose this live update channel.
A coordinator must fence/recreate both helpers, not update the packet guard
alone and leave a stale proxy origin dial. Public API revision coordination is
still missing.

## Reproduction and evidence

On Linux, using only disposable network namespaces:

```sh
cargo test --workspace --locked
cargo clippy --workspace --all-targets --locked -- -D warnings
cargo build -p lamp-guard --locked
sudo env TMPDIR=/var/tmp python3 -u tests/guard/network_policy_e2e.py "$PWD/target/debug/lamp-guard"
sudo env TMPDIR=/var/tmp python3 -u tests/guard/e2e.py "$PWD/target/debug/lamp-guard"
```

[Evidence and limitations](T12-evidence.md). The observer is an independent edge
namespace's nft packet counters, not the guard's allow/deny log and not merely
whether an app connection succeeded. Guest root has its own routes/firewall.
IPv6 negatives use a static next-hop neighbour so they reach the guard rather
than failing only at neighbour discovery. Successful public TCP/UDP and an exact
private TCP service are mandatory positives. The real HTTP proxy is exercised
with actual upstream requests and independent zero-denied-packet observations.
Every run retains its artifacts, including failures; cleanup errors are separate.
No host firewall, owner service or Docker object is modified by these tests.

## Work still required for T12 / E09

- NET-002: independent direct/VPN/proxy providers, checked numeric final-dial
  binding in each DNS context, host-held keys, three preverified reflector IPs,
  UDP and selected-exit-death proof. No provider implementation is added here.
- NET-003: real per-generation tsnet enrollment/state, outbound TCP/UDP and only
  mapped inbound bridges, independent peer identity, fork/resume authority,
  SaaS/Headscale capability evidence. Host Tailscale identity is not substituted.
- NET-004: sign-in integration, unique origins, generation/port-scoped ingress,
  one-use share exchange, expiry, secret-once completion and active revocation.
- NET-005: host/member DNS, IPv6 useful coverage, strict-site TLS/HTTP mediation,
  immutable principal/CI policy compilation at public admission, and coordinated
  public API/supervisor/guard/proxy revisions. Unsupported modes remain errors.
- NET-006: directed source/target sandbox generations, two-ended Connection
  validation, same/cross-host enforcement and active link revocation. Numeric
  exceptions alone do not prove this.
- Mac packet-stack/engine adapter and fallback integration, all required pairing
  runs, actual nested Docker, rebinding/fronting/QUIC/browser background traffic,
  and guard/VPN death sink observations. Current guard still denies all IPv6;
  neither Mac has a qualified network path in this change.
- Investigate the intermittent allowed-UDP and revision-ACK failures retained in
  the evidence. Repeated later successes are not a qualification waiver.
