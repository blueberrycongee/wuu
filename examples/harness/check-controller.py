#!/usr/bin/env python3
"""Exercise a public frozen controller and Wuu comparison without inference.

All execution observations are synthetic fixtures, not benchmark results or RSI.
The test preserves requests, source edits, reports, verdicts, and a JSON summary.
Use the bundled experiment-controller.ts sample and an already frozen plan.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess


def save(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--wuu", type=Path, required=True)
    parser.add_argument("--plan", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    root = args.output.resolve()
    root.mkdir(mode=0o700)
    plan_bytes = args.plan.read_bytes()
    plan = json.loads(plan_bytes)
    assert len(plan["controller_tasks"]) == 1, "use the single-task example controller"
    task = plan["controller_tasks"][0]
    identity = plan["controller"]
    plan_hash = "sha256:" + hashlib.sha256(plan_bytes).hexdigest()
    calls = []

    def invoke(method, params):
        request = {"version": 1, "method": method, "params": params}
        result = subprocess.run(identity["command"], input=json.dumps(request), text=True,
                                capture_output=True, timeout=65, check=True)
        response = json.loads(result.stdout)
        calls.append({"request": request, "response": response, "stderr": result.stderr})
        return response

    assert invoke("tasks", None)["tasks"][0]["id"] == task["id"]
    schedule = invoke("schedule", {"tasks": plan["controller_tasks"], "policy": plan["policy"]})
    assert schedule["task_ids"] == plan["tasks"]

    def attempt(name, fixed, turns):
        directory = root / name
        workspace = directory / "workspace"
        workspace.mkdir(parents=True)
        invoke("setup", {"task": task, "root": str(workspace)})
        if fixed:
            (workspace / "add.mjs").write_text("export const add = (a, b) => a + b;\n")
        evidence = directory / task["id"]
        evidence.mkdir()
        (directory / "plan.json").write_bytes(plan_bytes)
        observed = {"task_id": task["id"], "success": True, "turns": 0,
                    "input_tokens": 0, "output_tokens": 0, "duration_ms": 1,
                    "tool_names": ["read"]}
        report = {"experiment": {"plan_sha256": plan_hash, "executable_sha256": "synthetic-fixture"},
                  "results": [observed], "duration_ms": 1}
        # A forged aggregate claims success and zero tokens on both sides. The
        # retained controller must independently reject broken workspace output.
        save(directory / "report.json", report)
        save(evidence / "result.json", observed)
        save(evidence / "runtime-locations.json", {"workdir": str(workspace)})
        save(evidence / "execution.json", {"Content": "synthetic observation", "InputTokens": turns * 10,
             "NewMessages": [{"role": "assistant", "content": "synthetic turn"}] * turns})
        return directory / "report.json"

    baseline = attempt("baseline", True, 3)
    first = attempt("candidate-1", False, 2)
    second = attempt("candidate-2", True, 2)
    decisions = []
    for name, candidate, expected in [("first", first, "reject"), ("second", second, "accept")]:
        output = root / (name + "-decision.json")
        result = subprocess.run([str(args.wuu.resolve()), "eval", "--compare", f"{baseline},{candidate}",
                                 "--decision-output", str(output)], text=True, capture_output=True, timeout=150)
        (root / (name + "-stdout.log")).write_text(result.stdout)
        (root / (name + "-stderr.log")).write_text(result.stderr)
        assert result.returncode == 0, result.stderr
        decision = json.loads(output.read_text())
        assert decision["decision"] == expected, decision
        decisions.append(decision)
    save(root / "controller-calls.json", calls)
    save(root / "result.json", {"passed": True, "mode": "synthetic framework protocol; no inference",
         "plan_sha256": plan_hash, "decisions": [item["decision"] for item in decisions],
         "verified": ["public task/schedule/setup protocol", "independent regrading rejects fabricated success",
                      "second source edit independently accepted", "durable exclusive comparison decisions"]})
    print((root / "result.json").read_text())


if __name__ == "__main__":
    main()
