#!/usr/bin/env python3
"""Host-side owner work for tools/install_demo.py, driven through the generated
Python SDK. Prints one JSON line per command; never prints the bearer.

  save PREFIX FILE JOB      upload FILE, publish it as an artifact, back it up
  check PREFIX ARTIFACT SHA download the artifact and verify its SHA-256
  restore PREFIX SHA        restore every recovered backup and verify bytes
"""
import base64
import hashlib
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), 'sdk-python'))
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'sdk', 'python'))
from lamp_sdk import LampClient, LampError  # noqa: E402

CHUNK = 1 << 20


def client(prefix):
    token = open(os.path.join(prefix, 'config', 'owner.token')).read().strip()
    return LampClient(socket=os.path.join(prefix, 'run', 'api.sock'), token=token)


def done(c, accepted):
    op = c.wait_operation(accepted['operation_id'])
    if op['state'] != 'succeeded':
        raise SystemExit(json.dumps({'failed': op.get('operation_name'), 'outcome': op.get('outcome')}))
    return op


def download(c, artifact_id):
    out, offset = b'', 0
    while True:
        ch = c.artifact_download(artifact_id=artifact_id, offset_bytes=offset, length_bytes=CHUNK)
        ch = ch.get('data', ch)
        data = base64.b64decode(ch['data_base64'])
        out += data
        offset += len(data)
        if ch['eof'] or not data:
            return out


def save(prefix, path, job):
    c = client(prefix)
    data = open(path, 'rb').read()
    sha = hashlib.sha256(data).hexdigest()
    blob = done(c, c.blob_create(size_bytes=len(data), sha256=sha, media_type='application/octet-stream'))
    blob_id = blob['resource_ids'][0]['id']
    for off in range(0, len(data), CHUNK):
        part = data[off:off + CHUNK]
        done(c, c.blob_upload(blob_id=blob_id, offset_bytes=off, data_base64=base64.b64encode(part).decode(),
                              chunk_sha256=hashlib.sha256(part).hexdigest(), expected_revision=c.blob_get(blob_id=blob_id)['data']['revision']))
    done(c, c.blob_finalize(blob_id=blob_id, sha256=sha, size_bytes=len(data), expected_revision=c.blob_get(blob_id=blob_id)['data']['revision']))
    art = done(c, c.artifact_create(job_id=job, download_blob_id=blob_id))
    art_id = art['resource_ids'][0]['id']
    bk = done(c, c.backup_create(resource_kind='artifact', resource_id=art_id))
    bk_id = bk['resource_ids'][0]['id']
    b = c.backup_get(backup_id=bk_id)['data']
    print(json.dumps({'saved': path.rsplit('/', 1)[-1], 'bytes': len(data), 'sha256': sha, 'artifact_id': art_id, 'backup_id': bk_id,
                      'recovery_state': b.get('recovery_state'), 'key_envelopes': [e['recipient_kind'] + ':' + e['wrapping_algorithm'] for e in b.get('key_envelopes', [])]}))


def check(prefix, art_id, sha):
    got = hashlib.sha256(download(client(prefix), art_id)).hexdigest()
    print(json.dumps({'artifact_id': art_id, 'sha256': got, 'matches': got == sha}))
    if got != sha:
        raise SystemExit(1)


def restore(prefix, sha):
    c = client(prefix)
    for b in c.backup_list()['items']:
        if b.get('labels', {}).get('lamp.recovered') != 'owner-recovery-key' or b['state'] != 'ready':
            continue
        op = done(c, c.backup_restore(backup_id=b['id'], expected_revision=b['revision']))
        restored = [r['id'] for r in op['resource_ids'] if r['kind'] == 'artifact'][0]
        got = hashlib.sha256(download(c, restored)).hexdigest()
        print(json.dumps({'recovered_backup': b['id'], 'restored_artifact': restored, 'sha256': got, 'matches': got == sha}))
        if got != sha:
            raise SystemExit(1)


if __name__ == '__main__':
    cmd, args = sys.argv[1], sys.argv[2:]
    try:
        {'save': save, 'check': check, 'restore': restore}[cmd](*args)
    except LampError as e:
        raise SystemExit(json.dumps({'error': e.code, 'message': str(e)}))
