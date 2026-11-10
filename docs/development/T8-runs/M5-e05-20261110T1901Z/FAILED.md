# Failed: aborted at trial 648 (restore refused while the Mac was locked)

648 trials recorded (643 measured), 0 with any error
(state 0, clock 0, isolation 0).
Trial 648 (the next fork) failed: the child's restore was refused by
Virtualization.framework, "The virtual machine failed to restore with error
'permission denied'" (summary.json). The system log at that instant shows
`SecKeyCreateDecryptedDataWithParameters failed: -25308 <sepk:p256(u)>:
unable to compute shared secret`: the M5's console session had locked at
13:52:35 local, the restore ran at 13:52:48. VZ seals saved machine state
with a Secure Enclave key of the console session; it cannot be opened while
the session is locked. Reproduced afterwards on the still-locked M5 with a
bare lamp-vz save/restore (save ok, restore "permission denied").

The row is rejected as run. The source continued; the failure is
availability, but the run stopped and so did not reach 1,000 captures.
