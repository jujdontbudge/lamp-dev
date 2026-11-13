# T35 follow-up: native macOS DNS and the 8 GB Air

This follows merged T35 (`011618e`). Native macOS DNS required a small guest
helper fix: an isolated DNS dynamic-store key did not create a reachable
resolver. The helper now selects Lamp's assigned MAC and publishes the link's
IPv4 service state alongside its DNS state. No automated tests were added.

The demonstrations use disposable copies and headless, confined VZ workers
with the real managed network helper, through the authenticated guest vsock
channel. They do not exercise the privileged supervisor or public control API.
The owner's apps and existing Lamp installs remained untouched.

## Native macOS on the M5

[`native-macos.json`](native-macos.json) records a fresh disposable APFS clone
of the owner-built macOS 26.6.2 template, with 2 vCPUs and 4096 MiB. Its initial
available host memory was 6617 MiB, with 1654 MiB available headroom and the
unchanged 2048 MiB physical reserve. The coordinator stopped the idle 8 GiB
`lamp-la` Lima VM to free headroom; after the demonstration, it was restarted
with `limactl start lamp-la` and confirmed **Running**.

The old template predates the new helper. Only the disposable guest's helper
was upgraded, by transferring the locally built ad-hoc-signed binary through
its existing vsock exec channel. Only its guest LaunchDaemon was restarted;
no host launch item or Keychain was touched. The final helper SHA-256 is
`13ef9f1db06d1e0a149a13b2f3528881eb3ca599ff057b3be0587907fe6c55c0`.
The original template was never changed or mounted as a guest-written image.

The final fresh-clone run made **no manual guest network changes**. It shows:

- Lamp's MAC on `en1`, address `198.18.240.2/30`, gateway `198.18.240.1`.
- `scutil --dns`: resolver `198.18.240.1`, Reachable and Directly Reachable.
- `dscacheutil -q host -a name example.com`: public IPv4 and IPv6 answers.
- `curl -4 --noproxy '*' -fsSI --max-time 20 https://example.com`: HTTP/2 200.

The combined OS/DNS/HTTPS command exited 0 with no stderr, timeout or truncation.
The worker was asked to exit through its API before process fallback; both
cloned disks were removed, the helper stopped and the disposable host directory
was removed. Shared `lamp-la` was then restored as described above.

Earlier outcomes remain historical:

- [`native-macos-refusal.json`](native-macos-refusal.json): the **M5**, while busy,
  refused 4096 MiB plus the 256 MiB allowance with only 4850 MiB available and
  1212 MiB headroom. This was not an Air result or an unsupported-network claim.
- [`native-macos-failed-network.json`](native-macos-failed-network.json): the
  initial fixture's guest `nohup` restart failed. After a corrected restart of
  the original T35 helper, the interface had an address but native DNS still
  failed; isolated curl exited 6 (`Could not resolve host`).
- [`native-macos-investigation.json`](native-macos-investigation.json): live
  diagnosis, including an abandoned preferences-based setup and manual state
  publication. Its manual repair established the missing IPv4 service state;
  it is **not** the final demonstration. The separate final run above uses
  only the updated helper on a fresh copy.

Existing owner-built native templates need this helper upgraded inside the
guest or a rebuild. Merely updating the host worker does not replace an older
guest binary.

## Air

[`air.json`](air.json) records the M2 Air with 8192 MiB physical memory and
Chrome running before and after the demonstration. The real worker spec
confirms a 2-vCPU, 512-MiB Linux aarch64 guest. Before boot, available memory
was 1168 MiB with 292 MiB host headroom, in addition to the 2048 MiB physical
reserve. The worker's own admission check remained enabled.

Through the guest's authenticated vsock execution channel:

```sh
getent ahostsv4 example.com
curl -4 --noproxy '*' -fsSI --max-time 25 https://example.com
apt-get update -qq
apt-get install -y --no-install-recommends hello
hello
```

DNS returned public addresses, curl returned `HTTP/2 200`, and apt downloaded
and installed `hello` 2.10-5build1 from Ubuntu's public arm64 repository.
`hello` printed `Hello, world!`; both recorded commands exited 0 without a
timeout or truncated output. An initial package-presence check had a shell
quoting error (`Status: parameter not set`); the subsequent apt output directly
confirms `1 newly installed`, so the installation evidence does not depend
on that failed check. The original stderr is retained.

The guest used the sanitized `198.18.240.1` resolver and the same T35 managed
Ethernet helper as the M5 run. No host resolver repair, guest network repair,
password, sudo, desktop automation or owner-app shutdown was used. Cleanup
confirmed zero `lamp-vz` and `lamp-network` processes and removed the entire
Air disposable directory, including the transferred seed. The M5 disposable
directory was also removed. No host account or system service was created.

## Verification and review

`make build` passed (Go build, generated SDK consistency, Rust workspace).
`cargo clippy --locked -p lamp-init --bin lamp-mac-guest-helper -- -D warnings`
and `git diff --check` passed.
Sol review covers the native helper change and this follow-up's evidence; see
[`sol-review.md`](sol-review.md).
