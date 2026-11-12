import json,os,socket,subprocess,time
from pathlib import Path
base=Path.home()/'lamp-t26'
sup='/home/johan/lamp-t19/src/target/release/lamp-supervisor'
store='/var/lib/lamp/t26-private'
def sh(*args,**kw):return subprocess.run(args,check=True,capture_output=True,text=True,**kw)
source=base/'src';source.mkdir(exist_ok=True)
# Source is explicitly synchronized before each iteration; never overwrite it here.
policy=json.loads(sh('sudo','-n','python3','-c',"from pathlib import Path;print(Path('/var/lib/lamp/t10-private-bin/network.json').read_text())").stdout)
policy.update(guard='/var/lib/lamp/t19-private-bin/lamp-guard',audit_dir='/var/lib/lamp-audit/t26-private',require_allowlist=True,exceptions=[],subscription_domains=None,principal_id='pr_local',policy_layers=[],policy_ceiling=[dict(filter_mode='transport',default_egress='deny',allow=[dict(cidr='140.82.112.3/32',protocol='tcp',ports=[443])],deny=[])])
network=base/'network.json';network.write_text(json.dumps(policy))
sh('sudo','-n','install','-m','0644',str(network),'/var/lib/lamp/t26-private-bin/network.json')
sh('sudo','-n','install','-m','0755',str(base/'ci-network-attestation.py'),'/var/lib/lamp/t26-private-bin/ci-network-attestation')
log=(base/'supervisor.log').open('a')
worker=subprocess.Popen(['sudo','-n','sh','-c','echo "$$" > /run/t26/w.pid; exec "$@"','sh',sup,'serve','--store',store,'--socket','/run/t26/w.sock','--admin-socket','/run/t26/admin.sock','--network-policy','/var/lib/lamp/t26-private-bin/network.json','--mem-dir','/run/t26/m','--netns-dir','/run/t26/n','--uid-base','2100000000','--allow-uid',str(os.getuid())],stdout=log,stderr=log)
grant=None
try:
 deadline=time.time()+60
 while not Path('/run/t26/w.sock').exists():
  if worker.poll() is not None or time.time()>deadline:raise RuntimeError('supervisor did not start')
  time.sleep(.05)
 pid=int(sh('sudo','-n','python3','-c',"print(open('/run/t26/w.pid').read())").stdout)
 grant=json.loads(sh('sudo','-n',sup,'grant-install','--store',store,'--caller-uid',str(os.getuid()),'--ttl-s','3600','--max-generation','8').stdout)
 conf=base/'live-private.json'
 conf.write_text(json.dumps(dict(template=json.loads((base/'public-template.json').read_text())['template_id'],network_reader=['sudo','-n','/var/lib/lamp/t26-private-bin/ci-network-attestation','/var/lib/lamp-audit/t26-private'],state_admin=['sudo','-n',sup,'state','--admin-socket','/run/t26/admin.sock'],worker=dict(Socket='/run/t26/w.sock',WorkerID='lamp-supervisor@'+os.uname().nodename,BootID=Path('/proc/sys/kernel/random/boot_id').read_text().strip(),UID=0,PID=pid,GrantID=grant['grant_id'],Timeout=60000000000))))
 conf.chmod(0o600)
 env=dict(os.environ,LAMP_CI_TEMPLATE_READY='1',LAMP_CI_LIVE_CONFIG=str(conf),LAMP_CI_ZLIB_ARCHIVE=str(base/'zlib-v1.3.1.tar.gz'))
 p=subprocess.run(['go','test','./internal/control','-run','^TestCIControlledDemonstration$','-count=1','-v','-timeout','10m'],cwd=source,env=env)
 print('demonstration_exit',p.returncode,flush=True)
finally:
 if worker.poll() is None:
  subprocess.run(['sudo','-n','kill','-TERM',str(pid)],check=False)
  worker.wait(timeout=90)
 if grant:subprocess.run(['sudo','-n',sup,'grant-revoke','--store',store,grant['grant_id']],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 print('firecracker_remaining',subprocess.run(['pgrep','-c','firecracker'],capture_output=True,text=True).stdout.strip(),flush=True)
