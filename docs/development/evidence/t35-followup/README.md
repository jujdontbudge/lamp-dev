# T35 follow-up: native macOS admission and the 8 GB Air

This follows merged T35 (`011618e`). No product behavior is changed and no new
test is added. The owner's everyday apps and existing Lamp installs remain
untouched. Demonstrations use disposable copies and headless, confined VZ
workers with the real managed network helper; they do not run the privileged
supervisor or public control API.

## Native macOS on the M5

[`native-macos.json`](native-macos.json) records an actual `lamp-vz worker`
attempt using a disposable APFS clone of the existing owner-built macOS
26.6.2 template. Its published minimum is 2 vCPUs, 4096 MiB and 48 GiB. The
managed network helper became ready, then the VMM refused before boot:

```text
NO_CAPACITY: requested 4096 MiB plus 256 MiB VMM allowance;
physical=24576 MiB, available=4850 MiB, host reserve=2048 MiB,
available headroom=1212 MiB, reservations=0 MiB, pending=0 MiB;
retry when memory is available or use a smaller guest or another host
```

Thus native macOS DNS/HTTPS is **not verified**. This is a current host-memory
refusal, not a claim that native macOS networking is unsupported. The current guest helper includes native interface, gateway and resolver
setup; that code was not reached in this attempt. The existing template predates that helper; the planned
helper update would have affected only the disposable guest copy. It did not
run because admission refused the VM. No memory check was relaxed or owner
app stopped. Both cloned disk files were removed, the worker exited before
VM creation and the helper was stopped.

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
directory was also removed. No account or system service was created.

## Verification and review

`make build` passed (Go build, generated SDK consistency, Rust workspace).
Sol review is limited to this follow-up's evidence and documentation; see
[`sol-review.md`](sol-review.md).
