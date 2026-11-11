#!/bin/bash
# T28 review #8 on the M5: a release that changes the VM engine (lamp-vz and
# the signed snapshot format) is refused while saved machine state made by
# the running engine exists. Disposable store; usage: SCRIPT RELEASES_DIR TRUST
set -u
R=$1; TRUST=$2; P=~/lt28e; ST=/private/var/lamp-t28/store; TPL=tp_d6285dd359be9d5cef26d69ca7
step(){ echo; echo "=== $(date -u +%H:%M:%SZ) $*"; }
rm -rf $P; mkdir -p $P/kit; cp $R/6.0.0/darwin-arm64/payload/bin/lamp-installer $R/6.0.0/darwin-arm64/payload/bin/lamp $P/kit/
B=$R/6.0.0/darwin-arm64/payload/bin/lamp-supervisor
G=$(sudo -n $B grant-install --store $ST --caller-uid $(id -u) --max-generation 1000 --ttl-s 7200 | python3 -c 'import json,sys;print(json.load(sys.stdin)["grant_id"])')
step "install 6.0.0 (supervisor and lamp-vz from the release)"
SA=""; for a in --store $ST --socket $P/w.sock --allow-uid $(id -u); do SA="$SA --supervisor-arg=$a"; done
LA=""; for a in --worker-socket $P/w.sock --worker-id lamp-supervisor@$(sysctl -n kern.hostname) --worker-boot-id $(sysctl -n kern.bootsessionuuid) --worker-uid 0 --grant-id $G; do LA="$LA --lampd-arg=$a"; done
$P/kit/lamp-installer install --release $R/6.0.0 --prefix $P/f --name t28e --trust $TRUST --service process --supervisor-dir /usr/local/lib/lamp-t28e --agent=false $SA $LA 2>&1 | tail -1
export LAMP_SOCKET=$P/f/run/api.sock LAMP_TOKEN=$(cat $P/f/config/owner.token); L=$P/kit/lamp
SB=$($L sandbox create --template-id $TPL --resources-json '{"vcpus":2,"memory_mib":1024,"disk_gib":20,"pids_max":1024}' --wait-ms 60000 --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["resource_ids"][0]["id"])')
echo "sandbox $SB $($L sandbox get $SB --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["state"])')"
echo "signed engine 6.0.0: $(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["engine"])' $R/6.0.0/darwin-arm64/manifest.json)"
echo "signed engine 6.1.0: $(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["engine"])' $R/6.1.0/darwin-arm64/manifest.json)"
BEFORE=$($P/kit/lamp-installer status --prefix $P/f | tail -1); echo "before: $BEFORE" | cut -c1-400
step "update 6.0.0 -> 6.1.0 (new lamp-vz, snapshot format 1 -> 2)"
$P/kit/lamp-installer publish --prefix $P/f --release $R/6.1.0 >/dev/null
$P/kit/lamp-installer update --prefix $P/f --release $P/f/mirror/6.1.0 2>&1 | tail -1; echo "rc=${PIPESTATUS[0]}"
step "after: nothing changed"
$P/kit/lamp-installer status --prefix $P/f | tail -1 | cut -c1-400
echo "sandbox $SB $($L sandbox get $SB --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["state"])')"
grep -v '"installed"' $P/f/config/updates.jsonl
step "cleanup"
R2=$($L sandbox get $SB --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["revision"])'); $L sandbox delete $SB --expected-revision $R2 --wait-ms 60000 --json >/dev/null; echo "sandbox delete rc=$?"
$P/kit/lamp-installer uninstall --prefix $P/f --data delete --confirm-delete t28e 2>&1 | tail -1
sudo -n $B grant-revoke --store $ST $G >/dev/null 2>&1; rm -rf $P
echo "left: supervisor=$(pgrep -f 'lamp-t28e/' | wc -l | tr -d ' ') dir=$(ls -d /usr/local/lib/lamp-t28e 2>/dev/null | wc -l | tr -d ' ')"
