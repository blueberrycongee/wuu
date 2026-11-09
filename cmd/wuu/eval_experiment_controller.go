package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/evalharness"
)

// A controller runs outside candidate lifetimes. Every executable/script input
// is hash-bound in the plan. It is trusted evaluation code, not a candidate hook.
// Files list all transitive controller code/data inputs, excluding secrets.
type evalExperimentController struct {
	Command []string          `json:"command"`
	Files   map[string]string `json:"files"`
}

type evalControllerTask struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Prompt         string   `json:"prompt"`
	RequiredTools  []string `json:"required_tools,omitempty"`
	ForbiddenTools []string `json:"forbidden_tools,omitempty"`
}

type evalControllerTasks struct {
	Tasks []evalControllerTask `json:"tasks"`
}

func (c evalExperimentController) verify() error {
	if len(c.Command) == 0 || !filepath.IsAbs(c.Command[0]) || len(c.Files) == 0 {
		return errors.New("controller requires an absolute executable and pinned files")
	}
	if _, ok := c.Files[c.Command[0]]; !ok {
		return errors.New("controller executable must be pinned in files")
	}
	for path, hash := range c.Files {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("controller input must be absolute: %s", path)
		}
		actual, err := evalFileDigest(path)
		if err != nil {
			return err
		}
		if actual != hash {
			return fmt.Errorf("frozen controller input changed: %s; start a new experiment", path)
		}
	}
	return nil
}

func (c evalExperimentController) call(ctx context.Context, method string, params, result any) error {
	if err := c.verify(); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"version": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, c.Command[0], c.Command[1:]...)
	cleanup, err := prepareEvalProcess(command)
	if err != nil {
		return err
	}
	defer cleanup()
	command.Stdin = bytes.NewReader(data)
	output := evalControllerOutput{limit: 8 << 20, cancel: command.Cancel}
	diagnostic := evalControllerOutput{limit: 256 << 10, cancel: command.Cancel}
	command.Stdout = &output
	command.Stderr = &diagnostic
	runErr := command.Run()
	if output.overflow || diagnostic.overflow {
		return fmt.Errorf("experiment controller %s output exceeds protocol limits (stdout 8 MiB, stderr 256 KiB)", method)
	}
	if err := runErr; err != nil {
		return fmt.Errorf("experiment controller %s: %w: %s", method, err, diagnostic.buffer.String())
	}
	if err := c.verify(); err != nil {
		return err
	}
	if err := json.Unmarshal(output.buffer.Bytes(), result); err != nil {
		return fmt.Errorf("experiment controller %s response: %w", method, err)
	}
	return nil
}

func validateEvalControllerTasks(tasks []evalControllerTask) error {
	if len(tasks) == 0 {
		return errors.New("controller returned no tasks")
	}
	seen := map[string]bool{}
	valid := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
	for _, task := range tasks {
		if !valid.MatchString(task.ID) || seen[task.ID] || task.Prompt == "" {
			return fmt.Errorf("invalid or duplicate controller task %q", task.ID)
		}
		seen[task.ID] = true
	}
	return nil
}

func (c evalExperimentController) task(spec evalControllerTask) evalharness.Task {
	return evalharness.Task{ID: spec.ID, Name: spec.Name, Prompt: spec.Prompt, RequiredTools: spec.RequiredTools, ForbiddenTools: spec.ForbiddenTools,
		Setup: func(root string) error {
			var response struct {
				Error string `json:"error,omitempty"`
			}
			if err := c.call(context.Background(), "setup", map[string]any{"task": spec, "root": root}, &response); err != nil {
				return err
			}
			if response.Error != "" {
				return errors.New(response.Error)
			}
			return nil
		},
		Verify: func(ctx context.Context, root, answer string) (evalharness.Verification, error) {
			var response evalharness.Verification
			err := c.call(ctx, "score", map[string]any{"task": spec, "root": root, "answer": answer}, &response)
			return response, err
		},
	}
}

// Rescore in the retained controller, never accept the candidate's Success bit.
// Execution artifacts are observations, not proof of honest inference. Complete
// artifacts are required and the independent grader judges the retained workspace.
func rescoreEvalReport(path string, plan evalExperimentPlan, report evalReport) (evalReport, error) {
	if plan.Controller == nil {
		return report, nil
	}
	if err := validateEvalControllerTasks(plan.ControllerTasks); err != nil {
		return report, err
	}

	if len(report.Results) != len(plan.ControllerTasks) {
		return report, errors.New("controller task denominator differs")
	}
	for i, spec := range plan.ControllerTasks {
		directory := filepath.Join(filepath.Dir(path), spec.ID)
		locationsBytes, err := os.ReadFile(filepath.Join(directory, "runtime-locations.json"))
		if err != nil {
			return report, fmt.Errorf("independent grading requires retained task locations: %w", err)
		}
		var locations map[string]string
		if err := json.Unmarshal(locationsBytes, &locations); err != nil {
			return report, err
		}
		root := locations["workdir"]
		if !filepath.IsAbs(root) {
			return report, errors.New("independent grading requires an absolute retained task workspace")
		}
		info, err := os.Stat(root)
		if err != nil {
			return report, err
		}
		if !info.IsDir() {
			return report, errors.New("retained task workspace is not a directory")
		}
		executionBytes, err := os.ReadFile(filepath.Join(directory, "execution.json"))
		if err != nil {
			return report, fmt.Errorf("independent grading requires full execution evidence: %w", err)
		}
		var execution agent.LoopResult
		if err := json.Unmarshal(executionBytes, &execution); err != nil {
			return report, err
		}
		if execution.InputTokens < 0 || execution.OutputTokens < 0 || execution.CacheCreationTokens < 0 || execution.CacheReadTokens < 0 {
			return report, errors.New("negative usage in execution observations")
		}
		taskBytes, err := os.ReadFile(filepath.Join(directory, "result.json"))
		if err != nil {
			return report, err
		}
		var observed evalharness.Result
		if err := json.Unmarshal(taskBytes, &observed); err != nil {
			return report, err
		}
		if observed.TaskID != spec.ID || report.Results[i].TaskID != spec.ID {
			return report, errors.New("independent grading task identities differ")
		}
		var verification evalharness.Verification
		if err := plan.Controller.call(context.Background(), "score", map[string]any{"task": spec, "root": root, "answer": execution.Content}, &verification); err != nil {
			return report, err
		}
		result := &report.Results[i]
		// Recompute measurable usage from complete execution observations rather than
		// candidate-authored aggregate metrics; trusted provider receipts remain the
		// boundary for proving real inference rather than a synthetic fixture.
		result.InputTokens = execution.InputTokens
		result.OutputTokens = execution.OutputTokens
		result.CacheCreationTokens = execution.CacheCreationTokens
		result.CacheReadTokens = execution.CacheReadTokens
		result.Turns = countAssistantTurns(execution.NewMessages)
		result.MissingTools = missingRequiredTools(spec.RequiredTools, observed.ToolNames)
		result.ForbiddenToolsUsed = forbiddenToolsUsed(spec.ForbiddenTools, observed.ToolNames)
		result.Success = verification.Passed && observed.Success && observed.Error == "" && len(result.MissingTools) == 0 && len(result.ForbiddenToolsUsed) == 0
		result.Error = observed.Error
		result.VerificationReason = verification.Reason
		result.VerificationEvidence = verification.Evidence
	}
	report.Summary = summarizeEvalResults(report.Results)
	return report, nil
}

// A generated controller may accidentally stream forever. Bound each pipe and
// kill its owned process group immediately on overflow; never parse truncated JSON.
type evalControllerOutput struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
	cancel   func() error
}

func (w *evalControllerOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.buffer.Len() {
		w.overflow = true
		if w.cancel != nil {
			_ = w.cancel()
		}
		return 0, errors.New("experiment controller output exceeds limit")
	}
	return w.buffer.Write(p)
}

func evalFileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}
