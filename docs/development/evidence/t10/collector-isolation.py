import os,subprocess
# Called only by unshare --mount --propagation private, never in the host namespace.
assert os.readlink('/proc/self/ns/mnt') != os.readlink('/proc/1/ns/mnt')
mounts=[]
for line in open('/proc/self/mountinfo'):
    target=line.split()[4]
    if target.startswith('/var/lib/lamp-t7/'):
        mounts.append(target)
for target in sorted(set(mounts),key=len,reverse=True):
    subprocess.run(['umount','-l',target],check=True)
assert all('/var/lib/lamp-t7/' not in line.split()[4] for line in open('/proc/self/mountinfo'))
exec(compile(open('/home/owner/lamp-t10-control-core/collector-diagnostic.py').read(),'collector-diagnostic.py','exec'))
