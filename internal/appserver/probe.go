package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// loadProbeConfig mirrors the app-server config resolution order: try the
// workspace's .wuu.json, then fall back to the user-global config in the
// unified wuu home (~/.wuu/config.json, or WUU_HOME/config.json), and finally
// the legacy ~/.config/wuu/config.json. This is intentionally NOT the same as
// `wuu app-server`'s "create a starter config if missing" path — the probe
// must operate on the user's real config, otherwise it would silently target a
// different provider.
func loadProbeConfig(workdir, homeDir string) (config.Config, string, error) {
	if cfg, path, err := config.LoadFrom(workdir, homeDir); err == nil {
		return cfg, path, nil
	} else if !strings.Contains(err.Error(), "not found") {
		return config.Config{}, "", err
	}
	return config.Config{}, "", fmt.Errorf("no wuu config found for workdir %q (looked in %q, ~/.wuu/config.json, and the legacy ~/.config/wuu/config.json); run `wuu init` or `wuu app-server` once to create one",
		workdir, workdir)
}

// ProbeTitleOptions controls `wuu probe-title`. It mirrors the flags the CLI
// accepts and gives tests a single entry point into the title pipeline.
type ProbeTitleOptions struct {
	WorkDir       string
	HomeDir       string
	ProviderName  string
	ModelOverride string
	ThreadID      string
	UserPrompt    string
	DryRun        bool
	Verbose       bool
	JSON          bool
	Timeout       time.Duration
}

// ProbeTitle is the public entry point used by `wuu probe-title` and
// `thread/regenerate-title`. It mirrors the production path 1:1: same
// TitleClient, same prompt, same temperature mapping, same streaming
// aggregator, same cleaner, same persistence call. The only differences
// from the hot path are:
//
//   - No `thread/updated` notification (notify=false). The probe has no
//     client to send to; the live JSON-RPC method re-implements the
//     notification step on top.
//   - Optional dry-run.
//   - The result is returned to the caller (so the CLI can pretty-print
//     it) instead of being discarded.
func ProbeTitle(ctx context.Context, opts ProbeTitleOptions) (TitleGenerationResult, error) {
	cfg, _, err := loadProbeConfig(opts.WorkDir, opts.HomeDir)
	if err != nil {
		return TitleGenerationResult{}, err
	}
	rt, err := runtime.NewSession(runtime.Options{
		RootDir:       opts.WorkDir,
		HomeDir:       opts.HomeDir,
		Config:        cfg,
		ProviderName:  opts.ProviderName,
		ModelOverride: opts.ModelOverride,
	})
	if err != nil {
		return TitleGenerationResult{}, fmt.Errorf("build runtime: %w", err)
	}
	defer func() { _, _ = rt.Cleanup() }()

	binding, err := rt.RetainState()
	if err != nil {
		return TitleGenerationResult{}, err
	}
	defer binding.Close()
	srv := New(rt, io.Discard)
	defer srv.Close()

	threadID, history, synthetic, err := resolveProbeThread(binding, rt, opts)
	if err != nil {
		return TitleGenerationResult{}, err
	}

	dryRun := opts.DryRun || synthetic
	if !opts.JSON {
		printProbeHeader(os.Stdout, opts, threadID, synthetic, dryRun, rt)
	}

	result, err := srv.generateThreadTitleCore(binding, threadID, history, !dryRun, false, true, nil)
	if err != nil {
		if !opts.JSON {
			fmt.Fprintf(os.Stdout, "\nFAILED: %v\n", err)
		}
		return result, err
	}

	if opts.JSON {
		if !opts.Verbose {
			result.ContentDeltas = nil
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
		return result, nil
	}

	printProbeResult(os.Stdout, result, opts.Verbose)
	return result, nil
}

// resolveProbeThread picks a thread to operate on. If UserPrompt is set,
// a synthetic single-turn history is built and the function returns a fake
// threadID ("probe-synthetic") plus synthetic=true. Otherwise ThreadID is
// resolved (defaults to the most recent thread in the workspace) and the
// history is loaded from the SQLite-backed session store.
func resolveProbeThread(binding *runtime.State, rt *runtime.Session, opts ProbeTitleOptions) (string, []providers.ChatMessage, bool, error) {
	if strings.TrimSpace(opts.UserPrompt) != "" {
		history := []providers.ChatMessage{
			{Role: "user", Content: opts.UserPrompt},
			{Role: "assistant", Content: "(probe synthetic assistant turn; not used by the title model)"},
		}
		return "probe-synthetic", history, true, nil
	}
	target := strings.TrimSpace(opts.ThreadID)
	if target == "" {
		sessions, err := binding.Sessions.List(1)
		if err != nil {
			return "", nil, false, fmt.Errorf("list sessions to resolve default thread: %w", err)
		}
		if len(sessions) == 0 {
			return "", nil, false, fmt.Errorf("no threads found in %s; pass --thread or --user-prompt", rt.SessionDir)
		}
		target = sessions[0].ID
	}
	history, err := loadChatMessages(binding.Sessions, target)
	if err != nil {
		return "", nil, false, fmt.Errorf("load history for thread %q: %w", target, err)
	}
	return target, history, false, nil
}

func printProbeHeader(out io.Writer, opts ProbeTitleOptions, threadID string, synthetic, dryRun bool, rt *runtime.Session) {
	mode := "persist"
	if dryRun {
		mode = "dry-run"
	}
	if synthetic {
		mode = "dry-run (synthetic thread)"
	}
	fmt.Fprintf(out, "Probing title generation\n")
	fmt.Fprintf(out, "  workdir:    %s\n", opts.WorkDir)
	fmt.Fprintf(out, "  sessionDir: %s\n", rt.SessionDir)
	fmt.Fprintf(out, "  provider:   %s\n", firstNonEmpty(opts.ProviderName, rt.ProviderName))
	fmt.Fprintf(out, "  model:      %s\n", firstNonEmpty(opts.ModelOverride, rt.Model))
	fmt.Fprintf(out, "  thread:     %s%s\n", threadID, ifThen(synthetic, " (synthetic)", ""))
	fmt.Fprintf(out, "  mode:       %s\n", mode)
	fmt.Fprintf(out, "  verbose:    %v\n\n", opts.Verbose)
}

func printProbeResult(out io.Writer, r TitleGenerationResult, verbose bool) {
	fmt.Fprintf(out, "── result ──\n")
	fmt.Fprintf(out, "  model:            %s\n", r.Model)
	if r.SendTemperature {
		fmt.Fprintf(out, "  temperature:      %.2f  (sent in request)\n", r.Temperature)
	} else {
		fmt.Fprintf(out, "  temperature:      omitted  (model pins it)\n")
	}
	fmt.Fprintf(out, "  request messages: %d\n", r.RequestMessages)
	fmt.Fprintf(out, "  first user msg:   %d chars  %q\n", len(r.FirstUserPrompt), truncate(r.FirstUserPrompt, 80))
	fmt.Fprintf(out, "  content deltas:   %d\n", len(r.ContentDeltas))
	fmt.Fprintf(out, "  aggregated text:  %d chars  %q\n", len(r.AggregatedText), truncate(r.AggregatedText, 80))
	fmt.Fprintf(out, "  cleaned title:    %q\n", r.CleanedTitle)
	fmt.Fprintf(out, "  persisted:        %v\n", r.Persisted)
	if r.Persisted {
		fmt.Fprintf(out, "  persisted title:  %q\n", r.PersistedTitle)
		fmt.Fprintf(out, "  thread preview:   %q\n", r.PreviewAfter)
	}
	fmt.Fprintf(out, "  notified:         %v\n", r.Notified)
	if r.SkipReason != "" {
		fmt.Fprintf(out, "  skipped:          %s\n", r.SkipReason)
	}
	fmt.Fprintf(out, "  duration:         %dms\n", r.CompletedAt.Sub(r.StartedAt).Milliseconds())

	if verbose && len(r.ContentDeltas) > 0 {
		fmt.Fprintf(out, "\n── content deltas (in order) ──\n")
		for i, d := range r.ContentDeltas {
			fmt.Fprintf(out, "  [%2d] %q\n", i, d)
		}
	}
	if verbose && r.StreamErr != nil {
		fmt.Fprintf(out, "\n── stream error ──\n  %v\n", r.StreamErr)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func ifThen(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
