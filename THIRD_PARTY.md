# Dependency and license boundary

Lamp host source and generated bindings in this skeleton are Apache-2.0 (LICENSE).
A future relay server must be a separate AGPL-3.0 build; none is included here.
Dependencies retain their own licenses. No guest OS, VMM, Docker runtime, guest
media or third-party source is vendored by T1.

Direct build/runtime dependencies are pinned by go.sum and Cargo.lock; Python
contract verification dependencies are pinned in tools/requirements.txt.

| Dependency | Purpose | Upstream license |
| --- | --- | --- |
| google.golang.org/protobuf, protoc/protoc-gen-go | Bounded generated process protocol | BSD-3-Clause |
| golang.org/x/sys | OS peer credentials, journal locking and private Ethernet descriptors | BSD-3-Clause |
| tailscale.com v1.96.5 and its pinned module graph | Host-only sandbox member identities, TCP/UDP endpoints and selected-context DNS | BSD-3-Clause (Tailscale); transitive dependencies retain their own licenses |
| gvisor.dev/gvisor (pinned in go.mod) | Private Ethernet TCP/IP endpoint stack; no ambient host bridge | Apache-2.0 |
| golang.org/x/net | IDNA normalization and DNS-message parsing | BSD-3-Clause |
| prost/prost-build | Rust Protobuf messages and development generator | Apache-2.0 |
| serde/serde_json | Public wire serialization | MIT OR Apache-2.0 |
| jsonschema and its pinned Python dependencies | Development-only schema verification | See installed distribution license metadata; not shipped in host binaries |

Protocol reuse follows architecture §4: maintained implementations for wire
correctness at a trust boundary. Dependency security/advisory ownership and SBOM
publication are release work (T28/T31); T1 makes no release certification claim.
