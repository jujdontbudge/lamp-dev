import os, sys, json, base64, hashlib, time, stat
request = json.loads(sys.argv[1])
action, path = request['action'], request['path']
try:
    def entry(path, name=None):
        st = os.lstat(path)
        return dict(name=name or os.path.basename(path), path=path, type='directory' if stat.S_ISDIR(st.st_mode) else 'symlink' if stat.S_ISLNK(st.st_mode) else 'file', size_bytes=st.st_size)
    if action == 'read':
        with open(path, 'rb') as source:
            source.seek(request.get('offset_bytes', 0))
            data = source.read(min(request.get('length_bytes', 24576),24576))
            eof = source.tell() >= os.fstat(source.fileno()).st_size
        result = dict(data_base64=base64.b64encode(data).decode(), sha256=hashlib.sha256(data).hexdigest(), eof=eof)
    elif action == 'list':
        names = sorted(n for n in os.listdir(path) if n > request.get('cursor',''))
        count = min(request.get('limit',100),100)
        result = dict(items=[entry(os.path.join(path,n),n) for n in names[:count]])
        if len(names)>count: result['next_cursor']=names[count-1]
    elif action == 'stat':
        result = dict(items=[entry(path)])
    elif action == 'delete':
        if os.path.isdir(path) and not os.path.islink(path):
            if request.get('recursive'): __import__('shutil').rmtree(path)
            else: os.rmdir(path)
        else: os.unlink(path)
        result = {}
    elif action == 'rename':
        os.rename(path, request['destination'])
        result = {}
    elif action == 'search':
        needle=request['query'].encode()
        found=[]
        for root, dirs, files in os.walk(path, followlinks=False):
            for name in dirs+files:
                p=os.path.join(root,name)
                match=request['query'] in name
                if request.get('contents') and not match and not os.path.islink(p) and os.path.isfile(p):
                    with open(p,'rb') as stream:
                        tail=b''
                        while chunk:=stream.read(32768):
                            if needle in tail+chunk: match=True;break
                            tail=(tail+chunk)[-max(1,len(needle)):]
                if match: found.append(entry(p))
                if len(found)>=100: break
            if len(found)>=100: break
        result=dict(items=found)
        result['truncated']=len(found)>=100
    elif action == 'watch':
        def snapshot():
            return {n:[st.st_mtime_ns,st.st_size] for n in os.listdir(path) if (st:=os.lstat(os.path.join(path,n)))}
        old=json.loads(base64.b64decode(request['cursor'],validate=True)) if request.get('cursor') else snapshot()
        if not isinstance(old,dict) or any(not isinstance(n,str) or not isinstance(v,list) or len(v)!=2 for n,v in old.items()): raise ValueError('invalid watch cursor')
        deadline=time.monotonic()+request.get('wait_ms',0)/1000
        while True:
            new=snapshot()
            if old!=new or time.monotonic()>=deadline:break
            time.sleep(.05)
        result=dict(items=[entry(os.path.join(path,n)) if n in new else dict(name=n,path=os.path.join(path,n),type='deleted') for n in sorted(old.keys()|new.keys()) if old.get(n)!=new.get(n)],next_cursor=base64.b64encode(json.dumps(new).encode()).decode())
    else: raise ValueError('unsupported file action')
except FileNotFoundError:
    result={'error':'NOT_FOUND','message':'guest path does not exist'}
except (ValueError,NotADirectoryError,IsADirectoryError):
    result={'error':'INVALID_ARGUMENT','message':'invalid guest path or file operation'}
except PermissionError:
    result={'error':'SCOPE_DENIED','message':'guest path permission denied'}
print(json.dumps(result))
