#!/usr/bin/env python3
"""Record a real RFC 6962 CT entry and verify SCT/STH/inclusion evidence.

Uses cryptography's X.509/precertificate and signature implementations. This is
an on-demand public observation, not an issuance gate or a claim that an SCT
promise alone is an included log entry. Tiled-only logs are reported separately.
"""
import argparse
import base64
import datetime
import hashlib
import json
from pathlib import Path
import struct
import urllib.error
import urllib.parse
import urllib.request

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec, padding, rsa
from cryptography.x509.oid import ExtensionOID


def download(url):
    if not url.startswith('https://'):
        raise ValueError('HTTPS CT endpoint required')
    with urllib.request.urlopen(url, timeout=30) as response:
        raw = response.read(2 * 1024 * 1024 + 1)
    if len(raw) > 2 * 1024 * 1024:
        raise ValueError('bounded CT response required')
    return json.loads(raw)


def verify(key, signature, message):
    if isinstance(key, ec.EllipticCurvePublicKey):
        key.verify(signature, message, ec.ECDSA(hashes.SHA256()))
    elif isinstance(key, rsa.RSAPublicKey):
        key.verify(signature, message, padding.PKCS1v15(), hashes.SHA256())
    else:
        raise ValueError('unsupported CT signing key')


def inclusion(leaf, index, size, path, expected):
    if not 0 <= index < size or len(path) > 64:
        raise ValueError('invalid CT proof bounds')
    fn, sn, root = index, size - 1, leaf
    for node in path:
        if len(node) != 32 or sn == 0:
            raise ValueError('invalid CT proof node')
        if fn & 1 or fn == sn:
            root = hashlib.sha256(b'\x01' + node + root).digest()
            while not fn & 1 and fn:
                fn >>= 1
                sn >>= 1
        else:
            root = hashlib.sha256(b'\x01' + root + node).digest()
        fn >>= 1
        sn >>= 1
    if sn or root != expected:
        raise ValueError('CT inclusion root mismatch')


def observe(chain, log_list):
    leaf, issuer = chain[0], chain[1]
    issuer_spki = issuer.public_key().public_bytes(serialization.Encoding.DER, serialization.PublicFormat.SubjectPublicKeyInfo)
    issuer_hash = hashlib.sha256(issuer_spki).digest()
    logs = {}
    for operator in log_list['operators']:
        for log in operator.get('logs', []) + operator.get('tiled_logs', []):
            logs[base64.b64decode(log['log_id'])] = log
    evidence = {
        'certificate_sha256': leaf.fingerprint(hashes.SHA256()).hex(),
        'serial': format(leaf.serial_number, 'x'),
        'dns_names': leaf.extensions.get_extension_for_class(x509.SubjectAlternativeName).value.get_values_for_type(x509.DNSName),
        'observed_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
        'entries': [],
        'not_observed': [],
    }
    scts = leaf.extensions.get_extension_for_oid(ExtensionOID.PRECERT_SIGNED_CERTIFICATE_TIMESTAMPS).value
    for sct in scts:
        log = logs.get(sct.log_id)
        if not log:
            evidence['not_observed'].append({'log_id': sct.log_id.hex(), 'reason': 'not in the downloaded Chrome log list'})
            continue
        if not log.get('url'):
            evidence['not_observed'].append({'log_id': sct.log_id.hex(), 'log': log['description'], 'reason': 'tiled log; this observer implements RFC 6962 v1 endpoints'})
            continue
        key_der = base64.b64decode(log['key'])
        if hashlib.sha256(key_der).digest() != sct.log_id:
            raise ValueError('CT log key ID mismatch')
        key = serialization.load_der_public_key(key_der)
        timestamp = int(sct.timestamp.replace(tzinfo=datetime.timezone.utc).timestamp()) * 1000 + sct.timestamp.microsecond // 1000
        tbs = leaf.tbs_precertificate_bytes
        extensions = sct.extension_bytes
        entry = issuer_hash + len(tbs).to_bytes(3, 'big') + tbs
        timestamped = struct.pack('>QH', timestamp, 1) + entry + struct.pack('>H', len(extensions)) + extensions
        # version 0 + signature_type certificate_timestamp (also leaf_type timestamped_entry).
        message = b'\x00\x00' + timestamped
        if sct.signature_hash_algorithm.name != 'sha256':
            raise ValueError('unsupported SCT hash')
        verify(key, sct.signature, message)
        leaf_hash = hashlib.sha256(b'\x00' + message).digest()
        url = log['url'].rstrip('/')
        sth = download(url + '/ct/v1/get-sth')
        signature = base64.b64decode(sth['tree_head_signature'])
        expected_algorithm = 3 if isinstance(key, ec.EllipticCurvePublicKey) else 1
        if len(signature) < 4 or signature[0] != 4 or signature[1] != expected_algorithm or len(signature[4:]) != int.from_bytes(signature[2:4], 'big'):
            raise ValueError('invalid CT signed tree head signature')
        root = base64.b64decode(sth['sha256_root_hash'])
        verify(key, signature[4:], b'\x00\x01' + struct.pack('>QQ', sth['timestamp'], sth['tree_size']) + root)
        try:
            query = urllib.parse.urlencode({'hash': base64.b64encode(leaf_hash).decode(), 'tree_size': sth['tree_size']})
            proof = download(url + '/ct/v1/get-proof-by-hash?' + query)
        except urllib.error.HTTPError as error:
            if error.code == 404:
                evidence['not_observed'].append({'log_id': sct.log_id.hex(), 'log': log['description'], 'reason': 'SCT verified; inclusion not yet observed in the current signed tree'})
                continue
            raise
        path = [base64.b64decode(node) for node in proof['audit_path']]
        inclusion(leaf_hash, proof['leaf_index'], sth['tree_size'], path, root)
        entry_url = url + '/ct/v1/get-entries?' + urllib.parse.urlencode({'start': proof['leaf_index'], 'end': proof['leaf_index']})
        fetched = download(entry_url)
        entries = fetched.get('entries', [])
        if len(entries) != 1 or base64.b64decode(entries[0]['leaf_input']) != message:
            raise ValueError('CT entry does not match the included certificate')
        evidence['entries'].append({
            'log': log['description'], 'log_id': sct.log_id.hex(), 'url': entry_url,
            'leaf_index': proof['leaf_index'], 'leaf_hash': base64.b64encode(leaf_hash).decode(),
            'sct_timestamp': sct.timestamp.isoformat() + 'Z',
            'sct_signature_verified': True, 'sth_signature_verified': True, 'inclusion_verified': True,
            'signed_tree_head': sth, 'audit_path': proof['audit_path'],
            'leaf_input': entries[0]['leaf_input'], 'log_public_key': log['key'],
        })
    return evidence


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--certificate', required=True, type=Path)
    parser.add_argument('--out', required=True, type=Path)
    parser.add_argument('--log-list', type=Path)
    args = parser.parse_args()
    chain = x509.load_pem_x509_certificates(args.certificate.read_bytes())
    if len(chain) < 2:
        raise ValueError('leaf and issuer certificate required')
    logs = json.loads(args.log_list.read_text()) if args.log_list else download('https://www.gstatic.com/ct/log_list/v3/log_list.json')
    result = observe(chain, logs)
    # Preserve missing observations instead of turning an SCT promise into an inclusion claim.
    with args.out.open('x') as output:
        json.dump(result, output, indent=2)
        output.write('\n')
    print(json.dumps({'certificate_sha256': result['certificate_sha256'], 'verified_entries': len(result['entries']), 'not_observed': result['not_observed']}))
    if not result['entries']:
        raise SystemExit('No included CT entry observed; evidence records the missing observation')


if __name__ == '__main__':
    main()
