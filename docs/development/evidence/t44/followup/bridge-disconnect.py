import json,pathlib,socket,subprocess,sys,tempfile,time
source=pathlib.Path(sys.argv[1]).resolve()
with tempfile.TemporaryDirectory(prefix='t44-bridge-') as d:
 endpoint=d+'/io.sock';root=d+'/sessions'
 p=subprocess.Popen([sys.executable,str(source),'--serve',endpoint,root],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 try:
  until=time.monotonic()+5
  while not pathlib.Path(endpoint).exists():
   assert p.poll() is None,'bridge failed startup'
   assert time.monotonic()<until
   time.sleep(.01)
  with socket.socket(socket.AF_UNIX) as s:
   s.connect(endpoint);s.sendall(json.dumps({'action':'exec','argv':[sys.executable,'-c','import time;time.sleep(.2)'],'timeout_ms':1000}).encode()+b'\n')
  time.sleep(.5)
  result={'bridge_exit_after_disconnected_caller':p.poll()}
  try:
   with socket.socket(socket.AF_UNIX) as s:
    s.settimeout(3);s.connect(endpoint);s.sendall(json.dumps({'action':'exec','argv':[sys.executable,'-c','print(42)'],'timeout_ms':1000}).encode()+b'\n');result['next_reply']=json.loads(s.makefile().readline())
  except OSError as e:result['next_call_error']=type(e).__name__
  print(json.dumps(result))
 finally:
  if p.poll() is None:p.terminate()
  p.communicate(timeout=5)
