# Runs the T29 manual demonstration on the Ubuntu laptop: a private supervisor
# with its own store, deny-by-default policy plus one exact exception for the
# disposable Forgejo endpoint, then the demonstration test. Owner ceiling
# (pgrep -c firecracker < 2, >= 12 GiB available) is checked by lampd itself
# before every VM.
import hashlib,json,os,signal,subprocess,sys,time
signal.signal(signal.SIGTERM,lambda *_:sys.exit(1))  # still stop the supervisor and broker below
policy_mode=os.environ.get('LAMP_T29_POLICY','')  # trusted | public: one policy observation
ports=list(range(3929,3936))+([3936] if policy_mode=='trusted' else [])
from pathlib import Path
base=Path.home()/'lamp-t29'
sup='/home/johan/lamp-t19/src/target/release/lamp-supervisor'
store='/var/lib/lamp/t29-private';bindir='/var/lib/lamp/t29-private-bin';audit='/var/lib/lamp-audit/t29-private'
forge='http://10.219.29.1:3929'
def sh(*a,**kw):return subprocess.run(a,check=True,capture_output=True,text=True,**kw)
sh('sudo','-n','install','-d','-m','0755',bindir,'/run/t29','/run/t29/m','/run/t29/n')
sh('sudo','-n','install','-d','-m','0700',audit)
policy=json.loads(sh('sudo','-n','cat','/var/lib/lamp/t26-private-bin/network.json').stdout)
policy.update(audit_dir=audit,require_allowlist=True,subscription_domains=None,principal_id='pr_local',policy_layers=[],
 exceptions=[dict(address='10.219.29.1',transport='tcp',port=p) for p in ports],
 policy_ceiling=[dict(filter_mode='transport',default_egress='deny',allow=[dict(cidr='10.219.29.1/32',protocol='tcp',ports=ports)],deny=[])])
# This branch's guard (heartbeats coalesced), root-owned, not the shared T19 copy.
sh('sudo','-n','install','-m','0755',str(base/'src/target/release/lamp-guard'),bindir+'/lamp-guard')
policy['guard']=bindir+'/lamp-guard'
(base/'network.json').write_text(json.dumps(policy))
sh('sudo','-n','install','-m','0644',str(base/'network.json'),bindir+'/network.json')
sh('sudo','-n','install','-m','0755',str(base/'src/tools/ci-network-attestation.py'),bindir+'/ci-network-attestation')
pins={}
for name in ['ci.yml','slow.yml']:
 pins['.forgejo/workflows/'+name]=hashlib.sha256((base/'zlib-src/.forgejo/workflows'/name).read_bytes()).hexdigest()
log=(base/'supervisor.log').open('a')
worker=subprocess.Popen(['sudo','-n','sh','-c','echo "$$" > /run/t29/w.pid; exec "$@"','sh',sup,'serve','--store',store,'--socket','/run/t29/w.sock','--admin-socket','/run/t29/admin.sock','--network-policy',bindir+'/network.json','--mem-dir','/run/t29/m','--netns-dir','/run/t29/n','--uid-base','2200000000','--allow-uid',str(os.getuid())],stdout=log,stderr=log)
grant=None
try:
 deadline=time.time()+60
 while not Path('/run/t29/w.sock').exists():
  if worker.poll() is not None or time.time()>deadline:raise RuntimeError('supervisor did not start')
  time.sleep(.05)
 pid=int(sh('sudo','-n','cat','/run/t29/w.pid').stdout)
 grant=json.loads(sh('sudo','-n',sup,'grant-install','--store',store,'--caller-uid',str(os.getuid()),'--ttl-s','7200','--max-generation','16').stdout)
 template=json.loads((base/os.environ.get('LAMP_T29_TEMPLATE','template.json')).read_text())['template_id']
 eps=','.join('10.219.29.1:%d'%p for p in range(3929,3936))
 repo=dict(system='forgejo',name='lamp-admin/zlib',id=1,owner_id=1,label='lamp-t29',template_id=template,runner_directory='/opt/forgejo-runner',
  base_url=forge,token_file=str(base/'forgejo-token'),project='1',workflow_sha256=pins,options=dict(private_endpoint=eps))
 gpin=hashlib.sha256((base/'zlib-gitea/.gitea/workflows/ci.yml').read_bytes()).hexdigest()
 repos=[dict(system='gitea',name='lamp-admin/zlib',id=1,owner_id=1,label='lamp-t29g',template_id=template,runner_directory='/opt/gitea-runner',base_url='http://10.219.29.1:3930',token_file=str(base/'gitea-token'),project='1',workflow_sha256={'.gitea/workflows/ci.yml':gpin},options=dict(private_endpoint=eps)),
  dict(system='jenkins',name='zlib',label='lamp-t29j',template_id=template,runner_directory='/opt/jenkins-agent',base_url='http://10.219.29.1:3931',token_file=str(base/'jenkins-token'),project='zlib',options=dict(private_endpoint=eps)),
  dict(system='woodpecker',name='lamp-admin/zlib-public',label='lamp-t29w',template_id=template,runner_directory='/opt/woodpecker-agent',base_url='http://10.219.29.1:3932',token_file=str(base/'woodpecker-token'),project='1',options=dict(private_endpoint=eps,grpc_address='10.219.29.1:3933'))]
 broker=None
 if policy_mode:
  # Host broker on the private bridge. Its one connection is a loopback
  # service, so the grant it issues is a real scoped bearer but reaches no
  # third party. The trusted job may get it; the public job never does.
  bd=base/'broker';subprocess.run(['rm','-rf',str(bd)]);bd.mkdir(mode=0o700)
  subprocess.run(['openssl','req','-x509','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-days','2','-subj','/CN=lamp-t29-broker','-addext','subjectAltName=IP:10.219.29.1','-keyout',str(bd/'key.pem'),'-out',str(bd/'cert.pem')],check=True,capture_output=True)
  (bd/'broker.json').write_text(json.dumps({'connections':{'svc':{'kind':'service','access':'full','endpoint':'http://127.0.0.1:3937','operations':{'ping':{'method':'GET','path':'/','write':False,'semantic_read':True}}}},'settings':{}}))
  (bd/'broker.json').chmod(0o600);(bd/'sock').mkdir(mode=0o700)
  broker=subprocess.Popen([str(base/'bin/lamp-broker'),'--config',str(bd/'broker.json'),'--owner-socket',str(bd/'sock/broker.sock'),'--task-listen','10.219.29.1:3936','--tls-cert',str(bd/'cert.pem'),'--tls-key',str(bd/'key.pem'),'--audit',str(bd/'audit.jsonl')],stderr=(bd/'broker.log').open('w'))
  time.sleep(2)
  mb=dict(owner_socket=str(bd/'sock/broker.sock'),endpoint='https://10.219.29.1:3936',ca_file=str(bd/'cert.pem'),peer_ip='10.219.29.1',workflows={'.gitea/workflows/ci.yml':{'svc':['ping']},'.woodpecker':{'svc':['ping']}})
  keep='gitea' if policy_mode=='trusted' else 'woodpecker'
  repos=[dict(r,retain_stopped=True,model_broker=mb) for r in repos if r['system']==keep]
 conf=base/'live.json'
 conf.write_text(json.dumps(dict(template=template,forge=forge,token_file=str(base/'forgejo-token'),repository=repo,repositories=repos if os.environ.get('LAMP_T29_ONLY')=='systems' or policy_mode else [],
  network_reader=['sudo','-n',bindir+'/ci-network-attestation',audit],state_admin=['sudo','-n',sup,'state','--admin-socket','/run/t29/admin.sock'],
  worker=dict(Socket='/run/t29/w.sock',WorkerID='lamp-supervisor@'+os.uname().nodename,BootID=Path('/proc/sys/kernel/random/boot_id').read_text().strip(),UID=0,PID=pid,GrantID=grant['grant_id'],Timeout=60000000000))))
 conf.chmod(0o600)
 env=dict(os.environ,LAMP_T29_GITLAB_DRIVER=str(base/'src/docs/development/evidence/t29/gitlab_demo.py'),LAMP_T29_ONLY=os.environ.get('LAMP_T29_ONLY',''),LAMP_T29_LIVE_CONFIG=str(conf),PATH='/home/johan/go-sdk/bin:'+os.environ['PATH'])
 p=subprocess.run(['go','test','./internal/control','-run','^TestT29Demonstration$','-count=1','-v','-timeout','60m'],cwd=base/'src',env=env)
 print('demonstration_exit',p.returncode,flush=True)
 if broker:
  broker.terminate();broker.wait();broker=None
  print('broker audit (grant lifecycle, redacted by the broker):',flush=True)
  for l in (base/'broker/audit.jsonl').read_text().splitlines():print('  '+l[:300],flush=True)
finally:
 if broker:broker.terminate()
 if worker.poll() is None:
  subprocess.run(['sudo','-n','kill','-TERM',str(pid)],check=False)
  worker.wait(timeout=90)
 if grant:subprocess.run(['sudo','-n',sup,'grant-revoke','--store',store,grant['grant_id']],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 print('firecracker_remaining',subprocess.run(['pgrep','-c','firecracker'],capture_output=True,text=True).stdout.strip(),flush=True)
