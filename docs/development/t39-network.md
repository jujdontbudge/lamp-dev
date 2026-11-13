# Sandbox networking (T39)

Ordinary Linux Firecracker sandboxes use a generation-scoped `lamp-network`
sidecar beside the supervisor's `lamp-guard` binary. Install both binaries in
that directory. The supervisor opens the namespace TAP and audit files before
the sidecar drops to its assigned UID. The namespace has no kernel uplink;
all traffic passes through the userspace director. Private subscription
custody keeps its protected adapter.

## Preview a server

Start a server bound to `0.0.0.0` in the sandbox, then:

```sh
lamp port expose --sandbox-id "$SB" --port 3000 --json
lamp port share-link --port-id "$PORT" --expected-revision "$REV" --json
```

The first command returns `data.url` and the port resource. The share operation
returns a URL once. Open that URL in a browser: its fragment is exchanged for
an HttpOnly cookie and removed from browser history. Scripts send
`Authorization: Bearer <fragment token>` to the port's origin. Treat the share
URL as a secret. It is absent from durable control state and operation replay.
Without a share, private previews return 403. `port update --access public`
allows requests without a share. `grant revoke` closes a share's active
connections; `port unpublish` closes the listener. Both require the resource's
current expected revision. `expires_at` bounds ports and shares.

Each preview has a separate `http://p-<random>.localhost:<port>` origin bound
only to host loopback. HTTP, WebSocket upgrades and SSE use the same proxy.
For another machine, the owner must tunnel this exact loopback listener and
preserve the origin's Host header. Public means no preview authentication;
it does not expose a host interface to the Internet.

## Live policy

```sh
lamp network-policy create --filter-mode transport --default-egress deny \
  --allow-json '[{"host_pattern":"registry.npmjs.org","protocol":"tcp","ports":[443]}]' --json
lamp sandbox update --sandbox-id "$SB" --expected-revision "$REV" \
  --network-policy-id "$POLICY" --json
```

`network-policy update` changes attached ready sandboxes live. Their
`effective_policy_revision` advances after acknowledgement. `sandbox create`
also accepts `network_policy_id`; create, fork and resume install it before
guest execution. Attach `network_policy_id=none` to remove the sandbox layer.
Host/LAN denies and parent/CI ceilings remain immutable. New guest init repairs
missing or loopback-only resolver files inherited from seed build hosts.
Historical templates with an unserved systemd stub need a rebuilt guest init
or a resolver configured in their seed. Exact lowercase DNS
names and CIDRs select numeric TCP/UDP destinations and ports. This is
transport filtering: shared IPs are not distinguished by TLS or HTTP site.
`strict_sites` is rejected. Guest DNS is mediated by the selected provider.

## Private networks, exits and owner services

Create a `configuration` with `adapter_id=lamp.network/1`, a `schema_digest`
and `document={"type":"private","name":"n1"}`. Use its returned digest in
`config_ref={"configuration_id":"...","digest":"..."}` for
`network-attachment create --type private_member --sandbox-id ...` on both
sandboxes. Membership alone opens no port. Create a `connection` with
`type=sandbox_link`, `access=full`, and
`resource_policy={"source_sandbox_id":"A","target_sandbox_id":"B","port":8080}`.
Only A can dial the returned endpoint, only on TCP/8080. Revoke the connection
to close active flows. Membership, links and previews bind a generation and
are not copied into a fork or resumed generation.

An owner can enroll an exit configuration with `document={"type":"direct"}`
or `{"type":"ssh_connect","endpoint":"127.0.0.1:PORT"}` and attach it as
`type=exit`. The latter points at an owner-authenticated SSH tunnel to Lamp's
CONNECT endpoint; credentials stay in SSH. Each sandbox selects its own exit.
Exit loss fails closed with no direct fallback. Exit selection is fixed for
the generation; release the sandbox to remove it.

An owner service connection has `type=service`, a numeric loopback `endpoint`,
`resource_policy={"source_sandbox_id":"A"}` and `access` chosen by the owner.
`off` and `ask_first` refuse autonomous traffic. To approve access, the owner
revokes that connection and creates one with `read_only` or `full`.
`read_only` permits HTTP GET/HEAD/OPTIONS and rejects writes, CONNECT and
upgrades; `full` opens TCP to that exact service. Read-only HTTP methods assume
the enrolled service respects HTTP semantics. Guest requests use the returned
sandbox alias, never the real host address.

## Current limits

Native macOS VZ has no data NIC in the existing engine and rejects managed
network calls with `ENGINE_UNQUALIFIED`. These features currently require the
Linux worker. Private custody uses its existing adapter and rejects these
new resources. Configuration enrollment and host services require owner
authority. Network resources survive a control-daemon restart while their
supervisor generation lives; generation release ends the actual network.
