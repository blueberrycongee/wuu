package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	wuurt "github.com/blueberrycongee/wuu/internal/runtime"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/version"
)

func runPluginExperiment(args []string) error {
	if len(args) == 0 {
		return pluginCLIError(errors.New("plugin experiment requires capture, list, eval, select, or rollback"))
	}
	switch args[0] {
	case "capture":
		return runPluginExperimentCapture(args[1:])
	case "list":
		return runPluginExperimentList(args[1:])
	case "eval":
		return runPluginExperimentEval(args[1:])
	case "check-runtime":
		return runPluginExperimentCheckRuntime(args[1:])
	case "select", "rollback":
		return runPluginExperimentSelect(args[0], args[1:])
	default:
		return pluginCLIError(fmt.Errorf("unknown plugin experiment subcommand %q", args[0]))
	}
}

func runPluginExperimentCapture(args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin experiment capture")
	workspaceFlag := fs.String("workdir", "", "workspace containing the candidate source (defaults to current directory)")
	packageManager := fs.String("package-manager", "", "package manager executable for directory builds")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	source, err := requiredPluginArgument(fs, "plugin experiment capture requires one TypeScript file or plugin directory")
	if err != nil {
		return err
	}
	workspace, err := pluginExperimentWorkspace(*workspaceFlag)
	if err != nil {
		return pluginCLIError(err)
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return pluginCLIError(err)
	}
	// Keep the single-file path's existing restriction against symlink sources.
	if _, err := readPluginIdentity(source); err != nil {
		return pluginCLIError(err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return pluginCLIError(err)
	}
	if !pluginExperimentPathWithin(workspace, source) {
		return pluginCLIError(errors.New("experiment source must be inside its workspace"))
	}
	home, err := statepath.Home("")
	if err != nil {
		return pluginCLIError(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	info, err := os.Stat(source)
	if err != nil {
		return pluginCLIError(err)
	}
	var prepared string
	var cleanup func()
	if info.IsDir() {
		// Build scripts retain the existing host-code trust boundary. Their logs
		// go to stderr so --json always has one machine-readable result.
		if err := executePluginBuildOutput(ctx, source, strings.TrimSpace(*packageManager), os.Stderr); err != nil {
			return pluginCLIError(fmt.Errorf("capture build: %w", err))
		}
		prepared, cleanup, err = preparePluginSource(source)
	} else {
		prepared, cleanup, err = prepareSingleFilePlugin(ctx, source)
	}
	if err != nil {
		return pluginCLIError(fmt.Errorf("capture source: %w", err))
	}
	defer cleanup()
	manifest, err := pluginpkg.LoadManifest(filepath.Join(prepared, pluginpkg.ManifestFilename), "experiment")
	if err != nil {
		return pluginCLIError(err)
	}
	if err := pluginpkg.CheckMinimumWuuVersion(manifest.MinimumWuuVersion, version.Info().Version); err != nil {
		return pluginCLIError(err)
	}
	candidate, err := pluginpkg.CaptureExperimentCandidate(home, workspace, source, prepared)
	if err != nil {
		return pluginCLIError(err)
	}
	if *jsonOutput {
		return printPluginJSON(candidate)
	}
	fmt.Printf("Captured candidate %s for %s\n", candidate.CandidateID, candidate.ID)
	fmt.Printf("Fingerprint: %s\n", candidate.Fingerprint)
	fmt.Println("The published generation is unchanged. Evaluate this candidate before selecting it.")
	return nil
}

func runPluginExperimentList(args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin experiment list")
	workspaceFlag := fs.String("workdir", "", "workspace whose captured candidates should be listed")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	if fs.NArg() > 1 {
		return pluginCLIError(errors.New("plugin experiment list accepts at most one plugin id"))
	}
	workspace, err := pluginExperimentWorkspace(*workspaceFlag)
	if err != nil {
		return pluginCLIError(err)
	}
	home, err := statepath.Home("")
	if err != nil {
		return pluginCLIError(err)
	}
	candidates, err := pluginpkg.ListExperimentCandidates(home, strings.TrimSpace(fs.Arg(0)))
	if err != nil {
		return pluginCLIError(err)
	}
	visible := make([]pluginpkg.ExperimentCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Workspace == workspace && pluginExperimentPathWithin(workspace, candidate.SourcePath) {
			visible = append(visible, candidate)
		}
	}
	if *jsonOutput {
		return printPluginJSON(visible)
	}
	if len(visible) == 0 {
		fmt.Println("No experiment candidates found in this workspace.")
	}
	for _, candidate := range visible {
		fmt.Printf("%s\t%s\t%s\n", candidate.ID, candidate.CandidateID, candidate.Fingerprint)
	}
	return nil
}

func runPluginExperimentSelect(action string, args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin experiment " + action)
	workspaceFlag := fs.String("workdir", "", "workspace whose candidate should be selected")
	expectedCurrent := fs.String("expect-current", "", "required exact current published fingerprint; empty means plugin absent")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	hasExpected := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "expect-current" {
			hasExpected = true
		}
	})
	if fs.NArg() != 2 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" || !hasExpected {
		return pluginCLIError(fmt.Errorf("plugin experiment %s requires --expect-current, a plugin id, and an exact candidate id", action))
	}
	workspace, err := pluginExperimentWorkspace(*workspaceFlag)
	if err != nil {
		return pluginCLIError(err)
	}
	home, err := statepath.Home("")
	if err != nil {
		return pluginCLIError(err)
	}
	candidate, err := pluginpkg.ValidateExperimentSelection(home, workspace, fs.Arg(0), fs.Arg(1), *expectedCurrent)
	if err != nil {
		return pluginCLIError(err)
	}
	if candidate.Workspace != workspace || !pluginExperimentPathWithin(workspace, candidate.SourcePath) {
		return pluginCLIError(errors.New("candidate belongs to a different workspace"))
	}
	executable, err := os.Executable()
	if err != nil {
		return pluginCLIError(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	preflight, err := pluginpkg.EvaluateExperiments(ctx, home, pluginpkg.ExperimentEvaluationRequest{
		ID: candidate.ID, CandidateIDs: []string{candidate.CandidateID}, Workspace: workspace,
		Command:     []string{executable, "plugin", "experiment", "check-runtime", candidate.ID, candidate.Fingerprint},
		Concurrency: 1, Timeout: 30 * time.Second,
	})
	if err != nil {
		return pluginCLIError(fmt.Errorf("candidate readiness check: %w", err))
	}
	if len(preflight.Runs) != 1 || preflight.Runs[0].Status != "succeeded" {
		return pluginCLIError(fmt.Errorf("candidate startup readiness failed; this selection was not published; inspect %s", preflight.ReportPath))
	}
	selection, err := pluginpkg.SelectExperimentCandidate(home, workspace, candidate.ID, candidate.CandidateID, *expectedCurrent)
	if err != nil {
		return pluginCLIError(fmt.Errorf("candidate readiness passed, but selection failed (evidence: %s): %w", preflight.ReportPath, err))
	}
	if *jsonOutput {
		return printPluginJSON(struct {
			pluginpkg.ExperimentSelection
			ReadinessReport string `json:"readiness_report"`
		}{ExperimentSelection: selection, ReadinessReport: preflight.ReportPath})
	}
	fmt.Printf("Selected candidate %s for %s at epoch %d\n", candidate.CandidateID, candidate.ID, selection.Epoch)
	fmt.Println("Idle conversations can adopt the selected code. Running turns stay pinned; business state and external effects are unchanged.")
	return nil
}

// check-runtime is invoked only by the isolated evaluator. It uses the normal
// host lifecycle, with an unavailable inference client and no copied settings.
func runPluginExperimentCheckRuntime(args []string) (retErr error) {
	if len(args) != 2 || os.Getenv("WUU_EXPERIMENT_CANDIDATE") == "" || os.Getenv("WUU_EXPERIMENT_PACKAGE") == "" {
		return pluginCLIError(errors.New("runtime readiness requires an isolated experiment run, plugin id, and exact fingerprint"))
	}
	home, err := statepath.Home("")
	if err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	settings := &extensions.Settings{}
	found := false
	for _, item := range pluginpkg.Discover(root, home) {
		if item.ID != args[0] {
			continue
		}
		if item.Fingerprint != args[1] || item.Root != os.Getenv("WUU_EXPERIMENT_PACKAGE") || !item.AuthorizedDev {
			return errors.New("readiness package does not match the exact isolated candidate")
		}
		// Check dormant packages too, without changing their production policy.
		settings.SetEnabled(item.SubjectID, true)
		found = true
	}
	if !found {
		return errors.New("isolated candidate was not discovered; check compatibility and required dependencies")
	}
	cfg := config.Config{
		DefaultProvider: "experiment-unavailable",
		Providers: map[string]config.ProviderConfig{"experiment-unavailable": {
			Type: "openai-compatible", BaseURL: "http://127.0.0.1:1/v1",
			APIKeyEnv: "WUU_EXPERIMENT_NO_CREDENTIAL", Model: "experiment-readiness",
		}},
		Extensions: settings,
	}
	startup, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(startup, 20*time.Second)
	defer cancel()
	runtime, err := wuurt.NewSession(wuurt.Options{
		RootDir: root, HomeDir: os.Getenv("HOME"), Config: cfg,
		NonInteractive: true, MCPOAuth: mcp.NewOAuthManager(nil, nil), StartupContext: startup,
	})
	if err != nil {
		return fmt.Errorf("initialize isolated runtime: %w", err)
	}
	defer func() {
		_, cleanupErr := runtime.CleanupWithContext(startup)
		retErr = errors.Join(retErr, cleanupErr)
		reports, err := runtime.PluginGenerationRevocations(1)
		retErr = errors.Join(retErr, err)
		if len(reports) == 0 {
			retErr = errors.Join(retErr, errors.New("runtime cleanup produced no generation retirement receipt"))
		} else if reports[0].Failed() {
			retErr = errors.Join(retErr, errors.New("runtime generation cleanup failed; inspect its retirement receipt"))
		}
	}()
	for _, item := range runtime.ActivePlugins {
		if item.ID != args[0] || item.Fingerprint != args[1] {
			continue
		}
		if item.Runtime == nil {
			return nil
		}
		if runtime.PluginHost != nil {
			for _, status := range runtime.PluginHost.Statuses() {
				if status.ID == item.ID {
					if status.State == pluginhost.StateActive {
						return nil
					}
					return fmt.Errorf("candidate runtime is %s: %s", status.State, status.Error)
				}
			}
		}
		return errors.New("candidate runtime has no active host process")
	}
	if runtime.PluginHost != nil {
		for _, status := range runtime.PluginHost.Statuses() {
			if status.ID == args[0] {
				return fmt.Errorf("candidate did not become active (%s): %s", status.State, status.Error)
			}
		}
	}
	return errors.New("candidate did not become active in the isolated runtime; check startup and required dependencies")
}

func pluginExperimentWorkspace(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve experiment workspace: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("experiment workspace must be a directory")
	}
	return path, nil
}

func pluginExperimentPathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
