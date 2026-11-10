"""Stub reproductions for infra/linux/timing-run.sh.

systemctl and sudo are replaced on PATH by stubs that keep unit state in a
temporary directory and log every call, so no real service is touched.

The production script has no test hooks. Most tests run a patched copy (see
patched_script) that shortens its timeouts, points the vm sysctl writes at
a temporary directory, and optionally pauses inside the launch window. The
ProductionScript tests run the real file with every former hook variable set
and show it ignores them.
"""
import os
import signal
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[2] / "infra" / "linux" / "timing-run.sh"
UNITS = ["excerlock-dm-tt.service", "excerlock-dm-ig.service", "excerlock-dm-chrome.service", "excerlock-beeper.service"]

SYSTEMCTL = r'''#!/bin/bash
# stub: systemctl --user VERB [--quiet] UNIT...
shift  # --user
verb=$1; shift
[ "${1:-}" = --quiet ] && { quiet=1; shift; }
echo "$verb $*" >> "$STUB/events"
case $verb in
is-active)
	if [ -e "$STUB/active/$1" ]; then [ -z "${quiet:-}" ] && echo active; exit 0; fi
	[ -z "${quiet:-}" ] && echo inactive; exit 3 ;;
stop)
	[ -e "$STUB/fail-stop" ] && exit 1
	for u; do rm -f "$STUB/active/$u"; done ;;
start)
	# Any recorded measurement process still alive at restore time is a bug.
	for pid in $(cat "$STUB/pids" 2>/dev/null); do
		kill -0 "$pid" 2>/dev/null && echo "ALIVE $pid" >> "$STUB/events"
	done
	[ -e "$STUB/fail-start" ] && exit 1
	for u; do touch "$STUB/active/$u"; done ;;
esac
'''
SUDO = '''#!/bin/bash
# stub: runs the command itself (without -n); in the patched copy the vm
# sysctls are files in $STUB/vm. With $STUB/record-only it only logs.
echo "sudo $*" >> "$STUB/events"
[ "$1" = -n ] || exit 2
shift
[ -e "$STUB/fail-sudo" ] && exit 1
[ -e "$STUB/record-only" ] && exit 0
exec "$@"
'''

HOOK_VARIABLES = ("TIMING_RUN_VM_DIR", "TIMING_RUN_TEST_LAUNCH_DELAY", "TIMING_RUN_STOP_GRACE", "TIMING_RUN_RESTORE_WAIT")


def patched_script(directory, vm, launch_delay=None):
    """A test copy of timing-run.sh; every patch must apply exactly where expected."""
    text = SCRIPT.read_text()
    patches = [("readonly STOP_GRACE=60\n", "readonly STOP_GRACE=2\n", 1),
               ("readonly RESTORE_WAIT=10\n", "readonly RESTORE_WAIT=1\n", 1),
               ("/proc/sys/vm/", f"{vm}/", 2)]
    if launch_delay is not None:
        patches.append(("set +m\ngroup=$!\n", f"set +m\nsleep {launch_delay}\ngroup=$!\n", 1))
    for old, new, count in patches:
        assert text.count(old) == count, (old, text.count(old))
        text = text.replace(old, new)
    path = Path(directory) / "timing-run.sh"
    path.write_text(text)
    path.chmod(0o755)
    return path


class TimingRun(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="timing-run-"))
        (self.dir / "bin").mkdir()
        (self.dir / "active").mkdir()
        for name, body in (("systemctl", SYSTEMCTL), ("sudo", SUDO)):
            path = self.dir / "bin" / name
            path.write_text(body)
            path.chmod(0o755)
        for unit in UNITS:
            (self.dir / "active" / unit).touch()
        (self.dir / "vm").mkdir()
        for name in ("drop_caches", "compact_memory"):
            (self.dir / "vm" / name).touch()
        self.env = {**{k: v for k, v in os.environ.items() if k not in HOOK_VARIABLES},
                    "STUB": str(self.dir), "PATH": f"{self.dir / 'bin'}:{os.environ['PATH']}"}
        self.script = patched_script(self.dir, self.dir / "vm")

    def flag(self, name):
        (self.dir / name).touch()

    def command(self, body):
        # The measurement stub records which units were active while it ran.
        return ["--", "bash", "-c", f'echo "ran $(ls "$STUB/active" | wc -l)" >> "$STUB/events"; {body}']

    def start(self, *args):
        return subprocess.Popen([str(self.script), *args], env=self.env, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, text=True)

    def run_wrapper(self, *args):
        p = self.start(*args)
        out, err = p.communicate(timeout=60)
        return p.returncode, err

    def events(self):
        return (self.dir / "events").read_text().splitlines()

    def active(self):
        return sorted(os.listdir(self.dir / "active"))

    def wait_for(self, text):
        deadline = time.monotonic() + 20
        while not (self.dir / "events").exists() or text not in (self.dir / "events").read_text():
            self.assertLess(time.monotonic(), deadline, f"never saw {text!r}")
            time.sleep(0.05)

    def assert_dead(self, pid):
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            return
        os.kill(pid, signal.SIGKILL)
        self.fail(f"measurement process {pid} outlived the wrapper")

    def test_success_stops_runs_and_restores(self):
        code, err = self.run_wrapper(*self.command("exit 0"))
        self.assertEqual(code, 0, err)
        self.assertEqual((self.dir / "vm" / "drop_caches").read_text(), "3\n")
        self.assertEqual((self.dir / "vm" / "compact_memory").read_text(), "1\n")
        ran = [e for e in self.events() if e.startswith("ran")]
        self.assertEqual([r.split()[1] for r in ran], ["0"], self.events())  # all four stopped while measuring
        self.assertEqual(self.active(), sorted(UNITS))

    def test_command_status_is_kept_and_services_restored(self):
        code, err = self.run_wrapper(*self.command("exit 3"))
        self.assertEqual(code, 3, err)
        self.assertEqual(self.active(), sorted(UNITS))

    def test_failed_stop_does_not_measure(self):
        self.flag("fail-stop")
        code, err = self.run_wrapper(*self.command("exit 0"))
        self.assertEqual(code, 75, err)
        self.assertFalse(any(e.startswith("ran") for e in self.events()), self.events())
        self.assertEqual(self.active(), sorted(UNITS))

    def test_failed_host_preparation_does_not_measure_and_restores(self):
        self.flag("fail-sudo")
        code, err = self.run_wrapper(*self.command("exit 0"))
        self.assertEqual(code, 75, err)
        self.assertFalse(any(e.startswith("ran") for e in self.events()))
        self.assertEqual(self.active(), sorted(UNITS))

    def test_failed_cache_drop_does_not_measure_even_if_compaction_succeeds(self):
        (self.dir / "vm" / "drop_caches").unlink()
        (self.dir / "vm" / "drop_caches").mkdir()  # the write fails; compact_memory still works
        code, err = self.run_wrapper(*self.command("exit 0"))
        self.assertEqual(code, 75, err)
        self.assertFalse(any(e.startswith("ran") for e in self.events()), self.events())
        self.assertEqual(self.active(), sorted(UNITS))

    def test_failed_compaction_does_not_measure(self):
        (self.dir / "vm" / "compact_memory").unlink()
        (self.dir / "vm" / "compact_memory").mkdir()
        code, err = self.run_wrapper(*self.command("exit 0"))
        self.assertEqual(code, 75, err)
        self.assertFalse(any(e.startswith("ran") for e in self.events()))

    def test_signal_during_launch_is_held_until_the_command_is_recorded(self):
        # TERM arrives between `COMMAND &` and recording its PID.
        self.script = patched_script(self.dir, self.dir / "vm", launch_delay=1)
        body = ('echo $$ >> "$STUB/pids"; trap \'sleep 0.5; echo child-exit >> "$STUB/events"; exit 0\' TERM; '
                'echo started >> "$STUB/events"; while :; do sleep 0.05; done')
        p = self.start(*self.command(body))
        self.wait_for("started")
        p.send_signal(signal.SIGTERM)  # the wrapper is still in the launch delay
        _, err = p.communicate(timeout=30)
        self.assertEqual(p.returncode, 143, err)
        events = self.events()
        self.assertIn("child-exit", events)
        self.assertFalse([e for e in events if e.startswith("ALIVE")], events)
        first_start = min(i for i, e in enumerate(events) if e.startswith("start "))
        self.assertLess(events.index("child-exit"), first_start, events)

    def test_term_reaps_the_whole_process_group(self):
        body = 'sleep 30 & echo $! >> "$STUB/pids"; echo started >> "$STUB/events"; wait'
        p = self.start(*self.command(body))
        self.wait_for("started")
        p.send_signal(signal.SIGTERM)
        _, err = p.communicate(timeout=30)
        self.assertEqual(p.returncode, 143, err)
        self.assertFalse([e for e in self.events() if e.startswith("ALIVE")], self.events())
        self.assert_dead(int((self.dir / "pids").read_text().split()[0]))
        self.assertEqual(self.active(), sorted(UNITS))

    def test_group_member_ignoring_term_is_killed_before_restore(self):
        body = ('bash -c \'trap "" TERM; while :; do sleep 0.05; done\' & echo $! >> "$STUB/pids"; '
                'echo started >> "$STUB/events"; wait')
        p = self.start(*self.command(body))
        self.wait_for("started")
        p.send_signal(signal.SIGTERM)
        _, err = p.communicate(timeout=30)
        self.assertEqual(p.returncode, 143, err)
        self.assertIn("killing it", err)
        self.assertFalse([e for e in self.events() if e.startswith("ALIVE")], self.events())
        self.assert_dead(int((self.dir / "pids").read_text().split()[0]))

    def test_stragglers_after_a_normal_exit_are_stopped_before_restore(self):
        body = 'sleep 30 & echo $! >> "$STUB/pids"; exit 0'
        code, err = self.run_wrapper(*self.command(body))
        self.assertEqual(code, 0, err)
        self.assertFalse([e for e in self.events() if e.startswith("ALIVE")], self.events())
        self.assert_dead(int((self.dir / "pids").read_text().split()[0]))

    def test_failed_restore_exits_nonzero(self):
        self.flag("fail-start")
        code, err = self.run_wrapper(*self.command("exit 0"))
        self.assertEqual(code, 76, err)
        self.assertIn("RESTORE FAILED", err)
        for unit in UNITS:
            self.assertIn(f"start {unit}", self.events())

    def test_term_stops_and_reaps_the_command_before_restoring(self):
        body = ('trap \'sleep 0.5; echo child-exit >> "$STUB/events"; exit 0\' TERM; '
                'echo started >> "$STUB/events"; while :; do sleep 0.05; done')
        p = self.start(*self.command(body))
        deadline = time.monotonic() + 20
        while not (self.dir / "events").exists() or "started" not in (self.dir / "events").read_text():
            self.assertLess(time.monotonic(), deadline)
            time.sleep(0.05)
        p.send_signal(signal.SIGTERM)
        _, err = p.communicate(timeout=30)
        self.assertEqual(p.returncode, 143, err)
        events = self.events()
        self.assertIn("child-exit", events)
        first_start = min(i for i, e in enumerate(events) if e.startswith("start "))
        self.assertLess(events.index("child-exit"), first_start, events)
        self.assertEqual(self.active(), sorted(UNITS))

    def test_command_ignoring_term_is_killed_then_services_restored(self):
        body = 'trap "" TERM; echo started >> "$STUB/events"; while :; do sleep 0.05; done'
        p = self.start(*self.command(body))
        deadline = time.monotonic() + 20
        while not (self.dir / "events").exists() or "started" not in (self.dir / "events").read_text():
            self.assertLess(time.monotonic(), deadline)
            time.sleep(0.05)
        p.send_signal(signal.SIGTERM)
        _, err = p.communicate(timeout=30)
        self.assertEqual(p.returncode, 143, err)
        self.assertIn("killing it", err)
        self.assertEqual(self.active(), sorted(UNITS))


class ProductionScript(TimingRun):
    """The real timing-run.sh, with every former test hook set, ignores them."""

    def setUp(self):
        super().setUp()
        self.script = SCRIPT
        self.flag("record-only")  # the stub sudo must not write the real /proc
        hook_vm = self.dir / "hook-vm"
        hook_vm.mkdir()
        self.victim = self.dir / "victim"
        self.victim.write_text("unrelated\n")
        (hook_vm / "drop_caches").symlink_to(self.victim)
        (hook_vm / "compact_memory").write_text("")
        self.hook_vm = hook_vm
        self.env.update(TIMING_RUN_VM_DIR=str(hook_vm), TIMING_RUN_TEST_LAUNCH_DELAY="30",
                        TIMING_RUN_STOP_GRACE="999", TIMING_RUN_RESTORE_WAIT="999")

    def test_production_file_has_no_hooks(self):
        text = SCRIPT.read_text()
        for name in HOOK_VARIABLES:
            self.assertNotIn(name, text)
        self.assertIn("readonly STOP_GRACE=60\n", text)
        self.assertIn("readonly RESTORE_WAIT=10\n", text)

    def test_sysctl_paths_are_fixed(self):
        code, err = self.run_wrapper(*self.command("exit 0"))
        self.assertEqual(code, 0, err)
        sudo = [e for e in self.events() if e.startswith("sudo ")]
        self.assertEqual(len(sudo), 1, self.events())
        self.assertIn("/proc/sys/vm/drop_caches", sudo[0])
        self.assertIn("/proc/sys/vm/compact_memory", sudo[0])
        self.assertNotIn(str(self.hook_vm), sudo[0])
        self.assertEqual(self.victim.read_text(), "unrelated\n")
        self.assertEqual((self.hook_vm / "compact_memory").read_text(), "")

    def test_launch_delay_and_timeouts_are_ignored(self):
        body = ('trap \'echo child-exit >> "$STUB/events"; exit 0\' TERM; '
                'echo started >> "$STUB/events"; while :; do sleep 0.05; done')
        p = self.start(*self.command(body))
        self.wait_for("started")
        sent = time.monotonic()
        p.send_signal(signal.SIGTERM)
        _, err = p.communicate(timeout=20)
        self.assertEqual(p.returncode, 143, err)
        self.assertLess(time.monotonic() - sent, 5, "a 30 s launch delay or 999 s timeout was honoured")
        self.assertEqual(self.active(), sorted(UNITS))

    # The behavioural tests above need the patched copy; skip them here.
    for name in [n for n in dir(TimingRun) if n.startswith("test_")]:
        locals()[name] = None
    del name


if __name__ == "__main__":
    unittest.main()
