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
# - TERM, INT or HUP to the wrapper is forwarded to COMMAND, which is waited
#   for (KILLed after STOP_GRACE seconds) before any service is restored, so
#   no measurement runs with the services already back.
# - Every stopped unit is started and then verified active. If any restore
#   fails, the wrapper exits 76, whatever COMMAND's status was.
#
# Memory prep: every fork restores a 4 GiB guest; KVM vCPU creation needs
# order-5 pages, and a fragmented host fails it ("page allocation failure" in
# kvm_vm_ioctl_create_vcpu). Dropping clean caches and compacting first is a
# host preparation, not a change to anything that is measured.
set -u
OWNER_UNITS=(excerlock-dm-tt.service excerlock-dm-ig.service excerlock-dm-chrome.service excerlock-beeper.service)
STOP_GRACE=${TIMING_RUN_STOP_GRACE:-60}
RESTORE_WAIT=${TIMING_RUN_RESTORE_WAIT:-10}
stopped=()
child=
finishing=

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

# Stops the measurement and waits for it to exit, so services are never
# restored while it is still running.
reap() {
	[ -n "$child" ] || return 0
	if kill -0 "$child" 2>/dev/null; then
		kill -TERM "$child" 2>/dev/null
		local i
		for ((i = 0; i < STOP_GRACE * 10; i++)); do
			kill -0 "$child" 2>/dev/null || break
			sleep 0.1
		done
		if kill -0 "$child" 2>/dev/null; then
			log "command ignored TERM for ${STOP_GRACE}s; killing it"
			kill -KILL "$child" 2>/dev/null
		fi
	fi
	# A signal can interrupt `wait`; keep waiting until the child is reaped.
	while kill -0 "$child" 2>/dev/null; do
		wait "$child" 2>/dev/null
	done
	child=
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
trap 'on_signal TERM 15' TERM
trap 'on_signal INT 2' INT
trap 'on_signal HUP 1' HUP

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
	if ! sync || ! sudo -n sh -c 'echo 3 > /proc/sys/vm/drop_caches; echo 1 > /proc/sys/vm/compact_memory'; then
		log "host memory preparation failed"
		prepared=0
	fi
fi
if [ "$prepared" = 0 ]; then
	log "preparation failed; the command was not run"
	finish 75
fi
log "buddyinfo $(grep Normal /proc/buddyinfo 2>/dev/null)"

# COMMAND runs as a background child so the wrapper can forward signals and
# reap it; `wait` returns early when a trapped signal arrives.
"$@" &
child=$!
wait "$child"
status=$?
child=
log "command exit $status"
finish "$status"
