#!/bin/bash
# timing-run.sh -- COMMAND...: run one hard timing run on lane LX.
#
# The owner's own desktop services may be stopped only for a hard timing run
# and are started again as soon as it ends (owner, 2026-11-09). This stops the
# ones that are running, prepares host memory, runs COMMAND, and restarts
# exactly the services it stopped, whatever COMMAND's outcome. The T3 Code
# server, the Lamp CI runner and Docker are never touched.
#
# Memory prep: every fork restores a 4 GiB guest; KVM vCPU creation needs
# order-5 pages, and a fragmented host fails it ("page allocation failure" in
# kvm_vm_ioctl_create_vcpu). Dropping clean caches and compacting first is a
# host preparation, not a change to anything that is measured.
set -u
OWNER_UNITS=(excerlock-dm-tt.service excerlock-dm-ig.service excerlock-dm-chrome.service excerlock-beeper.service)
stopped=()
for unit in "${OWNER_UNITS[@]}"; do
	if systemctl --user is-active --quiet "$unit"; then
		stopped+=("$unit")
	fi
done
restart() {
	for unit in "${stopped[@]}"; do
		systemctl --user start "$unit" || echo "timing-run: FAILED to restart $unit" >&2
	done
	for unit in "${stopped[@]}"; do
		printf 'timing-run: %s %s\n' "$unit" "$(systemctl --user is-active "$unit")" >&2
	done
}
trap restart EXIT
[ "${1:-}" = "--" ] && shift
if [ ${#stopped[@]} -gt 0 ]; then
	echo "timing-run: stopping ${stopped[*]}" >&2
	systemctl --user stop "${stopped[@]}"
fi
sync
sudo -n sh -c 'echo 3 > /proc/sys/vm/drop_caches; echo 1 > /proc/sys/vm/compact_memory'
echo "timing-run: buddyinfo $(grep Normal /proc/buddyinfo)" >&2
"$@"
status=$?
echo "timing-run: command exit $status" >&2
exit $status
