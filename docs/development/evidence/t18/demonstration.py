"""One T18 journey. No trial, timing or benchmark modes."""
import base64,hashlib,json,os,pathlib,secrets,socket,subprocess as sp,time,ipaddress
os.umask(0o077);R=pathlib.Path(__import__('tempfile').mkdtemp(prefix='lamp-t18-demo-',dir='/var/tmp'));A='/tmp/lamp-access-t18-linux';D='/tmp/lampd-t18-linux';W='/usr/local/lib/lamp/lamp-supervisor';STORE='/var/lib/lamp/t10-private';HEAD='/var/tmp/lamp-t12-private-zRoLlfsQ/headscale_0.29.7_linux_amd64';processes=[];sandbox=None;grant=None;workerpid=None
log=(R/'observations.jsonl').open('x')
def record(event,**value):
 v={'event':event,**value};log.write(json.dumps(v)+'\n');log.flush();print(json.dumps(v),flush=True)
def cmd(*args,**kw):
 r=sp.run(list(map(str,args)),capture_output=True,text=True,**kw)
 if r.returncode:raise RuntimeError('command failed; inspect private run directory')
 return r.stdout.strip()
def port():
 with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]
def write(name,v):p=R/name;p.write_text(json.dumps(v) if not isinstance(v,str) else v);return p
def launch(name,args):
 p=sp.Popen(list(map(str,args)),stdout=open(R/(name+'.stdout'),'w'),stderr=open(R/(name+'.stderr'),'w'));processes.append(p);return p
def waitfile(p,proc):
 deadline=time.monotonic()+40
 while not p.exists():
  if proc.poll() is not None or time.monotonic()>deadline:raise RuntimeError('readiness failed: '+str(p))
  time.sleep(.1)
def call(method,path,body=None):
 args=['curl','--fail-with-body','--silent','--show-error','--unix-socket',str(R/'api.sock'),'-H','Authorization: Bearer '+bearer,'-H','Content-Type: application/json','-X',method]
 if body is not None:args+=['-H','Idempotency-Key: '+secrets.token_hex(16),'--data',json.dumps(body)]
 return json.loads(cmd(*args,'http://lamp'+path))
try:
 record('demonstrated-binaries',sha256={p:hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest() for p in (A,D,W)})
 for n in ['host-ca','user-ca','user']:cmd(A,'init-ca','--out',R/n)
 (R/'known_hosts').write_text('@cert-authority lamp-t18.eilmer.si '+(R/'host-ca.pub').read_text())
 bearer=secrets.token_hex(32);write('token',bearer);write('identity',{'owner_id':'pr_t18_demo','bootstrap_sha256':hashlib.sha256(bearer.encode()).hexdigest()})
 # Use the already prepared T10 template under its store lock. No template build.
 pidfile=R/'worker.pid'
 worker=launch('worker',['sudo','-n','sh','-c','echo "$$" > "$1"; shift; exec "$@"','sh',pidfile,W,'serve','--store',STORE,'--socket',R/'worker.sock','--mem-dir','/run/lamp-t18/mem','--netns-dir','/run/lamp-t18/netns','--uid-base','2180000000','--network-policy','/var/lib/lamp/t10-private-bin/network.json','--allow-uid',str(os.getuid())])
 waitfile(R/'worker.sock',worker);workerpid=int(cmd('sudo','-n','cat',pidfile))
 grant=json.loads(cmd('sudo','-n',W,'grant-install','--store',STORE,'--caller-uid',str(os.getuid()),'--ttl-s','1200'))
 daemon=launch('lampd',[D,'--identity-file',R/'identity','--socket',R/'api.sock','--state-dir',R/'control','--worker-socket',R/'worker.sock','--worker-id','lamp-supervisor@'+socket.gethostname(),'--worker-boot-id',pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip(),'--worker-uid','0','--worker-pid',workerpid,'--grant-id',grant['grant_id'],'--worker-timeout','1m']);waitfile(R/'api.sock',daemon)
 # Immediate owner limit check, directly before the sole create.
 count=sp.run(['pgrep','-c','firecracker'],capture_output=True,text=True);live=int(count.stdout.strip() or '0');available=int(next(l.split()[6] for l in cmd('free','-m').splitlines() if l.startswith('Mem:')))
 record('allocation-admission',firecracker_count=live,available_mib=available)
 if live>=2 or available<12288:raise RuntimeError('owner sandbox admission denied')
 create=call('POST','/v1/sandboxes',{'template_id':'tp_6b295ecbcad03c2a3b7370e577','wait_ms':60000});sandbox=create['result']['sandbox_id'];record('sandbox-created',sandbox_id=sandbox,state=call('GET','/v1/sandboxes/'+sandbox)['data']['state'])
 sshport=port();httpport=port();base={'state_dir':str(R/'access'),'api_socket':str(R/'api.sock'),'ssh_listen':f'127.0.0.1:{sshport}','http_listen':f'127.0.0.1:{httpport}','host_ca_file':str(R/'host-ca'),'user_ca_file':str(R/'user-ca'),'hostnames':['lamp-t18.eilmer.si'],'users':[{'name':'owner','authorized_keys':[(R/'user.pub').read_text().strip()],'token_file':str(R/'token'),'tailnet_login':'lamp-t18'}]}
 config=write('config',base);edge=launch('access-local',[A,'serve','--config',config]);waitfile(R/'access/ssh-host',edge);time.sleep(.3)
 ssh=['ssh','-F','/dev/null','-o','BatchMode=yes','-o','ConnectTimeout=20','-o','IdentitiesOnly=yes','-o','HostKeyAlias=lamp-t18.eilmer.si','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+str(R/'known_hosts'),'-i',str(R/'user'),'-p',str(sshport)]
 nonce=secrets.token_hex(12);line='printf '+nonce+' > /tmp/lamp-t18-marker; cat /tmp/lamp-t18-marker\nexit\n'
 direct=cmd(*ssh,'owner@127.0.0.1','shell '+sandbox,input=line,timeout=65);assert direct==nonce;record('local-openssh-guest-shell',stdout=direct,host_ca_verified=True)
 status=sp.run(ssh+['owner@127.0.0.1','shell '+sandbox],input='exit 37\n',capture_output=True,text=True,timeout=65);assert status.returncode==37;record('guest-exit-status',ssh_exit=status.returncode)
 websocket=cmd(*ssh,'-o',f'ProxyCommand={A} connect --url http://127.0.0.1:{httpport}/ssh','owner@127.0.0.1','shell '+sandbox,input='cat /tmp/lamp-t18-marker\nexit\n',timeout=65);assert websocket==nonce;record('websocket-openssh-guest-shell',stdout=websocket)
 for args,event in [(['owner@127.0.0.1','uname -a'],'host-command-denied'),(['-W','127.0.0.1:22','owner@127.0.0.1'],'forwarding-denied'),(['owner@127.0.0.1','sessions'],'session-backend-unavailable')]:
  r=sp.run(ssh+args,capture_output=True,text=True,timeout=30);record(event,exit=r.returncode,stderr=r.stderr.strip());assert r.returncode!=0
 edge.terminate();edge.wait(timeout=15)
 # Isolated userspace tailnet. Never change the owner's system Tailscale or ACL.
 existing=json.loads(cmd('tailscale','status','--json'));ips=existing.get('TailscaleIPs',[])+[ip for p in existing.get('Peer',{}).values() for ip in p.get('TailscaleIPs',[])]
 assert not any(ipaddress.ip_address(ip) in ipaddress.ip_network('100.127.240.0/24') for ip in ips if ':' not in ip)
 controlport=port();control=f'http://127.0.0.1:{controlport}'
 hs={'server_url':control,'listen_addr':f'127.0.0.1:{controlport}','metrics_listen_addr':'','grpc_listen_addr':'127.0.0.1:0','noise':{'private_key_path':str(R/'noise.key')},'prefixes':{'v4':'100.127.240.0/24','v6':'fd7a:115c:a1e0:ffff::/64','allocation':'sequential'},'derp':{'server':{'enabled':False},'urls':['https://controlplane.tailscale.com/derpmap/default'],'auto_update_enabled':False},'disable_check_updates':True,'database':{'type':'sqlite','sqlite':{'path':str(R/'hs.sqlite')}},'policy':{'mode':'file','path':''},'dns':{'magic_dns':True,'base_domain':'t18-tailnet.eilmer.si','override_local_dns':True,'nameservers':{'global':['1.1.1.1']}},'unix_socket':str(R/'hs.sock'),'unix_socket_permission':'0600','log':{'level':'error','format':'json'}}
 hscfg=write('headscale.json',hs);head=launch('headscale',[HEAD,'--config',hscfg,'serve']);waitfile(R/'hs.sock',head)
 user=json.loads(cmd(HEAD,'--config',hscfg,'users','create','lamp-t18','-o','json'));auth=json.loads(cmd(HEAD,'--config',hscfg,'preauthkeys','create','--user',user['id'],'--reusable','--ephemeral','--expiration','1h','-o','json'))['key'];write('auth-key',auth)
 tsport=2222;thport=8080;base['tailscale']={'hostname':'lamp-t18-edge','control_url':control,'auth_key_file':str(R/'auth-key'),'auth_url_file':str(R/'edge-auth-url'),'ssh_port':tsport,'http_port':thport};config=write('config',base);edge=launch('access-tailnet',[A,'serve','--config',config])
 deadline=time.monotonic()+65
 while True:
  if edge.poll() is not None or time.monotonic()>deadline:raise RuntimeError('tailnet listener not ready')
  if 'tailscale-http' in (R/'access-tailnet.stdout').read_text():break
  time.sleep(.2)
 nodes=json.loads(cmd(HEAD,'--config',hscfg,'nodes','list','-o','json'));edge_node=next(n for n in nodes if n['given_name']=='lamp-t18-edge');ip=next(a for a in edge_node['ip_addresses'] if ':' not in a);record('isolated-tailnet-enrolled',addresses=edge_node['ip_addresses'],control='owner Headscale 0.29.7',system_tailnet='untouched')
 proxy=f'{A} tailnet-connect --state-dir {R}/client --hostname lamp-t18-client --control-url {control} --auth-key-file {R}/auth-key --auth-url-file {R}/client-auth-url --target {ip}:{tsport}'
 result=cmd(*ssh,'-o','ProxyCommand='+proxy,'owner@127.0.0.1','shell '+sandbox,input='cat /tmp/lamp-t18-marker\nexit\n',timeout=80);assert result==nonce;record('tailnet-openssh-guest-shell',stdout=result,system_tun=False)
 # The same peer's cryptographic WhoIs identity is exchanged for a short-lived SSH user certificate.
 pub=(R/'user.pub').read_text();body=json.dumps({'user':'owner','public_key':pub});http='POST /ssh/certificate HTTP/1.1\r\nHost: lamp-t18\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: '+str(len(body.encode()))+'\r\n\r\n'+body
 request=proxy.replace(f'{ip}:{tsport}',f'{ip}:{thport}').split();response=cmd(*request,input=http,timeout=60)
 if '200 OK' not in response.splitlines()[0]:record('tailnet-certificate-denied',status=response.splitlines()[0]);raise RuntimeError('tailnet identity mapping rejected')
 cert=response.split('\n\n',1)[1];write('user-cert.pub',cert)
 cert_info=cmd('ssh-keygen','-L','-f',R/'user-cert.pub');record('tailnet-user-certificate',certificate=cert_info)
 # Prove the certificate without the public key allowlist by restarting edge configuration.
 edge.terminate();edge.wait(timeout=15);base['users'][0]['authorized_keys']=[];write('config',base);edge=launch('access-certificate',[A,'serve','--config',config]);time.sleep(1)
 result=cmd(*ssh,'-o','CertificateFile='+str(R/'user-cert.pub'),'owner@127.0.0.1','shell '+sandbox,input='cat /tmp/lamp-t18-marker\nexit\n',timeout=60);assert result==nonce;record('ephemeral-certificate-openssh-guest-shell',stdout=result,authorized_key_allowlist=False)
 record('demonstration-complete',root=str(R),features=['local/offline route','guest command console','SSH over WebSocket','embedded Tailscale/Headscale','WhoIs-to-SSH user certificate','fleet SSH host CA','host shell and forwarding denied'])
finally:
 if sandbox:
  try:
   sb=call('GET','/v1/sandboxes/'+sandbox)['data'];args=['curl','--fail-with-body','-sS','--unix-socket',str(R/'api.sock'),'-H','Authorization: Bearer '+bearer,'-H','Idempotency-Key: '+secrets.token_hex(16),'-H','If-Match: '+str(sb['revision']),'-X','DELETE','--data','{"wait_ms":60000}','http://lamp/v1/sandboxes/'+sandbox];deleted=json.loads(cmd(*args));record('sandbox-released',state=call('GET','/v1/sandboxes/'+sandbox)['data']['state'])
  except Exception as e:record('cleanup-failure',error=type(e).__name__)
 for p in reversed(processes):
  if p.poll() is None:
   if p is worker:
    if workerpid is None:workerpid=int(cmd('sudo','-n','cat',pidfile))
    sp.run(['sudo','-n','kill','-TERM',str(workerpid)],check=True)
   else:p.terminate()
  try:p.wait(timeout=20)
  except sp.TimeoutExpired:p.kill();p.wait()
 if grant:sp.run(['sudo','-n',W,'grant-revoke','--store',STORE,grant['grant_id']],capture_output=True,check=True)
 record('cleanup-complete',firecracker_count=int(sp.run(['pgrep','-c','firecracker'],capture_output=True,text=True).stdout.strip() or '0'),owner_services='untouched')
 log.close()
