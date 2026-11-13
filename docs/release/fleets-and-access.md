# Hosts, fleets and remote access

The Linux amd64, Linux arm64 and macOS arm64 distributions include `lamp-fleet`,
`lamp-access`, `lamp-access-cloudflare` and `lamp-relay-client`. The AGPL relay
server ships separately as `lamp-relay-VERSION-OS-ARCH.tar.gz` for those same
platforms. Keep its licence and offer corresponding source when operating it.

## Enroll a fleet

On a trusted owner machine, describe **all initial hosts** in `hosts.json`:

```json
[
  {"id":"hs_one","api":"https://10.0.0.1:7444","raft":"10.0.0.1:7446","os":"linux","arch":"x86_64","engines":["firecracker-1.17.0"],"capacity":{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}},
  {"id":"hs_two","api":"https://10.0.0.2:7444","raft":"10.0.0.2:7446","os":"darwin","arch":"arm64","engines":[],"capacity":{"vcpus":0,"memory_mib":0,"disk_gib":0,"pids_max":0}}
]
```

Use addresses reachable directly between hosts (LAN, VPN or explicit forwards).
`api` is the mutually authenticated peer listener; the public bearer API uses
its port **plus one** (7445 above). Raft needs its own port. Engines and capacity
must describe the actual enrolled supervisor; use empty engines and zero capacity
for a control-only host. Capacity is a fixed host slice, never borrowed from an
unavailable host.

```sh
lamp-fleet init --hosts hosts.json --out /absolute/private/new-fleet
```

This creates host certificates, a shared owner enrollment, a discovery file and
private bearer. Transfer **only** `new-fleet/hs_one` to the first machine and
`new-fleet/hs_two` to the second, over an authenticated channel. Never distribute
`ca.key` or `owner.token` with host bundles. Keep the CA offline; generated host
certificates expire after one year and must be replaced before then. Each host
joins and starts with one command:

```sh
lamp-fleet join --bundle /absolute/private/hs_two --root /absolute/private/lamp-fleet
```

Run this under your process supervisor; repeat the same command/root on restart.
For compute hosts append `--` and the existing enrolled supervisor flags:
`--worker-socket`, `--worker-id`, `--worker-boot-id`, `--worker-uid`, `--worker-pid`,
`--grant-id` and optionally `--worker-timeout 1m`. The first host ID in sorted
order bootstraps consensus; start every enrolled host. Host-to-host TLS requires
both certificates and the enrolled immutable host identity.

For an installer-managed host, prepare the configuration first:

```sh
lamp-fleet join --bundle /absolute/private/hs_two --root /absolute/private/lamp-fleet --prepare-only
lamp-installer install --prefix /absolute/private/lamp --release /absolute/signed-release --trust /absolute/release-trust.pub --service process --wait=false \
  --identity-file /absolute/private/lamp-fleet/identity.json --token-file /absolute/private/owner.token \
  --lampd-arg=--fleet-config --lampd-arg=/absolute/private/lamp-fleet/fleet.json
```

Add the compute worker flags using repeatable `--lampd-arg` options. The service
and separate updater agent retain these options across restarts and upgrades.
Supply the original fleet owner token file securely at install time; the installer
keeps a private copy for updater health checks. `--wait=false` allows the first
voter to start before the other voter joins.

On your client, retain the `init` directory's `ca.crt`, `discovery.json` and
`owner.token` privately. Its discovery document references the original absolute
CA path; update that path if moving it. Configure CLI or MCP:

```sh
export LAMP_API_URL=lamp+fleet:///absolute/private/new-fleet/discovery.json
lamp host list --token-file /absolute/private/new-fleet/owner.token --json
lamp engine list --token-file /absolute/private/new-fleet/owner.token --json
lamp sandbox create --host-id hs_one --template-id tp_EXAMPLE --token-file /absolute/private/new-fleet/owner.token --json
```

Omit `--host-id` for automatic placement on a compatible available host with
capacity. Pins are strict. `host list` includes engines and installed release
versions; `engine list` includes host IDs. `fleet get fl_local` includes
`unavailable_host_ids` and `stranded_sandbox_ids`. An unavailable host's sandboxes
retain their custody and are never silently recreated on another machine.
New unpinned work may use a remaining compatible host while its authority permit
is valid. Two voters need both online for new quorum mutations; three tolerate
one unavailable voter. Observations survive ingress restarts.

This command provisions a **new fleet**. Existing standalone state cannot be
converted in place. Online invitations/removal are not implemented: the initial
host manifest stays immutable. Do not edit a live fleet's manifest or restore its
independent recovery guard backwards.

## Rolling updates

Install each host with `lamp-installer` and its updater agent as above. Import
the same newer **signed** release directory into each installation's `mirror/`
under a version directory, using the pinned release trust key. For example,
`mirror/0.1.2/index.json`, `index.sig`, and the platform directories named by the
index. No unsigned version label can authorize an update.

```sh
lamp update create --version 0.1.2 --token-file /absolute/private/new-fleet/owner.token --json
lamp update list --token-file /absolute/private/new-fleet/owner.token --json
lamp update get up_EXAMPLE --token-file /absolute/private/new-fleet/owner.token --json
```

Alternatively supply `--release-digest sha256:...`; use exactly one selector.
The update's `current_host_id` advances one host at a time. Labels
`lamp.host.HOST_ID` record completed/rolled_back/failed results and versions.
A failed host rolls back and stops the rollout. Host observations report the
installed version after restart. Automatic updates remain off until explicitly
enabled through `fleet update --update-policy-json`.

## Remote access choices

All choices use `lamp-access` against the existing private daemon socket. Create
two distinct private SSH CAs with `lamp-access init-ca --out /absolute/host-ca`
and `lamp-access init-ca --out /absolute/user-ca`. Use the complete base config
and SSH trust setup in [owner-run access](../development/access.md). Its users map
to private token files; plaintext HTTP may bind only numeric loopback.

* **Tailscale:** add a `tailscale` block to that config, e.g.
  `{"hostname":"lamp-fleet","auth_url_file":"/absolute/private/signin-url","ssh_port":2222,"http_port":8089}`.
  Run `lamp-access serve --config /absolute/private/access.json`. Enroll its
  userspace node into your own tailnet via the private URL file, or configure a
  private `auth_key_file`. A second enrolled machine can use
  `lamp-access tailnet-api --state-dir /absolute/private/client-state --hostname lamp-client --auth-url-file /absolute/private/client-signin --target lamp-fleet:8089 --socket /absolute/private/client/api.sock`.
  In another terminal use `LAMP_SOCKET=/absolute/private/client/api.sock lamp sandbox list --token-file /absolute/private/owner.token` (unset `LAMP_API_URL`).
  WireGuard protects that HTTP hop. No system Tailscale service is changed.
* **Own domain:** add `domain` with `name`, `email`, and `listen`, e.g.
  `{"name":"lamp.example.org","email":"owner@example.org","listen":":443"}`.
  Run the same `lamp-access serve` command. Public DNS and incoming TCP 443 must
  already reach this host. ACME TLS-ALPN-01 issues/renews only that hostname.
  Use `LAMP_API_URL=https://lamp.example.org lamp sandbox list --token-file ...`.
* **Cloudflare:** the release includes `lamp-access-cloudflare`, the plugin
  wrapper. Supply your own installed `cloudflared`, a disposable tunnel and
  private credential file. Generate its manifest with
  `lamp-access cloudflare-manifest --plugin /absolute/bin/lamp-access-cloudflare --out /absolute/private/cloudflare-manifest.json`.
  Fill in the `cloudflare` block, pinned executable/manifest digests and launcher
  from [the Cloudflare configuration](../development/access.md#cloudflare-tunnel-access-plugin).
  Start `lamp-access serve --config /absolute/private/access.json`. Linux uses
  the compiled `lamp-plugin-launcher`; macOS wraps `/usr/bin/sandbox-exec` with
  that same launcher and requires explicit `diagnostic_soft_limits: true`. Engine
  releases include the launcher; a Linux control-only build includes its source:
  compile it with `cc -O2 -Wall -Wextra -Werror -o lamp-plugin-launcher tools/plugin-launcher.c`.
* **Self-hosted relay:** extract the separate relay archive and run
  `lamp-relay --config /absolute/private/relay.json`. Provision enrolled fleets,
  TLS and account trust following [the relay configuration](../development/hosted.md#relay-configuration-and-native-access).
  Production uses the existing Eilmer account issuer; this is not an anonymous
  public proxy. The relay holds routing metadata and forwards ciphertext.

For the relay host, obtain a short-lived account token into a private file, then:

```sh
lamp-relay-client issue-enrollment --relay https://relay.eilmer.si --fleet f123 --account-token-file /absolute/private/account.token --out /absolute/private/enrollment
lamp-relay-client enroll --relay https://relay.eilmer.si --fleet f123 --enrollment-file /absolute/private/enrollment --credential-file /absolute/private/host-credential.json
```

Start `lamp-access` with a loopback SSH listener, e.g. `127.0.0.1:2222`. Create a
0600 relay host config:

```json
{"origin":"https://relay.eilmer.si","credential_file":"/absolute/private/host-credential.json","ssh_target":"127.0.0.1:2222"}
```

Add `--relay-config /absolute/private/relay-host.json` to `lampd` (or its installer
arguments). It reconnects the outbound carrier and reports actual connection
state through `lamp relay list` / `lamp relay get rl_local`, MCP `relay_list` /
`relay_get`, and HTTP `GET /v1/relays` / `GET /v1/relays/rl_local`. No credential
paths, reconnect secrets or fleet keys appear in these responses. In a fleet,
status describes the ingress host you queried; query a specific ingress to
inspect its carrier.

On another machine, use stock SSH with independently pinned fleet host CA and
user key, and ProxyCommand:

```text
ProxyCommand /absolute/bin/lamp-relay-client connect --relay https://relay.eilmer.si --fleet f123 --account-token-file /absolute/private/account.token
```

`ssh owner@lamp-fleet sessions` reaches Lamp. For the HTTP CLI/MCP API, configure `https_target` to the loopback TLS fleet
ingress and its allocated SNI route as described in the relay guide, then use
`LAMP_API_URL=https://api.f123.fleet.eilmer.si`. Fleet TLS terminates on your host.
