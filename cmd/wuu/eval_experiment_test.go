package main

import (
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/evalharness"
	"os"
	"path/filepath"
	"testing"
)

// The comparison controller must reject edited conditions and missing/duplicate
// results, rather than trusting candidate-supplied summary or a claimed hash.
func TestFrozenExperimentComparisonEvidence(t *testing.T) {
	root := t.TempDir()
	plan := evalExperimentPlan{Version: 1, Tasks: []string{"one"}, Acceptance: evalAcceptance}
	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for _, side := range []string{"baseline", "candidate"} {
		dir := filepath.Join(root, side)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plan.json"), planBytes, 0600); err != nil {
			t.Fatal(err)
		}
		report := evalReport{Experiment: &evalExperimentIdentity{PlanHash: evalDigest(planBytes)}, Results: []evalharness.Result{{TaskID: "one", Success: true, Turns: 2}}}
		path := filepath.Join(dir, "report.json")
		paths = append(paths, path)
		if err := writeEvalEvidence(path, report); err != nil {
			t.Fatal(err)
		}
	}
	pair := paths[0] + "," + paths[1]
	if _, _, err := readEvalComparison(pair); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "candidate", "plan.json"), []byte(`{"tasks":["different"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readEvalComparison(pair); err == nil {
		t.Fatal("edited conditions accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "candidate", "plan.json"), planBytes, 0600); err != nil {
		t.Fatal(err)
	}
	for _, results := range [][]evalharness.Result{nil, {{TaskID: "other"}}, {{TaskID: "one", InputTokens: -1}}, {{TaskID: "one"}, {TaskID: "one"}}} {
		report := evalReport{Experiment: &evalExperimentIdentity{PlanHash: evalDigest(planBytes)}, Results: results}
		data, _ := json.Marshal(report)
		if err := os.WriteFile(paths[1], data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readEvalComparison(pair); err == nil {
			t.Fatalf("invalid denominator/metrics accepted: %#v", results)
		}
	}
}

func TestExperimentPolicyValidation(t *testing.T) {
	for _, bad := range []evalExperimentPolicy{
		{Concurrency: 0},
		{Concurrency: 1, Acceptance: evalAcceptancePolicy{MaxDurationRatio: -1}},
		{Concurrency: 1, Acceptance: evalAcceptancePolicy{MaxDurationRatio: 1, Improvement: "invented"}},
	} {
		if err := bad.validate(); err == nil {
			t.Fatalf("accepted invalid policy: %#v", bad)
		}
	}
	if err := defaultEvalExperimentPolicy().validate(); err != nil {
		t.Fatal(err)
	}
}
