#!/usr/bin/env python3
"""Build a reference package from exact executable bytes; never approve/install it."""
import argparse
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def main():
    contract = json.loads((ROOT / 'contracts/plugin/schema.json').read_text())
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--executable', required=True, type=Path)
    parser.add_argument('--type', required=True, choices=contract['$defs']['Type']['enum'])
    parser.add_argument('--publisher', default='reference.lamp')
    parser.add_argument('--version', default='1.0.0')
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    binary = args.executable.read_bytes()
    if not binary or len(binary) > 256 << 20:
        parser.error('executable must be nonempty and at most 256 MiB')
    kind = args.type
    trust = 'engine_trusted' if kind == 'engine' else 'credential' if kind == 'credential_source' else 'confined'
    manifest = {
        'type': kind, 'publisher': args.publisher, 'version': args.version,
        'digest': hashlib.sha256(binary).hexdigest(), 'protocol_version': 'lamp.plugin/1',
        'trust_class': trust, 'permissions': [kind + '.use'],
        'limits': {key: value['default'] for key, value in contract['$defs']['Limits']['properties'].items()},
        'config_schema': {'type': 'object'},
        'compatibility': {'api_major': 1, 'min_minor': 0, 'max_minor': 1},
        'required_features': ['typed-grants', 'cancel'],
        'methods': {'run': {
            'input': {'type': 'object', 'properties': {'payload': {'type': 'string', 'maxLength': 256}}, 'required': ['payload']},
            'result': {'type': 'object', 'properties': {'receipt': {'type': 'string', 'maxLength': 128}}, 'required': ['receipt']},
        }},
    }
    # Deliberately refuse existing output, retaining any failed/partial package.
    args.output.mkdir(mode=0o700)
    worker = args.output / 'worker'
    with worker.open('xb') as output:
        output.write(binary)
    worker.chmod(0o500)
    raw = (json.dumps(manifest, indent=2, sort_keys=True) + '\n').encode()
    with (args.output / 'manifest.json').open('xb') as output:
        output.write(raw)
    print(json.dumps({'package': str(args.output), 'manifest_digest': hashlib.sha256(raw).hexdigest(),
                      'executable_digest': manifest['digest'], 'approved': False}))


if __name__ == '__main__':
    main()
