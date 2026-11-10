#!/bin/bash
# ext4-store.sh setup|start|stop|lampd -- a second Lamp store on the laptop's
# ordinary ext4 root filesystem (T7, lane LX).
#
# The installed service keeps its XFS loop store (/var/lib/lamp) and is never
# touched. This store is a plain directory on the existing ext4 filesystem, with
# no reformatting and no loop filesystem: sandbox disks use Lamp's loop-backed
# dm-thin backend inside the store, and durable captures live in its own
# objects/journal. It has its own sockets, memory/netns directories and jail UID
# range.
#
#   setup   copy the bench template and the producer key from /var/lib/lamp
#   start   start the supervisor (sudo -n) in the background; prints its PID
#   lampd   exec a lampd for the calling user against it (fresh control grant)
#   stop    stop the supervisor (SIGTERM, which shuts the engine down)
#
# BIN (default ~/t7/bin/lamp-supervisor-plain) is a release build of this
# tree's lamp-supervisor, without fault injection; LAMPD defaults to
# ~/lamp-t6/bin/lampd.
set -eu
STORE=${STORE:-/var/lib/lamp-t7/ext4-store}
RUN=/run/lamp-t7/ext4
BIN=${BIN:-$HOME/t7/bin/lamp-supervisor-plain}
LAMPD=${LAMPD:-$HOME/lamp-t6/bin/lampd}
TEMPLATE=${TEMPLATE:-bench}
STATE=${LAMPD_STATE:-$HOME/t7/lampd-ext4}

case ${1:-} in
setup)
	sudo -n install -d -m 0755 "$STORE" "$STORE/templates/names"
	sudo -n install -d -m 0700 "$STORE/keys"
	fs=$(stat -f -c %T "$STORE")
	[ "$fs" = ext2/ext3 ] || { echo "store is on $fs, not ext4" >&2; exit 1; }
	tid=$(basename "$(sudo -n readlink "/var/lib/lamp/templates/names/$TEMPLATE")")
	sudo -n test -d "$STORE/templates/$tid" ||
		sudo -n cp -a --sparse=always "/var/lib/lamp/templates/$tid" "$STORE/templates/"
	sudo -n ln -sfn "../$tid" "$STORE/templates/names/$TEMPLATE"
	sudo -n cp -a /var/lib/lamp/keys/producer.key "$STORE/keys/producer.key"
	findmnt -T "$STORE" -o SOURCE,FSTYPE,OPTIONS,TARGET
	echo "$tid"
	;;
start)
	sudo -n install -d -m 0755 "$RUN"
	sudo -n rm -f "$RUN/worker.sock" "$RUN/admin.sock"
	sudo -n bash -c "nohup '$BIN' serve --store '$STORE' --socket '$RUN/worker.sock' \
		--admin-socket '$RUN/admin.sock' --allow-uid $(id -u) --mem-dir '$RUN/mem' \
		--netns-dir '$RUN/netns' --uid-base 950000 --disk-backend thin \
		--network-policy /etc/lamp/network.json >> '$STORE/supervisor.log' 2>&1 < /dev/null &
		echo \$! > '$RUN/pid'"
	for _ in $(seq 600); do
		sudo -n test -S "$RUN/worker.sock" && break
		sleep 0.1
	done
	sudo -n test -S "$RUN/worker.sock" || { sudo -n tail -5 "$STORE/supervisor.log"; exit 1; }
	sudo -n cat "$RUN/pid"
	;;
lampd)
	pid=$(sudo -n cat "$RUN/pid")
	g=$(sudo -n "$BIN" grant-install --store "$STORE" --caller-uid "$(id -u)" --ttl-s 172800 |
		python3 -c 'import json,sys; print(json.load(sys.stdin)["grant_id"])')
	mkdir -p "$STATE"
	chmod 700 "$STATE"
	rm -f "$STATE/api.sock"
	exec "$LAMPD" --worker-socket "$RUN/worker.sock" \
		--worker-id "lamp-supervisor@$(hostname)" --worker-boot-id "$(cat /proc/sys/kernel/random/boot_id)" \
		--worker-uid 0 --worker-pid "$pid" --grant-id "$g" --state-dir "$STATE/control" --socket "$STATE/api.sock"
	;;
stop)
	pid=$(sudo -n cat "$RUN/pid")
	sudo -n kill -TERM "$pid"
	for _ in $(seq 300); do
		sudo -n kill -0 "$pid" 2>/dev/null || break
		sleep 0.1
	done
	sudo -n kill -0 "$pid" 2>/dev/null && { echo "supervisor $pid still running" >&2; exit 1; }
	sudo -n rm -f "$RUN/pid"
	;;
*)
	echo "usage: ext4-store.sh setup|start|lampd|stop" >&2
	exit 2
	;;
esac
