"""Validate captured T3 wire documents against the canonical public schema."""
import json
from pathlib import Path
import sys

from jsonschema import Draft202012Validator, FormatChecker

root = Path(__file__).resolve().parents[3]
schema = json.loads((root / "contracts/public/schema.json").read_text())
directory = Path(sys.argv[1])
names = ["SandboxCreateResult", "SandboxGetResult", "ExecCreateResult", "SandboxForkResult", "OperationGetResult"]
for name in names:
    value = json.loads((directory / (name + ".json")).read_text())
    Draft202012Validator(
        {"$ref": "#/$defs/" + name, "$defs": schema["$defs"]},
        format_checker=FormatChecker(),
    ).validate(value)
    print(f"PASS {name}: captured CLI/API output matches canonical schema")
print("PASS 5 captured result documents; fixture evidence only, no microVM/performance claim")
