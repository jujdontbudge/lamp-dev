#!/usr/bin/env python3
"""One real canonical Lamp journey for unfinished vault fill/private typing.

The real agent VM is suspended before the separate browser VM is allocated, so
only one T22 Firecracker is live. No owner profile, vault, daemon or test double.
"""
import argparse
import base64
import html
import http.client
import json
import os
from pathlib import Path
import re
import secrets
import signal
import socket
import subprocess
import sys
import time
import urllib.request

from kits.control import Control
from kits.service import admission


def main():
    p = argparse.ArgumentParser(description=__doc__)
    for field in ('fixture', 'root', 'lamp'): p.add_argument('--'+field, required=True)
    p.add_argument('--vault-evidence')
    p.add_argument('--pause-after-vault', action='store_true')
    args = p.parse_args()
    fixture, root = Path(args.fixture).resolve(), Path(args.root).resolve()
    root.mkdir(mode=0o700)
    transcript = (root/'transcript.jsonl').open('x')
    control = Control(str(fixture/'api.sock'), (fixture/'token').read_text().strip())
    agent, browser, browser_vm = None, None, None
    def record(step, value):
        transcript.write(json.dumps({'step': step, 'value': value},sort_keys=True)+'\n')
        transcript.flush(); os.fsync(transcript.fileno())
    def interrupted(*_): raise KeyboardInterrupt()
    signal.signal(signal.SIGHUP, interrupted); signal.signal(signal.SIGTERM, interrupted)
    def cli(kind, action, body=None, identity=None, token_file=None, required=True):
        command = [args.lamp, kind, action]
        if identity: command.append(identity)
        result = subprocess.run(command+['--socket', str(fixture/'api.sock'), '--token-file', str(token_file or fixture/'token'), '--input', '-', '--json'],
            input=json.dumps(body or {}).encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        data = json.loads(result.stdout if result.returncode==0 else result.stderr)
        if required and result.returncode:
            code = data.get('error',{}).get('code','UNKNOWN')
            if not re.fullmatch('[A-Z_]{1,64}',code):code='UNKNOWN'
            raise RuntimeError('canonical '+kind+' '+action+' failed: '+code+'; private output withheld')
        return data if required else (result.returncode, data)
    def revision(): return cli('browser','get',identity=browser)['data']['revision']
    def action(operation, arguments=None, token_file=None, required=True):
        return cli('browser','action',{'action':operation,'arguments':arguments or {},'expected_revision':revision()},browser,token_file,required)
    def raw(method, path, body=None, token=None, rev=None):
        c = http.client.HTTPConnection('lamp',timeout=65)
        c.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);c.sock.settimeout(65);c.sock.connect(str(fixture/'api.sock'))
        headers={'Authorization':'Bearer '+(token or control.token),'Content-Type':'application/json'}
        if method!='GET':headers['Idempotency-Key']=secrets.token_hex(16)
        if rev is not None:headers['If-Match']=str(rev)
        try:
            c.request(method,path,None if body is None else json.dumps(body),headers)
            r=c.getresponse();return r.status,json.loads(r.read(8*1024*1024))
        finally:c.close()
    def login_observed():
        # Owner-only public exec reads a boolean, never a password/page body.
        source="""import importlib.util,json,time
s=importlib.util.spec_from_file_location('g','/tmp/lamp-kits-worker.py');g=importlib.util.module_from_spec(s);s.loader.exec_module(g)
c=g.page(json.loads(g.STATE.read_text())); end=time.monotonic()+30
while True:
 try:ok=g.evaluate(c,"location.pathname === '/secure'")
 except ValueError:ok=False
 if ok or time.monotonic()>end:break
 time.sleep(.1)
c.close();print(json.dumps({'real_login_completed':ok}))"""
        assert control.execute(browser_vm,['python3','-c',source])['real_login_completed']
    try:
        with urllib.request.urlopen('https://the-internet.herokuapp.com/login') as response:
            advertised=re.findall(r'<em>([^<]+)</em>',response.read(262144).decode())
        assert len(advertised)==2
        username,password=map(html.unescape,advertised)
        record('allocation admission',admission())
        template=json.loads((fixture/'template.json').read_text())['template_id']
        agent=control.create(template,{'vcpus':2,'memory_mib':4096,'disk_gib':20,'pids_max':4096})
        sb=control.sandbox(agent)
        control.mutation('POST','/v1/sandboxes/'+agent+':suspend',{},sb['revision'])
        assert control.call('GET','/v1/sandboxes/'+agent)['data']['state']=='suspended'
        record('real agent suspended to release live slot',{'sandbox_id':agent,'admission':admission()})
        key=cli('key','create',{'expires_at':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime(time.time()+3600)), 'scope':{'actions':['browser.action','browser.get','sandbox.get','exec.create','snapshot.create','snapshot.get']}})['result']
        credential=root/'agent-token';fd=os.open(credential,os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)
        with os.fdopen(fd,'w') as f:f.write(key['secret'])
        broad_token=key['secret'];del key
        browser=cli('browser','create',{'sandbox_id':agent})['result']['browser_id']
        stored=json.loads((fixture/'control/kits/browsers.json').read_text())
        browser_vm=stored[browser]['sandbox']
        assert control.sandbox(browser_vm)['labels']['lamp.browser-custody']=='external'
        record('canonical external browser',{'browser_id':browser,'separate_real_vm':browser_vm!=agent})
        status,_=raw('GET','/v1/sandboxes/'+browser_vm,token=broad_token);assert status==404 or status==403
        status,_=raw('POST','/v1/execs',{'sandbox_id':browser_vm,'argv':['/bin/true']},token=broad_token);assert status==403 or status==404
        sb=control.sandbox(browser_vm)
        status,_=raw('POST','/v1/snapshots',{'sandbox_id':browser_vm},token=broad_token,rev=sb['revision']);assert status==403 or status==404
        record('broad same-owner agent key denied underlying VM read exec capture',{'denied':True})
        if args.vault_evidence:
            vault=json.loads(Path(args.vault_evidence).read_text())
            action('navigate',{'url':'https://the-internet.herokuapp.com/login'},credential)
            proposal=cli('browser','fill-password',{'credential_source_id':vault['credential_source_id'],'secret_ref':vault['secret_ref'],
                'origin':'https://the-internet.herokuapp.com','submit':True,'expected_revision':revision()},browser)['result']
            assert proposal['state']=='approval_required'
            plan=proposal['plan'];assert plan['vault_id']==vault['credential_source_id'] and plan['item_id']==vault['secret_ref']
            record('owner reviews one-use per-vault item origin submit plan',plan)
            filled=cli('browser','fill-password',{'approval_id':proposal['approval_id'],'plan':plan,'expected_revision':revision()},browser)['result']
            assert filled['filled'] and filled['private'];login_observed()
            record('Vaultwarden native read approval real form fill',{'server':'Vaultwarden','real_login_completed':True})
            print('VAULT_PASS',flush=True)
            if args.pause_after_vault:
                if sys.stdin.readline().strip()!='continue':raise RuntimeError('continuation cancelled')
            cli('browser','return-control',{'expected_revision':revision()},browser)
        action('navigate',{'url':'https://the-internet.herokuapp.com/login'},credential)
        probe=secrets.token_hex(32)
        code,_=action('type',{'selector':'#password','text':probe},credential,False)
        assert code!=0
        record('password field refuses ordinary agent typing',{'private_typing_required':True})
        cli('browser','takeover',{'expected_revision':revision()},browser)
        assert action('type',{'selector':'#username','text':username})['result']['observations']['typed']
        assert action('type',{'selector':'#password','text':password})['result']['observations']['typed']
        action('click',{'selector':'button[type=submit]'})
        login_observed()
        for path in (fixture/'control').rglob('*'):
            if path.is_file():assert probe.encode() not in path.read_bytes(), 'private input entered journal'
        record('private password-specific typing and real submit',{'real_login_completed':True,'rejected_input_absent_from_control_files':True})
        returned=cli('browser','return-control',{'expected_revision':revision()},browser)['result']
        assert returned['safe_page']=='about:blank'
        record('canonical safe handback',returned)
        print('PRIVATE_TYPING_PASS',flush=True)
    finally:
        try:
            if browser:
                cli('browser','delete',{'expected_revision':revision()},browser)
                record('browser released',{'deleted':True})
        finally:
            try:
                if agent:control.delete(agent);record('agent released',{'deleted':True})
            finally:
                controller=int((fixture/'host.pid').read_text())
                try:
                    argv=(Path('/proc')/str(controller)/'cmdline').read_bytes().split(b'\0')
                    assert any(Path(os.fsdecode(x)).name=='kits_lx_demo_host.py' for x in argv if x)
                    assert os.fsencode(str(fixture)) in argv
                    os.kill(controller,signal.SIGHUP)
                except FileNotFoundError:pass
                transcript.close()
    print(root/'transcript.jsonl')


if __name__=='__main__':main()
