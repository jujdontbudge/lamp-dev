#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Deploy the release Worker and its two public names after release publication.

Default is a read-only plan. --apply writes once per resource with pauses.
Credentials are read from files and are never printed or passed in argv values.
"""
import argparse
import ipaddress
import json
import time
import urllib.error
import urllib.request
from pathlib import Path

API = 'https://api.cloudflare.com/client/v4'
SCRIPT = 'lamp-site'
NAMES = ('lamp.eilmer.si', 'account.eilmer.si')


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('workers_token_file', type=Path)
    ap.add_argument('dns_token_file', type=Path)
    ap.add_argument('--apply', action='store_true')
    a = ap.parse_args()
    wt = a.workers_token_file.expanduser().read_text().strip()
    dt = a.dns_token_file.expanduser().read_text().strip()

    def call(token, path, method='GET', data=None, content_type='application/json'):
        if isinstance(data, dict):
            data = json.dumps(data).encode()
        req = urllib.request.Request(API + path, data=data, method=method,
                                     headers={'Authorization': 'Bearer ' + token,
                                              'Content-Type': content_type})
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                result = json.load(r)
        except urllib.error.HTTPError as e:
            raise SystemExit(f'Cloudflare {method} failed: HTTP {e.code}; no automatic retry') from None
        if not result.get('success'):
            raise SystemExit('Cloudflare operation failed; inspect account before retrying')
        return result['result']

    zone = call(dt, '/zones?name=eilmer.si')[0]
    zid, aid = zone['id'], zone['account']['id']
    routes = call(wt, f'/zones/{zid}/workers/routes')
    plans = []
    for name in NAMES:
        matching = [r for r in routes if r['pattern'] == name + '/*']
        if matching and any(r.get('script') != SCRIPT for r in matching):
            raise SystemExit(f'{name} route belongs to another Worker; refusing')
        records = [r for r in call(dt, f'/zones/{zid}/dns_records?name={name}')
                   if r['type'] in ('A', 'AAAA', 'CNAME')]
        if name == NAMES[1]:
            if len(records) > 1:
                raise SystemExit('account has multiple DNS records; refusing ambiguous replacement')
            if records:
                r = records[0]
                unchanged = r['type'] == 'AAAA' and r['content'] == '100::' and r['proxied']
                private = r['type'] in ('A', 'AAAA') and ipaddress.ip_address(r['content']).is_private
                if not (unchanged or private):
                    raise SystemExit('account DNS is not the private-address record; refusing replacement')
                if not unchanged:
                    plans.append((dt, f'/zones/{zid}/dns_records/{r["id"]}', 'PUT',
                                  {'type': 'AAAA', 'name': name, 'content': '100::', 'proxied': True}))
        if not records:
            plans.append((dt, f'/zones/{zid}/dns_records', 'POST',
                          {'type': 'AAAA', 'name': name, 'content': '100::', 'proxied': True}))
        if not matching:
            plans.append((wt, f'/zones/{zid}/workers/routes', 'POST',
                          {'pattern': name + '/*', 'script': SCRIPT}))
    print('Plan: update lamp-site; ensure lamp/account Worker routes; replace only private account DNS.')
    print(f'Additional DNS/route writes: {len(plans)}. Release assets must already be public.')
    if not a.apply:
        print('Read-only plan; no changes made.')
        return
    # A module upload needs multipart metadata; token values never enter the body.
    boundary = 'lamp-release-worker-upload'
    source = Path(__file__).with_name('worker.js').read_bytes()
    body = (f'--{boundary}\r\nContent-Disposition: form-data; name="metadata"\r\n'
            'Content-Type: application/json\r\n\r\n'
            '{"main_module":"worker.js","compatibility_date":"2026-11-01"}\r\n'
            f'--{boundary}\r\nContent-Disposition: form-data; name="worker.js"; filename="worker.js"\r\n'
            'Content-Type: application/javascript+module\r\n\r\n').encode()
    body += source + f'\r\n--{boundary}--\r\n'.encode()
    call(wt, f'/accounts/{aid}/workers/scripts/{SCRIPT}', 'PUT', body,
         'multipart/form-data; boundary=' + boundary)
    print('Updated lamp-site.')
    for token, path, method, data in plans:
        time.sleep(20)
        call(token, path, method, data)
        print(f'Applied {method} for {data.get("name", data.get("pattern"))}.')


if __name__ == '__main__':
    main()
