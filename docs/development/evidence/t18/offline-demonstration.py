import pathlib,tempfile,subprocess as sp,json,os,time,hashlib,socket
os.umask(0o077);root=pathlib.Path(tempfile.mkdtemp(prefix='lamp-t18-local-'));base=pathlib.Path('/tmp/lamp-t18-offline-bin');a=base/'lamp-access';processes=[]
print(json.dumps({'event':'network-namespace','routes':sp.check_output(['ip','route','show'],text=True).strip()}))
def cmd(*args,**kw):
 r=sp.run(list(map(str,args)),capture_output=True,text=True,**kw)
 if r.returncode:raise RuntimeError('command failed; inspect private run directory')
 return r.stdout
try:
 for name in ['host-ca','user-ca','user']:cmd(a,'init-ca','--out',root/name)
 bearer=os.urandom(32).hex();(root/'token').write_text(bearer);(root/'identity').write_text(json.dumps({'owner_id':'pr_t18','bootstrap_sha256':hashlib.sha256(bearer.encode()).hexdigest()}))
 daemon=sp.Popen([str(base/'lampd'),'--socket',str(root/'api.sock'),'--state-dir',str(root/'control'),'--identity-file',str(root/'identity'),'--worker-socket',str(root/'unused-worker'),'--worker-id','unused','--worker-boot-id','unused','--worker-uid',str(os.getuid()),'--worker-pid',str(os.getpid()),'--grant-id','unused'],stdout=sp.DEVNULL,stderr=open(root/'lampd.log','w'));processes.append(daemon)
 def port():
  s=socket.socket();s.bind(('127.0.0.1',0));p=s.getsockname()[1];s.close();return p
 sshport=port();httpport=port()
 c={'state_dir':str(root/'access'),'api_socket':str(root/'api.sock'),'ssh_listen':f'127.0.0.1:{sshport}','http_listen':f'127.0.0.1:{httpport}','host_ca_file':str(root/'host-ca'),'user_ca_file':str(root/'user-ca'),'hostnames':['lamp-local.eilmer.si'],'users':[{'name':'owner','authorized_keys':[(root/'user.pub').read_text().strip()],'token_file':str(root/'token')}]}
 (root/'config').write_text(json.dumps(c));(root/'known_hosts').write_text('@cert-authority lamp-local.eilmer.si '+(root/'host-ca.pub').read_text())
 edge=sp.Popen([str(a),'serve','--config',str(root/'config')],stdout=open(root/'access.stdout','w'),stderr=open(root/'access.stderr','w'));processes.append(edge)
 for _ in range(100):
  if daemon.poll() is not None or edge.poll() is not None:raise RuntimeError('daemon or edge stopped: '+str(root))
  if (root/'api.sock').exists() and (root/'access.stdout').stat().st_size:break
  time.sleep(.1)
 ssh=['ssh','-F','/dev/null','-o','BatchMode=yes','-o','IdentitiesOnly=yes','-o','HostKeyAlias=lamp-local.eilmer.si','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+str(root/'known_hosts'),'-i',str(root/'user'),'-p',str(sshport),'owner@127.0.0.1']
 deny=sp.run(ssh+['uname -a'],capture_output=True,text=True,timeout=15);print(json.dumps({'event':'host-command-denied','exit':deny.returncode,'stderr':deny.stderr.strip()}))
 listing=sp.run(ssh+['sessions'],capture_output=True,text=True,timeout=15);print(json.dumps({'event':'sessions-backend','exit':listing.returncode,'stderr':listing.stderr.strip()}))
 proxy=sp.run(ssh[:-1]+['-o',f'ProxyCommand={a} connect --url http://127.0.0.1:{httpport}/ssh',ssh[-1],'uname -a'],capture_output=True,text=True,timeout=15);print(json.dumps({'event':'websocket-openssh','exit':proxy.returncode,'stderr':proxy.stderr.strip()}))
 # No external network route configured; exercise the authenticated API.
 r=cmd('curl','-fsS','-H','Authorization: Bearer '+bearer,f'http://127.0.0.1:{httpport}/v1/fleets/fl_local');print(json.dumps({'event':'local-api','fleet':json.loads(r)['data']['id']}))
 forward=sp.run(ssh[:-1]+['-W','127.0.0.1:22',ssh[-1]],capture_output=True,text=True,timeout=15);print(json.dumps({'event':'forwarding-denied','exit':forward.returncode,'stderr':forward.stderr.strip()}))
 print(json.dumps({'event':'local-demo','root':str(root),'no_external_routes':True,'host_ca_verified':True}))
finally:
 for p in reversed(processes):
  if p.poll() is None:p.terminate()
  try:p.wait(timeout=10)
  except sp.TimeoutExpired:p.kill();p.wait()
