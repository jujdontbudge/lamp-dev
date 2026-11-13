import base64,json,os,pathlib,subprocess,time

def workload(cli, sid, root):
 def act(kind,**kw):return cli(['sandbox',kind,sid,*[v for k,x in kw.items() for v in ['--'+k.replace('_','-'),str(x)]],'--wait-ms','60000'])['result']
 def code(c):
  result=act('run-code',code=c)
  assert result.get('exception') is None,result
  return result
 def save(a,name):
  data=bytearray()
  while len(data)<a['size_bytes']:
   r=cli(['sandbox','file-read',sid,'--path',a['path'],'--offset-bytes',str(len(data)),'--length-bytes','24576']);data.extend(base64.b64decode(r['data_base64']))
  (root/name).write_bytes(data)
 act('kit',kit='python');assert code('x=41; x+1')['result']=='42';assert code('x+2')['result']=='43'
 print('PYTHON_OK',flush=True)
 act('kit',kit='browser');assert act('browser',action='navigate',url='https://example.com')['title']=='Example Domain';assert act('browser',action='evaluate',expression='document.title')['value']=='Example Domain';save(act('browser',action='screenshot'),'browser.png');act('browser',action='close')
 print('CHROMIUM_OK',flush=True)
 act('kit',kit='docker');result=code('import subprocess; r=subprocess.run(["docker","run","--rm","--network","host","alpine:3.20","echo","DIND_OK"],capture_output=True,text=True); print(r.returncode); print(r.stdout); print(r.stderr)');assert result['stdout'].startswith('0\nDIND_OK'),result
 print('DOCKER_OK',flush=True)
 act('kit',kit='lsp');code('from pathlib import Path; Path("/tmp/example.ts").write_text("export function greet(name: string) { return name; }"+chr(10)+"gre")')
 act('lsp',action='start',path='/tmp');act('lsp',action='open',path='/tmp/example.ts');assert act('lsp',action='symbols',path='/tmp/example.ts')['items'][0]['name']=='greet';assert act('lsp',action='completions',path='/tmp/example.ts',line=1,character=3)['items'];act('lsp',action='stop')
 print('LSP_OK',flush=True)
 act('kit',kit='desktop');assert act('desktop',action='info')['width']==1024;act('desktop',action='move',x=100,y=100);act('desktop',action='click',button=1);act('desktop',action='type',text='echo ARM_KITS_OK');act('desktop',action='key',key='Return');save(act('desktop',action='screenshot'),'desktop.png');act('desktop',action='record_start');act('desktop',action='move',x=200,y=200);save(act('desktop',action='record_stop'),'desktop.mp4')
 print('DESKTOP_OK',flush=True)
 result=code('import urllib.request,platform; print(platform.machine()); print(urllib.request.urlopen("https://example.com",timeout=20).status)');assert '200' in result['stdout'],result
 print('NETWORK_STILL_OK_NO_RECOVERY',flush=True)
