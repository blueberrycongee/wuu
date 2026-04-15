package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/blueberrycongee/wuu/internal/agent"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/coordinator"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/memory"
	processruntime "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/prompt"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/tui"
	"github.com/blueberrycongee/wuu/internal/version"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return runTUI(nil)
	}

	switch args[0] {
	case "init":
		return runInit(args[1:])
	case "run":
		return runTask(args[1:])
	case "tui":
		return runTUI(args[1:])
	case "version", "-v", "--version":
		if args[0] == "version" {
			return runVersion(args[1:])
		}
		return runVersion(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		// No subcommand → default to TUI.
		return runTUI(args)
	}
}

func runVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	long := fs.Bool("long", false, "show detailed version info")
	jsonOutput := fs.Bool("json", false, "output version as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	info := version.Info()
	if *jsonOutput {
		data, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal version info: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}
	if *long {
		fmt.Println(info.LongString())
		return nil
	}

	fmt.Println(info.String())
	return nil
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	force := fs.Bool("force", false, "overwrite existing .wuu.json")
	if err := fs.Parse(args); err != nil {
		return err
	}

	workdir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	configPath := filepath.Join(workdir, ".wuu.json")

	if !*force {
		if _, err := os.Stat(configPath); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", configPath)
		}
	}

	result, err := runOnboarding()
	if err != nil {
		return err
	}
	if !result.Completed {
		fmt.Println("setup cancelled")
		return nil
	}

	return writeOnboardingResult(workdir, os.Getenv("HOME"), result)
}

func runTask(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	providerName := fs.String("provider", "", "provider name in config")
	modelOverride := fs.String("model", "", "model override")
	maxSteps := fs.Int("max-steps", 0, "max tool loop steps")
	temperature := fs.Float64("temperature", -1, "sampling temperature override")
	systemPrompt := fs.String("system-prompt", "", "system prompt override")
	workdir := fs.String("workdir", "", "workspace directory")
	noTools := fs.Bool("no-tools", false, "disable local tools")
	timeout := fs.Duration("timeout", 10*time.Minute, "request timeout (e.g. 5m)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rootDir, err := resolveWorkdir(*workdir)
	if err != nil {
		return err
	}

	cfg, configPath, err := config.LoadFrom(rootDir, os.Getenv("HOME"))
	if err != nil {
		return err
	}

	providerCfg, resolvedName, err := cfg.ResolveProvider(*providerName)
	if err != nil {
		return err
	}
	if *modelOverride != "" {
		providerCfg.Model = *modelOverride
	}

	client, err := providerfactory.BuildStreamClient(providerCfg, resolvedName)
	if err != nil {
		return err
	}

	var toolExecutor agent.ToolExecutor
	var processMgr *processruntime.Manager
	processMgr, err = processruntime.NewManager(rootDir)
	if err != nil {
		return err
	}
	if !*noTools {
		kit, newErr := tools.New(rootDir)
		if newErr != nil {
			return newErr
		}
		// Main agent is read-oriented: remove direct/indirect file-writing primitives.
		kit.DisableTools("write_file", "edit_file", "run_shell")
		kit.SetProcessManager(processMgr)
		toolExecutor = kit
	}

	runner := agent.StreamRunner{
		Client:       client,
		Tools:        toolExecutor,
		Model:        providerCfg.Model,
		SystemPrompt: cfg.Agent.SystemPrompt,
		MaxSteps:     cfg.Agent.MaxSteps,
		Temperature:  cfg.Agent.Temperature,
		ContextWindowOverride: resolveContextWindow(
			providerCfg.Model,
			providerCfg.ContextWindow,
			cfg.Agent.MaxContextTokens,
		),
	}

	if *maxSteps > 0 {
		runner.MaxSteps = *maxSteps
	}
	if *temperature >= 0 {
		runner.Temperature = *temperature
	}
	if strings.TrimSpace(*systemPrompt) != "" {
		runner.SystemPrompt = *systemPrompt
	}

	prompt, err := resolvePrompt(fs.Args())
	if err != nil {
		return err
	}

	ctx := context.Background()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	answer, err := runner.Run(ctx, prompt)
	if err != nil {
		return err
	}

	fmt.Printf("provider: %s\nmodel: %s\nconfig: %s\n\n", resolvedName, providerCfg.Model, configPath)
	fmt.Println(answer)
	return nil
}

func runTUI(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	providerName := fs.String("provider", "", "provider name in config")
	modelOverride := fs.String("model", "", "model override")
	maxSteps := fs.Int("max-steps", 0, "max tool loop steps")
	temperature := fs.Float64("temperature", -1, "sampling temperature override")
	systemPrompt := fs.String("system-prompt", "", "system prompt override")
	themeMode := fs.String("theme", "", "theme override: auto|dark|light")
	workdir := fs.String("workdir", "", "workspace directory")
	noTools := fs.Bool("no-tools", false, "disable local tools")
	requestTimeout := fs.Duration("request-timeout", 0, "turn timeout (e.g. 2m, 0 disables)")
	memoryFile := fs.String("memory-file", "", "session memory file path (deprecated, use sessions)")
	resumeID := fs.String("resume", "", "resume session by ID (empty with flag = most recent)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rootDir, err := resolveWorkdir(*workdir)
	if err != nil {
		return err
	}

	providers.InitDebugLog(rootDir)

	homeDir := os.Getenv("HOME")

	resolvedTheme, err := resolveTUIThemeMode(homeDir, strings.TrimSpace(*themeMode))
	if err != nil {
		return err
	}
	if err := tui.SetThemeMode(resolvedTheme); err != nil {
		if strings.TrimSpace(*themeMode) != "" {
			return err
		}
		// Invalid persisted preference should never block startup.
		if fallbackErr := tui.SetThemeMode("auto"); fallbackErr != nil {
			return fallbackErr
		}
	}

	cfg, configPath, err := config.LoadFrom(rootDir, homeDir)
	if err != nil {
		// Only enter onboarding when the config genuinely does not
		// exist. A present-but-broken config (parse error, failed
		// validation, etc.) must surface so the user can fix it —
		// otherwise onboarding would silently overwrite their
		// existing .wuu.json with a fresh template.
		if !errors.Is(err, config.ErrConfigNotFound) {
			return err
		}
		result, onboardErr := runOnboarding()
		if onboardErr != nil {
			return onboardErr
		}
		if !result.Completed {
			return nil // user cancelled
		}

		if writeErr := writeOnboardingResult(rootDir, homeDir, result); writeErr != nil {
			return writeErr
		}

		// Reload config.
		cfg, configPath, err = config.LoadFrom(rootDir, homeDir)
		if err != nil {
			return fmt.Errorf("config still invalid after onboarding: %w", err)
		}
	}

	providerCfg, resolvedName, err := cfg.ResolveProvider(*providerName)
	if err != nil {
		return err
	}
	if *modelOverride != "" {
		providerCfg.Model = *modelOverride
	}

	client, err := providerfactory.BuildStreamClient(providerCfg, resolvedName)
	if err != nil {
		return err
	}

	// Initialize debug logging.
	providers.InitDebugLog(rootDir)

	// Catwalk model registry. Always installs the syncer (so the
	// embedded → cache resolution chain runs even with autoupdate
	// off), but only attaches a remote client when autoupdate is
	// enabled in config. The first ContextWindowFor lookup populates
	// the index; if autoupdate is on, a tiny background goroutine
	// also kicks off a remote refresh that swaps the in-memory
	// index when the fetch returns.
	catwalkCfg := providers.CatwalkSyncConfig{
		CachePath: providers.DefaultCatwalkCachePath(),
	}
	if cfg.Agent.CatwalkAutoupdate {
		catwalkCfg.Client = catwalk.NewWithURL(providers.DefaultCatwalkURL)
	}
	catwalkSync := providers.NewCatwalkSync(catwalkCfg)
	providers.SetCatwalkSync(catwalkSync)
	if cfg.Agent.CatwalkAutoupdate {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = providers.RefreshCatwalkIndex(ctx)
		}()
	}

	// Build hook dispatcher from config.
	hookEntries := make(map[hooks.Event][]hooks.HookConfig)
	for evName, entries := range cfg.Hooks {
		ev := hooks.Event(evName)
		for _, e := range entries {
			hookEntries[ev] = append(hookEntries[ev], hooks.HookConfig{
				Matcher: e.Matcher,
				Type:    e.Type,
				Command: e.Command,
				Prompt:  e.Prompt,
				Model:   e.Model,
				Timeout: e.Timeout,
			})
		}
	}
	hookRegistry := hooks.NewRegistry(hookEntries)
	hookDispatcher := hooks.NewDispatcher(hookRegistry)

	// Discover skills from project and user dirs.
	projectSkillsDir := filepath.Join(rootDir, ".claude", "skills")
	userSkillsDir := ""
	if home := os.Getenv("HOME"); home != "" {
		userSkillsDir = filepath.Join(home, ".claude", "skills")
	}
	discoveredSkills := skills.Discover(projectSkillsDir, userSkillsDir)

	// AskUserBridge connects the ask_user tool to the TUI's modal
	// dialog. The main agent's toolkit gets it via SetAskUserBridge;
	// sub-agent workers get a fresh toolkit without it (see the
	// WorkerFactory below) so they cannot interrupt the human.
	askBridge := tui.NewAskUserBridge()

	var toolExecutor agent.ToolExecutor
	var toolkit *tools.Toolkit
	var processMgr *processruntime.Manager
	processMgr, err = processruntime.NewManager(rootDir)
	if err != nil {
		return err
	}
	if !*noTools {
		kit, newErr := tools.New(rootDir)
		if newErr != nil {
			return newErr
		}
		// Main agent is read-oriented: remove direct/indirect file-writing primitives.
		kit.DisableTools("write_file", "edit_file", "run_shell")
		kit.SetProcessManager(processMgr)
		kit.SetSkills(discoveredSkills)
		kit.SetAskUserBridge(askBridge)
		// Wire FileChanged hook dispatch from write_file/edit_file.
		kit.SetOnFileChanged(func(absPath string) {
			_, _ = hookDispatcher.Dispatch(context.Background(), hooks.FileChanged, &hooks.Input{
				CWD:      rootDir,
				FilePath: absPath,
			})
		})
		toolkit = kit
		toolExecutor = hooks.NewHookedExecutor(kit, hookDispatcher, "", rootDir)
	}

	// Discover memory files (AGENTS.md / CLAUDE.md / AGENTS.override.md)
	// from the project hierarchy (bounded by .git markers) and from
	// user-level directories (~/.config/wuu, ~/.claude, ~/.codex).
	// Honor any overrides set under config.memory.
	var memoryFiles []memory.File
	if !cfg.Memory.Disable {
		memOpts := memory.DefaultOptions()
		if len(cfg.Memory.Filenames) > 0 {
			memOpts.Filenames = cfg.Memory.Filenames
		}
		if len(cfg.Memory.ProjectRootMarkers) > 0 {
			memOpts.ProjectRootMarkers = cfg.Memory.ProjectRootMarkers
		}
		if len(cfg.Memory.UserDirs) > 0 {
			memOpts.UserDirs = cfg.Memory.UserDirs
		}
		memoryFiles = memory.Discover(rootDir, homeDir, memOpts)
	}

	// Assemble system prompt via the section-based builder. Static
	// sections (base prompt, coordinator preamble) are placed first for
	// prompt-cache stability; dynamic sections (memory, skills, git)
	// follow. Memory files are auto-truncated to 200 lines / 25 KB.
	var pb prompt.Builder
	pb.AddSection("base", cfg.Agent.SystemPrompt, true)
	pb.AddMemory(memoryFiles)
	pb.AddSkills(discoveredSkills)

	// Inject git context when the workspace is a git repo.
	if worktree.IsGitRepo(rootDir) {
		gitCtx := prompt.NewGitContext(rootDir)
		pb.AddGitContext(gitCtx.Collect())
	}

	systemPromptText := pb.Build()

	// Ensure the cross-agent shared filesystem region exists. Agents
	// use .wuu/shared/{findings,plans,status,reports} as the data
	// plane between themselves; the system prompt teaches the
	// convention but the directories must exist on disk so list_files
	// returns something sensible on a fresh session.
	if toolkit != nil {
		if err := coordinator.EnsureSharedDir(rootDir); err != nil {
			return fmt.Errorf("ensure shared dir: %w", err)
		}
	}

	// If the workspace is a git repo and we have a toolkit, wire up the
	// coordinator runtime so the orchestration tools (spawn_agent,
	// send_message_to_agent, stop_agent, list_agents) become callable
	// and the orchestration preamble gets prepended to the system
	// prompt. The main agent keeps its full tool set either way; the
	// coordinator just adds the inter-agent primitives on top.
	var coord *coordinator.Coordinator
	if toolkit != nil && worktree.IsGitRepo(rootDir) {
		// Capture the worker base prompt BEFORE we prepend the
		// coordinator preamble — workers should see the project
		// memory & skills, not the coordinator instructions.
		workerBasePrompt := systemPromptText

		// Sub-agents get their own client instance with a more
		// aggressive HTTP retry policy than the interactive main
		// agent (6 attempts, 2s→60s backoff). Workers run for many
		// minutes and frequently sit through rate-limit bursts that
		// would otherwise kill them; the main TUI agent stays on the
		// snappier 3-attempt default so failures surface faster.
		workerRetry := providerfactory.SubAgentRetryConfig()
		workerClient, werr := providerfactory.BuildStreamClientWithRetry(providerCfg, resolvedName, &workerRetry)
		if werr != nil {
			return fmt.Errorf("build worker client: %w", werr)
		}

		c, cerr := coordinator.New(coordinator.Config{
			Client:          workerClient,
			DefaultModel:    providerCfg.Model,
			ParentRepo:      rootDir,
			WorktreeRoot:    filepath.Join(rootDir, ".wuu", "worktrees"),
			SessionID:       "session-pending", // overwritten via SetSessionInfo
			HistoryDir:      "",                // overwritten via SetSessionInfo
			WorkerSysPrompt: workerBasePrompt,
			WorkerFactory: func(workerRoot string, _ coordinator.WorkerType) (agent.ToolExecutor, error) {
				wkit, werr := tools.New(workerRoot)
				if werr != nil {
					return nil, werr
				}
				wkit.SetProcessManager(processMgr)
				wkit.SetSkills(discoveredSkills)
				// Workers do NOT get a coordinator (no recursive spawns).
				return wkit, nil
			},
			MaxParallel: 5,
		})
		if cerr == nil {
			coord = c
			toolkit.SetCoordinator(coord)
			// Prepend the orchestration preamble as a static section
			// (it goes before the base prompt in the cache prefix).
			pb.AddSection("coordinator", coordinator.SystemPromptPreamble(), true)
			systemPromptText = pb.Build()
		}
	}

	streamRunner := &agent.StreamRunner{
		Client:       client,
		Tools:        toolExecutor,
		Model:        providerCfg.Model,
		SystemPrompt: systemPromptText,
		MaxSteps:     cfg.Agent.MaxSteps,
		Temperature:  cfg.Agent.Temperature,
		ContextWindowOverride: resolveContextWindow(
			providerCfg.Model,
			providerCfg.ContextWindow,
			cfg.Agent.MaxContextTokens,
		),
		DisableAutoCompact: cfg.Agent.DisableAutoCompact,
		BeforeStep:         envContextInjector(rootDir),
	}
	if *maxSteps > 0 {
		streamRunner.MaxSteps = *maxSteps
	}
	if *temperature >= 0 {
		streamRunner.Temperature = *temperature
	}
	if strings.TrimSpace(*systemPrompt) != "" {
		streamRunner.SystemPrompt = *systemPrompt
	}

	resolvedMemoryPath, err := resolveRuntimePath(rootDir, *memoryFile)
	if err != nil {
		return err
	}

	sessDir := session.Dir(rootDir)

	// Handle --resume flag.
	resolvedResumeID := strings.TrimSpace(*resumeID)
	// Check if --resume was passed without a value (flag present but empty).
	for _, a := range args {
		if a == "--resume" && resolvedResumeID == "" {
			// Resume most recent session.
			recent, err := session.MostRecent(sessDir)
			if err == nil && recent != "" {
				resolvedResumeID = recent
			}
			break
		}
	}

	cfgUI := tui.Config{
		Provider:       resolvedName,
		Model:          providerCfg.Model,
		ConfigPath:     configPath,
		MemoryPath:     resolvedMemoryPath,
		SessionDir:     sessDir,
		ResumeID:       resolvedResumeID,
		RequestTimeout: *requestTimeout,
		StreamRunner:   streamRunner,
		HookDispatcher: hookDispatcher,
		Skills:         discoveredSkills,
		Memory:         memoryFiles,
		Coordinator:    coord,
		AskUserBridge:  askBridge,
		ProcessManager: processMgr,
	}
	if toolkit != nil {
		cfgUI.OnSessionID = func(id string) {
			toolkit.SetSessionID(id)
			sessionDir := filepath.Join(rootDir, ".wuu", "sessions", id)
			toolkit.SetSessionDir(sessionDir)
			if coord != nil {
				historyDir := filepath.Join(sessionDir, "workers")
				coord.SetSessionInfo(id, historyDir)
			}
		}
	}
	var cleanupSummary processruntime.CleanupResult
	defer func() {
		if coord != nil {
			_ = coord.CleanupSession()
		}
	}()
	if err := tui.Run(cfgUI); err != nil {
		return err
	}
	if processMgr != nil {
		result, err := processMgr.CleanupSessionWithResult()
		if err != nil {
			return err
		}
		cleanupSummary = result
	}
	if len(cleanupSummary.Cleaned) > 0 {
		fmt.Println()
		fmt.Printf("Cleaned up %d session process(es):\n", len(cleanupSummary.Cleaned))
		for _, proc := range cleanupSummary.Cleaned {
			fmt.Printf("  - %s (%s)\n", proc.Command, proc.ID)
		}
	}
	return nil
}

func resolveTUIThemeMode(homeDir, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if strings.TrimSpace(homeDir) == "" {
		return "auto", nil
	}
	globalCfg, err := config.LoadGlobalConfig(homeDir)
	if err != nil {
		return "", fmt.Errorf("load global preferences: %w", err)
	}
	resolvedTheme := strings.TrimSpace(globalCfg.Theme)
	if resolvedTheme == "" {
		return "auto", nil
	}
	return resolvedTheme, nil
}

func resolveWorkdir(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get current directory: %w", err)
		}
		return cwd, nil
	}

	abs, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}
	return abs, nil
}

func resolveRuntimePath(rootDir, input string) (string, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", nil
	}
	if filepath.IsAbs(value) {
		return value, nil
	}
	return filepath.Join(rootDir, value), nil
}

func resolveContextWindow(model string, providerOverride, agentOverride int) int {
	if providerOverride > 0 {
		return providerOverride
	}
	if agentOverride > 0 {
		return agentOverride
	}
	return providers.ContextWindowFor(model)
}

// envContextInjector returns a BeforeStep callback that injects dynamic
// environment context (CWD, date, git branch/status) as a system-reminder
// user message before each model round. This aligns with Claude Code's
// per-turn context injection — the model always knows where it is and
// what the current state looks like.
//
// The injected message is ephemeral: it goes into the live history for
// the current round but the conversation loop naturally replaces it on
// the next round. This keeps the context fresh without bloating history.
func envContextInjector(rootDir string) func() []providers.ChatMessage {
	return func() []providers.ChatMessage {
		env := wuucontext.Snapshot(rootDir)
		reminder := wuucontext.FormatSystemReminder(env)
		return []providers.ChatMessage{{
			Role:    "user",
			Content: reminder,
		}}
	}
}

// appendMemoryToPrompt and appendSkillsToPrompt removed — replaced by
// the section-based prompt.Builder in internal/prompt/.

func resolvePrompt(args []string) (string, error) {
	if len(args) > 0 {
		prompt := strings.TrimSpace(strings.Join(args, " "))
		if prompt != "" {
			return prompt, nil
		}
	}

	if !stdinHasInput() {
		return "", errors.New("prompt is required (pass text or pipe stdin)")
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", errors.New("prompt is empty")
	}
	return prompt, nil
}

func stdinHasInput() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}

func runOnboarding() (tui.OnboardingResult, error) {
	m := tui.NewOnboardingModel()
	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return tui.OnboardingResult{}, fmt.Errorf("onboarding: %w", err)
	}
	om, ok := finalModel.(tui.OnboardingModel)
	if !ok {
		return tui.OnboardingResult{}, fmt.Errorf("unexpected model type")
	}
	return om.Result(), nil
}

func writeOnboardingResult(rootDir, home string, r tui.OnboardingResult) error {
	// 1. Save API key to global auth store.
	providerName := r.ProviderType
	if providerName == "openai-compatible" {
		providerName = "custom"
	}
	if err := config.SaveAuthKey(home, providerName, r.APIKey); err != nil {
		return fmt.Errorf("save auth key: %w", err)
	}

	// 2. Write .wuu.json (no API key stored in project config).
	cfg := config.Default()
	cfg.DefaultProvider = providerName
	cfg.Providers = map[string]config.ProviderConfig{
		providerName: {
			Type:    r.ProviderType,
			BaseURL: r.BaseURL,
			Model:   r.Model,
		},
	}
	configPath := filepath.Join(rootDir, ".wuu.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// 3. Save global preferences.
	gc := config.GlobalConfig{
		Theme:                  r.Theme,
		HasCompletedOnboarding: true,
	}
	return config.SaveGlobalConfig(home, gc)
}

func printUsage() {
	fmt.Println(`wuu - coding agent CLI (MVP)

Usage:
  wuu init [--force]
  wuu run [flags] "your coding task"
  wuu tui [flags]
  wuu version [--long|--json]

Run flags:
  --provider        provider name from config
  --model           model override
  --max-steps       max tool loop steps
  --temperature     temperature override
  --system-prompt   system prompt override
  --workdir         workspace directory
  --no-tools        disable local tools
  --timeout         total timeout (default 10m)

TUI flags:
  --provider        provider name from config
  --model           model override
  --theme           theme override: auto|dark|light
  --max-steps       max tool loop steps
  --temperature     temperature override
  --system-prompt   system prompt override
  --workdir         workspace directory
  --no-tools        disable local tools
  --memory-file     session memory file path
  --request-timeout turn timeout (default disabled)`)
}
