# T32 implementation evidence

A signed candidate was installed into a disposable prefix on the owner's
Ubuntu amd64 host, then its public CLI created a real Firecracker VM and ran
Python in it. The candidate archive included the engine, Firecracker/jailer
and compressed Linux seed. A loopback HTTP origin stood in for the release
server; the install script's digest and signature checks ran normally with a
disposable signing key. This was a fresh prefix, **not a freshly provisioned
OS**: the host already had its virtualization dependencies. No public release
was published, and the public URL still serves the older 0.1.1 path.

The run printed:

```text
hello from the signed Lamp candidate
x86_64
42
install to code and cleanup seconds: 165.473
```

That observation exceeds the ticket's two-minute target. It is reported as
observed on the shared host, not replaced with another timing run.

The default template listed as ready with 2 vCPUs, 2048 MiB and a 20 GiB disk.
A subsequent zero-argument `sandbox create` succeeded. Rebuilt CLI fixes were
then checked against this same installed daemon and VM: separate stdout
`out`, stderr `err`, exit 7; code after daemon restart; positional IDs and global
credential flags. The final credential and cleanup check also demonstrated
MCP's automatic local credential discovery, `LAMP_TOKEN_FILE`, sandbox deletion,
and removal of the disposable engine. Remaining Firecracker count was 1,
matching the other sandbox present before this installation. No owner engine,
Docker object or visible app was changed.

The signed candidate's CLI exposed additional bugs during that continuation;
the source fixes were exercised with the rebuilt CLI using `LAMP_PREFIX` to
point at the installed daemon. These later CLI binaries were not re-published
or described as matching the earlier archive's signed digest.

## Preserved failures and fixes

- `01-long-socket-failure.log`: engine/template setup worked, but creating the
  sandbox hit the jail audit socket length limit. Engine paths were shortened.
- `02-admission-wait-failure.log`: the sandbox became ready, but the CLI treated
  exec admission as completion. It now follows public output cursors until
  completion and then reads the exit status.
- `03-signed-install.log`: the install-to-code flow worked; the extra stream
  assertion then found the CLI reading `stream` instead of the API's `channel`.
- `04-streams-restart-and-flags.log`: the stream correction and daemon restart
  worked; the global-flag check then found argument reordering ahead of a
  positional ID. Global flags now remain after positional IDs and before `--`.
- `05-credentials-and-cleanup.log`: the corrected argument path, MCP, token
  override, deletion and engine removal succeeded.

Transient demonstration-script errors (a local HTTP readiness race and a
Python indentation error) did not reach product operations. No historical
failure above was relabeled as a passing result.

## Checks

`make build` passed: C launcher, `go build ./...`, SDK generation consistency
(no SDK implementation changes), and `cargo build --workspace --locked`.
`build.log` records the final cached build. Go vet passed for the changed CLI,
installer, control, surface, release and localconfig packages. The existing
`TestCLIUsesCanonicalMutationHeaders` smoke passed. Public generation checks,
Python compilation, shell syntax checks, helper `--help`, rejection of an
unknown helper option, and `git diff --check` passed. No tests were added by
the implementation agent.

## Limits

Mac and Linux arm64 setup is implemented and packaged but not demonstrated
here. No privileged command was run on a Mac. This evidence does not establish
that the M2 Air restore failure is fixed, or that Mac DNS/HTTPS works. The Mac
setup avoids the old global RAM directory and fixed 4 GiB RAM disk, uses a
2 GiB template and allocates separate worker accounts. Those changes still
need actual release observations on both Macs. Automatic gVisor enrollment
on hosts without KVM remains unavailable; the installer explicitly refuses
those hosts. Fresh-host apt dependency installation was not exercised because
the Ubuntu host already had the dependencies.

The coordinator must build and publish complete platform archives with their
prepared seeds before the public README's two commands can use this path.
