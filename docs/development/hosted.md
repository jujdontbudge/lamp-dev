# Optional hosted account, relay and separate issuer

The private owner-hardware demonstration uses the **existing production Eilmer
Clerk instance**, trusted production certificates and browser/native traffic.
See [actual evidence and historical failures](T24-evidence.md). Nothing is opened
to the public Internet; published DNS addresses are RFC1918 and sign-up is closed.

## Components and trust

```sh
go build -o lamp-account ./cmd/lamp-account
go build -o lamp-ingress ./cmd/lamp-ingress
go build -o lamp-relay ./cmd/lamp-relay
go build -o lamp-relay-client ./cmd/lamp-relay-client
go build -o lamp-issuer ./cmd/lamp-issuer
go build -o lamp-dns-cloudflare ./cmd/lamp-dns-cloudflare
```

`lamp-relay` is a separate AGPL-3.0-only executable. Offer its users corresponding
source, including modifications. Clients, account/ingress and issuer code are
Apache-2.0; do not link relay-server code into hosts.

- **Account:** `https://account.eilmer.si`, separate OS identity and leaf TLS key.
  Clerk issuer stays `https://clerk.eilmer.si`. Never merge identities by email.
- **Relay:** routing/account metadata and outer transport TLS only. No fleet
  root/TLS/SSH key, ACME key, DNS API token, model credential or work transcript.
- **Fleet ingress:** owns the exact fleet wildcard key and terminates browser
  work TLS. Its API maps verified issuer/subject to an already enrolled fleet
  principal's private credential. Its content host has its own explicit ACL.
- **Issuer:** separate identity, ACME account/ledger and pinned public fleet roots.
- **DNS custodian:** separate constrained SSH effect endpoint; alone holds the
  zone-scoped Cloudflare token. Neither issuer nor forwarding process gets it.

Browser work follows:

```text
browser -- fleet TLS --> private owner TCP bridge --> relay SNI router
        --> outbound host WebSocket carrier --> fleet TLS ingress --> local app/API
```

The relay reads a bounded ClientHello to select an allocated SNI route, then
forwards the original bytes without completing fleet TLS. Account SNI goes to
the account process; relay SNI goes to the relay's separate leaf-TLS listener.
The raw router itself does not authenticate encrypted HTTP cookies: the fleet
endpoint gates work requests inside end-to-end TLS. The shared 64-slot raw
admission pool covers only bounded ClientHello parsing, not established streams.
Routing (SSH and HTTPS combined) and attachments each have a separate 32-stream
budget per fleet; account/relay ingress use independent control-origin budgets.
Long-lived attachments bypass API slots, and upgraded carriers/connect streams
release their API slots. Splices expire after 30 seconds without byte progress in
either direction, including blocked writes; active one-way traffic refreshes both
endpoints. These relay deadlines do not depend on a fleet's own TLS timeout.

## Account sign-in and origin isolation

All config/credential/state files are private regular files (0600, directories
0700). No secrets in argv values, URLs, logs or evidence. Run:

```sh
lamp-account --config /private/account.json
lamp-ingress --config /private/ingress.json
lamp-relay --config /private/relay.json
```

Account config fields are `listen`, `origin`, `certificate`, `key`, `clients`
(exact HTTPS origins), and `subjects` (`{issuer,subject}` allowlist). Registered
clients are the relay and allocated fleet API/content hosts. Distinct **DNS
hostnames**, not just different ports, are mandatory: cookies ignore ports.

The account server uses production Clerk email codes and keeps frontend cookie
jars/JWTs **server-side**. Its SDK client uses Clerk's original
`https://accounts.eilmer.si` party; no Clerk cookie reaches the product browser.
The existing managed Account Portal is disabled, instance access is invite-only,
and its configured sign-in/sign-out URLs use the canonical isolated account
origin. Sign-up points to `/closed`. No replacement Clerk app/key/user is created
on startup. Additional members require explicit enrollment/configuration.

Product `/account/login` starts a CSRF/state-bound form POST to `/authorize`.
A short-lived one-use code returns by form POST, never a query string. Redemption
at `/redeem` checks exact audience and S256 PKCE verifier. Product sessions are
opaque, in-memory, scoped to that product; account and product restarts fail
closed and require login again. Logout and private CLI-token download require
session CSRF. Clerk refresh verifies RS256, issuer, immutable subject, original
party and token lifetime. Provider failures never fabricate an identity.

Cookies are Secure, HttpOnly, SameSite=Strict, Path=/, without Domain:
`__Host-eilmer_account`, `__Host-lamp_session`, and `__Host-lamp_port` on their
respective, distinct hosts. Exact POST Origin checks and no credentialed CORS
prevent a sibling content origin from reading/mutating account state. Account
pages disallow framing and use nonce-bound scripts. A harmless unprefixed
parent-domain cookie can still exist; it is never used for authentication.

Ingress config has `listen`, `certificate`, `key`, `account_origin`, and `hosts`.
Each host has `origin`, `users` (`identity`, plus `token_file` for API access),
and exactly one of `api_socket` or numeric-loopback HTTP `upstream`. The supplied
content adapter strips **all inbound cookies and Authorization**, platform and
forwarding identity headers before the app; it is not an app-cookie session
adapter. It strips parent-domain/reserved authentication response cookies and
credentialed CORS headers. Configure an app's ACL deliberately; a fleet account
is not automatically entitled to every published app. No sandbox HTML runs on
an account, relay or fleet API origin.

## Relay configuration and native access

```json
{
  "listen": "127.0.0.1:7443",
  "hostname": "relay.eilmer.si",
  "state": "/var/lib/lamp-relay/private/state.json",
  "certificate": "/private/relay-chain.pem",
  "key": "/private/relay-key.pem",
  "issuer": "https://clerk.eilmer.si",
  "audience": "lamp:relay",
  "authorized_parties": ["https://accounts.eilmer.si"],
  "fleets": [{
    "id": "f123",
    "owner": {"issuer":"https://clerk.eilmer.si","subject":"user_REPLACE"},
    "members": []
  }],
  "browser": {
    "origin": "https://relay.eilmer.si:37443",
    "account_origin": "https://account.eilmer.si",
    "raw_listen": "127.0.0.1:7444",
    "private_listen": "10.0.0.2:443",
    "account_target": "127.0.0.1:7445",
    "routes": {"api.f123.fleet.eilmer.si":"f123","p-RANDOM.f123.fleet.eilmer.si":"f123"}
  }
}
```

Use an allocated lowercase random content label in place of `p-RANDOM`.
`private_listen` is optional and accepts only explicit RFC1918 IPv4. Raw/TLS
service targets are numeric loopback. Only the direct private listener needs
low-port binding privilege; it must not acquire private-key read privileges.

The relay page exports its own opaque session to a private file for the CLI:

```sh
lamp-relay-client issue-enrollment --fleet f123 \
  --account-token-file /private/relay-account --out /private/enrollment
lamp-relay-client enroll --fleet f123 --enrollment-file /private/enrollment \
  --credential-file /private/host-credential
lamp-relay-client host --credential-file /private/host-credential \
  --target 127.0.0.1:2222 --https-target 127.0.0.1:7460
```

`--relay-dial-address` may override only the numeric-loopback dial target for
private deployment; it preserves normal TLS hostname verification. The host
creates its reconnect secret locally before one-use enrollment redemption.
Enrollment consumption plus host hash is an fsynced atomic update with a process
lock. A timed-out redemption may have committed: retain the credential and try
hosting before revoking/re-enrolling. Owner `DELETE /v1/hosts/f123` revokes the
host, pending enrollments and streams. The state stores hashes/metadata, not work.

The legacy explicit JWT input requires a Clerk template with audience
`lamp:relay` and the configured original account party. The demonstrated browser
path instead exports a product-scoped opaque token; **no template is needed**.
JWT API endpoints reject browser Cookie/Origin. Browser login is handled only by
the separate browser routes, not by loosening native endpoint checks.

For SSH use an independently trusted fleet CA, strict host checking, a separate
user SSH key, and `ProxyCommand lamp-relay-client connect --fleet f123
--account-token-file /private/relay-account`. Relay membership does not create
SSH/fleet authority. Token expiry and host revocation terminate streams.

## Owner-only private TCP bridge

No bore.pub, Serveo, third-party tunnel or edge TLS termination is used. M5 runs
verified bore 0.6.0; Ubuntu connects outbound to M5's private LAN address. The
server binds that address, control port 7835 and only allocated tunnel port 37443.
The shared transport HMAC is loaded from a 0600 file, never argv.

`tools/hosted/private_bridge.py` launches the M5 server under `sandbox-exec`
with a minimal environment, read-only system-library access, no home-directory
data access, no outbound-connect grant and only the two listening ports.
Seatbelt's local IP matcher supports wildcard/localhost only: port confinement
is in the sandbox and the explicit LAN bind is in bore's verified CLI arguments.
The optional open-only credential probe never reads or prints credential bytes.
Provide `--binary`, `--bind`, `--port`, `--secret-file`, `--log`, `--pid-file`, and
optionally `--denied-file-probe`. No sudo or Mac launch-item bootstrap is needed.
Ubuntu's client runs under the relay OS identity and forwards loopback 7444.

## Separate certificate issuer

Fleet generates its TLS key and CSR locally. The issuer accepts only a pinned
Ed25519 root proof binding allocation, one-minute nonce and CSR DER hash.
`fleet_roots` allocates exact `*.f123.fleet.eilmer.si`; `services` separately
allocates exact `relay.eilmer.si`/`account.eilmer.si` leaves to public root keys.
No platform-wide wildcard/private key is distributed.

Private config: `state_dir`, `directory`, optional `email`, `fleet_roots`,
`services`, `weekly_capacity` (default zero), `authoritative_dns`, `dns_helper`
and `dns_helper_sha256`. Capacity is an actual allocated shared-domain slice,
not an assumed quota increase; failures conservatively retain reservations.

```sh
lamp-issuer init-account --config /private/issuer.json
lamp-issuer challenge --config /private/issuer.json --fleet f123
lamp-issuer request --fleet f123 --nonce NONCE --csr /private/fleet.csr \
  --root-key /private/fleet-root.pem --out /private/request.json
lamp-issuer issue --config /private/issuer.json --request /private/request.json \
  --out /private/fleet-chain.pem
```

Only DNS-01 is enabled. Authoritative CAA must agree and bind ordinary/wildcard
issuance to the account URI and DNS-01, at the fleet boundary and allocation.
Service CAA denies wildcards. Preserve apex CAA, delegations, aliases, mail and
other products; inventory the whole zone before administration. The Cloudflare
custodian config is `{zone_id,token_file,records}` with exact challenge names.
Its fixed SSH command accepts only `{action:present|cleanup,name,value}`; cleanup
is exact-value/idempotent. It cannot mutate CAA, arbitrary names or other types.

A fresh root proof through `issue` forces renewal, binding the persisted
predecessor with ACME ARI `replaces`. Missing policy/capacity or provider failure
never activates plaintext/self-signed routing. `certificate --fleet ... --out
...` recovers a persisted chain after a lost response. The demonstration issued
four production certificates using a separately allocated capacity of four.
Automatic renewal scheduling/shared usage ingestion are not implemented.

`tools/hosted/ct_evidence.py` (Python `cryptography` required) verifies public SCT,
STH and RFC6962-v1 inclusion against a matching fetched entry. Output is
exclusive-create so missing observations are preserved. Tiled-only logs remain
explicitly unobserved; this is an observation tool, not a continuous CT monitor.

## Local/own-provider operation and cleanup

Local SSH/public keys and opaque fleet tokens do not contact Clerk or the relay.
Optional `lamp-access` OIDC config supplies `issuer`, `audience`, `jwks_url` and
`authorized_parties`, mapping verified issuer/subject to enrolled `token_file`.
It supports exact-party RS256 JWT input, not an owner-IdP browser discovery UI.
Email is ignored; opaque local tokens work even while that provider is offline.

Stop only ticket-owned bridge/carrier/account/relay/ingress/app processes. The
browser demonstration's `stop-live` signal cleans its disposable control/app
processes; it allocates no VM. Revoke host enrollment before discarding reconnect
credentials. Remove only recorded ticket-owned address/CAA records if retiring
the deployment; preserve all original records and account identities. Never
remove shared Clerk users/keys, original DNS, owner services, Docker resources,
or main-tailnet settings as generic cleanup. Private operator state stays out
of the repository. Public rollout, signup and unattended operations are separate
from this private demonstration.
