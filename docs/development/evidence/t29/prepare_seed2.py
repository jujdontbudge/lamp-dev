# Disposable T29 seed: T26's native seed plus the official Forgejo runner.
# Only this copied image changes; nothing is installed into the host root.
import hashlib,json,os,subprocess
from pathlib import Path
base=Path.home()/'lamp-t29';out=base/'seed2';out.mkdir(exist_ok=True)
src=Path.home()/'lamp-t26/native-seed';mount=Path('/var/lib/lamp/t29-mount')
def run(*a):subprocess.run(a,check=True)
FINAL=os.environ.get('FINALIZE_ONLY')=='1'
if not (out/'rootfs.ext4').exists():
 run('cp','--reflink=auto','--sparse=always',str(src/'rootfs.ext4'),str(out/'rootfs.ext4'))
 run('cp',str(src/'vmlinux'),str(out/'vmlinux'))
if not FINAL:
 run('sudo','-n','install','-d','-m','0755',str(mount))
 run('sudo','-n','mount','-o','loop',str(out/'rootfs.ext4'),str(mount))
try:
 b=base/'bin'
 for d,f in [('forgejo-runner','forgejo-runner'),('gitea-runner','gitea-runner'),('woodpecker-agent','woodpecker-agent'),('gitlab-runner','gitlab-runner')]:
  run('sudo','-n','install','-d','-m','0755',str(mount/'opt'/d))
  run('sudo','-n','install','-m','0755',str(b/f),str(mount/'opt'/d/f))
 run('sudo','-n','install','-d','-m','0755',str(mount/'opt/jenkins-agent'))
 run('sudo','-n','cp','-a',str(b/'jdk'),str(mount/'opt/jdk'))
 run('sudo','-n','ln','-sf','/opt/jdk/bin/java',str(mount/'usr/local/bin/java'))
 run('sudo','-n','sh','-c','cd "$1" && unzip -p "$2" "WEB-INF/lib/remoting-*.jar" > "$3"','sh',str(b),str(b/'jenkins.war'),str(mount/'opt/jenkins-agent/agent.jar'))
 run('sudo','-n','sh','-c','printf "nameserver 1.1.1.1\\n" > "$1/etc/resolv.conf"','sh',str(mount))
 for d in ('proc','sys','dev'):run('sudo','-n','mount','--bind','/'+d,str(mount/d))
 run('sudo','-n','chroot',str(mount),'apt-get','-o','Acquire::ForceIPv4=true','update','-qq')
 run('sudo','-n','chroot',str(mount),'env','DEBIAN_FRONTEND=noninteractive','apt-get','-y','-qq','-o','Acquire::ForceIPv4=true','install','openssh-server')
 run('sudo','-n','chroot',str(mount),'apt-get','clean')
 for d in ('dev','sys','proc'):run('sudo','-n','umount',str(mount/d))
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
seed['name']='ubuntu-26.04-t29-ci'
seed['rootfs']['digest']=digest(out/'rootfs.ext4');seed['rootfs']['size']=(out/'rootfs.ext4').stat().st_size
seed['packages']={'digest':digest(out/'packages.txt'),'size':(out/'packages.txt').stat().st_size}
(out/'seed.json').write_text(json.dumps(seed,sort_keys=True,separators=(',',':')))
print('t29 seed prepared')
