#!/bin/bash
# T31 Linux arm64 evidence on lane LA (Ubuntu 26.04 arm64 Lima VM on the M5, nested KVM):
# curl | sh from the public release, then one Firecracker sandbox run by that
# release's own arm64 supervisor, guard and lampd. Everything is disposable.
set -u
B=https://github.com/JohanSardar/lamp/releases/download/v0.1.0
P=/tmp/l31u; K=/var/lib/lamp-t31; RT=/run/l31; IMG=/var/tmp/lamp-t31.xfs
FC=$HOME/lamp-t20-la/firecracker-v1.17.0-aarch64.tgz; MACSEED=$HOME/lamp-t8/seed
step(){ echo; echo "=== $(date -u +%H:%M:%SZ) $*"; }
step "VM capacity: MiB available=$(free -m | awk '/Mem:/{print $7}'), live firecracker=$(pgrep -c firecracker), $(uname -m), $(grep PRETTY_NAME /etc/os-release)"
[ "$(free -m | awk '/Mem:/{print $7}')" -ge 4096 ] || { echo "VM memory too low; stopping"; exit 1; }
step "curl | sh from the public release into a fresh prefix"
rm -rf $P
curl -fsSL $B/install.sh | LAMP_PREFIX=$P LAMP_SERVICE=process sh 2>&1 | tail -2
$P/current/bin/lamp-installer status --prefix $P
file -b $P/current/lib/lamp-supervisor | cut -c1-60
step "host tools: passt for the guard uplink; Firecracker v1.17.0 aarch64 (digest pinned by install-engine.sh)"
command -v pasta >/dev/null || sudo -n DEBIAN_FRONTEND=noninteractive apt-get install -y -qq passt >/dev/null
echo "pasta: $(command -v pasta)"
echo "e351ebe4f7a16b5873bbd51005d2e6767103cff4d5ebc829df2d3f95a93e2256  $FC" | sha256sum -c -
step "disposable XFS reflink store"
for m in $(findmnt -rno TARGET | grep "^$RT" | sort -r); do sudo -n umount $m; done
findmnt -rno TARGET $K >/dev/null && sudo -n umount $K
sudo -n rm -rf $K $RT $IMG; truncate -s 28G $IMG; mkfs.xfs -q -m reflink=1 $IMG
sudo -n mkdir -p $K && sudo -n mount -o loop $IMG $K && sudo -n chmod 700 $K; sudo -n mkdir -p -m 755 $RT
sudo -n mkdir -m 755 $K/bin; sudo -n tar -xzf $FC -C $K/bin --strip-components=1 release-v1.17.0-aarch64/firecracker-v1.17.0-aarch64 release-v1.17.0-aarch64/jailer-v1.17.0-aarch64
sudo -n mv $K/bin/firecracker-v1.17.0-aarch64 $K/bin/firecracker; sudo -n mv $K/bin/jailer-v1.17.0-aarch64 $K/bin/jailer
step "Linux arm64 seed: the pinned 6.18.62 kernel (Firecracker's aarch64 config) and Ubuntu 26.04 root from the Mac seed, in the Linux seed format"
sudo -n mkdir -m 700 $K/seed
sudo -n cp --reflink=auto --sparse=always $MACSEED/Image $K/seed/vmlinux
sudo -n cp --reflink=auto --sparse=always $MACSEED/rootfs.ext4 $K/seed/rootfs.ext4
sudo -n cp $MACSEED/packages.txt $K/seed/packages.txt
sudo -n python3 - $K/seed $MACSEED/seed.json <<'PY'
import hashlib, json, os, sys
out, mac = sys.argv[1], json.load(open(sys.argv[2]))
CHUNK = 4 << 20; ZERO = hashlib.sha256(bytes(CHUNK)).digest()
def digest(p):
    h = hashlib.sha256(); size = os.path.getsize(p)
    with open(p, 'rb') as f:
        fd = f.fileno()
        for start in range(0, size, CHUNK):
            n = min(CHUNK, size - start)
            try: data_at = os.lseek(fd, start, os.SEEK_DATA)
            except OSError: data_at = size
            if n == CHUNK and data_at >= start + n:
                h.update(ZERO); continue
            f.seek(start); h.update(hashlib.sha256(f.read(n)).digest())
    return h.hexdigest()
obj = lambda p: {"digest": digest(os.path.join(out, p)), "size": os.path.getsize(os.path.join(out, p))}
seed = {"format": "lamp.seed/v0", "digest_alg": "sha256-c4m", "name": "ubuntu-26.04-arm64", "guest_os": "linux", "arch": "arm64",
        "kernel": {**obj("vmlinux"), "source": mac["kernel"]["source"], "source_sha256": mac["kernel"]["source_sha256"],
                   "cmdline": "console=ttyS0 reboot=k panic=1 root=/dev/vda rw init=/sbin/lamp-init quiet"},
        "rootfs": {**obj("rootfs.ext4"), "filesystem": "ext4", "distribution": mac["rootfs"]["distribution"]},
        "init": mac["init"], "packages": obj("packages.txt")}
open(os.path.join(out, "seed.json"), "w").write(json.dumps(seed, sort_keys=True, separators=(",", ":")))
PY
SUP=$P/current/lib/lamp-supervisor
C="--store $K/store --mem-dir $RT/m --netns-dir $RT/n --uid-base 2310000000 --host-id hs_t31la --firecracker $K/bin/firecracker --jailer $K/bin/jailer"
REF=$(sudo -n $SUP seed-import $K/seed $C 2>&1 | grep '^lamp-seed/' | tail -1); echo "seed $REF"
printf 'format="lamp.template/v1"\nname="t31-release-arm64"\nguest_os="linux"\narch="arm64"\nmemory_mib=1024\nseed_ref="%s"\n' "$REF" | sudo -n tee $K/task.toml >/dev/null
sudo -n $SUP template-build $K/task.toml $C > /tmp/l31-tpl.log 2>&1; tail -3 /tmp/l31-tpl.log
TPL=$(grep '^{' /tmp/l31-tpl.log | tail -1 | python3 -c 'import json,sys;print(json.load(sys.stdin)["template_id"])') || { echo "template build failed"; TPL=""; }
echo "template $TPL"
if [ -n "$TPL" ]; then
G=$(sudo -n $SUP grant-install --store $K/store --caller-uid $(id -u) --ttl-s 3600 | python3 -c 'import json,sys;print(json.load(sys.stdin)["grant_id"])')
printf '{"guard":"%s","pasta":"%s","audit_dir":"%s/audit","revision":1,"principal_id":"pr_local","exceptions":[]}\n' "$P/current/lib/lamp-guard" "$(command -v pasta)" "$K" | sudo -n tee $K/network.json >/dev/null
sudo -n setsid sh -c "echo \$\$ > $RT/sup.pid; exec $SUP serve $C --socket $RT/w.sock --allow-uid $(id -u) --network-policy $K/network.json" > /tmp/l31-sup.log 2>&1 < /dev/null &
for i in $(seq 1 60); do sudo -n test -S $RT/w.sock && break; sleep 1; done
SPID=$(sudo -n cat $RT/sup.pid); echo "supervisor pid $SPID: $(sudo -n readlink /proc/$SPID/exe)"
step "the installed lampd attached to that supervisor"
mkdir -p -m 700 /tmp/l31d
setsid $P/current/bin/lampd --state-dir /tmp/l31d/state --socket /tmp/l31d/api.sock --worker-socket $RT/w.sock --worker-id lamp-supervisor@hs_t31la --worker-boot-id $(cat /proc/sys/kernel/random/boot_id) --worker-uid 0 --worker-pid $SPID --grant-id $G --worker-timeout 1m > /tmp/l31-lampd.log 2>&1 < /dev/null &
echo $! > /tmp/l31d/lampd.pid
for i in $(seq 1 30); do [ -S /tmp/l31d/api.sock ] && break; sleep 1; done
export LAMP_SOCKET=/tmp/l31d/api.sock; L=$P/current/bin/lamp
step "one sandbox (template memory 1 GiB, to fit the 8 GiB VM)"
$L sandbox create --template-id $TPL --resources-json '{"vcpus":2,"memory_mib":1024,"disk_gib":20,"pids_max":4096}' --wait-ms 60000 --json > /tmp/l31d/create.json; head -c 400 /tmp/l31d/create.json; echo
SB=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["resource_ids"][0]["id"])' /tmp/l31d/create.json 2>/dev/null) || SB=""
if [ -n "$SB" ]; then
for i in $(seq 1 120); do st=$($L sandbox get --sandbox-id $SB --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["state"])'); case $st in ready|failed) break;; esac; sleep 1; done
echo "sandbox $SB $st; live firecracker=$(pgrep -c firecracker); VM MiB available=$(free -m | awk '/Mem:/{print $7}')"
X=$($L exec create --sandbox-id $SB --argv-json '["sh","-c","echo hello from a Lamp 0.1.0 arm64 sandbox; uname -srm; grep PRETTY_NAME /etc/os-release; nproc"]' --wait-ms 60000 --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["resource_ids"][0]["id"])')
for i in $(seq 1 30); do s=$($L exec get --exec-id $X --json | python3 -c 'import json,sys;d=json.load(sys.stdin)["data"];print(d["state"], d.get("exit_code"))'); case $s in exited*|failed*|succeeded*|completed*) break;; esac; sleep 1; done; echo "exec $X: $s"
$L exec watch --exec-id $X --json | python3 -c '
import json,sys,base64
w=json.load(sys.stdin); print("".join(base64.b64decode(f.get("data_base64","")).decode(errors="replace") for f in (w.get("frames") or w.get("items") or []) if f.get("stream","stdout")=="stdout").strip())'
step "release the sandbox"
R2=$($L sandbox get --sandbox-id $SB --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["revision"])'); $L sandbox delete --sandbox-id $SB --expected-revision $R2 --wait-ms 60000 --json >/dev/null; echo "sandbox delete rc=$?; live firecracker=$(pgrep -c firecracker)"
else tail -5 /tmp/l31-sup.log; fi
kill $(cat /tmp/l31d/lampd.pid) 2>/dev/null; sudo -n kill $SPID 2>/dev/null; sleep 3
fi
step "clean up"
$P/current/bin/lamp-installer uninstall --prefix $P --data delete --confirm-delete default 2>&1 | tail -1
for m in $(findmnt -rno TARGET | grep "^$RT" | sort -r); do sudo -n umount $m; done
sudo -n umount $K; sudo -n rm -rf $K $RT $IMG /tmp/l31d
echo "left: live firecracker=$(pgrep -c firecracker), VM MiB available=$(free -m | awk '/Mem:/{print $7}')"
