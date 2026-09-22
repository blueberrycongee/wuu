package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/gitattribution"
	"github.com/blueberrycongee/wuu/internal/providers/codex"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/runtimeconfig"
	wuusdk "github.com/blueberrycongee/wuu/internal/sdk"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/version"
)

func main() {
	if handled, exitCode := gitattribution.Dispatch(os.Args[1:]); handled {
		os.Exit(exitCode)
	}
	lockProcessUmask()
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(wuuexec.ExitCode(err))
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}
	if args[0] == "session-tools" {
		return runSessionTools(args[1:])
	}

	// Pre-launch installs left credential-bearing files at 0o644 / 0o755.
	// Normalize them once, then rely on securefs writers and the process umask
	// for new paths. Rewalking a large session home on every command makes even
	// `wuu version` take seconds. Best-effort: a failed migration never blocks
	// startup and remains unmarked so a later launch retries it.
	if home, err := statepath.Home(""); err == nil {
		if err := securefs.TightenHomeOnce(home); err != nil {
			fmt.Fprintf(os.Stderr, "wuu: tighten %s: %v\n", home, err)
		}
	}

	// Handle top-level -c and -r flags as shortcuts to wuu exec
	if args[0] == "-c" || args[0] == "--continue" {
		return runExec(args)
	}
	if args[0] == "-r" || args[0] == "--resume" {
		return runExec(args)
	}

	switch args[0] {
	case "init":
		return runInit(args[1:])
	case "login":
		return runLogin(args[1:])
	case "models":
		return runModels(args[1:])
	case "run":
		return runLegacyRun(args[1:])
	case "runs":
		return runExecutionRuns(args[1:])
	case "exec":
		return runExec(args[1:])
	case "probe-title":
		return runProbeTitle(args[1:])
	case "eval":
		return runEval(args[1:])
	case "tui":
		return errors.New("the TUI has been removed; use the desktop GUI or `wuu exec` for agent-friendly text tasks")
	case "session":
		return runSession(args[1:])
	case "skills":
		return runSkills(args[1:])
	case "plugin":
		return runPlugin(args[1:])
	case "session-show":
		return runSessionShow(args[1:])
	case "debug":
		return runDebug(args[1:])
	case "app-server":
		return runAppServer(args[1:])
	case "relay":
		return runRelay(args[1:])
	case "remote":
		return runRemote(args[1:])
	case "version", "-v", "--version":
		if args[0] == "version" {
			return runVersion(args[1:])
		}
		return runVersion(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
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

func runExecutionRuns(args []string) error {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output runs as JSON")
	allWorkspaces := fs.Bool("all-workspaces", false, "include runs from every workspace")
	workdir := fs.String("workdir", "", "workspace directory")
	status := fs.String("status", "", "filter by status")
	limit := fs.Int("limit", 50, "maximum number of runs")
	readID := ""
	parseArgs := args
	if len(args) > 0 && args[0] == "read" {
		if len(args) < 2 {
			return errors.New("usage: wuu runs read RUN_ID [flags]")
		}
		readID = args[1]
		parseArgs = args[2:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return err
	}
	if readID == "" && fs.NArg() == 2 && fs.Arg(0) == "read" {
		readID = fs.Arg(1)
	} else if fs.NArg() != 0 {
		return errors.New("usage: wuu runs [read RUN_ID] [flags]")
	}
	home, err := statepath.Home("")
	if err != nil {
		return err
	}
	store, err := execution.NewStore(statepath.SessionsDir(home))
	if err != nil {
		return err
	}
	if readID != "" {
		return runExecutionRead(store, readID, *jsonOutput)
	}
	var workspaceRoot string
	if !*allWorkspaces {
		workspaceRoot, err = resolveWorkdir(*workdir)
		if err != nil {
			return err
		}
	}
	runs, err := store.List(context.Background(), execution.ListOptions{
		WorkspaceRoot: workspaceRoot, Status: execution.Status(strings.TrimSpace(*status)), Limit: *limit,
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(map[string]any{"runs": runs})
	}
	for _, run := range runs {
		fmt.Printf("%s\t%s\t%s\t%s\n", run.ID, run.Status, run.ThreadID, run.UpdatedAt.Format(time.RFC3339))
	}
	return nil
}

func runExecutionRead(store *execution.Store, id string, jsonOutput bool) error {
	run, err := store.Get(context.Background(), strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if jsonOutput {
		return printJSON(run)
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	force := fs.Bool("force", false, "overwrite existing user config")
	if err := fs.Parse(args); err != nil {
		return err
	}

	configPath, err := statepath.ConfigPath(os.Getenv("HOME"))
	if err != nil {
		return fmt.Errorf("resolve user config: %w", err)
	}

	if !*force {
		if _, err := os.Stat(configPath); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", configPath)
		}
	}

	cfg := config.Default()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("create user config directory: %w", err)
	}
	if err := securefs.WriteFileAtomic(configPath, append(data, '\n')); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("created %s\n", configPath)
	return nil
}

func runProbeTitle(args []string) error {
	fs := flag.NewFlagSet("probe-title", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	workdir := fs.String("workdir", "", "workspace directory (default: cwd)")
	threadID := fs.String("thread", "", "thread id to regenerate title for (default: most recent)")
	userPrompt := fs.String("user-prompt", "", "synthetic first user message; probe runs in dry-run mode")
	providerName := fs.String("provider", "", "override provider name from config")
	modelOverride := fs.String("model", "", "override model from config")
	dryRun := fs.Bool("dry-run", false, "do not persist the title")
	verbose := fs.Bool("verbose", true, "print every step in human-readable mode")
	jsonOut := fs.Bool("json", false, "emit TitleGenerationResult as JSON")
	quiet := fs.Bool("quiet", false, "suppress human-readable summary; implies --json")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *quiet {
		*jsonOut = true
	}

	if *userPrompt == "" && *threadID == "" && *dryRun == false {
		// Default to dry-run for the implicit "use most recent thread" path
		// so an accidental invocation cannot overwrite a real title without
		// intent. Pass --dry-run=false to persist.
		*dryRun = true
	}

	rootDir, err := resolveWorkdir(*workdir)
	if err != nil {
		return err
	}
	homeDir := os.Getenv("HOME")

	opts := appserver.ProbeTitleOptions{
		WorkDir:       rootDir,
		HomeDir:       homeDir,
		ProviderName:  *providerName,
		ModelOverride: *modelOverride,
		ThreadID:      *threadID,
		UserPrompt:    *userPrompt,
		DryRun:        *dryRun,
		Verbose:       *verbose,
		JSON:          *jsonOut,
	}
	_, err = appserver.ProbeTitle(context.Background(), opts)
	if err != nil {
		// TitleGenerationResult is already pretty-printed or JSON-encoded by
		// ProbeTitle itself. We only need to surface the error to the shell.
		return err
	}
	return nil
}

func runModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	providerName := fs.String("provider", "", "provider name in config")
	workdir := fs.String("workdir", "", "workspace directory")
	jsonOutput := fs.Bool("json", false, "output model metadata as JSON")
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
	if !isCodexModelsProvider(providerCfg.Type) {
		return fmt.Errorf("provider %q uses type %q; live model lookup currently supports openai-codex providers only", resolvedName, providerCfg.Type)
	}

	client, err := codex.New(codex.ClientConfig{
		BaseURL:               providerCfg.BaseURL,
		APIKey:                explicitProviderAPIKey(providerCfg),
		Headers:               providerCfg.Headers,
		ReuseCodexCredentials: providerCfg.ReuseCodexCredentials,
	})
	if err != nil {
		return err
	}
	models, err := client.Models(context.Background())
	if err != nil {
		return err
	}
	if *jsonOutput {
		data, err := json.MarshalIndent(models, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal models: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("provider: %s\nconfig: %s\n\n", resolvedName, configPath)
	for _, model := range models {
		fmt.Println(model.Slug)
	}
	return nil
}

func runSessionShow(args []string) error {
	fs := flag.NewFlagSet("session-show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	threadID := fs.String("thread", "", "thread (session) ID; defaults to the most recent session")
	jsonOutput := fs.Bool("json", false, "output as JSON (default: human-readable)")
	limit := fs.Int("limit", 200, "max history records to print (ignored with --json)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	home, err := statepath.Home("")
	if err != nil {
		return fmt.Errorf("resolve wuu home: %w", err)
	}
	sessDir := statepath.SessionsDir(home)
	if sessDir == "" {
		return errors.New("sessions dir is empty")
	}

	id := strings.TrimSpace(*threadID)
	if id == "" {
		recent, err := session.MostRecent(sessDir)
		if err != nil {
			return fmt.Errorf("find most recent session: %w", err)
		}
		if recent == "" {
			return errors.New("no sessions found")
		}
		id = recent
	}

	meta, ok, err := session.Find(sessDir, id)
	if err != nil {
		return fmt.Errorf("lookup %q: %w", id, err)
	}
	if !ok {
		return fmt.Errorf("%w: %q", session.ErrSessionNotFound, id)
	}
	records, err := session.LoadHistoryRecords(sessDir, id, true)
	if err != nil {
		return fmt.Errorf("load history %q: %w", id, err)
	}

	if *jsonOutput {
		payload := map[string]any{
			"thread_id": id,
			"session":   meta,
			"history":   records,
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal session: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("thread_id: %s\n", id)
	fmt.Printf("title: %s\n", meta.Title)
	if meta.Summary != "" {
		fmt.Printf("summary: %s\n", meta.Summary)
	}
	if meta.CWD != "" {
		fmt.Printf("cwd: %s\n", meta.CWD)
	}
	fmt.Printf("created: %s\n", meta.CreatedAt.Format(time.RFC3339))
	if meta.UpdatedAt.After(meta.CreatedAt) {
		fmt.Printf("updated: %s\n", meta.UpdatedAt.Format(time.RFC3339))
	}
	if meta.PinnedAt != nil {
		fmt.Printf("pinned: %s\n", meta.PinnedAt.Format(time.RFC3339))
	}
	if meta.ArchivedAt != nil {
		fmt.Printf("archived: %s\n", meta.ArchivedAt.Format(time.RFC3339))
	}
	if meta.ForkedFromID != "" {
		fmt.Printf("forked_from: %s\n", meta.ForkedFromID)
	}
	fmt.Printf("entries: %d\n", meta.Entries)
	fmt.Printf("history_records: %d\n\n", len(records))

	shown := *limit
	if shown > 0 && shown < len(records) {
		fmt.Printf("(showing first %d of %d records; use --limit 0 for all or --json for full output)\n\n", shown, len(records))
	}
	for i, rec := range records {
		if shown > 0 && i >= shown {
			break
		}
		prefix := "  "
		if rec.Steered {
			prefix = "* "
		}
		role := rec.Role
		if role == "" {
			role = "?"
		}
		content := rec.Content
		if len(content) > 240 {
			content = content[:240] + "..."
		}
		fmt.Printf("%s[%d] %s: %s\n", prefix, i+1, role, content)
	}
	return nil
}

func runSkills(args []string) error {
	if len(args) == 0 {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("skills subcommand is required (available: lint)"))
	}
	switch args[0] {
	case "lint":
		return runSkillsLint(args[1:])
	default:
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, fmt.Errorf("unknown skills subcommand %q (available: lint)", args[0]))
	}
}

func runSkillsLint(args []string) error {
	fs := flag.NewFlagSet("skills lint", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output issues as JSON")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	paths := fs.Args()
	if len(paths) == 0 {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("skills lint requires at least one path (a skill directory, a skills root, or a flat .md file)"))
	}

	var issues []skills.LintIssue
	for _, path := range paths {
		found, err := skills.LintPath(path)
		if err != nil {
			return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, fmt.Errorf("lint %s: %w", path, err))
		}
		issues = append(issues, found...)
	}

	errorCount := 0
	for _, issue := range issues {
		if issue.Severity == skills.LintError {
			errorCount++
		}
	}

	if *jsonOutput {
		data, err := json.MarshalIndent(issues, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal lint issues: %w", err)
		}
		fmt.Println(string(data))
	} else {
		for _, issue := range issues {
			fmt.Printf("%s: %s: %s\n", issue.Severity, issue.Path, issue.Message)
		}
		fmt.Printf("%d issue(s): %d error(s), %d warning(s)\n", len(issues), errorCount, len(issues)-errorCount)
	}

	if errorCount > 0 {
		return wuuexec.WithExitCode(wuuexec.ExitTurnFailed, fmt.Errorf("skills lint found %d error(s)", errorCount))
	}
	return nil
}

func runSession(args []string) error {
	if len(args) == 0 {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("session subcommand is required"))
	}
	switch args[0] {
	case "list":
		return runSessionList(args[1:])
	case "show":
		return runSessionShowSubcommand(args[1:])
	case "trace":
		return runSessionTrace(args[1:])
	case "search":
		return runSessionSearch(args[1:])
	case "archive":
		return runSessionArchive(args[1:])
	case "delete":
		return runSessionDelete(args[1:])
	case "export":
		return runSessionExport(args[1:])
	default:
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, fmt.Errorf("unknown session subcommand %q", args[0]))
	}
}

func runSessionList(args []string) error {
	fs := flag.NewFlagSet("session list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output as JSON")
	limit := fs.Int("limit", 50, "max sessions to print")
	workdir := fs.String("workdir", "", "workspace directory")
	allWorkdirs := fs.Bool("all-workdirs", false, "list sessions from every workspace")
	includeArchived := fs.Bool("include-archived", false, "include archived sessions")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}

	sessDir, err := resolveSessionsDir()
	if err != nil {
		return err
	}
	var sessions []session.Session
	if *allWorkdirs {
		sessions, err = session.List(sessDir, *limit)
	} else {
		rootDir, werr := resolveWorkdir(*workdir)
		if werr != nil {
			return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, werr)
		}
		sessions, err = session.ListForCWD(sessDir, rootDir, "", *limit)
	}
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	sessions = filterArchivedSessions(sessions, *includeArchived)
	if *jsonOutput {
		return printJSON(map[string]any{"sessions": sessions})
	}
	for _, sess := range sessions {
		title := firstNonEmptyString(sess.Title, sess.Summary, sess.ID)
		fmt.Printf("%s\t%s\t%s\n", sess.ID, sess.UpdatedAt.Format(time.RFC3339), title)
	}
	return nil
}

func runSessionShowSubcommand(args []string) error {
	fs := flag.NewFlagSet("session show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output as JSON")
	limit := fs.Int("limit", 200, "max history records to print in human mode")
	threadFlag := fs.String("thread", "", "thread id; defaults to most recent for this workspace")
	last := fs.Bool("last", false, "show the most recent session for this workspace")
	workdir := fs.String("workdir", "", "workspace directory")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	id := strings.TrimSpace(*threadFlag)
	if !*last && id == "" && len(fs.Args()) > 0 {
		id = strings.TrimSpace(fs.Args()[0])
	}
	sessDir, err := resolveSessionsDir()
	if err != nil {
		return err
	}
	rootDir, err := resolveWorkdir(*workdir)
	if err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	return printSession(sessDir, id, rootDir, *jsonOutput, *limit)
}

func runSessionTrace(args []string) error {
	fs := flag.NewFlagSet("session trace", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output as JSON")
	threadFlag := fs.String("thread", "", "thread id; defaults to most recent for this workspace")
	last := fs.Bool("last", false, "trace the most recent session for this workspace")
	workdir := fs.String("workdir", "", "workspace directory")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	id := strings.TrimSpace(*threadFlag)
	if !*last && id == "" && len(fs.Args()) > 0 {
		id = strings.TrimSpace(fs.Args()[0])
	}
	sessDir, err := resolveSessionsDir()
	if err != nil {
		return err
	}
	rootDir, err := resolveWorkdir(*workdir)
	if err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	if id == "" {
		id, err = session.MostRecentForCWD(sessDir, rootDir, "")
		if err != nil {
			return fmt.Errorf("find most recent session: %w", err)
		}
		if id == "" {
			return errors.New("no sessions found")
		}
	}
	meta, ok, err := session.Find(sessDir, id)
	if err != nil {
		return fmt.Errorf("lookup %q: %w", id, err)
	}
	if !ok {
		return fmt.Errorf("%w: %q", session.ErrSessionNotFound, id)
	}
	tracePath, err := tracePathForSession(meta, rootDir)
	if err != nil {
		return err
	}
	summary, err := sessiontrace.ReplayTrace(tracePath)
	if err != nil {
		return fmt.Errorf("replay trace %q: %w", tracePath, err)
	}
	if *jsonOutput {
		return printJSON(map[string]any{
			"thread_id":  id,
			"trace_path": tracePath,
			"summary":    summary,
		})
	}
	fmt.Printf("thread_id: %s\ntrace_path: %s\n", id, tracePath)
	if summary.LatestTurn != nil {
		fmt.Printf("latest_status: %s\n", summary.LatestTurn.Status)
	}
	if summary.Final != nil {
		fmt.Printf("final_status: %s\n", summary.Final.Status)
	}
	fmt.Printf("events: %d\n", summary.EventCount)
	return nil
}

type sessionSearchResult struct {
	ThreadID string          `json:"thread_id"`
	Session  session.Session `json:"session"`
	Snippet  string          `json:"snippet,omitempty"`
}

func runSessionSearch(args []string) error {
	fs := flag.NewFlagSet("session search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output as JSON")
	limit := fs.Int("limit", 40, "max results to print")
	workdir := fs.String("workdir", "", "workspace directory")
	allWorkdirs := fs.Bool("all-workdirs", false, "search sessions from every workspace")
	includeArchived := fs.Bool("include-archived", false, "include archived sessions")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("search query is required"))
	}
	sessDir, err := resolveSessionsDir()
	if err != nil {
		return err
	}
	var sessions []session.Session
	if *allWorkdirs {
		sessions, err = session.List(sessDir, 0)
	} else {
		rootDir, werr := resolveWorkdir(*workdir)
		if werr != nil {
			return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, werr)
		}
		sessions, err = session.ListForCWD(sessDir, rootDir, "", 0)
	}
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	results, err := searchSessions(sessDir, filterArchivedSessions(sessions, *includeArchived), query, *limit)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(map[string]any{"query": query, "results": results})
	}
	for _, result := range results {
		title := firstNonEmptyString(result.Session.Title, result.Session.Summary, result.ThreadID)
		if result.Snippet != "" {
			fmt.Printf("%s\t%s\t%s\t%s\n", result.ThreadID, result.Session.UpdatedAt.Format(time.RFC3339), title, result.Snippet)
			continue
		}
		fmt.Printf("%s\t%s\t%s\n", result.ThreadID, result.Session.UpdatedAt.Format(time.RFC3339), title)
	}
	return nil
}

func runSessionArchive(args []string) error {
	fs := flag.NewFlagSet("session archive", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output as JSON")
	unarchive := fs.Bool("unarchive", false, "mark session as active instead of archived")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	if len(fs.Args()) == 0 || strings.TrimSpace(fs.Args()[0]) == "" {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("thread id is required"))
	}
	threadID := strings.TrimSpace(fs.Args()[0])
	sessDir, err := resolveSessionsDir()
	if err != nil {
		return err
	}
	meta, err := session.UpdateArchived(sessDir, threadID, !*unarchive)
	if err != nil {
		return fmt.Errorf("archive %q: %w", threadID, err)
	}
	if *jsonOutput {
		return printJSON(map[string]any{
			"thread_id": threadID,
			"session":   meta,
			"archived":  meta.ArchivedAt != nil,
		})
	}
	if meta.ArchivedAt != nil {
		fmt.Printf("archived: %s\n", threadID)
		return nil
	}
	fmt.Printf("unarchived: %s\n", threadID)
	return nil
}

func runSessionDelete(args []string) error {
	fs := flag.NewFlagSet("session delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output as JSON")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	if len(fs.Args()) == 0 || strings.TrimSpace(fs.Args()[0]) == "" {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("thread id is required"))
	}
	threadID := strings.TrimSpace(fs.Args()[0])
	sessDir, err := resolveSessionsDir()
	if err != nil {
		return err
	}
	deleted, err := session.Delete(sessDir, threadID)
	if err != nil {
		return fmt.Errorf("delete %q: %w", threadID, err)
	}
	artifactPath, artifactsDeleted, err := deleteSessionArtifacts(deleted)
	if err != nil {
		return fmt.Errorf("delete artifacts for %q: %w", threadID, err)
	}
	if *jsonOutput {
		return printJSON(map[string]any{
			"thread_id":         threadID,
			"session":           deleted,
			"deleted":           true,
			"artifact_path":     artifactPath,
			"artifacts_deleted": artifactsDeleted,
		})
	}
	if artifactPath != "" && artifactsDeleted {
		fmt.Printf("deleted: %s\nartifacts: %s\n", threadID, artifactPath)
		return nil
	}
	fmt.Printf("deleted: %s\n", threadID)
	return nil
}

func runSessionExport(args []string) error {
	fs := flag.NewFlagSet("session export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output metadata as JSON wrapper instead of JSONL")
	threadFlag := fs.String("thread", "", "thread id; defaults to most recent for this workspace")
	last := fs.Bool("last", false, "export the most recent session for this workspace")
	outFile := fs.String("out", "", "output file; defaults to stdout")
	workdir := fs.String("workdir", "", "workspace directory")

	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}

	id := strings.TrimSpace(*threadFlag)
	if !*last && id == "" && len(fs.Args()) > 0 {
		id = strings.TrimSpace(fs.Args()[0])
	}

	sessDir, err := resolveSessionsDir()
	if err != nil {
		return err
	}

	rootDir, err := resolveWorkdir(*workdir)
	if err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}

	// Find the session ID
	if id == "" {
		id, err = session.MostRecentForCWD(sessDir, rootDir, "")
		if err != nil {
			return fmt.Errorf("find most recent session: %w", err)
		}
		if id == "" {
			return errors.New("no sessions found")
		}
	}

	// Load session metadata and history
	meta, ok, err := session.Find(sessDir, id)
	if err != nil {
		return fmt.Errorf("lookup %q: %w", id, err)
	}
	if !ok {
		return fmt.Errorf("%w: %q", session.ErrSessionNotFound, id)
	}

	records, err := session.LoadHistoryRecords(sessDir, id, true)
	if err != nil {
		return fmt.Errorf("load history %q: %w", id, err)
	}

	// Prepare output
	var output *os.File
	if *outFile == "" {
		output = os.Stdout
	} else {
		outFile := *outFile
		f, err := os.Create(outFile)
		if err != nil {
			return fmt.Errorf("create output file %q: %w", outFile, err)
		}
		defer f.Close()
		output = f
	}

	writer := bufio.NewWriter(output)
	defer writer.Flush()

	if *jsonOutput {
		// Wrapper format with metadata
		data := map[string]any{
			"type":      "session",
			"thread_id": id,
			"session":   meta,
			"history":   records,
		}
		jsonBytes, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("marshal session: %w", err)
		}
		if _, err := writer.Write(jsonBytes); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
		if _, err := writer.WriteString("\n"); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	} else {
		// JSONL format: one record per line

		// First line: session metadata header
		sessionHeader := map[string]any{
			"type":      "session",
			"thread_id": id,
			"session":   meta,
		}
		headerBytes, err := json.Marshal(sessionHeader)
		if err != nil {
			return fmt.Errorf("marshal session header: %w", err)
		}
		if _, err := writer.Write(headerBytes); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
		if _, err := writer.WriteString("\n"); err != nil {
			return fmt.Errorf("write output: %w", err)
		}

		// Write each history record as a separate JSON line
		for _, rec := range records {
			recBytes, err := json.Marshal(rec)
			if err != nil {
				return fmt.Errorf("marshal history record: %w", err)
			}
			if _, err := writer.Write(recBytes); err != nil {
				return fmt.Errorf("write output: %w", err)
			}
			if _, err := writer.WriteString("\n"); err != nil {
				return fmt.Errorf("write output: %w", err)
			}
		}
	}

	return nil
}

func deleteSessionArtifacts(sess session.Session) (string, bool, error) {
	if strings.TrimSpace(sess.CWD) == "" {
		return "", false, nil
	}
	home, err := statepath.Home("")
	if err != nil {
		return "", false, fmt.Errorf("resolve wuu home: %w", err)
	}
	workspaceStateDir, err := sessionWorkspaceStateDir(home, sess.WorkspaceID, sess.CWD)
	if err != nil {
		return "", false, fmt.Errorf("resolve workspace state: %w", err)
	}
	artifactPath := statepath.SessionArtifactDir(workspaceStateDir, sess.ID)
	if _, err := os.Stat(artifactPath); err != nil {
		if os.IsNotExist(err) {
			return artifactPath, false, nil
		}
		return artifactPath, false, err
	}
	if err := os.RemoveAll(artifactPath); err != nil {
		return artifactPath, false, err
	}
	return artifactPath, true, nil
}

func searchSessions(sessDir string, sessions []session.Session, query string, limit int) ([]sessionSearchResult, error) {
	normalizedQuery := normalizeSessionSearchText(query)
	if normalizedQuery == "" {
		return nil, nil
	}
	results := make([]sessionSearchResult, 0, minPositive(limit, len(sessions)))
	for _, sess := range sessions {
		snippet, err := sessionSearchSnippet(sessDir, sess, normalizedQuery)
		if err != nil {
			return nil, err
		}
		if snippet == "" {
			continue
		}
		results = append(results, sessionSearchResult{
			ThreadID: sess.ID,
			Session:  sess,
			Snippet:  snippet,
		})
		if limit > 0 && len(results) >= limit {
			break
		}
	}
	return results, nil
}

func sessionSearchSnippet(sessDir string, sess session.Session, normalizedQuery string) (string, error) {
	for _, candidate := range []string{sess.Title, sess.Summary, sess.ID, sess.CWD} {
		if sessionSearchMatches(candidate, normalizedQuery) {
			return compactSessionSearchSnippet(candidate), nil
		}
	}
	records, err := session.LoadHistoryRecords(sessDir, sess.ID, true)
	if err != nil {
		return "", fmt.Errorf("load history %q: %w", sess.ID, err)
	}
	for _, rec := range records {
		for _, candidate := range []string{rec.Content, rec.Name, rec.ToolCallID} {
			if sessionSearchMatches(candidate, normalizedQuery) {
				return compactSessionSearchSnippet(candidate), nil
			}
		}
	}
	return "", nil
}

func sessionSearchMatches(value, normalizedQuery string) bool {
	return strings.Contains(normalizeSessionSearchText(value), normalizedQuery)
}

func normalizeSessionSearchText(value string) string {
	return strings.ToLower(compactSessionSearchSnippet(value))
}

func compactSessionSearchSnippet(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func minPositive(a, b int) int {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}

func printSession(sessDir, id, rootDir string, jsonOutput bool, limit int) error {
	var err error
	id = strings.TrimSpace(id)
	if id == "" {
		id, err = session.MostRecentForCWD(sessDir, rootDir, "")
		if err != nil {
			return fmt.Errorf("find most recent session: %w", err)
		}
		if id == "" {
			return errors.New("no sessions found")
		}
	}

	meta, ok, err := session.Find(sessDir, id)
	if err != nil {
		return fmt.Errorf("lookup %q: %w", id, err)
	}
	if !ok {
		return fmt.Errorf("%w: %q", session.ErrSessionNotFound, id)
	}
	records, err := session.LoadHistoryRecords(sessDir, id, true)
	if err != nil {
		return fmt.Errorf("load history %q: %w", id, err)
	}
	if jsonOutput {
		return printJSON(map[string]any{
			"thread_id": id,
			"session":   meta,
			"history":   records,
		})
	}

	fmt.Printf("thread_id: %s\n", id)
	fmt.Printf("title: %s\n", meta.Title)
	if meta.Summary != "" {
		fmt.Printf("summary: %s\n", meta.Summary)
	}
	if meta.CWD != "" {
		fmt.Printf("cwd: %s\n", meta.CWD)
	}
	fmt.Printf("created: %s\n", meta.CreatedAt.Format(time.RFC3339))
	if meta.UpdatedAt.After(meta.CreatedAt) {
		fmt.Printf("updated: %s\n", meta.UpdatedAt.Format(time.RFC3339))
	}
	fmt.Printf("entries: %d\nhistory_records: %d\n\n", meta.Entries, len(records))
	shown := limit
	if shown > 0 && shown < len(records) {
		fmt.Printf("(showing first %d of %d records; use --limit 0 for all or --json for full output)\n\n", shown, len(records))
	}
	for i, rec := range records {
		if shown > 0 && i >= shown {
			break
		}
		role := firstNonEmptyString(rec.Role, "?")
		content := rec.Content
		if len(content) > 240 {
			content = content[:240] + "..."
		}
		fmt.Printf("  [%d] %s: %s\n", i+1, role, content)
	}
	return nil
}

func resolveSessionsDir() (string, error) {
	home, err := statepath.Home("")
	if err != nil {
		return "", fmt.Errorf("resolve wuu home: %w", err)
	}
	sessDir := statepath.SessionsDir(home)
	if sessDir == "" {
		return "", errors.New("sessions dir is empty")
	}
	return sessDir, nil
}

func filterArchivedSessions(sessions []session.Session, includeArchived bool) []session.Session {
	if includeArchived {
		return sessions
	}
	filtered := sessions[:0]
	for _, sess := range sessions {
		if sess.ArchivedAt == nil {
			filtered = append(filtered, sess)
		}
	}
	return filtered
}

func tracePathForSession(sess session.Session, fallbackRoot string) (string, error) {
	rootDir := strings.TrimSpace(sess.CWD)
	if rootDir == "" {
		rootDir = fallbackRoot
	}
	if rootDir == "" {
		return "", errors.New("session has no workspace cwd; pass --workdir")
	}
	home, err := statepath.Home("")
	if err != nil {
		return "", fmt.Errorf("resolve wuu home: %w", err)
	}
	workspaceStateDir, err := sessionWorkspaceStateDir(home, sess.WorkspaceID, rootDir)
	if err != nil {
		return "", fmt.Errorf("resolve workspace state: %w", err)
	}
	return sessiontrace.Path(statepath.SessionArtifactDir(workspaceStateDir, sess.ID)), nil
}

// sessionWorkspaceStateDir resolves a session's workspace state directory,
// preferring its stable workspace id (so it survives the project moving on
// disk) and falling back to the recorded cwd for id-less sessions.
func sessionWorkspaceStateDir(home, workspaceID, cwd string) (string, error) {
	if id := strings.TrimSpace(workspaceID); id != "" {
		return statepath.WorkspaceDirByID(home, id)
	}
	return statepath.WorkspaceDir(home, cwd)
}

func printJSON(payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type debugAppServerClient interface {
	Call(context.Context, string, any, any) error
	Notifications() <-chan wuuexec.Notification
	SandboxDir() string
	Shutdown(context.Context) error
}

var debugAppServerClientOverride func(context.Context, debugAppServerOptions) (debugAppServerClient, error)

type debugAppServerOptions struct {
	workdir     string
	provider    string
	model       string
	driver      string
	noTools     bool
	sandbox     bool
	sandboxName string
	keepSandbox bool
}

type debugAppServerCLIConfig struct {
	workdir  *string
	provider *string
	model    *string
	driver   *string
	noTools  *bool
}

func runDebug(args []string) error {
	if len(args) == 0 {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("debug subcommand is required"))
	}
	switch args[0] {
	case "app-server":
		return runDebugAppServer(args[1:])
	case "channel":
		return runDebugChannel(args[1:])
	case "sandbox":
		return runDebugSandbox(args[1:])
	case "protocol":
		return runDebugProtocol(args[1:])
	default:
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, fmt.Errorf("unknown debug subcommand %q", args[0]))
	}
}

func runDebugAppServer(args []string) error {
	if len(args) == 0 {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("debug app-server subcommand is required"))
	}
	switch args[0] {
	case "initialize":
		return runDebugAppServerInitialize(args[1:])
	case "send":
		return runDebugAppServerSend(args[1:])
	case "registry":
		return runDebugAppServerRegistry(args[1:])
	case "executions":
		return runDebugAppServerExecutions(args[1:])
	default:
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, fmt.Errorf("unknown debug app-server subcommand %q", args[0]))
	}
}

func runDebugAppServerInitialize(args []string) error {
	fs := flag.NewFlagSet("debug app-server initialize", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addDebugAppServerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	client, err := newDebugAppServerClient(context.Background(), debugAppServerOptionsFromCLI(cfg))
	if err != nil {
		return err
	}
	defer shutdownDebugClient(client)

	var result json.RawMessage
	if err := client.Call(context.Background(), appserver.MethodInitialize, nil, &result); err != nil {
		return err
	}
	return printRawJSON(result)
}

func runDebugAppServerRegistry(args []string) error {
	fs := flag.NewFlagSet("debug app-server registry", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addDebugAppServerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	client, err := newDebugAppServerClient(context.Background(), debugAppServerOptionsFromCLI(cfg))
	if err != nil {
		return err
	}
	defer shutdownDebugClient(client)

	var result json.RawMessage
	if err := client.Call(context.Background(), appserver.MethodPluginRegistryIntrospect, nil, &result); err != nil {
		return err
	}
	return printRawJSON(result)
}

func runDebugAppServerExecutions(args []string) error {
	fs := flag.NewFlagSet("debug app-server executions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addDebugAppServerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	client, err := newDebugAppServerClient(context.Background(), debugAppServerOptionsFromCLI(cfg))
	if err != nil {
		return err
	}
	defer shutdownDebugClient(client)

	var result json.RawMessage
	if err := client.Call(context.Background(), appserver.MethodPluginExecutionsList, nil, &result); err != nil {
		return err
	}
	return printRawJSON(result)
}

func runDebugAppServerSend(args []string) error {
	fs := flag.NewFlagSet("debug app-server send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addDebugAppServerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	remaining := fs.Args()
	if len(remaining) == 0 || strings.TrimSpace(remaining[0]) == "" {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("method is required"))
	}
	method := strings.TrimSpace(remaining[0])
	if method == appserver.MethodChannelMessageSend {
		if agentID := strings.TrimSpace(os.Getenv(channels.NamedAgentIDEnv)); agentID != "" {
			return wuuexec.WithExitCode(
				wuuexec.ExitInvalidInput,
				fmt.Errorf("%s is human-only and cannot send as named agent %q; use chat_send so the message keeps the agent identity", method, agentID),
			)
		}
	}
	params, err := parseDebugJSONParams(remaining[1:])
	if err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	client, err := newDebugAppServerClient(context.Background(), debugAppServerOptionsFromCLI(cfg))
	if err != nil {
		return err
	}
	defer shutdownDebugClient(client)

	var result json.RawMessage
	if err := client.Call(context.Background(), method, params, &result); err != nil {
		return err
	}
	return printRawJSON(result)
}

func addDebugAppServerFlags(fs *flag.FlagSet) debugAppServerCLIConfig {
	return debugAppServerCLIConfig{
		workdir:  fs.String("workdir", "", "workspace directory"),
		provider: fs.String("provider", "", "provider name in config"),
		model:    fs.String("model", "", "model override"),
		driver:   fs.String("driver", "", "loop driver profile provided as a plugin service (empty keeps the default driver)"),
		noTools:  fs.Bool("no-tools", false, "disable local tools"),
	}
}

func debugAppServerOptionsFromCLI(cfg debugAppServerCLIConfig) debugAppServerOptions {
	return debugAppServerOptions{
		workdir:  valueOfStringFlag(cfg.workdir),
		provider: valueOfStringFlag(cfg.provider),
		model:    valueOfStringFlag(cfg.model),
		driver:   valueOfStringFlag(cfg.driver),
		noTools:  valueOfBoolFlag(cfg.noTools),
	}
}

func newDebugAppServerClient(ctx context.Context, opts debugAppServerOptions) (debugAppServerClient, error) {
	if debugAppServerClientOverride != nil {
		return debugAppServerClientOverride(ctx, opts)
	}
	return newLocalDebugAppServerClient(ctx, opts)
}

type localDebugAppServerClient struct {
	rt             *runtime.Session
	client         *wuuexec.ProtocolClient
	cancel         context.CancelFunc
	done           chan error
	pipes          []io.Closer
	sandboxDir     string
	sandboxCleanup func()
}

var debugSandboxMu sync.Mutex

func newLocalDebugAppServerClient(ctx context.Context, opts debugAppServerOptions) (*localDebugAppServerClient, error) {
	rootDir, err := resolveWorkdir(opts.workdir)
	if err != nil {
		return nil, wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	homeDir := os.Getenv("HOME")
	cfg, configPath, err := loadOrCreateAppServerConfig(rootDir, homeDir)
	if err != nil {
		return nil, err
	}
	var sandboxDir string
	var sandboxCleanup func()
	if opts.sandbox || strings.TrimSpace(opts.sandboxName) != "" {
		realWuuHome, homeErr := statepath.Home(homeDir)
		if homeErr != nil {
			return nil, homeErr
		}
		hydrateDebugSandboxCredentials(&cfg, homeDir)
		sandboxDir, sandboxCleanup, err = activateDebugSandbox(realWuuHome, opts.sandboxName, opts.keepSandbox)
		if err != nil {
			return nil, err
		}
	}
	rt, err := runtime.NewSession(runtime.Options{
		RootDir:       rootDir,
		HomeDir:       homeDir,
		ConfigPath:    configPath,
		Config:        cfg,
		ProviderName:  opts.provider,
		ModelOverride: opts.model,
		DriverProfile: opts.driver,
		NoTools:       opts.noTools,
	})
	if err != nil {
		if sandboxCleanup != nil {
			sandboxCleanup()
		}
		return nil, err
	}
	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()
	serverCtx, cancel := context.WithCancel(ctx)
	client := &localDebugAppServerClient{
		rt:             rt,
		client:         wuuexec.NewProtocolClient(serverOutR, serverInW),
		cancel:         cancel,
		done:           make(chan error, 1),
		pipes:          []io.Closer{serverInR, serverInW, serverOutR, serverOutW},
		sandboxDir:     sandboxDir,
		sandboxCleanup: sandboxCleanup,
	}
	go func() {
		client.done <- appserver.RunStdio(serverCtx, rt, serverInR, serverOutW)
	}()
	return client, nil
}

// hydrateDebugSandboxCredentials carries credentials into the in-process
// runtime only. The sandbox remains free of credential files, while provider
// clients built after WUU_HOME switches still use the user's configured auth.
func hydrateDebugSandboxCredentials(cfg *config.Config, home string) {
	if cfg == nil {
		return
	}
	store, err := authstorage.ForHome(home)
	if err != nil {
		return
	}
	file, err := store.Load()
	if err != nil {
		return
	}
	for name, provider := range cfg.Providers {
		credentials, ok := file.Providers[name]
		if !ok {
			continue
		}
		if strings.TrimSpace(provider.APIKey) == "" {
			provider.APIKey = strings.TrimSpace(credentials.APIKey)
		}
		if strings.TrimSpace(provider.AuthToken) == "" {
			provider.AuthToken = strings.TrimSpace(credentials.AuthToken)
		}
		cfg.Providers[name] = provider
	}
}

func (c *localDebugAppServerClient) Call(ctx context.Context, method string, params any, result any) error {
	return c.client.Call(ctx, method, params, result)
}

func (c *localDebugAppServerClient) Notifications() <-chan wuuexec.Notification {
	return c.client.Notifications()
}

func (c *localDebugAppServerClient) SandboxDir() string {
	return c.sandboxDir
}

func (c *localDebugAppServerClient) Shutdown(ctx context.Context) error {
	if c.sandboxCleanup != nil {
		defer c.sandboxCleanup()
		c.sandboxCleanup = nil
	}
	if c.cancel != nil {
		defer c.cancel()
	}
	var result appserver.OKResult
	err := c.client.Call(ctx, appserver.MethodShutdown, nil, &result)
	for _, pipe := range c.pipes {
		_ = pipe.Close()
	}
	if c.done != nil {
		select {
		case runErr := <-c.done:
			if err == nil && runErr != nil && !errors.Is(runErr, io.ErrClosedPipe) {
				err = runErr
			}
		case <-ctx.Done():
			// The run loop is still live, so cleaning up the shared runtime
			// underneath it would race with in-flight turns; process teardown
			// reclaims those resources instead.
			return errors.Join(err, fmt.Errorf("app server run loop did not exit before shutdown deadline: %w", ctx.Err()))
		}
	}
	// RunStdio performs a synchronous Server.Close. Runtime cleanup must run
	// afterwards because active turns and worker terminal finalizers still use
	// session-scoped stores, MCP clients, and process ownership while closing.
	if c.rt != nil {
		_, _ = c.rt.Cleanup()
	}
	return err
}

func shutdownDebugClient(client debugAppServerClient) {
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = client.Shutdown(ctx)
}

func parseDebugJSONParams(args []string) (json.RawMessage, error) {
	if len(args) == 0 {
		return nil, nil
	}
	raw := strings.TrimSpace(strings.Join(args, " "))
	if raw == "" {
		return nil, nil
	}
	var params json.RawMessage
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("parse params JSON: %w", err)
	}
	return params, nil
}

func runDebugProtocol(args []string) error {
	if len(args) == 0 {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("debug protocol subcommand is required"))
	}
	switch args[0] {
	case "events":
		return runDebugProtocolEvents(args[1:])
	default:
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, fmt.Errorf("unknown debug protocol subcommand %q", args[0]))
	}
}

func runDebugProtocolEvents(args []string) error {
	fs := flag.NewFlagSet("debug protocol events", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output as one JSON object")
	workdir := fs.String("workdir", "", "workspace directory")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	remaining := fs.Args()
	if len(remaining) == 0 || strings.TrimSpace(remaining[0]) == "" {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("thread id is required"))
	}
	threadID := strings.TrimSpace(remaining[0])
	sessDir, err := resolveSessionsDir()
	if err != nil {
		return err
	}
	rootDir, err := resolveWorkdir(*workdir)
	if err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	meta, ok, err := session.Find(sessDir, threadID)
	if err != nil {
		return fmt.Errorf("lookup %q: %w", threadID, err)
	}
	if !ok {
		return fmt.Errorf("%w: %q", session.ErrSessionNotFound, threadID)
	}
	tracePath, err := tracePathForSession(meta, rootDir)
	if err != nil {
		return err
	}
	events, err := readTraceEvents(tracePath)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(map[string]any{
			"thread_id":  threadID,
			"trace_path": tracePath,
			"events":     events,
		})
	}
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshal trace event: %w", err)
		}
		fmt.Println(string(data))
	}
	return nil
}

func readTraceEvents(path string) ([]json.RawMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open trace %q: %w", path, err)
	}
	defer file.Close()

	var events []json.RawMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var event json.RawMessage
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, fmt.Errorf("decode trace line %d: %w", line, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read trace %q: %w", path, err)
	}
	return events, nil
}

func printRawJSON(raw json.RawMessage) error {
	if len(raw) == 0 {
		fmt.Println("null")
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode JSON result: %w", err)
	}
	return printJSON(value)
}

type execCLIConfig struct {
	provider          *string
	model             *string
	effort            *string
	variant           *string
	permissionMode    *string
	workdir           *string
	configPath        *string
	agentProfile      *string
	ignoreUserConfig  *bool
	env               *stringListFlag
	files             *stringListFlag
	images            *stringListFlag
	imageOriginal     *bool
	noTools           *bool
	jsonOutput        *bool
	timeout           *time.Duration
	ephemeral         *bool
	outputLastMessage *string
	inputJSON         *bool
	maxTurns          *int
	outputSchema      *string
}

var execControllerOverride wuuexec.Controller

func runLegacyRun(args []string) error {
	if flagName, ok := firstLegacyRunOnlyFlag(args); ok {
		return wuuexec.WithExitCode(
			wuuexec.ExitInvalidInput,
			fmt.Errorf("wuu run is now a compatibility wrapper around wuu exec; %s is not supported by the app-server path", flagName),
		)
	}
	return runExec(args)
}

func firstLegacyRunOnlyFlag(args []string) (string, bool) {
	for _, arg := range args {
		if arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-") {
			return "", false
		}
		name := strings.TrimLeft(arg, "-")
		if name == "" {
			return "", false
		}
		name, _, _ = strings.Cut(name, "=")
		switch name {
		case "max-steps", "temperature", "system-prompt":
			return "--" + name, true
		}
	}
	return "", false
}

func runExec(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "resume":
			return runExecResume(args[1:])
		case "fork":
			return runExecFork(args[1:])
		case "review":
			return runExecReview(args[1:])
		case "-c", "--continue":
			// Alias for resume --last
			return runExecResume(append([]string{"--last"}, args[1:]...))
		case "-r", "--resume":
			// Alias for resume THREAD_ID or show sessions if no ID
			if len(args) == 1 {
				// Bare -r: show available sessions
				return runExecShowSessions()
			}
			// -r THREAD_ID or further args
			return runExecResume(args[1:])
		}
	}

	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addExecFlags(fs)
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	if err := validateExecFlags(cfg); err != nil {
		return err
	}
	return runExecWithInput(cfg, fs.Args(), "", false, "")
}

func runExecResume(args []string) error {
	fs := flag.NewFlagSet("exec resume", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addExecFlags(fs)
	last := fs.Bool("last", false, "resume the most recent session for this workspace")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	if err := validateExecFlags(cfg); err != nil {
		return err
	}

	remaining := fs.Args()
	threadID := ""
	if !*last {
		if len(remaining) == 0 {
			return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("resume requires --last or a thread id"))
		}
		threadID = strings.TrimSpace(remaining[0])
		remaining = remaining[1:]
	}
	return runExecWithInput(cfg, remaining, threadID, *last, "")
}

func runExecFork(args []string) error {
	fs := flag.NewFlagSet("exec fork", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addExecFlags(fs)
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	if err := validateExecFlags(cfg); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("fork requires a thread id"))
	}
	forkID := strings.TrimSpace(remaining[0])
	if forkID == "" {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("fork requires a thread id"))
	}
	return runExecWithInput(cfg, remaining[1:], "", false, forkID)
}

func runExecReview(args []string) error {
	fs := flag.NewFlagSet("exec review", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addExecFlags(fs)
	uncommitted := fs.Bool("uncommitted", false, "review current uncommitted changes")
	base := fs.String("base", "", "review changes against base ref")
	commit := fs.String("commit", "", "review one commit")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	if err := validateExecFlags(cfg); err != nil {
		return err
	}
	if valueOfBoolFlag(cfg.inputJSON) {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("wuu exec review does not support --input-json"))
	}
	prompt, err := reviewPromptFromFlags(*uncommitted, *base, *commit, fs.Args())
	if err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	opts, err := execOptionsFromCLI(cfg, prompt, "", false, nil)
	if err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	ctx, stop := execContext(opts.Timeout)
	defer stop()
	return wuuexec.Run(ctx, opts)
}

func runExecShowSessions() error {
	sessDir, err := resolveSessionsDir()
	if err != nil {
		return err
	}
	rootDir, err := resolveWorkdir("")
	if err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	sessions, err := session.ListForCWD(sessDir, rootDir, "", 50)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	sessions = filterArchivedSessions(sessions, false)
	if len(sessions) == 0 {
		fmt.Fprintf(os.Stderr, "no sessions found for current workspace\n")
		fmt.Fprintf(os.Stderr, "usage: wuu exec -r THREAD_ID [prompt...]\n")
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("no sessions found"))
	}
	fmt.Fprintf(os.Stderr, "available sessions for current workspace:\n\n")
	for i, sess := range sessions {
		title := firstNonEmptyString(sess.Title, sess.Summary, sess.ID)
		fmt.Fprintf(os.Stderr, "%d. %s\n   %s\t%s\n\n", i+1, sess.ID, sess.UpdatedAt.Format(time.RFC3339), title)
	}
	fmt.Fprintf(os.Stderr, "usage: wuu exec -r <THREAD_ID> [prompt...]\n")
	return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("thread id required"))
}

func reviewPromptFromFlags(uncommitted bool, base, commit string, extraArgs []string) (string, error) {
	base = strings.TrimSpace(base)
	commit = strings.TrimSpace(commit)
	selected := 0
	if uncommitted {
		selected++
	}
	if base != "" {
		selected++
	}
	if commit != "" {
		selected++
	}
	if selected != 1 {
		return "", errors.New("review requires exactly one of --uncommitted, --base, or --commit")
	}

	var prompt string
	switch {
	case uncommitted:
		prompt = strings.Join([]string{
			"Review the current uncommitted changes in this repository.",
			"Inspect the repository status and current diff using the tools available under the active model surface before reporting findings.",
		}, "\n")
	case base != "":
		prompt = fmt.Sprintf("Review the changes in this repository against base ref `%s`.\nInspect the merge-base diff using the tools available under the active model surface before reporting findings.", base)
	case commit != "":
		prompt = fmt.Sprintf("Review commit `%s` in this repository.\nInspect the commit details using the tools available under the active model surface before reporting findings.", commit)
	}
	prompt += "\n\nFocus only on real bugs, security issues, behavior regressions, and missing tests that matter. Do not nitpick style."

	if extra := strings.TrimSpace(strings.Join(extraArgs, " ")); extra != "" {
		prompt += "\n\nAdditional instructions:\n" + extra
	}
	return prompt, nil
}

func addExecFlags(fs *flag.FlagSet) execCLIConfig {
	files := stringListFlag{}
	images := stringListFlag{}
	env := stringListFlag{}
	fs.Var(&files, "file", "attach a local PDF file (repeatable)")
	fs.Var(&images, "image", "attach a local image (repeatable)")
	imageOriginal := fs.Bool("image-original", false, "send --image attachments at original resolution without resizing (Codex ImageDetail::Original equivalent)")
	fs.Var(&env, "env", "set an environment variable for the run (KEY=VALUE, repeatable)")
	return execCLIConfig{
		provider:          fs.String("provider", "", "provider name in config"),
		model:             fs.String("model", "", "model override"),
		effort:            fs.String("effort", "", "reasoning effort override"),
		variant:           fs.String("variant", "", "model variant override"),
		permissionMode:    fs.String("permission-mode", "", "permission mode override"),
		workdir:           fs.String("workdir", "", "workspace directory"),
		configPath:        fs.String("config", "", "trust one explicit config file path"),
		agentProfile:      fs.String("profile", "", "agent profile name"),
		ignoreUserConfig:  fs.Bool("ignore-user-config", false, "trust project config and ignore user config"),
		env:               &env,
		files:             &files,
		images:            &images,
		imageOriginal:     imageOriginal,
		noTools:           fs.Bool("no-tools", false, "disable local tools"),
		jsonOutput:        fs.Bool("json", false, "emit machine-readable JSONL to stdout"),
		timeout:           fs.Duration("timeout", 0, "total timeout (e.g. 20m)"),
		ephemeral:         fs.Bool("ephemeral", false, "run without creating a persistent session"),
		outputLastMessage: fs.String("output-last-message", "", "write final agent message to a file"),
		inputJSON:         fs.Bool("input-json", false, "read machine input JSON from stdin"),
		maxTurns:          fs.Int("max-turns", 0, "max agent turns"),
		outputSchema:      fs.String("output-schema", "", "JSON schema for structured final output"),
	}
}

func validateExecFlags(cfg execCLIConfig) error {
	for _, path := range append(stringListValues(cfg.files), stringListValues(cfg.images)...) {
		if strings.TrimSpace(path) == "" {
			return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("attachment path is required"))
		}
	}
	if cfg.maxTurns != nil && *cfg.maxTurns < 0 {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("wuu exec --max-turns must be non-negative"))
	}
	if cfg.permissionMode != nil {
		mode := strings.TrimSpace(*cfg.permissionMode)
		switch mode {
		case "", config.PermissionModeStandard, config.PermissionModeReadOnly, config.PermissionModeUnconfined:
		default:
			return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, fmt.Errorf("invalid --permission-mode %q: must be standard, read_only, or unconfined", mode))
		}
	}
	return nil
}

type execInputPayload struct {
	Prompt            string                     `json:"prompt"`
	Stdin             string                     `json:"stdin"`
	Files             []string                   `json:"files"`
	Images            []string                   `json:"images"`
	FileAttachments   []appserver.TurnStartFile  `json:"file_attachments"`
	ImageAttachments  []appserver.TurnStartImage `json:"image_attachments"`
	Workdir           string                     `json:"workdir"`
	Provider          string                     `json:"provider"`
	Model             string                     `json:"model"`
	Effort            string                     `json:"effort"`
	Variant           string                     `json:"variant"`
	PermissionMode    string                     `json:"permission_mode"`
	ConfigPath        string                     `json:"config"`
	AgentProfile      string                     `json:"profile"`
	IgnoreUserConfig  *bool                      `json:"ignore_user_config"`
	Env               []string                   `json:"env"`
	MaxTurns          *int                       `json:"max_turns"`
	NoTools           *bool                      `json:"no_tools"`
	JSON              *bool                      `json:"json"`
	Ephemeral         *bool                      `json:"ephemeral"`
	Timeout           string                     `json:"timeout"`
	OutputLastMessage string                     `json:"output_last_message"`
	OutputSchema      string                     `json:"output_schema"`
}

func execOptionsFromCLI(cfg execCLIConfig, prompt, resumeID string, resumeLast bool, input *execInputPayload) (wuuexec.Options, error) {
	opts := wuuexec.Options{
		Prompt:            prompt,
		ImagePaths:        stringListValues(cfg.images),
		ImageOriginal:     valueOfBoolFlag(cfg.imageOriginal),
		FilePaths:         stringListValues(cfg.files),
		Provider:          valueOfStringFlag(cfg.provider),
		Model:             valueOfStringFlag(cfg.model),
		Effort:            valueOfStringFlag(cfg.effort),
		Variant:           valueOfStringFlag(cfg.variant),
		PermissionMode:    valueOfStringFlag(cfg.permissionMode),
		Workdir:           valueOfStringFlag(cfg.workdir),
		ConfigPath:        valueOfStringFlag(cfg.configPath),
		AgentProfile:      valueOfStringFlag(cfg.agentProfile),
		IgnoreUserConfig:  valueOfBoolFlag(cfg.ignoreUserConfig),
		Env:               stringListValues(cfg.env),
		MaxTurns:          valueOfIntFlag(cfg.maxTurns),
		NoTools:           valueOfBoolFlag(cfg.noTools),
		JSON:              valueOfBoolFlag(cfg.jsonOutput),
		Ephemeral:         valueOfBoolFlag(cfg.ephemeral),
		Timeout:           valueOfDurationFlag(cfg.timeout),
		OutputLastMessage: valueOfStringFlag(cfg.outputLastMessage),
		OutputSchemaPath:  valueOfStringFlag(cfg.outputSchema),
		ResumeID:          resumeID,
		ResumeLast:        resumeLast,
		Stdout:            os.Stdout,
		Stderr:            os.Stderr,
		Controller:        execControllerOverride,
	}
	if err := applyExecInputPayload(&opts, input); err != nil {
		return wuuexec.Options{}, err
	}
	return opts, nil
}

func applyExecInputPayload(opts *wuuexec.Options, input *execInputPayload) error {
	if opts == nil || input == nil {
		return nil
	}
	opts.FilePaths = append(opts.FilePaths, input.Files...)
	opts.ImagePaths = append(opts.ImagePaths, input.Images...)
	opts.Attachments.Files = append(opts.Attachments.Files, input.FileAttachments...)
	opts.Attachments.Images = append(opts.Attachments.Images, input.ImageAttachments...)
	if opts.Workdir == "" {
		opts.Workdir = strings.TrimSpace(input.Workdir)
	}
	if opts.Provider == "" {
		opts.Provider = strings.TrimSpace(input.Provider)
	}
	if opts.Model == "" {
		opts.Model = strings.TrimSpace(input.Model)
	}
	if opts.Effort == "" {
		opts.Effort = strings.TrimSpace(input.Effort)
	}
	if opts.Variant == "" {
		opts.Variant = strings.TrimSpace(input.Variant)
	}
	if opts.PermissionMode == "" {
		opts.PermissionMode = strings.TrimSpace(input.PermissionMode)
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = strings.TrimSpace(input.ConfigPath)
	}
	if opts.AgentProfile == "" {
		opts.AgentProfile = strings.TrimSpace(input.AgentProfile)
	}
	if input.IgnoreUserConfig != nil && !opts.IgnoreUserConfig {
		opts.IgnoreUserConfig = *input.IgnoreUserConfig
	}
	opts.Env = append(opts.Env, input.Env...)
	if input.MaxTurns != nil && opts.MaxTurns == 0 {
		opts.MaxTurns = *input.MaxTurns
	}
	if opts.MaxTurns < 0 {
		return errors.New("max_turns must be non-negative")
	}
	if input.NoTools != nil && !opts.NoTools {
		opts.NoTools = *input.NoTools
	}
	if input.JSON != nil && !opts.JSON {
		opts.JSON = *input.JSON
	}
	if input.Ephemeral != nil && !opts.Ephemeral {
		opts.Ephemeral = *input.Ephemeral
	}
	if opts.OutputLastMessage == "" {
		opts.OutputLastMessage = strings.TrimSpace(input.OutputLastMessage)
	}
	if opts.OutputSchemaPath == "" {
		opts.OutputSchemaPath = strings.TrimSpace(input.OutputSchema)
	}
	if opts.Timeout == 0 && strings.TrimSpace(input.Timeout) != "" {
		timeout, err := time.ParseDuration(strings.TrimSpace(input.Timeout))
		if err != nil {
			return fmt.Errorf("parse input_json.timeout: %w", err)
		}
		opts.Timeout = timeout
	}
	return nil
}

func resolveExecPromptAndInput(cfg execCLIConfig, args []string, allowEmpty bool) (string, *execInputPayload, error) {
	if !valueOfBoolFlag(cfg.inputJSON) {
		prompt, err := resolveExecPrompt(args, allowEmpty)
		return prompt, nil, err
	}
	if len(args) > 0 {
		return "", nil, errors.New("positional prompt is not allowed with --input-json")
	}
	input, err := readExecInputPayload(os.Stdin, stdinHasInput())
	if err != nil {
		return "", nil, err
	}
	prompt := input.promptText()
	if prompt == "" && !allowEmpty && !input.hasAttachments() {
		return "", nil, errors.New("prompt is required in --input-json input")
	}
	return prompt, input, nil
}

func resolveExecPrompt(args []string, allowEmpty bool) (string, error) {
	return wuuexec.ResolvePrompt(wuuexec.PromptInput{
		Args:        args,
		Stdin:       os.Stdin,
		StdinIsPipe: stdinHasInput(),
		AllowEmpty:  allowEmpty,
	})
}

func readExecInputPayload(r io.Reader, stdinIsPipe bool) (*execInputPayload, error) {
	if !stdinIsPipe {
		return nil, errors.New("--input-json requires JSON on stdin")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read input JSON: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, errors.New("--input-json input is empty")
	}
	var input execInputPayload
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return nil, fmt.Errorf("decode input JSON: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("decode input JSON: multiple JSON values are not allowed")
		}
		return nil, fmt.Errorf("decode input JSON: %w", err)
	}
	return &input, nil
}

func (p *execInputPayload) promptText() string {
	if p == nil {
		return ""
	}
	prompt := strings.TrimSpace(p.Prompt)
	stdinText := strings.TrimSpace(p.Stdin)
	if prompt != "" && stdinText != "" {
		return prompt + "\n\n<stdin>\n" + stdinText + "\n</stdin>"
	}
	if prompt != "" {
		return prompt
	}
	return stdinText
}

func (p *execInputPayload) hasAttachments() bool {
	return p != nil && (len(p.Files) > 0 || len(p.Images) > 0 || len(p.FileAttachments) > 0 || len(p.ImageAttachments) > 0)
}

func stringListValues(f *stringListFlag) []string {
	if f == nil || len(*f) == 0 {
		return nil
	}
	return append([]string(nil), (*f)...)
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func hasExecAttachments(cfg execCLIConfig) bool {
	return len(stringListValues(cfg.files)) > 0 || len(stringListValues(cfg.images)) > 0
}

func valueOfStringFlag(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func valueOfBoolFlag(v *bool) bool {
	return v != nil && *v
}

func valueOfDurationFlag(v *time.Duration) time.Duration {
	if v == nil {
		return 0
	}
	return *v
}

func valueOfIntFlag(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func runAppServer(args []string) error {
	fs := flag.NewFlagSet("app-server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	providerName := fs.String("provider", "", "provider name in config")
	modelOverride := fs.String("model", "", "model override")
	workdir := fs.String("workdir", "", "workspace directory")
	workspaceID := fs.String("workspace-id", "", "stable workspace identity (survives the workspace moving on disk)")
	configFile := fs.String("config", "", "explicit runtime config file")
	hostKind := fs.String("host", string(runtime.HostLocal), "runtime host: local or cloud")
	instanceID := fs.String("instance-id", "", "cloud runtime instance identity")
	noTools := fs.Bool("no-tools", false, "disable local tools")
	safeMode := fs.Bool("safe-mode", false, "start without activating plugins")
	if err := fs.Parse(args); err != nil {
		return err
	}

	host, err := resolveAppServerHost(*hostKind, *instanceID, *workspaceID, *configFile)
	if err != nil {
		return err
	}
	// Finder and Dock start the core with the system PATH. Install locations
	// have to be visible before engine detection and before child processes
	// inherit the environment.
	if host.Kind == runtime.HostLocal {
		enginecatalog.InstallUserPath()
	}
	rt, err := wuusdk.New(wuusdk.Options{
		WorkDir:               *workdir,
		WorkspaceID:           *workspaceID,
		HomeDir:               os.Getenv("HOME"),
		ConfigPath:            *configFile,
		CreateConfigIfMissing: strings.TrimSpace(*configFile) == "",
		Host: wuusdk.Host{
			Kind:       wuusdk.HostKind(host.Kind),
			InstanceID: host.InstanceID,
		},
		Provider: *providerName,
		Model:    *modelOverride,
		NoTools:  *noTools,
		SafeMode: *safeMode,
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = rt.Close()
	}()

	return rt.Serve(context.Background(), os.Stdin, os.Stdout)
}

func resolveAppServerHost(kind, instanceID, workspaceID, configFile string) (runtime.Host, error) {
	host, err := runtime.ResolveHost(runtime.Host{Kind: runtime.HostKind(kind), InstanceID: instanceID})
	if err != nil {
		return runtime.Host{}, err
	}
	if host.Kind != runtime.HostCloud {
		return host, nil
	}
	if strings.TrimSpace(workspaceID) == "" {
		return runtime.Host{}, errors.New("cloud host requires --workspace-id")
	}
	if strings.TrimSpace(configFile) == "" {
		return runtime.Host{}, errors.New("cloud host requires --config; managed runtimes do not create starter config")
	}
	return host, nil
}

func loadAppServerRuntimeConfig(rootDir, homeDir, configFile string) (config.Config, string, runtime.ConfigLoadMode, error) {
	return runtimeconfig.Load(runtimeconfig.LoadOptions{
		RootDir:               rootDir,
		HomeDir:               homeDir,
		ConfigPath:            configFile,
		CreateConfigIfMissing: strings.TrimSpace(configFile) == "",
	})
}

func loadOrCreateAppServerConfig(rootDir, homeDir string) (config.Config, string, error) {
	cfg, configPath, _, err := runtimeconfig.Load(runtimeconfig.LoadOptions{
		RootDir:               rootDir,
		HomeDir:               homeDir,
		CreateConfigIfMissing: true,
	})
	return cfg, configPath, err
}

func appServerStarterConfig() config.Config {
	return runtimeconfig.StarterConfig()
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

func isCodexModelsProvider(providerType string) bool {
	s := strings.ToLower(strings.TrimSpace(providerType))
	s = strings.ReplaceAll(s, "_", "-")
	return s == "openai-codex" || s == "codex-subscription" || s == "chatgpt-codex"
}

func explicitProviderAPIKey(provider config.ProviderConfig) string {
	if key := strings.TrimSpace(provider.APIKey); key != "" {
		return key
	}
	if envKey := strings.TrimSpace(provider.APIKeyEnv); envKey != "" {
		return strings.TrimSpace(os.Getenv(envKey))
	}
	return ""
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

func stdinHasInput() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}

func printUsage() {
	fmt.Println(`wuu - GUI-first coding agent backend and CLI tools

Usage:
  wuu init [--force]
  wuu login xai [--no-open]
  wuu models [flags]
  wuu exec [flags] "your coding task"
  wuu exec -c|--continue [flags] [prompt...]     alias: resume --last
  wuu exec -r|--resume THREAD_ID [flags] [prompt...]
                                                  alias: resume THREAD_ID
  wuu exec resume (--last|THREAD_ID) [flags] "continue task"
  wuu exec fork THREAD_ID [flags] "continue from a fork"
  wuu exec review (--uncommitted|--base REF|--commit SHA) [flags]
  wuu -c|--continue [flags] [prompt...]          shortcut: exec -c
  wuu -r|--resume THREAD_ID [flags] [prompt...]  shortcut: exec -r
  wuu session list|show|trace|search|archive|delete|export [flags]
  wuu skills lint [--json] PATH...
  wuu plugin inspect|install|update|list|approve|reject|enable|disable|remove [flags]
  wuu debug app-server initialize [flags]
  wuu debug app-server send [flags] METHOD [JSON]
  wuu debug channel e2e (--sandbox|--sandbox-name NAME) [flags]
  wuu debug channel inspect [--sandbox NAME] [flags]
  wuu debug channel send [--sandbox NAME] [flags] "message"
  wuu debug sandbox list
  wuu debug sandbox delete NAME
  wuu debug protocol events [flags] THREAD_ID
  wuu run [flags] "your coding task"
  wuu eval [flags]
  wuu app-server [flags]
  wuu relay [--addr HOST:PORT] [--state FILE] [--push-webhook URL]
  wuu remote init --relay ws://HOST:PORT/v1/connect [--name NAME]
  wuu remote host [--workdir DIR] [--pair] [flags]
  wuu remote devices
  wuu remote phone pair --uri "wuu://pair?..." [--store FILE]
  wuu remote phone status|send|watch [--store FILE] [flags]
  wuu probe-title [flags]   run the LLM title pipeline against a real provider
  wuu version [--long|--json]

Models flags:
  --provider        provider name from config
  --workdir         workspace directory
  --json            output model metadata as JSON

Exec flags:
  --provider        provider name from config
  --model           model override
  --effort          reasoning effort override
  --variant         model variant override
  --permission-mode permission mode override
  --workdir         workspace directory
  --config          trust one explicit config file path
  --profile         agent profile name
  --ignore-user-config
                   trust project config and ignore user config
  --env KEY=VALUE   set environment variable for the run (repeatable)
  --file            attach a local PDF file (repeatable)
  --image           attach a local image (repeatable)
  --image-original  send images without resizing
  --no-tools        disable local tools
  --json            emit JSONL to stdout
  --ephemeral       run without creating a persistent session
  --input-json      read machine input JSON from stdin
  --max-turns       max agent loop turns
  --output-schema   JSON schema for structured final output
  --timeout         total timeout (e.g. 20m)
  --output-last-message
                   write final agent message to a file

Exec review:
  --uncommitted     review current uncommitted changes
  --base REF        review changes against base ref
  --commit SHA      review one commit

Session commands:
  list [--json] [--workdir DIR] [--all-workdirs]
                   list visible sessions for the workspace
  show [--json] [--last|THREAD_ID] [--workdir DIR]
                   show session metadata and history
  trace [--json] [--last|THREAD_ID] [--workdir DIR]
                   replay a session trace artifact
  search [--json] QUERY [--workdir DIR]
                   search session metadata and history

Execution runs:
  runs [--json] [--workdir DIR] [--status STATUS]
                   list persisted execution Run manifests
  runs read RUN_ID  inspect one execution Run manifest
  archive [--json] THREAD_ID
                   hide a session from default lists
  delete [--json] THREAD_ID
                   delete a session and its workspace artifacts
  export [--json] [--last|THREAD_ID] [--out FILE] [--workdir DIR]
                   export session history as JSONL format

Skills commands:
  lint [--json] PATH...
                   check skill files with the same rules discovery uses;
                   PATH is a skill directory, a skills root, or a flat .md file

Debug commands:
  app-server initialize [--workdir DIR] [--provider NAME] [--model MODEL] [--no-tools]
                   start a local app-server and print its initialize result
  app-server send [flags] METHOD [JSON]
                   send one app-server method and print the raw JSON result
  channel e2e (--sandbox|--sandbox-name NAME) [--keep-sandbox] [--agent NAME] [--room NAME] [--message TEXT] [--expect TEXT] [--timeout DURATION] [app-server flags]
                   create or resume an isolated real-provider scenario and assert the named-agent reply
  channel inspect [--sandbox NAME] [--room ID|NAME] [--after SEQ] [--limit N] [app-server flags]
                   inspect persistent rooms and optionally one room's messages
  channel send [--sandbox NAME] --room ID|NAME [--wait DURATION] [--replies N] [app-server flags] "message"
                   send through the real channel path and optionally wait for agent replies
  sandbox list      list reusable named debug sandboxes
  sandbox delete NAME
                   delete one reusable named debug sandbox
  protocol events [--json] [--workdir DIR] THREAD_ID
                   print trace JSONL events recorded for a session

Run:
  wuu run forwards to wuu exec; --max-steps, --temperature, and --system-prompt are not accepted.

Eval flags:
  --provider        provider name from config
  --model           model override
  --workdir         workspace directory containing wuu config
  --task            task id, comma-separated ids, or all
  --list            list built-in eval tasks
  --json            output eval report as JSON
  --output          write eval report JSON to path
  --max-steps       max tool loop steps per task
  --timeout         timeout per task (default 10m)
  --keep-workdirs   keep temporary task workdirs
  --replay-trace    replay an eval trace JSONL without calling a model or tools
  --live-codex-oauth
                   run live Codex OAuth E2E eval using local Codex CLI or wuu OAuth credentials

App server flags:
  --provider        provider name from config
  --model           model override
  --workdir         workspace directory
  --no-tools        disable local tools

Probe-title flags:
  --workdir         workspace directory (default: cwd)
  --thread          thread id to regenerate title for (default: most recent)
  --user-prompt     synthetic first user message; auto dry-run
  --provider        override provider from config
  --model           override model from config
  --dry-run         do not persist the title
  --verbose         print every step in human-readable mode
  --json            emit the result struct as JSON
  --quiet           suppress human-readable summary`)
}
