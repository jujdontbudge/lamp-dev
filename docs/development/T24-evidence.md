# T24 hosted side — private implementation evidence, 2026-11-10

**The full ticket Goal is not done.** The private relay journey, immutable token
identity adapter and separate issuer admission path work. Real shared Eilmer
sign-in, account cookie isolation, public hosted routing and trusted fleet
wildcard issuance are not demonstrated. This PR does not turn those gaps green
or open anything publicly. Operational details: [hosted.md](hosted.md).

## Production provisioning follow-up — actual results

The follow-up request authorizes account-gated public addresses and free
production Let's Encrypt issuance. There is no need for a paid plan or a CA
quota increase for this demonstration. **It is still not completed:** the real
account reauthentication steps below require credentials/codes not available to
this session. No failure is being represented as successful production setup.
[Recorded observations](evidence/t24/provisioning-followup.jsonl).

- Repeated the prescribed headless Cloudflare procedure on the Air, using a
  scratch profile and filtered cookie backup, not the owner's visible browser.
  Cloudflare sent it to sign-in. Its authenticated user API returned HTTP 403,
  error **9300**. The copied Google session also shows signed-out accounts.
  The M5 has no usable Cloudflare sign-in either. The known cloudflared/wrangler
  credential locations contain no credential. Secure password requests timed
  out without a response; no password reset was attempted. Consequently no
  authenticated whole-zone inventory or DNS/CAA mutation was performed.
- The M5's copied Clerk session likewise needs sign-in. The working GitHub
  session signs into another Clerk workspace, which does not contain the
  production Eilmer application. It was **not** joined by email and no
  replacement Eilmer app was created. The existing app owner's actual Clerk
  sign-in advertises `email_code`; that factor was prepared. Secure code
  delivery failed at the tool transport, and no code was received. The
  existing production instance was not modified.
- **Created the real GitHub OAuth app `Eilmer`, application `4010008`**, in the
  existing owner GitHub account. Homepage is `https://eilmer.si`; the exact
  callback is `https://clerk.eilmer.si/v1/oauth_callback`, with no callback
  wildcard or device flow. GitHub requires sudo-mode confirmation before
  generating the client secret. Its email-code flow was selected, without
  invoking a passkey/OS approval or phone prompt. The secure code request
  timed out. The secret has **not** been generated or installed in Clerk.
- The Google Cloud credentials page reaches the signed-out account chooser;
  no production Google OAuth client was created. No phone prompt was reached,
  so this is **not** reported as a phone-2FA skip.
- Ubuntu currently has only RFC1918 Wi-Fi IPv4 and link-local/private-tailnet
  IPv6. No new publicly reachable entry point has been configured. The owner's
  main tailnet and existing listeners remain untouched. A DNS record pointing
  at a private address, or TLS termination of browser work at a third-party
  edge, was not substituted for the required ciphertext-only route.

Two implementation changes are ready for the authenticated provisioning work:

1. `lamp-dns-cloudflare` implements the real Cloudflare TXT API effects, with an
   explicit exact-name challenge allowlist, fixed zone, bounded JSON, no
   redirects, no credentials in URLs, and exact-value/idempotent cleanup. It
   cannot request CAA, apex, arbitrary-name or zone-deletion effects. It belongs
   on a separate DNS custodian, invocable by a dedicated SSH forced command;
   the API credential must not be copied to the issuer or relay. The real
   executable's [pre-credential rejection observations](evidence/t24/dns-helper-admission.jsonl)
   cover forbidden requests; **no authenticated DNS effect is claimed**.
2. A fresh root-signed issuance for a fleet with a stored predecessor now sets
   ACME ARI `replaces` to that predecessor. It checks the predecessor's exact
   fleet wildcard before ordering and keeps capacity accounting conservative.
   This provides the forced-renewal order path, **not evidence of a real
   production renewal**.

Focused builds and vet for those changes passed. No trusted fleet certificate,
renewal or CT entry exists from this follow-up. Account code exchange, isolated
browser ingress and the remaining production journey are still open. All
scratch Chrome instances, browser tabs and the Air debugging tunnel used here
were closed; copied browser state was removed. No owner app, Docker resource,
DNS/CAA record or main-tailnet configuration was changed.

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
