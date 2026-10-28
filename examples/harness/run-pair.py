#!/usr/bin/env python3
"""Run one frozen Wuu evaluation pair in separate processes; retain all evidence.

Source arguments are archives prepared by the caller (including dirty changes).
They are evidence, never executed or unpacked by this controller. The binaries
must have been built from those archives. No model credentials are copied.
"""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            value.update(block)
    return "sha256:" + value.hexdigest()


def save(path, value):
    with path.open("x", encoding="utf-8") as output:
        json.dump(value, output, indent=2)
        output.write("\n")


def snapshot(source, destination):
    # Exclusive creation prevents a retry from replacing the original evidence.
    with source.open("rb") as reader, destination.open("xb") as writer:
        shutil.copyfileobj(reader, writer)
    destination.chmod(source.stat().st_mode & 0o777)
    return {"path": str(destination), "sha256": digest(destination)}


def stop(process):
    deadline = time.monotonic() + 3
    try:
        os.killpg(process.pid, signal.SIGTERM)
        while True:
            process.poll()
            try:
                os.killpg(process.pid, 0)
            except ProcessLookupError:
                break
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                os.killpg(process.pid, signal.SIGKILL)
                break
            time.sleep(min(0.05, remaining))
    except ProcessLookupError:
        pass
    process.wait()


def run(command, directory, timeout):
    record = {"command": [str(value) for value in command], "timeout_seconds": timeout}
    started = time.monotonic()
    process = None
    try:
        with (directory / "stdout.log").open("xb") as out, (directory / "stderr.log").open("xb") as err:
            process = subprocess.Popen(record["command"], stdout=out, stderr=err, start_new_session=True)
            try:
                record["exit_code"] = process.wait(timeout=timeout)
                stop(process)
            except subprocess.TimeoutExpired:
                record["failure"] = "controller deadline exceeded"
                stop(process)
                record["exit_code"] = process.returncode
    except BaseException as error:
        if process is not None:
            stop(process)
            record["exit_code"] = process.returncode
        record["failure"] = str(error) or type(error).__name__
        raise
    finally:
        record["duration_ms"] = round((time.monotonic() - started) * 1000)
        save(directory / "process.json", record)
    return record


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", type=Path, required=True)
    parser.add_argument("--baseline", type=Path, required=True, help="baseline Wuu executable")
    parser.add_argument("--candidate-host", type=Path, help="defaults to the same executable")
    parser.add_argument("--candidate", type=Path, help="single-file TypeScript policy candidate")
    parser.add_argument("--source-baseline", type=Path, required=True, help="complete baseline source archive")
    parser.add_argument("--source-candidate", type=Path, help="required when candidate-host differs")
    parser.add_argument("--output", type=Path, required=True, help="new evidence directory")
    parser.add_argument("--timeout", type=float, required=True, help="predeclared wall-clock seconds per side")
    args = parser.parse_args()
    if os.name != "posix":
        parser.error("this example uses POSIX process groups")
    if not math.isfinite(args.timeout) or args.timeout <= 0:
        parser.error("timeout must be positive")
    if args.candidate_host and not args.source_candidate:
        parser.error("candidate-host requires source-candidate")
    plan = json.loads(args.plan.read_text())
    tasks = plan["tasks"]
    if args.candidate_host and not plan.get("controller"):
        parser.error("host comparisons require an independent frozen experiment controller")
    if plan["version"] != 1 or not tasks or len(set(tasks)) != len(tasks) or not isinstance(plan["concurrency"], int) or not 1 <= plan["concurrency"] <= 256:
        parser.error("expected a frozen version 1 plan with unique ordered tasks and bounded positive concurrency")
    output = args.output.resolve()
    output.mkdir(mode=0o700)
    record = {"status": "failed", "decision": "reject", "tasks": tasks, "denominator": len(tasks),
              "timeout_seconds_per_side": args.timeout, "order": ["baseline", "candidate"],
              "attempts": [], "cost": "see evaluation reports; monetary cost may be unavailable"}
    try:
        plan_copy = output / "plan.json"
        record["plan"] = snapshot(args.plan, plan_copy)
        record["baseline_source"] = snapshot(args.source_baseline, output / "baseline-source.archive")
        baseline = output / "baseline-host"
        record["baseline_host"] = snapshot(args.baseline, baseline)
        candidate_host = baseline
        if args.candidate_host:
            candidate_host = output / "candidate-host"
            record["candidate_host"] = snapshot(args.candidate_host, candidate_host)
            record["candidate_source"] = snapshot(args.source_candidate, output / "candidate-source.archive")
        else:
            record["candidate_host"] = record["baseline_host"]
            record["candidate_source"] = record["baseline_source"]
        candidate = None
        if args.candidate:
            candidate = output / "candidate.ts"
            record["candidate_plugin"] = snapshot(args.candidate, candidate)
        save(output / "conditions.json", {key: value for key, value in record.items() if key not in {"status", "decision", "attempts"}})
        for name, binary in [("baseline", baseline), ("candidate", candidate_host)]:
            attempt = output / name
            attempt.mkdir()
            evidence = attempt / "evaluation"
            command = [baseline if args.candidate_host else binary, "eval", "--plan", plan_copy, "--artifacts", evidence]
            if args.candidate_host:
                command += ["--candidate-host", binary]
            if name == "candidate" and candidate:
                command += ["--candidate", candidate]
            report_path = evidence / "report.json"
            entry = {"side": name, "report": str(report_path), "tasks": []}
            record["attempts"].append(entry)
            try:
                run(command, attempt, args.timeout)
            finally:
                entry["process"] = json.loads((attempt / "process.json").read_text())
                report = None
                if report_path.exists():
                    try:
                        report = json.loads(report_path.read_text())
                    except (ValueError, OSError) as error:
                        entry["report_error"] = str(error)
                results = report.get("results", []) if isinstance(report, dict) else []
                identity = report.get("experiment") if isinstance(report, dict) else None
                valid = (isinstance(identity, dict)
                         and identity.get("plan_sha256") == record["plan"]["sha256"]
                         and isinstance(results, list)
                         and all(isinstance(result, dict) for result in results)
                         and [result.get("task_id") for result in results] == tasks)
                entry["complete_report"] = valid
                for index, task_id in enumerate(tasks):
                    result = results[index] if valid else None
                    task_path = evidence / task_id / "result.json"
                    task_error = None
                    if result is None and task_path.exists():
                        try:
                            result = json.loads(task_path.read_text())
                            if not isinstance(result, dict) or result.get("task_id") != task_id:
                                raise ValueError("task result identity differs from frozen task")
                        except (ValueError, OSError) as error:
                            result = None
                            task_error = str(error)
                    # Partial results retain the grader's verdict and usage, but
                    # cannot authorize comparison or acceptance of a failed run.
                    task = {"task_id": task_id, "passed": bool(result and result.get("success")),
                            "recorded": result is not None,
                            "failure": task_error or (None if result else "task did not produce a result")}
                    if result is not None:
                        task["result"] = result
                        task["evidence"] = str(report_path if valid else task_path)
                    entry["tasks"].append(task)
                save(attempt / "ledger.json", entry)
        complete = all(attempt["complete_report"] for attempt in record["attempts"])
        if complete:
            comparison = output / "comparison"
            comparison.mkdir()
            paths = ",".join(attempt["report"] for attempt in record["attempts"])
            checked = run([baseline, "eval", "--compare", paths], comparison, 30)
            if checked.get("exit_code") != 0 or "failure" in checked:
                raise RuntimeError("comparison failed; see comparison/process.json")
            decision = json.loads((comparison / "stdout.log").read_text())
            if decision["decision"] not in {"accept", "reject", "revise"}:
                raise ValueError("unrecognized comparison decision")
            record["decision"] = decision["decision"]
            record["comparison"] = decision
            record["status"] = "completed"
            if any(attempt["process"].get("exit_code") != 0 or "failure" in attempt["process"]
                   for attempt in record["attempts"]):
                record["decision"] = "reject"
                record["failure"] = "evaluation process failed; retained reports were compared, acceptance is forbidden"
        else:
            record["failure"] = "a process or evaluation failed; pair rejected with the frozen denominator"
    except KeyboardInterrupt:
        record["status"] = "cancelled"
        record["failure"] = "controller interrupted; unfinished tasks count as failed"
    except Exception as error:
        record["failure"] = str(error)
    finally:
        admitted = {attempt["side"] for attempt in record["attempts"]}
        for name in record["order"]:
            if name not in admitted:
                record["attempts"].append({"side": name, "tasks": [
                    {"task_id": task_id, "passed": False, "recorded": False, "failure": "attempt did not complete"}
                    for task_id in tasks]})
        save(output / "pair.json", record)
        print(output / "pair.json")
    return 0 if record["status"] == "completed" else 1


if __name__ == "__main__":
    def interrupted(_signal, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    raise SystemExit(main())
