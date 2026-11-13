# Linux inside a Lamp sandbox

New seeds include curl, wget, CA certificates, git, Python 3 with pip and venv,
Node.js with npm, OpenSSH client/server, apt, gcc and libc development headers.
`python3` and `pip` use `/opt/venv` by default so pip can install packages without
changing the distribution's managed Python. SSH is installed; this does not
start a public SSH service or enroll credentials.

Task root owns the writable disk view. Install packages with `apt-get update &&
apt-get install -y PACKAGE`, or write under `/etc`, `/root`, `/usr/local`, `/opt`
and `/home`. These changes belong to the sandbox disk and follow its forks.
`/tmp` and `/var/tmp` use that disk rather than a fixed RAM filesystem.
The protected helper and collector remain outside task authority under
`/run/lamp`; the boot helper binaries cannot be replaced from a task.

The seed resolver is `198.18.240.1`, Lamp's private guest gateway. Images do not
copy the builder's resolver, search domains, machine identity or SSH host keys.
Managed Firecracker and Virtualization.framework guests use the same guarded
Ethernet resolver and connection policy. DNS follows the selected exit; policy
answers never grant unchecked dial authority. The legacy Linux subscription
adapter retains its IPv4 UDP A resolver (empty AAAA replies). DNS over HTTPS is
ordinary HTTPS under the transport policy.

Mac VMM workers receive a private Ethernet descriptor. A separate helper under
the assigned worker account mediates TCP/UDP and DNS, excludes host/LAN/tailnet
and metadata destinations, and writes its audit under the store's private
`network/<sandbox>-g<generation>-*` directory, outside VMM filesystem grants. The VMM retains its no-network confinement. Install
`lamp-network` beside `lamp-vz` and rebuild old templates for the guarded NIC
device graph. Template builds retain default-deny egress. The Mac supervisor
accepts the same generation-bound managed calls as Linux: preview ports, shares,
live allow/deny policy, selected exits and explicit service links. A create-time
policy is installed before boot; later edits use the same helper without
restarting the guest. Closing the generation tears down its previews and flows.

`lamp engine list --json` reports devices accepted by the enrolled worker.
Firecracker accepts extra disks, with no display, audio, keyboard, mouse or GPU.
Mac optional devices are configured in templates; per-create device requests
are refused cleanly before acceptance. A device refusal does not consume a
sandbox allocation or break the worker channel.

Guest memory pressure should kill the guest process while keeping the VM
available. The Linux host cgroup reclaims cache before its hard VMM allowance; disks
use Firecracker’s asynchronous backend so disk I/O does not stall the VMM event
loop. Rebuild older Firecracker templates for this device graph as well. If the VMM dies, the
control daemon observes the failure during its lifecycle tick, marks the
sandbox failed and emits a state event with the engine's reason; cgroup OOM
kills are reported as `oom_killed`.

Existing seeds/templates are immutable. Rebuild and import them to receive the
new tools and sanitized resolver.
