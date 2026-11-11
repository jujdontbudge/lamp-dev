"""Start only disposable fleet control, private ingress and a public README app.
No worker/VM allocation is needed for this browser-transport demonstration.
"""
import hashlib,http.client,json,os,pathlib,secrets,socket,subprocess as sp,time
os.umask(0o077)
R=pathlib.Path('/var/tmp/lamp-t24-hosted-prod-ws7un5w6');B=pathlib.Path('/opt/lamp-t24/bin');data=json.loads(pathlib.Path('/tmp/lamp-t24-build/live-identities.json').read_text());ids=data['identities'];origins=data['origins'];processes=[]
root=secrets.token_hex(32);tokens={n:secrets.token_hex(32) for n in ids}
(R/'stop-live').unlink(missing_ok=True)
state_dir=R/('control-live-'+secrets.token_hex(4))
identity={'owner_id':'pr_t24_bootstrap','bootstrap_sha256':hashlib.sha256(root.encode()).hexdigest(),'subjects':[dict(v,sha256=hashlib.sha256(tokens[n].encode()).hexdigest()) for n,v in ids.items()]}
(R/'identity-live.json').write_text(json.dumps(identity))
for n,t in tokens.items():(R/(n+'-fleet-token')).write_text(t)
def launch(name,args):
 p=sp.Popen(list(map(str,args)),stdout=open(R/(name+'.stdout'),'w'),stderr=open(R/(name+'.stderr'),'w'));processes.append(p);return p
class UnixHTTP(http.client.HTTPConnection):
 def connect(self):self.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);self.sock.connect(str(R/'api-live.sock'))
def call(method,path,body=None,revision=None):
 c=UnixHTTP('lamp',timeout=20);headers={'Authorization':'Bearer '+root,'Content-Type':'application/json'}
 if method!='GET':headers['Idempotency-Key']=secrets.token_hex(16)
 if revision is not None:headers['If-Match']=str(revision)
 c.request(method,path,None if body is None else json.dumps(body),headers);r=c.getresponse();data=json.loads(r.read());c.close()
 if not 200<=r.status<300:raise RuntimeError('public fleet setup denied: '+str(r.status))
 return data
try:
 daemon=launch('lampd-live',['/tmp/lampd-t18-linux','--socket',R/'api-live.sock','--state-dir',state_dir,'--identity-file',R/'identity-live.json','--worker-socket',R/'unused-worker','--worker-id','unused','--worker-boot-id','unused','--worker-uid',os.getuid(),'--worker-pid',os.getpid(),'--grant-id','unused'])
 deadline=time.monotonic()+15
 while not (R/'api-live.sock').exists():
  if daemon.poll() is not None or time.monotonic()>deadline:raise RuntimeError('daemon not ready')
  time.sleep(.1)
 call('PATCH','/v1/fleets/fl_local',{'mode':'team','wait_ms':60000},1)
 for n,v in ids.items():
  principal=call('POST','/v1/principals',dict(v,wait_ms=60000))['result']['principal_id']
  call('POST','/v1/memberships',{'principal_id':principal,'role':'member','wait_ms':60000})
 users=[{'identity':v,'token_file':str(R/(n+'-fleet-token'))} for n,v in ids.items()]
 ingress={'listen':'127.0.0.1:7460','certificate':str(R/'renewed-chain.pem'),'key':str(R/'fleet-key.pem'),'account_origin':origins['account_origin'],'hosts':[{'origin':origins['api_origin'],'api_socket':str(R/'api-live.sock'),'users':users},{'origin':origins['content_origin'],'upstream':'http://127.0.0.1:7470','users':[{'identity':v} for v in ids.values()]}]}
 (R/'ingress-live.json').write_text(json.dumps(ingress));launch('ingress-live',[B/'lamp-ingress','--config',R/'ingress-live.json'])
 # The app receives no Lamp/Clerk cookie or bearer; it deliberately attempts a
 # parent-domain response cookie to demonstrate ingress filtering in real use.
 app=launch('public-readme-app',['python3','/tmp/lamp-t24-build/public-readme-app.py',str(R)])
 (R/'live-processes.json').write_text(json.dumps({str(p.pid):str(p.args[0]) for p in processes}))
 print(json.dumps({'fleet_ready':True,'private_state':str(R),'real_control_api':'fl_local','vms_allocated':0}),flush=True)
 deadline=time.monotonic()+3600
 while not (R/'stop-live').exists():
  if time.monotonic()>deadline:raise RuntimeError('disposable live journey expired')
  if any(p.poll() is not None for p in processes):raise RuntimeError('private journey process exited')
  time.sleep(.5)
finally:
 for p in reversed(processes):
  if p.poll() is None:p.terminate()
  try:p.wait(timeout=10)
  except sp.TimeoutExpired:p.kill();p.wait()
