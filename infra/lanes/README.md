# Lamp nested Linux test lanes

Verified on `ubuntu-server` over Tailscale on 2026-11-09. All five guests booted and accepted SSH. Each is defined in system libvirt, powered off, and has an offline internal qcow2 snapshot named `clean`. Lamp is not installed. No lanes are blocked.

Windows lanes were removed by the owner on 2026-11-09.

## Inventory

The outer host is Ubuntu 26.04.1, Intel Core Ultra 9 288V, 8 cores / 30 GiB RAM, kernel `7.0.0-42-generic`, ext4, QEMU 10.2.1, libvirt 12.0.0. Outer CPU flags include `vmx ept vpid`; `kvm_intel.nested=Y`; `/dev/kvm` remains readable and writable. Full host and guest Linux CPU flags are recorded in [inventory.json](inventory.json). These nested fixtures provide compatibility evidence, not native performance certification.

Each guest has 2 vCPU, 4 GiB RAM, and a thin-provisioned 40 GiB qcow2 disk. Disk paths below are on `ubuntu-server`.

| Lane / libvirt domain | Guest OS / exact kernel | Outer → inner CPU flags | Verified nested status | Disk path | Recreate script |
| --- | --- | --- | --- | --- | --- |
| LX22 / `lamp-lx22` | Ubuntu 22.04.5 LTS; `5.15.0-203-generic` | `vmx ept vpid` → `vmx ept vpid` | Enabled: `/dev/kvm`; KVM API 12 + CREATE_VM as `lamp`; SSH passed | `/var/lib/libvirt/images/lamp-lanes/LX22.qcow2` | [lx22.sh](lx22.sh) |
| D12 / `lamp-d12` | Debian GNU/Linux 12 (bookworm); `6.1.0-54-amd64` | `vmx ept vpid` → `vmx ept vpid` | Enabled: `/dev/kvm`; KVM API 12 + CREATE_VM as `lamp`; SSH passed | `/var/lib/libvirt/images/lamp-lanes/D12.qcow2` | [d12.sh](d12.sh) |
| F40 / `lamp-f40` | Fedora Linux 40 (Cloud Edition); `6.8.5-301.fc40.x86_64` | `vmx ept vpid` → `vmx ept vpid` | Enabled: `/dev/kvm`; KVM API 12 + CREATE_VM as `lamp`; SSH passed | `/var/lib/libvirt/images/lamp-lanes/F40.qcow2` | [f40.sh](f40.sh) |
| L510 / `lamp-l510` | Debian GNU/Linux 11 (bullseye); `5.10.0-46-amd64` | `vmx ept vpid` → `vmx ept vpid` | Enabled: `/dev/kvm`; KVM API 12 + CREATE_VM as `lamp`; SSH passed | `/var/lib/libvirt/images/lamp-lanes/L510.qcow2` | [l510.sh](l510.sh) |
| NV / `lamp-nv` | Ubuntu 24.04.5 LTS; `6.8.0-147-generic` | `vmx ept vpid` → no `vmx`/`svm` | Disabled: no `/dev/kvm`; SSH passed | `/var/lib/libvirt/images/lamp-lanes/NV.qcow2` | [nv.sh](nv.sh) |

No evaluation licenses or expiry dates apply to these Linux guests. L510 uses Debian's stock 5.10 kernel; the exact package build observed was `5.10.262-1` (kernel release `5.10.0-46-amd64`). F40 intentionally uses the official Fedora 40 archive image to preserve the required version boundary.

## Start, stop, and SSH

Run these commands on `ubuntu-server` as `johan`. Substitute any domain from the inventory. Boot only one lane at a time to preserve host capacity.

```bash
sudo -n virsh -c qemu:///system start lamp-d12
sudo -n virsh -c qemu:///system domifaddr lamp-d12 --source lease
# Use the IPv4 address reported above:
ssh -i ~/.lamp-lanes/id_ed25519 lamp@192.168.178.X
sudo -n virsh -c qemu:///system shutdown lamp-d12
sudo -n virsh -c qemu:///system domstate lamp-d12
sudo -n virsh -c qemu:///system snapshot-list lamp-d12
```

The dedicated SSH key stays on the host at `~/.lamp-lanes/id_ed25519`. Guest password authentication is disabled; `lamp` has passwordless sudo and, in positive nesting lanes, usable KVM group access. Connect to `ubuntu-server` first. Guest addresses are in the inventory and in `~/.lamp-lanes/evidence/LANE-ip.txt`; addresses can change after recreation. The dedicated NAT network is `lamp-lanes` / `virbr-lamp`, `192.168.178.0/24`, gateway `192.168.178.1`. Guest domains do not autostart.

To restore a fixture while it is powered off:

```bash
sudo -n virsh -c qemu:///system snapshot-revert lamp-d12 clean
```

## Recreate from scratch

The five wrappers share [common.sh](common.sh), [linux.sh](linux.sh), and [verify-kvm.py](verify-kvm.py). Keep these files together. All commands that use sudo run on Ubuntu; no sudo is needed on the Mac.

```bash
# From the Mac, in the repo:
scp infra/lanes/*.sh infra/lanes/verify-kvm.py ubuntu-server:.lamp-lanes/
# On ubuntu-server, choose one wrapper:
bash ~/.lamp-lanes/lx22.sh
bash ~/.lamp-lanes/d12.sh
bash ~/.lamp-lanes/f40.sh
bash ~/.lamp-lanes/l510.sh
bash ~/.lamp-lanes/nv.sh
```

Each wrapper destroys and replaces only its named Lamp fixture and disk, downloads missing official cloud media, creates a cloud-init seed, boots and verifies the guest, shuts it down, and creates `clean`. Repeating a wrapper replaces that fixture and its old snapshots. A host-side lock prevents concurrent provisioners, and provisioning refuses to proceed with another Lamp lane running. Failed fixtures are powered off and their status/evidence is retained. Existing Docker workloads are never used for provisioning.

The scripts require the host's installed `qemu-system-x86 qemu-utils libvirt-daemon-system libvirt-clients virtinst cloud-image-utils curl` packages, plus standard Ubuntu Python 3, SSH, and util-linux tools. Media comes from official Ubuntu and Debian cloud-image servers and Fedora's official archive. Downloaded media SHA-256 hashes and source URLs are saved beside files in `~/.lamp-lanes/media/`; a changed cached checksum fails the preflight. To refresh a cached image, move its `LANE-base.qcow2`, `.sha256`, and `.source` files out of the cache before recreating.

Credentials, cloud-init seed images, disks, and raw provisioning evidence stay on the host. Nothing secret belongs in this repository. Evidence under `~/.lamp-lanes/evidence/` includes each guest's OS/kernel/CPU/filesystem, regular-user KVM probe (positive lanes), domain XML, status, and IP; `final-host-checks.json` records final state, snapshots, cleanup, Docker comparison, and space usage.

## Host checks and disk space

Docker is active. `docker ps --no-trunc` formatted with container ID, image, name, and status is byte-identical before and after: zero running containers at both points. The raw comparison files are `docker-before.txt` and `docker-after.txt` under the host evidence directory. No existing Docker containers, networks, or services were configured, stopped, or removed. No host reboot or kernel command-line changes were performed.

Final allocated space is **8.92 GiB**, including **6.53 GiB** under `/var/lib/libvirt/images/lamp-lanes/` and **2.39 GiB** under `~/.lamp-lanes/` (official cloud-image cache, keys, seeds, and evidence). The five virtual disk capacities total 200 GiB and remain thin provisioned. The host has approximately 581 GiB available. Exact allocated byte counts and per-disk allocations are in [inventory.json](inventory.json).

## LA (M5)

Linux arm64 VM `lamp-la` on the M5 Mac. It runs Lima 2.2.0 on Apple's Virtualization framework with nested virtualization, and has 4 vCPU, 8 GiB of RAM and a 60 GiB disk. The guest is Ubuntu 26.04.1 arm64 with kernel 7.0.0-38-generic. Nested KVM was verified on 2026-11-09: a guest booted with `-accel kvm` and shut down cleanly. The VM is stopped when not in use. Setup, start, stop, SSH and recreate commands are in [la/README.md](la/README.md).
