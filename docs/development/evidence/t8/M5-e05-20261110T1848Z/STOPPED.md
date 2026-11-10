# Stopped: failed (clock errors)

Stopped by the agent after 191 trials (186 measured), 2026-11-10.
187 trials failed. Every failure is a clock error: the *source* guest's
wall clock fell ~0.4 s further behind the host after each fork, because the
guest clock stands still while the VM is paused for the capture and nothing
re-applied the time to the source (the fork got it through its barrier).
State errors: 0. Isolation errors: 0.

This row is rejected as run. The fix (re-apply the source's own identity
barrier with the host time after every capture) is a code change; the
re-run is a separate run directory. This record is kept unchanged.
