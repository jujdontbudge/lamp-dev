"""T20 end-to-end demonstration on lane LX, through Lamp's public HTTP API.

    python3 demo.py <part> <new run directory>     part: vm | gvisor

vm      caller-chosen CPU, memory, disk, process limit and an extra disk
        (cold boot at that shape); GPU and oversize requests refused before
        any work; memory fork, durable suspend/resume and the guard's
        audited network on a caller-shaped sandbox.
gvisor  the weaker gVisor fallback through lampd's trusted native route:
        an owner task runs labeled weaker_than_vm; public-repository CI and
        login/protected-connection tasks are refused with VM_REQUIRED.

At most two live sandboxes; the LX budget is checked before every create.
Nothing here measures time.
"""
import hashlib, json, os, secrets, sys, time, datetime
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from lamp_env import *

PART, ROOT = sys.argv[1], sys.argv[2]
TEMPLATE = 'tp_6b295ecbcad03c2a3b7370e577'
env = Env(ROOT)
api = None
live = []


def note(text, **kw):
    env.log('note', dict(text=text, **kw))


def expect_error(code, fn, *a, **kw):
    try:
        fn(*a, **kw)
    except APIError as e:
        assert e.code == code, (e.code, e.body)
        note('refused as expected', code=e.code, message=e.body.get('error', {}).get('message'))
        return e
    raise AssertionError('expected ' + code)


def rev(kind, ident):
    return api.get(kind, ident)['revision']


def wait_budget(at_most):
    deadline = time.monotonic() + 3600
    while True:
        b = budget()
        if b['firecracker'] <= at_most and b['available_mib'] >= 12 * 1024:
            return b
        assert time.monotonic() < deadline, b
        time.sleep(10)


def sandbox_of(r, parent=None):
    ids = [x['id'] for x in r.get('resource_ids') or [] if x['kind'] == 'sandbox' and x['id'] != parent]
    res = r.get('result') or {}
    return res.get('sandbox_id') if res.get('sandbox_id') not in (None, parent) else ids[-1]


def create(**fields):
    env.log('budget before create', wait_budget(1))
    sb = sandbox_of(api.op('sandbox_create', dict(template_id=TEMPLATE, **fields)))
    live.append(sb)
    return sb


def delete(sb):
    s = api.get('sandbox', sb)
    if s['state'] != 'deleted':
        api.op('sandbox_delete', dict(sandbox_id=sb, expected_revision=s['revision']))
    live.remove(sb)


EXIT70 = []


def ok(sb, script):
    """Every command here is safe to repeat. The guard collector's known
    intermittent exit 70 (T10/T19, also seen on prepared-image sandboxes) is
    logged as a failure and the command is issued once more; nothing else is
    retried."""
    code, out = api.run(sb, script)
    if code == 70:
        EXIT70.append(dict(sandbox=sb, script=script[:120], stdout=out[-300:]))
        note('collector exit 70 observed (failure kept); issuing the command once more', sandbox=sb)
        code, out = api.run(sb, script)
    assert code == 0, (code, out)
    return out


SHAPE = """echo "nproc=$(nproc)"; grep MemTotal /proc/meminfo;
for d in /sys/block/vd*; do echo "$(basename $d) $(( $(cat $d/size) * 512 / 1048576 )) MiB"; done;
echo "pids.max=$(cat /sys/fs/cgroup/tasks/pids.max)"; uname -r"""


# Forks paused children until the kernel refuses one, then reaps them all.
FORKS = """python3 - <<'EOF'
import os, signal
kids = []
try:
    while len(kids) < 1000:
        pid = os.fork()
        if pid == 0:
            signal.pause()
        kids.append(pid)
    err = 'none'
except OSError as e:
    err = e.strerror
current = open('/sys/fs/cgroup/tasks/pids.current').read().strip()
for k in kids:
    os.kill(k, signal.SIGKILL)
    os.waitpid(k, 0)
print('children_forked=%d refused_with=%r pids.current_at_refusal=%s' % (len(kids), err, current))
EOF"""


# A process that keeps a random nonce only in memory and answers it on a
# FIFO: a fork or resume that answers the same nonce restored that memory.
HOLDER = """[ -p /var/tmp/ask ] && echo holder already running && exit 0
mkfifo /var/tmp/ask
cat > /var/tmp/holder.py <<'PY'
import os
nonce = os.urandom(16).hex()
while True:
    open('/var/tmp/ask').read()
    with open('/var/tmp/say.tmp', 'w') as f: f.write(nonce + '\\n')
    os.rename('/var/tmp/say.tmp', '/var/tmp/say')
PY
setsid python3 /var/tmp/holder.py </dev/null >/dev/null 2>&1 &
sleep 1; echo holder started"""


def part_vm():
    # 1. Caller-chosen shape plus an extra disk: a cold boot at exactly that shape.
    shaped = dict(resources=dict(vcpus=4, memory_mib=3072, disk_gib=24, pids_max=200),
                  devices=[dict(id='scratch', type='disk', options=dict(size_mib=512))])
    a = create(**shaped)
    note('sandbox with caller-chosen resources and an extra disk', sandbox=api.get('sandbox', a))
    ok(a, SHAPE)
    ok(a, "dd if=/dev/urandom of=/tmp/blk bs=4096 count=1 2>/dev/null; dd if=/tmp/blk of=/dev/vdb bs=4096 count=1 conv=fsync 2>/dev/null;"
          " dd if=/dev/vdb bs=4096 count=1 2>/dev/null | cmp - /tmp/blk && echo extra-disk-roundtrip-ok; sha256sum /tmp/blk")
    # Process limit: 250 sleepers against pids.max=200.
    note('process limit observed', stdout=ok(a, FORKS))
    expect_error('DEVICE_UNAVAILABLE', api.op, 'sandbox_fork', dict(sandbox_id=a, expected_revision=rev('sandbox', a)))
    delete(a)

    # 2. Refusals before any work: GPU, too much memory, too many CPUs.
    expect_error('DEVICE_UNAVAILABLE', api.op, 'sandbox_create', dict(template_id=TEMPLATE, devices=[dict(id='gpu0', type='gpu')]))
    expect_error('NO_CAPACITY', api.op, 'sandbox_create', dict(template_id=TEMPLATE, resources=dict(vcpus=2, memory_mib=40960, disk_gib=20, pids_max=4096)))
    expect_error('NO_CAPACITY', api.op, 'sandbox_create', dict(template_id=TEMPLATE, resources=dict(vcpus=16, memory_mib=2048, disk_gib=20, pids_max=4096)))
    note('after refusals', budget=budget())

    # 3. A caller-shaped sandbox: fork, durable suspend/resume, guard and audit.
    b = create(resources=dict(vcpus=2, memory_mib=2048, disk_gib=20, pids_max=512))
    ok(b, SHAPE)
    ok(b, HOLDER)
    ask = "rm -f /var/tmp/say; echo q > /var/tmp/ask; for i in $(seq 50); do [ -f /var/tmp/say ] && break; sleep 0.1; done; cat /var/tmp/say; cat /proc/uptime"
    before = ok(b, ask)
    env.log('budget before fork (our parent counts as one)', wait_budget(1))
    r = api.op('sandbox_fork', dict(sandbox_id=b, expected_revision=rev('sandbox', b)))
    c = sandbox_of(r, b)
    live.append(c)
    child = ok(c, ask + "; cat /etc/machine-id")
    note('fork child answers from memory', parent=before, child=child, same_nonce=before.split()[0] == child.split()[0])
    ok(c, SHAPE)
    delete(c)
    api.op('sandbox_suspend', dict(sandbox_id=b, expected_revision=rev('sandbox', b)))
    s = api.get('sandbox', b)
    note('suspended', state=s['state'], budget=budget())
    env.log('budget before resume', wait_budget(1))
    api.op('sandbox_resume', dict(sandbox_id=b, expected_revision=s['revision']))
    after = ok(b, ask)
    note('resumed from durable capture', before=before, after=after, same_nonce=before.split()[0] == after.split()[0])
    note('guard network', stdout=ok(b, "wget -T5 -qO- http://example.com >/dev/null 2>&1 && echo egress-allowed || echo egress-refused"))
    note('sandbox record', sandbox=api.get('sandbox', b))
    delete(b)


def part_gvisor():
    expires = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(hours=1)).strftime('%Y-%m-%dT%H:%M:%SZ')
    keys = {}
    for k in ('public', 'private'):
        status, data = api.raw('POST', '/v1/keys', {'Idempotency-Key': secrets.token_hex(8)}, dict(expires_at=expires))
        assert status == 200, data
        keys[k] = data['result']['secret']
    cfg = env.private / 'gvisor.json'
    cfg.write_text(json.dumps(dict(runtime='gvisor', launcher=BIN + '/gvisor-launcher', rootfs='/var/lib/lamp/t20-gvisor-rootfs',
                                   allow_with_vm=True, jobs={hashlib.sha256(keys[v].encode()).hexdigest(): dict(job_id='jb_' + v, repository_visibility=v) for v in keys})))
    cfg.chmod(0o600)
    env.stop_daemon()
    env.start_daemon(['--native-fallback-file', str(cfg)])
    note('native fallback configured', runtime='gvisor', allow_with_vm='LX has KVM; the custodian enrolled the fallback explicitly for this demonstration')

    def task(token, body, label):
        c = Client(env.sock, token, env.log)
        status, data = c.raw('POST', '/internal/native-tasks', {'Idempotency-Key': secrets.token_hex(8)}, body)
        env.log(label, dict(status=status, response=data))
        return status, data

    t = dict(argv=['sh', '-c', 'uname -sr; id -u; dmesg | head -1; wget -T3 -qO- http://example.com >/dev/null 2>&1 || echo no-network; '
                   'sh -c "for i in \\$(seq 100); do sleep 3 >/dev/null 2>&1 & echo \\$i > /tmp/n; done" 2>/dev/null; '
                   'read n < /tmp/n; echo "background_processes_started_before_refusal=$n (pids_max=48)"'],
             memory_mib=256, pids_max=48, timeout_seconds=60)
    s, d = task(env.owner, t, 'owner gvisor task')
    assert s == 200 and d['isolation'] == 'weaker_than_vm', d
    s, d = task(keys['private'], t, 'private-repository CI gvisor task')
    assert s == 200, d
    s, d = task(keys['public'], t, 'public-repository CI gvisor task')
    assert s == 403 and d['error']['code'] == 'VM_REQUIRED', d
    s, d = task(env.owner, dict(t, login=True), 'login task')
    assert d['error']['code'] == 'VM_REQUIRED', d
    s, d = task(env.owner, dict(t, protected_connection=True), 'protected-connection task')
    assert d['error']['code'] == 'VM_REQUIRED', d


try:
    env.install()
    env.start_worker()
    env.start_daemon()
    api = env.api
    {'vm': part_vm, 'gvisor': part_gvisor}[PART]()
    note('part complete', part=PART, collector_exit70_failures=EXIT70)
finally:
    for sb in list(live):
        try:
            delete(sb)
        except Exception as e:
            note('cleanup failed', sandbox=sb, error=str(e))
    env.close()
