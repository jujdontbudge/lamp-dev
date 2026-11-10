import argparse
import json
from pathlib import Path
import platform
import sys
import traceback

from benchmarks.engine import EngineHarness
from benchmarks.evidence import Evidence
from benchmarks.protocol import STORAGE_FORKS, plan
from benchmarks.public import PublicLamp, token_from_environment
from benchmarks.storage import storage_suite


def main():
    parser = argparse.ArgumentParser(description="Independent Lamp public-boundary engine gate")
    parser.add_argument("--suite", choices=("E01", "E02", "E04", "all"), required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--metadata", required=True)
    transport = parser.add_mutually_exclusive_group()
    transport.add_argument("--api-url")
    transport.add_argument("--cli")
    parser.add_argument("--template", default="tp_bench")
    parser.add_argument("--workload", default="/opt/lamp/bench-workload.py")
    parser.add_argument("--guest-python", default="python3")
    parser.add_argument("--native-python", default=sys.executable)
    parser.add_argument("--timeout", type=float, default=120)
    parser.add_argument("--extra-rows")
    parser.add_argument("--network-fixture")
    parser.add_argument("--proxy")
    parser.add_argument("--storage-fixture", help="JSON argv for an external disposable filesystem/block fixture")
    parser.add_argument("--observer", help="JSON argv for independent pause/RSS/disk/pressure collector")
    parser.add_argument("--directory", help="matched private-disk workload directory on native host and guest")
    parser.add_argument("--native-cpus", help="comma-separated native CPU IDs; count must match guest vCPUs")
    parser.add_argument("--model", action="store_true", help="diagnostic disposable backend, never E04 qualification")
    parser.add_argument("--smoke", action="store_true", help="reduced diagnostic run; cannot pass a gate")
    args = parser.parse_args()
    metadata = json.loads(Path(args.metadata).read_text())
    configuration = {key: value for key, value in vars(args).items() if key != "metadata"}
    evidence = Evidence(args.output, metadata, configuration)
    results = {}
    lamp = None
    try:
        if args.suite in ("E01", "E02", "all"):
            lamp = PublicLamp(args.api_url, args.cli, args.timeout, token_from_environment())
            harness = EngineHarness(lamp, evidence, args.template, args.workload, args.guest_python, args.native_python,
                                    directory=args.directory,
                                    fixture=json.loads(Path(args.network_fixture).read_text()) if args.network_fixture else None,
                                    proxy=args.proxy, observer=json.loads(args.observer) if args.observer else None,
                                    native_cpus=[int(cpu) for cpu in args.native_cpus.split(",")] if args.native_cpus else None)
            if args.suite in ("E01", "all"):
                extra = json.loads(Path(args.extra_rows).read_text()) if args.extra_rows else []
                results["E01"] = harness.e01(extra, args.smoke)
            if args.suite in ("E02", "all"):
                results["E02"] = harness.e02(plan()[3], args.smoke)
        if args.suite in ("E04", "all"):
            if args.model:
                command = [sys.executable, str(Path(__file__).with_name("block_fixture.py"))]
            elif args.storage_fixture:
                command = json.loads(args.storage_fixture)
            else:
                raise ValueError("E04 requires --storage-fixture or diagnostic --model")
            results["E04"] = storage_suite(command, evidence, count=30 if args.smoke else STORAGE_FORKS, model=args.model)
    except Exception as error:
        evidence.observe("runner_failure", {"type": type(error).__name__, "message": str(error), "traceback": traceback.format_exc()})
        results["runner"] = {"status": "fail", "error": type(error).__name__ + ": " + str(error)}
    finally:
        if lamp:
            for call in lamp.calls:
                evidence.observe("public_call", call)
        if args.suite == "all":
            for name in ("E01", "E02", "E04"):
                results.setdefault(name, {"status": "incomplete"})
        operating_system = metadata.get("os", {})
        native = metadata.get("lane") == "LX" and metadata.get("topology") == "native" and isinstance(operating_system, dict) and operating_system.get("kernel") == platform.system() == "Linux"
        summary = evidence.finish(results, diagnostic=args.smoke or args.model or (args.suite != "E04" and not native))
    print(json.dumps({"status": summary["status"], "evidence": str(evidence.root.resolve())}))
    return 0 if summary["status"] == "pass" else 1


if __name__ == "__main__":
    sys.exit(main())
