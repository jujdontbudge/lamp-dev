#!/usr/bin/env bash
# T20 lane run on ubuntu-server: for each nested lane in turn, boot it from
# its clean snapshot, copy in the static lamp-supervisor, runsc and a public
# Alpine root, record engine detection/selection and one gVisor task, then
# power it off and revert it to `clean`. One lane runs at a time.
#   bash lanes.sh OUT_DIR lane...      (lanes: nv l510 lx22 d12 f40)
set -Eeuo pipefail
OUT=$1; shift; mkdir -p "$OUT"
SUP=$HOME/lamp-t20/src/target/x86_64-unknown-linux-musl/release/lamp-supervisor
KEY=$HOME/.lamp-lanes/id_ed25519
v() { sudo -n virsh -c qemu:///system "$@"; }
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
sudo -n tar -C /var/lib/lamp/t20-gvisor-rootfs -czf "$work/rootfs.tgz" .
sudo -n chown "$USER" "$work/rootfs.tgz"
tar -C /usr/local/lib/lamp -czf "$work/gvisor.tgz" runsc gvisor-bin firecracker
cp "$SUP" "$work/lamp-supervisor"
TASK='{"origin":"owner_task","argv":["sh","-c","uname -sr; id -u; dmesg | head -1; wget -T3 -qO- http://example.com >/dev/null 2>&1 || echo no-network"],"memory_mib":256,"timeout_seconds":60}'
for lane in "$@"; do
  name=lamp-$lane; log=$OUT/$lane.json
  [[ -z $(v list --name | grep '^lamp-') ]] || { echo "another lane is running" >&2; exit 1; }
  echo "free before $lane: $(free -m | awk 'NR==2{print $7}') MiB" >&2
  v start "$name" >/dev/null
  ip=
  for _ in $(seq 120); do
    ip=$(v domifaddr "$name" --source lease 2>/dev/null | awk '/ipv4/{split($4,a,"/");print a[1]}' | head -1)
    [[ -n $ip ]] && ssh -i "$KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=3 -o LogLevel=ERROR "lamp@$ip" true 2>/dev/null && break
    sleep 2
  done
  S="ssh -i $KEY -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR lamp@$ip"
  scp -q -i "$KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR "$work"/* "lamp@$ip:/tmp/"
  $S 'set -e; cd /tmp; sudo -n mkdir -p /opt/lamp/root; sudo -n tar -C /opt/lamp -xzf gvisor.tgz; sudo -n tar -C /opt/lamp/root -xzf rootfs.tgz; sudo -n install -m 0755 lamp-supervisor /opt/lamp/lamp-supervisor' >&2
  {
    echo "{\"lane\": \"$lane\", \"os_release\": $($S '. /etc/os-release; printf %s "\"$PRETTY_NAME\""'), \"kernel\": \"$($S uname -r)\","
    echo "\"engines_default\": $($S 'sudo -n /opt/lamp/lamp-supervisor engines --firecracker /opt/lamp/firecracker --runsc /opt/lamp/runsc' || true),"
    echo "\"engines_secrets_public_ci\": $($S 'sudo -n /opt/lamp/lamp-supervisor engines --firecracker /opt/lamp/firecracker --runsc /opt/lamp/runsc --secrets --public-ci' || true),"
    echo "\"engines_capture\": $($S 'sudo -n /opt/lamp/lamp-supervisor engines --firecracker /opt/lamp/firecracker --runsc /opt/lamp/runsc --capture' || true),"
    echo "\"gvisor_task\": $($S "echo '$TASK' | sudo -n /opt/lamp/lamp-supervisor gvisor-task --firecracker /opt/lamp/firecracker --runsc /opt/lamp/runsc --rootfs /opt/lamp/root" || true),"
    echo "\"gvisor_task_custodian_override\": $($S "echo '$TASK' | sudo -n /opt/lamp/lamp-supervisor gvisor-task --firecracker /opt/lamp/firecracker --runsc /opt/lamp/runsc --rootfs /opt/lamp/root --allow-with-vm 2>/dev/null" || true),"
    echo "\"gvisor_login_task\": $($S "echo '${TASK%\}},\"login\":true}' | sudo -n /opt/lamp/lamp-supervisor gvisor-task --firecracker /opt/lamp/firecracker --runsc /opt/lamp/runsc --rootfs /opt/lamp/root --allow-with-vm 2>/dev/null" || true)"
    echo "}"
  } > "$log.raw"
  python3 -c 'import json,sys; json.dump(json.load(open(sys.argv[1])), open(sys.argv[2],"w"), indent=1)' "$log.raw" "$log" && rm "$log.raw"
  v shutdown "$name" >/dev/null
  for _ in $(seq 60); do [[ $(v domstate "$name") == "shut off" ]] && break; sleep 2; done
  [[ $(v domstate "$name") == "shut off" ]] || v destroy "$name" >/dev/null
  v snapshot-revert "$name" clean
  echo "$lane done: $(python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));print(d["kernel"], d["engines_default"]["choice"]["engine"], d["gvisor_task"].get("exit_code", d["gvisor_task"].get("error")))' "$log")" >&2
done
