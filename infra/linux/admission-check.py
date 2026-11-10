#!/usr/bin/env python3
"""Owner LX guard: call before sandbox/RAM allocation; never changes services."""
import json
from pathlib import Path
import subprocess
import sys
import time


def main():
    raw = subprocess.check_output(['free', '-m'], text=True)
    memory = next(line.split() for line in raw.splitlines() if line.startswith('Mem:'))
    available = int(memory[-1])
    allocations = sorted({p.name for p in Path('/sys/fs/cgroup').rglob('sb-*') if p.is_dir()})
    vmm = []
    for p in Path('/proc').glob('[0-9]*/comm'):
        try:
            if p.read_text().strip().startswith('firecracker'):
                vmm.append(int(p.parent.name))
        except (FileNotFoundError, ProcessLookupError):
            pass
    value = dict(observed_at_ns=time.time_ns(), free_m=raw, available_mib=available,
                 allocations=allocations, vmm_pids=sorted(vmm), minimum_available_mib=12288, maximum_sandboxes=2)
    print(json.dumps(value))
    if available < 12288 or max(len(allocations), len(vmm)) >= 2:
        print('owner LX allocation guard denied: need >=12 GiB available and a free slot below two sandboxes', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
