#!/bin/sh
# Review round 2 timing run (R1 freeze covers every cgroup): full 4 GiB capture inside every timed fork.
# The owner Chrome service is stopped only for this run and started again on exit.
set -u
export XDG_RUNTIME_DIR=/run/user/$(id -u)
export LAMP_GRANT_ID=$(cat ~/lamp-t2/grant-id)
B=/usr/local/lib/lamp/lamp-supervisor
T=tp_6e9a8241175f34853740c06b41
E=~/lamp-t2/evidence/r2
mkdir -p $E
cd $E
restart() { systemctl --user start owner-app-dm-chrome.service; echo "chrome restarted $(date -Is): $(systemctl --user is-active owner-app-dm-chrome.service)"; }
trap restart EXIT
trap "exit 1" INT TERM HUP
echo "chrome stop $(date -Is)"; systemctl --user stop owner-app-dm-chrome.service
{ date -Is; uname -a; nproc; lscpu | grep -E "Model name|MHz"; free -m; uptime; cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor 2>/dev/null; findmnt /run/lamp/mem; findmnt -T /var/lib/lamp; echo "chrome: $(systemctl --user is-active owner-app-dm-chrome.service)"; echo "t3: $(pgrep -fc t3 || true) t3 processes"; } > host.txt
chrome_state() { echo "chrome $(date -Is): $(systemctl --user is-active owner-app-dm-chrome.service) since $(systemctl --user show -p ActiveEnterTimestamp --value owner-app-dm-chrome.service)"; }
run() { name=$1; shift; chrome_state; echo "== $name $(date -Is)"; $B ctl "$@" > $name.jsonl 2> $name.err; tail -1 $name.jsonl > $name.summary; cat $name.summary; }
run create-200 bench-create $T 200 20
run fork-0m bench-fork $T 0 100 5
run fork-128m bench-fork $T 128 100 5
run fork-1g bench-fork $T 1024 100 5
run fork-1g-local bench-fork $T 1024 100 5 --durability local
chrome_state; echo DONE $(date -Is)
