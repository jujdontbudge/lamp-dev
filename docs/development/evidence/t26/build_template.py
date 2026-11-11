import base64,json,subprocess,time
from pathlib import Path
base=Path.home()/'lamp-t26';sup='/home/johan/lamp-t19/src/target/release/lamp-supervisor';store='/var/lib/lamp/t26-private'
def sh(*a):
 r=subprocess.run(a,capture_output=True,text=True)
 if r.returncode: print(r.stderr,flush=True);r.check_returncode()
 return r.stdout
n=int(subprocess.run(['pgrep','-c','firecracker'],capture_output=True,text=True).stdout.strip() or 0)
avail=int(sh('free','-m').splitlines()[1].split()[6]);assert n<2 and avail>=12288,(n,avail)
ref=sh('sudo','-n',sup,'seed-import','--store',store,'/var/lib/lamp/t19-private/seeds/b4567a56bcde61d2c074a0773ca157079af2f24fd88acfb30faceab5133c3c9e').strip()
assert ref.startswith('lamp-seed/'),ref
script='''#!/bin/sh
set -eu
test ! -e /tmp/lamp-ci-private-marker
echo fresh-private-root > /tmp/lamp-ci-demo.log
echo private-job-state > /tmp/lamp-ci-private-marker
mkdir /tmp/zlib-build
tar xzf ./zlib.tgz -C /tmp/zlib-build --strip-components=1
cd /tmp/zlib-build
touch crc32.h
CFLAGS=-DDYNAMIC_CRC_TABLE ./configure --static >> /tmp/lamp-ci-demo.log 2>&1
gcc -DDYNAMIC_CRC_TABLE -O2 -I. test/minigzip.c adler32.c crc32.c deflate.c infback.c inffast.c inflate.c inftrees.c trees.c zutil.c compress.c uncompr.c gzclose.c gzlib.c gzread.c gzwrite.c -o minigzip >> /tmp/lamp-ci-demo.log 2>&1
./minigzip < README > /tmp/readme.gz
./minigzip -d < /tmp/readme.gz > /tmp/readme.restored
cmp README /tmp/readme.restored
echo zlib-compression-roundtrip-ok >> /tmp/lamp-ci-demo.log
touch /tmp/lamp-ci-demo-finished
'''
steps=[['python3','-c',"import os;os.makedirs('/opt/lamp-ci-demo-dist',exist_ok=True)"]]
b=(base/'zlib-build-inputs.tar.gz').read_bytes()
for off in range(0,len(b),32000):steps.append(['python3','-c',"import base64;open('/opt/lamp-ci-demo-dist/zlib.tgz','ab').write(base64.b64decode('"+base64.b64encode(b[off:off+32000]).decode()+"'))"])
steps.append(['python3','-c',"import base64,os;open('/opt/lamp-ci-demo-dist/run.sh','wb').write(base64.b64decode('"+base64.b64encode(script.encode()).decode()+"'));os.chmod('/opt/lamp-ci-demo-dist/run.sh',0o755)"])
text='format = "lamp.template/v1"\nname = "t26-public-zlib-fixture"\nguest_os = "linux"\narch = "x86_64"\nseed_ref = '+json.dumps(ref)+'\nnetwork = "none"\n'
for argv in steps:text+='\n[[steps]]\nargv = '+json.dumps(argv)+'\n'
p=base/'public-zlib.lamp';p.write_text(text)
# Observe again immediately before the builder creates a VM.
n=int(subprocess.run(['pgrep','-c','firecracker'],capture_output=True,text=True).stdout.strip() or 0);avail=int(sh('free','-m').splitlines()[1].split()[6]);assert n<2 and avail>=12288,(n,avail)
out=sh('sudo','-n',sup,'template-build','--store',store,str(p));result=json.loads(out);print(json.dumps(result),flush=True);(base/'public-template.json').write_text(json.dumps(result))
