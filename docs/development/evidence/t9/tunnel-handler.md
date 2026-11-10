# Subscription tunnel handler: local fix

This is implementation and local regression evidence, not signed-in E07
qualification. Neither official harness has completed subscription sign-in.
The Ubuntu fixture was released for T10 E03; no new laptop sandbox is permitted
until the coordinator explicitly releases that lane.

## Failure and fix

The previous subscription proxy attempted both DNS resolution and thread
creation after entering the existing proxy seccomp profile. The profile
allows neither resolver file opens nor thread creation. The failed t9e06
sign-in and its raw artifacts remain on LX at `/var/lib/lamp/t9e06`; they
must not be reported as successful authentication.

The subscription proxy now resolves only configured vendor names while still
in the host namespace, before binding the guest endpoint, dropping privileges
or installing seccomp. It filters answers through the existing public-IPv4
and host-deny checks, pins at most eight distinct addresses per vendor at port
443, and fails startup if a configured vendor has no acceptable answer.
The pinned set lasts for that proxy process; a new generation resolves again.
There is no guest-triggered DNS refresh or guest-supplied destination address.

Only the subscription attachment uses the new optional namespace argument:
`lamp-guard proxy CONFIG SPOOL NETNS`. Resolution precedes `setns`; binding,
audit-spool creation and confinement follow namespace entry. This avoids
depending on a host loopback DNS stub from inside an isolated namespace.
Ordinary attachments retain their existing `nsenter` launch and HTTP proxy.

The serving path uses one nonblocking poll loop:

- At most 16 active connections and 16 accepts per iteration.
- At most 64 KiB queued in either direction per connection.
- CONNECT headers and the first TLS record are bounded to 16 KiB.
- A ten-second absolute handshake/connect deadline and 300-second idle timeout.
- Exact approved CONNECT authority and matching visible TLS SNI before dialing.
- Partial writes, backpressure and independent directional EOF handling.
- Durable admission audit before connecting or forwarding; audit failure stops
  the entire loop. Audit contains policy names and counters, not headers or
  application/TLS payloads.

`crates/lamp-guard/src/confinement.rs`, including `PROXY_SYSCALLS`, is unchanged.
The fix does not permit file opens, DNS, threads or subprocess creation inside
the serving loop. Direct guest egress remains denied for subscription policy.

## Regression coverage

`cargo test -p lamp-guard --locked` on macOS runs the actual portable resolver,
parsers and reactor, including:

- Only approved names resolved; private, host-denied, IPv6 and wrong-port
  answers rejected; bounded pinning; resolver failure fails closed.
- Ordinary and invalid policy perform no DNS.
- Fragmented CONNECT/TLS, stalled concurrent peer, duplex traffic and half-close.
- Retry of the next pinned address after a failed nonblocking connect.
- Rejected authority/SNI and oversized or malformed handshakes never connect.
- Admission-audit failure prevents upstream connect.
- Connection limits, absolute handshake timeout, bounded reads, wrapped output
  queues and preservation of unsent bytes under write backpressure.
- Synthetic private payload markers never enter the audit callback.

All 15 guard tests pass locally. Linux-target guard clippy also passes with
`--all-targets --target x86_64-unknown-linux-gnu --locked -- -D warnings`.
A root-only Linux regression runs a real duplex exchange after entering the
unchanged confinement profile in a disposable subprocess. It is included and
cross-checked, but has not been executed on Linux for this revision.

Full workspace verification is retained separately. Real confined Linux
execution, official authentication, continued inference after fork and
suspend/resume, permitted refresh, clean handoff and full zero-canary scans
remain pending the laptop lane. None of these local tests authorizes T14's
format freeze.
