#!/usr/bin/env python3
"""Generate/check both checked-in Protobuf bindings with pinned generators."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile

ROOT=Path(__file__).resolve().parents[1]


def main():
    parser=argparse.ArgumentParser();parser.add_argument('--check',action='store_true');args=parser.parse_args()
    version=subprocess.check_output(['protoc','--version'],text=True).strip()
    if version!='libprotoc 35.0':raise SystemExit('regeneration requires protoc 35.0 (build/test does not require protoc)')
    env=dict(os.environ);env['GOBIN']=str(ROOT/'.tools/bin');env['PATH']=env['GOBIN']+os.pathsep+env['PATH']
    subprocess.run(['go','install','google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11'],cwd=ROOT,env=env,check=True)
    with tempfile.TemporaryDirectory() as tmp:
        subprocess.run(['protoc','-I','contracts/worker','--go_out='+tmp,'--go_opt=module=lamp.eilmer.si','contracts/worker/worker.proto'],cwd=ROOT,env=env,check=True)
        subprocess.run(['cargo','run','--locked','-p','lamp-protogen','--',tmp],cwd=ROOT,check=True)
        rust=Path(tmp)/'lamp.worker.v1.rs'
        subprocess.run(['rustfmt','--edition','2024',str(rust)],check=True)
        for source,target in [(Path(tmp)/'pkg/workerpb/worker.pb.go',ROOT/'pkg/workerpb/worker.pb.go'),(rust,ROOT/'crates/lamp-worker-protocol/src/lamp.worker.v1.rs')]:
            if args.check:
                if source.read_bytes()!=target.read_bytes():raise SystemExit('stale protobuf binding: '+str(target.relative_to(ROOT)))
            else:target.write_bytes(source.read_bytes())
    print('worker codegen: clean' if args.check else 'worker codegen: generated')

if __name__=='__main__':main()
