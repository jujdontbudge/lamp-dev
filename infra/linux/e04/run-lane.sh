#!/bin/bash
# run-lane.sh LANE RUN_NAME [--smoke] -- one E04 run on lane LX for one filesystem.
#
# LANE is ext4 (Lamp's loop-backed dm-thin disks), xfs or btrfs (reflinks).
# Prepares the lane's disposable volumes (fixture.py prepare), collects the
# §22 metadata, runs the independent harness (benchmarks.run --suite E04)
# with this fixture, then tears the volumes down. The harness output and
# the fixture's per-scenario artifacts are kept, whatever the result.
#
# Needs: a fault-injection supervisor at $E04_BINARY (default
# ~/t7/bin/lamp-supervisor-e04), sudo -n, and the 512 MiB `e04` template
# (images/templates/make-bench.py SEED e04 512) in $E04_TEMPLATE_STORE.
# Not a hard timing run: no service is stopped. The fixture refuses to
# start a scenario unless host memory stays above its floor.
set -u
# One exclusive timing window per lane on LX, shared with other tickets'
# measurements (coordinator, 2026-11-10): the lock is held for this run only.
LOCK=/tmp/lamp-lx-timing.lock
if [ -z "${LAMP_LX_TIMING_LOCKED:-}" ]; then
  LAMP_LX_TIMING_LOCKED=1 exec flock "$LOCK" "$0" "$@"
fi
LANE=$1; NAME=$2; shift 2
SRC=$(cd "$(dirname "$0")/../../.." && pwd)
BIN=${E04_BINARY:-$HOME/t7/bin/lamp-supervisor-e04}
WORK=/var/lib/lamp-t7/e04
RUNS=/var/lib/lamp-t7/e04-runs
OUT=$RUNS/$NAME-$LANE
ART=/var/lib/lamp-t7/e04-artifacts/$NAME
STORE=${E04_TEMPLATE_STORE:-/var/lib/lamp-t7/e04-template/mnt}
TEMPLATE=$(basename "$(sudo -n readlink "$STORE/templates/names/e04")")
case $LANE in ext4) UIDB=970000;; xfs) UIDB=980000;; btrfs) UIDB=990000;; *) echo "bad lane" >&2; exit 2;; esac
[ -e "$OUT" ] && { echo "refusing: $OUT exists" >&2; exit 2; }
[ -d "$RUNS" ] || sudo -n install -d -o "$(id -u)" -g "$(id -g)" "$RUNS" || exit 1
F=(sudo -n python3 -I "$SRC/infra/linux/e04/fixture.py")
"${F[@]}" prepare --lane "$LANE" --work $WORK --binary "$BIN" --artifacts "$ART" --uid-base $UIDB \
  --template-store "$STORE" --template e04 || exit 1
CFG=$WORK/$LANE/config.json
META=$(mktemp)
python3 "$SRC/infra/linux/run-metadata.py" --store "$STORE" "$SRC" $WORK "$TEMPLATE" > "$META.base" &&
  python3 -I "$SRC/infra/linux/e04/metadata.py" "$META.base" "$CFG" > "$META"
cd "$SRC"
python3 -m benchmarks.run --suite E04 --metadata "$META" --output "$OUT" \
  --storage-fixture "$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1:]))' "${F[@]}" run --config "$CFG")" "$@"
rc=$?
"${F[@]}" teardown --config "$CFG"
cp "$META" "$OUT.metadata.json"
for f in failures.log memory-guard-tripped; do
  sudo -n test -e "$WORK/$LANE/$f" && sudo -n mv "$WORK/$LANE/$f" "$OUT.$f"
done
rm -f "$META" "$META.base"
echo "run $NAME-$LANE exit $rc, output $OUT, artifacts $ART/$LANE"
exit $rc
