# T24 hosted side — private implementation evidence, 2026-11-10

**The full ticket Goal is not done.** The private relay journey, immutable token
identity adapter and separate issuer admission path work. Real shared Eilmer
sign-in, account cookie isolation, public hosted routing and trusted fleet
wildcard issuance are not demonstrated. This PR does not turn those gaps green
or open anything publicly. Operational details: [hosted.md](hosted.md).

## Real private M5 → Ubuntu → fleet → Firecracker journey

- [Ubuntu observations](evidence/t24/ubuntu-observations.jsonl)
- [M5 observations](evidence/t24/m5-observations.jsonl)
- Recipes: [Ubuntu](evidence/t24/demonstration.py),
  [M5 driver](evidence/t24/m5-driver.py)

The development relay ran on the Ubuntu laptop. Only its loopback port was
forwarded to M5 over the existing SSH connection; the fleet SSH listener and API
socket were **not** forwarded. The host connector maintained its own outbound
carrier and dialed only the private loopback `lamp-access` listener. This shows
private remote reachability without an inbound fleet listener, not Internet
exposure through a newly provisioned `relay.eilmer.si` address.

The actual T18 `lamp-access`, `lampd`, installed supervisor and Firecracker engine
were used, not protocol stand-ins. Alice and Bea had distinct SSH keys and
immutable issuer/subject enrollments in the real team fleet. The relay's **local
RSA/JWKS signer was a disposable fixture, not Clerk**. All three account tokens
intentionally carried the same disposable email claim.

Before the sole allocation in the successful journey, **1 Firecracker process**
was live and **26,479 MiB** was available. One sandbox reached ready. From M5,
ordinary OpenSSH with a locally obtained, strictly checked fleet host CA carried
SSH through the relay. Alice wrote/read the public `octocat/Hello-World` README
inside that guest and printed nonce `1e78b1923d9d61e6836f2f9e`.
Bea authenticated to the same fleet with her own key but Alice's sandbox returned
`control sandbox_get: NOT_FOUND`. An outsider with the same email claim failed
relay membership. Bea's relay token could not authenticate Alice's SSH username
with Bea's key. Routing membership did not become fleet authority.

Enrollment was consumed once, a second redemption was refused, and it remained
refused after a relay process restart. The locally generated reconnect secret
continued to work through that restart. The relay persisted only version,
host credential hashes and enrollment metadata; the public guest content and
nonce were absent from that state. There was **no** relay memory/packet capture
or comprehensive secret-leak measurement; no zero-leak certification is claimed.

The sandbox was deleted and the worker grant revoked. The final independent
Firecracker count was **0**. All ticket processes and the development SSH tunnel
were stopped. No Docker resources, visible apps, owner projects or main-tailnet
ACLs were used or changed. Private credentials remain outside the repository.

### Earlier failed attempt retained

[Attempt 1](evidence/t24/attempt-1.jsonl) reached a ready guest, but the M5 driver
hung while `subprocess` captured a background SSH tunnel's inherited pipes. The
120-second command timeout was a failed attempt, not a successful journey. The
worker was signalled to clean up, the guest was deleted, and its final count of
**1** reflected another ticket's live guest. The historical event named
`m5-driver-complete` in that log merely reflects the manually created cleanup
signal, **not** a completed M5 journey. The corrected driver gives the tunnel a
private file log. No benchmark loop or prescribed trial count was used.

## Non-relay local and own-OIDC operation

[Observations](evidence/t24/owner-oidc-observations.jsonl) and
[recipe](evidence/t24/owner-oidc-demonstration.py) use the actual M5 `lampd` and
`lamp-access`, a disposable local OIDC JWT signer and no VM.

A verified owner-provider issuer/subject read real fleet `fl_local` without the
relay or an Eilmer account. A wrong audience and a different subject with the
same email were refused. After stopping the provider, an opaque local account
credential still read the fleet without fetching JWKS or contacting Clerk.
This proves the token adapter and local-account behavior, **not** a browser
code+PKCE flow or sign-in against a real third-party owner IdP.

## Independent certificate issuer

[Observations](evidence/t24/issuer-observations.jsonl) and
[recipe](evidence/t24/issuer-demonstration.py) use the separate `lamp-issuer` on
M5 and a **real, free Let's Encrypt staging ACME account**. Its private account
key/ledger stayed on M5, not on the Ubuntu relay. Fleet TLS keys and root keys
were generated separately and never sent to the issuer/relay respectively.

A correctly fleet-root-signed CSR for another fleet's wildcard was refused. A
forged fleet proof was refused. A valid exact-fleet CSR with **zero allocated
production/shared-domain capacity** returned `CERTIFICATE_ISSUANCE_DEFERRED`
without an ACME order or plaintext fallback. The unconfigured public CAA check
also refused activation. **No public certificate was issued**, no DNS record or
CAA was changed, and no paid service was used. This does not prove DNS-01 helper
operation, authoritative policy readiness or actual trusted wildcard issuance.

## Remaining product/provisioning work

- Use the existing shared Clerk application, complete its relay audience token
  template, account sign-in/code exchange and production provider setup. No
  replacement Clerk app was created. Existing memory also records unfinished
  production Google/GitHub OAuth credentials.
- Verify actual Clerk/account cookies and product sign-in isolation in headless
  disposable sessions. The relay's explicit rejection of Cookie/Origin is not
  evidence about Clerk's cookie scopes. No account origin was migrated.
- Inventory other `eilmer.si` products and their DNS/certificate use before
  provisioning any record or policy. Public DNS inspection found no relay A
  record or fleet-subtree CAA, while the apex already allows multiple CAs.
  No apex policy was touched. T18's evidence records the saved Cloudflare
  headless session as signed out; this work did not mutate that session.
- Provision the separate constrained DNS helper, account-bound DNS-01 CAA,
  actual shared-domain certificate allocation, trusted wildcard issuance,
  renewal/CT operations and fleet route activation. No increased CA capacity
  was obtained or assumed; new issuance remains deferred with capacity zero.
- Native/browser HTTPS ingress, per-content origins/cookies and the account
  page are not implemented by this SSH-only relay. Nothing is publicly opened.

## Iteration checks and scope

Focused Go builds and `go vet` cover the new commands/packages and access
adapter. No unit tests, Makefile, CI or test-suite files were added or modified;
no full suite or benchmark was run. Two already pinned dependencies became
direct imports; no dependency versions changed.

The relay journey's binary hashes identify the demonstrated build. Subsequent
small hardening changes bind late attachment cleanup to the caller context,
require production audience/Host/SNI/TLS 1.3, and share the verifier with the
non-relay OIDC adapter. The guest journey was not repeated to relabel it as a
run of those later binaries. The issuer and own-OIDC observations separately
identify what their real executions covered; all remaining gaps stay open.
