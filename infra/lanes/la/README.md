# LA: Linux arm64 nested-KVM lane

**Status: ready, verified, and stopped** (2026-11-09). `lamp-la` remains defined in Lima. It reserves 4 vCPU, 8 GiB RAM and a 60 GiB sparse disk when started. Its stopped state releases VM RAM. This is the nested functional/security host lane from `docs/spec/architecture.md` §20.1; native arm64 speed certification and Lamp's later compatibility/security tests remain separate work. Lamp has not been built or installed.

## Versions and configuration

| Component | Verified version/configuration |
| --- | --- |
| Mac | Apple M5 MacBook Pro, 10 cores, 24 GiB |
| Host OS | macOS 26.5.2, build `25F84`; Darwin `25.5.0` |
| Lima | `2.2.0`, already installed through Homebrew |
| Hypervisor | Apple Virtualization.framework, `vmType: vz`, `nestedVirtualization: true` |
| Guest | Ubuntu **26.04.1 LTS**, arm64 cloud image |
| Guest / inner test kernel | `7.0.0-38-generic`, Ubuntu package `7.0.0-38.38`, underlying `7.0.14` |
| Nested QEMU | `10.2.1`, Ubuntu `1:10.2.1+ds-1ubuntu3.3` |
| Inner test allocation | 1 vCPU, 512 MiB RAM, no disk or network; BusyBox initramfs |

The image URL and SHA-256 are pinned in `lamp-la.yaml`. The vendor SHA256SUMS captured at setup is in `evidence/ubuntu-SHA256SUMS`. The matching dated image is also available at `https://cloud-images.ubuntu.com/releases/26.04/release-20261016/ubuntu-26.04-server-cloudimg-arm64.img`. Ubuntu may eventually remove old images. If the rolling release URL changes, Lima's checksum check deliberately fails rather than silently booting different media; use the matching dated URL or deliberately update the URL/digest and requalify. Ubuntu package repositories remain current, so later clean recreations may install newer QEMU packages; every verification run records installed versions.

## Commands

Run from the repository root:

```sh
# Start the existing definition; no interactive editor.
limactl start --tty=false lamp-la

# SSH shell (the workdir avoids depending on a mounted host path).
limactl shell --tty=false --workdir=/home/owner.guest lamp-la
# Or use the generated SSH configuration directly:
ssh -F "$HOME/.lima/lamp-la/ssh.config" lima-lamp-la

# Stop and free VM RAM, retaining its disk and definition.
limactl stop --tty=false lamp-la

# Idempotent: create if absent, start, verify, then stop.
./infra/lanes/la/recreate.sh

# Clean recreation: explicitly deletes ONLY lamp-la and its guest disk,
# then creates, verifies and stops it. Any data in that guest is lost.
./infra/lanes/la/recreate.sh --reset

# Rerun just the probe against an already running lane.
limactl shell --tty=false --workdir=/tmp lamp-la bash -s \
  < infra/lanes/la/verify-guest.sh

# Inspect the resulting stopped definition.
limactl list lamp-la
```

The scripts require Bash and Lima 2.2.0 or newer. Missing Lima is reported; the script does not attempt a privileged install. VM state and downloaded images live in Lima's normal user directories (`~/.lima` and `~/Library/Caches/lima`), outside the repository. All repository outputs stay in this directory. `recreate.sh` retains an existing definition by default; use `--reset` to apply changed provisioning/configuration. Each run overwrites `evidence/latest-run.log`; the captured acceptance runs are retained separately.

## Real nested KVM evidence

`/dev/kvm` is a character device owned by `root:kvm`, mode `0660`, accessible to the guest login user through its `kvm` group. The test runs as that unprivileged user:

```sh
qemu-system-aarch64 \
  -machine virt -accel kvm -cpu host -smp 1 -m 512M \
  -kernel /opt/lamp-la/Image -initrd /opt/lamp-la/initramfs.cpio.gz \
  -append 'console=ttyAMA0 rdinit=/init panic=-1' \
  -nographic -no-reboot -nic none
```

The inner serial console reports:

```text
smccc: KVM: hypervisor services detected
Run /init as init process
LAMP_LA_NESTED_KVM_BOOT_OK
Linux (none) 7.0.0-38-generic ... aarch64 GNU/Linux
reboot: Power down
PASS: nested Linux booted with mandatory -accel kvm and powered off (QEMU exit 0).
```

The runner requires both that marker and QEMU exit 0, with a 90-second timeout and forced termination after another 5 seconds. There is no TCG fallback. The nested guest uses the outer guest's actual Ubuntu kernel extracted from its UKI/EFI zboot wrapper and a small static BusyBox initramfs. No second cloud image or credentials are needed. Extracted assets stay in `/opt/lamp-la/` for subsequent tests. Initial SSH connections predate the first-boot group change, so the runner closes that lane's SSH ControlMaster before verification to refresh group membership.

Evidence:

- `evidence/clean-recreate.log`: successful creation from a clean guest disk, nested boot and shutdown.
- `evidence/latest-run.log`: latest verification, including explicit QEMU invocation, versions, guest resources, asset hashes and final stopped status.
- `evidence/final-status.json`: final Lima definition/status snapshot.
- `evidence/initial-attempt.log`: initial failed direct-UKI test, retained as progress history. The EFI wrapper extraction, SSH group refresh and QEMU stdin isolation are fixed in the delivered scripts.

## Boundaries and blockers

No blockers remain for lane setup. No spending, password entry, approval dialogs, host privilege escalation, keychain changes, system launch items, commits or changes elsewhere in the repository were used. Guest package installation and group setup run as cloud-init system provisioning; the lane scripts invoke no `sudo`. Host directories are not mounted in the guest; containerd is disabled. This VM is a development test host, not a completed Lamp security boundary or a native performance result.
