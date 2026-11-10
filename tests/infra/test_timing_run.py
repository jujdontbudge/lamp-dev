"""Stub reproductions for infra/linux/timing-run.sh (review M2 and S1).

systemctl and sudo are replaced on PATH by stubs that keep unit state in a
temporary directory and log every call, so no real service is touched.
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
	[ -e "$STUB/fail-start" ] && exit 1
	for u; do touch "$STUB/active/$u"; done ;;
esac
'''
SUDO = '''#!/bin/bash
echo "sudo $*" >> "$STUB/events"
[ -e "$STUB/fail-sudo" ] && exit 1
exit 0
'''


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
        self.env = {**os.environ, "STUB": str(self.dir), "PATH": f"{self.dir / 'bin'}:{os.environ['PATH']}",
                    "TIMING_RUN_RESTORE_WAIT": "1", "TIMING_RUN_STOP_GRACE": "2"}

    def flag(self, name):
        (self.dir / name).touch()

    def command(self, body):
        # The measurement stub records which units were active while it ran.
        return ["--", "bash", "-c", f'echo "ran $(ls "$STUB/active" | wc -l)" >> "$STUB/events"; {body}']

    def start(self, *args):
        return subprocess.Popen([str(SCRIPT), *args], env=self.env, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, text=True)

    def run_wrapper(self, *args):
        p = self.start(*args)
        out, err = p.communicate(timeout=60)
        return p.returncode, err

    def events(self):
        return (self.dir / "events").read_text().splitlines()

    def active(self):
        return sorted(os.listdir(self.dir / "active"))

    def test_success_stops_runs_and_restores(self):
        code, err = self.run_wrapper(*self.command("exit 0"))
        self.assertEqual(code, 0, err)
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


if __name__ == "__main__":
    unittest.main()
