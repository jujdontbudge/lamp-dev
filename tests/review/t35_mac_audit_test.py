"""Run: python3 tests/review/t35_mac_audit_test.py (macOS, no VM/root)."""
import json
import pathlib
import subprocess
import tempfile
import unittest

REPO = pathlib.Path(__file__).resolve().parents[2]


class MacNetworkAuditBoundary(unittest.TestCase):
    def test_vmm_cannot_truncate_network_audit(self):
        with tempfile.TemporaryDirectory(prefix="t35-review-") as temporary:
            root = pathlib.Path(temporary).resolve()
            sandbox = root / "sandbox"
            sandbox.mkdir(mode=0o700)
            # The engine keeps helper state in store/network, outside sb.dir.
            network = root / "network" / "review-g1-probe"
            network.mkdir(parents=True, mode=0o700)
            audit = network / "audit.ndjson"
            original = b'{"decision":"deny"}\n'
            audit.write_bytes(original)
            audit.chmod(0o600)
            spec = root / "spec.json"
            spec.write_text(json.dumps({
                "sandbox_id": "review", "dir": str(sandbox),
                "machine": {"guest": "linux", "kernel": str(root / "kernel"),
                            "cmdline": "", "machine_identifier_b64": ""},
                "cpus": 2, "memory_mib": 1024, "disks": [], "ethernet_fd": 3,
            }))
            profile = json.loads(subprocess.check_output([
                str(REPO / "target/debug/lamp-vz"), "profile", "--spec", str(spec)
            ]))["profile"]
            profile_path = root / "profile.sb"
            profile_path.write_text(profile)
            source = root / "probe.c"
            source.write_text(r'''
#include <sandbox.h>
#include <stdio.h>
#include <stdlib.h>
#include <fcntl.h>
#include <unistd.h>
int main(int argc, char **argv) {
    FILE *f = fopen(argv[1], "r");
    if (!f) return 90;
    fseek(f, 0, SEEK_END); long n = ftell(f); rewind(f);
    char *profile = calloc(n + 1, 1);
    if (fread(profile, 1, n, f) != n) return 91;
    fclose(f);
    char *error = NULL;
    if (sandbox_init(profile, 0, &error)) return 92;
    int fd = open(argv[2], O_WRONLY | O_TRUNC);
    if (fd < 0) return 0;
    close(fd);
    return 42;
}
''')
            probe = root / "probe"
            subprocess.run(["cc", "-Wno-deprecated-declarations", str(source), "-o", str(probe)], check=True)
            result = subprocess.run([str(probe), str(profile_path), str(audit)])
            self.assertEqual(result.returncode, 0,
                             f"VMM profile allowed audit truncation; exit={result.returncode}, bytes={audit.read_bytes()!r}")
            self.assertEqual(audit.read_bytes(), original)


if __name__ == "__main__":
    unittest.main()
