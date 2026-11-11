import importlib.util
import json
from pathlib import Path
import unittest
from jsonschema import Draft202012Validator

ROOT=Path(__file__).resolve().parents[2]
SCHEMA=json.loads((ROOT/'contracts/public/schema.json').read_text())
loader=importlib.util.spec_from_file_location('check_contracts',ROOT/'tools/check_contracts.py')
conformance=importlib.util.module_from_spec(loader);loader.loader.exec_module(conformance)


class Contracts(unittest.TestCase):
    def test_all_schemas_well_formed(self):
        Draft202012Validator.check_schema(SCHEMA)
        for name,s in SCHEMA['$defs'].items():
            Draft202012Validator.check_schema(s)
        self.assertTrue(conformance.check())


if __name__=='__main__':unittest.main()
