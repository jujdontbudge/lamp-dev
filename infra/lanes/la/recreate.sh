#!/bin/bash
# Idempotent ensure/start/verify/stop. --reset explicitly deletes only lamp-la.
set -euo pipefail
cd "$(dirname "$0")"
command -v limactl >/dev/null || { echo 'Install Lima without administrator privileges first.' >&2; exit 1; }
case "${1:-}" in ''|--reset) ;; *) echo "Usage: $0 [--reset]" >&2; exit 2;; esac
mkdir -p evidence
exec > >(tee evidence/latest-run.log) 2>&1
stop_lane() {
  if [[ $(limactl list lamp-la --format '{{.Status}}' 2>/dev/null) == Running ]]; then
    limactl stop --tty=false lamp-la
  fi
}
finish() {
  result=$?
  trap - EXIT
  if ! stop_lane; then
    echo 'ERROR: lane shutdown failed; check limactl list lamp-la.' >&2
    exit 1
  fi
  limactl list --format '{{.Name}} {{.Status}}' lamp-la
  exit "$result"
}
trap finish EXIT
if limactl list --format '{{.Name}}' | grep -qx lamp-la; then
  if [[ ${1:-} == --reset ]]; then
    stop_lane
    limactl delete --tty=false lamp-la
  fi
fi
if ! limactl list --format '{{.Name}}' | grep -qx lamp-la; then
  limactl create --tty=false --name=lamp-la lamp-la.yaml
fi
date -u
sw_vers
sysctl -n machdep.cpu.brand_string hw.ncpu hw.memsize
limactl --version
limactl start --tty=false --timeout=20m lamp-la
# Refresh login groups after first-boot cloud-init adds the user to kvm.
ssh -F "${LIMA_HOME:-$HOME/.lima}/lamp-la/ssh.config" -O exit lima-lamp-la || true
limactl shell --tty=false --workdir=/tmp lamp-la bash -s < verify-guest.sh
limactl list --format '{{.Name}} {{.Status}}' lamp-la
