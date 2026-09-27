#!/usr/bin/env python3
"""Score extracted layout errors. Does not capture or interpret screenshots."""

import argparse
import hashlib
import json
import math
import statistics
import sys
from pathlib import Path


def require(condition, message):
    if not condition:
        raise ValueError(message)


def number(value, label, minimum=0):
    require(type(value) in (int, float) and math.isfinite(value),
            f"{label} must be a finite number")
    require(value >= minimum, f"{label} must be >= {minimum}")
    return value


def text(value, label):
    require(isinstance(value, str) and bool(value.strip()), f"{label} must be nonempty text")


def fingerprint(value):
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False)
    return hashlib.sha256(encoded.encode()).hexdigest()


def validate_profile(profile):
    require(isinstance(profile, dict), "profile must be an object")
    text(profile.get("version"), "profile.version")
    dimensions = profile.get("dimensions")
    require(isinstance(dimensions, list) and bool(dimensions), "dimensions must be nonempty")
    dimension_ids, metric_ids = set(), set()
    for dimension in dimensions:
        require(isinstance(dimension, dict), "dimension must be an object")
        name = dimension.get("id")
        text(name, "dimension.id")
        require(name not in dimension_ids, f"duplicate dimension: {name}")
        dimension_ids.add(name)
        require(number(dimension.get("weight"), name + ".weight") > 0, "weight must be positive")
        metrics = dimension.get("metrics")
        require(isinstance(metrics, list) and bool(metrics), f"{name}.metrics must be nonempty")
        for metric in metrics:
            require(isinstance(metric, dict), "metric must be an object")
            key = metric.get("id")
            text(key, "metric.id")
            require(key not in metric_ids, f"duplicate metric: {key}")
            metric_ids.add(key)
            require(number(metric.get("weight"), key + ".weight") > 0, "weight must be positive")
            good = number(metric.get("good"), key + ".good")
            bad = number(metric.get("bad"), key + ".bad")
            require(bad > good, f"{key}.bad must exceed good")
    guards = profile.get("guards")
    require(isinstance(guards, list) and bool(guards), "guards must be nonempty")
    for guard in guards:
        text(guard, "guard")
    require(len(set(guards)) == len(guards), "duplicate guards")
    return dimensions, metric_ids, guards


def metric_score(error, rule):
    penalty = (error - rule["good"]) / (rule["bad"] - rule["good"])
    return 100 * (1 - min(1, max(0, penalty)))


def aggregate(values):
    if any(v is None for v in values):
        return None
    return {"score": statistics.mean(v["score"] for v in values),
            "range": [min(v["range"][0] for v in values),
                      max(v["range"][1] for v in values)]}


def weighted(values):
    if any(v is None for _, v in values):
        return None
    total = sum(w for w, _ in values)
    return {"score": sum(w * v["score"] for w, v in values) / total,
            "range": [sum(w * v["range"][i] for w, v in values) / total for i in (0, 1)]}


def score_run(profile, run):
    dimensions, metric_ids, guard_ids = validate_profile(profile)
    require(isinstance(run, dict), "input must be an object")
    for field in ("scene", "scene_version", "extractor_version"):
        text(run.get(field), field)
    environment = run.get("environment")
    require(isinstance(environment, dict) and bool(environment), "environment must be nonempty")
    # Applications may add further fields; all fields participate in comparison.
    for key in ("viewport", "theme", "ui_font", "code_font", "scale", "platform",
                "renderer", "locale", "state", "content_version"):
        require(key in environment and environment[key] is not None, f"environment.{key} is required")
    fingerprint(environment)
    samples = run.get("samples")
    require(isinstance(samples, list) and bool(samples), "samples must be nonempty")
    captures, results = set(), []
    dimension_weight = sum(d["weight"] for d in dimensions)
    for sample in samples:
        require(isinstance(sample, dict), "sample must be an object")
        capture = sample.get("capture_id")
        text(capture, "capture_id")
        require(capture not in captures, "capture_id must be distinct for independent captures")
        captures.add(capture)
        observed, guards = sample.get("metrics"), sample.get("guards")
        require(isinstance(observed, dict), "sample.metrics must be an object")
        require(isinstance(guards, dict), "sample.guards must be an object")
        require(not set(observed) - metric_ids, "unknown metric ID")
        require(not set(guards) - set(guard_ids), "unknown guard ID")
        for value in guards.values():
            require(value in ("pass", "fail", "unknown"), "invalid guard status")
        coverage, missing, subresults, metric_results = 0, [], {}, {}
        for dimension in dimensions:
            items = []
            metric_weight = sum(m["weight"] for m in dimension["metrics"])
            for rule in dimension["metrics"]:
                key = rule["id"]
                value = observed.get(key)
                result = None
                if value is None:
                    missing.append(key)
                else:
                    require(isinstance(value, dict), f"{key} must be an object or null")
                    error = number(value.get("error"), key + ".error")
                    uncertainty = number(value.get("uncertainty"), key + ".uncertainty")
                    text(value.get("evidence"), key + ".evidence")
                    result = {"score": metric_score(error, rule),
                              "range": [metric_score(error + uncertainty, rule),
                                        metric_score(max(0, error - uncertainty), rule)]}
                    coverage += (dimension["weight"] / dimension_weight
                                 * rule["weight"] / metric_weight)
                items.append((rule["weight"], result))
                metric_results[key] = result
            subresults[dimension["id"]] = weighted(items)
        total = weighted([(d["weight"], subresults[d["id"]]) for d in dimensions])
        results.append({"capture_id": capture, "total": total, "dimensions": subresults,
                        "metrics": metric_results, "coverage": min(1, coverage), "missing": missing,
                        "guards": {key: guards.get(key, "unknown") for key in guard_ids}})
    failed = sorted({key for s in results for key, v in s["guards"].items() if v == "fail"})
    unknown = sorted({key for s in results for key, v in s["guards"].items() if v == "unknown"})
    total = aggregate([s["total"] for s in results])
    summary = {
        "total": total,
        "dimensions": {d["id"]: aggregate([s["dimensions"][d["id"]] for s in results]) for d in dimensions},
        "coverage": min(s["coverage"] for s in results),
        "missing": sorted({key for s in results for key in s["missing"]}),
        "captures": len(samples), "stability": "repeated" if len(samples) >= 3 else "provisional",
        "constraints": {"status": "fail" if failed else "unknown" if unknown else "pass",
                        "failed": failed, "unknown": unknown},
    }
    return {"schema_version": 1, "profile_sha256": fingerprint(profile), "profile": profile,
            "input": run, "summary": summary, "sample_scores": results}


def compare(current, baseline):
    # Recompute from original evidence rather than trusting an edited summary.
    baseline = score_run(baseline["profile"], baseline["input"])
    reasons = []
    if current["profile_sha256"] != baseline["profile_sha256"]:
        reasons.append("profile_changed")
    for key in ("scene", "scene_version", "extractor_version", "environment"):
        if current["input"][key] != baseline["input"][key]:
            reasons.append(key + "_changed")
    a, b = current["summary"], baseline["summary"]
    if a["total"] is None or b["total"] is None:
        reasons.append("incomplete_coverage")
    if min(a["captures"], b["captures"]) < 3:
        reasons.append("insufficient_repeats")
    if reasons:
        return {"comparable": False, "reasons": reasons, "delta": None}
    lo = a["total"]["range"][0] - b["total"]["range"][1]
    hi = a["total"]["range"][1] - b["total"]["range"][0]
    return {"comparable": True, "delta": a["total"]["score"] - b["total"]["score"],
            "delta_range": [lo, hi],
            "trend": "higher" if lo > 0 else "lower" if hi < 0 else "inconclusive",
            "constraints": {"before": b["constraints"], "after": a["constraints"]},
            "visual_acceptance": "not_determined"}


def load(path):
    def reject_constant(value):
        raise ValueError(f"nonfinite JSON value: {value}")
    return json.loads(Path(path).read_text(encoding="utf-8"), parse_constant=reject_constant)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", required=True)
    parser.add_argument("--input", required=True)
    parser.add_argument("--baseline", help="Previously generated score report, including profile and input")
    args = parser.parse_args()
    try:
        report = score_run(load(args.profile), load(args.input))
        if args.baseline:
            report["comparison"] = compare(report, load(args.baseline))
        print(json.dumps(report, ensure_ascii=False, indent=2, allow_nan=False))
    except (ValueError, KeyError, TypeError, OSError) as error:
        print(f"score: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
