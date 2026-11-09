# All hosted Lamp addresses live under eilmer.si

The website, docs, dashboard, relay, per-host addresses and sandbox web addresses all live under eilmer.si, the owner's domain for every Eilmer project. A separate domain for sandbox content was considered and rejected by the owner.

## Consequences

- Sandbox pages run untrusted code but share a registrable domain with account pages. The design must isolate cookies and origins within eilmer.si, for example with `__Host-` cookies and no domain-wide cookies.
- Every name shares one Let's Encrypt limit of 50 new certificates per week. Certificates must be issued without using it up, for example with wildcard certificates.
