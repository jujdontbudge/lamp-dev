#!/bin/bash
# T31 release evidence on the Ubuntu laptop: curl | sh from lamp.eilmer.si,
# then one Firecracker sandbox run by that release's own supervisor, guard and lampd.
set -u
B=https://lamp.eilmer.si
P=/tmp/l31u; K=/var/lib/lamp/t31; RT=/run/l31; SEED=$HOME/lamp-t6/seed-ubuntu-6
step(){ echo; echo "=== $(date -u +%H:%M:%SZ) $*"; }
step "capacity: live firecracker=$(pgrep -c firecracker), MiB available=$(free -m | awk '/Mem:/{print $7}')"
[ "$(pgrep -c firecracker)" -lt 2 ] && [ "$(free -m | awk '/Mem:/{print $7}')" -ge 12288 ] || { echo "capacity rule not met; stopping"; exit 1; }
step "curl | sh from lamp.eilmer.si into a fresh prefix"
rm -rf $P
curl -fsSL $B/install.sh | LAMP_PREFIX=$P LAMP_SERVICE=process sh 2>&1 | tail -2
$P/current/bin/lamp-installer status --prefix $P
ls $P/current/lib
step "disposable store from the release's own supervisor"
for m in $(findmnt -rno TARGET | grep "^$RT" | sort -r); do sudo -n umount $m; done
sudo -n rm -rf $K $RT; sudo -n mkdir -p -m 700 $K; sudo -n mkdir -p -m 755 $RT
SUP=$P/current/lib/lamp-supervisor
C="--store $K/store --mem-dir $RT/m --netns-dir $RT/n --uid-base 2310000000 --host-id hs_t31"
sudo -n mkdir -m 700 $K/seed; for f in vmlinux rootfs.ext4 packages.txt seed.json; do sudo -n cp --reflink=auto $SEED/$f $K/seed/; done
sudo -n $SUP seed-import $K/seed $C 2>&1 | tail -1
REF="lamp-seed/$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["name"])' $SEED/seed.json)@sha256:$(sha256sum $SEED/seed.json | cut -d' ' -f1)"
printf 'format="lamp.template/v1"\nname="t31-release"\nguest_os="linux"\narch="x86_64"\nseed_ref="%s"\n' "$REF" | sudo -n tee $K/task.toml >/dev/null
TPL=$(sudo -n $SUP template-build $K/task.toml $C 2>&1 | grep '^{' | tail -1 | python3 -c 'import json,sys;print(json.load(sys.stdin)["template_id"])') || { echo "template build failed"; exit 1; }
echo "template $TPL"
G=$(sudo -n $SUP grant-install --store $K/store --caller-uid $(id -u) --ttl-s 3600 | python3 -c 'import json,sys;print(json.load(sys.stdin)["grant_id"])')
printf '{"guard":"%s","pasta":"/usr/bin/pasta","audit_dir":"%s/audit","revision":1,"principal_id":"pr_local","exceptions":[]}\n' "$P/current/lib/lamp-guard" "$K" | sudo -n tee $K/network.json >/dev/null
sudo -n setsid sh -c "echo \$\$ > $RT/sup.pid; exec $SUP serve $C --socket $RT/w.sock --allow-uid $(id -u) --network-policy $K/network.json" > /tmp/l31-sup.log 2>&1 < /dev/null &
for i in $(seq 1 60); do sudo -n test -S $RT/w.sock && break; sleep 1; done
SPID=$(sudo -n cat $RT/sup.pid); echo "supervisor pid $SPID: $(sudo -n readlink /proc/$SPID/exe)"
step "the installed lampd attached to that supervisor"
mkdir -p -m 700 /tmp/l31d
setsid $P/current/bin/lampd --state-dir /tmp/l31d/state --socket /tmp/l31d/api.sock --worker-socket $RT/w.sock --worker-id lamp-supervisor@hs_t31 --worker-boot-id $(cat /proc/sys/kernel/random/boot_id) --worker-uid 0 --worker-pid $SPID --grant-id $G --worker-timeout 1m > /tmp/l31-lampd.log 2>&1 < /dev/null &
echo $! > /tmp/l31d/lampd.pid
for i in $(seq 1 30); do [ -S /tmp/l31d/api.sock ] && break; sleep 1; done
export LAMP_SOCKET=/tmp/l31d/api.sock; L=$P/current/bin/lamp
step "one sandbox"
$L sandbox create --template-id $TPL --wait-ms 60000 --json > /tmp/l31d/create.json; head -c 300 /tmp/l31d/create.json; echo
SB=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["resource_ids"][0]["id"])' /tmp/l31d/create.json) || { echo "create failed"; SB=""; }
for i in $(seq 1 60); do st=$($L sandbox get --sandbox-id $SB --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["state"])'); [ "$st" = ready ] && break; sleep 1; done; echo "sandbox $SB $st; live firecracker=$(pgrep -c firecracker)"
X=$($L exec create --sandbox-id $SB --argv-json '["sh","-c","echo hello from a Lamp 0.1.1 sandbox; uname -sr; grep PRETTY_NAME /etc/os-release; nproc"]' --wait-ms 60000 --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["resource_ids"][0]["id"])')
for i in $(seq 1 30); do s=$($L exec get $X --json | python3 -c 'import json,sys;d=json.load(sys.stdin)["data"];print(d["state"], d.get("exit_code"))'); case $s in exited*|failed*|succeeded*|completed*) break;; esac; sleep 1; done; echo "exec $X: $s"
$L exec watch $X --json | python3 -c '
import json,sys,base64
w=json.load(sys.stdin); print("".join(base64.b64decode(f.get("data_base64","")).decode(errors="replace") for f in (w.get("frames") or w.get("items") or []) if f.get("stream","stdout")=="stdout").strip())'
step "release the sandbox and clean up"
R2=$($L sandbox get --sandbox-id $SB --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["revision"])'); $L sandbox delete --sandbox-id $SB --expected-revision $R2 --wait-ms 60000 --json >/dev/null; echo "sandbox delete rc=$?"
kill $(cat /tmp/l31d/lampd.pid); sudo -n kill $SPID; sleep 3
sudo -n $SUP grant-revoke $G --store $K/store >/dev/null 2>&1
$P/current/bin/lamp-installer uninstall --prefix $P --data delete --confirm-delete default 2>&1 | tail -1
for m in $(findmnt -rno TARGET | grep "^$RT" | sort -r); do sudo -n umount $m; done
sudo -n rm -rf $K $RT; rm -rf /tmp/l31d
echo "left: live firecracker=$(pgrep -c firecracker), MiB available=$(free -m | awk '/Mem:/{print $7}')"
