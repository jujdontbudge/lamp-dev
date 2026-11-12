#!/usr/bin/env python3
"""One headless accountability revocation journey on the owner's M5, LX and Air; no benchmark.

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

ROOT = Path('/tmp/l23-' + secrets.token_hex(4))
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
MEMBER=secrets.token_hex(32)
OWNER = 'pr_t23_demo'
BINS = Path('/tmp/lamp-t23-build')
ACTIVE = {}
GRANT = None
WORKER = None
STORE = "/var/lib/lamp/t23"
WORKER_SOCKET = str(ROOT/"worker.sock")
TEMPLATE = "tp_8a6922170222418fabafdc0c95"
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

def cli(host, noun, verb, data=None, token=TOKEN, okay=True, retry=True, direct=False):
    source = '''import json,os,subprocess,sys
root,noun,verb,token,data,direct=sys.argv[1:]
env=dict(os.environ,LAMP_SOCKET=root+'/api.sock',LAMP_TOKEN=token,LAMP_API_URL='' if direct=='1' else 'lamp+fleet://'+root+'/discovery.json')
args=[root+'/lamp',noun,verb,'--json']
if data!='null':
 p=root+'/input-'+str(os.getpid())+'.json';open(p,'w').write(data);args+=['--input',p]
r=subprocess.run(args,env=env,capture_output=True,text=True,timeout=100)
print(json.dumps({'exit':r.returncode,'reply':json.loads(r.stdout or r.stderr)}))
'''
    result=json.loads(python(host, source, str(CURRENT), noun, verb, token, json.dumps(data), "1" if direct else "0"))
    if retry and result['reply'].get('error',{}).get('code')=='REVISION_CONFLICT':return cli(host,noun,verb,data,token,okay,retry=False,direct=direct)
    if okay and result['exit']: raise RuntimeError(result)
    return result

def cert(host):
    key=ROOT/(host+'.key'); csr=ROOT/(host+'.csr'); crt=ROOT/(host+'.crt')
    subprocess.run(['openssl','req','-new','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-keyout',str(key),'-out',str(csr),'-subj','/CN='+host],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    ext=ROOT/(host+'.ext'); ext.write_text('subjectAltName=DNS:'+host+'\nextendedKeyUsage=serverAuth,clientAuth\n')
    subprocess.run(['openssl','x509','-req','-in',str(csr),'-CA',str(ROOT/'ca.crt'),'-CAkey',str(ROOT/'ca.key'),'-CAcreateserial','-out',str(crt),'-days','2','-extfile',str(ext)],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    key.chmod(0o600)

def guard():
    # This waits only for the owner's shared capacity allowance, not a trial.
    deadline=time.monotonic()+300
    while True:
        raw=run('hs_lx',['sh','-c','pgrep -c firecracker || true; free -m'],text=True)
        lines=raw.splitlines();count=int(lines[0]);memory=next(l.split() for l in lines if l.startswith('Mem:'))
        value=dict(firecracker=count,available_mib=int(memory[-1]),raw=raw)
        if count<2 and value['available_mib']>=12288:
            record('LX allocation guard',value)
            return
        if time.monotonic()>deadline:
            record('LX allocation guard denied',value)
            raise RuntimeError('owner LX allocation guard denied')
        time.sleep(3)

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
        put(host,CURRENT/'identity.json',json.dumps(dict(owner_id=OWNER,bootstrap_sha256=hashlib.sha256(TOKEN.encode()).hexdigest(),subjects=[dict(issuer="t23-disposable",subject="bob",sha256=hashlib.sha256(MEMBER.encode()).hexdigest())])))
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
                subprocess.run(['scp','-q','-C',str(BINS/(binary+'-'+suffix)),HOSTS[host][0]+':'+str(CURRENT/binary)],check=True)
            run(host,['chmod','700',str(CURRENT/binary)])
        args=[str(CURRENT/'lampd'),'--fleet-config',str(CURRENT/'fleet.json'),'--identity-file',str(CURRENT/'identity.json'),'--state-dir',str(CURRENT/'state'),'--socket',str(CURRENT/'api.sock')]
        if host=='hs_lx':
            if GRANT is None:
                GRANT=json.loads(run(host,['sudo','-n','/usr/local/lib/lamp/lamp-supervisor','grant-install','--store',STORE,'--caller-uid','1000','--ttl-s','3600'],text=True))
                put('hs_lx',ROOT/'grant.json',json.dumps(GRANT))
            pid=str(WORKER)
            boot=run(host,['cat','/proc/sys/kernel/random/boot_id'],text=True).strip()
            args+=['--worker-socket',WORKER_SOCKET,'--worker-id','lamp-supervisor@hs_t23','--worker-boot-id',boot,'--worker-uid','0','--worker-pid',pid,'--grant-id',GRANT['grant_id'],'--worker-timeout','60s']
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


def until(predicate, description, seconds=45):
    deadline=time.monotonic()+seconds
    while True:
        value=predicate()
        if value:return value
        if time.monotonic()>deadline:raise RuntimeError(description)
        time.sleep(.2)

try:
    subprocess.run(['openssl','req','-x509','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-keyout',str(ROOT/'ca.key'),'-out',str(ROOT/'ca.crt'),'-days','2','-subj','/CN=Lamp T23 disposable fleet'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    (ROOT/'ca.key').chmod(0o600)
    for host in HOSTS:cert(host)
    python('hs_lx','import os,sys;os.makedirs(sys.argv[1],mode=0o700,exist_ok=False)',str(ROOT))
    worker_args=['sudo','-n','sh','-c','echo "$$" > "$1"; shift; exec "$@"','sh',str(ROOT/'worker.pid'),STORE+'/bin/lamp-supervisor','serve','--store',STORE,'--socket',WORKER_SOCKET,'--network-policy','/home/owner/lamp-t23/policy.json','--mem-dir',str(ROOT/'mem'),'--netns-dir',str(ROOT/'netns'),'--uid-base','1850000000','--host-id','hs_t23','--allow-uid','1000']
    python('hs_lx','import json,subprocess,sys;f=open(sys.argv[1],"wb");subprocess.Popen(json.loads(sys.argv[2]),stdout=f,stderr=f,start_new_session=True)',str(ROOT/'worker.log'),json.dumps(worker_args))
    until(lambda:python('hs_lx','import pathlib,sys;print(int(pathlib.Path(sys.argv[1]).exists()))',WORKER_SOCKET).strip()=='1','worker readiness')
    WORKER=int(run('hs_lx',['cat',str(ROOT/'worker.pid')],text=True))
    start('revocation',['hs_m5','hs_lx','hs_air'])
    if '--role-only' not in sys.argv:
        parent=cli('hs_m5','key','create',dict(expires_at=(datetime.now(timezone.utc)+timedelta(hours=1)).isoformat().replace('+00:00','Z'),idempotency_key='t23-parent-key'))['reply']['result']
        until(lambda:not cli('hs_m5','fleet','get',dict(fleet_id='fl_local'),token=parent['secret'],okay=False)['exit'],'parent bearer propagation')
        child=cli('hs_m5','key','create',dict(idempotency_key='t23-child-key'),token=parent['secret'])['reply']['result']
        until(lambda:not cli('hs_m5','fleet','get',dict(fleet_id='fl_local'),token=child['secret'],okay=False)['exit'],'child bearer propagation')
        budget=cli('hs_m5','budget','create',dict(wall_time_ms=180000,token_mode='best_effort',idempotency_key='lk1.1.hs_lx.budget'))['reply']['result']['budget_id']
        task=cli('hs_m5','task','create',dict(budget_id=budget,idempotency_key='t23-task'),token=child['secret'])['reply']['result']['task_id']
        guard()
        made=cli('hs_m5','sandbox','create',dict(template_id=TEMPLATE,task_id=task,idempotency_key='t23-sandbox',wait_ms=60000),token=child['secret'])
        SANDBOX=made['reply']['result']['sandbox_id']
        record('derived authority real sandbox',made)
        cli('hs_m5','exec','create',dict(sandbox_id=SANDBOX,argv=['/bin/sleep','120'],idempotency_key='t23-long-command',wait_ms=60000),token=child['secret'])
        # Suspend this demo's Air daemon only, keeping its owner apps and OS online.
        signal_own('hs_air',ACTIVE['hs_air'],'SIGSTOP')
        # A read-only readiness wait lets the surviving two peers elect their leader.
        def quorum():
            result=cli('hs_m5','fleet','get',dict(fleet_id='fl_local'),okay=False)
            return not result['exit'] and result['reply']['data'].get('coordinator_host_id') not in (None,'','hs_air')
        until(quorum,'surviving fleet quorum',45)
        parent_key=cli('hs_m5','key','get',dict(key_id=parent['key_id']))['reply']['data']
        revoked=cli('hs_m5','key','delete',dict(key_id=parent['key_id'],expected_revision=parent_key['revision'],idempotency_key='t23-revoke'))
        operation=revoked['reply']['operation_id'];record('revocation committed',revoked)
        until(lambda:cli('hs_lx','sandbox','get',dict(sandbox_id=SANDBOX))['reply']['data']['state']=='deleted','derived workload stopped')
        record('connected host released revoked workload',cli('hs_lx','sandbox','get',dict(sandbox_id=SANDBOX)))
        SANDBOX=None
        def pending():
            reply=cli('hs_m5','operation','get',dict(operation_id=operation))
            enforcers=reply['reply']['data'].get('revocation_status',{}).get('enforcers',[])
            return reply if any(e['host_id']=='hs_air' and e['acknowledgment']=='pending' and e.get('authority_expires_at') for e in enforcers) and any(e['host_id']=='hs_lx' and e['acknowledgment']=='acknowledged' for e in enforcers) else None
        record('offline authority waiting to expire',until(pending,'offline status'))
        record('derived bearer denied',cli('hs_m5','fleet','get',dict(fleet_id='fl_local'),token=child['secret'],okay=False))
        signal_own('hs_air',ACTIVE['hs_air'],'SIGCONT')
        def complete():
            reply=cli('hs_m5','operation','get',dict(operation_id=operation));return reply if reply['reply']['data'].get('revocation_status',{}).get('complete') else None
        record('all hosts acknowledge after reconnect',until(complete,'reconnected authority enforcement'))
    if True:
        fleet=cli('hs_m5','fleet','get',dict(fleet_id='fl_local'))['reply']['data']
        cli('hs_m5','fleet','update',dict(fleet_id='fl_local',expected_revision=fleet['revision'],mode='team',idempotency_key='t23-team'))
        principal=cli('hs_m5','principal','create',dict(issuer='t23-disposable',subject='bob',display_name='Disposable Bob',idempotency_key='t23-bob'))['reply']['result']['principal_id']
        membership=cli('hs_m5','membership','create',dict(principal_id=principal,role='member',idempotency_key='t23-member'))['reply']['result']['membership_id']
        until(lambda:not cli('hs_m5','fleet','get',dict(fleet_id='fl_local'),token=MEMBER,okay=False)['exit'],'member authority propagation')
        for host in HOSTS:
            until(lambda:not cli(host,'fleet','get',dict(fleet_id='fl_local'),token=MEMBER,okay=False,direct=True)['exit'],'member authority at '+host)
        budget=cli('hs_m5','budget','create',dict(wall_time_ms=180000,token_mode='best_effort',idempotency_key='lk1.1.hs_lx.member-budget'),token=MEMBER)['reply']['result']['budget_id']
        task=cli('hs_m5','task','create',dict(budget_id=budget,idempotency_key='t23-member-task'),token=MEMBER)['reply']['result']['task_id']
        guard()
        made=cli('hs_m5','sandbox','create',dict(template_id=TEMPLATE,task_id=task,idempotency_key='t23-member-sandbox',wait_ms=60000),token=MEMBER)
        SANDBOX=made['reply']['result']['sandbox_id']
        cli('hs_m5','exec','create',dict(sandbox_id=SANDBOX,argv=['/bin/sleep','120'],idempotency_key='t23-member-command',wait_ms=60000),token=MEMBER)
        member=cli('hs_m5','membership','get',dict(membership_id=membership))['reply']['data']
        changed=cli('hs_m5','membership','update',dict(membership_id=membership,expected_revision=member['revision'],role='viewer',idempotency_key='t23-viewer'))
        record('role removal committed',changed)
        until(lambda:cli('hs_lx','sandbox','get',dict(sandbox_id=SANDBOX),token=MEMBER)['reply']['data']['state']=='deleted','revoked role workload stop')
        record('role removal stops real workload',cli('hs_lx','sandbox','get',dict(sandbox_id=SANDBOX),token=MEMBER))
        SANDBOX=None
        record('viewer cannot restart workload',cli('hs_m5','sandbox','create',dict(template_id=TEMPLATE,task_id=task,idempotency_key='t23-viewer-denied'),token=MEMBER,okay=False))

finally:
    try:release()
    finally:
        stop()
        if GRANT:run('hs_lx',['sudo','-n',STORE+'/bin/lamp-supervisor','grant-revoke','--store',STORE,GRANT['grant_id']])
        if WORKER:run('hs_lx',['sudo','-n','kill','-TERM',str(WORKER)])
        LOG.close()
