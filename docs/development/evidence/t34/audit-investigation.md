# Audit-loss investigation

Attempt 3's wake restored the VM and produced one counter read, then the exec
exited 70. Its retained journal contains stdout only, with no stderr reason.
The guest collector uses exit 70 to fail closed when its protected audit
transport fails. The retained host process stream ends around the sleep exit;
an inherited collector opened a resume segment but made no further recorded
progress. Kernel logs for the UTC failure window contain no OOM kill.

The installed guard and checkout-built guard differ in binary digest. That
alone does not establish a version defect: `audit-protocol.txt` records both
actual receiver binaries correctly acknowledging the same process/heartbeat
stream. The exact cause of the historical transport loss cannot be established
from its retained diagnostics. An audit ACK delayed beyond the existing five
second deadline is a plausible cause under storage load, not a proven diagnosis
of attempt 3. Its failed result remains preserved.

A separate, reproducible recovery failure was diagnosed by
`restore-blocked-stack.txt`: dense disk extraction was blocked in write while
the lifecycle monitor held the service mutex inside filesystem rename. Public
operation reads then blocked and the HTTP connection expired. Extraction now
preserves zero runs as sparse holes and honors operation cancellation; the
engine still verifies the complete signed disk digest on import.

Using the checkout-built guard, attempt 5 completed the heavy-write fork and
wake, attempt 10 completed wake and encrypted backup, and attempt 14 restored
that long-suspended counter through a fresh prefix and another daemon restart
with advancing values. These observations establish the delivered lifecycle
paths; they do not turn attempt 3 into a success or prove its unknown cause.
