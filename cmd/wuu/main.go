package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "init":
		return runInit(args[1:])
	case "run":
		return runTask(args[1:])
	case "tui":
		return runTUI(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
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

	content, err := config.TemplateJSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("created %s\n", configPath)
	return nil
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
	workdir := fs.String("workdir", "", "workspace directory")
	noTools := fs.Bool("no-tools", false, "disable local tools")
	fs.Duration("request-timeout", 10*time.Minute, "single request timeout (e.g. 2m)")
	memoryFile := fs.String("memory-file", "", "session memory file path (deprecated, use sessions)")
	resumeID := fs.String("resume", "", "resume session by ID (empty with flag = most recent)")
	fs.String("pre-hook", strings.TrimSpace(os.Getenv("WUU_PRE_HOOK")), "shell command before each prompt")
	fs.String("post-hook", strings.TrimSpace(os.Getenv("WUU_POST_HOOK")), "shell command after each prompt")
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

	client, err := providerfactory.BuildStreamClient(providerCfg)
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

	streamRunner := &agent.StreamRunner{
		Client:       client,
		Tools:        toolExecutor,
		Model:        providerCfg.Model,
		SystemPrompt: cfg.Agent.SystemPrompt,
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

	return tui.Run(tui.Config{
		Provider:     resolvedName,
		Model:        providerCfg.Model,
		ConfigPath:   configPath,
		MemoryPath:   resolvedMemoryPath,
		SessionDir:   sessDir,
		ResumeID:     resolvedResumeID,
		StreamRunner: streamRunner,
	})
}

func runPromptWithHooks(
	ctx context.Context,
	preHook string,
	postHook string,
	prompt string,
	run func(context.Context, string) (string, error),
) (string, error) {
	if strings.TrimSpace(preHook) != "" {
		if err := runHook(ctx, preHook, map[string]string{
			"WUU_PROMPT": prompt,
		}); err != nil {
			return "", fmt.Errorf("pre-hook failed: %w", err)
		}
	}

	answer, runErr := run(ctx, prompt)
	if strings.TrimSpace(postHook) != "" {
		hookErr := runHook(ctx, postHook, map[string]string{
			"WUU_PROMPT": prompt,
			"WUU_ANSWER": answer,
		})
		if hookErr != nil {
			if runErr == nil {
				runErr = fmt.Errorf("post-hook failed: %w", hookErr)
			} else {
				runErr = fmt.Errorf("%w; post-hook failed: %v", runErr, hookErr)
			}
		}
	}
	return answer, runErr
}

func runHook(ctx context.Context, command string, env map[string]string) error {
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, trimmed)
	}
	return nil
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

func printUsage() {
	fmt.Println(`wuu - coding agent CLI (MVP)

Usage:
  wuu init [--force]
  wuu run [flags] "your coding task"
  wuu tui [flags]

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
  --max-steps       max tool loop steps
  --temperature     temperature override
  --system-prompt   system prompt override
  --workdir         workspace directory
  --no-tools        disable local tools
  --memory-file     session memory file path
  --pre-hook        shell hook before each prompt
  --post-hook       shell hook after each prompt
  --request-timeout single request timeout (default 10m)`)
}
