# Dependency and license boundary

Lamp host source and generated bindings are Apache-2.0 (LICENSE), except the
optional relay server: `internal/relay` and `cmd/lamp-relay` are AGPL-3.0-only
(`internal/relay/LICENSE`). Build `lamp-relay` separately. Host/client/issuer
binaries do not import relay server code and retain the Apache-2.0 boundary.
Operators must offer corresponding relay source to its network users, including
local modifications; the repository's private status does not waive that duty.
Dependencies retain their own licenses. No guest OS, VMM, Docker runtime, guest
media or third-party source is vendored by T1.

Direct build/runtime dependencies are pinned by go.sum and Cargo.lock; Python
contract verification dependencies are pinned in tools/requirements.txt.

| Dependency | Purpose | Upstream license |
| --- | --- | --- |
| google.golang.org/protobuf, protoc/protoc-gen-go | Bounded generated process protocol | BSD-3-Clause |
| golang.org/x/sys | OS peer credentials, journal locking and private Ethernet descriptors | BSD-3-Clause |
| tailscale.com v1.96.5 and its pinned module graph | Embedded userspace tailnet, host-only sandbox member identities, workload identity federation, TCP/UDP endpoints and selected-context DNS | BSD-3-Clause (Tailscale); transitive dependencies retain their own licenses |
| gvisor.dev/gvisor (pinned in go.mod) | Private Ethernet TCP/IP endpoint stack; no ambient host bridge | Apache-2.0 |
| golang.org/x/net | IDNA normalization and DNS-message parsing | BSD-3-Clause |
| github.com/caddyserver/certmagic | Automatic ACME HTTPS and renewal | Apache-2.0 |
| github.com/mholt/acmez/v3 | Separate fleet CSR issuer, DNS-01 only | Apache-2.0 |
| github.com/miekg/dns | Authoritative CAA and DNS-01 policy queries | BSD-3-Clause |
| github.com/gliderlabs/ssh, golang.org/x/crypto/ssh | Restricted OpenSSH console and fleet certificates | BSD-3-Clause |
| github.com/coder/websocket | SSH transport over owner HTTPS and tunnel routes | ISC |
| cloudflared (owner-installed, digest pinned) | Optional named Cloudflare Tunnel connector | Apache-2.0 |
| prost/prost-build | Rust Protobuf messages and development generator | Apache-2.0 |
| serde/serde_json | Public wire serialization | MIT OR Apache-2.0 |
| jsonschema and its pinned Python dependencies | Development-only schema verification | See installed distribution license metadata; not shipped in host binaries |

Protocol reuse follows architecture §4: maintained implementations for wire
correctness at a trust boundary. Dependency security/advisory ownership and SBOM
publication are release work (T28/T31); T1 makes no release certification claim.
