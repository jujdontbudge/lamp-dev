# Owner-run fleet access

`lamp-access` exposes an existing credentialed `lampd` Unix socket. It does not
start the engine, change DNS, change a system tailnet, or require a Lamp account.
Every API request keeps its original bearer and the control core still checks
scope. Each SSH username maps to an owner-provisioned control credential file;
the credential is read on each new console channel and never returned to clients.

Build and provision two distinct fleet CAs:

```sh
go build -o bin/lamp-access ./cmd/lamp-access
bin/lamp-access init-ca --out /absolute/private/fleet-host-ca
bin/lamp-access init-ca --out /absolute/private/fleet-user-ca
```

Keep both private CA files and access configuration mode `0600`, and the state
directory mode `0700`. Distribute only the host CA **public** key to clients.
Fleet access nodes must be explicitly provisioned with the same fleet signing
keys; there is no automatic CA synchronization or hosted key custodian. Serving
never silently replaces a fleet CA. Host leaf keys are private to each node;
host certificates last 24 hours and are renewed every 12 hours while serving.

This is a complete local configuration. Fill in the public key line and private
credential file from the existing control enrollment; no token belongs in JSON:

```json
{
  "state_dir": "/absolute/private/access",
  "api_socket": "/absolute/private/lamp/api.sock",
  "ssh_listen": "127.0.0.1:2222",
  "http_listen": "127.0.0.1:8089",
  "host_ca_file": "/absolute/private/fleet-host-ca",
  "user_ca_file": "/absolute/private/fleet-user-ca",
  "hostnames": ["lamp-local.eilmer.si"],
  "users": [{
    "name": "owner",
    "authorized_keys": ["ssh-ed25519 REPLACE_WITH_PUBLIC_KEY"],
    "token_file": "/absolute/private/owner-token"
  }]
}
```

Run `bin/lamp-access serve --config /absolute/private/access.json`. Omitting
`tailscale`, `domain`, and `cloudflare` makes this fully local and offline.
SSH can also bind an explicitly configured numeric LAN address. Plain HTTP
origins bind only numeric loopback addresses; remote APIs use HTTPS or WireGuard.
Only public contract API paths and `/ssh` are exposed. Browser-origin WebSocket
requests are refused. Headers claiming proxy or Lamp identity confer no authority.

Trust the fleet host CA in a dedicated OpenSSH known-hosts file:

```text
@cert-authority lamp-local.eilmer.si ssh-ed25519 REPLACE_WITH_HOST_CA_PUBLIC_KEY
```

Use `HostKeyAlias` when the dialed address differs from the certificate name:

```sh
ssh -p 2222 -o HostKeyAlias=lamp-local.eilmer.si owner@127.0.0.1 sessions
ssh -p 2222 -o HostKeyAlias=lamp-local.eilmer.si owner@127.0.0.1 attach ss_example
printf 'uname -a\nexit\n' | ssh -p 2222 \
  -o HostKeyAlias=lamp-local.eilmer.si owner@127.0.0.1 shell sb_example
```

The console admits exactly those commands. Empty SSH commands select `sessions`.
Host commands, subsystems, forwarding, and agent forwarding have no handler.
`shell` currently executes each input line in a fresh **guest** `/bin/sh` using
public exec operations, preserves the last command's exit status, and ends at
EOF or `exit`. It is a command console, not a persistent terminal: `cd`, exported
variables, multiline syntax and interactive programs cannot span input lines.
T21's session backend is required for real persistent terminal attachment.

`sessions` calls `session_list`. `attach` obtains a bounded input lease, calls
`session_attach`, streams public frames, and sends sequenced, location-epoch-bound
input and PTY sizes with current revisions. It releases the input lease on exit
and disconnects before the lease expires. Session movement requires reconnecting.
An absent session backend fails explicitly; the access edge fabricates no session.

## Embedded Tailscale

Add this object to the same configuration:

```json
{
  "tailscale": {
    "hostname": "lamp-fleet",
    "auth_url_file": "/absolute/private/tailscale-signin-url",
    "ssh_port": 2222,
    "http_port": 8089
  }
}
```

The node has its own userspace WireGuard stack and state beneath `state_dir`.
It needs no system daemon, TUN device, router port mapping, or OS DNS changes.
Sign-in URLs go only to the named private file. An existing deployment can instead
set `auth_key_file`. Set `control_url` for owner-run Headscale. Node enrollment
and the owner's tailnet policy remain under the owner's control.

System Tailscale clients can run ordinary SSH against the node address. Clients
without it can use the embedded client as an OpenSSH `ProxyCommand`:

```text
ProxyCommand /absolute/bin/lamp-access tailnet-connect --state-dir /absolute/private/client --hostname lamp-client --auth-url-file /absolute/private/client-signin --target %h:%p
```

For certificate admission, a user may additionally set exactly one of
`tailnet_login` or `tailnet_tag`. The Tailscale-only HTTP endpoint
`POST /ssh/certificate` takes `{"user":"owner","public_key":"ssh-ed25519 ..."}`.
The service derives identity from the embedded stack's `WhoIs` for the actual
TCP peer. Tagged nodes cannot inherit a human owner's login privileges. No
forwarded header or requested SSH username can supply a tailnet identity.
The endpoint signs the caller's key for five minutes for the mapped username,
with PTY permission and no forwarding extensions. This endpoint is absent from
the local, domain and Cloudflare HTTP listeners.

## CI without stored client secrets

An owner first configures a **separate, intended** Tailscale federated identity
for GitHub's issuer, repository/ref subject, audience, and allowed CI tag. The
fleet user maps that tag to a scoped control principal. This command does not
create or change the owner's federation or ACL.

Give the GitHub job `permissions: {id-token: write}` and run:

```sh
lamp-access ci --client-id CLIENT_ID --audience AUDIENCE --tags tag:lamp-ci \
  --target lamp-fleet:2222 --certificate-address lamp-fleet:8089 \
  --user ci --host-ca /path/to/fleet-host-ca.pub sessions
```

The command obtains the job OIDC token, uses Tailscale's identity federation
implementation to join an ephemeral embedded node, generates an in-memory SSH
key, requests the short-lived user certificate, verifies the fleet host CA and
runs the restricted SSH command. Node state and SSH keys stay in memory. The
job stores no reusable Lamp bearer, tailnet auth key, OAuth secret or SSH key.
`TS_AUTHKEY` and `TS_CLIENT_SECRET` must be unset so upstream environment fallbacks
cannot replace federation with a stored secret. Include the target's Tailscale
hostname in the fleet host certificate's `hostnames`, as well as any domain name.
The server's scoped principal credential remains on the owner-controlled host.
Tailscale federation is distinct from Headscale enrollment; the Headscale demo
does not prove GitHub OIDC federation.

## Owner-domain HTTPS

Add `domain` with `name`, `email`, and `listen`, for example
`{"name":"lamp-direct.eilmer.si","email":"owner@eilmer.si","listen":":443"}`.
Only that configured name is managed. CertMagic obtains and renews its certificate
using ACME TLS-ALPN-01 and persists it beneath the private state directory.
DNS must already resolve to this host and public TCP 443 must reach it; this
route cannot bypass NAT. No DNS records or CAA records are changed by the service.
`acme_ca` can select an owner's ACME server or Let's Encrypt staging.

Use the same API over HTTPS. Ordinary OpenSSH uses the SSH WebSocket transport:

```text
Host lamp-domain
  HostName lamp-direct.eilmer.si
  User owner
  HostKeyAlias lamp-direct.eilmer.si
  ProxyCommand /absolute/bin/lamp-access connect --url https://lamp-direct.eilmer.si/ssh
```

Include that name in the host certificate's `hostnames` and client CA trust.
The connector verifies normal HTTPS certificates and refuses redirects.

## Cloudflare Tunnel access plugin

Provision a named tunnel and one unused hostname beneath `eilmer.si` in the
owner's Cloudflare account, checking existing DNS and CAA before changes. The
hostname's tunnel ingress must point to Lamp's loopback HTTP origin. Cloudflare
provides automatic public HTTPS; its WebSocket support carries ordinary SSH
through the same `lamp-access connect` ProxyCommand. Lamp authentication remains
mandatory inside the tunnel. No Cloudflare service token substitutes for it.

Build the confined planner and create an exact-byte manifest:

```sh
go build -o bin/lamp-access-cloudflare ./cmd/lamp-access-cloudflare
lamp-access cloudflare-manifest --plugin /absolute/bin/lamp-access-cloudflare \
  --out /absolute/private/cloudflare-manifest.json
```

Add a `cloudflare` object with `executable` (owner-installed `cloudflared`), its
`executable_sha256`, `tunnel_id`, private `credentials_file`, `hostname`, `plugin`,
`manifest`, `manifest_sha256`, and the T11 `launcher` / `launcher_sha256`.
Use actual SHA-256 values from the reviewed files. Keep `http_listen` configured
as the tunnel's loopback origin. Credentials never enter the plugin process.
The admitted `access` plugin requests a typed `host.effect`; the host checks the
configured tunnel and executes a private copy of the pinned connector bytes.
Ingress has an exact hostname rule and a final 404 rule. Connector logs remain
private. Plugin revocation stops the connector; connector exit fails the service
so an owner service manager can restart it. A connector-started observation is
not a claim that Cloudflare has registered the route.

Linux uses T11's user systemd/bubblewrap runner. On macOS the existing T11 runner
requires explicit `diagnostic_soft_limits: true`; its hard quotas remain
unqualified. This access implementation adds no new qualification gate.

See [T18-evidence.md](T18-evidence.md) for demonstrated behavior and outstanding
account/backend dependencies.
