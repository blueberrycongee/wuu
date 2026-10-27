package main

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/evalharness"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExperimentControllerIdentityAndProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess contract")
	}
	root := t.TempDir()
	path := filepath.Join(root, "controller.sh")
	source := []byte("#!/bin/sh\nprintf '%s' '{\"tasks\":[{\"id\":\"external\",\"name\":\"External\",\"prompt\":\"work\"}]}'\n")
	if err := os.WriteFile(path, source, 0700); err != nil {
		t.Fatal(err)
	}
	c := evalExperimentController{Command: []string{path}, Files: map[string]string{path: evalDigest(source)}}
	var response evalControllerTasks
	if err := c.call(context.Background(), "tasks", nil, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Tasks) != 1 || response.Tasks[0].ID != "external" {
		t.Fatalf("bad response: %#v", response)
	}
	if err := os.WriteFile(path, append(source, []byte("# changed")...), 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.call(context.Background(), "tasks", nil, &response); err == nil {
		t.Fatal("modified controller admitted")
	}
}

func TestExperimentControllerRejectsUnsafeTaskIDs(t *testing.T) {
	for _, id := range []string{"../escape", "", "a/b", ".", ".."} {
		if err := validateEvalControllerTasks([]evalControllerTask{{ID: id, Prompt: "work"}}); err == nil {
			t.Fatalf("accepted task %q", id)
		}
	}
}

// Candidate reports are observations, never the authority for external grading.
func TestIndependentControllerRejectsFabricatedReport(t *testing.T) {
	root := t.TempDir()
	plan := evalExperimentPlan{Tasks: []string{"one"}, ControllerTasks: []evalControllerTask{{ID: "one", Prompt: "work"}}, Controller: &evalExperimentController{}}
	report := evalReport{Results: []evalharness.Result{{TaskID: "one", Success: true}}}
	if _, err := rescoreEvalReport(filepath.Join(root, "report.json"), plan, report); err == nil {
		t.Fatal("candidate success without task evidence accepted")
	}
	if runtime.GOOS == "windows" {
		return
	}
	script := filepath.Join(root, "grader.sh")
	source := []byte("#!/bin/sh\nprintf '%s' '{\"passed\":false,\"reason\":\"independent rejection\"}'\n")
	if err := os.WriteFile(script, source, 0700); err != nil {
		t.Fatal(err)
	}
	plan.Controller = &evalExperimentController{Command: []string{script}, Files: map[string]string{script: evalDigest(source)}}
	task := filepath.Join(root, "one")
	if err := os.Mkdir(task, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"runtime-locations.json": map[string]string{"workdir": root}, "execution.json": map[string]any{"Content": "claimed success", "InputTokens": 17}, "result.json": report.Results[0]} {
		if err := writeEvalEvidence(filepath.Join(task, name), value); err != nil {
			t.Fatal(err)
		}
	}
	rescored, err := rescoreEvalReport(filepath.Join(root, "report.json"), plan, report)
	if err != nil {
		t.Fatal(err)
	}
	if rescored.Results[0].Success || rescored.Results[0].InputTokens != 17 {
		t.Fatalf("trusted candidate verdict/aggregate instead of independent controller: %#v", rescored.Results[0])
	}

}

func TestExperimentControllerBoundsProtocolOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess contract")
	}
	root := t.TempDir()
	path := filepath.Join(root, "overflow.sh")
	source := []byte("#!/bin/sh\nhead -c 9000000 /dev/zero\n")
	if err := os.WriteFile(path, source, 0700); err != nil {
		t.Fatal(err)
	}
	controller := evalExperimentController{Command: []string{path}, Files: map[string]string{path: evalDigest(source)}}
	var result any
	if err := controller.call(context.Background(), "tasks", nil, &result); err == nil || !strings.Contains(err.Error(), "output exceeds") {
		t.Fatalf("overflow not rejected explicitly: %v", err)
	}
}
