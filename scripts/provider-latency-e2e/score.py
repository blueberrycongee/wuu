#!/usr/bin/env python3
"""Summarize metadata-only JSONL; failed and unrun planned turns score as timeouts."""
import argparse
import json
import math
import statistics
from collections import defaultdict
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("reports", nargs="+", type=Path)
parser.add_argument("--baseline-label", default="baseline")
args = parser.parse_args()
groups = defaultdict(list)
for path in args.reports:
    rows = [json.loads(line) for line in path.read_text().splitlines() if line.strip()]
    if not rows or rows[0].get("kind") != "run":
        raise SystemExit(f"Missing run manifest: {path}")
    run = rows[0]
    observed = {}
    for row in rows[1:]:
        if row["kind"] != "turn":
            continue
        key = row["repeat"], row["turn"]
        if key in observed:
            raise SystemExit(f"Duplicate turn in {path}: {key}")
        observed[key] = row
    for turn in range(1, run["turns"] + 1):
        key = (run["provider"], run["model"], run["fixture_sha256"], run["records"], run["tool"],
               run["timeout_ms"], turn, run["transport"], run["gzip"], run["prewarm"], run["label"], run.get("force_tool", run["tool"]))
        for repeat in range(1, run["repeats"] + 1):
            row = observed.get((repeat, turn))
            groups[key].append(row)

results = []
for key, rows in groups.items():
    provider, model, fixture, records, tool, timeout, turn, transport, gzip, prewarm, label, force_tool = key
    successes = [r for r in rows if r and r["passed"]]
    first_text = sorted(r["first_text_ms"] for r in successes)
    penalized = [r["first_text_ms"] if r and r["passed"] and r["first_text_ms"] > 0 else timeout for r in rows]
    complete = [r["total_ms"] if r and r["passed"] else timeout for r in rows]
    reused = [v for r in rows if r for v in (r["transport"]["http_connections_reused"] or [])]
    states = [s for r in rows if r for s in (r["provider_states"] or [])]
    results.append({
        "provider": provider, "model": model, "fixture_sha256": fixture, "records": records, "tool": tool,
        "timeout_ms": timeout, "turn": turn, "transport": transport, "gzip": gzip, "prewarm": prewarm, "label": label, "force_tool": force_tool,
        "planned": len(rows), "passed": len(successes), "failed": sum(r is not None and not r["passed"] for r in rows),
        "unrun": sum(r is None for r in rows), "correctness_score": round(100 * len(successes) / len(rows), 2),
        "first_text_p50_ms": statistics.median(first_text) if first_text else None,
        "first_text_p90_ms": first_text[math.ceil(0.9 * len(first_text)) - 1] if first_text else None,
        "first_text_penalized_mean_ms": round(statistics.mean(penalized), 2),
        "completion_penalized_mean_ms": round(statistics.mean(complete), 2),
        "http_connections": len(reused), "http_connections_reused": sum(reused),
        "ws_requests": sum(s["transport"] == "websocket" for s in states),
        "ws_reused": sum(s.get("connection_reused", False) for s in states),
        "delta_requests": sum(s.get("previous_response_id_used", False) for s in states),
    })

# A baseline is the uncompressed SSE build, matched by workload, model, deadline
# and turn number. No index is emitted if its correctness gate failed.
def workload(r):
    return tuple(r[k] for k in ("provider", "model", "fixture_sha256", "records", "tool", "force_tool", "timeout_ms", "turn"))

baselines = {workload(r): r for r in results if r["label"] == args.baseline_label and r["transport"] == "sse" and not r["gzip"] and not r["prewarm"]}
for result in results:
    baseline = baselines.get(workload(result))
    result["latency_index_baseline_100"] = None
    if baseline and baseline["correctness_score"] == 100 and result["first_text_penalized_mean_ms"] > 0:
        result["latency_index_baseline_100"] = round(100 * baseline["first_text_penalized_mean_ms"] / result["first_text_penalized_mean_ms"], 2)

print(json.dumps({"score_contract": "Correctness is passed/planned * 100. Latency index is baseline/candidate failure-penalized mean first-text time * 100; higher is faster. Failures and unrun turns use the full turn deadline. Baseline must pass all its turns. Quantiles use successful turns only; small N does not establish tail latency.", "groups": results}, indent=2))
