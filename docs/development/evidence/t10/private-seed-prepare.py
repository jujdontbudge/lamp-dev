import hashlib,json,os,subprocess
from pathlib import Path
original=Path('/home/johan/lamp-t6/seed-ubuntu-6')
out=Path('/var/lib/lamp/t10-e03-seed')
binaries=Path('/home/johan/lamp-t10-control-core/src/target/x86_64-unknown-linux-musl/release')
print(subprocess.check_output(['free','-m'],text=True),flush=True)
out.mkdir(exist_ok=False)
for name in ['rootfs.ext4','vmlinux','packages.txt']:
 subprocess.run(['cp','--reflink=auto','--sparse=always',str(original/name),str(out/name)],check=True)
commands=[]
for name in ['lamp-init','lamp-guest-helper','lamp-guard']:
 commands += [f'rm /usr/sbin/{name}',f'write {binaries/name} /usr/sbin/{name}',f'set_inode_field /usr/sbin/{name} mode 0100755']
p=subprocess.run(['debugfs','-w','-f','-',str(out/'rootfs.ext4')],input='\n'.join(commands)+'\n',text=True,check=True,capture_output=True)
print(p.stdout,p.stderr,flush=True)
subprocess.run(['e2fsck','-fn',str(out/'rootfs.ext4')],check=True)
CHUNK=4<<20
zero=hashlib.sha256(bytes(CHUNK)).digest()
def digest(path):
 h=hashlib.sha256();size=path.stat().st_size
 with path.open('rb') as f:
  for start in range(0,size,CHUNK):
   n=min(CHUNK,size-start)
   try:data_at=os.lseek(f.fileno(),start,os.SEEK_DATA)
   except OSError:data_at=size
   if n==CHUNK and data_at>=start+n:h.update(zero);continue
   f.seek(start);h.update(hashlib.sha256(f.read(n)).digest())
 return h.hexdigest()
seed=json.loads((original/'seed.json').read_text())
seed['rootfs']['digest']=digest(out/'rootfs.ext4')
for name in ['lamp-init','lamp-guest-helper','lamp-guard']:
 seed['init'][name]=digest(binaries/name)
 # Verify actual image bytes, including executable mode, against built artifact.
 extracted=out/(name+'.verified')
 subprocess.run(['debugfs','-R',f'dump /usr/sbin/{name} {extracted}',str(out/'rootfs.ext4')],check=True)
 assert extracted.read_bytes()==(binaries/name).read_bytes()
 extracted.unlink()
with (out/'seed.json').open('x') as f:f.write(json.dumps(seed,sort_keys=True,separators=(',',':')));f.flush();os.fsync(f.fileno())
print(json.dumps(dict(seed_directory=str(out),seed_ref='lamp-seed/'+seed['name']+'@sha256:'+hashlib.sha256((out/'seed.json').read_bytes()).hexdigest(),seed=seed)),flush=True)
