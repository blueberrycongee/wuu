package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestHostCandidateFailureRetainsCheckpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess contract")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "candidate")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho build-or-start-failed >&2\nexit 17\n"), 0700); err != nil {
		t.Fatal(err)
	}
	artifacts := filepath.Join(root, "attempt")
	opts := evalExperimentOptions{Plan: filepath.Join(root, "plan.json"), Artifacts: artifacts}
	data, _ := os.ReadFile(binary)
	plan := evalExperimentPlan{Version: 1, Tasks: []string{"one"}, Timeout: time.Second, Concurrency: 1, Controller: &evalExperimentController{Command: []string{binary}, Files: map[string]string{binary: evalDigest(data)}}}
	planData, _ := json.Marshal(plan)
	if err := os.WriteFile(opts.Plan, planData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runEvalHostCandidate(binary, opts); err == nil {
		t.Fatal("failed host admitted")
	}
	for _, name := range []string{"checkpoint.json", "process.json", "stderr.log"} {
		if _, err := os.Stat(artifacts + ".host/" + name); err != nil {
			t.Fatal(err)
		}
	}
	if err := runEvalHostCandidate(binary, opts); err == nil {
		t.Fatal("reused attempt evidence")
	}
}

func TestHostCandidateDurationComesFromSupervisor(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "candidate", "report.json")
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	identity := evalExperimentIdentity{PlanHash: "plan", ExecutableHash: "candidate-code"}
	report := evalReport{Experiment: &identity, DurationMS: 0}
	duration, supervised, err := readEvalHostDuration(path, report)
	if err != nil || supervised {
		t.Fatalf("unsupervised report: %v %v", supervised, err)
	}
	evidence := filepath.Dir(path) + ".host"
	if err := os.Mkdir(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeEvalEvidence(filepath.Join(evidence, "checkpoint.json"), map[string]any{"candidate_sha256": "candidate-code", "plan_sha256": "plan"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readEvalHostDuration(path, report); err == nil {
		t.Fatal("missing process evidence accepted")
	}
	if err := writeEvalEvidence(filepath.Join(evidence, "process.json"), map[string]any{"duration_ms": 2500, "exited_cleanly": true, "candidate_sha256": "candidate-code"}); err != nil {
		t.Fatal(err)
	}
	duration, supervised, err = readEvalHostDuration(path, report)
	if err != nil || !supervised || duration != 2500 {
		t.Fatalf("candidate forged duration trusted: %d %v %v", duration, supervised, err)
	}
	report.Experiment.ExecutableHash = "different"
	if _, _, err := readEvalHostDuration(path, report); err == nil {
		t.Fatal("mismatched host evidence accepted")
	}
}
