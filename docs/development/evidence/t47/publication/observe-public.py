from pathlib import Path
import subprocess,sys,os,json,time,hashlib,base64,platform,shutil
label=sys.argv[1];prefix=Path(sys.argv[2]);out=Path(sys.argv[3]);out.mkdir(parents=True,exist_ok=True)
assert not prefix.exists()
r={'platform':label,'host':platform.platform(),'observations':[],'errors':[]};owned=[]
def save():out.joinpath('result.json').write_text(json.dumps(r,indent=2)+'\n')
def run(argv,**kw):
 p=subprocess.run(list(map(str,argv)),text=True,capture_output=True,timeout=kw.pop('timeout',300),**kw)
 if p.returncode:raise RuntimeError(f'{Path(str(argv[0])).name} {argv[1]} failed ({p.returncode}): '+p.stderr[-1800:])
 return p.stdout
bins=prefix/'current/bin';lamp=bins/'lamp';inst=bins/'lamp-installer'
def cli(*args):return json.loads(run([lamp,*args,'--json']))
def resource(d,kind):
 if d.get('id','').startswith({'sandbox':'sb_','connection':'cn_'}[kind]):return d['id']
 for obj in d.get('resource_ids',[]):
  if obj['kind']==kind:return obj['id']
 if isinstance(d.get('result'),dict):return resource(d['result'],kind)
 raise ValueError('missing resource id: '+json.dumps(d)[:500])
def execute(sb,code):return run([lamp,'sandbox','exec',sb,'--','python3','-c',code])
def delete(sb):
 run([lamp,'sandbox','delete',sb,'--wait-ms','60000','--json']);owned.remove(sb)
try:
 t=time.monotonic();script=out/'install.sh';run(['curl','-fsSL','https://lamp.eilmer.si/install.sh','-o',script])
 env={**os.environ,'LAMP_PREFIX':str(prefix),'LAMP_SERVICE':'process'}
 p=subprocess.run(['sh',str(script)],env=env,text=True,capture_output=True,timeout=600)
 (out/'install.log').write_text(p.stdout+p.stderr)
 if p.returncode:raise RuntimeError('public installer failed; see install.log')
 r['install_script_sha256']=hashlib.sha256(script.read_bytes()).hexdigest()
 r['installed_version']=cli('version');r['installer_version']=run([inst,'--version']).strip()
 r['binary_versions']={name:run([bins/name,'--version']).strip() for name in ['lampd','lamp-supervisor']}
 try:
  discovery=cli('--help');r['cli_discovery']=discovery
  api=cli('api','describe');r['openapi_info']=api.get('info',api.get('data',{}).get('info'))
  requests=[{'jsonrpc':'2.0','id':1,'method':'initialize'},{'jsonrpc':'2.0','id':2,'method':'tools/list'},{'jsonrpc':'2.0','id':3,'method':'tools/call','params':{'name':'version_get','arguments':{}}}]
  responses=[json.loads(line) for line in run([bins/'lamp-mcp'],input=''.join(json.dumps(x)+'\n' for x in requests)).splitlines()]
  r['mcp_discovery']=[{'id':x['id'],'result':({'tool_count':len(x['result']['tools']),'tools':[t['name'] for t in x['result']['tools']]} if x['id']==2 and 'result' in x else x.get('result')), 'error':x.get('error')} for x in responses]
 except Exception as e:r['errors'].append('connected discovery: '+str(e))
 r['quickstart_output']=run([lamp,'sandbox','run','--','python3','-c','import platform; print("hello from Lamp 0.1.2"); print(platform.machine()); print(6*7)'])
 r['install_through_code_seconds']=time.monotonic()-t;save()
 # One release observation per operation, not a p50 distribution or a gate.
 started=time.monotonic();d=cli('sandbox','create','--resources','{"vcpus":1,"memory_mib":512,"disk_gib":20,"pids_max":4096}','--wait-ms','60000');sb=resource(d,'sandbox');owned.append(sb)
 useful=execute(sb,'print("release-create-useful")').strip();r['create_to_useful_ms']=(time.monotonic()-started)*1000;r['create_witness']=useful
 details=cli('sandbox','get',sb);r['resources']=details.get('resources',details.get('data',{}).get('resources'));save()
 workload=Path(sys.argv[4]).read_text();work='/tmp/lamp-release-workload.py'
 execute(sb,'from pathlib import Path; Path('+repr(work)+').write_text('+repr(workload)+')')
 def guest(*args):return json.loads(run([lamp,'sandbox','exec',sb,'--','python3',work,*args]))
 r['workload_sha256']=hashlib.sha256(workload.encode()).hexdigest()
 nonce='lamp-0.1.2-release-'+label
 before=guest('start','--path','/tmp/lamp-release-resident','--nonce',nonce,'--bytes',str(1<<20));save()
 started=time.monotonic()
 try:
  fd=cli('sandbox','fork',sb,'--fork-mode','memory','--durability','local','--wait-ms','60000');child=resource(fd,'sandbox');owned.append(child)
  child_state=json.loads(run([lamp,'sandbox','exec',child,'--','python3',work,'call','--path','/tmp/lamp-release-resident']))
  r['live_fork_to_useful_ms']=(time.monotonic()-started)*1000
  parent_state=guest('call','--path','/tmp/lamp-release-resident')
  r['live_fork_witness']={'before':before,'child':child_state,'parent':parent_state}
  assert child_state['nonce']==parent_state['nonce']==nonce
  assert parent_state['ticks']>before['ticks'] and child_state['ticks']>before['ticks']
  delete(child)
 except Exception as e:r['errors'].append('single live-fork observation: '+str(e));r['live_fork_elapsed_to_error_ms']=(time.monotonic()-started)*1000
 save()
 r['native_speed']={}
 try:r['toolchains']={'native':json.loads(run([sys.executable,sys.argv[4],'identity'])),'sandbox':guest('identity')}
 except Exception as e:r['errors'].append('toolchain metadata: '+str(e))
 for name in ['compile','compression','crypto','seq_write','fsync']:
  try:
   native=json.loads(run([sys.executable,sys.argv[4],'measure','--measure',name,'--directory',str(out)]))
   sandbox=guest('measure','--measure',name,'--directory','/tmp')
   ratio=native['value']/sandbox['value'] if name=='fsync' else sandbox['value']/native['value']
   r['native_speed'][name]={'native':native,'sandbox':sandbox,'sandbox_over_native':ratio}
  except Exception as e:r['native_speed'][name]={'error':str(e)}
  save()
 # Bounded default-install observation; no model-provider custody is enrolled.
 token=prefix.joinpath('config/owner.token').read_text().strip().encode()
 env_bytes=base64.b64decode(execute(sb,'import base64; print(base64.b64encode(open("/proc/self/environ","rb").read()).decode())').strip())
 r['zero_leak']={'control_owner_token_in_guest_process_environment':token in env_bytes,'scope':'single default guest process environment only; no complete memory/disk/provider-custody measurement'}
 fixture=None;connection=None
 try:
  fixture=subprocess.Popen([sys.executable,sys.argv[4],'serve-network'],text=True,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
  ports=json.loads(fixture.stdout.readline());port=ports['tcp_port']
  cd=cli('connection','create','--type','service','--endpoint','127.0.0.1:'+str(port),'--access','full','--resource-policy',json.dumps({'source_sandbox_id':sb}),'--wait-ms','60000')
  connection=resource(cd,'connection');details=cli('connection','get',connection)
  endpoint=details.get('endpoint') or details.get('data',{}).get('endpoint')
  host,guest_port=endpoint.rsplit(':',1)
  native=json.loads(run([sys.executable,sys.argv[4],'measure','--measure','tcp_goodput','--fixture',json.dumps({'host':'127.0.0.1','tcp_port':port,'udp_port':0})]))
  sandbox=guest('measure','--measure','tcp_goodput','--fixture',json.dumps({'host':host,'tcp_port':int(guest_port),'udp_port':0}))
  r['native_speed']['tcp_goodput']={'native':native,'sandbox':sandbox,'sandbox_over_native':sandbox['value']/native['value'],'scope':'native loopback versus guarded owner-service TCP alias; diagnostic, not independent remote wire-speed parity'}
 except Exception as e:r['native_speed']['tcp_goodput']={'error':str(e),'scope':'loopback versus guarded owner-service diagnostic'}
 finally:
  if connection:
   try:cli('connection','revoke',connection,'--wait-ms','60000')
   except Exception as e:r['errors'].append('connection cleanup: '+str(e))
  if fixture:fixture.terminate();fixture.wait(timeout=10)
 save()
except Exception as e:r['errors'].append(str(e));save()
finally:
 if lamp.exists():
  for sb in owned.copy():
   try:delete(sb)
   except Exception as e:r['errors'].append('sandbox cleanup: '+str(e))
  try:
   status=json.loads(run([inst,'status','--prefix',prefix,'--json']))
   p=subprocess.run([str(inst),'uninstall','--prefix',str(prefix),'--data','delete','--confirm-delete',status['name']],text=True,capture_output=True,timeout=180)
   (out/'cleanup.log').write_text(p.stdout+p.stderr);r['cleanup_exit']=p.returncode;r['prefix_removed']=not prefix.exists()
  except Exception as e:r['errors'].append('uninstall: '+str(e))
 save()
print(json.dumps({'platform':label,'result':str(out/'result.json'),'errors':r['errors'],'quickstart':r.get('quickstart_output'),'prefix_removed':r.get('prefix_removed')}))
