# Disposable T29 seed: T26's native seed plus the official Forgejo runner.
# Only this copied image changes; nothing is installed into the host root.
import hashlib,json,os,subprocess
from pathlib import Path
base=Path.home()/'lamp-t29';out=base/'seed';out.mkdir(exist_ok=True)
src=Path.home()/'lamp-t26/native-seed';mount=Path('/var/lib/lamp/t29-mount')
def run(*a):subprocess.run(a,check=True)
if not (out/'rootfs.ext4').exists():
 run('cp','--reflink=auto','--sparse=always',str(src/'rootfs.ext4'),str(out/'rootfs.ext4'))
 run('cp',str(src/'vmlinux'),str(out/'vmlinux'))
run('sudo','-n','install','-d','-m','0755',str(mount))
run('sudo','-n','mount','-o','loop',str(out/'rootfs.ext4'),str(mount))
try:
 run('sudo','-n','install','-d','-m','0755',str(mount/'opt/forgejo-runner'))
 run('sudo','-n','install','-m','0755',str(base/'bin/forgejo-runner'),str(mount/'opt/forgejo-runner/forgejo-runner'))
 (out/'packages.txt').write_bytes(subprocess.check_output(['sudo','-n','chroot',str(mount),'dpkg-query','-W','-f','${Package} ${Version}\\n']))
finally:run('sudo','-n','umount',str(mount))
CHUNK=4<<20;ZERO=hashlib.sha256(bytes(CHUNK)).digest()
def digest(path):
 h=hashlib.sha256();size=path.stat().st_size
 with path.open('rb') as f:
  for start in range(0,size,CHUNK):
   n=min(CHUNK,size-start)
   try:data_at=os.lseek(f.fileno(),start,os.SEEK_DATA)
   except OSError:data_at=size
   if n==CHUNK and data_at>=start+n:h.update(ZERO);continue
   f.seek(start);h.update(hashlib.sha256(f.read(n)).digest())
 return h.hexdigest()
seed=json.loads((src/'seed.json').read_text())
seed['name']='ubuntu-26.04-t29-forgejo'
seed['rootfs']['digest']=digest(out/'rootfs.ext4');seed['rootfs']['size']=(out/'rootfs.ext4').stat().st_size
seed['packages']={'digest':digest(out/'packages.txt'),'size':(out/'packages.txt').stat().st_size}
(out/'seed.json').write_text(json.dumps(seed,sort_keys=True,separators=(',',':')))
print('t29 seed prepared')
