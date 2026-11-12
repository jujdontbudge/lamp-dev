#!/usr/bin/env python3
"""One headless fleet journey on the owner's M5, LX and Air; no benchmark.

Uses disposable ticket processes and a prebuilt template. Does not stop owner
services or prepare a VM image. Run from M5, with the binaries named below built.
Private certificates/bearers stay under the new /tmp root, never in evidence.
"""
import hashlib
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import secrets
import shlex
import signal
import subprocess
import sys
import time

ROOT = Path('/tmp/l16-' + secrets.token_hex(4))
ROOT.mkdir(mode=0o700)
EVIDENCE = Path(sys.argv[1])
EVIDENCE.mkdir(parents=True, exist_ok=False)
LOG = (EVIDENCE / 'transcript.jsonl').open('x')
HOSTS = {
    'hs_lx': ('ubuntu-server', '100.64.10.6', 'linux', 'x86_64'),
    'hs_m5': (None, '100.64.10.5', 'darwin', 'arm64'),
    'hs_air': ('owner-air', '100.64.10.7', 'darwin', 'arm64'),
}
TOKEN = secrets.token_hex(32)
OWNER = 'pr_t16_demo'
BINS = Path('/tmp/lamp-t16-build')
ACTIVE = {}
GRANT = None
WORKER = None
STORE = "/var/lib/lamp/t10-private"
WORKER_SOCKET = str(ROOT/"worker.sock")
TEMPLATE = "tp_6b295ecbcad03c2a3b7370e577"
SANDBOX = None
CURRENT = None
TUNNELS = []
PORT_BASE = 30000 + secrets.randbelow(9000)
PORTS = {"hs_lx": PORT_BASE, "hs_m5": PORT_BASE+10, "hs_air": PORT_BASE+20}

def record(step, value):
    LOG.write(json.dumps(dict(step=step, value=value), sort_keys=True) + '\n')
    LOG.flush()
    print(step, flush=True)

def run(host, argv, **kwargs):
    command = argv if HOSTS[host][0] is None else ['ssh', '-o', 'BatchMode=yes', HOSTS[host][0], shlex.join(argv)]
    return subprocess.check_output(command, **kwargs)

def signal_own(host, pid, name):
    return python(host,"import os,signal,sys\ntry:os.kill(int(sys.argv[1]),getattr(signal,sys.argv[2]))\nexcept ProcessLookupError:pass",str(pid),name)

def python(host, source, *args):
    return run(host, ['python3', '-c', source, *args], text=True)

def put(host, path, data):
    if isinstance(data, str): data = data.encode()
    if HOSTS[host][0] is None:
        Path(path).write_bytes(data); Path(path).chmod(0o600)
    else:
        subprocess.run(['ssh', '-o', 'BatchMode=yes', HOSTS[host][0],
                        shlex.join(['python3', '-c', 'import os,sys; p=sys.argv[1]; f=os.open(p,os.O_CREAT|os.O_TRUNC|os.O_WRONLY,0o600); os.write(f,sys.stdin.buffer.read()); os.close(f)', str(path)])], input=data, check=True)

def cli(host, noun, verb, data=None, token=TOKEN, okay=True):
    source = '''import json,os,subprocess,sys
root,noun,verb,token,data=sys.argv[1:]
env=dict(os.environ,LAMP_SOCKET=root+'/api.sock',LAMP_TOKEN=token,LAMP_API_URL='lamp+fleet://'+root+'/discovery.json')
args=[root+'/lamp',noun,verb,'--json']
if data!='null':
 p=root+'/input-'+str(os.getpid())+'.json';open(p,'w').write(data);args+=['--input',p]
r=subprocess.run(args,env=env,capture_output=True,text=True,timeout=100)
print(json.dumps({'exit':r.returncode,'reply':json.loads(r.stdout or r.stderr)}))
'''
    result=json.loads(python(host, source, str(CURRENT), noun, verb, token, json.dumps(data)))
    if okay and result['exit']: raise RuntimeError(result)
    return result

def cert(host):
    key=ROOT/(host+'.key'); csr=ROOT/(host+'.csr'); crt=ROOT/(host+'.crt')
    subprocess.run(['openssl','req','-new','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-keyout',str(key),'-out',str(csr),'-subj','/CN='+host],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    ext=ROOT/(host+'.ext'); ext.write_text('subjectAltName=DNS:'+host+'\nextendedKeyUsage=serverAuth,clientAuth\n')
    subprocess.run(['openssl','x509','-req','-in',str(csr),'-CA',str(ROOT/'ca.crt'),'-CAkey',str(ROOT/'ca.key'),'-CAcreateserial','-out',str(crt),'-days','2','-extfile',str(ext)],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    key.chmod(0o600)

def guard():
    raw=run('hs_lx',['sh','-c','pgrep -c firecracker; free -m'],text=True)
    lines=raw.splitlines(); count=int(lines[0]); memory=next(x.split() for x in lines if x.startswith('Mem:'))
    value=dict(firecracker=count, available_mib=int(memory[-1]), raw=raw)
    record('LX allocation guard',value)
    if count>=2 or value['available_mib']<12288:raise RuntimeError('owner LX allocation guard denied')

def start(phase, hosts):
    global CURRENT, GRANT
    CURRENT=ROOT/phase
    manifest=[]
    for host in sorted(hosts):
        _,ip,system,arch=HOSTS[host]
        capacity=dict(vcpus=2,memory_mib=4096,disk_gib=20,pids_max=4096) if host=='hs_lx' else dict(vcpus=0,memory_mib=0,disk_gib=0,pids_max=0)
        manifest.append(dict(id=host,api='https://127.0.0.1:'+str(PORTS[host]),raft='127.0.0.1:'+str(PORTS[host]+1),os=system,arch=arch,engines=['firecracker-1.17.0'] if host=='hs_lx' else [],capacity=capacity))
    for host in hosts:
        python(host,'import os,sys;os.makedirs(sys.argv[1],mode=0o700,exist_ok=False);os.chmod(sys.argv[1],0o700)',str(CURRENT))
        for name in ['ca.crt',host+'.crt',host+'.key']:put(host,CURRENT/name,(ROOT/name).read_bytes())
        put(host,CURRENT/'identity.json',json.dumps(dict(owner_id=OWNER,bootstrap_sha256=hashlib.sha256(TOKEN.encode()).hexdigest())))
        config=dict(public_api_listen="127.0.0.1:"+str(PORTS[host]+2),host_id=host,hosts=manifest,ca_file=str(CURRENT/'ca.crt'),cert_file=str(CURRENT/(host+'.crt')),key_file=str(CURRENT/(host+'.key')),guard_dir=str(CURRENT/'guard'),bootstrap=host==sorted(hosts)[0])
        put(host,CURRENT/'fleet.json',json.dumps(config))
        order=sorted(hosts,key=lambda host:host!='hs_m5')
        discovery=dict(ca_file=str(CURRENT/'ca.crt'),ingresses=[dict(url='https://127.0.0.1:'+str(PORTS[item]+2),server_name=item) for item in order])
        put(host,CURRENT/'discovery.json',json.dumps(discovery))
        suffix='linux' if host=='hs_lx' else 'darwin'
        for binary in ['lamp','lampd']:
            if HOSTS[host][0] is None:
                import shutil
                shutil.copy(BINS/(binary+'-'+suffix),CURRENT/binary)
            else:
                subprocess.run(['scp','-q',str(BINS/(binary+'-'+suffix)),HOSTS[host][0]+':'+str(CURRENT/binary)],check=True)
            run(host,['chmod','700',str(CURRENT/binary)])
        args=[str(CURRENT/'lampd'),'--fleet-config',str(CURRENT/'fleet.json'),'--identity-file',str(CURRENT/'identity.json'),'--state-dir',str(CURRENT/'state'),'--socket',str(CURRENT/'api.sock')]
        if host=='hs_lx':
            if GRANT is None:
                GRANT=json.loads(run(host,['sudo','-n','/usr/local/lib/lamp/lamp-supervisor','grant-install','--store',STORE,'--caller-uid','1000','--ttl-s','3600'],text=True))
                put('hs_lx',ROOT/'grant.json',json.dumps(GRANT))
            pid=str(WORKER)
            boot=run(host,['cat','/proc/sys/kernel/random/boot_id'],text=True).strip()
            args+=['--worker-socket',WORKER_SOCKET,'--worker-id','lamp-supervisor@ubuntu-server','--worker-boot-id',boot,'--worker-uid','0','--worker-pid',pid,'--grant-id',GRANT['grant_id'],'--worker-timeout','60s']
        pid=int(python(host,'import os,sys,json,subprocess; f=open(sys.argv[1],"wb"); p=subprocess.Popen(json.loads(sys.argv[2]),stdout=f,stderr=f,start_new_session=True); print(p.pid)',str(CURRENT/'lampd.log'),json.dumps(args)))
        ACTIVE[host]=pid
    # Each pair has its own SSH connection over Tailscale, with mTLS inside.
    # No peer uses a third machine as a relay or consensus witness.
    pairs=[('hs_m5','hs_lx'),('hs_m5','hs_air'),('hs_air','hs_lx')]
    for source,destination in pairs:
        if source not in hosts or destination not in hosts:continue
        args=['ssh','-N','-o','BatchMode=yes','-o','ExitOnForwardFailure=yes','-o','ServerAliveInterval=15']
        for offset in (0,1,2):
            remote=PORTS[destination]+offset;local=PORTS[source]+offset
            args+=['-L',f'127.0.0.1:{remote}:127.0.0.1:{remote}','-R',f'127.0.0.1:{local}:127.0.0.1:{local}']
        args.append(HOSTS[destination][0])
        pid=int(python(source,'import json,subprocess,sys;f=open(sys.argv[1],"wb");p=subprocess.Popen(json.loads(sys.argv[2]),stdout=f,stderr=f,start_new_session=True);print(p.pid)',str(CURRENT/('ssh-'+destination+'.log')),json.dumps(args)))
        TUNNELS.append((source,pid))
    record(phase+' transport',dict(address='lamp+fleet://'+str(CURRENT)+'/discovery.json',transport='pairwise SSH over Tailscale with end-to-end host mTLS',pairs=[list(p) for p in pairs if p[0] in hosts and p[1] in hosts]))
    # This is service readiness, not a measured trial or benchmark loop.
    deadline=time.monotonic()+35
    for host in hosts:
        while True:
            result=cli(host,'fleet','get',dict(fleet_id='fl_local'),okay=False)
            if not result['exit']:break
            if time.monotonic()>deadline:raise RuntimeError(result)
            time.sleep(1)
    record(phase+' fleet',cli(hosts[0],'fleet','get',dict(fleet_id='fl_local')))
    record(phase+' hosts',cli(hosts[0],'host','list'))

def stop():
    global ACTIVE
    for host,pid in ACTIVE.items():
        signal_own(host,pid,'SIGCONT')
        signal_own(host,pid,'SIGTERM')
    for host in ACTIVE:
        # Keep original failed logs too; no private key or bearer is copied.
        text=run(host,['cat',str(CURRENT/'lampd.log')],text=True)
        (EVIDENCE/(CURRENT.name+'-'+host+'.log')).write_text(text)
    ACTIVE={}
    for host,pid in TUNNELS:signal_own(host,pid,'SIGTERM')
    TUNNELS.clear()
    time.sleep(2)

def release():
    global SANDBOX
    if SANDBOX:
        sb=cli('hs_lx','sandbox','get',dict(sandbox_id=SANDBOX))['reply']['data']
        result=cli('hs_lx','sandbox','delete',dict(sandbox_id=SANDBOX,expected_revision=sb['revision'],idempotency_key='lk1.1.hs_lx.cleanup-'+secrets.token_hex(4),wait_ms=60000))
        record('sandbox released',result)
        SANDBOX=None

def create(host,key,pin=None):
    global SANDBOX
    guard()
    data=dict(template_id=TEMPLATE,resources=dict(vcpus=2,memory_mib=4096,disk_gib=20,pids_max=4096),idempotency_key=key,wait_ms=60000)
    if key is None:data.pop('idempotency_key')
    if pin:data['host_id']=pin
    result=cli(host,'sandbox','create',data,okay=False)
    if result['exit']:
        operation=result['reply'].get('error',{}).get('operation_id')
        if operation:
            record('failed allocation',result)
            rec=cli('hs_lx','operation','get',dict(operation_id=operation))['reply']['data']
            SANDBOX=next(ref['id'] for ref in rec['resource_ids'] if ref['kind']=='sandbox')
        raise RuntimeError(result)
    SANDBOX=result['reply']['result']['sandbox_id']
    record('real sandbox create',result)
    sb=cli(host,'sandbox','get',dict(sandbox_id=SANDBOX))['reply']['data']
    if sb['state']!='ready' or sb['host_id']!='hs_lx':raise RuntimeError(sb)
    record('ready sandbox on LX',sb)
    return data,result

try:
    subprocess.run(['openssl','req','-x509','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-keyout',str(ROOT/'ca.key'),'-out',str(ROOT/'ca.crt'),'-days','2','-subj','/CN=Lamp T16 disposable fleet'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    (ROOT/'ca.key').chmod(0o600)
    for host in HOSTS:
        cert(host)
        python(host,'import socket,sys;ports=__import__("json").loads(sys.argv[1]);sockets=[]\nfor port in ports:\n s=socket.socket();s.bind(("127.0.0.1",port));sockets.append(s)',json.dumps([port+offset for port in PORTS.values() for offset in (0,1,2)]))
    record('source and binaries',dict(source=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),binaries={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in BINS.iterdir()},private_root=str(ROOT)))
    python('hs_lx','import os,sys;os.makedirs(sys.argv[1],mode=0o700,exist_ok=True);os.chmod(sys.argv[1],0o700)',str(ROOT))
    worker_args=['sudo','-n','sh','-c','echo "$$" > "$1"; shift; exec "$@"','sh',str(ROOT/'worker.pid'),'/usr/local/lib/lamp/lamp-supervisor','serve','--store',STORE,'--socket',WORKER_SOCKET,'--network-policy','/var/lib/lamp/t10-private-bin/network.json','--allow-uid','1000']
    python('hs_lx','import json,subprocess,sys;f=open(sys.argv[1],"wb");subprocess.Popen(json.loads(sys.argv[2]),stdout=f,stderr=f,start_new_session=True)',str(ROOT/'worker.log'),json.dumps(worker_args))
    time.sleep(2)
    WORKER=int(run('hs_lx',['cat',str(ROOT/'worker.pid')],text=True))
    record('prepared worker',dict(pid=WORKER,store=STORE,template=TEMPLATE,owner_services='untouched'))
    start('one',['hs_lx'])
    python('hs_lx','import shutil,sys;shutil.copytree(sys.argv[1],sys.argv[2])',str(CURRENT/'state'),str(CURRENT/'old-state'))
    record('one-host key namespace',cli('hs_lx','key','create',dict(principal_id=OWNER,expires_at=(datetime.now(timezone.utc)+timedelta(hours=1)).isoformat().replace("+00:00","Z"),scope=dict(actions=['fleet.get','host.list']),idempotency_key='one-key'))['reply']['result']['key_id'])
    stop()
    rollback=python('hs_lx','import json,subprocess,sys;root=sys.argv[1];r=subprocess.run([root+"/lampd","--fleet-config",root+"/fleet.json","--identity-file",root+"/identity.json","--state-dir",root+"/old-state","--socket",root+"/rollback.sock"],capture_output=True,text=True,timeout=10);print(json.dumps(dict(exit=r.returncode,stderr=r.stderr)))',str(CURRENT))
    record('stale local backup refused',json.loads(rollback))
    if 'rolled back' not in rollback:raise RuntimeError('stale backup was not rejected')
    start('two',['hs_lx','hs_m5'])
    key=cli('hs_m5','key','create',dict(principal_id=OWNER,expires_at=(datetime.now(timezone.utc)+timedelta(hours=1)).isoformat().replace("+00:00","Z"),scope=dict(actions=['fleet.get','host.list']),idempotency_key='two-key'))['reply']['result']
    record('shared key accepted at LX',cli('hs_lx','fleet','get',dict(fleet_id='fl_local'),token=key['secret']))
    data,created=create('hs_m5','automatic-placement')
    # A guest marker demonstrates execution through the M5 ingress and the LX worker.
    executed=cli('hs_m5','exec','create',dict(sandbox_id=SANDBOX,argv=['/bin/sh','-c','printf t16-real-guest'],idempotency_key='lk1.1.hs_lx.guest-marker',wait_ms=60000))
    record('remote real guest command',executed)
    ex=executed['reply']['result']['exec_id']
    record('guest command output',cli('hs_m5','exec','watch',dict(exec_id=ex,wait_ms=1000)))
    replay=cli('hs_lx','sandbox','create',data)
    if replay['reply']['operation_id']!=created['reply']['operation_id'] or not replay['reply']['replayed']:raise RuntimeError('key replay duplicated')
    record('same keyed create through other ingress',replay)
    record('strict incompatible Mac pin rejected',cli('hs_m5','sandbox','create',dict(template_id=TEMPLATE,host_id='hs_m5',idempotency_key='lk1.1.hs_m5.strict-pin',wait_ms=60000),okay=False))
    record('live sandbox catalog',cli('hs_m5','sandbox','list'))
    run('hs_lx',['kill','-STOP',str(ACTIVE['hs_lx'])])
    record('LX daemon cut off, custody remains LX',cli('hs_m5','sandbox','list'))
    record('sleeping-home sandbox remains inspectable',cli('hs_m5','sandbox','get',dict(sandbox_id=SANDBOX)))
    record('old keyed effect not reassigned',cli('hs_m5','sandbox','create',data,okay=False))
    run('hs_lx',['kill','-CONT',str(ACTIVE['hs_lx'])]);time.sleep(3)
    release()
    run('hs_m5',['kill','-STOP' ,str(ACTIVE['hs_m5'])])
    record('M5 lampd cut off',dict(host='hs_m5',fault='SIGSTOP ticket daemon only; owner apps and machine remain running'))
    create('hs_lx',None)
    record('survivor global authority refused',cli('hs_lx','key','create',dict(principal_id=OWNER,idempotency_key='blocked-global'),okay=False))
    release()
    run('hs_m5',['kill','-CONT',str(ACTIVE['hs_m5'])]);time.sleep(4)
    record('M5 rejoins same fleet',cli('hs_m5','host','list'))
    current=cli('hs_lx','key','get',dict(key_id=key['key_id']))['reply']['data']
    revoke=dict(key_id=key['key_id'],expected_revision=current['revision'],idempotency_key='revoke-key')
    deadline=time.monotonic()+45
    while True:
        result=cli('hs_m5','key','delete',revoke,okay=False)
        if result['exit']==0:break
        record('rejoin authority still recovering, same revoke identity retained',result)
        if result['reply'].get('error',{}).get('code')!='QUORUM_REQUIRED' or time.monotonic()>deadline:raise RuntimeError(result)
        time.sleep(2)
    record('key revoked with quorum',result)
    time.sleep(2)
    denied=cli('hs_lx','fleet','get',dict(fleet_id='fl_local'),token=key['secret'],okay=False)
    if denied['exit']==0:raise RuntimeError('revoked key accepted')
    record('revoked shared key rejected at LX',denied)
    stop()
    start('three',['hs_lx','hs_m5','hs_air'])
    record('Air changes replicated fleet name',cli('hs_air','fleet','update',dict(fleet_id='fl_local',expected_revision=1,name='T16 three owned hosts',idempotency_key='air-name')))
    time.sleep(2)
    record('M5 sees same fleet name',cli('hs_m5','fleet','get',dict(fleet_id='fl_local')))
    record('three hosts complete',dict(compute='LX real Firecracker',control=['LX','M5','AIR'],owner_services='untouched',benchmarks='none'))
except Exception as error:
    record('demonstration failure',dict(type=type(error).__name__,message=str(error)))
    raise
finally:
    try:
        for host,pid in ACTIVE.items():signal_own(host,pid,'SIGCONT')
        release()
    finally:
        if ACTIVE:stop()
        if GRANT:run('hs_lx',['sudo','-n','/usr/local/lib/lamp/lamp-supervisor','grant-revoke','--store',STORE,GRANT['grant_id']])
        if WORKER:run('hs_lx',['sudo','-n','kill','-TERM',str(WORKER)])
        record('cleanup',dict(live_ticket_sandboxes=0 if SANDBOX is None else 'cleanup incomplete',private_roots_retained=str(ROOT)))
        LOG.close()
