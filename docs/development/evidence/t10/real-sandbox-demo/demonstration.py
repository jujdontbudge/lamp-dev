"""One real LX sandbox journey through HTTP, CLI and stdio MCP; no timing gate."""
import base64, hashlib, json, os, secrets, signal, subprocess, sys, time
from pathlib import Path
base=Path('/home/owner/lamp-t10-control-core')
source=base/'src'
root=base/'demonstration-1'
root.mkdir(mode=0o700,exist_ok=False)
private=root/'private';private.mkdir(mode=0o700)
sys.path.insert(0,str(source))
from benchmarks.public import PublicLamp
worker_binary='/usr/local/lib/lamp/lamp-supervisor'
store='/var/lib/lamp/t10-private'
policy='/var/lib/lamp/t10-private-bin/network.json'
worker_socket='/run/lamp/t10-private/worker.sock'
pidfile=Path('/run/lamp/t10-private/worker.pid')
template='tp_6b295ecbcad03c2a3b7370e577'
transcript=(root/'transcript.jsonl').open('x')
def record(step,value):
    transcript.write(json.dumps(dict(step=step,value=value),sort_keys=True)+'\n');transcript.flush()
def output(argv):return subprocess.check_output(argv,text=True).strip()
for name in ('lamp','lampd','lamp-mcp'):
    subprocess.run(['/home/owner/go-sdk/bin/go','build','-o',str(base/name),'./cmd/'+name],cwd=source,check=True)
artifacts={str(base/name):hashlib.sha256((base/name).read_bytes()).hexdigest() for name in ('lamp','lampd','lamp-mcp')}
for name in (worker_binary,'/var/lib/lamp/t10-private-bin/lamp-guard',policy):
    artifacts[name]=hashlib.sha256(Path(name).read_bytes()).hexdigest()
record('artifacts',dict(sha256=artifacts,template=template,seed='b4567a56bcde61d2c074a0773ca157079af2f24fd88acfb30faceab5133c3c9e',host=output(['hostname']),boot_id=Path('/proc/sys/kernel/random/boot_id').read_text().strip()))
check=subprocess.run(['python3',str(source/'infra/linux/admission-check.py')],capture_output=True,text=True)
record('allocation_guard',json.loads(check.stdout))
if check.returncode:raise RuntimeError(check.stderr)
subprocess.run(['sudo','-n','install','-d','-m','0755',str(pidfile.parent)],check=True)
worker_log=(root/'worker.log').open('x')
worker=subprocess.Popen(['sudo','-n','sh','-c','echo "$$" > "$1"; shift; exec "$@"','sh',str(pidfile),worker_binary,'serve','--store',store,'--socket',worker_socket,'--network-policy',policy,'--allow-uid',str(os.getuid())],stdout=worker_log,stderr=worker_log)
daemon=None;grant=None;client=None;sandbox=None;cleanup=[]
try:
    deadline=time.monotonic()+30
    while not Path(worker_socket).exists():
        if worker.poll() is not None or time.monotonic()>deadline:raise RuntimeError('worker did not become ready')
        time.sleep(.01)
    worker_pid=int(pidfile.read_text().strip())
    grant=json.loads(output(['sudo','-n',worker_binary,'grant-install','--store',store,'--caller-uid',str(os.getuid()),'--ttl-s','600']))
    bearer=secrets.token_hex(32)
    identity=private/'identity.json';identity.write_text(json.dumps(dict(owner_id='pr_t10_real_demo',bootstrap_sha256=hashlib.sha256(bearer.encode()).hexdigest())));identity.chmod(0o600)
    socket=str(private/'api.sock')
    daemon_log=(root/'lampd.log').open('x')
    daemon=subprocess.Popen([str(base/'lampd'),'--identity-file',str(identity),'--state-dir',str(private/'state'),'--socket',socket,'--worker-socket',worker_socket,'--worker-id','lamp-supervisor@'+output(['hostname']),'--worker-boot-id',Path('/proc/sys/kernel/random/boot_id').read_text().strip(),'--worker-uid','0','--worker-pid',str(worker_pid),'--grant-id',grant['grant_id'],'--worker-timeout','60s'],stdout=daemon_log,stderr=daemon_log)
    deadline=time.monotonic()+30
    while not Path(socket).exists():
        if daemon.poll() is not None or time.monotonic()>deadline:raise RuntimeError('daemon did not become ready')
        time.sleep(.01)
    client=PublicLamp(socket_path=socket,token=bearer,timeout=120)
    created=client.call('sandbox','create',dict(template_id=template,idempotency_key='real-demo-create',wait_ms=60000))
    record('HTTP sandbox.create',created)
    sandbox=created['result']['sandbox_id']
    resource=client.get(sandbox);record('HTTP sandbox.get ready',resource)
    if resource['state']!='ready':raise RuntimeError('sandbox not ready')
    record('independent live allocation',json.loads(output(['python3',str(source/'infra/linux/admission-check.py')])))
    nonce=secrets.token_hex(16);marker='/tmp/lamp-control-demo-'+nonce
    env=dict(os.environ,LAMP_SOCKET=socket,LAMP_TOKEN=bearer)
    def completed(reply,label):
        record(label+' acceptance',reply)
        identity=reply['result']['exec_id'];cursor=None;data=bytearray();deadline=time.monotonic()+60
        while True:
            page=client.call('exec','watch',dict(exec_id=identity,wait_ms=1000,**({'cursor':cursor} if cursor else {})))
            for frame in page['items']:
                if frame['channel']=='stdout':data.extend(base64.b64decode(frame['data_base64']))
            cursor=page.get('next_cursor')
            ex=client.call('exec','get',dict(exec_id=identity))['data']
            if ex['state']=='exited' and not cursor:
                value=json.loads(data);record(label+' real command completion',dict(exec=ex,stdout=value))
                if ex['exit_code']!=0:raise RuntimeError(label+' command failed')
                return value
            if ex['state'] in ('failed','outcome_unknown') or time.monotonic()>deadline:raise RuntimeError(label+' missing completion')
    cli_program='import json,sys;open(sys.argv[1],"w").write(sys.argv[2]);print(json.dumps({"transport":"CLI","written":sys.argv[2]}))'
    fields=dict(sandbox_id=sandbox,argv=['python3','-c',cli_program,marker,nonce],idempotency_key='real-demo-cli',wait_ms=60000)
    path=private/'cli-input.json';path.write_text(json.dumps(fields))
    cli=json.loads(subprocess.check_output([str(base/'lamp'),'exec','create','--input',str(path),'--json'],env=env,text=True))
    completed(cli,'CLI exec.create')
    mcp_program='import json,sys;value=open(sys.argv[1]).read();open(sys.argv[1],"a").write("/MCP");print(json.dumps({"transport":"MCP","read":value,"appended":"/MCP"}))'
    fields=dict(sandbox_id=sandbox,argv=['python3','-c',mcp_program,marker],idempotency_key='real-demo-mcp',wait_ms=60000)
    request=dict(jsonrpc='2.0',id=1,method='tools/call',params=dict(name='exec_create',arguments=fields))
    rpc=json.loads(subprocess.check_output([str(base/'lamp-mcp')],input=json.dumps(request)+'\n',env=env,text=True))
    record('stdio MCP tools/call',rpc)
    if rpc.get('result',{}).get('isError'):raise RuntimeError('MCP refused command')
    mcp=completed(rpc['result']['structuredContent'],'MCP exec_create')
    if mcp['read']!=nonce:raise RuntimeError('MCP did not read CLI effect')
    verify='import json,sys;print(json.dumps({"transport":"HTTP","guest_file":open(sys.argv[1]).read()}))'
    reply=client.call('exec','create',dict(sandbox_id=sandbox,argv=['python3','-c',verify,marker],idempotency_key='real-demo-http-read',wait_ms=60000))
    value=completed(reply,'HTTP exec.create')
    if value['guest_file']!=nonce+'/MCP':raise RuntimeError('HTTP did not observe both guest effects')
    client.delete(sandbox)
    record('HTTP sandbox.delete confirmed',client.get(sandbox))
    if client.get(sandbox)['state']!='deleted':raise RuntimeError('release not confirmed')
    record('demonstration complete',dict(transports=['HTTP API','lamp CLI JSON','lamp-mcp stdio JSON-RPC'],real_guest_effect=nonce+'/MCP',worker_pid=worker_pid,owner_services='untouched',timing='not measured'))
finally:
    if sandbox and client:
        try:
            if client.get(sandbox)['state']!='deleted':client.delete(sandbox)
            if client.get(sandbox)['state']!='deleted':raise RuntimeError('release not confirmed')
        except Exception as error:cleanup.append(str(error))
    (root/'cleanup.json').write_text(json.dumps(cleanup))
    if daemon is not None:
        daemon.terminate();daemon.wait(timeout=70);daemon_log.close()
    if grant:subprocess.run(['sudo','-n',worker_binary,'grant-revoke','--store',store,grant['grant_id']],check=True,stdout=subprocess.DEVNULL)
    if worker.poll() is None:
        subprocess.run(['sudo','-n','kill','-TERM',str(worker.pid)],check=True);worker.wait(timeout=70)
    worker_log.close();transcript.close()
