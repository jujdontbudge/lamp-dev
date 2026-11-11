"""Rootless named toolchains in a ready VM; never host packages or Docker."""
import base64
import json
import secrets
import time

from .control import KitError

COMMANDS = {
    'python': ['python3', 'pip3'], 'node': ['node', 'npm'], 'go': ['go'],
    'rust': ['rustc', 'cargo'], 'c': ['cc', 'gcc', 'g++', 'make'],
    'git': ['git'], 'docker': ['docker', 'dockerd', 'containerd', 'runc'],
    'browser': ['google-chrome', 'chromium', 'chromium-browser'],
}

PACKAGES = {
    'python': ['python3', 'python3-venv', 'python3-pip'],
    'node': ['nodejs', 'npm'], 'go': ['golang-go'],
    'rust': ['rustc', 'cargo'], 'c': ['build-essential'],
    'git': ['git', 'ca-certificates'], 'docker': ['docker.io', 'busybox-static'],
}


def start_install(control, sandbox, names):
    if not isinstance(names, list) or not names or len(names) > 10 or any(n not in (*PACKAGES, 'browser') for n in names):
        raise KitError('UNSUPPORTED_TOOLCHAIN')
    generation = control.sandbox(sandbox)['generation']
    job = secrets.token_hex(16)
    packages = sorted({p for n in names if n in PACKAGES for p in PACKAGES[n]})
    # Public exec maps namespace root to a subordinate guest UID. Install into a
    # user prefix without bypassing the audit collector or adding setuid helpers.
    script = '''import json,subprocess,os,pathlib,shutil,glob,time
root=pathlib.Path('/tmp/lamp-toolchains');root.mkdir(mode=0o700,exist_ok=True)
cache=root/'cache'/JOB;cache.mkdir(parents=True,exist_ok=True)
archives=cache/'archives';archives.mkdir(exist_ok=True)
(archives/'partial').mkdir(exist_ok=True)
env=dict(os.environ,DEBIAN_FRONTEND='noninteractive')
needed=[];reused=[];cached=[];phase='packages'
def run(argv): subprocess.run(argv,check=True,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=600,env=env,cwd=archives)
opts=['-o','APT::Sandbox::User=root','-o','Acquire::ForceIPv4=true','-o','Debug::NoLocking=true','-o','Dir::Cache='+str(cache)]
'''.replace('JOB', repr(job))
    if packages:
        script += 'packages=' + repr(packages) + '\n'
        script += '''# Reuse tools/cached packages from the authenticated prepared template.
# Each job has a fresh download directory; failed partial downloads never enter
# a later job's extraction set. Online apt retains its signed-index/hash checks.
needed=[];reused=[];cached=[]
for package in packages:
 candidates=sorted(pathlib.Path('/var/cache/apt/archives').glob(package+'_*.deb'))
 if candidates:
  shutil.copyfile(candidates[-1],archives/candidates[-1].name);cached.append(package)
 else:
  query=subprocess.run(['dpkg-query','-W','-f','${db:Status-Status}',package],stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
  if query.returncode==0 and query.stdout==b'installed':reused.append(package)
  else:needed.append(package)
if needed:
 run(['apt-get']+opts+['--download-only','-y','--no-install-recommends','install']+needed)
 run(['apt-get']+opts+['download']+needed)
for package in archives.glob('*.deb'):run(['dpkg-deb','--extract',str(package),str(root)])
'''
    if 'browser' in names:
        script += '''if not any(shutil.which(n) for n in ('google-chrome','chromium','chromium-browser')):
 import urllib.request
 # Browser shared-library prerequisites must be in the prepared template.
 p=archives/'lamp-browser.deb'
 with urllib.request.urlopen('https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb',timeout=40) as r,p.open('wb') as f:
  shutil.copyfileobj(r,f)
 run(['dpkg-deb','--extract',str(p),str(root)])
'''
    script += '''bindir=root/'bin';bindir.mkdir(exist_ok=True)
# Return a usable PATH rather than modifying shell files or claiming a system
# install. Wrappers scope the extracted shared libraries to these guest tools.
for directory in ('usr/bin','usr/sbin'):
 for binary in (root/directory).glob('*'):
  if binary.is_file() and os.access(binary,os.X_OK):
   target=bindir/binary.name
   target.unlink(missing_ok=True)
   target.write_text('#!/bin/sh\\nexport PATH='+str(root/'usr/bin')+':"$PATH"\\nexport LD_LIBRARY_PATH='+str(root/'usr/lib/x86_64-linux-gnu')+':'+str(root/'usr/lib')+'\\nexec '+str(binary)+' "$@"\\n')
   target.chmod(0o700)
'''
    commands = sorted({command for name in names for command in COMMANDS[name]})
    script += 'commands=' + repr(commands) + '\n'
    script += '''for name in commands:
 target=bindir/name
 binary=shutil.which(name)
 if not target.exists() and binary:target.symlink_to(binary)
'''
    if 'docker' in names:
        script += '''# Rootless Docker must work inside the existing guest namespace. No host
# daemon/socket, privileged helper, bridge, iptables or owner resource is used.
phase='docker'
state=pathlib.Path('/tmp/lamp-kit-docker');state.mkdir(mode=0o700,exist_ok=True)
runtime_log=(state/'runtime.log').open('ab',buffering=0);os.chmod(state/'runtime.log',0o600)
sock='unix:///tmp/lamp-kit-docker/docker.sock'
docker_env={'PATH':str(bindir)+':/usr/sbin:/usr/bin:/sbin:/bin','HOME':str(state),
 'XDG_RUNTIME_DIR':str(state)}
# Guest init prepares the hard-coded shim directory /run/containerd. Keep
# containerd's data, state and socket in our private guest prefix; no root
# helper or host containerd socket is involved.
# Docker's plugin manager hard-codes /run/docker/plugins, which the mapped
# task cannot create under guest-root-owned /run. This job owns its own user and
# mount namespace, so give it a private tmpfs /run (visible to nothing else).
phase='runtime-dir'
if not pathlib.Path('/run/docker').is_dir():
 subprocess.run(['mount','-t','tmpfs','-o','mode=755','lamp-kit-run','/run'],check=True,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=runtime_log)
 for d in ('/run/containerd','/run/docker'):os.makedirs(d,mode=0o711,exist_ok=True)
phase='containerd'
containerd=None
containerd_socket=state/'containerd.sock'
if not containerd_socket.exists():
 config=state/'containerd.toml'
 generated=subprocess.run([str(bindir/'containerd'),'config','default'],check=True,
  stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,env=docker_env)
 config.write_bytes(generated.stdout);config.chmod(0o600)
 containerd=subprocess.Popen([str(bindir/'containerd'),'--config='+str(config),
  '--root='+str(state/'containerd-data'),'--state='+str(state/'containerd-run'),
  '--address='+str(containerd_socket),'--log-level=error'],
  stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=runtime_log,start_new_session=True,env=docker_env)
while not containerd_socket.exists():
 if containerd is not None and containerd.poll() is not None:raise RuntimeError('guest containerd exited')
 time.sleep(.25)
phase='docker'
daemon=None
if not (state/'docker.sock').exists():
 daemon=subprocess.Popen([str(bindir/'dockerd'),'--rootless','--group=0','--host='+sock,'--data-root='+str(state/'data'),
  '--containerd='+str(containerd_socket),'--exec-root='+str(state/'exec'),'--pidfile='+str(state/'daemon.pid'),
  '--bridge=none','--iptables=false','--ip6tables=false','--storage-driver=vfs'],
  stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=runtime_log,start_new_session=True,env=docker_env)
while True:
 try:
  probe=subprocess.run([str(bindir/'docker'),'--host='+sock,'info'],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=10,env=docker_env)
  if probe.returncode==0:break
 except subprocess.TimeoutExpired:
  pass  # A live daemon still starting is pending, not a failed speed target.
 if daemon is not None and daemon.poll() is not None:raise RuntimeError('rootless guest Docker exited')
 if containerd is not None and containerd.poll() is not None:raise RuntimeError('guest containerd exited')
 time.sleep(.25)
'''
    script += 'result={"installed":' + json.dumps(names) + ',"reused_packages":reused,"cached_packages":cached,"downloaded_packages":needed,"boundary":"vm","path":str(bindir),"docker_host":"unix:///tmp/lamp-kit-docker/docker.sock" if "docker" in ' + repr(names) + ' else None}\n'
    runner = '''import base64,fcntl,json,os,pathlib,subprocess,sys
root=pathlib.Path('/tmp/lamp-kit-jobs')
scope={}
try:
 if _NEEDS_NAMESPACE_ and sys.argv[1:] != ['--in-namespace']:
  # Docker needs mounts owned by its own user namespace, never guest/host root.
  # Single-UID mapping needs no privileged helper and preserves no_new_privs.
  scope['phase']='namespace'
  with (root/(JOB+'.bootstrap.log')).open('xb') as log:
   os.chmod(log.name,0o600)
   child=subprocess.run(['/usr/bin/unshare','--user','--map-root-user','--mount','--propagation=private','--',sys.executable,__file__,'--in-namespace'],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=log)
  completed=root/(JOB+'.result.json')
  value=json.loads(completed.read_text()) if completed.exists() else {'state':'failed','failure_code':'KIT_NAMESPACE_FAILED','phase':'namespace','exit_code':child.returncode}
 else:
  with (root/'install.lock').open('a') as lock:
   fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
   exec(base64.b64decode(SCRIPT),scope)
   value={'state':'ready','result':scope['result']}
except BaseException as error:
 kind=type(error).__name__
 if kind not in ('TimeoutExpired','CalledProcessError','RuntimeError','FileNotFoundError','PermissionError','ValueError','OSError'):kind='InstallError'
 value={'state':'failed','failure_code':'KIT_INSTALL_FAILED','phase':scope.get('phase','setup'),'exception_class':kind}
p=root/(JOB+'.result.new')
with p.open('x') as out:
 json.dump(value,out);out.flush();os.fsync(out.fileno())
os.replace(p,root/(JOB+'.result.json'))
'''.replace('_NEEDS_NAMESPACE_', repr('docker' in names)).replace('JOB', repr(job)).replace('SCRIPT', repr(base64.b64encode(script.encode()).decode()))
    launcher = '''import base64,json,pathlib,subprocess,sys
root=pathlib.Path('/tmp/lamp-kit-jobs');root.mkdir(mode=0o700,exist_ok=True)
p=root/(JOB+'.py');p.write_bytes(base64.b64decode(RUNNER));p.chmod(0o600)
(root/(JOB+'.meta.json')).write_text(json.dumps({'generation':GENERATION}))
subprocess.Popen([sys.executable,str(p)],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=True)
print(json.dumps({'state':'installing','job_id':JOB,'sandbox_id':SANDBOX,'generation':GENERATION}))
'''.replace('JOB', repr(job)).replace('GENERATION', repr(generation)).replace('SANDBOX', repr(sandbox)).replace('RUNNER', repr(base64.b64encode(runner.encode()).decode()))
    return control.execute(sandbox, ['python3', '-c', launcher])


def install_status(control, sandbox, job):
    if not isinstance(job, str) or len(job) != 32 or any(c not in '0123456789abcdef' for c in job):
        raise KitError('INVALID_ARGUMENT')
    generation = control.sandbox(sandbox)['generation']
    script = '''import json,pathlib
root=pathlib.Path('/tmp/lamp-kit-jobs');job=JOB
metadata=json.loads((root/(job+'.meta.json')).read_text())
if metadata['generation']!=GENERATION:
 result={'state':'failed','failure_code':'GENERATION_CHANGED'}
else:
 p=root/(job+'.result.json')
 result=json.loads(p.read_text()) if p.exists() else {'state':'installing'}
result.update(job_id=job,sandbox_id=SANDBOX,generation=GENERATION)
print(json.dumps(result))
'''.replace('JOB', repr(job)).replace('GENERATION', repr(generation)).replace('SANDBOX', repr(sandbox))
    return control.execute(sandbox, ['python3', '-c', script])


def install(control, sandbox, names):
    """Blocking convenience for operators; polling observes, never replays work."""
    job = start_install(control, sandbox, names)
    while job['state'] == 'installing':
        time.sleep(1)
        job = install_status(control, sandbox, job['job_id'])
    if job['state'] != 'ready':
        raise KitError('KIT_INSTALL_FAILED')
    return job['result']
