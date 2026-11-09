#!/usr/bin/env python3
"""Exercise the real run() function using only isolated synthetic POSIX processes."""
import argparse
import json
import os
from pathlib import Path
import runpy
import signal
import subprocess
import sys
import time

CHILD = r'''
import signal, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
print("ready", flush=True)
while True:
    time.sleep(0.1)
'''
PARENT = r'''
import json, os, signal, subprocess, sys, time
from pathlib import Path
mode, metadata = sys.argv[1:]
if mode == "normal":
    Path(metadata).write_text(json.dumps({"parent": os.getpid(), "pgid": os.getpgrp()}))
    raise SystemExit(0)
if mode == "all-ignore":
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
child = subprocess.Popen([sys.executable, "-c", CHILD], stdout=subprocess.PIPE, text=True)
assert child.stdout.readline().strip() == "ready"
Path(metadata).write_text(json.dumps({"parent": os.getpid(), "child": child.pid, "pgid": os.getpgrp()}))
if mode == "parent-exit":
    raise SystemExit(0)
if mode == "parent-failure":
    raise SystemExit(7)
while True:
    time.sleep(0.1)
'''


def running(pid):
    result = subprocess.run(["ps", "-p", str(pid), "-o", "stat="], capture_output=True, text=True)
    status = result.stdout.strip()
    return bool(status and not status.startswith("Z"))


def metadata_at(path):
    return json.loads(path.read_text()) if path.exists() else {}


def cleanup(metadata):
    if not metadata:
        return
    assert metadata["pgid"] == metadata["parent"]
    assert metadata["pgid"] != os.getpgrp()
    try:
        os.killpg(metadata["pgid"], signal.SIGKILL)
    except ProcessLookupError:
        pass
    deadline = time.monotonic() + 2
    while any(running(metadata[key]) for key in ("parent", "child") if key in metadata):
        if time.monotonic() >= deadline:
            raise RuntimeError(f"Synthetic process cleanup failed: {metadata}")
        time.sleep(0.02)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", type=Path, default=Path(__file__).with_name("run-pair.py"))
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if os.name != "posix":
        parser.error("POSIX required")
    args.output.mkdir(parents=True, exist_ok=False)
    functions = runpy.run_path(str(args.target.resolve()))
    fixture = args.output / "fixture.py"
    fixture.write_text("CHILD = " + repr(CHILD) + "\n" + PARENT)
    cases = []
    for mode, expected_exit, timeout in [
        ("normal", 0, 3),
        ("parent-exit", 0, 3),
        ("parent-failure", 7, 3),
        ("all-ignore", -signal.SIGKILL, 1),
    ]:
        directory = args.output / mode
        directory.mkdir()
        metadata_path = directory / "pids.json"
        case = {"case": mode}
        try:
            record = functions["run"]([sys.executable, fixture.resolve(), mode, metadata_path.resolve()], directory, timeout)
            metadata = metadata_at(metadata_path)
            if not metadata:
                raise AssertionError("Fixture failed before readiness")
            # KILL delivery is asynchronous; allow a short bounded observation window.
            deadline = time.monotonic() + 0.5
            survivors = []
            while True:
                survivors = [metadata[key] for key in ("parent", "child") if key in metadata and running(metadata[key])]
                if not survivors or time.monotonic() >= deadline:
                    break
                time.sleep(0.02)
            case.update(record=record, pids=metadata, survivors=survivors)
            case["passed"] = (record.get("exit_code") == expected_exit and not survivors
                              and (("failure" in record) == (mode == "all-ignore")))
        except Exception as error:
            case.update(passed=False, error=f"{type(error).__name__}: {error}")
        finally:
            cleanup(metadata_at(metadata_path))
        cases.append(case)
        print(json.dumps(case), flush=True)
    (args.output / "result.json").write_text(json.dumps(cases, indent=2) + "\n")
    return 0 if all(case["passed"] for case in cases) else 1


if __name__ == "__main__":
    raise SystemExit(main())
