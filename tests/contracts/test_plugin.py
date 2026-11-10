import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from jsonschema import Draft202012Validator, ValidationError

ROOT = Path(__file__).resolve().parents[2]
SCHEMA = json.loads((ROOT / 'contracts/plugin/schema.json').read_text())


class PluginContract(unittest.TestCase):
    def test_embedded_schema_rejects_empty_enum_and_null_keywords(self):
        validator = Draft202012Validator({'$ref': '#/$defs/Schema', '$defs': SCHEMA['$defs']})
        with self.assertRaises(ValidationError):
            validator.validate({'type': 'object', 'enum': []})
        for keyword in SCHEMA['$defs']['Schema']['properties']:
            with self.subTest(keyword=keyword), self.assertRaises(ValidationError):
                validator.validate({'type': 'object', keyword: None})
        validator.validate({'type': 'null', 'enum': [None]})

    def test_reference_packages_all_ten_types(self):
        Draft202012Validator.check_schema(SCHEMA)
        validator = Draft202012Validator(SCHEMA)
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            binary = root / 'fixture-executable'
            binary.write_bytes(b'independently selected fixture bytes')
            kinds = SCHEMA['$defs']['Type']['enum']
            self.assertEqual(len(kinds), 10)
            for kind in kinds:
                with self.subTest(kind=kind):
                    output = root / kind
                    command = [sys.executable, str(ROOT / 'tools/package_plugin.py'),
                               '--executable', str(binary), '--type', kind,
                               '--output', str(output)]
                    result = subprocess.run(command, capture_output=True, text=True)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    manifest = json.loads((output / 'manifest.json').read_text())
                    validator.validate(manifest)
                    self.assertEqual(manifest['digest'], hashlib.sha256(binary.read_bytes()).hexdigest())
                    self.assertEqual((output / 'worker').read_bytes(), binary.read_bytes())
                    self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)
                    for field, value in [('protocol_version', 'lamp.plugin/2'),
                                         ('permissions', ['ram.read']),
                                         ('permissions', ['console.use' if kind == 'engine' else 'engine.use']),
                                         ('digest', 'not a hash'),
                                         ('ambient_network', True)]:
                        invalid = copy.deepcopy(manifest)
                        invalid[field] = value
                        with self.assertRaises(ValidationError):
                            validator.validate(invalid)


if __name__ == '__main__':
    unittest.main()
