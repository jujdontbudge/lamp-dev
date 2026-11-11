"""T19 end-to-end demonstration on lane LX, through Lamp's public HTTP API.

    python3 demo.py <part> <new run directory>     part: A | B | C | D

A lifecycle (LIF-001/002/003), B forks and change sets (LIF-006),
C saved templates, recipes and warm pools (TPL-002/003, LIF-005),
D lasting storage (STO-001..005). Each part keeps at most two live sandboxes
and checks the LX budget before every create. Nothing here measures time.
"""
import base64, hashlib, json, os, re, secrets, sys, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from lamp_env import *

PART, ROOT = sys.argv[1], sys.argv[2]
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
        note('rejected as expected', code=e.code, message=e.body.get('error', {}).get('message'))
        return e
    raise AssertionError('expected ' + code)


def rev(kind, ident):
    return api.get(kind, ident)['revision']


def wait_budget(at_most):
    """Wait (up to an hour) until at most `at_most` Firecracker processes run
    on LX and at least 12 GiB is available; other tickets share the limit."""
    deadline = time.monotonic() + 3600
    while True:
        b = budget()
        if b['firecracker'] <= at_most and b['available_mib'] >= 12 * 1024:
            return b
        assert time.monotonic() < deadline, b
        time.sleep(10)


def create(**fields):
    b = wait_budget(1)
    env.log('budget before create', b)
    r = api.op('sandbox_create', fields)
    sb = sandbox_of(r)
    live.append(sb)
    return sb


def sandbox_of(r, parent=None):
    ids = [x['id'] for x in r.get('resource_ids') or [] if x['kind'] == 'sandbox' and x['id'] != parent]
    res = r.get('result') or {}
    return res.get('sandbox_id') if res.get('sandbox_id') not in (None, parent) else ids[-1]


def delete(sb):
    s = api.get('sandbox', sb)
    if s['state'] != 'deleted':
        api.op('sandbox_delete', dict(sandbox_id=sb, expected_revision=s['revision']))
    if sb in live:
        live.remove(sb)


def ok(sb, script):
    code, out = api.run(sb, script)
    assert code == 0, (code, out)
    return out


def wait_state(sb, states, timeout=300):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        s = api.get('sandbox', sb)
        if s['state'] in states:
            return s
        time.sleep(1)
    raise TimeoutError((sb, states, s['state']))


def fresh_eligibility(sb, after):
    """The first monitor observation started after `after` (RFC 3339)."""
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        e = api.get('sandbox', sb).get('suspend_eligibility') or {}
        if e.get('observed_at', '') > after:
            return e
        time.sleep(1)
    raise TimeoutError('no fresh idle observation')


def now():
    return time.strftime('%Y-%m-%dT%H:%M:%S.000000000Z', time.gmtime())


def upload(data, media='application/octet-stream'):
    sha = hashlib.sha256(data).hexdigest()
    b = api.op('blob_create', dict(sha256=sha, size_bytes=len(data), media_type=media))
    blob = b['resource_ids'][0]['id']
    for off in range(0, len(data), 1 << 20):
        chunk = data[off:off + (1 << 20)]
        api.op('blob_upload', dict(blob_id=blob, offset_bytes=off, data_base64=base64.b64encode(chunk).decode(),
                                   chunk_sha256=hashlib.sha256(chunk).hexdigest(), expected_revision=rev('blob', blob)), quiet=True)
    api.op('blob_finalize', dict(blob_id=blob, sha256=sha, size_bytes=len(data), expected_revision=rev('blob', blob)))
    return blob


def download(kind, ident):
    out, offset = bytearray(), 0
    while True:
        page = api.op(kind + '_download', {kind + '_id': ident, 'offset_bytes': offset}, quiet=True)
        chunk = base64.b64decode(page['data_base64'])
        assert hashlib.sha256(chunk).hexdigest() == page['chunk_sha256'].removeprefix('sha256:')
        out.extend(chunk)
        offset += len(chunk)
        if not chunk or page.get('eof') or offset >= page.get('size_bytes', 1 << 62):
            return bytes(out)


def attach(sb, kind, ident, mount, mode='read_write'):
    r = api.op('attachment_create', dict(sandbox_id=sb, resource_kind=kind, resource_id=ident, mount_path=mount, mode=mode, fork_policy='detach'))
    return r['resource_ids'][0]['id'] if r.get('resource_ids') else r['result']['attachment_id']


def detach(at):
    return api.op('attachment_delete', dict(attachment_id=at, expected_revision=rev('attachment', at)))


HARNESS = r'''
mkdir -p /tmp/lamp/harness-wait /var/tmp/work
rm -f /var/tmp/work/inbox /var/tmp/work/got; mkfifo /var/tmp/work/inbox
cat > /var/tmp/work/harness.py <<'PY'
import fcntl, json, os, secrets
nonce = secrets.token_hex(16)            # lives only in this process's memory
open('/var/tmp/work/nonce-shown', 'w').write(nonce)  # shown to the demo once, before suspend
marker = open('/tmp/lamp/harness-wait/harness-1', 'w')
fcntl.flock(marker, fcntl.LOCK_EX)       # live only while this process holds it
json.dump({'waiting_for': 'person_message'}, marker); marker.flush()
msg = open('/var/tmp/work/inbox').read()             # parked: blocked on the person
os.remove(marker.name); marker.close()
open('/var/tmp/work/got', 'w').write(json.dumps({'message': msg.strip(), 'nonce_from_memory': nonce, 'pid': os.getpid()}))
PY
setsid python3 /var/tmp/work/harness.py </dev/null >/dev/null 2>&1 &
sleep 1; cat /var/tmp/work/nonce-shown
'''


def part_a():
    """LIF-001/002/003: owner idle rules, automatic suspend, wake by exec
    across a full control restart, retention and hard expiry."""
    global api
    sb = create(template_id=TEMPLATE, lifecycle=dict(idle_action='suspend', idle_grace_seconds=600))
    s = api.get('sandbox', sb)
    note('sandbox ready with a per-sandbox lifecycle', lifecycle=s.get('lifecycle'), revision=s['revision'])
    expect_error('REVISION_CONFLICT', api.op, 'sandbox_update', dict(sandbox_id=sb, expected_revision=s['revision'] - 1, lifecycle=dict(idle_grace_seconds=15)))
    expect_error('INVALID_ARGUMENT', api.op, 'sandbox_update', dict(sandbox_id=sb, expected_revision=s['revision'], labels=dict(x='y')))

    # LIF-002 truth table: one workload at a time, judged by a fresh observation.
    rows = []
    def bg(name, body):
        return f"rm -f /var/tmp/stop.{name}; setsid sh -c '{body}' </dev/null >/dev/null 2>&1 &"
    until = lambda name: f'[ ! -e /var/tmp/stop.{name} ]'
    for name, start, stop in [
        ('nothing running', 'true', None),
        ('sleeping background process', bg('sleep', f'while {until("sleep")}; do sleep 0.5; done'), 'sleep'),
        ('dev server listening on :8000', bg('web', f'python3 -m http.server 8000 & p=$!; while {until("web")}; do sleep 0.5; done; kill $p'), 'web'),
        ('busy build loop', bg('busy', f'while {until("busy")}; do :; done'), 'busy'),
        ('harness parked on a person message', HARNESS, None),
        ('parked harness plus a sleeping process', bg('extra', f'while {until("extra")}; do sleep 0.5; done'), 'extra'),
        ('parked harness alone again', 'true', None),
    ]:
        ok(sb, start)
        time.sleep(6)  # let the exec's own session activity age out
        mark = now()
        e = fresh_eligibility(sb, mark)
        rows.append(dict(workload=name, eligible=e.get('eligible'), reasons=e.get('reasons')))
        if stop:
            ok(sb, f'touch /var/tmp/stop.{stop}; sleep 2')
    env.log('LIF-002 idle truth table', rows)
    nonce = ok(sb, 'cat /var/tmp/work/nonce-shown').strip()

    # LIF-001: lower the grace; the monitor suspends the parked sandbox itself.
    api.op('sandbox_update', dict(sandbox_id=sb, expected_revision=rev('sandbox', sb), lifecycle=dict(idle_grace_seconds=15)))
    s = wait_state(sb, ['suspended'])
    note('suspended automatically after the idle grace', state=s['state'], snapshot_id=s.get('snapshot_id'), budget=budget(), captures=[c['capture_id'] for c in env.captures()])
    snap = s['snapshot_id']
    note('suspend snapshot', snapshot=api.get('snapshot', snap))

    # Full restart of the control plane and the root worker while suspended.
    env.stop_daemon(); env.stop_worker(); env.start_worker(); env.start_daemon()
    api = env.api
    note('after restart', state=api.get('sandbox', sb)['state'])

    # An exec from the person wakes it; the parked harness gets the message
    # with its in-memory nonce intact (no cold boot).
    out = ok(sb, "echo 'hello from the person' > /var/tmp/work/inbox; sleep 1; cat /var/tmp/work/got; echo; cut -d' ' -f1 /proc/uptime")
    got = json.loads(out.splitlines()[0])
    assert got['nonce_from_memory'] == nonce, (got, nonce)
    s = api.get('sandbox', sb)
    note('woken by exec, RAM preserved', state=s['state'], generation=s.get('generation'), nonce_before=nonce, harness_reply=got, uptime_s=out.splitlines()[-1])
    note('suspend snapshot after resume', state=api.get('snapshot', snap)['state'])

    # LIF-003 retention: a suspended sandbox past its retention is deleted
    # and its saved state discarded.
    api.op('sandbox_update', dict(sandbox_id=sb, expected_revision=rev('sandbox', sb), lifecycle=dict(suspended_retention_seconds=20)))
    api.op('sandbox_suspend', dict(sandbox_id=sb, expected_revision=rev('sandbox', sb)))
    cap = [c['capture_id'] for c in env.captures() if c['source_sandbox_id'] == sb]
    s = wait_state(sb, ['deleted'], 120)
    deadline = time.monotonic() + 60
    while (left := [c['capture_id'] for c in env.captures() if c['source_sandbox_id'] == sb]) and time.monotonic() < deadline:
        time.sleep(2)
    note('retention expired', state=s['state'], capture_before=cap, captures_left=left)
    assert not left
    live.remove(sb)

    # LIF-003 hard expiry: suspends at the deadline; resuming needs new authority.
    expires = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(time.time() + 40))
    sb2 = create(template_id=TEMPLATE, lifecycle=dict(idle_action='keep', expires_at=expires))
    s = wait_state(sb2, ['suspended'], 180)
    note('hard deadline reached', expires_at=expires, state=s['state'])
    expect_error('TASK_AUTHORITY_EXPIRED', api.op, 'sandbox_resume', dict(sandbox_id=sb2, expected_revision=s['revision']))
    delete(sb2)


def part_b():
    """LIF-006: fork alternatives, keep the winner, merge back through git."""
    git = "git -c user.name=demo -c user.email=demo@localhost"
    p = create(template_id=TEMPLATE)
    ok(p, f"""mkdir -p /var/tmp/workspace && cd /var/tmp/workspace && git init -q -b main &&
printf 'def greet():\\n    return "hi"\\n' > app.py && printf '# demo\\n' > README.md &&
{git} add . && {git} commit -qm base && git rev-parse HEAD""")
    base = ok(p, 'git -C /var/tmp/workspace rev-parse HEAD').strip()

    r = api.op('sandbox_fork', dict(sandbox_id=p, expected_revision=rev('sandbox', p)))
    c1 = sandbox_of(r, p); live.append(c1)
    ok(c1, f"""cd /var/tmp/workspace && printf 'def greet():\\n    return "hello (alternative A)"\\n' > app.py && {git} commit -qam 'alternative A'""")
    api.op('sandbox_suspend', dict(sandbox_id=c1, expected_revision=rev('sandbox', c1)))
    note('alternative A suspended', state=api.get('sandbox', c1)['state'], budget=budget())

    wait_budget(1)
    r = api.op('sandbox_fork', dict(sandbox_id=p, expected_revision=rev('sandbox', p)))
    c2 = sandbox_of(r, p); live.append(c2)
    ok(c2, f"""cd /var/tmp/workspace && printf 'def greet():\\n    return "hey (alternative B)"\\n' > app.py &&
printf 'from app import greet\\nassert greet().startswith("hey")\\n' > test_app.py && {git} add . && {git} commit -qm 'alternative B'""")
    ok(c2, 'cd /var/tmp/workspace && python3 test_app.py')
    ok(p, f"""cd /var/tmp/workspace && printf '# demo\\n\\nParent work kept going.\\n' > README.md && {git} commit -qam 'parent docs'""")

    r = api.op('change_set_create', dict(source_session_id=c2, target_session_id=p, labels=dict(repository='/var/tmp/workspace')))
    cs = r['resource_ids'][0]['id']
    c = api.get('change_set', cs)
    note('winner B proposed', change_set=c)
    api.op('change_set_apply', dict(change_set_id=cs, expected_revision=c['revision']))
    note('winner B applied', change_set=api.get('change_set', cs),
         parent=ok(p, 'cd /var/tmp/workspace && git log --oneline && cat app.py README.md && python3 test_app.py && git status --short'))
    delete(c2)

    head = ok(p, 'git -C /var/tmp/workspace rev-parse HEAD').strip()
    r = api.op('change_set_create', dict(source_session_id=c1, target_session_id=p, labels=dict(repository='/var/tmp/workspace')))
    cs1 = r['resource_ids'][0]['id']
    c = api.get('change_set', cs1)
    note('loser A proposed', change_set=c)
    api.op('change_set_apply', dict(change_set_id=cs1, expected_revision=c['revision']))
    c = api.get('change_set', cs1)
    after = ok(p, 'cd /var/tmp/workspace && git rev-parse HEAD && git status --short && cat app.py')
    note('conflict recorded, parent untouched', change_set=c, head_before=head, parent=after)
    assert after.startswith(head) and c['state'] == 'conflicted'
    api.op('change_set_reject', dict(change_set_id=cs1, expected_revision=c['revision']))
    note('loser rejected', state=api.get('change_set', cs1)['state'], base=base)
    delete(c1); delete(p)


TOOL = b'#!/bin/sh\necho "greet-tool v1: hello, $1"\n'


def part_c():
    """TPL-002 save installed tools, clean rebuild when tainted; TPL-003
    recipes; LIF-005 warm pools of never-used, signed-out sandboxes."""
    s = create(template_id=TEMPLATE)
    ok(s, "mkdir -p /var/tmp/tools/bin && cat > /var/tmp/tools/bin/greet <<'EOF'\n" + TOOL.decode() + "EOF\nchmod 755 /var/tmp/tools/bin/greet && echo private > /var/tmp/not-selected")
    r = api.op('sandbox_save_template', dict(sandbox_id=s, name='greet-tools', selected_paths=['/var/tmp/tools'], expected_revision=rev('sandbox', s)))
    tp1 = r['result']['template_id']
    note('template saved from a clean sandbox', template=api.get('template', tp1))

    # Taint: the source now holds bytes Lamp must not trust; a copy is refused.
    ok(s, 'echo injected > /var/tmp/tools/bin/canary')
    api.op('sandbox_update', dict(sandbox_id=s, expected_revision=rev('sandbox', s), capture_trust='rebuild_required'))
    expect_error('CLEAN_REBUILD_REQUIRED', api.op, 'sandbox_save_template', dict(sandbox_id=s, name='greet-tools', selected_paths=['/var/tmp/tools'], expected_revision=rev('sandbox', s)))
    tool = upload(TOOL)
    plan = dict(files=[dict(path='/var/tmp/tools/bin/greet', blob_id=tool, sha256=hashlib.sha256(TOOL).hexdigest(), mode='0755')],
                commands=[['sh', '-c', 'echo rebuilt-from-pinned-inputs > /var/tmp/tools/BUILD']])
    pb = upload(json.dumps(plan).encode(), 'application/json')
    r = api.op('sandbox_save_template', dict(sandbox_id=s, name='greet-tools', reconstruction_blob_id=pb, expected_revision=rev('sandbox', s)))
    tp = r['result']['template_id']
    note('rebuilt clean from pinned inputs', template=api.get('template', tp), plan=plan)
    delete(s)

    r = api.op('recipe_create', dict(name='greet', template_id=tp, auth_mode='none', lifecycle=dict(idle_action='suspend', idle_grace_seconds=300)))
    rc1 = r['resource_ids'][0]['id']
    v1 = api.get('recipe', rc1)
    expect_error('REVISION_CONFLICT', api.op, 'recipe_update', dict(recipe_id=rc1, expected_revision=v1['revision'] + 5, lifecycle=dict(idle_grace_seconds=120)))
    r = api.op('recipe_update', dict(recipe_id=rc1, expected_revision=v1['revision'], lifecycle=dict(idle_action='suspend', idle_grace_seconds=120)))
    rc2 = (r.get('result') or {}).get('recipe_id') or r['resource_ids'][-1]['id']
    v2 = api.get('recipe', rc2)
    note('recipe versions are immutable', v1=api.get('recipe', rc1), v2=v2, list=api.op('recipe_list', {}, quiet=True)['items'])

    s = create(recipe_id=rc2)
    sb = api.get('sandbox', s)
    out = ok(s, '/var/tmp/tools/bin/greet person; cat /var/tmp/tools/BUILD; ls /var/tmp/tools/bin; test ! -e /var/tmp/not-selected && echo no-unselected-files')
    note('sandbox from the recipe runs the tool with no install step', sandbox=dict(recipe_id=sb.get('recipe_id'), template_id=sb.get('template_id'), lifecycle=sb.get('lifecycle')), output=out)
    assert 'canary' not in out
    delete(s)

    # The pool holds one entry and refills after a claim: two of ours at once.
    wait_budget(0)
    r = api.op('warm_pool_create', dict(recipe_id=rc2, target_size=1, max_size=1, match_digest=v2['digest']))
    wp = r['resource_ids'][0]['id']
    deadline = time.monotonic() + 300
    while api.get('warm_pool', wp).get('ready_count', 0) < 1:
        assert time.monotonic() < deadline
        time.sleep(2)
    note('pool filled', pool=api.get('warm_pool', wp), budget=budget())
    r = api.op('sandbox_create', dict(recipe_id=rc2))
    claimed = sandbox_of(r); live.append(claimed)
    c = api.get('sandbox', claimed)
    out = ok(claimed, '/var/tmp/tools/bin/greet pool; ls -A /var/tmp; env | grep -ci token || true')
    note('claimed a never-used, signed-out sandbox', sandbox=dict(id=claimed, warm_pool_id=c.get('warm_pool_id'), auth_mode=c.get('auth_mode'), recipe_id=c.get('recipe_id')), output=out)
    deadline = time.monotonic() + 300
    while api.get('warm_pool', wp).get('ready_count', 0) < 1:
        assert time.monotonic() < deadline
        time.sleep(2)
    note('pool refilled', pool=api.get('warm_pool', wp), budget=budget())
    expect_error('RESOURCE_IN_USE', api.op, 'recipe_delete', dict(recipe_id=rc2, expected_revision=rev('recipe', rc2)))
    delete(claimed)
    api.op('warm_pool_delete', dict(warm_pool_id=wp, expected_revision=rev('warm_pool', wp)))
    for rc in (rc2, rc1):
        api.op('recipe_delete', dict(recipe_id=rc, expected_revision=rev('recipe', rc)))
    roots = [r for t in (tp, tp1) for r in api.get('template', t)['root_objects']]
    for t in (tp, tp1):
        api.op('template_delete', dict(template_id=t, expected_revision=rev('template', t)))
    # Deleted templates' saved state is discarded by the monitor.
    deadline = time.monotonic() + 60
    while (left := [c['capture_id'] for c in env.captures() if c['capture_id'] in roots]) and time.monotonic() < deadline:
        time.sleep(2)
    note('cleaned up', budget=budget(), template_captures=roots, captures_left=left)
    assert not left


def part_d():
    """STO-001..005: caches, named volumes, artifacts, owner-only, backups."""
    wait_budget(0)
    a = create(template_id=TEMPLATE)
    b = create(template_id=TEMPLATE)

    # STO-001 shared cache with generations.
    cache = api.op('cache_create', dict(name='pip', trust_class='trusted'))['resource_ids'][0]['id']
    c0 = api.get('cache', cache)
    at = attach(a, 'cache', cache, '/var/tmp/cache')
    sums_a = ok(a, 'mkdir -p /var/tmp/cache/wheels && head -c 300000 /dev/urandom > /var/tmp/cache/wheels/pkg.whl && echo v1 > /var/tmp/cache/index && cd /var/tmp/cache && sha256sum wheels/pkg.whl index')
    api.op('cache_publish', dict(cache_id=cache, base_digest=c0['generation_digest'], expected_revision=rev('cache', cache)))
    c1 = api.get('cache', cache)
    expect_error('REVISION_CONFLICT', api.op, 'cache_publish', dict(cache_id=cache, base_digest=c0['generation_digest'], expected_revision=c1['revision']))
    detach(at)
    at = attach(b, 'cache', cache, '/var/tmp/cache', 'read_only')
    sums_b = ok(b, 'cd /var/tmp/cache && sha256sum wheels/pkg.whl index')
    note('cache reused read-only in another sandbox', generation_before=c0['generation_digest'], generation_after=c1['generation_digest'], writer=sums_a, reader=sums_b)
    assert sums_a.split() == sums_b.split()
    detach(at)

    # STO-002 shared volume, two writers, merge then conflict.
    vol = api.op('volume_create', dict(name='notes', mode='shared_files'))['resource_ids'][0]['id']
    xa, xb = attach(a, 'volume', vol, '/var/tmp/data'), attach(b, 'volume', vol, '/var/tmp/data')
    ok(a, 'echo from-a > /var/tmp/data/a.txt; echo shared-v1 > /var/tmp/data/shared.txt')
    ok(b, 'echo from-b > /var/tmp/data/b.txt')
    detach(xa); detach(xb)
    xa, xb = attach(a, 'volume', vol, '/var/tmp/data'), attach(b, 'volume', vol, '/var/tmp/data')
    merged = ok(b, 'cd /var/tmp/data && ls && cat a.txt b.txt shared.txt')
    note('two attachments merged', volume=api.get('volume', vol), seen_by_b=merged)
    ok(a, 'echo edit-from-a > /var/tmp/data/shared.txt')
    ok(b, 'echo edit-from-b > /var/tmp/data/shared.txt')
    detach(xa)
    e = expect_error('RESOURCE_IN_USE', detach, xb)
    note('conflicting edit kept attached, not overwritten', attachment=api.get('attachment', xb))
    ok(b, 'echo edit-from-a > /var/tmp/data/shared.txt')
    detach(xb)
    ex = api.op('volume_create', dict(name='db', mode='exclusive_block'))['resource_ids'][0]['id']
    w1 = attach(a, 'volume', ex, '/var/tmp/db')
    expect_error('RESOURCE_IN_USE', attach, b, 'volume', ex, '/var/tmp/db')
    ok(a, 'echo row > /var/tmp/db/table')
    detach(w1)

    # STO-003 CI output outlives its job.
    sha = ok(a, 'mkdir -p /var/tmp/out && head -c 1048576 /dev/urandom > /var/tmp/out/report.bin && sha256sum /var/tmp/out/report.bin').split()[0]
    r = api.op('sandbox_file_transfer', dict(sandbox_id=a, path='/var/tmp/out/report.bin', direction='download', expected_revision=rev('sandbox', a)))
    blob = r['result']['blob_id']
    ar = api.op('artifact_create', dict(job_id='job_ci_1', download_blob_id=blob))['resource_ids'][0]['id']
    delete(a)
    data = download('artifact', ar)
    note('artifact downloaded after its job sandbox was deleted', artifact=api.get('artifact', ar), guest_sha256=sha, downloaded_sha256=hashlib.sha256(data).hexdigest(), bytes=len(data))
    assert hashlib.sha256(data).hexdigest() == sha
    part = upload(b'half a log\n')
    man = upload(json.dumps(dict(format='lamp.artifact-manifest/v1', entries=[dict(blob_id=part, sha256=hashlib.sha256(b'half a log\n').hexdigest(), size_bytes=11),
                                                                         dict(blob_id='bl_' + secrets.token_hex(13), sha256='0' * 64, size_bytes=4096)])).encode(), 'application/json')
    ar2 = api.op('artifact_create', dict(job_id='job_ci_2', manifest_blob_id=man))['resource_ids'][0]['id']
    note('partial output is marked incomplete', artifact=api.get('artifact', ar2))

    # STO-004 owner-only: a real team member of the same fleet sees nothing.
    fleet = api.op('fleet_get', dict(fleet_id='fl_local'), quiet=True)['data']
    api.op('fleet_update', dict(fleet_id='fl_local', mode='team', expected_revision=fleet['revision']))
    pr = api.op('principal_create', dict(issuer='https://demo.invalid', subject='member-1', display_name='member'))['resource_ids'][0]['id']
    api.op('membership_create', dict(principal_id=pr, role='member'))
    rows = {}
    for k in ('cache', 'volume', 'artifact', 'blob', 'backup'):
        try:
            rows[k] = [x['id'] for x in env.member_api.op(k + '_list', {}, quiet=True)['items']]
        except APIError as e:
            rows[k] = e.code
    note('member lists the owner\'s lasting storage', member=pr, rows=rows, owner_rows={k: len(api.op(k + '_list', {}, quiet=True)['items']) for k in ('cache', 'volume', 'artifact', 'blob')})
    for kind, ident in (('artifact', ar), ('volume', vol), ('cache', cache)):
        try:
            env.member_api.op(kind + '_get', {kind + '_id': ident}, quiet=True)
            raise AssertionError('member read ' + ident)
        except APIError as e:
            note('member get of owner storage', kind=kind, code=e.code)
    try:
        env.member_api.op('artifact_download', dict(artifact_id=ar, offset_bytes=0), quiet=True)
        raise AssertionError('member downloaded the artifact')
    except APIError as e:
        note('member download of owner artifact', code=e.code)
    expect_error('SCOPE_DENIED', api.op, 'volume_create', dict(name='public', mode='shared_files', visibility='shared'))

    # STO-005 encrypted backup and restore.
    bk = api.op('backup_create', dict(resource_kind='volume', resource_id=vol))['resource_ids'][0]['id']
    note('backup sealed', backup=api.get('backup', bk))
    api.op('volume_delete', dict(volume_id=vol, expected_revision=rev('volume', vol)))
    api.op('backup_restore', dict(backup_id=bk, new_authority_epoch=2, expected_revision=rev('backup', bk)))
    restored = api.get('volume', vol)
    x = attach(b, 'volume', vol, '/var/tmp/restored')
    out = ok(b, 'cd /var/tmp/restored && cat a.txt b.txt shared.txt')
    note('volume restored from backup', volume=restored, contents=out)
    detach(x)
    bk2 = api.op('backup_create', dict(resource_kind='cache', resource_id=cache))['resource_ids'][0]['id']
    f = env.private / 'state' / 'backups' / (bk2 + '.lbk')
    raw = bytearray(f.read_bytes()); raw[len(raw) // 2] ^= 1; f.write_bytes(bytes(raw))
    head = bytes(raw[:64])
    note('one byte flipped in the stored backup', file=str(f), plaintext_visible=b'from-a' in raw or b'v1\n' in raw)
    api.op('cache_delete', dict(cache_id=cache, expected_revision=rev('cache', cache)))
    expect_error('DATA_UNAVAILABLE', api.op, 'backup_restore', dict(backup_id=bk2, new_authority_epoch=3, expected_revision=rev('backup', bk2)))
    delete(b)


env.install(); env.start_worker(); env.start_daemon()
api = env.api
try:
    if PART == 'A':
        part_a()
    elif PART == 'B':
        part_b()
    elif PART == 'C':
        part_c()
    elif PART == 'D':
        part_d()
    note('part complete', part=PART)
finally:
    for sb in list(live):
        try:
            delete(sb)
        except Exception as e:
            env.log('cleanup failed', dict(sandbox=sb, error=str(e)))
    env.close()
