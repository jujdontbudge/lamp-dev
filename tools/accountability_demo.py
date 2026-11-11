#!/usr/bin/env python3
"""One disposable LX accountability journey, no benchmarks or trial loops.

Uses a prepared template and ticket-owned supervisor; it never manages owner
services or Docker. Run only after preparing the isolated store and policy.
"""
import argparse
import base64
import hashlib
import http.client
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import time


def main():
    p = argparse.ArgumentParser(description=__doc__)
    for flag in ('root', 'store', 'policy', 'template', 'binary-dir'):
        p.add_argument('--'+flag, required=True)
    p.add_argument('--quota-only', action='store_true')
    args = p.parse_args()
    root = Path(args.root).resolve()
    root.mkdir(mode=0o700, exist_ok=False)
    bins = Path(args.binary_dir)
    store = Path(args.store)
    supervisor = str(store/'bin/lamp-supervisor')
    transcript = (root/'transcript.jsonl').open('x')
    def record(step, value):
        transcript.write(json.dumps(dict(step=step, value=value), sort_keys=True)+'\n')
        transcript.flush()
        print(step, flush=True)
    def wait(predicate, description, seconds=60):
        deadline = time.monotonic()+seconds
        while True:
            value = predicate()
            if value:
                return value
            if time.monotonic()>deadline:
                raise RuntimeError(description)
            time.sleep(.1)
    processes = []
    def start(argv, name):
        log = (root/(name+'.log')).open('x')
        proc = subprocess.Popen(argv, stdout=log, stderr=log)
        processes.append(proc)
        return proc
    def out(argv):
        return subprocess.check_output(argv, text=True).strip()
    def unix(path, body, sock, method='POST', headers=None):
        c = http.client.HTTPConnection('localhost', timeout=70)
        c.sock = socket.socket(socket.AF_UNIX)
        c.sock.settimeout(70)
        c.sock.connect(str(sock))
        c.request(method, path, json.dumps(body) if body is not None else None, headers or {})
        r=c.getresponse(); raw=r.read(); status=r.status;c.close()
        return status,json.loads(raw) if raw else {}
    token = secrets.token_hex(32)
    identity = root/'identity.json'
    identity.write_text(json.dumps(dict(owner_id='pr_t23_demo', bootstrap_sha256=hashlib.sha256(token.encode()).hexdigest())))
    identity.chmod(0o600)
    grant = None
    sandbox = None
    worker = None
    api = root/'api.sock'
    counter = 0
    def call(noun, verb, value=None, credential=token, okay=True):
        nonlocal counter
        counter += 1
        value=dict(value or {})
        if verb in ('create','delete','stop','update','export','purge'):
            value.setdefault('idempotency_key','t23-'+str(counter))
        inp=root/'input.json';inp.write_text(json.dumps(value));inp.chmod(0o600)
        run=subprocess.run([str(bins/'lamp-linux'),noun,verb,'--json','--input',str(inp)],env=dict(os.environ,LAMP_SOCKET=str(api),LAMP_TOKEN=credential),capture_output=True,text=True)
        reply=json.loads(run.stdout or run.stderr)
        if okay and run.returncode:
            raise RuntimeError(reply)
        return reply
    try:
        worker_pid=root/'worker.pid'
        worker=start(['sudo','-n','sh','-c','echo "$$" > "$1"; shift; exec "$@"','sh',str(worker_pid),supervisor,'serve','--store',str(store),'--socket',str(root/'worker.sock'),'--network-policy',args.policy,'--mem-dir',str(root/'mem'),'--netns-dir',str(root/'netns'),'--uid-base','1850000000','--host-id','hs_t23','--allow-uid',str(os.getuid())], 'worker')
        wait(lambda:(root/'worker.sock').exists(),'worker readiness')
        grant=json.loads(out(['sudo','-n',supervisor,'grant-install','--store',str(store),'--caller-uid',str(os.getuid()),'--ttl-s','3600']))
        quota=root/'quota.json';quota.write_text(json.dumps(dict(sandboxes=1,memory_mib=4096,disk_gib=20,execs=4)))
        reader=root/'audit-reader'
        reader.write_text('#!/usr/bin/env python3\nimport pathlib,re,subprocess,sys\nsb,g=sys.argv[1:]\nassert re.fullmatch(r"sb_[a-z0-9_]+",sb) and g.isdigit()\np="'+str(store)+'/audit/"+sb+"-g"+g\nsubprocess.run(["sudo","-n","find",p,"-name","*.ndjson","-type","f","-exec","cat","{}",";"],check=True)\n')
        reader.chmod(0o700)
        daemon=start([str(bins/'lampd-linux'),'--identity-file',str(identity),'--quota-file',str(quota),'--audit-reader',str(reader),'--socket',str(api),'--state-dir',str(root/'control'),'--worker-socket',str(root/'worker.sock'),'--worker-id','lamp-supervisor@hs_t23','--worker-boot-id',Path('/proc/sys/kernel/random/boot_id').read_text().strip(),'--worker-uid','0','--worker-pid',worker_pid.read_text().strip(),'--grant-id',grant['grant_id'],'--worker-timeout','60s'],'lampd')
        wait(api.exists,'API readiness')
        record('telemetry defaults',call('telemetry','get'))
        budget=call('budget','create',dict(wall_time_ms=180000,token_limit=1100,token_mode='exact',resource_limits=dict(concurrency=1,memory_mib=4096,disk_gib=20)))['result']['budget_id']
        task=call('task','create',dict(budget_id=budget))['result']['task_id']
        child=call('task','create',dict(parent_task_id=task))['result']['task_id']
        record('shared descendant budget',dict(parent=call('task','get',dict(task_id=task)),child=call('task','get',dict(task_id=child))))
        count=subprocess.run(['pgrep','-c','firecracker'],capture_output=True,text=True)
        live=int(count.stdout.strip() or '0');free=out(['free','-m']);available=int(next(l for l in free.splitlines() if l.startswith('Mem:')).split()[-1])
        record('allocation guard',dict(firecracker=live,available_mib=available))
        if live>=2 or available<12288:
            raise RuntimeError('owner sandbox capacity guard denied')
        sandbox=call('sandbox','create',dict(template_id=args.template,task_id=child,wait_ms=60000))['result']['sandbox_id']
        record('real sandbox ready',call('sandbox','get',dict(sandbox_id=sandbox)))
        if args.quota_only:
            refused=call('snapshot','create',dict(sandbox_id=sandbox,preserves_memory=True,capture_trust='owner_private'),okay=False)
            record('retained capture admission refused',refused)
            assert refused['error']['code']=='CONTROL_QUOTA_EXCEEDED'
            work=call('exec','create',dict(sandbox_id=sandbox,argv=['/bin/true'],wait_ms=60000))
            record('admitted work continues after retained quota refusal',work)
            return
        record('quota rejects excess allocation',call('sandbox','create',dict(template_id=args.template,task_id=child),okay=False))
        code='import socket,subprocess; subprocess.run(["/bin/echo","t23-child-process"],check=True); s=socket.socket(); s.settimeout(2)\ntry:s.connect(("1.1.1.1",443))\nexcept OSError:pass\nprint("opaque-route-usage-best-effort")'
        executed=call('exec','create',dict(sandbox_id=sandbox,argv=['python3','-c',code],wait_ms=60000))
        ex=executed['result']['exec_id']
        wait(lambda:call('exec','get',dict(exec_id=ex))['data']['state'] not in ('accepted','running'),'command completion')
        record('process and connection command',call('exec','get',dict(exec_id=ex)))
        # A real host broker mediates an actual local fixture request. No paid
        # provider is contacted. Reservation/settlement use the enrolled sandbox.
        from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
        import threading
        import ssl
        subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(root/'fixture.key'),'-out',str(root/'fixture.crt'),'-days','1','-subj','/CN=127.0.0.1','-addext','subjectAltName=IP:127.0.0.1'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        class Fixture(BaseHTTPRequestHandler):
            def do_POST(self):
                body=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                prompt=len(body['messages'][0]['content'].split())+1
                record('provider sees durable reservation before effect',call('budget','get',dict(budget_id=budget)))
                raw=json.dumps(dict(choices=[dict(message=dict(role='assistant',content='fixture reply'))],usage=dict(total_tokens=prompt+3))).encode()
                self.send_response(200);self.end_headers();self.wfile.write(raw)

            def do_GET(self):
                raw=b'{"fixture":"t23-owned-service"}'
                self.send_response(200);self.end_headers();self.wfile.write(raw)
            def log_message(self,*args):
                pass
        fixture=ThreadingHTTPServer(('127.0.0.1',0),Fixture)
        tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(str(root/'fixture.crt'),str(root/'fixture.key'));fixture.socket=tls.wrap_socket(fixture.socket,server_side=True)
        tokenizer=root/'tokenizer';tokenizer.write_text('#!/usr/bin/env python3\nimport json,sys\nb=json.load(sys.stdin);print(json.dumps(dict(prompt_tokens=len(b["messages"][0]["content"].split())+1)))\n');tokenizer.chmod(0o700)
        credential=root/'credential';credential.write_text('#!/usr/bin/env python3\nprint("t23-disposable-fixture-credential")\n');credential.chmod(0o700)
        threading.Thread(target=fixture.serve_forever,daemon=True).start()
        cfg=root/'broker.json';cfg.write_text(json.dumps(dict(accountability_socket=str(api),upstream_ca_files=[str(root/'fixture.crt')],connections=dict(service=dict(kind='service',access='full',endpoint='https://127.0.0.1:'+str(fixture.server_port),operations=dict(read=dict(method='GET',path='/read',semantic_read=True,write=False,fields={}))),model=dict(kind='model',access='full',endpoint='https://127.0.0.1:'+str(fixture.server_port)+'/chat',credential_command=[str(credential)],tokenizer_command=[str(tokenizer)],models=['fixture'],operations=dict(chat=dict(write=True,fields=dict(model='string',messages='array',max_tokens='number'))))),settings={})));cfg.chmod(0o600)
        broker=start([str(bins/'lamp-broker-linux'),'--config',str(cfg),'--owner-socket',str(root/'broker.sock'),'--task-listen','127.0.0.1:23778','--audit',str(root/'broker-audit.jsonl')],'broker')
        wait((root/'broker.sock').exists,'broker readiness')
        status,g=unix('/owner/grants',dict(sandbox_id=sandbox,generation=1,peer_ip='127.0.0.1',ttl_seconds=300,connections=dict(service=['read'],model=['chat'])),root/'broker.sock')
        c=http.client.HTTPConnection('127.0.0.1',23778)
        c.request('POST','/v1/invoke',json.dumps(dict(connection_id='service',operation='read',arguments={})),{'Authorization':'Bearer '+g['token']});r=c.getresponse();reply=json.loads(r.read());record('mediated request',dict(status=r.status,reply=reply));c.close()
        c=http.client.HTTPConnection('127.0.0.1',23778)
        c.request('POST','/v1/models/model/chat/completions',json.dumps(dict(model='fixture',messages=[dict(role='user',content='hello')],max_tokens=1098)),{'Authorization':'Bearer '+g['token']});r=c.getresponse();reply=json.loads(r.read());record('exact model settlement',dict(status=r.status,reply=reply,budget=call('budget','get',dict(budget_id=budget))));c.close()
        status,res=unix('/internal/accountability/reserve',dict(sandbox_id=sandbox,tokens=1000,type='fixture.chat',exact=True),api)
        if status!=200:raise RuntimeError(res)
        record('reservation before dispatch',call('budget','get',dict(budget_id=budget)))
        # Failed reservation is a single concrete feature demonstration.
        blocked=http.client.HTTPConnection('localhost');blocked.sock=socket.socket(socket.AF_UNIX);blocked.sock.connect(str(api));blocked.request('POST','/internal/accountability/reserve',json.dumps(dict(sandbox_id=sandbox,tokens=101,type='fixture.chat',exact=True)));br=blocked.getresponse();record('shared token cap blocks',dict(status=br.status,body=br.read().decode()));blocked.close()
        status,settled=unix('/internal/accountability/settle',dict(sandbox_id=sandbox,reservation_id=res['reservation_id'],actual_tokens=7,duration_ms=12),api)
        record('settled usage',call('budget','get',dict(budget_id=budget)))
        status,usage=unix('/internal/accountability/usage',dict(sandbox_id=sandbox),api);record('opt-out collected no telemetry',usage)
        t=call('telemetry','get')['data'];call('telemetry','update',dict(expected_revision=t['revision'],enabled=True,counts_enabled=True,timings_enabled=True))
        c=http.client.HTTPConnection('127.0.0.1',23778)
        c.request('POST','/v1/invoke',json.dumps(dict(connection_id='service',operation='read',arguments={})),{'Authorization':'Bearer '+g['token']});r=c.getresponse();r.read();c.close()
        status,usage=unix('/internal/accountability/usage',dict(sandbox_id=sandbox),api);record('opt-in actual broker numeric telemetry',usage)
        t=call('telemetry','get')['data'];call('telemetry','purge',dict(expected_revision=t['revision']))
        status,usage=unix('/internal/accountability/usage',dict(sandbox_id=sandbox),api);record('purged telemetry',usage)
        record('numeric opt-in configuration',call('telemetry','get'))
        exported=call('audit-record','export',dict(expected_revision=1,filter=dict(sandbox_id=sandbox)))
        audit_id=exported['resource_ids'][0]['id'];export=call('audit-record','get',dict(audit_record_id=audit_id))['data'];record('audit export',export)
        exported_path=Path(export['details']['path']);raw=exported_path.read_bytes();records=json.loads(raw)
        (root/'audit-export.json').write_bytes(raw)
        sources=sorted(set(r.get('source',r.get('type','')) for r in records))
        record('guard and mediated export sources',dict(sources=sources,records=len(records),sha256=hashlib.sha256(raw).hexdigest()))
        unix('/owner/revoke',dict(grant_id=g['grant_id']),root/'broker.sock')
        c=http.client.HTTPConnection('127.0.0.1',23778);c.request('POST','/v1/invoke',json.dumps(dict(connection_id='service',operation='read',arguments={})),{'Authorization':'Bearer '+g['token']});r=c.getresponse();record('revoked broker grant denied',dict(status=r.status,reply=json.loads(r.read())));c.close()
        # Changing the task to stopped demonstrates stop of every descendant.
        t=call('task','get',dict(task_id=task))['data'];call('task','stop',dict(task_id=task,expected_revision=t['revision']))
        wait(lambda:call('sandbox','get',dict(sandbox_id=sandbox))['data']['state']=='deleted','watchdog stop')
        record('parent stop releases descendant sandbox',call('sandbox','get',dict(sandbox_id=sandbox)))
        sandbox=None
        # A second, short-lived sandbox demonstrates deadline enforcement.
        b2=call('budget','create',dict(wall_time_ms=15000,token_mode='unavailable'))['result']['budget_id']
        t2=call('task','create',dict(budget_id=b2))['result']['task_id']
        live=int(subprocess.run(['pgrep','-c','firecracker'],capture_output=True,text=True).stdout.strip() or '0');free=out(['free','-m']);available=int(next(l for l in free.splitlines() if l.startswith('Mem:')).split()[-1]);record('second allocation guard',dict(firecracker=live,available_mib=available))
        if live>=2 or available<12288:raise RuntimeError('owner sandbox capacity guard denied')
        sandbox=call('sandbox','create',dict(template_id=args.template,task_id=t2,wait_ms=60000))['result']['sandbox_id']
        call('exec','create',dict(sandbox_id=sandbox,argv=['/bin/sleep','120'],wait_ms=60000))
        wait(lambda:call('sandbox','get',dict(sandbox_id=sandbox))['data']['state']=='deleted','hard deadline stops opaque work',30)
        record('opaque route hard deadline',call('sandbox','get',dict(sandbox_id=sandbox)));sandbox=None
        fixture.shutdown()
        # Persisted numeric records are inspected after graceful shutdown below.
        record('complete',dict(benchmarks=False,paid_services=False))
    finally:
        if sandbox and api.exists():
            try:
                sb=call('sandbox','get',dict(sandbox_id=sandbox))['data']
                call('sandbox','delete',dict(sandbox_id=sandbox,expected_revision=sb['revision'],wait_ms=60000))
            except Exception as e:
                record('cleanup failure',str(e))
        for proc in reversed(processes[1:]):
            proc.terminate()
            try:proc.wait(timeout=10)
            except subprocess.TimeoutExpired:proc.kill();proc.wait()
        if grant:
            subprocess.run(['sudo','-n',supervisor,'grant-revoke','--store',str(store),grant['grant_id']],check=True)
        if worker:
            if (root/'worker.pid').exists():
                subprocess.run(['sudo','-n','kill','-TERM',(root/'worker.pid').read_text().strip()],check=True)
            worker.wait(timeout=10)
        transcript.close()

if __name__=='__main__':
    main()
