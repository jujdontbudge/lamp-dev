#!/bin/bash
# T28 review #9 and #1 on the Ubuntu laptop: disposable store, systemd lampd,
# installer-run Linux supervisor (firecracker), one sandbox.
set -u
K=/home/owner/lamp-t28k; P=$K/p; R=$K/releases; ST=/var/lib/lamp/t28/store; TPL=tp_b116a7a5482b6d66a32bd9d892
step(){ echo; echo "=== $(date -u +%H:%M:%SZ) $*"; }
step "capacity before the sandbox: live firecracker=$(pgrep -c firecracker), MiB available=$(free -m | awk '/Mem:/{print $7}')"
[ "$(pgrep -c firecracker)" -lt 2 ] && [ "$(free -m | awk '/Mem:/{print $7}')" -ge 12288 ] || { echo "capacity rule not met; stopping"; exit 1; }
B=$R/5.0.0/linux-amd64/payload/bin/lamp-supervisor
G=$(sudo -n $B grant-install --store $ST --caller-uid $(id -u) --max-generation 1000 --ttl-s 7200 | python3 -c 'import json,sys;print(json.load(sys.stdin)["grant_id"])'); echo "$G" > $K/grant
step "install 5.0.0 (systemd lampd, supervisor 5.0.0 from the release)"
SA=""; for a in --store $ST --socket /run/t28/w.sock --admin-socket /run/t28/w.admin --mem-dir /run/t28/m --netns-dir /run/t28/n --uid-base 2200000000 --host-id hs_lx --allow-uid $(id -u); do SA="$SA --supervisor-arg=$a"; done
LA=""; for a in --worker-socket /run/t28/w.sock --worker-id lamp-supervisor@hs_lx --worker-boot-id $(cat /proc/sys/kernel/random/boot_id) --worker-uid 0 --grant-id $G --worker-timeout 60s; do LA="$LA --lampd-arg=$a"; done
$K/lamp-installer install --release $R/5.0.0 --prefix $P --name t28lx --trust $K/release-trust.pub --service systemd --supervisor-dir /usr/local/lib/lamp-t28lx --agent=false $SA $LA 2>&1 | tail -1
$K/lamp-installer status --prefix $P | tail -1
export LAMP_SOCKET=$P/run/api.sock LAMP_TOKEN=$(cat $P/config/owner.token); L=$K/lamp
step "sandbox from $TPL with a live guest process (tmpfs heartbeat)"
$L sandbox create --template-id $TPL --wait-ms 60000 --json > $K/create.json; head -c 400 $K/create.json; echo
SB=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["resource_ids"][0]["id"])' $K/create.json) || exit 1; echo $SB > $K/sb
for i in $(seq 1 60); do st=$($L sandbox get $SB --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["state"])'); [ "$st" = ready ] && break; sleep 2; done; echo "sandbox $SB $st"
g(){ X=$($L exec create --sandbox-id $SB --argv-json "$(python3 -c 'import json,sys;print(json.dumps(["sh","-c",sys.argv[1]]))' "$1")" --wait-ms 60000 --json | python3 -c 'import json,sys;d=json.load(sys.stdin);print(d["resource_ids"][0]["id"])'); for i in $(seq 1 30); do s=$($L exec get $X --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["state"])'); case $s in exited|failed|killed|completed|succeeded) break;; esac; sleep 1; done; $L exec watch $X --json | python3 -c '
import json,sys,base64
w=json.load(sys.stdin); print("".join(base64.b64decode(f.get("data_base64","")).decode(errors="replace") for f in (w.get("frames") or w.get("items") or []) if f.get("stream","stdout")=="stdout").strip())'; }
g "setsid sh -c 'echo \$\$ > /dev/shm/beat.pid; while :; do date +%s >> /dev/shm/beat; sleep 1; done' >/dev/null 2>&1 </dev/null & sleep 2; echo boot=\$(cat /proc/sys/kernel/random/boot_id) pid=\$(cat /dev/shm/beat.pid) beats=\$(wc -l < /dev/shm/beat)"
PROBE="echo boot=\$(cat /proc/sys/kernel/random/boot_id) pid=\$(cat /dev/shm/beat.pid) alive=\$(kill -0 \$(cat /dev/shm/beat.pid) && echo yes) beats=\$(wc -l < /dev/shm/beat)"
step "#9 update 5.0.0 -> 5.1.0: new Linux supervisor, same firecracker/jailer"
$K/lamp-installer publish --prefix $P --release $R/5.1.0 >/dev/null
$K/lamp-installer update --prefix $P --release $P/mirror/5.1.0 2>&1 | tail -1; echo "rc=$?"
$K/lamp-installer status --prefix $P | tail -1
g "$PROBE"
step "#1 update 5.1.0 -> 5.2.0: signed release whose supervisor fails to start"
$K/lamp-installer publish --prefix $P --release $R/5.2.0 >/dev/null
$K/lamp-installer update --prefix $P --release $P/mirror/5.2.0 2>&1 | tail -1; echo "rc=${PIPESTATUS[0]}"
$K/lamp-installer status --prefix $P | tail -1
echo "supervisor process: $(ps -o args= -p $(cat $P/run/supervisor.pid) | cut -c1-90)"
g "$PROBE"
step "updater events"
grep -v '"installed"' $P/config/updates.jsonl | cut -c1-330
step "cleanup"
R2=$($L sandbox get $SB --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["revision"])'); $L sandbox delete $SB --expected-revision $R2 --wait-ms 60000 --json >/dev/null; echo "sandbox delete rc=$?"
$K/lamp-installer uninstall --prefix $P --data delete --confirm-delete t28lx 2>&1 | tail -1
sudo -n $R/5.0.0/linux-amd64/payload/bin/lamp-supervisor grant-revoke --store $ST $G >/dev/null 2>&1
echo "left: lamp-t28lx units=$(systemctl list-unit-files 'lamp-t28*' --no-legend | wc -l) supervisor dir=$(ls -d /usr/local/lib/lamp-t28lx 2>/dev/null | wc -l) live firecracker=$(pgrep -c firecracker)"
