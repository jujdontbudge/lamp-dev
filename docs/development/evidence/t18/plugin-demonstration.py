import os,pathlib,tempfile,subprocess as sp,json,hashlib,socket
os.umask(0o077);root=pathlib.Path(tempfile.mkdtemp(prefix='lamp-t18-plugin-'));base=pathlib.Path.cwd();a=base/'bin/lamp-access';p=base/'bin/lamp-access-cloudflare';launcher=base/'bin/lamp-plugin-launcher';cloudflared=pathlib.Path('/opt/homebrew/bin/cloudflared')
def cmd(*args):
 r=sp.run(list(map(str,args)),capture_output=True,text=True)
 if r.returncode:raise RuntimeError('private setup command failed')
 return r.stdout
for name in ['host-ca','user-ca']:cmd(a,'init-ca','--out',root/name)
cmd(a,'cloudflare-manifest','--plugin',p,'--out',root/'manifest')
(root/'token').write_text('fixture-token-not-a-real-credential');(root/'invalid-credentials').write_text('{}')
s=socket.socket();s.bind(('127.0.0.1',0));port=s.getsockname()[1];s.close()
digest=lambda path:hashlib.sha256(path.read_bytes()).hexdigest()
c={'state_dir':str(root/'state'),'api_socket':str(root/'unused.sock'),'http_listen':f'127.0.0.1:{port}','host_ca_file':str(root/'host-ca'),'user_ca_file':str(root/'user-ca'),'hostnames':['lamp-t18.eilmer.si'],'users':[{'name':'fixture','token_file':str(root/'token')}],'cloudflare':{'executable':str(cloudflared),'executable_sha256':digest(cloudflared),'tunnel_id':'00000000-0000-4000-8000-000000000018','credentials_file':str(root/'invalid-credentials'),'hostname':'lamp-t18.eilmer.si','plugin':str(p),'manifest':str(root/'manifest'),'manifest_sha256':digest(root/'manifest'),'launcher':str(launcher),'launcher_sha256':digest(launcher),'diagnostic_soft_limits':True}}
(root/'config').write_text(json.dumps(c))
import select,signal
process=sp.Popen([str(a),'serve','--config',str(root/'config')],stdout=sp.PIPE,stderr=sp.PIPE,text=True)
observations=[]
try:
 while True:
  if not select.select([process.stdout],[],[],20)[0]:raise RuntimeError('plugin startup unavailable')
  line=process.stdout.readline()
  if not line:raise RuntimeError('service exited before connector start')
  event=json.loads(line);observations.append(event)
  if event.get('route')=='cloudflare-connector-started':break
 # Deliberately end only this disposable connector; the edge must report loss.
 rows=sp.check_output(['ps','-ax','-o','pid=,command='],text=True).splitlines()
 pids=[int(row.strip().split(None,1)[0]) for row in rows if str(root/'state/cloudflared-executable-') in row]
 assert len(pids)==1
 os.kill(pids[0],signal.SIGKILL)
 stdout,stderr=process.communicate(timeout=15)
 print(json.dumps({'event':'cloudflare-plugin-integration','root':str(root),'observations':observations,'exit':process.returncode,'error':stderr.strip(),'fault':'owned disposable connector explicitly killed','credentials':'intentionally invalid; no live tunnel claimed'}))
 assert process.returncode!=0 and 'cloudflare connector exited' in stderr
finally:
 if process.poll() is None:process.terminate();process.communicate(timeout=15)
