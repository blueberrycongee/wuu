package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/evalharness"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/version"
)

const (
	evalEnvironmentIsolated  = "isolated"
	evalEnvironmentInherited = "inherited"
)

// Keep inference choices, but do not import executable user context into a
// catalog task. Task.Configure runs afterward to declare its own dependencies.
func isolatedEvalConfig(source config.Config, providerName, modelOverride, home string) (config.Config, error) {
	provider, name, err := source.ResolveProvider(providerName)
	if err != nil {
		return config.Config{}, err
	}
	if modelOverride != "" {
		provider.Model = modelOverride
	}
	roles, err := modelroles.Resolve(source, modelroles.ResolveOptions{
		ProviderName: name, ProviderConfig: provider, Model: provider.Model,
		Effort: source.Agent.Effort, Variant: source.Agent.Variant,
	})
	if err != nil {
		return config.Config{}, err
	}
	selections := roles.List()
	aliasNames := make([]string, 0, len(source.Agent.ModelAliases))
	for name := range source.Agent.ModelAliases {
		aliasNames = append(aliasNames, name)
	}
	sort.Strings(aliasNames)
	for _, name := range aliasNames {
		selection, err := modelroles.ResolveAlias(source, name)
		if err != nil {
			return config.Config{}, err
		}
		selections = append(selections, selection)
	}

	out := config.Config{
		DefaultProvider: name,
		Providers:       make(map[string]config.ProviderConfig),
		Agent:           source.Agent,
		PTC:             source.PTC,
	}
	out.Agent.CatwalkAutoupdate = false
	for _, selection := range selections {
		// Match runtime client construction: catalog defaults and per-model
		// transport overrides can change both the key source and auth mode.
		key, token, err := providerfactory.ResolveStaticCredentials(selection.RuleProviderConfig, selection.Provider, home)
		if errors.Is(err, providerfactory.ErrHomeBackedCredentials) {
			return config.Config{}, fmt.Errorf("provider %q needs its subscription credential store; use --environment inherited to evaluate with the existing home", selection.Provider)
		}
		if err != nil {
			return config.Config{}, fmt.Errorf("resolve eval credentials for %q model %q: %w", selection.Provider, selection.Model, err)
		}
		if previous, ok := out.Providers[selection.Provider]; ok {
			if previous.APIKey != key || previous.AuthToken != token {
				return config.Config{}, fmt.Errorf("provider %q selects different credentials across its models; use --environment inherited to preserve per-model authentication", selection.Provider)
			}
			continue
		}
		// Retain the original transport and model variants for runtime
		// enrichment. In particular, do not freeze one role's wire protocol
		// into a provider shared with another model. Keep explicit env selectors:
		// removing an unset selector would enable catalog/ambient fallbacks.
		provider := source.Providers[selection.Provider]
		provider.APIKey, provider.AuthToken = key, token
		out.Providers[selection.Provider] = provider
	}
	return out, nil
}

// Each invocation gets its own directory, including when reusing --artifacts-dir.
// Private runtime state is retained here; credentials stay in memory only.
func createEvalArtifactsDir(parent string) (string, error) {
	if parent != "" {
		var err error
		parent, err = filepath.Abs(parent)
		if err != nil {
			return "", err
		}
		if err := securefs.Mkdir(parent); err != nil {
			return "", err
		}
	}
	return os.MkdirTemp(parent, "wuu-eval-results-*")
}

// A fresh process also resets provider coordination, caches and diagnostic
// sinks. Only this worker receives the changed environment.
func runEvalTaskProcess(ctx context.Context, cfg evalTaskRunConfig) (result evalharness.Result) {
	started := time.Now()
	result.TaskID, result.TaskName = cfg.Task.ID, cfg.Task.Name
	defer func() {
		result.DurationMS = time.Since(started).Milliseconds()
		result.Validation = evalharness.BuildValidationSummary(result)
		persistEvalTrace(&result)
	}()
	if ctx.Err() != nil {
		result.Error = "eval task not started: " + ctx.Err().Error()
		return result
	}
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	artifactDir, err := os.MkdirTemp(cfg.ArtifactsDir, cfg.Task.ID+"-*")
	if err != nil {
		result.Error = err.Error()
		return result
	}
	cfg.TaskID, cfg.ArtifactsDir = cfg.Task.ID, artifactDir
	result.Environment = evalEnvironmentObservation(cfg, cfg.Config, nil)
	tempRoot, err := os.MkdirTemp("", "wuu-eval-"+cfg.Task.ID+"-*")
	if err != nil {
		result.Error = err.Error()
		return result
	}
	cfg.Workdir = filepath.Join(tempRoot, "workspace")
	if cfg.KeepWorkdir {
		result.Workdir = cfg.Workdir
	} else {
		defer os.RemoveAll(tempRoot)
	}
	if err := securefs.Mkdir(cfg.Workdir); err != nil {
		result.Error = err.Error()
		return result
	}
	env := os.Environ()
	if cfg.Environment == evalEnvironmentIsolated {
		cfg.HomeDir, cfg.ConfigPath = filepath.Join(tempRoot, "home"), ""
		if err := securefs.Mkdir(cfg.HomeDir); err != nil {
			result.Error = err.Error()
			return result
		}
		// Bound instruction and skill ancestor discovery even if TMPDIR is
		// inside a repository. Individual git fixtures create their own repo.
		if err := securefs.Mkdir(filepath.Join(tempRoot, ".git")); err != nil {
			result.Error = err.Error()
			return result
		}
		for key, value := range map[string]string{
			"HOME": cfg.HomeDir, "WUU_HOME": filepath.Join(artifactDir, "state"),
			"XDG_CONFIG_HOME":         filepath.Join(cfg.HomeDir, ".config"),
			"XDG_CACHE_HOME":          filepath.Join(cfg.HomeDir, ".cache"),
			"XDG_DATA_HOME":           filepath.Join(cfg.HomeDir, ".local", "share"),
			"XDG_STATE_HOME":          filepath.Join(cfg.HomeDir, ".local", "state"),
			"GIT_CEILING_DIRECTORIES": tempRoot,
		} {
			// os/exec keeps the last entry for each environment key.
			env = append(env, key+"="+value)
		}
	}
	payload, err := json.Marshal(cfg)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	executable, err := os.Executable()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	stderr, err := securefs.OpenAppend(filepath.Join(artifactDir, "worker.log"))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer stderr.Close()
	cmd := exec.Command(executable, "eval-worker")
	var output bytes.Buffer
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, bytes.NewReader(payload), &output, io.MultiWriter(os.Stderr, stderr)
	cmd.WaitDelay = process.DefaultStopGracePeriod
	if ctx.Err() != nil {
		result.Error = "eval worker not started: " + ctx.Err().Error()
		return result
	}
	handle, err := process.StartCommand(cmd)
	if err != nil {
		result.Error = fmt.Sprintf("start eval worker: %v", err)
		return result
	}
	select {
	case <-handle.Done():
	case <-ctx.Done():
		// The worker cancels inference on SIGTERM and cleans up its managed
		// tools/plugins. Bound that cleanup before force-stopping the tree.
		if err := handle.Stop(10 * time.Second); err != nil {
			result.Error = fmt.Sprintf("stop eval worker: %v", err)
			return result
		}
	}
	waitErr := handle.Wait()
	var workerResult evalharness.Result
	decodeErr := json.Unmarshal(output.Bytes(), &workerResult)
	if decodeErr == nil {
		result = workerResult
	}
	if ctx.Err() != nil {
		result.Success, result.Error = false, ctx.Err().Error()
	} else if waitErr != nil {
		result.Success, result.Error = false, fmt.Sprintf("eval worker failed: %v (see worker.log)", waitErr)
	} else if decodeErr != nil {
		result.Error = fmt.Sprintf("decode eval worker result: %v", decodeErr)
	}
	return result
}

func runEvalWorker(input io.Reader, output io.Writer) error {
	var cfg evalTaskRunConfig
	if err := json.NewDecoder(input).Decode(&cfg); err != nil {
		return fmt.Errorf("read eval worker configuration: %w", err)
	}
	task, ok := evalharness.ByID(cfg.TaskID)
	if !ok {
		return fmt.Errorf("unknown eval task %q", cfg.TaskID)
	}
	cfg.Task = task
	return json.NewEncoder(output).Encode(runEvalTask(cfg))
}

func evalEnvironmentObservation(run evalTaskRunConfig, cfg config.Config, rt *runtime.Session) *evalharness.EnvironmentObservation {
	policy := "provider_agent_ptc_and_task"
	if run.Environment == evalEnvironmentInherited {
		policy = "existing_config_and_task"
	}
	observation := &evalharness.EnvironmentObservation{
		Mode: run.Environment, Configuration: policy, ArtifactsDir: run.ArtifactsDir,
		HostEnvironmentInherited: true,
		Platform:                 goruntime.GOOS + "/" + goruntime.GOARCH,
		WuuVersion:               version.Info().Version,
		Agent:                    cfg.Agent,
		TaskMaxSteps:             cfg.Agent.MaxSteps,
		TaskTimeoutMS:            run.Timeout.Milliseconds(),
		PTC:                      cfg.PTC,
	}
	if run.MaxSteps > 0 {
		observation.TaskMaxSteps = run.MaxSteps
	}
	for name := range cfg.MCPServers {
		observation.MCPServers = append(observation.MCPServers, name)
	}
	sort.Strings(observation.MCPServers)
	if rt == nil {
		return observation
	}
	for _, plugin := range rt.ActivePlugins {
		observation.Plugins = append(observation.Plugins, evalharness.PluginObservation{
			ID: plugin.ID, Version: plugin.Version, Fingerprint: plugin.Fingerprint,
		})
	}
	for _, file := range rt.InstructionFiles {
		sum := sha256.Sum256([]byte(file.Content))
		observation.Instructions = append(observation.Instructions, evalharness.GuidanceObservation{
			Name: file.Name, Source: file.Source, ContentSHA256: hex.EncodeToString(sum[:]),
		})
	}
	for _, skill := range rt.AvailableSkills() {
		sum := sha256.Sum256([]byte(skill.Description + "\n" + skill.Content))
		observation.Skills = append(observation.Skills, evalharness.GuidanceObservation{
			Name: skill.Name, Source: skill.Source, ContentSHA256: hex.EncodeToString(sum[:]),
		})
	}
	return observation
}
