# T24 hosted side — private owner-hardware evidence

## Current result — 2026-11-11

Real shared production Clerk sign-in, code exchange, isolated cookies, trusted
fleet HTTPS and native ciphertext forwarding are demonstrated on owner hardware.
**No public Internet route or public signup was opened.** This follows the later
private-only instruction; it is not evidence of an Internet deployment.
Operational details: [hosted.md](hosted.md). Earlier failed results remain below,
explicitly historical, rather than being relabelled by later successes.

### Real Clerk → private relay ingress → fleet browser journey

- [Sign-in observation](evidence/t24/production/alice-signin-observation.jsonl)
- [Both users' browser journey](evidence/t24/production/browser-journey.jsonl)
- [Runtime/credential separation](evidence/t24/production/runtime-observations.jsonl)
- [Private provisioning](evidence/t24/production/private-provisioning.json)
- [Content screenshot](evidence/t24/production/browser-content.png)
- Recipes: [sign-in](evidence/t24/production/browser-signin.py),
  [browser](evidence/t24/production/browser-journey.py),
  [real control/app startup](evidence/t24/production/start-live-fleet.py),
  [public README app](evidence/t24/production/public-readme-app.py).
  Operator credentials/config/storage states remain outside the repository.

The original Eilmer production instance and issuer `https://clerk.eilmer.si`
were reused. Exactly one provisioning key and two disposable users were created;
no replacement app or email-based identity merge was performed. Alice and Bea
both completed actual production email-code sign-in. The account server retains
Clerk client cookies/JWTs server-side; the browser gets only host-prefixed opaque
account/product sessions. The original managed Account Portal was disabled,
access mode is invite-only, and sign-in/sign-out now point at
`https://account.eilmer.si`; signup points to its `/closed` page. Original Clerk
DNS names/issuer were not replaced.

M5's **self-hosted** bore 0.6.0 forwards private LAN TCP to Ubuntu; neither
bore.pub nor any third-party tunnel/edge terminates traffic. The M5 forwarding
process is sandboxed with a minimal environment and cannot open the actual DNS
custodian credential. Ubuntu's relay identity cannot read account, ACME, fleet
TLS or fleet-root keys. Only exact private address records were added. All 19
original zone records remain byte-for-byte identical in the authenticated final
snapshot (31 records total: original 19, eight CAA and four private A records).

Both browsers reached real `lampd` fleet `fl_local` and the public
`octocat/Hello-World` README app through the raw SNI relay and outbound host
carrier. Chromium used normal certificate validation: TLS 1.3, issuer YE1,
subject `*.f24demo.fleet.eilmer.si`. Fleet ingress, not the relay/bridge,
terminates work TLS. The API and random content label are distinct hosts under
`f24demo.fleet.eilmer.si`, reached on private bridge port 37443. Account uses its
separate leaf and private canonical port 443.

For each user, auth cookies were Secure, HttpOnly, SameSite Strict and scoped to
exact account, relay, API and content hosts. There were no JS-visible auth
cookies. Deliberate parent-domain response cookies were filtered; attempts to
toss host-prefixed parent cookies did not change account/product sessions. A
harmless unprefixed parent cookie remained JS-visible but was ignored for auth.
The content script could not read a credentialed sibling account response due
to CORS; a separate credentialed HTTP POST with that sibling Origin received
403. **The browser response listener did not itself observe that status.** The
app upstream recorded no Cookie names and no Authorization header.

The new browser demonstration allocates **zero VMs**, uses a real control daemon
and real public project app, and does not claim the app ran inside a guest. The
earlier native Firecracker journey below separately demonstrates guest access
and Alice/Bea resource isolation. No private worker RPC was added. Observed relay
state/log files lacked the public work marker; no memory/packet or comprehensive
zero-leak measurement is claimed.

Earlier browser iterations failed on Clerk frontend warmup/User-Agent handling,
actual `sia_` sign-in IDs, nested JWT response extraction, and an Origin lost by
`no-referrer`. Those paths were corrected and the final executions completed.
One post-restart inbox fetch returned 429; Alice's old account session correctly
failed closed, and a fresh email sign-in completed. The initial M5 confinement
profile also failed admission on unsupported IP matcher syntax and dyld root
access; the final profile, denied-open observation and browser executions are
separate results, not relabelled initial successes.

### Provider setup and requested verification

[Google provisioning](evidence/t24/production/google-oauth-observation.json): a
separate free `eilmer-shared-account` project owns the Web OAuth client, with
exact callback `https://clerk.eilmer.si/v1/oauth_callback`. Its custom credentials
were installed in the **existing production Clerk Google connection**, clearing
"Setup required". Billing is explicitly disabled/unlinked on this new project.
Google consent remains private Testing with only the owner test user; no public
Google rollout is claimed. An earlier direct billing read returned 429; the
subsequent explicit unlink returned `billingEnabled: false`. No phone prompt
was reached, so this is not reported as a phone-2FA skip.

**GitHub social login is not configured.** The existing `Eilmer` OAuth app
4010008 has the exact same callback and no device flow/wildcard, but GitHub
requires sudo email verification before client-secret creation. The coordinator
reported no forwarded code after two requests and more than ten minutes. The
earlier "requested" report had only selected the email method; the follow-up
**Verify via email** action did explicitly show that a code was sent. No delivery
was observed. At the coordinator's instruction, requests stopped and no GitHub
client secret was created or installed. All ticket-owned headless GitHub tabs
were closed, leaving no sudo prompt pending; no passkey/OS approval was invoked.
[Closure observation](evidence/t24/production/github-oauth-not-configured.json).

The completed demonstration uses **production Clerk email-code sign-in**, not
GitHub social login. GitHub delivery is therefore a recorded provider limitation,
not an outstanding dependency of the private T24 demonstration.

The requested **single `make verify` invocation passed** (exit 0): workspace
Go/Rust/C builds, selected existing smokes, contracts, interoperability and clean
public codegen. [Full output](evidence/t24/production/make-verify.log). No new unit
tests, Makefile/CI gates, benchmarks, trial quotas or performance prerequisites
were added. This is not a release target/zero-leak measurement or public rollout.

## Trusted production certificates — 2026-11-11

[Observations](evidence/t24/production/observations.jsonl) and
[certificate metadata](evidence/t24/production/certificates.json) record actual
Let's Encrypt **production**, not staging. A fresh authenticated Cloudflare
inventory found 19 existing records. All 19 remained identical after issuance;
only eight new scoped CAA records were added. The apex CAA, root ACME alias,
mail/DKIM records, existing Clerk names, Apple Bridge delegation and website
records were preserved. Full zone snapshots stay private outside this repo.

The separate issuer runs as `lamp-t24-issuer` on Ubuntu. Its ACME account URI is
`https://acme-v02.api.letsencrypt.org/acme/acct/3983124462`. The DNS API token
remains on M5, behind the real exact-name DNS custodian and a dedicated SSH key
with `restrict` and a fixed command. The issuer receives no Cloudflare token.
The live forced-command helper denied an unallocated name, and an allowed
cleanup with an already absent value was idempotent against the real API.

Both authoritative servers agreed on account-bound ordinary/wildcard CAA and
DNS-01-only validation at `fleet.eilmer.si` and `f24demo.fleet.eilmer.si`.
The first wildcard certificate SHA-256 is
`bcbd098328da7c8ad33c7623cd2b957b0e4d257ecc40c6b6443abc0bd8b6ac3f`.
A fresh fleet-root proof then forced a replacement using the persisted
predecessor through ARI. The renewed certificate has a new serial and SHA-256
`12c6c7f1b1425a40fdccbe333a318701e6c7628fe7b3ba8a00cdbf5a1c938d66`.
`openssl verify` with the normal system trust store and hostname
`api.f24demo.fleet.eilmer.si` returned **OK**. Public certificate chains are
retained beside the metadata; no private key is in evidence.

The [actual first-certificate CT entry](evidence/t24/production/first-ct-inclusion.json)
was fetched from **Google Argon2027h1**. The observer verified the SCT signature,
signed tree-head signature and Merkle audit path, and matched the fetched
`leaf_input` to the issued precertificate. `tools/hosted/ct_evidence.py` records
this public evidence. The renewal's initial CT observation is separately
retained as **not yet included in that observed signed tree**; a signed SCT alone
was not called an inclusion. A later
[renewal follow-up](evidence/t24/production/renewed-ct-followup.json) independently
verified its Argon2027h1 SCT, STH, matching fetched entry and Merkle inclusion.
The initial missing observation remains unchanged. Tiled-only logs are explicitly
unobserved by this RFC 6962 v1 observer, not passed.

Separate exact-leaf certificates were also issued for `relay.eilmer.si` and
`account.eilmer.si`, using different keys. Their CAA allows only this ACME
account's DNS-01 issuance and forbids all service wildcards (`issuewild ";"`).
Actual OS permission checks showed that `lamp-t24-relay` cannot read the account,
ACME or fleet TLS keys, and that the issuer cannot read the fleet root key.
Four production certificates were issued in total, including the one forced
renewal; no paid plan or quota increase was used. DNS-01 cleanup left no new
challenge TXT records. No public A/AAAA route or sign-up endpoint was opened.

At the certificate-only checkpoint, account reauthentication and browser
routing were still incomplete. The later private journey above records their
actual completion without turning this earlier checkpoint into that result.

## Historical 2026-11-10 provisioning follow-up — failed/partial results

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

## Historical 2026-11-10 remaining-work snapshot

These were the open items at that checkpoint, not the current status above.

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

## Historical 2026-11-10 iteration checks and scope

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
