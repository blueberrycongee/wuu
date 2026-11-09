"""Behavioral coverage for scoring, missing evidence, and comparable baselines."""

import copy
import importlib.util
import json
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("layout_score", ROOT / "scripts" / "score.py")
score = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(score)


class ScoringTests(unittest.TestCase):
    def setUp(self):
        self.profile = json.loads((ROOT / "examples" / "profile.json").read_text())
        self.run = json.loads((ROOT / "examples" / "measurements.json").read_text())

    def repeated(self, error, uncertainty=0):
        result = copy.deepcopy(self.run)
        first = result["samples"][0]
        for metric in first["metrics"].values():
            metric.update(error=error, uncertainty=uncertainty)
        result["samples"] = [copy.deepcopy(first) for _ in range(3)]
        for i, sample in enumerate(result["samples"]):
            sample["capture_id"] = f"independent-fixture-{i}"
        return result

    def test_scores_are_bounded_and_monotonic(self):
        values = [score.score_run(self.profile, self.repeated(e))["summary"]["total"]["score"]
                  for e in (0, 0.01, 0.05, 0.1, 0.2, 1)]
        self.assertEqual(values, sorted(values, reverse=True))
        self.assertEqual(values[0], 100)
        self.assertEqual(values[-1], 0)

    def test_missing_metric_does_not_become_full_score(self):
        run = self.repeated(0)
        run["samples"][1]["metrics"]["effects_error"] = None
        result = score.score_run(self.profile, run)["summary"]
        self.assertIsNone(result["total"])
        self.assertIsNone(result["dimensions"]["effects"])
        self.assertEqual(result["dimensions"]["alignment"]["score"], 100)
        self.assertAlmostEqual(result["coverage"], 0.9)
        self.assertIn("effects_error", result["missing"])

    def test_metric_weight_affects_coverage_without_renormalizing(self):
        rules = self.profile["dimensions"][0]["metrics"]
        extra = dict(rules[0], id="another_anchor", weight=3)
        rules.append(extra)
        result = score.score_run(self.profile, self.repeated(0))["summary"]
        self.assertAlmostEqual(result["coverage"], 1 - 0.25 * 0.75)
        self.assertIsNone(result["total"])

    def test_more_uncertainty_widens_range_without_changing_estimate(self):
        narrow = score.score_run(self.profile, self.repeated(0.05, 0.001))["summary"]["total"]
        wide = score.score_run(self.profile, self.repeated(0.05, 0.03))["summary"]["total"]
        self.assertEqual(narrow["score"], wide["score"])
        self.assertLess(wide["range"][0], narrow["range"][0])
        self.assertGreater(wide["range"][1], narrow["range"][1])

    def test_repeat_variation_appears_in_report_range(self):
        run = self.repeated(0.05)
        run["samples"][0]["metrics"]["alignment_error"]["error"] = 0.1
        result = score.score_run(self.profile, run)["summary"]["total"]
        self.assertLess(result["range"][0], result["score"])
        self.assertGreater(result["range"][1], result["score"])

    def test_constraints_cannot_be_cancelled_by_high_scores(self):
        run = self.repeated(0)
        run["samples"][0]["guards"]["operable"] = "fail"
        result = score.score_run(self.profile, run)["summary"]
        self.assertEqual(result["total"]["score"], 100)
        self.assertEqual(result["constraints"]["status"], "fail")
        self.assertIn("readable", result["constraints"]["unknown"])

    def test_improvement_and_noise_are_distinguished(self):
        baseline = score.score_run(self.profile, self.repeated(0.1, 0.001))
        better = score.score_run(self.profile, self.repeated(0.05, 0.001))
        self.assertEqual(score.compare(better, baseline)["trend"], "higher")
        self.assertEqual(score.compare(baseline, better)["trend"], "lower")
        noisy = score.score_run(self.profile, self.repeated(0.05, 0.1))
        self.assertEqual(score.compare(noisy, baseline)["trend"], "inconclusive")

    def test_changed_rules_or_environment_prevent_comparison(self):
        baseline = score.score_run(self.profile, self.repeated(0.1))
        for change in ("profile", "environment", "extractor", "scene"):
            with self.subTest(change=change):
                profile, run = copy.deepcopy(self.profile), self.repeated(0.05)
                if change == "profile":
                    profile["dimensions"][0]["metrics"][0]["good"] = 0.02
                elif change == "environment":
                    run["environment"]["scale"] = 2
                elif change == "extractor":
                    run["extractor_version"] = "different"
                else:
                    run["scene_version"] = "different"
                result = score.compare(score.score_run(profile, run), baseline)
                self.assertFalse(result["comparable"])
                self.assertIsNone(result["delta"])

    def test_build_changes_are_allowed_and_tampered_summary_is_ignored(self):
        baseline = score.score_run(self.profile, self.repeated(0.1))
        baseline["summary"]["total"]["score"] = 999
        run = self.repeated(0.05)
        run["build_id"] = "new-build"
        comparison = score.compare(score.score_run(self.profile, run), baseline)
        self.assertTrue(comparison["comparable"])
        self.assertGreater(comparison["delta"], 0)

    def test_insufficient_or_incomplete_captures_reject_trend(self):
        baseline = score.score_run(self.profile, self.repeated(0.1))
        once = score.score_run(self.profile, self.run)
        self.assertIn("insufficient_repeats", score.compare(once, baseline)["reasons"])
        run = self.repeated(0.05)
        del run["samples"][1]["metrics"]["weight_error"]
        incomplete = score.score_run(self.profile, run)
        self.assertIn("incomplete_coverage", score.compare(incomplete, baseline)["reasons"])

    def test_invalid_measurement_values_are_rejected(self):
        for value in (-1, float("nan"), float("inf"), True, "0.1"):
            with self.subTest(value=value):
                run = copy.deepcopy(self.run)
                run["samples"][0]["metrics"]["alignment_error"]["error"] = value
                with self.assertRaises(ValueError):
                    score.score_run(self.profile, run)

    def test_missing_uncertainty_is_not_assumed_zero(self):
        del self.run["samples"][0]["metrics"]["alignment_error"]["uncertainty"]
        with self.assertRaises(ValueError):
            score.score_run(self.profile, self.run)

    def test_unknown_ids_duplicate_captures_and_bad_thresholds_fail(self):
        run = self.repeated(0.05)
        run["samples"][1]["capture_id"] = run["samples"][0]["capture_id"]
        with self.assertRaises(ValueError):
            score.score_run(self.profile, run)
        self.run["samples"][0]["metrics"]["typo"] = None
        with self.assertRaises(ValueError):
            score.score_run(self.profile, self.run)
        rule = self.profile["dimensions"][0]["metrics"][0]
        rule["bad"] = rule["good"]
        with self.assertRaises(ValueError):
            score.validate_profile(self.profile)

    def test_cli_emits_complete_machine_readable_report(self):
        process = subprocess.run([sys.executable, str(ROOT / "scripts" / "score.py"),
                                  "--profile", str(ROOT / "examples" / "profile.json"),
                                  "--input", str(ROOT / "examples" / "measurements.json")],
                                 check=True, capture_output=True, text=True)
        report = json.loads(process.stdout)
        self.assertEqual(report["input"], self.run)
        self.assertEqual(report["summary"]["stability"], "provisional")
        self.assertEqual(report["summary"]["constraints"]["status"], "unknown")
        self.assertEqual(process.stderr, "")


if __name__ == "__main__":
    unittest.main()
