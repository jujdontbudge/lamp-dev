# GitLab half of the T29 demonstration. Run by t29_demo_test.go's "gitlab"
# stage with LAMP_SOCKET (this run's Lamp API) and LAMP_GITLAB_RECIPE set.
# One project, two jobs: one through the Custom executor, one through the
# Fleeting plugin (instance executor). Each gets its own fresh Lamp sandbox.
import json, os, signal, subprocess, sys, tempfile, time, urllib.request
from pathlib import Path

signal.signal(signal.SIGTERM, lambda *_: sys.exit(1))  # still run the cleanup below
base = Path.home() / 'lamp-t29'
api = 'http://10.219.29.1:3934/api/v4'
token = (base / 'gitlab-token').read_text().strip()
bin = base / 'bin'


def call(method, path, body=None, raw=False):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(api + path, data=data, method=method, headers={'PRIVATE-TOKEN': token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=60) as r:
        b = r.read()
        return b.decode(errors='replace') if raw else (json.loads(b) if b else None)


def log(*a):
    print('gitlab:', *a, flush=True)


projects = call('GET', '/projects?search=zlib-demo&owned=true')
project = projects[0] if projects else call('POST', '/projects', {'name': 'zlib-demo', 'visibility': 'private', 'initialize_with_readme': True})
pid = project['id']
log('project', project['path_with_namespace'], 'id', pid, 'visibility', project['visibility'])

runners, ids = {}, []
for tag in ('lamp-custom', 'lamp-fleeting'):
    r = call('POST', '/user/runners', {'runner_type': 'project_type', 'project_id': pid, 'tag_list': [tag], 'description': 't29 ' + tag})
    runners[tag] = r['token']
    ids.append(r['id'])

work = Path(tempfile.mkdtemp(prefix='t29-gitlab-'))
os.chmod(work, 0o700)
cfg = work / 'config.toml'
ex = str(bin / 'lamp-gitlab-executor')
cfg.write_text(f'''concurrent = 1
check_interval = 3
[[runners]]
  name = "lamp-custom"
  url = "http://10.219.29.1:3934"
  token = "{runners['lamp-custom']}"
  executor = "custom"
  builds_dir = "/var/tmp/lamp-ci/builds"
  [runners.custom]
    config_exec = "{ex}"
    config_args = ["config"]
    prepare_exec = "{ex}"
    prepare_args = ["prepare"]
    run_exec = "{ex}"
    run_args = ["run"]
    cleanup_exec = "{ex}"
    cleanup_args = ["cleanup"]
[[runners]]
  name = "lamp-fleeting"
  url = "http://10.219.29.1:3934"
  token = "{runners['lamp-fleeting']}"
  executor = "instance"
  shell = "bash"
  [runners.autoscaler]
    plugin = "{bin / 'lamp-fleeting'}"
    capacity_per_instance = 1
    max_use_count = 1
    max_instances = 1
    [runners.autoscaler.plugin_config]
      recipe_id = "{os.environ['LAMP_GITLAB_RECIPE']}"
      callback_addr = "10.219.29.1:3935"
      state_dir = "{work / 'fleeting'}"
      max_size = 1
    [runners.autoscaler.connector_config]
      use_static_credentials = false
    [[runners.autoscaler.policy]]
      idle_count = 0
      idle_time = "1m"
''')
cfg.chmod(0o600)

ci = '''.build: &build
  script:
    - uname -a
    - test ! -e /var/tmp/lamp-previous-job && touch /var/tmp/lamp-previous-job
    - cc -O2 -o hello -x c - <<< 'int main(){return 0;}' && ./hello && echo "built in $(hostname)" | tee out.txt
  artifacts:
    paths: [out.txt]
custom-executor:
  <<: *build
  tags: [lamp-custom]
fleeting:
  <<: *build
  tags: [lamp-fleeting]
'''
(work / 'wd').mkdir()
runner_log = (work / 'runner.log').open('w')
runner = subprocess.Popen([str(bin / 'gitlab-runner'), 'run', '--config', str(cfg), '--working-directory', str(work / 'wd')],
                          stdout=runner_log, stderr=subprocess.STDOUT, env=dict(os.environ, PATH=f"{bin}:{os.environ['PATH']}"))
ok = False
try:
    action = 'update'
    try:
        call('GET', f'/projects/{pid}/repository/files/.gitlab-ci.yml?ref=main')
    except urllib.error.HTTPError:
        action = 'create'
    commit = call('POST', f'/projects/{pid}/repository/commits', {'branch': 'main', 'commit_message': 't29 demonstration ' + time.strftime('%H:%M:%S'),
                                                                 'actions': [{'action': action, 'file_path': '.gitlab-ci.yml', 'content': ci}]})
    pipeline = None
    for _ in range(60):
        ps = call('GET', f"/projects/{pid}/pipelines?sha={commit['id']}")
        if ps:
            pipeline = ps[0]
            break
        time.sleep(2)
    log('pipeline', pipeline['id'], 'for commit', commit['short_id'])
    deadline = time.time() + 40 * 60
    while True:
        jobs = call('GET', f"/projects/{pid}/pipelines/{pipeline['id']}/jobs")
        if all(j['status'] in ('success', 'failed', 'canceled', 'skipped') for j in jobs) or time.time() > deadline or runner.poll() is not None:
            break
        time.sleep(5)
    ok = True
    for j in jobs:
        trace = call('GET', f"/projects/{pid}/jobs/{j['id']}/trace", raw=True).splitlines()
        art = [a['filename'] for a in j.get('artifacts', [])]
        log(f"job {j['name']} id={j['id']} status={j['status']} runner={(j.get('runner') or {}).get('description')} artifacts={art}")
        log('  trace tail:\n    ' + '\n    '.join(l[:200] for l in trace[-14:]))
        ok = ok and j['status'] == 'success'
finally:
    runner.send_signal(signal.SIGQUIT)  # graceful: the Fleeting plugin's Shutdown deletes its instances
    try:
        runner.wait(timeout=180)
    except subprocess.TimeoutExpired:
        runner.kill()
    for i in ids:
        call('DELETE', f'/runners/{i}')
    left = call('GET', f'/projects/{pid}/runners')
    log('runners left on project:', len(left))
    log('runner log tail:\n    ' + '\n    '.join(l[:200] for l in (work / 'runner.log').read_text().splitlines()[-25:]))
sys.exit(0 if ok else 1)
