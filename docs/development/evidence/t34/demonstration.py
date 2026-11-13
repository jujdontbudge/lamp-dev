#!/usr/bin/env python3
"""One disposable T34 demonstration on the owner's Ubuntu laptop (no credentials logged)."""
import shlex
import base64, hashlib, http.client, json, os, secrets, socket, subprocess, sys, time, urllib.parse
from pathlib import Path

SRC=Path(sys.argv[1]); ROOT=Path(sys.argv[2]); ROOT.mkdir(mode=0o700)
SUP=str(SRC/'target/debug/lamp-supervisor'); STORE=os.environ.get('T34_STORE','/var/lib/lamp/'+ROOT.name)
OPS=json.loads((SRC/'contracts/public/schema.json').read_text())['x-lamp-operations']
transcript=(ROOT/'transcript.jsonl').open('x')
def log(step,data):
    line=json.dumps(dict(step=step,data=data),sort_keys=True); print(line,flush=True); transcript.write(line+'\n'); transcript.flush()
def sh(*args): return subprocess.check_output(list(map(str,args)),text=True).strip()
def capacity():
    r=subprocess.run(['pgrep','-c','firecracker'],capture_output=True,text=True)
    n=int(r.stdout.strip() or 0); available=int(sh('free','-m').splitlines()[1].split()[6])
    log('capacity',dict(firecracker=n,available_mib=available))
    if n>=2 or available<12288: raise RuntimeError('shared capacity unavailable')
class API:
    def __init__(self,sock,token): self.sock,self.token=sock,token
    def raw(self,method,path,body=None,headers=None):
        c=http.client.HTTPConnection('lamp',timeout=180); c.sock=socket.socket(socket.AF_UNIX); c.sock.settimeout(180); c.sock.connect(str(self.sock))
        h=dict(headers or {},Authorization='Bearer '+self.token,**{'Content-Type':'application/json'})
        c.request(method,path,None if body is None else json.dumps(body),h); r=c.getresponse(); out=json.loads(r.read() or '{}'); c.close()
        if r.status>=400: raise RuntimeError(json.dumps(out))
        return out
    def op(self,op_name,**fields):
        o=OPS[op_name]; fields=dict(fields); path=o['path']; h={}
        for k in list(fields):
            if '{'+k+'}' in path: path=path.replace('{'+k+'}',str(fields.pop(k)))
        if o['mutation']:
            h['Idempotency-Key']=secrets.token_hex(8)
            if 'expected_revision' in fields: h['If-Match']=str(fields.pop('expected_revision'))
            fields['wait_ms']=60000
        elif fields: path+='?'+urllib.parse.urlencode(fields)
        out=self.raw(o['method'].upper(),path,fields if o['mutation'] else None,h)
        if o['mutation']:
            while out.get('operation',{}).get('state') not in ('succeeded','failed','outcome_unknown','canceled'):
                op=self.raw('GET','/v1/operations/'+out['operation_id']+'/wait?timeout_ms=60000')['data']
                out=dict(operation=op,operation_id=op['id'],resource_ids=op['resource_ids'],result=(op.get('outcome') or {}).get('result'))
            if out['operation']['state']!='succeeded': raise RuntimeError(json.dumps(out['operation'].get('error') or out))
        return out
    def get(self,kind,id): return self.op(kind+'_get',**{kind+'_id':id})['data']
    def run(self,sb,script):
        out=self.op('exec_create',sandbox_id=sb,argv=['sh','-c',script]); ex=out['resource_ids'][0]['id']; data=bytearray(); errors=bytearray(); cursor=None
        while True:
            f=dict(exec_id=ex,wait_ms=20000)
            if cursor: f['cursor']=cursor
            p=self.op('exec_watch',**f)
            for item in p['items']:
                if item['channel']=='stdout': data.extend(base64.b64decode(item['data_base64']))
                elif item['channel']=='stderr': errors.extend(base64.b64decode(item['data_base64']))
            cursor=p.get('next_cursor') or cursor
            e=self.get('exec',ex)
            if e['state'] in ('exited','failed','outcome_unknown') and not p.get('next_cursor'):
                assert e.get('exit_code')==0,(e,data.decode(),errors.decode()); return data.decode().strip()
    def advancing(self,sb):
        values=list(map(int,self.run(sb,'cat /tmp/counter; echo; sleep 1; cat /tmp/counter; echo').splitlines())); assert len(values)==2 and values[1]>values[0],values; return values
    def change(self,id,verb,**kw): return self.op('sandbox_'+verb,sandbox_id=id,expected_revision=self.get('sandbox',id)['revision'],**kw)
    def create(self,**kw):
        capacity(); o=self.op('sandbox_create',**kw); sb=o['resource_ids'][0]['id']; owned.append((self,sb)); assert self.get('sandbox',sb)['state']=='ready'; return sb

token=secrets.token_hex(32); ident=ROOT/'identity.json'; ident.write_text(json.dumps(dict(owner_id='pr_t34_demo',bootstrap_sha256=hashlib.sha256(token.encode()).hexdigest()))); ident.chmod(0o600)
logfile=(ROOT/'services.log').open('a'); worker=None; daemon=None; owned=[]; api=None
try:
    capacity()
    # New store: import only a public third-party demonstration seed, never an owner repository.
    tpl=os.environ.get('T34_TEMPLATE')
    if not tpl:
        seed=sh('sudo','-n',SUP,'seed-import','/home/johan/lamp-t29/seed2','--store',STORE)
        template=ROOT/'template.lamp'; template.write_text('format = "lamp.template/v1"\nname = "t34-public-demo"\nguest_os = "linux"\narch = "x86_64"\nseed_ref = '+json.dumps(seed)+'\nnetwork = "none"\n')
        capacity()
        built=sh('sudo','-n',SUP,'template-build',template,'--store',STORE)
        tpl=json.loads(built.splitlines()[-1])['template_id']; log('public template',dict(template_id=tpl))
    log('using public template',dict(template_id=tpl))
    # This network policy is new and uses the installed guard without altering it.
    policy=ROOT/'network.json'; policy.write_text(json.dumps(dict(guard=str(SRC/'target/debug/lamp-guard'),pasta='/usr/bin/pasta',audit_dir=STORE+'/audit',revision=1)))
    pidfile=ROOT/'worker.pid'; ws=ROOT/'worker.sock'; admin=ROOT/'admin.sock'
    worker=subprocess.Popen(['sudo','-n','sh','-c','echo "$$" > "$1"; shift; exec "$@"','sh',str(pidfile),SUP,'serve','--store',STORE,'--socket',str(ws),'--admin-socket',str(admin),'--network-policy',str(policy),'--mem-dir','/run/'+ROOT.name+'/m','--netns-dir','/run/'+ROOT.name+'/n','--uid-base','2130000000','--host-id',os.uname().nodename,'--allow-uid',str(os.getuid())],stdout=logfile,stderr=logfile)
    until=time.monotonic()+600
    while not ws.exists():
        if worker.poll() is not None or time.monotonic()>until: raise RuntimeError('worker failed to start')
        time.sleep(.1)
    grant=json.loads(sh('sudo','-n',SUP,'grant-install','--store',STORE,'--caller-uid',str(os.getuid()),'--ttl-s','7200'))
    log('default grant lifecycle scope',dict(generation=grant['generation'],max_generation=grant.get('max_generation')))
    sh(SRC/'bin/lamp-installer','recovery-keygen','--out',ROOT/'offline.key','--public',ROOT/'recovery.pub')
    wargs=['--worker-socket',str(ws),'--worker-id','lamp-supervisor@'+os.uname().nodename,'--worker-boot-id',Path('/proc/sys/kernel/random/boot_id').read_text().strip(),'--worker-uid','0','--worker-pid',pidfile.read_text().strip(),'--grant-id',grant['grant_id'],'--worker-timeout','60s','--state-admin',json.dumps(['sudo','-n',SUP,'state','--admin-socket',str(admin)])]
    def start(prefix,recovery=False):
        global daemon,api
        sock=ROOT/(prefix+'.sock')
        if sock.exists(): sock.unlink()
        args=[str(SRC/'bin/lampd')]+wargs+['--identity-file',str(ident),'--state-dir',str(ROOT/prefix),'--socket',str(sock),'--recovery-public-key',str(ROOT/'recovery.pub')]
        if recovery: args+=['--recovery-key-file',str(ROOT/'offline.key'),'--recovery-import-dir',str(ROOT/'state/backups')]
        daemon=subprocess.Popen(args,stdout=logfile,stderr=logfile)
        until=time.monotonic()+30
        while not sock.exists():
            if daemon.poll() is not None or time.monotonic()>until: raise RuntimeError('daemon failed to start')
            time.sleep(.1)
        api=API(sock,token)
    def stop():
        global daemon
        if daemon and daemon.poll() is None: daemon.terminate(); daemon.wait(timeout=60)
    start('state')
    limits=dict(vcpus=2,memory_mib=1024,disk_gib=20,pids_max=4096)
    sb=api.create(template_id=tpl,resources=limits,lifecycle=dict(idle_grace_seconds=10,suspended_retention_seconds=600))
    program='import time,os\ni=0\nopen("/tmp/counter.pid","w").write(str(os.getpid()))\nwhile True:\n i+=1; f=open("/tmp/counter-next","w"); f.write(str(i)); f.close(); os.replace("/tmp/counter-next","/tmp/counter"); time.sleep(.1)'
    counter='setsid python3 -u -c '+shlex.quote(program)+' >/dev/null 2>&1 </dev/null &'
    api.run(sb,counter); before=api.advancing(sb); log('counter before suspend',before)
    api.change(sb,'suspend'); assert api.get('sandbox',sb)['state']=='suspended'
    stop(); start('state'); capacity(); api.change(sb,'resume'); log('resume after daemon restart',dict(state=api.get('sandbox',sb)['state'],counter=api.advancing(sb)))
    # Background process must prevent idle suspension, despite no mediated execs.
    until=time.monotonic()+25
    while True:
        state=api.get('sandbox',sb); eligibility=state.get('suspend_eligibility',{})
        if 'task_process' in eligibility.get('reasons',[]): break
        if time.monotonic()>until: raise RuntimeError('busy process not observed')
        time.sleep(.5)
    log('busy eligibility',dict(state=state['state'],eligibility=eligibility)); assert state['state']=='ready'
    snap=api.op('snapshot_create',sandbox_id=sb)['resource_ids'][0]['id']; api.change(sb,'delete')
    log('snapshot outlives source',api.op('snapshot_list')['items'])
    restored=api.create(snapshot_id=snap); log('snapshot memory restored',api.advancing(restored))
    api.op('snapshot_delete',snapshot_id=snap,expected_revision=api.get('snapshot',snap)['revision'])
    assert all(s['id']!=snap for s in api.op('snapshot_list')['items']); log('snapshot explicitly deleted',snap)
    vol=api.op('volume_create',name='t34-public-data',mode='shared_files',quota_bytes=1048576)['resource_ids'][0]['id']
    at=api.op('attachment_create',sandbox_id=restored,resource_kind='volume',resource_id=vol,mount_path='/tmp/t34-volume',mode='read_write')['resource_ids'][0]['id']
    api.run(restored,'echo volume-survives > /tmp/t34-volume/proof.txt')
    api.op('attachment_delete',attachment_id=at,expected_revision=api.get('attachment',at)['revision'])
    volume_backup=api.op('backup_create',resource_kind='volume',resource_id=vol)['resource_ids'][0]['id']
    log('encrypted volume backup',dict(volume=vol,backup=volume_backup))
    # Fork during writes; delete child promptly before starting anything else.
    api.run(restored,"setsid sh -c 'dd if=/dev/zero of=/var/tmp/t34-write bs=1M count=400; sync' >/dev/null 2>&1 </dev/null &")
    capacity(); child=api.change(restored,'fork',fork_mode='memory')['resource_ids'][0]['id']; owned.append((api,child)); api.change(child,'delete')
    log('source usable after writing fork',dict(state=api.get('sandbox',restored)['state'],counter=api.run(restored,'cat /tmp/counter')))
    api.change(restored,'suspend'); capacity(); log('automatic exec wake',api.advancing(restored))
    api.change(restored,'suspend'); backup=api.op('backup_create',resource_kind='sandbox',resource_id=restored)['resource_ids'][0]['id']; log('encrypted sandbox backup',api.get('backup',backup))
    # Stop the counter, establish an idle countdown, then restart mid-countdown.
    capacity(); api.run(restored,'kill $(cat /tmp/counter.pid)');
    api.change(restored,'update',lifecycle=dict(idle_grace_seconds=15,suspended_retention_seconds=20))
    until=time.monotonic()+20
    while True:
        idle=api.get('sandbox',restored)['idle_countdown']
        if 'fires_at' in idle: break
        if time.monotonic()>until: raise RuntimeError('idle countdown not observed')
        time.sleep(.25)
    stop(); start('state'); after=api.get('sandbox',restored)['idle_countdown']; log('countdown survives restart',dict(before=idle,after=after)); assert idle['fires_at']==after['fires_at']
    until=time.monotonic()+45
    while api.get('sandbox',restored)['state']!='suspended':
        if time.monotonic()>until: raise RuntimeError('idle suspension did not run')
        time.sleep(.5)
    log('idle suspended',api.get('sandbox',restored))
    until=time.monotonic()+40
    while api.get('sandbox',restored)['state']!='deleted':
        if time.monotonic()>until: raise RuntimeError('retention deletion did not run')
        time.sleep(.5)
    log('suspended retention expired',api.get('sandbox',restored)['state'])
    stop(); start('recovered',True); log('offline recovery imported',api.op('backup_list')['items'])
    recovered=api.op('backup_restore',backup_id=backup,expected_revision=1)['resource_ids'][-1]['id']; owned.append((api,recovered)); capacity(); api.change(recovered,'resume')
    log('fresh prefix recovered memory',dict(sandbox=recovered,counter=api.advancing(recovered)))
    recovered_volume=api.op('backup_restore',backup_id=volume_backup,expected_revision=1)['resource_ids'][-1]['id']
    api.op('attachment_create',sandbox_id=recovered,resource_kind='volume',resource_id=recovered_volume,mount_path='/tmp/t34-recovered-volume',mode='read_only')
    content=api.run(recovered,'cat /tmp/t34-recovered-volume/proof.txt'); assert content=='volume-survives'; log('fresh prefix recovered volume file',content)
    api.change(recovered,'delete')
    try: api.op('budget_create',deadline_at='1999-01-01T00:00:00Z')
    except RuntimeError as e: assert 'INVALID_ARGUMENT' in str(e); log('past budget deadline refused','INVALID_ARGUMENT')
    else: raise AssertionError('past deadline admitted')
    budget=api.op('budget_create',wall_time_ms=15000,watchdog=dict(warn_after_ms=1000))['resource_ids'][0]['id']
    busy=api.create(template_id=tpl,resources=limits,budget_id=budget); task=api.get('sandbox',busy)['task_id']; api.run(busy,"setsid sh -c 'while :; do :; done' >/dev/null 2>&1 </dev/null &")
    until=time.monotonic()+30
    while api.get('sandbox',busy)['state']!='deleted':
        if time.monotonic()>until: raise RuntimeError('budget stop did not run')
        time.sleep(.25)
    log('budget warning then hard stop',dict(task=api.get('task',task),sandbox=api.get('sandbox',busy)['state']))
    from datetime import datetime,timezone,timedelta
    expires=(datetime.now(timezone.utc)+timedelta(seconds=20)).isoformat()
    aged=api.create(template_id=tpl,resources=limits,lifecycle=dict(idle_action='keep',expires_at=expires))
    until=time.monotonic()+40
    while api.get('sandbox',aged)['state']!='deleted':
        if time.monotonic()>until: raise RuntimeError('maximum age did not delete')
        time.sleep(.25)
    log('maximum age enforced',api.get('sandbox',aged))
    log('DONE','T34 demonstrated against real Firecracker and public API')
finally:
    if api and daemon and daemon.poll() is None:
        for client,sb in reversed(owned):
            if client.sock!=api.sock: continue
            try:
                if api.get('sandbox',sb)['state']!='deleted': api.change(sb,'delete')
            except Exception as e: log('cleanup requires attention',dict(sandbox=sb,error=str(e)))
    if daemon and daemon.poll() is None: daemon.terminate(); daemon.wait(timeout=60)
    if worker and worker.poll() is None:
        sh('sudo','-n','kill','-TERM',(ROOT/'worker.pid').read_text().strip()); worker.wait(timeout=120)
    log('live firecracker after cleanup',subprocess.run(['pgrep','-c','firecracker'],capture_output=True,text=True).stdout.strip())
