#!/usr/bin/env python3
"""One real check that a task's delegated cgroup cannot block later execs.

Run against an isolated T22 fixture (tools/kits_lx_demo_host.py). As the
confined task it tries the review's sequence on the shared tasks cgroup, then
does the same inside its own delegated leaf, keeps those processes alive, and
asks Lamp for one more exec. Releases the VM.
"""
import argparse
import json
from pathlib import Path

from kits.control import Control

ATTEMPT = r'''
import json,os,pathlib,subprocess,time
out={}
def tryw(path,value):
    try:
        pathlib.Path(path).write_text(value);return 'ok'
    except OSError as e:
        return type(e).__name__+':'+str(e.errno)
own='/sys/fs/cgroup'+next(l[3:] for l in open('/proc/self/cgroup').read().splitlines() if l.startswith('0::'))
out['own_leaf']=own
# The review's sequence against the shared tasks cgroup.
out['mkdir_tasks_child']='ok'
try:os.mkdir('/sys/fs/cgroup/tasks/x')
except OSError as e:out['mkdir_tasks_child']=type(e).__name__+':'+str(e.errno)
out['tasks_subtree_control_+pids']=tryw('/sys/fs/cgroup/tasks/cgroup.subtree_control','+pids')
out['tasks_subtree_control_+memory']=tryw('/sys/fs/cgroup/tasks/cgroup.subtree_control','+memory')
# The same inside this exec's own delegated leaf: move a long-lived child
# into a sub-cgroup, then enable controllers so the leaf has no internal procs.
child=subprocess.Popen(['sleep','600'],start_new_session=True,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
os.mkdir(own+'/x')
out['move_child_into_leaf_x']=tryw(own+'/x/cgroup.procs',str(child.pid))
out['move_self_into_leaf_x']=tryw(own+'/x/cgroup.procs',str(os.getpid()))
out['leaf_subtree_control_+pids']=tryw(own+'/cgroup.subtree_control','+pids')
out['leaf_subtree_control_+memory']=tryw(own+'/cgroup.subtree_control','+memory')
out['leaf_subtree_control']=pathlib.Path(own+'/cgroup.subtree_control').read_text().strip()
print(json.dumps(out))
'''

AFTER = r'''
import json,os
print(json.dumps({'later_exec_ran':True,'later_exec_cgroup':next(l[3:] for l in open('/proc/self/cgroup').read().splitlines() if l.startswith('0::')),'uid_map':open('/proc/self/uid_map').read().split()}))
'''


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--fixture', required=True)
    args = p.parse_args()
    fixture = Path(args.fixture).resolve()
    control = Control(str(fixture / 'api.sock'), (fixture / 'token').read_text().strip())
    template = json.loads((fixture / 'template.json').read_text())['template_id']
    sandbox = control.create(template, {'vcpus': 2, 'memory_mib': 4096, 'disk_gib': 20, 'pids_max': 4096})
    try:
        print(json.dumps({'step': 'task attempts', 'sandbox_id': sandbox,
                          'value': control.execute(sandbox, ['python3', '-c', ATTEMPT])}), flush=True)
        print(json.dumps({'step': 'one more Lamp exec', 'value': control.execute(sandbox, ['python3', '-c', AFTER])}), flush=True)
    finally:
        control.delete(sandbox)
        print(json.dumps({'step': 'released', 'value': True}), flush=True)


if __name__ == '__main__':
    main()
