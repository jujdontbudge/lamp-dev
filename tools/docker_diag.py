"""Disposable diagnosis: start the docker kit install and read guest files while it runs."""
import json,sys,time
from pathlib import Path
from kits.control import Control
from kits.toolchains import start_install,install_status
fx=Path(sys.argv[1]).resolve()
c=Control(str(fx/"api.sock"),(fx/"token").read_text().strip())
t=json.loads((fx/"template.json").read_text())["template_id"]
sb=c.create(t,{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096})
print("vm",sb,flush=True)
probe="""import json,pathlib,os
out={}
for d in ("/tmp/lamp-kit-jobs","/tmp/lamp-kit-docker","/tmp/lamp-toolchains/cache"):
 p=pathlib.Path(d); out[d]=sorted(x.name for x in p.iterdir()) if p.exists() else None
for f in ["/tmp/lamp-kit-docker/runtime.log"]+[str(x) for x in pathlib.Path("/tmp/lamp-kit-jobs").glob("*.log")]:
 p=pathlib.Path(f)
 if p.exists(): out[f]=p.read_bytes()[-3000:].decode(errors="replace")
print(json.dumps(out))"""
try:
  job=start_install(c,sb,["git","docker"]);print(job,flush=True)
  for i in range(14):
    time.sleep(30)
    st=install_status(c,sb,job["job_id"]);print(i,st,flush=True)
    print(json.dumps(c.execute(sb,["python3","-c",probe]),indent=0)[:6000],flush=True)
    if st["state"]!="installing":break
finally:
  c.delete(sb);print("released",flush=True)
