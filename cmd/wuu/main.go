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

	tea "github.com/charmbracelet/bubbletea"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/memory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/tui"
	"github.com/blueberrycongee/wuu/internal/version"
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
		return runVersion(nil)
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

	client, err := providerfactory.BuildClient(providerCfg)
	if err != nil {
		return err
	}

	var toolExecutor agent.ToolExecutor
	if !*noTools {
		kit, newErr := tools.New(rootDir)
		if newErr != nil {
			return newErr
		}
		toolExecutor = kit
	}

	runner := agent.Runner{
		Client:       client,
		Tools:        toolExecutor,
		Model:        providerCfg.Model,
		SystemPrompt: cfg.Agent.SystemPrompt,
		MaxSteps:     cfg.Agent.MaxSteps,
		Temperature:  cfg.Agent.Temperature,
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
	requestTimeout := fs.Duration("request-timeout", 10*time.Minute, "single request timeout (e.g. 2m)")
	memoryFile := fs.String("memory-file", "", "session memory file path (deprecated, use sessions)")
	resumeID := fs.String("resume", "", "resume session by ID (empty with flag = most recent)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rootDir, err := resolveWorkdir(*workdir)
	if err != nil {
		return err
	}

	homeDir := os.Getenv("HOME")

	// Resolve theme mode from CLI override or global preferences.
	globalCfg, err := config.LoadGlobalConfig(homeDir)
	if err != nil {
		return fmt.Errorf("load global preferences: %w", err)
	}
	resolvedTheme := strings.TrimSpace(globalCfg.Theme)
	if resolvedTheme == "" {
		resolvedTheme = "auto"
	}
	overrideTheme := strings.TrimSpace(*themeMode)
	if overrideTheme != "" {
		resolvedTheme = overrideTheme
	}
	if err := tui.SetThemeMode(resolvedTheme); err != nil {
		if overrideTheme != "" {
			return err
		}
		// Invalid persisted preference should never block startup.
		if fallbackErr := tui.SetThemeMode("auto"); fallbackErr != nil {
			return fallbackErr
		}
	}

	cfg, configPath, err := config.LoadFrom(rootDir, homeDir)
	if err != nil {
		// No config found — run onboarding.
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

	client, err := providerfactory.BuildStreamClient(providerCfg)
	if err != nil {
		return err
	}

	// Initialize debug logging.
	providers.InitDebugLog(rootDir)

	// Build hook dispatcher from config.
	hookEntries := make(map[hooks.Event][]hooks.HookConfig)
	for evName, entries := range cfg.Hooks {
		ev := hooks.Event(evName)
		for _, e := range entries {
			hookEntries[ev] = append(hookEntries[ev], hooks.HookConfig{
				Matcher: e.Matcher,
				Command: e.Command,
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

	var toolExecutor agent.ToolExecutor
	var toolkit *tools.Toolkit
	if !*noTools {
		kit, newErr := tools.New(rootDir)
		if newErr != nil {
			return newErr
		}
		kit.SetSkills(discoveredSkills)
		toolkit = kit
		toolExecutor = hooks.NewHookedExecutor(kit, hookDispatcher, "", rootDir)
	}

	// Discover memory files (CLAUDE.md / AGENTS.md) from project hierarchy
	// and ~/.claude/, then bake them into the system prompt before skills.
	memoryFiles := memory.Discover(rootDir, homeDir)

	systemPromptText := cfg.Agent.SystemPrompt
	if len(memoryFiles) > 0 {
		systemPromptText = appendMemoryToPrompt(systemPromptText, memoryFiles)
	}
	if len(discoveredSkills) > 0 {
		systemPromptText = appendSkillsToPrompt(systemPromptText, discoveredSkills)
	}

	streamRunner := &agent.StreamRunner{
		Client:       client,
		Tools:        toolExecutor,
		Model:        providerCfg.Model,
		SystemPrompt: systemPromptText,
		MaxSteps:     cfg.Agent.MaxSteps,
		Temperature:  cfg.Agent.Temperature,
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
		Provider:         resolvedName,
		Model:            providerCfg.Model,
		ConfigPath:       configPath,
		MemoryPath:       resolvedMemoryPath,
		SessionDir:       sessDir,
		ResumeID:         resolvedResumeID,
		MaxContextTokens: cfg.Agent.MaxContextTokens,
		RequestTimeout:   *requestTimeout,
		StreamRunner:     streamRunner,
		HookDispatcher:   hookDispatcher,
		Skills:           discoveredSkills,
	}
	if toolkit != nil {
		cfgUI.OnSessionID = toolkit.SetSessionID
	}
	return tui.Run(cfgUI)
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

// appendMemoryToPrompt prepends the contents of discovered memory files
// (CLAUDE.md / AGENTS.md) into the system prompt under a clearly-labeled
// section. Each file is shown with its source and path so the model can
// track which conventions came from where.
func appendMemoryToPrompt(base string, files []memory.File) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(base, "\n"))
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString("# Memory\n\n")
	b.WriteString("The following memory files contain project- and user-defined conventions, ")
	b.WriteString("style guides, and constraints. Treat them as binding instructions for this session.\n\n")
	for _, f := range files {
		fmt.Fprintf(&b, "## %s _[%s · %s]_\n\n", f.Name, f.Source, f.Path)
		b.WriteString(strings.TrimRight(f.Content, "\n"))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// appendSkillsToPrompt adds a "Session-specific guidance" section that lists
// available skills, their descriptions, and instructions for invocation.
// Format mirrors Claude Code's session_guidance system prompt section so the
// model treats wuu skills with the same conventions.
func appendSkillsToPrompt(base string, sks []skills.Skill) string {
	// Filter out skills hidden from model invocation.
	visible := make([]skills.Skill, 0, len(sks))
	for _, s := range sks {
		if s.DisableModelInvoke {
			continue
		}
		visible = append(visible, s)
	}
	if len(visible) == 0 {
		return base
	}

	var b strings.Builder
	b.WriteString(strings.TrimRight(base, "\n"))
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}

	b.WriteString("# Session-specific guidance\n\n")
	b.WriteString("## Skills\n\n")
	b.WriteString("The following skills are available in this session. Each skill is a reusable, ")
	b.WriteString("project- or user-defined instruction set that encodes conventions, recipes, or workflows.\n\n")
	b.WriteString("**How to use skills:**\n")
	b.WriteString("1. Read the skill catalog below — match the user's intent against each skill's description and \"when to use\" guidance.\n")
	b.WriteString("2. When a skill applies, call the `load_skill` tool with the skill's name to retrieve the full body. ")
	b.WriteString("Pass any user-supplied arguments via the `arguments` parameter.\n")
	b.WriteString("3. Follow the loaded skill's instructions exactly. If the skill body contains tool restrictions or step orderings, respect them.\n")
	b.WriteString("4. Users can also invoke skills directly by typing `/<skill-name>` (e.g. `/commit`). When that happens, the skill body is injected as a user message — no need to call `load_skill` separately.\n\n")

	b.WriteString("**Skill catalog:**\n\n")
	for _, s := range visible {
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "- **%s** _[%s]_ — %s\n", s.Name, s.Source, desc)
		if s.WhenToUse != "" {
			fmt.Fprintf(&b, "  - When to use: %s\n", s.WhenToUse)
		}
		if s.ArgumentHint != "" {
			fmt.Fprintf(&b, "  - Usage: `/%s %s`\n", s.Name, s.ArgumentHint)
		}
		if len(s.AllowedTools) > 0 {
			fmt.Fprintf(&b, "  - Allowed tools: %s\n", strings.Join(s.AllowedTools, ", "))
		}
	}
	return b.String()
}

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
  --request-timeout single request timeout (default 10m)`)
}
