package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Host experiments start at a fresh task boundary. This does not migrate an
// arbitrary running stack or retry an uncertain external operation. The original
// executable remains the controller and records exit evidence even on a crash.
func runEvalHostCandidate(binary string, opts evalExperimentOptions) (err error) {
	if opts.Plan == "" || opts.Artifacts == "" || opts.Freeze != "" {
		return errors.New("--candidate-host requires --plan and new --artifacts; cannot freeze")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return err
	}
	executable, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	plan, err := os.ReadFile(opts.Plan)
	if err != nil {
		return err
	}
	var frozen evalExperimentPlan
	if err := json.Unmarshal(plan, &frozen); err != nil {
		return err
	}
	if frozen.Version != 1 || len(frozen.Tasks) == 0 || frozen.Timeout <= 0 || frozen.Concurrency < 1 || frozen.Concurrency > 256 {
		return errors.New("unsupported or incomplete host experiment plan")
	}
	if frozen.Controller == nil {
		return errors.New("host candidates require an independent pinned --experiment-controller in the frozen plan")
	}
	if err := frozen.Controller.verify(); err != nil {
		return err
	}
	evidence := opts.Artifacts + ".host"
	if err := os.Mkdir(evidence, 0700); err != nil {
		return err
	}
	checkpoint := map[string]any{"boundary": "fresh evaluation tasks; no in-flight execution migrated", "candidate_executable": binary, "candidate_sha256": evalDigest(executable), "plan_sha256": evalDigest(plan), "previous_host": "unchanged; candidate is isolated", "external_effects": "not rolled back; uncertain tool effects must not be retried"}
	if err := writeEvalEvidence(filepath.Join(evidence, "checkpoint.json"), checkpoint); err != nil {
		return err
	}
	stdout, err := os.OpenFile(filepath.Join(evidence, "stdout.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(filepath.Join(evidence, "stderr.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer stderr.Close()
	snapshot := filepath.Join(evidence, "candidate-host")
	if err := os.WriteFile(snapshot, executable, 0700); err != nil {
		return err
	}
	args := []string{"eval", "--plan", opts.Plan, "--artifacts", opts.Artifacts}
	if opts.Workdir != "" {
		args = append(args, "--workdir", opts.Workdir)
	}
	if opts.Candidate != "" {
		args = append(args, "--candidate", opts.Candidate)
	}
	if opts.KeepWorkdirs {
		args = append(args, "--keep-workdirs")
	}
	// A finite experiment budget bounds startup and admitted tasks, including a
	// crashed/hung candidate that never produces its own evidence.
	timeout := time.Duration(len(frozen.Tasks))*frozen.Timeout + 2*time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, snapshot, args...)
	cleanup, err := prepareEvalProcess(command)
	if err != nil {
		return err
	}
	defer cleanup()
	command.Stdout = stdout
	command.Stderr = stderr
	started := time.Now()
	runErr := command.Run()
	durationMS := time.Since(started).Milliseconds()
	status := map[string]any{"duration_ms": durationMS, "exited_cleanly": runErr == nil, "candidate_sha256": evalDigest(executable)}
	if runErr != nil {
		status["error"] = runErr.Error()
	}
	saveErr := writeEvalEvidence(filepath.Join(evidence, "process.json"), status)
	if runErr != nil {
		return errors.Join(fmt.Errorf("candidate host failed; inspect %s: %w", evidence, runErr), saveErr)
	}
	if saveErr != nil {
		return saveErr
	}
	// A clean exit without report is not success. The retained controller checks
	// the worker identity as well as the complete frozen task denominator.
	reportBytes, err := os.ReadFile(filepath.Join(opts.Artifacts, "report.json"))
	if err != nil {
		return err
	}
	var report evalReport
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		return err
	}
	if report.Experiment == nil || report.Experiment.PlanHash != evalDigest(plan) || report.Experiment.ExecutableHash != evalDigest(executable) {
		return errors.New("candidate host report identity differs from checkpoint")
	}
	if len(report.Results) != len(frozen.Tasks) {
		return errors.New("candidate host omitted task results")
	}
	for i, task := range frozen.Tasks {
		if report.Results[i].TaskID != task {
			return errors.New("candidate host task identities differ from checkpoint")
		}
	}
	rescored, err := rescoreEvalReport(filepath.Join(opts.Artifacts, "report.json"), frozen, report)
	if err != nil {
		return err
	}
	rescored.DurationMS = durationMS
	if err := writeEvalEvidence(filepath.Join(evidence, "controller-verification.json"), rescored); err != nil {
		return err
	}
	if rescored.Summary.Failed > 0 {
		return fmt.Errorf("independent controller rejected %d task(s)", rescored.Summary.Failed)
	}
	return frozen.Controller.verify()
}

// Supervisor evidence is written by the original host after the candidate exits.
// Candidate aggregate timing is not authoritative for a rebuilt-host comparison.
func readEvalHostDuration(reportPath string, report evalReport) (int64, bool, error) {
	evidence := filepath.Dir(reportPath) + ".host"
	info, err := os.Stat(evidence)
	if errors.Is(err, os.ErrNotExist) {
		return report.DurationMS, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !info.IsDir() {
		return 0, false, errors.New("host supervisor evidence is not a directory")
	}
	var checkpoint struct {
		Candidate string `json:"candidate_sha256"`
		Plan      string `json:"plan_sha256"`
	}
	var process struct {
		Candidate  string `json:"candidate_sha256"`
		DurationMS int64  `json:"duration_ms"`
		Clean      bool   `json:"exited_cleanly"`
	}
	for name, target := range map[string]any{"checkpoint.json": &checkpoint, "process.json": &process} {
		data, err := os.ReadFile(filepath.Join(evidence, name))
		if err != nil {
			return 0, true, err
		}
		if err = json.Unmarshal(data, target); err != nil {
			return 0, true, err
		}
	}
	if report.Experiment == nil || checkpoint.Candidate == "" || checkpoint.Candidate != report.Experiment.ExecutableHash || process.Candidate != checkpoint.Candidate || checkpoint.Plan != report.Experiment.PlanHash {
		return 0, true, errors.New("host supervisor identity differs from retained report")
	}
	if !process.Clean || process.DurationMS < 0 {
		return 0, true, errors.New("host supervisor did not record a successful finite attempt")
	}
	return process.DurationMS, true, nil
}
