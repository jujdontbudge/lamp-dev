#!/bin/bash
# timing-run.sh -- COMMAND...: run one hard timing run on lane LX.
#
# The owner's own desktop services may be stopped only for a hard timing run
# and are started again as soon as it ends (owner, 2026-11-09). This stops the
# ones that are running, prepares host memory, runs COMMAND, and restarts
# exactly the services it stopped, whatever COMMAND's outcome. The T3 Code
# server, the Lamp CI runner and Docker are never touched.
#
# Failure handling:
# - If any stop or host preparation fails, COMMAND is not run; whatever was
#   stopped is restored and the wrapper exits 75.
# - COMMAND runs in its own process group. On TERM, INT or HUP to the wrapper,
#   and also after COMMAND exits, the whole group is sent TERM (KILL after
#   STOP_GRACE seconds), and no service is restored until the group is empty,
#   so no part of a measurement runs with the services already back. A signal
#   that arrives while COMMAND is being launched is held until its PID is
#   recorded. (A process that leaves the group with setsid escapes this; the
#   benchmark runner does not do that.)
# - Every stopped unit is started and then verified active. If any restore
#   fails, the wrapper exits 76, whatever COMMAND's status was.
#
# Memory prep: every fork restores a 4 GiB guest; KVM vCPU creation needs
# order-5 pages, and a fragmented host fails it ("page allocation failure" in
# kvm_vm_ioctl_create_vcpu). Dropping clean caches and compacting first is a
# host preparation, not a change to anything that is measured.
set -u
OWNER_UNITS=(owner-app-dm-tt.service owner-app-dm-ig.service owner-app-dm-chrome.service owner-app-beeper.service)
# Fixed: no environment variable changes any of these. The tests run a
# patched copy of this file instead (tests/infra/test_timing_run.py).
readonly STOP_GRACE=60
readonly RESTORE_WAIT=10
stopped=()
group=
finishing=
pending=

log() { echo "timing-run: $*" >&2; }

restore() {
	local failed=0 unit state i
	for unit in "${stopped[@]}"; do
		systemctl --user start "$unit" || log "FAILED to start $unit"
	done
	for unit in "${stopped[@]}"; do
		state=$(systemctl --user is-active "$unit")
		for ((i = 0; i < RESTORE_WAIT * 2; i++)); do
			[ "$state" = active ] && break
			sleep 0.5
			state=$(systemctl --user is-active "$unit")
		done
		log "$unit $state"
		[ "$state" = active ] || failed=1
	done
	return "$failed"
}

group_alive() { [ -n "$group" ] && kill -0 -- "-$group" 2>/dev/null; }

# Stops every process of the measurement's group and waits until the group is
# empty, so services are never restored while any part of it still runs.
reap() {
	[ -n "$group" ] || return 0
	local i
	if group_alive; then
		kill -TERM -- "-$group" 2>/dev/null
		for ((i = 0; i < STOP_GRACE * 10; i++)); do
			group_alive || break
			sleep 0.1
		done
		if group_alive; then
			log "command group ignored TERM for ${STOP_GRACE}s; killing it"
			kill -KILL -- "-$group" 2>/dev/null
		fi
		i=0
		while group_alive; do
			i=$((i + 1))
			[ $((i % 100)) -eq 0 ] && log "waiting for the command group to exit"
			sleep 0.1
		done
	fi
	wait "$group" 2>/dev/null
	group=
}

finish() {
	local status=$1
	finishing=1
	reap
	if ! restore; then
		log "RESTORE FAILED: some owner services are not running"
		status=76
	fi
	log "exit $status"
	exit "$status"
}

on_signal() {
	# A second signal while restoring must not abandon the restoration.
	if [ -n "$finishing" ]; then
		log "received $1 while finishing; continuing"
		return
	fi
	log "received $1; stopping the command before restoring services"
	finish $((128 + $2))
}
handle_signals() {
	trap 'on_signal TERM 15' TERM
	trap 'on_signal INT 2' INT
	trap 'on_signal HUP 1' HUP
}
handle_signals

[ "${1:-}" = "--" ] && shift
if [ $# -eq 0 ]; then
	log "usage: timing-run.sh -- COMMAND..."
	exit 64
fi

for unit in "${OWNER_UNITS[@]}"; do
	if systemctl --user is-active --quiet "$unit"; then
		stopped+=("$unit")
	fi
done

prepared=1
if [ ${#stopped[@]} -gt 0 ]; then
	log "stopping ${stopped[*]}"
	if ! systemctl --user stop "${stopped[@]}"; then
		log "stopping owner services failed"
		prepared=0
	fi
	for unit in "${stopped[@]}"; do
		if systemctl --user is-active --quiet "$unit"; then
			log "$unit is still active after stop"
			prepared=0
		fi
	done
fi
if [ "$prepared" = 1 ]; then
	# Both writes must succeed: a failed drop_caches is not hidden by a
	# successful compact_memory.
	if ! sync || ! sudo -n sh -c 'echo 3 > /proc/sys/vm/drop_caches && echo 1 > /proc/sys/vm/compact_memory'; then
		log "host memory preparation failed"
		prepared=0
	fi
fi
if [ "$prepared" = 0 ]; then
	log "preparation failed; the command was not run"
	finish 75
fi
log "buddyinfo $(grep Normal /proc/buddyinfo 2>/dev/null)"

# COMMAND runs as a background job in its own process group (job control on
# for the launch only); `wait` returns early when a trapped signal arrives.
# Signals are held from the launch until the group is recorded.
trap 'pending="TERM 15"' TERM
trap 'pending="INT 2"' INT
trap 'pending="HUP 1"' HUP
set -m
"$@" </dev/null &
set +m
group=$!
handle_signals
if [ -n "$pending" ]; then
	# shellcheck disable=SC2086 # "NAME NUMBER"
	on_signal $pending
fi
wait "$group"
status=$?
log "command exit $status"
finish "$status"
