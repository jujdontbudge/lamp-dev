import html,http.server,json,os,pathlib,secrets,sys,urllib.request
os.umask(0o077);root=pathlib.Path(sys.argv[1]);origins=json.loads(pathlib.Path('/tmp/lamp-t24-build/live-identities.json').read_text())['origins']
with urllib.request.urlopen('https://raw.githubusercontent.com/octocat/Hello-World/master/README',timeout=30) as r:readme=r.read(4096).decode()
marker=secrets.token_hex(16);(root/'browser-work-marker').write_text(marker)
script='''window.isolationProbe={};
document.cookie='__Host-eilmer_account=forged; Domain=eilmer.si; Secure; Path=/';
document.cookie='__Host-lamp_session=forged; Domain=eilmer.si; Secure; Path=/';
document.cookie='lamp_t24_parent=probe; Domain=eilmer.si; Secure; Path=/';
fetch(ACCOUNT+'/signin',{method:'POST',credentials:'include',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'csrf=wrong&email=not-used'}).then(()=>window.isolationProbe.accountReadBlocked=false).catch(()=>window.isolationProbe.accountReadBlocked=true).finally(()=>window.isolationProbe.done=true);
'''.replace('ACCOUNT',json.dumps(origins['account_origin']))
class App(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  # Store names only even if a future proxy bug leaked a credential.
  (root/'app-request-metadata.json').write_text(json.dumps({'cookie_names':[x.split('=',1)[0].strip() for x in self.headers.get('Cookie','').split(';') if x.strip()],'authorization_present':bool(self.headers.get('Authorization'))}))
  body=('<!doctype html><meta charset=utf-8><title>Public Hello-World project</title><h1>Public Hello-World project</h1><pre>'+html.escape(readme)+'</pre><p id=work-marker>'+marker+'</p><script>'+script+'</script>').encode()
  self.send_response(200);self.send_header('Content-Type','text/html; charset=utf-8');self.send_header('Content-Length',str(len(body)));self.send_header('Set-Cookie','lamp_t24_from_backend=forged; Domain=eilmer.si; Secure; Path=/');self.send_header('Set-Cookie','__Host-eilmer_account=forged; Domain=eilmer.si; Secure; HttpOnly; Path=/');self.end_headers();self.wfile.write(body)
 def log_message(self,*args):pass
http.server.ThreadingHTTPServer(('127.0.0.1',7470),App).serve_forever()
