#!/usr/bin/env python3
"""Real Go/Rust process round trips; no worker/engine or host privilege."""
import json
from pathlib import Path
import socket
import subprocess
import tempfile

ROOT=Path(__file__).resolve().parents[1]


def main():
    subprocess.run(['cargo','build','--locked','--examples','-p','lamp-contracts','-p','lamp-worker-protocol'],cwd=ROOT,check=True)
    with tempfile.TemporaryDirectory() as tmp:
        go=str(Path(tmp)/'probe')
        subprocess.run(['go','build','-o',go,'./tools/contractprobe'],cwd=ROOT,check=True)
        target=Path(__import__('os').environ.get('CARGO_TARGET_DIR',ROOT/'target'))
        rust=str(target/'debug/examples/contract_json')
        values=[('scope-json',{}),('scope-json',{'sandbox_ids':[]}),('scope-json',{'sandbox_ids':['sb_1'],'limits':{'token_limit':18446744073709551615}}),('input-json',{'session_id':'se_1','lease_id':'le_1','location_epoch':18446744073709551615,'sequence':18446744073709551615,'data_base64':'8J+SqQ==','idempotency_key':'lk1.1.hs.nonce','expected_revision':18446744073709551615})]
        for mode,value in values:
            original=json.dumps(value,ensure_ascii=False).encode()
            step=subprocess.check_output([go,mode],input=original)
            step=subprocess.check_output([rust,mode],input=step)
            step=subprocess.check_output([go,mode],input=step)
            assert json.loads(step)==value,(mode,step)
        for program in [go,rust]:
            assert subprocess.run([program,'scope-json'],input=b'{"host_path":"/tmp"}',stdout=subprocess.PIPE,stderr=subprocess.PIPE).returncode!=0
        # Both real binaries exchange framed Protobuf over a local Unix socket.
        left,right=socket.socketpair(socket.AF_UNIX,socket.SOCK_STREAM)
        with left,right:
            server=subprocess.Popen([str(target/'debug/examples/contract_worker')],stdin=right,stdout=right,stderr=subprocess.PIPE)
            client=subprocess.Popen([go,'worker-client'],stdin=left,stdout=left,stderr=subprocess.PIPE)
            try:
                _,client_err=client.communicate(timeout=20)
                _,server_err=server.communicate(timeout=20)
                assert client.returncode==server.returncode==0,(client_err,server_err)
            finally:
                for p in [client,server]:
                    if p.poll() is None:p.kill();p.wait()
    print('PASS: Go → Rust → Go JSON retains uint64, Unicode/base64 and absent/empty scope selectors')
    print('PASS: Go request → Unix socket → Rust typed Protobuf → Go response')

if __name__=='__main__':main()
