import concurrent.futures as futures
import json,os,socket,subprocess,tempfile,shutil,threading
from pathlib import Path
binary=os.environ.get('LAMP_DIAGNOSTIC_GUARD','/usr/local/lib/lamp/lamp-guard')
root=Path(tempfile.mkdtemp(prefix='lamp-t10-collector-',dir='/var/tmp'))
stop=threading.Event()
def one(index):
 if stop.is_set():return None
 d=root/str(index);d.mkdir()
 cfg=d/'config.json'
 cfg.write_text(json.dumps(dict(attribution=dict(principal_id='t10-diagnostic',host_id=os.uname().nodename,task_id=str(index),sandbox_id='t10-native-collector',generation=1,session_id='diagnostic',operation_id=str(index),policy_revision=1),interface='task0',guest_ip='198.18.240.2',queue=314,host_denies=['198.18.240.1/32'],spool=str(d/'audit.ndjson'),spool_bytes=65536,guard_uid=1000001)))
 a,b=socket.socketpair();r,w=os.pipe()
 sink=subprocess.Popen([binary,'receive',str(cfg),str(a.fileno())],pass_fds=(a.fileno(),),stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 collect=subprocess.Popen(['strace','-s','256','-e','trace=ptrace,write,wait4,exit_group','-o',str(d/'trace'),binary,'collect',str(b.fileno()),'1000000','status:'+str(w),'--','/bin/echo','t10-marker'],pass_fds=(b.fileno(),w),stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
 a.close();b.close();os.close(w)
 try:
  out,_=collect.communicate(timeout=30)
  with os.fdopen(r) as report:status=report.read()
  try:receiver=sink.wait(timeout=2)
  except subprocess.TimeoutExpired:sink.kill();receiver=sink.wait()
  result=dict(index=index,collector_exit=collect.returncode,report=status,stdout=out.decode(),receiver_exit=receiver)
  if collect.returncode or status!='0\n' or receiver:
   stop.set();(d/'result.json').write_text(json.dumps(result));return result
  shutil.rmtree(d);return dict(ok=True)
 finally:
  for p in (collect,sink):
   if p.poll() is None:p.kill();p.wait()
count=0;failed=[]
with futures.ThreadPoolExecutor(max_workers=32) as pool:
 for result in pool.map(one,range(6400)):
  if result is None:continue
  count+=1
  if result.get('ok')!=True:failed.append(result)
summary=dict(root=str(root),attempts=count,failures=failed,diagnostic=True,VMs_created=0)
(root/'summary.json').write_text(json.dumps(summary,indent=2))
print(json.dumps(summary),flush=True)
