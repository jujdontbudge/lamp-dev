# Optional hosted relay and separate issuer

Status: implemented private SSH relay and issuer admission path; **T24 is not
fully demonstrated**. See [the evidence and remaining work](T24-evidence.md).
Nothing in this change opens public sign-up, creates public DNS, or changes the
owner's existing Clerk application, tailnet, Docker resources or other products.

## Build and trust boundary

```sh
go build -o lamp-relay ./cmd/lamp-relay
go build -o lamp-relay-client ./cmd/lamp-relay-client
go build -o lamp-issuer ./cmd/lamp-issuer
go build -o lamp-dns-cloudflare ./cmd/lamp-dns-cloudflare
```

The relay server is a separate AGPL-3.0-only binary. The host/client, shared JWT
verifier and issuer are Apache-2.0. Offer relay users the corresponding source,
including deployed modifications. Do not link relay server code into hosts.

The relay owns outer TLS and account/routing metadata, **not** inner SSH keys.
Only the host terminates SSH. Use OpenSSH with strict host verification against
a fleet CA obtained locally or from a previously trusted caller. A CA supplied
by the relay is not first-pairing trust. A valid relay account/membership cannot
create a fleet principal, bypass SSH user authentication, recover a root, or
read another principal's private resources.

Version 1 uses outbound WebSockets: one host carrier per fleet, one one-use
attachment per caller. The host connector reaches only its configured numeric
loopback SSH listener. This is not a browser HTTPS/SNI ingress, QUIC multiplexer
or public stock-SSH port allocator. Neither Cloudflare edge decryption nor
private worker RPCs are involved.

## Relay configuration

All config, enrollment, token and state files are private (0600; directories
0700). Keep credentials out of argv, URLs, logs and evidence.

```json
{
  "listen": "127.0.0.1:7443",
  "hostname": "relay.eilmer.si",
  "state": "/var/lib/lamp-relay/private/state.json",
  "certificate": "/etc/lamp-relay/private/relay.pem",
  "key": "/etc/lamp-relay/private/relay-key.pem",
  "issuer": "https://clerk.eilmer.si",
  "audience": "lamp:relay",
  "authorized_parties": ["https://accounts.eilmer.si"],
  "fleets": [{
    "id": "f123",
    "owner": {"issuer": "https://clerk.eilmer.si", "subject": "user_REPLACE"},
    "members": [{"issuer": "https://clerk.eilmer.si", "subject": "user_REPLACE_BEA"}]
  }]
}
```

Production requires the **existing shared Eilmer Clerk issuer**, exact
`lamp:relay` audience and existing account portal origin. Provision a short-lived
Clerk token template with that audience; only the account portal is an allowed
`azp`. JWT signature, RS256 algorithm, key ID, issuer, subject, audience, party,
expiry/not-before/issued-at and maximum one-hour lifetime are checked. Public
JWKS is cached for five minutes; unknown rotated keys fail closed until refresh.
Email is ignored. Membership is an explicit issuer/subject configuration, not
self-registration. Removing members requires a process restart, terminating
existing streams; host revocation also cancels active carriers/streams.

The existing portal is `accounts.eilmer.si`, not the proposed architecture's
`account.eilmer.si`. Do not move it or change the issuer without checking other
products and explicitly migrating provider identities. Never join old/new
identities by email. The browser sign-in/code exchange and real Clerk cookie
scope still need implementation/provisioning; a token file is currently an
operator-provided input, not a completed sign-in UX.

```sh
lamp-relay --config /private/relay.json
lamp-relay-client issue-enrollment --fleet f123 \
  --account-token-file /private/alice.jwt --out /private/enrollment
# Securely hand only the enrollment file to the intended host; redeem within 60s.
lamp-relay-client enroll --fleet f123 --enrollment-file /private/enrollment \
  --credential-file /private/host-credential
lamp-relay-client host --credential-file /private/host-credential \
  --target 127.0.0.1:2222
```

The host generates its reconnect secret locally, writes it before redemption,
and sends only its SHA-256. Enrollment consumption and the host hash are one
fsynced atomic state update, protected against concurrent server processes.
If redemption times out, retain the credential: the enrollment may already be
consumed. Try hosting with it before asking the owner to revoke/re-enroll.
`DELETE /v1/hosts/f123` with the owner's account bearer revokes the host and all
pending enrollments before allowing replacement.

Example OpenSSH options (use ordinary `-i` for the separate fleet user key):

```text
Host h-private.f123.fleet.eilmer.si
  StrictHostKeyChecking yes
  UserKnownHostsFile /private/fleet-known-hosts
  ProxyCommand /path/lamp-relay-client connect --fleet f123 --account-token-file /private/alice.jwt
```

`fleet-known-hosts` contains the independently obtained `@cert-authority` entry.
The relay forwards the resulting SSH stream without transcript logging or disk
spooling. It persists only versioned host/enrollment hashes and enrollment
expiry. It sees account/fleet/address/timing/byte metadata; it cannot hide this
metadata. Existing streams stop at account-token expiry. The server bounds
concurrent requests/streams and each host connector bounds active SSH links.
One-use routing attachments time out after ten seconds.

There are no browser sessions here: Cookie, Origin and query-bearing requests
are rejected, CORS is not enabled, and no Set-Cookie is emitted. Host/SNI must
match `relay.eilmer.si`; production TLS is 1.3 minimum. Cross-origin/frame/cache
headers are restrictive. This does **not** prove isolation of Clerk's existing
cookies or provide sandbox web ingress. No untrusted HTML is served here.

`private_development: true` permits only numeric loopback listeners and a
numeric loopback HTTP identity signer. Clients permit plain HTTP only at numeric
loopback. The demonstration forwards only that Ubuntu loopback relay over SSH;
this SSH tunnel is a private development transport, not a second hosted domain
or a public relay deployment.

## Non-relay local accounts and owner OIDC

Existing local SSH/public-key and opaque fleet-token routes do not construct a
hosted account verifier or contact Clerk. `lamp-access` additionally accepts an
optional owner OIDC JWT provider:

```json
"oidc": {
  "issuer": "https://id.eilmer.si/owner",
  "audience": "my-fleet",
  "jwks_url": "https://id.eilmer.si/owner/keys",
  "authorized_parties": ["https://account.eilmer.si"]
}
```

These are configuration-shape examples, **not provisioned services**. Owner
OIDC may be the fleet owner's existing provider; Eilmer-hosted services never
use another domain. Add each user's immutable `identity: {issuer,subject}` next
to its existing `token_file`. The verified JWT maps to that user's enrolled
fleet credential, never to an email or an unauthenticated socket owner. The JWT
is not forwarded to the control daemon. Requests/streams are bounded by JWT
expiry. The JWT API rejects browser origins/cookies. Supported input is RS256
JWT with exact audience and `azp`, obtained through the owner's provider; there
is no new code+PKCE discovery/login UI. Omit `oidc` and user `identity` for a
fully local-only configuration. A local opaque token does not need JWKS even
when an OIDC provider is configured and unavailable.

## Separate fleet certificate issuer

Run `lamp-issuer` as a different OS principal/machine from the relay. Never copy
its ACME account key, ledger, DNS helper or DNS credentials onto the relay. The
fleet creates its TLS private key and CSR on its own approved ingress host.
The issuer receives only the CSR and an Ed25519 fleet-root proof binding fleet,
one-minute one-use issuer nonce and SHA-256 of CSR DER. Its private registry
pins the root for each allocated fleet. Exact CSR name is
`*.f123.fleet.eilmer.si`; no per-sandbox issuance or platform-wide fleet key.

Private issuer config fields:

- `state_dir`, `directory` (LE production or staging), optional contact `email`.
- `fleet_roots`: fleet ID to base64 Ed25519 public key.
- `weekly_capacity`: **default zero**. This is the actually allocated slice of
  shared-domain capacity after other products and repair reserve, not a quota
  increase. It can be at most 50; failed attempts conservatively retain their
  reservation for seven days. No production allocation was obtained here.
- `authoritative_dns`: numeric authoritative server addresses with port 53.
- `dns_helper`, `dns_helper_sha256`: a separately provisioned, digest-pinned
  executable with permission only for the allocated DNS-01 challenge records.
  It receives JSON `{action:present|cleanup,name,value}` on stdin; cleanup must
  remove only the matching value and be idempotent. No shell or captured helper
  output is used. `cmd/lamp-dns-cloudflare` supplies the Cloudflare helper;
  its production credential and custodian deployment are not yet provisioned.

```sh
lamp-issuer init-account --config /private/issuer.json  # account URI is public
lamp-issuer challenge --config /private/issuer.json --fleet f123
# On the fleet host, using the returned public nonce:
lamp-issuer request --fleet f123 --nonce NONCE --csr /private/fleet.csr \
  --root-key /private/fleet-root.pem --out /private/request.json
# Back at the separate issuer:
lamp-issuer issue --config /private/issuer.json --request /private/request.json \
  --out /private/fleet-chain.pem
```

Only DNS-01 is enabled. Both `fleet.eilmer.si` and the exact allocated fleet
subtree must have ordinary and wildcard CAA restricted to the registered ACME
account URI and `validationmethods=dns-01`. All configured authoritative servers
must answer authoritatively and agree. CNAME/DNAME answers and additive
permissive policies are refused. Domain administration must separately inventory
all descendants, delegations and aliases before activation; this CLI does not
claim a whole-zone audit or CT monitor. Do **not** change apex CAA used by other
Eilmer products. Inspect their records/usage before assigning any capacity.

For Cloudflare, run the helper under a separate DNS-custodian identity with
private config `{zone_id,token_file,records}`. `records` must explicitly enumerate
names such as `_acme-challenge.f123.fleet.eilmer.si`; arbitrary suffix grants are
not accepted. Use a zone-limited DNS-edit API token. Keep that token on the DNS
custodian, **not** the issuer. A dedicated SSH authorized key with `restrict`
and a fixed `command="/path/lamp-dns-cloudflare --config /private/dns.json"` can
expose only the JSON effect protocol to the issuer. Do not reuse the owner's
unrestricted SSH key. The helper lists only the exact TXT name, preserves other
values, and deletes only matching challenge values. Neither CAA nor other DNS
record types can be requested through this helper.

To force renewal, obtain a fresh issuer challenge and submit a fresh fleet-root
proof for the CSR through the same `issue` command. If a certificate is already
persisted for that fleet, the ACME order binds `replaces` to it using ARI. A corrupt
or foreign-fleet predecessor fails closed. Capacity reservations remain
conservative even for replacements. This path still needs its production run;
a build is not renewal evidence.

An absent policy, exhausted allocation or ACME failure never returns a ready
certificate or falls back to plaintext. Successful chains are checked against
the authorized CSR and persisted; `certificate --fleet f123 --out ...` retrieves
them after a lost response. Automatic ARI renewal, shared product usage ingestion,
CT monitoring, fleet route activation and trusted public issuance remain open.
