# Publishing 0.1.2

> **Moved to GitHub (2026-11-16).** Development happens in
> https://github.com/jujdontbudge/lamp-dev and the public project is
> https://github.com/jujdontbudge/lamp, with its releases, tags and files.
> Use `gh` for source pushes, pull requests and releases (`gh release create`,
> `gh release upload`). The steps below record how 0.1.2 was published on
> GitLab and are kept as history; where they say GitLab, MR or `glab`, or that
> GitHub must not be accessed, read GitHub, PR and `gh` for any new work. The
> GitLab projects stay as read-only backups: don't push or upload to them.

This is the maintainer procedure for the existing public GitLab project
`johansardar/lamp` (project 88830487) and the `lamp-site` Cloudflare Worker.
The development MR is reviewed and merged by the coordinator **before** any
public upload, source publication, Worker change or account DNS change.
GitHub is unavailable and must not be accessed. Model tokens are the only
permitted expense; use existing owner machines, keys and free services.

## Build from the merge

Fetch development `origin/main`, confirm the coordinator's merge includes T47,
and use that exact clean commit. Keep work on `t3/t47-honest-release`; do not
push development `main`. The release-facing README and installation guide describe the published
0.1.2 behavior; keep them private until publication. Record the actual source
and build provenance in the release evidence. Do not describe earlier
candidate observations as observations of this merged artifact.

Check M5 `df -h` before staging; retain at least 20 GiB free. Use sequential
platform builds and promptly delete their own scratch space. Ubuntu must have
at least 12 GiB available and fewer than two Firecracker processes before any
VM allocation; this is a shared limit. Do not stop everyday services or touch
existing Docker resources. Air has 8 GB and is fanless.

`tools/release/build.py --version 0.1.2 --out NEW_DIR --release-key KEY`
requires existing prepared seeds for `--seed-linux-amd64`, `--seed-linux-arm64`
and `--seed-darwin-arm64`, existing Firecracker/jailer directories via
`--firecracker-amd64` and `--firecracker-arm64`, and `--linux-host` /
`--linux-arm64-host` builders. `--ssh-config` selects the existing Lima host if
needed. Do not fetch Firecracker from GitHub: reuse verified local distribution
inputs. Rebuild guest init/helper/guard from the merged source in disposable
seed copies, recompute their format-specific digests and verify them. Old
helpers have failed real installation probes after protocol changes.

Use `--apple-dir` with the existing file-backed Developer ID and notary keys.
No Keychain writes or dialogs. A public Mac archive needs actual Apple
acceptance, not the builder's ad-hoc-signing fallback. No SDK is packed.
For staged builds, `--reuse PRIOR_DIR --platforms PLATFORM` preserves prior
asset bytes. Keep all platform commits identical; `--reuse` does not enforce
that for you. Do not release a mixed-commit set.

Inspect each archive for its engine, seed, installer and documented binaries;
verify its signed index/manifest with the existing trust key and all entries in
SHA256SUMS. Run the signed candidate's documented install/code/delete flow from
a fresh disposable prefix. Keep credentials in private files and out of
transcripts. Do not change existing installs.

## Publish once, slowly

The old `publish_repo.sh` is a **one-time project-creation script** and refuses
this existing project. Do not run it for 0.1.2. Use `glab` against GitLab only.

1. Inspect project visibility, attached runners and protected branches. Disable
   public-project CI and shared runners; detach/retire any private persistent
   repository runners before publishing new source. No public job may reach an
   owner machine. Do not infer safety just from an absent pipeline file.
2. Export only the reviewed merged source into a disposable public-source
   checkout. Apply `tools/release/redact.py` using the existing private pattern
   file, then run its `--check` and the credential-shape/fixture scan used by
   `publish_repo.sh`. Inspect new evidence too. Never print the pattern file.
   Keep historical failures unchanged apart from privacy redaction. Commit the
   export on the ticket's public `t3/t47-honest-release` branch, not `main`.
   If branch protection must be adjusted, narrow that exception to this branch
   for the source push, then restore protection immediately. Use this reviewed
   public branch as the public default so the README describes the new release;
   do not force-push or rewrite existing public history.
3. Confirm that generic package version `lamp/0.1.2` and tag/release `v0.1.2`
   do not already exist. If either exists, stop and inspect; never blindly
   replace, delete or re-upload an asset. Keep a private upload ledger.
4. Upload SHA256SUMS and exactly its listed files, **each once**, using
   `glab api -X PUT projects/88830487/packages/generic/lamp/0.1.2/FILENAME
   --input LOCAL_FILE`. Wait 20 seconds between writes, sequentially, with no
   automatic retries. If a response is lost, read the registry and verify the
   uploaded digest before deciding what remains. Do not rerun the whole loop.
5. Create the GitLab `v0.1.2` release pointing at the redacted public source
   commit, with `docs/release/0.1.2.md` as its description and asset links under
   `https://lamp.eilmer.si/releases/v0.1.2/`. Preserve the private merged SHA
   and public export SHA mapping in the release evidence.
6. Only after assets and tag are publicly readable, run the read-only plan:
   `tools/release/deploy_worker.sh WORKERS_TOKEN_FILE DNS_TOKEN_FILE`.
   Then use the same command with `--apply` once. It uploads `lamp-site` with
   `LATEST=0.1.2`, serves `/releases/latest.json`, ensures the two Worker routes,
   and replaces only the private account address with proxied Worker DNS.
   CAA records and other DNS names remain untouched. It refuses unrelated
   address/route ownership, and pauses between writes. It does not upload or
   replace any release asset.

If something is wrong after publication, inspect first and fix it in one
deliberate step. Never issue rapid asset replacements or bursts of GitLab writes.

## Observe the public result

Read `https://lamp.eilmer.si/releases/latest.json`, `/install.sh`, the versioned
SHA256SUMS and release.json without credentials. Verify every public asset's
bytes against SHA256SUMS. Confirm the installer points at 0.1.2 and its digest
pins match those public archives. Follow README, release and installer links:
user actions must land on user pages, with engine setup at `docs/install.md`.

Check `dig +short account.eilmer.si @1.1.1.1` (including A and AAAA) and an
independent public resolver: no private address. `curl -sI
https://account.eilmer.si/` must answer; the page says invite-only/unavailable
hosted sign-ups. Do not claim a working account login from that static page.

Install from the **public URL** into new disposable prefixes, use the README's
Python command, compare CLI/daemon/supervisor/installer/MCP/OpenAPI release
versions, inspect connected CLI/MCP discovery and exercise the documented
usable feature commands. Enrollment-only features keep their stated limits.
Delete sandboxes and uninstall only those prefixes; record cleanup failures
honestly. Do not use owner projects, real vault entries or browser profiles.

Measure the owner's create, live-fork, native-speed and zero-leak goals once at
release on each available platform using the public CLI/API. Record platform,
release/build SHA, command boundaries, actual values, failures and unobserved
parts in measurements.md and evidence. Do not impose a trial quota or rerun a
miss to select a green result. The existing standard-library benchmark tools
are optional; they must not use private worker RPCs. Historical 0.1.0/T32 values
stay historical. Observations never block release.

Report the live source/release/installer/latest/asset/account URLs and the actual
fresh-install outcomes to the coordinator, including any missing platform run.
