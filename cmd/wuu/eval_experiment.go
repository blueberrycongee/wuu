package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/evalharness"
	"github.com/blueberrycongee/wuu/internal/modelprofile"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers/codex"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// A plan fixes the denominator and conditions before either side is admitted.
// Executable identity is recorded per attempt: host changes may be compared,
// whereas changing task setup or grading requires a new plan and baseline.
type evalExperimentPlan struct {
	Version        int               `json:"version"`
	Tasks          []string          `json:"tasks"`
	CatalogVersion string            `json:"catalog_version"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model"`
	Effort         string            `json:"effort"`
	Variant        string            `json:"variant"`
	LiveCodex      bool              `json:"live_codex_oauth"`
	MaxSteps       int               `json:"max_steps"`
	Timeout        time.Duration     `json:"timeout_ns"`
	ConfigHash     string            `json:"config_sha256"`
	Packages       map[string]string `json:"parent_packages"`
	Environment    map[string]string `json:"environment"`
	Concurrency    int               `json:"concurrency"`
	Acceptance     string            `json:"acceptance"`
}

const evalAcceptance = "all tasks pass on both sides; fewer model turns or total tokens; duration at most 1.25 times baseline"

// Freeze command-side grading and selection as well as task graders. This is
// deliberately conservative: changes elsewhere in these files also rebaseline.
//
//go:embed eval.go eval_experiment.go
var evalScoringSource embed.FS

func evalScoringVersion() string {
	hash := sha256.New()
	fmt.Fprint(hash, evalharness.CatalogVersion())
	for _, name := range []string{"eval.go", "eval_experiment.go"} {
		source, _ := evalScoringSource.ReadFile(name)
		fmt.Fprintf(hash, "%s\x00%d\x00", name, len(source))
		hash.Write(source)
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

type evalExperimentIdentity struct {
	PlanHash       string            `json:"plan_sha256"`
	ExecutableHash string            `json:"executable_sha256"`
	Build          *debug.BuildInfo  `json:"build"`
	Candidate      string            `json:"candidate,omitempty"`
	Packages       map[string]string `json:"loaded_packages"`
	Cost           string            `json:"cost"`
}

type evalExperimentOptions struct {
	Freeze, Plan, Candidate, Artifacts, Workdir, Provider, Model, Tasks string
	MaxSteps                                                            int
	Timeout                                                             time.Duration
	LiveCodex, KeepWorkdirs                                             bool
}

func evalDigest(data []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(data)) }

func evalEnvironment() map[string]string {
	env := map[string]string{"os": goruntime.GOOS, "arch": goruntime.GOARCH, "host_go": goruntime.Version()}
	for _, name := range []string{"go", "node", "git"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		arg := "--version"
		if name == "go" {
			arg = "version"
		}
		output, err := exec.CommandContext(ctx, name, arg).CombinedOutput()
		cancel()
		if err != nil {
			env[name] = "unavailable: " + err.Error()
		} else {
			env[name] = strings.TrimSpace(string(output))
		}
	}
	return env
}

func evalPackageIdentities(catalog []pluginpkg.Plugin) map[string]string {
	result := make(map[string]string, len(catalog))
	for _, item := range catalog {
		result[item.ID] = item.Fingerprint
	}
	return result
}

func runEvalExperiment(opts evalExperimentOptions) (err error) {
	if opts.Freeze != "" && (opts.Plan != "" || opts.Candidate != "" || opts.Artifacts != "") {
		return errors.New("--freeze cannot be combined with --plan, --candidate or --artifacts")
	}
	var plan evalExperimentPlan
	var planBytes []byte
	var tasks []evalharness.Task
	if opts.Plan != "" {
		planBytes, err = os.ReadFile(opts.Plan)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(planBytes, &plan); err != nil {
			return err
		}
		if plan.Version != 1 || plan.Concurrency != 1 || plan.MaxSteps <= 0 || plan.Timeout <= 0 || plan.Acceptance != evalAcceptance || len(plan.Tasks) == 0 {
			return errors.New("unsupported or incomplete evaluation plan")
		}
		seen := map[string]bool{}
		for _, id := range plan.Tasks {
			task, ok := evalharness.ByID(id)
			if !ok || seen[id] {
				return fmt.Errorf("unknown or duplicate frozen task %q", id)
			}
			seen[id] = true
			tasks = append(tasks, task)
		}
		if opts.Artifacts == "" {
			return errors.New("--plan requires a new --artifacts directory")
		}
		opts.Artifacts, err = filepath.Abs(opts.Artifacts)
		if err != nil {
			return err
		}
		if err := os.Mkdir(opts.Artifacts, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(opts.Artifacts, "plan.json"), planBytes, 0o600); err != nil {
			return err
		}
	}

	started := time.Now()
	report := evalReport{StartedAt: started, Provider: plan.Provider, Model: plan.Model}
	if opts.Plan != "" {
		build, _ := debug.ReadBuildInfo()
		report.Experiment = &evalExperimentIdentity{PlanHash: evalDigest(planBytes), Build: build, Candidate: opts.Candidate, Cost: "unavailable: subscription provider does not report monetary cost"}
		executable, executableErr := os.Executable()
		if executableErr != nil {
			return executableErr
		}
		file, openErr := os.Open(executable)
		if openErr != nil {
			return openErr
		}
		hash := sha256.New()
		_, hashErr := io.Copy(hash, file)
		closeErr := file.Close()
		if err := errors.Join(hashErr, closeErr); err != nil {
			return err
		}
		report.Experiment.ExecutableHash = fmt.Sprintf("sha256:%x", hash.Sum(nil))
		// Even pre-admission failures keep the fixed task denominator. A partial
		// run fills only its unattempted tail; completed failures are not erased.
		defer func() {
			for len(report.Results) < len(tasks) {
				task := tasks[len(report.Results)]
				reason := "experiment did not admit this task"
				if err != nil {
					reason = err.Error()
				}
				report.Results = append(report.Results, evalharness.Result{TaskID: task.ID, TaskName: task.Name, Error: reason})
			}
			report.Summary = summarizeEvalResults(report.Results)
			report.DurationMS = time.Since(started).Milliseconds()
			err = errors.Join(err, writeEvalEvidence(filepath.Join(opts.Artifacts, "report.json"), report))
			printEvalReport(report)
			if report.Summary.Failed > 0 {
				err = errors.Join(err, fmt.Errorf("experiment failed: %d/%d passed", report.Summary.Passed, report.Summary.Total))
			}
		}()
		if plan.CatalogVersion != evalScoringVersion() {
			return errors.New("frozen catalog/scorer differs from this executable; freeze a new plan and rebaseline")
		}
		if !reflect.DeepEqual(plan.Environment, evalEnvironment()) {
			return errors.New("frozen tool environment differs")
		}
	}

	binding, err := openCommandState(context.Background(), opts.Workdir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, binding.Close()) }()
	services, err := binding.Services()
	if err != nil {
		return err
	}
	selected, err := services.Config.Read(context.Background())
	if err != nil {
		return err
	}
	cfg := selected.Config
	provider, model, live := opts.Provider, opts.Model, opts.LiveCodex
	if opts.Plan != "" {
		provider, model, live = plan.Provider, plan.Model, plan.LiveCodex
	}
	if live {
		provider = "openai-codex"
		pc, exists := cfg.Providers[provider]
		if !exists {
			pc = config.Default().Providers[provider]
		}
		pc.ReuseCodexCredentials = true
		if cfg.Providers == nil {
			cfg.Providers = map[string]config.ProviderConfig{}
		}
		cfg.Providers[provider] = pc
	}
	pc, provider, err := cfg.ResolveProvider(provider)
	if err != nil {
		return err
	}
	if model != "" {
		pc.Model = model
	}
	cfg.DefaultProvider = provider
	cfg.Providers[provider] = pc
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	catalog := binding.Generation.ActivePackages()
	if opts.Freeze != "" {
		if opts.MaxSteps <= 0 || opts.Timeout <= 0 {
			return errors.New("freeze requires positive --max-steps and --timeout")
		}
		surface := modelprofile.DefaultCompiler{}.Compile(modelprofile.Resolve(provider, pc.Model), modelprofile.SurfaceMain)
		tasks, err = resolveEvalTasks(opts.Tasks, evalVisibleToolSet(surface.ToolNames()))
		if err != nil {
			return err
		}
		plan = evalExperimentPlan{Version: 1, CatalogVersion: evalScoringVersion(), Provider: provider, Model: pc.Model,
			Effort: cfg.Agent.Effort, Variant: cfg.Agent.Variant, LiveCodex: live, MaxSteps: opts.MaxSteps, Timeout: opts.Timeout,
			ConfigHash: evalDigest(configBytes), Packages: evalPackageIdentities(catalog), Environment: evalEnvironment(), Concurrency: 1, Acceptance: evalAcceptance}
		seen := map[string]bool{}
		for _, task := range tasks {
			if seen[task.ID] {
				return fmt.Errorf("duplicate task %q", task.ID)
			}
			seen[task.ID] = true
			plan.Tasks = append(plan.Tasks, task.ID)
		}
		if err := writeEvalEvidence(opts.Freeze, plan); err != nil {
			return err
		}
		fmt.Printf("Frozen %d tasks in %s; no model calls made.\n", len(tasks), opts.Freeze)
		return nil
	}
	if evalDigest(configBytes) != plan.ConfigHash || !reflect.DeepEqual(plan.Packages, evalPackageIdentities(catalog)) {
		return errors.New("frozen parent configuration or packages differ; rebaseline explicitly")
	}
	if live {
		if _, err := codex.OAuthStatus(context.Background(), services.Credentials, true); err != nil {
			return fmt.Errorf("experiment authentication: %w", err)
		}
	}

	root, err := resolveWorkdir(opts.Workdir)
	if err != nil {
		return err
	}
	// Parent packages already belong to the retained generation. Preserve their
	// exact trees without reinstalling them: imported packages can have a nested
	// manifest alongside the normalized host manifest and retain their provenance.
	for _, item := range catalog {
		if err := os.CopyFS(filepath.Join(opts.Artifacts, "parent-packages", item.ID), os.DirFS(item.Root)); err != nil {
			return err
		}
	}
	paths := []string{}
	if opts.Candidate != "" {
		paths = append(paths, opts.Candidate)
	}
	// Candidate packages are durable evidence. Mutable runtime state is separate
	// and disposable; source and SDK snapshots survive normal candidate teardown.
	packageHome := filepath.Join(opts.Artifacts, "packages")
	cfg, catalog, err = runtime.PrepareCompositionSources(context.Background(), cfg, catalog, packageHome, root, paths)
	if err != nil {
		return err
	}
	report.Experiment.Packages = evalPackageIdentities(catalog)
	for _, task := range tasks {
		evidence := filepath.Join(opts.Artifacts, task.ID)
		if err := os.Mkdir(evidence, 0o700); err != nil {
			return err
		}
		state := filepath.Join(evidence, "runtime-state")
		if err := os.Mkdir(state, 0o700); err != nil {
			return err
		}
		result := runEvalTask(evalTaskRunConfig{Task: task, Config: cfg, ProviderName: plan.Provider, ModelOverride: plan.Model,
			MaxSteps: plan.MaxSteps, Timeout: plan.Timeout, HomeDir: os.Getenv("HOME"), WuuHome: state,
			Catalog: &catalog, EvidenceDir: evidence, ExpectedPackages: evalPackageIdentities(catalog), KeepWorkdir: opts.KeepWorkdirs})
		// Copy only this task's journal, never credentials or the user's profile.
		if result.Observability != nil && result.Observability.SessionDir != "" {
			if copyErr := os.CopyFS(filepath.Join(evidence, "session"), os.DirFS(result.Observability.SessionDir)); copyErr != nil {
				result.Success = false
				result.RetainedStateDir = state
				result.Error = errors.Join(errors.New(result.Error), fmt.Errorf("retain session evidence (state %s): %w", state, copyErr)).Error()
				err = copyErr
			}
		}
		if saveErr := writeEvalEvidence(filepath.Join(evidence, "result.json"), result); saveErr != nil {
			result.Success = false
			result.RetainedStateDir = state
			err = errors.Join(err, saveErr)
		}
		report.Results = append(report.Results, result)
		if err != nil {
			return fmt.Errorf("evidence failed; state retained at %s: %w", state, err)
		}
		if result.RetainedStateDir == "" {
			if err := os.RemoveAll(state); err != nil {
				return err
			}
		}
	}
	return nil
}

// Exclusive creation prevents an accidental rerun from overwriting evidence.
func writeEvalEvidence(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	return errors.Join(writeErr, file.Close())
}

func saveEvalRuntimeEvidence(directory string, rt *runtime.Session, expected map[string]string) error {
	actual := evalPackageIdentities(rt.ActivePlugins)
	if err := writeEvalEvidence(filepath.Join(directory, "packages.json"), actual); err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, actual) {
		return fmt.Errorf("loaded packages differ from prepared composition: expected=%v actual=%v", expected, actual)
	}
	snapshot, err := rt.PluginServiceRegistrySnapshot()
	if err != nil {
		return err
	}
	if err := writeEvalEvidence(filepath.Join(directory, "bindings.json"), snapshot); err != nil {
		return err
	}
	if err := writeEvalEvidence(filepath.Join(directory, "modules.json"), rt.PluginHost.Statuses()); err != nil {
		return err
	}
	for _, status := range rt.PluginHost.Statuses() {
		if status.State != "active" {
			return fmt.Errorf("module %s is %s: %s", status.ID, status.State, status.Error)
		}
	}
	if len(rt.ReadinessIssues) != 0 {
		return fmt.Errorf("runtime not ready: %v", rt.ReadinessIssues)
	}
	return nil
}

func compareEvalExperiments(input string) error {
	paths := strings.Split(input, ",")
	if len(paths) != 2 {
		return errors.New("--compare requires baseline/report.json,candidate/report.json")
	}
	var reports [2]evalReport
	for i, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &reports[i]); err != nil {
			return err
		}
		if reports[i].Experiment == nil {
			return errors.New("comparison requires frozen experiment reports")
		}
	}
	baseline, candidate := reports[0], reports[1]
	if baseline.Experiment.PlanHash != candidate.Experiment.PlanHash || len(baseline.Results) == 0 || len(baseline.Results) != len(candidate.Results) {
		return errors.New("comparison conditions or denominator differ")
	}
	var turns, tokens [2]int
	for i, report := range reports {
		for j, result := range report.Results {
			if result.TaskID != baseline.Results[j].TaskID {
				return errors.New("comparison task order differs")
			}
			turns[i] += result.Turns
			tokens[i] += result.InputTokens + result.OutputTokens + result.CacheReadTokens + result.CacheCreationTokens
		}
	}
	bs, cs := summarizeEvalResults(baseline.Results), summarizeEvalResults(candidate.Results)
	accepted := bs.Failed == 0 && cs.Failed == 0 && (turns[1] < turns[0] || tokens[1] < tokens[0]) && float64(candidate.DurationMS) <= 1.25*float64(baseline.DurationMS)
	decision := "reject"
	if accepted {
		decision = "accept"
	}
	data, err := json.MarshalIndent(map[string]any{"decision": decision, "plan_sha256": baseline.Experiment.PlanHash,
		"baseline": bs, "candidate": cs, "model_turns": turns, "total_tokens": tokens,
		"duration_ms": [2]int64{baseline.DurationMS, candidate.DurationMS}, "rule": evalAcceptance,
		"interpretation": "one pair demonstrates selection, not general capability improvement"}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
