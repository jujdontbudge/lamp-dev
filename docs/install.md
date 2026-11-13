# Install and run code

The next release installer includes the engine and a signed Linux seed. The
published 0.1.1 installer predates this path and installs control binaries only;
these instructions require a release built with the new installer.

On Ubuntu/Linux amd64 or arm64 with KVM, or Apple Silicon macOS 26+:

```sh
curl -fsSL https://lamp.eilmer.si/install.sh | sh
"$HOME/.local/lamp/current/bin/lamp" sandbox run -- python3 -c 'print("hello from Lamp")'
```

`run` creates a VM, waits for readiness, runs the command with separate stdout
and stderr and its real exit code, then deletes the sandbox. It uses the
installed default template: 2 vCPUs, up to 2048 MiB RAM, 20 GiB disk, 4096 PIDs.
On Macs the installer selects 2048, 1024, 512 or 256 MiB from current available
memory, leaving a quarter of those pages for host activity after accounting
for the 256 MiB VMM allowance. It retains the engine's physical host reserve
and capacity checks. `lamp template list` shows the selected default resources.
If even the smallest guest cannot fit, installation refuses with observed
memory, the required headroom and a next step before creating engine files.
The default template is private. Persistent sandboxes suspend after 300 seconds
idle and retain suspended state for 7 days. Previews are private by default;
network/package setup is documented separately when available on the host.

For a sandbox you keep, add the printed PATH line to your shell profile and
current shell (`export PATH="$HOME/.local/lamp/current/bin:$PATH"`), then:

```sh
lamp template list
lamp sandbox create
lamp sandbox exec <id-from-create> -- uname -a
lamp sandbox get <id-from-create>
```

Delete using `lamp sandbox delete ID --expected-revision N`, where N is the
revision from `sandbox get`. `sandbox run` does this cleanup automatically.

## Host requirements

Linux requires `/dev/kvm`, administrator access available through `sudo -n`
(or running as root), and host dependencies. On Ubuntu/Debian the installer installs missing
`xfsprogs nftables util-linux passt iproute2 python3` packages automatically
through apt. On other distributions it names the packages to install. It never opens a password prompt, downloads Firecracker from a
third-party service during setup, or substitutes host execution for a VM.
Hosts without KVM are refused with an explanation. Automatic gVisor enrollment
is not available in this installer.

macOS requires Apple Silicon, macOS 26+, APFS clone support, and noninteractive
administrator access for isolated worker accounts and root-owned engine files.
Log into macOS before installation. The installer runs its engine commands
in that login session so Virtualization.framework can seal saved state,
including when installation is initiated over SSH. This does not install a
system launch item. A locked session can cold-boot Linux templates; memory
fork and resume still require the session to be unlocked.
It uses the release's signed/notarized engine without building from source or
accessing the Keychain. The 8 GB Air uses a smaller default when its everyday
apps leave less memory available, and no RAM-backed capture disk. The reserve
limits total physical VM allocations; it is not added to the available-page
requirement. Mac networking still needs separate release validation.

The default store on Linux is an **80 GiB sparse XFS image**, with reflinks.
It occupies physical space as data is written. Allow at least 20 GiB of logical
store space per live sandbox, plus the seed, templates and saved state. Set
`LAMP_STORE_GIB` for a larger store; values below 80 are refused. Keep enough
physical disk space for your actual writes. macOS uses a separate APFS directory.

## Locations, credentials and a second install

The installer prints the exact binary directory and PATH line. It does not
edit your shell profile. Clients resolve their own installation, even when
called through a symlink, so no socket or token flags are needed.

- Binaries: `<prefix>/current/bin`
- Owner token: `<prefix>/config/owner.token` (private; never paste it into chat)
- Local API socket: `<prefix>/run/api.sock`
- Linux engine: `/var/lib/lamp-i/<prefix-hash>` and `/run/lamp-<prefix-hash>`
- Mac engine: `/private/var/lamp-i/<prefix-hash>` and
  `/private/var/run/lamp-<prefix-hash>`

`lamp-mcp` finds the same socket and credential. `LAMP_TOKEN_FILE` works in
both clients; `--token-file` also works before or after CLI command words.
`LAMP_SOCKET`/`--socket` and `LAMP_API_URL`/`--api-url` select another endpoint.
Selecting a remote endpoint never automatically sends the local owner token;
provide an explicit token or token file for that endpoint.

To install beside another engine, use a short, new prefix:

```sh
curl -fsSL https://lamp.eilmer.si/install.sh | LAMP_PREFIX="$HOME/.local/lamp-alt" sh
"$HOME/.local/lamp-alt/current/bin/lamp" sandbox run -- uname -a
```

Services, worker identities, engine binaries, network policy, capture paths and
stores are specific to that prefix. Setup refuses an existing engine directory
rather than replacing it. Prefixes exceeding the Unix socket path limit are
refused before files are installed. Use `LAMP_SERVICE=process` when you do not
want service-manager enrollment. Engine grants are renewed hourly by the
installer agent; leave it running. `lamp-installer status --prefix PREFIX`
reports the supervisor and API health. Logs are in `<prefix>/log`.

## Building a release

Release maintainers supply prepared seeds to `tools/release/build.py` via
`--seed-linux-amd64`, `--seed-linux-arm64` and `--seed-darwin-arm64`, alongside
the Linux engine builders and `--firecracker-amd64`/`--firecracker-arm64`
directories containing `firecracker` and `jailer`. Linux seeds use the
Firecracker `sha256-c4m` format and `vmlinux`; Mac seeds use the VZ seed format
and `Image`. They are distinct artifacts. The release signs every seed file,
including its compressed disk. Installing verifies the release signature and
the engine verifies the expanded seed before probing the default template.
A missing engine or seed prevents building an installable sandbox release.
