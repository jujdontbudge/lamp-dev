# T34 real VM evidence

The demonstration uses the public HTTP API, real Firecracker, a disposable
public-project template and owner-private temporary recovery keys. It never
prints authentication or recovery keys. Run it only on the enrolled Ubuntu
host after checking the shared capacity policy; it checks capacity before
creating, restoring or forking a VM.

Historical attempts are preserved honestly:

- `attempt-01.txt`: setup failed because the worker host identity differed
  from the template custody host.
- `attempt-02.txt`: suspend/restart/resume and memory snapshot restoration
  worked; the demonstration wrapper collided with the volume name argument.
- `attempt-03.txt`: those paths worked, along with encrypted volume backup
  and a memory fork during a 400 MB write. The source stayed ready and readable.
  The next wake ran its first counter read, then exited 70 after losing the
  protected audit collector. This attempt used the host-installed guard.

The current script uses this checkout's built guard for its new network policy.
The remaining recovery and expiry demonstration is pending shared VM capacity.
