"""Guest-only kit runtime. All processes remain under Lamp's audited bridge."""
import ast
import base64
import contextlib
import io
import json
import os
from pathlib import Path
import select
import socket
import subprocess
import sys
import threading
import time
import traceback
import uuid

ROOT = Path('/tmp/lamp-sandbox-kits')
ENDPOINT = str(ROOT / 'runtime.sock')
ENV = dict(os.environ, DISPLAY=':99', DEBIAN_FRONTEND='noninteractive', PIP_BREAK_SYSTEM_PACKAGES='1')
PACKAGES = {
 'browser': ['python3-venv', 'libnss3', 'libatk-bridge2.0-0', 'libcups2', 'libxkbcommon0', 'libxcomposite1', 'libxdamage1', 'libxfixes3', 'libxrandr2', 'libgbm1', 'libasound2t64', 'libpango-1.0-0', 'libcairo2'],
 'desktop': ['xvfb','fluxbox','xterm','xdotool','ffmpeg','x11-utils'],
 'docker': ['docker.io', 'containerd', 'runc'],
 'python': ['python3','python3-pip','python3-venv','python3-matplotlib'],
 'node': ['nodejs','npm'], 'go':['golang-go'], 'rust':['rustc','cargo'], 'c':['build-essential'],
 'lsp':['nodejs','npm'],
}

def run(argv, **kw):
    try:
        return subprocess.run(argv, check=True, env=ENV, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=900, **kw)
    except subprocess.CalledProcessError as e:
        (ROOT/'last-command.log').write_bytes(e.stdout+b'\n'+e.stderr)
        raise

def launch(argv):
    log = (ROOT / 'services.log').open('ab', buffering=0)
    try:
        return subprocess.Popen(argv, env=ENV, stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
    finally:
        log.close()

def artifact(path, mime):
    return {'path':str(path), 'mime_type':mime, 'size_bytes':path.stat().st_size}

def output_path(suffix):
    return ROOT / (uuid.uuid4().hex + suffix)

class LSP:
    def __init__(self, path):
        self.process = subprocess.Popen(['typescript-language-server','--stdio'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=(ROOT/'lsp.log').open('ab'),env=ENV)
        self.pending = {}; self.diagnostics = {}; self.sequence=0; self.lock=threading.Lock()
        threading.Thread(target=self.reader,daemon=True).start()
        self.call('initialize',{'processId':None,'rootUri':Path(path).absolute().as_uri(),'capabilities':{}})
        self.send('initialized',{})
    def reader(self):
        stream=self.process.stdout
        while True:
            headers={}
            while True:
                line=stream.readline()
                if not line:return
                if line==b'\r\n':break
                k,v=line.decode().split(':',1);headers[k.lower()]=v.strip()
            msg=json.loads(stream.read(int(headers['content-length'])))
            if 'id' in msg:
                if 'method' in msg:
                    self.write({'jsonrpc':'2.0','id':msg['id'],'result':None})
                elif msg['id'] in self.pending:
                    event,box=self.pending[msg['id']];box.append(msg);event.set()
            elif msg.get('method')=='textDocument/publishDiagnostics':
                self.diagnostics[msg['params']['uri']]=msg['params']['diagnostics']
    def write(self,msg):
        data=json.dumps(msg).encode()
        with self.lock:
            self.process.stdin.write(('Content-Length: %d\r\n\r\n'%len(data)).encode()+data);self.process.stdin.flush()
    def send(self,method,params):self.write({'jsonrpc':'2.0','method':method,'params':params})
    def call(self,method,params):
        self.sequence+=1;seq=self.sequence;event=threading.Event();box=[];self.pending[seq]=(event,box)
        try:
            self.write({'jsonrpc':'2.0','id':seq,'method':method,'params':params})
            if not event.wait(45):raise TimeoutError('language server did not reply')
            if 'error' in box[0]:raise ValueError(str(box[0]['error']))
            return box[0].get('result')
        finally:self.pending.pop(seq,None)

class Runtime:
    def __init__(self):
        self.contexts={}; self.browser=None; self.lsp=None; self.recording=None; self.desktop=[];self.docker=None
    def install(self,kit):
        if kit not in PACKAGES:raise ValueError('unknown kit')
        run(['apt-get','update'])
        # No package services may start outside the supervised runtime.
        policy=Path('/usr/sbin/policy-rc.d');previous=policy.read_bytes() if policy.exists() else None
        previous_mode=policy.stat().st_mode & 0o777 if policy.exists() else None
        try:
            policy.write_text('#!/bin/sh\nexit 101\n');policy.chmod(0o755)
            run(['apt-get','install','-y','--no-install-recommends']+PACKAGES[kit])
        finally:
            if previous is None:policy.unlink(missing_ok=True)
            else:policy.write_bytes(previous);policy.chmod(previous_mode)
        if kit=='browser':
            venv=ROOT/'playwright'
            if not (venv/'bin/python').exists():run(['python3','-m','venv',str(venv)])
            run([str(venv/'bin/pip'),'install','playwright'])
            run([str(venv/'bin/python'),'-m','playwright','install','chromium'])
        elif kit=='lsp':run(['npm','install','-g','typescript','typescript-language-server'])
        elif kit=='docker':
            if self.docker is None or self.docker.poll() is not None:
                # Private guest socket only. VFS and host networking avoid
                # overlay/iptables capabilities absent in an unprivileged VM task.
                self.docker=launch(['dockerd','--host=unix:///tmp/lamp-docker.sock','--data-root=/tmp/lamp-docker-data','--exec-root=/tmp/lamp-docker-run','--pidfile=/tmp/lamp-docker.pid','--storage-driver=vfs','--bridge=none','--iptables=false','--ip6tables=false'])
            deadline=time.monotonic()+60
            while True:
                if self.docker.poll() is not None:raise RuntimeError('guest Docker exited; read '+str(ROOT/'services.log'))
                try:
                    run(['docker','--host=unix:///tmp/lamp-docker.sock','info']);break
                except subprocess.CalledProcessError:
                    if time.monotonic()>deadline:raise TimeoutError('guest Docker startup')
                    time.sleep(.2)
            # Ordinary docker commands select this guest daemon automatically.
            Path('/etc/profile.d/lamp-docker.sh').write_text('export DOCKER_HOST=unix:///tmp/lamp-docker.sock\n')
            Path('/var/run/docker.sock').unlink(missing_ok=True)
            Path('/var/run/docker.sock').symlink_to('/tmp/lamp-docker.sock')
        elif kit=='desktop':
            if not self.desktop or self.desktop[0].poll() is not None:
                self.desktop=[launch(['Xvfb',':99','-screen','0','1024x768x24','-ac','-nolisten','tcp'])]
                deadline=time.monotonic()+15
                while not Path('/tmp/.X11-unix/X99').exists():
                    if time.monotonic()>deadline:raise TimeoutError('virtual display startup')
                    time.sleep(.1)
                self.desktop.extend([launch(['fluxbox']),launch(['xterm'])])
                # The view is read-only HTTP, so it works through Lamp's
                # authenticated preview without a second VNC credential.
                threading.Thread(target=self.view,daemon=True).start()
        result={'kit':kit,'state':'ready','boundary':'vm'}
        if kit=='docker':result.update(docker_host='unix:///tmp/lamp-docker.sock',container_network='use --network host')
        if kit=='desktop':result.update(display=':99',view_port=6080,view_path='/')
        return result
    def view(self):
        from http.server import BaseHTTPRequestHandler,HTTPServer
        class Handler(BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def do_GET(self):
                if self.path=='/screen.png':
                    data=run(['ffmpeg','-loglevel','error','-f','x11grab','-video_size','1024x768','-i',':99','-frames:v','1','-f','image2pipe','-vcodec','png','-threads','1','-']).stdout;mime='image/png'
                else:
                    data=b'<!doctype html><title>Lamp desktop</title><img id="screen" width="1024" height="768"><script>setInterval(()=>document.getElementById("screen").src="screen.png?t="+Date.now(),1000)</script>';mime='text/html'
                self.send_response(200);self.send_header('Content-Type',mime);self.send_header('Cache-Control','no-store');self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
        HTTPServer(('0.0.0.0',6080),Handler).serve_forever()
    def code(self,q):
        name=q.get('context','default')
        if not isinstance(name,str) or not name or len(name)>64 or any(c not in 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-' for c in name):raise ValueError('invalid context')
        scope=self.contexts.setdefault(name,{'__name__':'__main__'})
        stdout=io.StringIO();stderr=io.StringIO();result={'context':name,'outputs':[]}
        with contextlib.redirect_stdout(stdout),contextlib.redirect_stderr(stderr):
            try:
                tree=ast.parse(q['code']);last=tree.body.pop() if tree.body and isinstance(tree.body[-1],ast.Expr) else None
                exec(compile(tree,'<lamp>','exec'),scope)
                value=eval(compile(ast.Expression(last.value),'<lamp>','eval'),scope) if last else None
                result['result']=repr(value) if value is not None else None
            except BaseException as e:
                result['exception']={'name':type(e).__name__,'message':str(e),'traceback':traceback.format_exc()}
            if 'matplotlib.pyplot' in sys.modules:
                plt=sys.modules['matplotlib.pyplot']
                for number in plt.get_fignums():
                    p=output_path('.png');plt.figure(number).savefig(p);result['outputs'].append(artifact(p,'image/png'))
                plt.close('all')
        result.update(stdout=stdout.getvalue(),stderr=stderr.getvalue())
        return result
    def browser_action(self,q):
        # Run the sync Playwright API in its owning process/thread, with a
        # dedicated interpreter so distro Python stays unmodified.
        request=dict(q);request['component']='browser_worker'
        worker=ROOT/'browser.py'
        if not worker.exists():worker.write_text(SOURCE)
        return relay(request, str(ROOT/'browser.sock'), [str(ROOT/'playwright/bin/python'),str(worker),'--browser-serve'])
    def desktop_action(self,q):
        action=q['action']
        if action=='info':
            location=run(['xdotool','getmouselocation','--shell']).stdout.decode()
            return {'width':1024,'height':768,'pointer':dict(line.split('=',1) for line in location.splitlines()),'view_port':6080}
        if action=='move':run(['xdotool','mousemove',str(q['x']),str(q['y'])])
        elif action=='click':run(['xdotool','click',str(q.get('button',1))])
        elif action=='type':run(['xdotool','type','--clearmodifiers','--',q['text']])
        elif action=='key':run(['xdotool','key','--clearmodifiers',q['key']])
        elif action=='screenshot':
            p=output_path('.png');run(['ffmpeg','-loglevel','error','-f','x11grab','-video_size','1024x768','-i',':99','-frames:v','1','-threads','1',str(p)]);return artifact(p,'image/png')
        elif action=='record_start':
            if self.recording and self.recording.poll() is None:raise ValueError('recording already active')
            self.record_path=output_path('.mp4');self.recording=launch(['ffmpeg','-loglevel','error','-f','x11grab','-framerate','10','-video_size','1024x768','-i',':99','-c:v','libx264','-threads','1','-pix_fmt','yuv420p',str(self.record_path)])
        elif action=='record_stop':
            if self.recording is None:raise ValueError('no recording')
            self.recording.send_signal(2);self.recording.wait(timeout=20);self.recording=None;return artifact(self.record_path,'video/mp4')
        else:raise ValueError('unknown desktop action')
        return {'ok':True}
    def lsp_action(self,q):
        action=q['action']
        if action=='start':
            if self.lsp:raise ValueError('language server already started')
            self.lsp=LSP(q.get('path','/tmp'));return {'started':True}
        if not self.lsp:raise ValueError('start the language server first')
        if action=='stop':
            self.lsp.call('shutdown',None);self.lsp.send('exit',None);self.lsp.process.wait(timeout=10);self.lsp=None;return {'stopped':True}
        uri=Path(q['path']).absolute().as_uri();doc={'uri':uri}
        if action=='open':self.lsp.send('textDocument/didOpen',{'textDocument':dict(doc,languageId='typescript',version=1,text=q['text'] if 'text' in q else Path(q['path']).read_text())});return {'opened':True}
        if action=='close':self.lsp.send('textDocument/didClose',{'textDocument':doc});return {'closed':True}
        if action=='diagnostics':return {'diagnostics':self.lsp.diagnostics.get(uri,[])}
        method='textDocument/documentSymbol' if action=='symbols' else 'textDocument/completion'
        params={'textDocument':doc}
        if action=='completions':params['position']={'line':q.get('line',0),'character':q.get('character',0)}
        return {'items':self.lsp.call(method,params)}
    def call(self,q):
        component=q['component']
        if component=='kit':return self.install(q['kit'])
        if component=='run_code':return self.code(q)
        if component=='desktop':return self.desktop_action(q)
        if component=='browser':return self.browser_action(q)
        if component=='lsp':return self.lsp_action(q)
        raise ValueError('unknown component')

class Browser:
    def __init__(self):self.pw=None;self.browser=None
    def call(self,q):
        if q['action']=='close':
            if self.browser:self.browser.close();self.pw.stop();self.browser=None
            return {'closed':True}
        if self.browser is None:
            from playwright.sync_api import sync_playwright
            self.pw=sync_playwright().start();self.browser=self.pw.chromium.launch(headless=True,args=['--no-sandbox']);self.page=self.browser.new_page()
        if q['action']=='navigate':self.page.goto(q['url'],wait_until='domcontentloaded');return {'title':self.page.title(),'url':self.page.url}
        if q['action']=='evaluate':return {'value':self.page.evaluate(q['expression'])}
        p=output_path('.png');self.page.screenshot(path=str(p));return artifact(p,'image/png')

def serve(endpoint, runtime):
    listener=socket.socket(socket.AF_UNIX);listener.bind(endpoint);os.chmod(endpoint,0o600);listener.listen(16)
    while True:
        conn,_=listener.accept()
        with conn:
            try:
                raw=conn.makefile('rb').readline(131073)
                if len(raw)>131072:raise ValueError('request too large')
                result=runtime.call(json.loads(raw))
            except BaseException as e:result={'error':type(e).__name__,'message':str(e)[:1000]}
            data=json.dumps(result).encode()
            if len(data)>60000: data=json.dumps({'error':'OUTPUT_TOO_LARGE','message':'write large outputs to guest files and use sandbox file-read'}).encode()
            try:conn.sendall(data)
            except OSError:pass

def relay(q, endpoint=ENDPOINT, command=None):
    import fcntl
    ROOT.mkdir(mode=0o700,exist_ok=True)
    with (ROOT/'boot.lock').open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        sock=socket.socket(socket.AF_UNIX)
        try:sock.connect(endpoint)
        except (FileNotFoundError,ConnectionRefusedError):
            sock.close();Path(endpoint).unlink(missing_ok=True)
            if command is None:
                worker=ROOT/'runtime.py';worker.write_text(SOURCE);worker.chmod(0o600)
                command=[sys.executable,str(worker),'--serve']
            process=launch(command);deadline=time.monotonic()+15
            while True:
                sock=socket.socket(socket.AF_UNIX)
                try:sock.connect(endpoint);break
                except (FileNotFoundError,ConnectionRefusedError):
                    sock.close()
                    if process.poll() is not None or time.monotonic()>deadline:raise RuntimeError('guest kit runtime did not start')
                    time.sleep(.05)
    with sock:
        sock.settimeout(1100);sock.sendall(json.dumps(q).encode()+b'\n');sock.shutdown(socket.SHUT_WR)
        data=bytearray()
        while chunk:=sock.recv(65536):data.extend(chunk)
        return json.loads(data)

if __name__=='__main__':
    # SOURCE is injected by lampd for -c, or read from our guest-only file.
    if 'SOURCE' not in globals():SOURCE=Path(__file__).read_text()
    if sys.argv[1]=='--serve':serve(ENDPOINT,Runtime())
    elif sys.argv[1]=='--browser-serve':serve(str(ROOT/'browser.sock'),Browser())
    elif sys.argv[1]=='--job':
        job=ROOT/sys.argv[2]
        try:result=relay(json.loads(job.with_suffix('.request').read_text()))
        except BaseException as e:result={'error':type(e).__name__,'message':str(e)[:1000]}
        tmp=job.with_suffix('.new');tmp.write_text(json.dumps(result));tmp.replace(job.with_suffix('.result'))
    else:
        ROOT.mkdir(mode=0o700,exist_ok=True)
        worker=ROOT/'client.py';worker.write_text(SOURCE);worker.chmod(0o600)
        job=uuid.uuid4().hex
        (ROOT/(job+'.request')).write_text(sys.argv[1])
        launch([sys.executable,str(worker),'--job',job])
        print(json.dumps({'job':job}))
