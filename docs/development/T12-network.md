# T12 host networking

## Owner-scoped delivery (2026-11-10, 21:30Z)

The continuation implements and demonstrates an exit, a private identity, a dev
URL, allow/deny rules and a directional sandbox link. See
[T12-evidence.md](T12-evidence.md) for actual observations and retained failures.
No new tests or extensions to tests were made after the owner's scope change;
uncommitted development test additions were removed. No SaaS qualification,
benchmark, trial or soak was run under that instruction. Headscale is the private
control plane used in the demonstration.

## Host service and engine boundary

`cmd/lamp-network` / `internal/network` provide a host-owned private-Ethernet
service. `serve CONFIG` accepts supervisor-assigned TAPs (Linux) or connected
Ethernet descriptors. The supervisor supplies immutable sandbox ID/generation,
guest/gateway addresses, initial policy, independently selected exit and optional
private membership. A service may own more than one explicitly assigned bridge.
The demo uses two Linux network namespaces, **not VMMs or the public Lamp API**.

The service opens its assigned descriptors in their namespaces, restores the
host namespace, then drops to its configured subordinate UID before enrollment
or data service. Capability bounding sets, dumpability and no-new-privileges
are applied across Go OS threads; core dumps are disabled. Linux requires a
`CGO_ENABLED=0` build. Authentication descriptors and the inherited control
stream must be distinct from each other and Ethernet descriptors. Nothing
starts the owner's Tailscale daemon, changes its routes/policy or enters its
tailnet.

Host control is an inherited, connected Unix-stream capability, not a guest
socket, public API, plugin or added worker RPC. It carries `publish_port`,
`share_port`, `revoke_share`, `unpublish_port`, `sandbox_link`, `revoke_link` and
CAS `policy_update` actions. Share responses are transient on that private
stream only, never stdout or audit. `tools/network-demo-peer` is an independent
Tailscale SDK caller without Lamp runtime/control imports; it reports identity,
bytes and HTTPS outcomes without printing bearer URLs, bodies or cookies.

The existing engine/public control still uses the older Rust helper described
below. The Go service is a descriptor-based integration boundary, **not a
claim that selecting these attachments in `lamp sandbox create` already wires
this service into a VMM lifecycle**. Public resource persistence, authority and
operation envelopes belong at the control-core adapter, not in this private
channel. Mac's actual Ethernet adapter remains dependent on T8. The Go service
has UID/capability confinement but not the Rust guard's seccomp profile.

## Exits and private membership

- `DirectProvider` makes numeric TCP/UDP dials; `ConnectProvider` connects only
  to an explicitly selected loopback endpoint, intended behind an authenticated
  SSH tunnel. The owned CONNECT service supports TCP and a bounded framed UDP
  extension; it is not a generic CONNECT-UDP implementation or SSH launcher.
- A final dial takes a checked numeric destination, not a name that a provider
  may resolve differently. DNS comes from the selected exit, or the member for
  explicitly configured private DNS zones. Mixed disallowed answers fail closed.
  The bridge's gateway serves bounded A/AAAA DNS over UDP/TCP; DNS answers do not
  replace the policy check on a later dial.
- `AttachedProvider` keeps an internet exit independent of private membership.
  Tailnet destinations always use the sandbox member, not the host's daemon;
  neither route falls back to the other on failure.
- Each `Member` uses real tsnet v1.96.5, an exclusively locked, private state
  directory bound to sandbox ID/generation and control URL, host-only enrollment
  FD and its own addresses/DNS context. Unhealthy membership fences its sockets.
- Inbound TCP/UDP registrations are explicit member-port to fixed guest-port
  mappings. Peer identity comes from `WhoIsProto`, never a guest/header label.
  Guest dials bind the configured gateway source, not a learned remote address.
  There is no subnet router, guest VPN key, or arbitrary inbound host dial.
- `verify-exit` supports TLS-verified reflector observations against a preverified
  address. The owner-scoped demo proves one owned CONNECT exit with real HTTPS;
  it does not claim three externally verified exits or an SSH/VPN failure row.

## Policy and flow lifetime

The Go compiler supports IPv4/IPv6, mapped-address normalization, intersected
immutable ceilings/mutable layers, immutable safety denies, host/LAN inventory,
exact private services and the public-PR private exclusion. Explicit denies win
in every layer; a private service cannot bypass one. Unsupported hostname/site
selectors and `strict_sites` are rejected, not approximated by IP rules.

Numeric permits bind director, subject, policy revision, destination, selected
link (if any) and deadline. CAS revisions retire all old active admissions before
ACK. Flow counts, datagrams, frame queues and audit segments are bounded; flow
leases expire. Audit failure/stall stops dependent data paths. The private frame
classifier rejects spoofed sources, fragments, unsupported IPv6 extensions and
mapped IPv6 wire addresses. There is no kernel default-route escape from the
assigned Ethernet path. The audit spool is a durable private bounded spool, not
T23's encrypted/signed external ledger.

## Private dev URLs and shares

`PortManager` publishes a new HTTPS hostname for one guest TCP port and sandbox
generation. A durable origin tombstone ledger prevents deliberate reuse; each
publication also has a fresh random identifier. The host holds a private CA and
leaf keys; clients trust its public CA explicitly, without disabling TLS
verification. DNS publication is performed by the trusted host/controller (the
demo updates its disposable Headscale extra-records file).

Default viewing authenticates the real private-network transport and checks an
explicit host-configured viewer node set. This is **Headscale/Tailscale node
sign-in**, not an Eilmer OAuth relay. An unrelated enrolled peer is denied unless
it has a current port-scoped share. Shares have explicit expiry (one hour by
default), optional recipient binding and secret-once, one-use exchange. The
bearer is a URL fragment; landing JS removes it from history before posting to
the host exchange endpoint. It is not sent to the app, redirect or access log.
Only token/session hashes are retained; replaying creation's idempotency key
returns grant metadata without the URL.

Session cookies are host-only, Secure, HttpOnly, SameSite=Strict. Credentials,
cookies and host identity/forwarding headers are stripped before app forwarding;
app Set-Cookie is suppressed. Per-request backend connections are bound to the
port/share rather than pooled across grants. Revocation/expiry/unpublish cancel
authority and close associated active backend streams. The demonstration
observed an open HTTPS stream close on share revocation and subsequent 403s.
It was an actual HTTPS client demonstration, not browser UI qualification.

## Directed sandbox links

A host-created `SandboxLink` binds one source director/generation, an immutable
target engine handle/generation, private alias and exact TCP/UDP service. The
existing ceilings must already permit that service; creation lifts only the
private-address default floor. Public-PR mode cannot acquire a link. Ownership,
shared host and private-network membership alone create no link.

The alias dispatches directly to the target's assigned private guest endpoint,
not a host IP or management listener. A new director/fork has no inherited link.
Revocation closes link-owned connections and retains a routing tombstone so the
same alias cannot silently fall through to an exit. Wrong ports and reverse
traffic require their own grants. Current alias allocation is IPv4 within
198.18.0.0/15; this is not a cross-host broker or cross-host link qualification.

## Existing Rust engine integration

The original PR addition makes `lamp-guard` and the numeric HTTP proxy share
numeric IPv4 transport decisions: safety denies, all-layer explicit denies,
private/LAN defaults and intersected default-deny permits. The engine passes
initial policy into both before release. Legacy policy defaults remain intact.
The original Rust compiler rejects IPv6/site rules rather than silently ignoring
them.

A standalone guard may receive an inherited AF_UNIX SOCK_SEQPACKET revision
channel, `lamp-guard run CONFIG control:FD`. Replacement messages have
`expected_revision`, `revision` and `policy_layers`; stale/unsupported input is
rejected. Counters, retired admissions and policy SHA-256 are durably recorded
before ACK. Disconnect/failed durability terminates the guard; NFQUEUE has no
bypass. Proxy-enabled live revisions remain rejected: the existing blocking
Rust proxy and engine do not implement the coordinated active-stream fence.

## Limits of this delivery

The requested five real network demonstrations are recorded. They are not E09
host/engine pairing certification or completion of every original ticket row.
Remaining integration includes public control/VMM lifecycle attachment wiring,
fork/resume orchestration, cross-host links, production ingress/DNS provisioning,
strict-site HTTP/TLS mediation, coordinated legacy proxy revisions, nested
Docker and Mac packet paths. The earlier intermittent UDP/revision timeouts and
private-demo process failures remain recorded, not erased by later successes.
No SaaS work or additional qualification loop is required by the owner's revised
21:30Z scope, and none was started afterward.
