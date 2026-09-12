package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/config"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/evalharness"
	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	defer file.Close()
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode image: %v", err)
	}
}

func TestRunVersionAliasForwardsJSONFlag(t *testing.T) {
	output := captureStdout(t, func() {
		if err := run([]string{"--version", "--json"}); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", output, err)
	}
	if _, ok := payload["version"]; !ok {
		t.Fatalf("expected version field in JSON output: %v", payload)
	}
}

func TestRunVersionAliasForwardsLongFlag(t *testing.T) {
	output := captureStdout(t, func() {
		if err := run([]string{"-v", "--long"}); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})

	if !strings.Contains(output, "version:") {
		t.Fatalf("expected long version output, got %q", output)
	}
	if !strings.Contains(output, "commit:") {
		t.Fatalf("expected long version output to include commit, got %q", output)
	}
}

func TestRunWithoutArgsPrintsUsage(t *testing.T) {
	output := captureStdout(t, func() {
		if err := run(nil); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})
	if !strings.Contains(output, "GUI-first") || strings.Contains(output, "wuu tui") {
		t.Fatalf("unexpected usage output: %q", output)
	}
}

func TestRunTUICommandIsRemoved(t *testing.T) {
	err := run([]string{"tui"})
	if err == nil || !strings.Contains(err.Error(), "TUI has been removed") {
		t.Fatalf("expected removed TUI error, got %v", err)
	}
}

func TestRunExecJSONUsesControllerPath(t *testing.T) {
	controller := newCLIExecFakeController(
		cliExecNotification(appserver.NotificationAgentMessageDelta, appserver.AgentMessageDeltaNotification{ThreadID: "thread-1", TurnID: "turn-1", Delta: "ok"}),
		cliExecNotification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "ok"}),
	)
	restore := installExecControllerOverride(t, controller)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"exec", "--json", "hello"}); err != nil {
			t.Fatalf("run exec: %v", err)
		}
	})

	if !controller.startedThread || controller.startedPrompt != "hello" {
		t.Fatalf("exec did not use expected controller path: %+v", controller)
	}
	events := parseCLIJSONLines(t, output)
	if got := events[0]["type"]; got != "session_configured" {
		t.Fatalf("first event = %v, want session_configured", got)
	}
	if got := events[len(events)-1]["type"]; got != "result" {
		t.Fatalf("last event = %v, want result", got)
	}
}

func TestRunExecRejectsRemovedParticipantFlags(t *testing.T) {
	for _, flagName := range []string{"participant", "thread"} {
		t.Run(flagName, func(t *testing.T) {
			err := run([]string{"exec", "--" + flagName, "legacy-value", "hello"})
			if wuuexec.ExitCode(err) != wuuexec.ExitInvalidInput {
				t.Fatalf("ExitCode = %d, err=%v", wuuexec.ExitCode(err), err)
			}
			if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -"+flagName) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunExecInputJSONRejectsUnknownActionsField(t *testing.T) {
	err := withStdin(t, `{"actions":[{"action":"create_group"}]}`, func() error {
		return run([]string{"exec", "--input-json"})
	})
	if wuuexec.ExitCode(err) != wuuexec.ExitInvalidInput {
		t.Fatalf("ExitCode = %d, err=%v", wuuexec.ExitCode(err), err)
	}
	if err == nil || !strings.Contains(err.Error(), `unknown field "actions"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunExecResumeLastUsesResumePath(t *testing.T) {
	controller := newCLIExecFakeController(
		cliExecNotification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "continued"}),
	)
	restore := installExecControllerOverride(t, controller)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"exec", "resume", "--last", "--json", "continue"}); err != nil {
			t.Fatalf("run exec resume: %v", err)
		}
	})

	if controller.startedThread {
		t.Fatal("resume should not start a new thread")
	}
	if controller.resumedThread != "" {
		t.Fatalf("resume --last should pass empty thread id, got %q", controller.resumedThread)
	}
	events := parseCLIJSONLines(t, output)
	if got := events[1]["type"]; got != "thread_resumed" {
		t.Fatalf("second event = %v, want thread_resumed\n%s", got, output)
	}
}

func TestRunExecForkUsesForkPath(t *testing.T) {
	controller := newCLIExecFakeController(
		cliExecNotification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "fork-thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "forked"}),
	)
	restore := installExecControllerOverride(t, controller)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"exec", "fork", "--json", "source-thread", "continue"}); err != nil {
			t.Fatalf("run exec fork: %v", err)
		}
	})

	if controller.startedThread || controller.resumedThread != "" {
		t.Fatalf("fork should not start or resume: started=%v resumed=%q", controller.startedThread, controller.resumedThread)
	}
	if controller.forkedThread != "source-thread" {
		t.Fatalf("forkedThread = %q", controller.forkedThread)
	}
	if controller.startedPrompt != "continue" {
		t.Fatalf("prompt = %q", controller.startedPrompt)
	}
	events := parseCLIJSONLines(t, output)
	if got := events[1]["type"]; got != "thread_forked" {
		t.Fatalf("second event = %v, want thread_forked\n%s", got, output)
	}
}

func TestRunExecEphemeralUsesEphemeralStart(t *testing.T) {
	controller := newCLIExecFakeController(
		cliExecNotification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "done"}),
	)
	restore := installExecControllerOverride(t, controller)
	defer restore()

	_ = captureStdout(t, func() {
		if err := run([]string{"exec", "--ephemeral", "--json", "scratch task"}); err != nil {
			t.Fatalf("run exec ephemeral: %v", err)
		}
	})

	if !controller.startedThread || !controller.startEphemeral {
		t.Fatalf("expected ephemeral thread start: %+v", controller)
	}
}

func TestRunExecPassesFileAndImageAttachments(t *testing.T) {
	workdir := t.TempDir()
	writeTestPNG(t, filepath.Join(workdir, "shot.png"))
	if err := os.WriteFile(filepath.Join(workdir, "report.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	controller := newCLIExecFakeController(
		cliExecNotification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "ok"}),
	)
	restore := installExecControllerOverride(t, controller)
	defer restore()

	_ = captureStdout(t, func() {
		if err := run([]string{"exec", "--workdir", workdir, "--file", "report.pdf", "--image", "shot.png", "inspect"}); err != nil {
			t.Fatalf("run exec: %v", err)
		}
	})

	if controller.startedPrompt != "inspect" {
		t.Fatalf("prompt = %q", controller.startedPrompt)
	}
	if len(controller.startedFiles) != 1 || controller.startedFiles[0].Filename != "report.pdf" || controller.startedFiles[0].MediaType != "application/pdf" {
		t.Fatalf("unexpected file attachments: %+v", controller.startedFiles)
	}
	if len(controller.startedImages) != 1 || controller.startedImages[0].MediaType != "image/png" {
		t.Fatalf("unexpected image attachments: %+v", controller.startedImages)
	}
	if controller.startedFiles[0].Data == "" || controller.startedImages[0].Data == "" {
		t.Fatalf("attachment data should be base64 encoded: files=%+v images=%+v", controller.startedFiles, controller.startedImages)
	}
}

func TestRunExecInputJSONUsesMachineInput(t *testing.T) {
	workdir := t.TempDir()
	writeTestPNG(t, filepath.Join(workdir, "shot.png"))
	if err := os.WriteFile(filepath.Join(workdir, "report.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	controller := newCLIExecFakeController(
		cliExecNotification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "ok"}),
	)
	restore := installExecControllerOverride(t, controller)
	defer restore()
	input := `{
		"prompt": "use this log",
		"stdin": "panic: boom",
		"workdir": "` + filepath.ToSlash(workdir) + `",
		"provider": "p",
		"model": "m",
		"json": true,
		"ephemeral": true,
		"files": ["report.pdf"],
		"images": ["shot.png"]
	}`

	output := withStdin(t, input, func() string {
		return captureStdout(t, func() {
			if err := run([]string{"exec", "--input-json"}); err != nil {
				t.Fatalf("run exec input JSON: %v", err)
			}
		})
	})

	if controller.startedPrompt != "use this log\n\n<stdin>\npanic: boom\n</stdin>" {
		t.Fatalf("prompt = %q", controller.startedPrompt)
	}
	if !controller.startEphemeral {
		t.Fatalf("expected ephemeral start: %+v", controller)
	}
	if len(controller.startedFiles) != 1 || controller.startedFiles[0].Filename != "report.pdf" {
		t.Fatalf("unexpected file attachments: %+v", controller.startedFiles)
	}
	if len(controller.startedImages) != 1 || controller.startedImages[0].MediaType != "image/png" {
		t.Fatalf("unexpected image attachments: %+v", controller.startedImages)
	}
	events := parseCLIJSONLines(t, output)
	if got := events[0]["type"]; got != "session_configured" {
		t.Fatalf("expected JSONL output from input JSON, got %v\n%s", got, output)
	}
}

func TestRunExecInputJSONRejectsPositionalPrompt(t *testing.T) {
	err := withStdin(t, `{"prompt":"hello"}`, func() error {
		return run([]string{"exec", "--input-json", "extra"})
	})
	if wuuexec.ExitCode(err) != wuuexec.ExitInvalidInput {
		t.Fatalf("ExitCode = %d, err=%v", wuuexec.ExitCode(err), err)
	}
	if err == nil || !strings.Contains(err.Error(), "positional prompt") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadExecInputPayloadRejectsUnknownFields(t *testing.T) {
	input, err := readExecInputPayload(strings.NewReader(`{"promtp":"typo"}`), true)
	if err == nil || input != nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got input=%+v err=%v", input, err)
	}
}

func TestRunExecAllowsAttachmentOnlyPrompt(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "report.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	controller := newCLIExecFakeController(
		cliExecNotification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "ok"}),
	)
	restore := installExecControllerOverride(t, controller)
	defer restore()

	_ = captureStdout(t, func() {
		if err := run([]string{"exec", "--workdir", workdir, "--file", "report.pdf"}); err != nil {
			t.Fatalf("run exec: %v", err)
		}
	})

	if controller.startedPrompt != "" {
		t.Fatalf("prompt = %q, want empty attachment-only prompt", controller.startedPrompt)
	}
	if len(controller.startedFiles) != 1 {
		t.Fatalf("file attachment missing: %+v", controller.startedFiles)
	}
}

func TestExecOptionsFromCLIAcceptsMaxTurns(t *testing.T) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addExecFlags(fs)
	if err := fs.Parse([]string{"--max-turns", "3"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	opts, err := execOptionsFromCLI(cfg, "hello", "", false, nil)
	if err != nil {
		t.Fatalf("execOptionsFromCLI: %v", err)
	}
	if opts.MaxTurns != 3 {
		t.Fatalf("MaxTurns = %d, want 3", opts.MaxTurns)
	}
}

func TestExecOptionsFromInputJSONAcceptsMaxTurns(t *testing.T) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addExecFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	maxTurns := 4
	opts, err := execOptionsFromCLI(cfg, "hello", "", false, &execInputPayload{MaxTurns: &maxTurns})
	if err != nil {
		t.Fatalf("execOptionsFromCLI: %v", err)
	}
	if opts.MaxTurns != 4 {
		t.Fatalf("MaxTurns = %d, want 4", opts.MaxTurns)
	}
}

func TestExecOptionsFromCLIAcceptsOutputSchema(t *testing.T) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addExecFlags(fs)
	if err := fs.Parse([]string{"--output-schema", "schema.json"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	opts, err := execOptionsFromCLI(cfg, "hello", "", false, nil)
	if err != nil {
		t.Fatalf("execOptionsFromCLI: %v", err)
	}
	if opts.OutputSchemaPath != "schema.json" {
		t.Fatalf("OutputSchemaPath = %q, want schema.json", opts.OutputSchemaPath)
	}
}

func TestExecOptionsFromInputJSONAcceptsOutputSchema(t *testing.T) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addExecFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	opts, err := execOptionsFromCLI(cfg, "hello", "", false, &execInputPayload{OutputSchema: "schema.json"})
	if err != nil {
		t.Fatalf("execOptionsFromCLI: %v", err)
	}
	if opts.OutputSchemaPath != "schema.json" {
		t.Fatalf("OutputSchemaPath = %q, want schema.json", opts.OutputSchemaPath)
	}
}

func TestRunExecRejectsNegativeMaxTurnsWithExitCodeTwo(t *testing.T) {
	err := run([]string{"exec", "--max-turns=-1", "hello"})
	if wuuexec.ExitCode(err) != wuuexec.ExitInvalidInput {
		t.Fatalf("ExitCode = %d, err=%v", wuuexec.ExitCode(err), err)
	}
	if err == nil || !strings.Contains(err.Error(), "--max-turns must be non-negative") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunExecRejectsInvalidPermissionModeWithExitCodeTwo(t *testing.T) {
	err := run([]string{"exec", "--permission-mode", "readonly", "hello"})
	if wuuexec.ExitCode(err) != wuuexec.ExitInvalidInput {
		t.Fatalf("ExitCode = %d, err=%v", wuuexec.ExitCode(err), err)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid --permission-mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunExecRejectsInvalidEnvWithExitCodeTwo(t *testing.T) {
	err := run([]string{"exec", "--env", "not-an-assignment", "hello"})
	if wuuexec.ExitCode(err) != wuuexec.ExitInvalidInput {
		t.Fatalf("ExitCode = %d, err=%v", wuuexec.ExitCode(err), err)
	}
	if err == nil || !strings.Contains(err.Error(), "--env must be KEY=VALUE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCommandForwardsToExecControllerPath(t *testing.T) {
	controller := newCLIExecFakeController(
		cliExecNotification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "run result"}),
	)
	restore := installExecControllerOverride(t, controller)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"run", "--json", "hello from legacy run"}); err != nil {
			t.Fatalf("run legacy run: %v", err)
		}
	})

	if !controller.startedThread || controller.startedPrompt != "hello from legacy run" {
		t.Fatalf("run did not use expected exec controller path: %+v", controller)
	}
	events := parseCLIJSONLines(t, output)
	if got := events[len(events)-1]["type"]; got != "result" {
		t.Fatalf("last event = %v, want result\n%s", got, output)
	}
}

func TestRunCommandRejectsLegacyOnlyFlags(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--max-steps", "3", "hello"},
		{"run", "--temperature=0.2", "hello"},
		{"run", "--system-prompt", "be terse", "hello"},
	} {
		err := run(args)
		if wuuexec.ExitCode(err) != wuuexec.ExitInvalidInput {
			t.Fatalf("ExitCode(%v) = %d, err=%v", args, wuuexec.ExitCode(err), err)
		}
		if err == nil || !strings.Contains(err.Error(), "compatibility wrapper around wuu exec") {
			t.Fatalf("unexpected error for %v: %v", args, err)
		}
	}
}

func TestRunExecReviewUsesExecControllerPath(t *testing.T) {
	controller := newCLIExecFakeController(
		cliExecNotification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "reviewed"}),
	)
	restore := installExecControllerOverride(t, controller)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"exec", "review", "--uncommitted", "--json", "prioritize tests"}); err != nil {
			t.Fatalf("run exec review: %v", err)
		}
	})

	if !controller.startedThread {
		t.Fatalf("review should start an exec thread: %+v", controller)
	}
	if !strings.Contains(controller.startedPrompt, "Review the current uncommitted changes") ||
		!strings.Contains(controller.startedPrompt, "current diff using the tools available under the active model surface") ||
		!strings.Contains(controller.startedPrompt, "prioritize tests") {
		t.Fatalf("unexpected review prompt: %q", controller.startedPrompt)
	}
	events := parseCLIJSONLines(t, output)
	if got := events[len(events)-1]["type"]; got != "result" {
		t.Fatalf("last event = %v, want result\n%s", got, output)
	}
}

func TestRunExecReviewRequiresOneScope(t *testing.T) {
	for _, args := range [][]string{
		{"exec", "review"},
		{"exec", "review", "--uncommitted", "--base", "main"},
		{"exec", "review", "--base", "main", "--commit", "abc123"},
	} {
		err := run(args)
		if wuuexec.ExitCode(err) != wuuexec.ExitInvalidInput {
			t.Fatalf("ExitCode(%v) = %d, err=%v", args, wuuexec.ExitCode(err), err)
		}
		if err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("unexpected review error for %v: %v", args, err)
		}
	}
}

func TestRunInitWritesDefaultConfig(t *testing.T) {
	workdir := t.TempDir()
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	t.Chdir(workdir)
	output := captureStdout(t, func() {
		if err := run([]string{"init", "--force"}); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})
	if !strings.Contains(output, "created") {
		t.Fatalf("expected created output, got %q", output)
	}
	configPath := filepath.Join(wuuHome, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("expected user config file: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("expected JSON config: %v", err)
	}
	if cfg.DefaultProvider == "" || len(cfg.Providers) == 0 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat user config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("user config mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(workdir, ".wuu.json")); !os.IsNotExist(err) {
		t.Fatalf("wuu init must not create a provider-bearing project config: %v", err)
	}
}

func TestLoadOrCreateAppServerConfigCreatesStarterConfig(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()

	cfg, configPath, err := loadOrCreateAppServerConfig(root, home)
	if err != nil {
		t.Fatalf("loadOrCreateAppServerConfig: %v", err)
	}

	expectedPath, err := statepath.ConfigPath(home)
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if configPath != expectedPath {
		t.Fatalf("expected config path %q, got %q", expectedPath, configPath)
	}
	if cfg.DefaultProvider != "openai-codex" {
		t.Fatalf("expected openai-codex default provider, got %q", cfg.DefaultProvider)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected one starter provider, got %+v", cfg.Providers)
	}
	if _, ok := cfg.Providers["openai-codex"]; !ok {
		t.Fatalf("starter config missing openai-codex provider: %+v", cfg.Providers)
	}
	if cfg.Providers["openai-codex"].ReuseCodexCredentials {
		t.Fatal("starter openai-codex must keep reuse_codex_credentials off until first-run setup confirms it")
	}

	loaded, loadedPath, err := config.LoadFrom(root, home)
	if err != nil {
		t.Fatalf("reload starter config: %v", err)
	}
	if loadedPath != configPath || loaded.DefaultProvider != "openai-codex" {
		t.Fatalf("unexpected reloaded config: path=%q cfg=%+v", loadedPath, loaded)
	}
	if loaded.Providers["openai-codex"].ReuseCodexCredentials {
		t.Fatal("reloaded starter openai-codex must keep reuse_codex_credentials off")
	}
}

func TestLoadOrCreateAppServerConfigUsesWUUHomeWhenHomeIsEmpty(t *testing.T) {
	root := t.TempDir()
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)

	_, configPath, err := loadOrCreateAppServerConfig(root, "")
	if err != nil {
		t.Fatalf("loadOrCreateAppServerConfig: %v", err)
	}
	wantPath := filepath.Join(wuuHome, "config.json")
	if configPath != wantPath {
		t.Fatalf("config path = %q, want %q", configPath, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("WUU_HOME starter config missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".wuu.json")); !os.IsNotExist(err) {
		t.Fatalf("app-server wrote starter config into project: %v", err)
	}
}

func TestResolveAppServerHostValidatesCloudLaunch(t *testing.T) {
	tests := []struct {
		name        string
		kind        string
		instanceID  string
		workspaceID string
		configFile  string
		wantErr     string
	}{
		{name: "local defaults", kind: "local"},
		{name: "local instance rejected", kind: "local", instanceID: "run-123", wantErr: "instance id"},
		{name: "cloud ready", kind: "cloud", instanceID: "run-123", workspaceID: "workspace-123", configFile: "/run/wuu/config.json"},
		{name: "cloud instance required", kind: "cloud", workspaceID: "workspace-123", configFile: "/run/wuu/config.json", wantErr: "instance id"},
		{name: "cloud workspace required", kind: "cloud", instanceID: "run-123", configFile: "/run/wuu/config.json", wantErr: "--workspace-id"},
		{name: "cloud config required", kind: "cloud", instanceID: "run-123", workspaceID: "workspace-123", wantErr: "--config"},
		{name: "unknown host", kind: "serverless", wantErr: "unsupported runtime host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, err := resolveAppServerHost(tt.kind, tt.instanceID, tt.workspaceID, tt.configFile)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveAppServerHost: %v", err)
			}
			if host.Kind != runtime.HostKind(tt.kind) {
				t.Fatalf("host = %+v", host)
			}
		})
	}
}

func TestLoadAppServerRuntimeConfigUsesExplicitFile(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	data, err := json.Marshal(appServerStarterConfig())
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cloud.json"), data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, configPath, mode, err := loadAppServerRuntimeConfig(root, home, "cloud.json")
	if err != nil {
		t.Fatalf("loadAppServerRuntimeConfig: %v", err)
	}
	if configPath != filepath.Join(root, "cloud.json") || mode != runtime.ConfigLoadFile {
		t.Fatalf("path=%q mode=%v", configPath, mode)
	}
	defaultPath, err := statepath.ConfigPath(home)
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if _, err := os.Stat(defaultPath); !os.IsNotExist(err) {
		t.Fatalf("explicit cloud config created local starter config: %v", err)
	}
}

func TestRunModelsRejectsUnsupportedProvider(t *testing.T) {
	workdir := t.TempDir()
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	if err := os.MkdirAll(wuuHome, 0o700); err != nil {
		t.Fatalf("mkdir WUU_HOME: %v", err)
	}
	configPath := filepath.Join(wuuHome, "config.json")
	data := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://example.com/v1",
      "api_key": "sk-test",
      "model": "gpt-test"
    }
  },
  "agent": {
    "system_prompt": "test"
  }
}`
	if err := os.WriteFile(configPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := run([]string{"models", "--workdir", workdir})
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
	if !strings.Contains(err.Error(), "openai-codex providers only") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunEvalListDoesNotRequireConfig(t *testing.T) {
	output := captureStdout(t, func() {
		if err := run([]string{"eval", "--list"}); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})

	if !strings.Contains(output, "test_failure_fix") ||
		!strings.Contains(output, "git_test_failure_fix") ||
		!strings.Contains(output, "multi_file_pricing") ||
		!strings.Contains(output, "long_process_output") ||
		!strings.Contains(output, "tool_search_deferred") ||
		!strings.Contains(output, "stale_read_guard") ||
		!strings.Contains(output, "mcp_readonly_concurrency") ||
		!strings.Contains(output, "mcp_live_discovery") ||
		!strings.Contains(output, "multi_agent_worker") {
		t.Fatalf("expected built-in eval tasks, got %q", output)
	}
}

func TestResolveEvalTasksSelectsCommaSeparatedIDs(t *testing.T) {
	tasks, err := resolveEvalTasks("test_failure_fix,multi_file_pricing")
	if err != nil {
		t.Fatalf("resolveEvalTasks: %v", err)
	}
	if len(tasks) != 2 || tasks[0].ID != "test_failure_fix" || tasks[1].ID != "multi_file_pricing" {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}
}

func TestResolveEvalTasksAllFiltersByActiveSurface(t *testing.T) {
	openaiSurface := modelprofile.DefaultCompiler{}.Compile(modelprofile.Resolve("openai", "gpt-5.5"), modelprofile.SurfaceMain)
	openaiTasks, err := resolveEvalTasks("all", evalVisibleToolSet(openaiSurface.ToolNames()))
	if err != nil {
		t.Fatalf("resolveEvalTasks openai: %v", err)
	}
	openaiIDs := evalTaskIDSet(openaiTasks)
	for _, want := range []string{"test_failure_fix", "patch_review_risk"} {
		if !openaiIDs[want] {
			t.Fatalf("OpenAI default eval tasks missing %s: %v", want, sortedEvalTaskIDs(openaiTasks))
		}
	}
	for _, excluded := range []string{"stale_read_guard", "mcp_live_discovery", "mcp_readonly_concurrency"} {
		if openaiIDs[excluded] {
			t.Fatalf("OpenAI default eval tasks must not include %s: %v", excluded, sortedEvalTaskIDs(openaiTasks))
		}
	}

	claudeSurface := modelprofile.DefaultCompiler{}.Compile(modelprofile.Resolve("anthropic", "claude-sonnet-4-5"), modelprofile.SurfaceMain)
	claudeTasks, err := resolveEvalTasks("all", evalVisibleToolSet(claudeSurface.ToolNames()))
	if err != nil {
		t.Fatalf("resolveEvalTasks claude: %v", err)
	}
	claudeIDs := evalTaskIDSet(claudeTasks)
	for _, want := range []string{"test_failure_fix", "stale_read_guard", "mcp_live_discovery"} {
		if !claudeIDs[want] {
			t.Fatalf("Claude default eval tasks missing %s: %v", want, sortedEvalTaskIDs(claudeTasks))
		}
	}
	for _, excluded := range []string{"patch_review_risk"} {
		if claudeIDs[excluded] {
			t.Fatalf("Claude default eval tasks must not include %s: %v", excluded, sortedEvalTaskIDs(claudeTasks))
		}
	}
}

func TestResolveEvalTasksExplicitIDBypassesSurfaceFilter(t *testing.T) {
	openaiSurface := modelprofile.DefaultCompiler{}.Compile(modelprofile.Resolve("openai", "gpt-5.5"), modelprofile.SurfaceMain)
	tasks, err := resolveEvalTasks("stale_read_guard", evalVisibleToolSet(openaiSurface.ToolNames()))
	if err != nil {
		t.Fatalf("resolveEvalTasks explicit: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "stale_read_guard" {
		t.Fatalf("explicit task selection should bypass default surface filter, got %v", sortedEvalTaskIDs(tasks))
	}
}

func TestSummarizeEvalResultsIncludesCacheMetrics(t *testing.T) {
	summary := summarizeEvalResults([]evalharness.Result{
		{Success: true, InputTokens: 70, OutputTokens: 10, CacheReadTokens: 30, CacheCreationTokens: 20},
		{Success: false, InputTokens: 30, OutputTokens: 5, CacheReadTokens: 70, CacheCreationTokens: 10},
	})
	if summary.Total != 2 || summary.Passed != 1 || summary.Failed != 1 {
		t.Fatalf("unexpected summary counts: %+v", summary)
	}
	if summary.InputTokens != 100 || summary.OutputTokens != 15 || summary.CacheReadTokens != 100 || summary.CacheCreationTokens != 30 {
		t.Fatalf("unexpected summary token metrics: %+v", summary)
	}
	if summary.CacheHitRate != evalharness.CacheHitRate(100, 100) {
		t.Fatalf("unexpected cache hit rate: %+v", summary)
	}
}

func evalTaskIDSet(tasks []evalharness.Task) map[string]bool {
	out := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		out[task.ID] = true
	}
	return out
}

func sortedEvalTaskIDs(tasks []evalharness.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.ID)
	}
	sort.Strings(out)
	return out
}

func TestResolveEvalTasksRejectsUnknownTask(t *testing.T) {
	_, err := resolveEvalTasks("missing")
	if err == nil || !strings.Contains(err.Error(), "unknown eval task") {
		t.Fatalf("expected unknown task error, got %v", err)
	}
}

func TestRunEvalLiveCodexOAuthSkipsWithoutCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex-missing"))

	output := captureStdout(t, func() {
		if err := run([]string{"eval", "--live-codex-oauth"}); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})

	if !strings.Contains(output, "SKIP live Codex OAuth eval") {
		t.Fatalf("expected skip output, got %q", output)
	}
}

func TestMissingRequiredTools(t *testing.T) {
	missing := missingRequiredTools([]string{"tool_search", "cron"}, []string{"tool_search", "write_file"})
	if len(missing) != 1 || missing[0] != "cron" {
		t.Fatalf("unexpected missing tools: %+v", missing)
	}
	if got := missingRequiredTools([]string{"tool_search"}, []string{"tool_search"}); len(got) != 0 {
		t.Fatalf("expected no missing tools, got %+v", got)
	}

	forbidden := forbiddenToolsUsed([]string{"deprecated_tool", "unsafe_tool"}, []string{"start_tool", "deprecated_tool", "deprecated_tool"})
	if len(forbidden) != 1 || forbidden[0] != "deprecated_tool" {
		t.Fatalf("unexpected forbidden tools: %+v", forbidden)
	}
}

func TestToolNameSequencePreservesRepeatedCalls(t *testing.T) {
	got := toolNameSequence([]tools.ToolExecutionRecord{
		{Name: "read_file"},
		{Name: "checkpoint"},
		{Name: "apply_patch"},
		{Name: "checkpoint"},
		{Name: "apply_patch"},
	})
	want := []string{"read_file", "checkpoint", "apply_patch", "checkpoint", "apply_patch"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool sequence mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestMissingRequiredToolErrors(t *testing.T) {
	required := []evalharness.ToolErrorRequirement{
		{ToolName: "edit_file", ErrorContains: "changed since last read"},
	}
	records := []tools.ToolExecutionRecord{
		{Name: "edit_file", Success: false, Error: "file changed since last read. Use read_file again before editing"},
	}
	if got := missingRequiredToolErrors(required, records); len(got) != 0 {
		t.Fatalf("expected no missing errors, got %+v", got)
	}

	missing := missingRequiredToolErrors(required, []tools.ToolExecutionRecord{
		{Name: "edit_file", Success: false, Error: "old_text not found"},
	})
	if len(missing) != 1 || missing[0] != "edit_file:changed since last read" {
		t.Fatalf("unexpected missing errors: %+v", missing)
	}
}

func TestMissingRequiredToolCalls(t *testing.T) {
	messages := []providers.ChatMessage{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{Name: "checkpoint", Arguments: `{"action":"create","checkpoint_id":"before_bad_edit","paths":["target.txt","scratch.txt"]}`},
				{Name: "write_file", Arguments: `{"path":"checkpoint_result.txt","content":"CHECKPOINT_ROLLBACK_DONE\n"}`},
			},
		},
	}
	required := []evalharness.ToolCallRequirement{
		{ToolName: "checkpoint", ArgumentEquals: map[string]string{"action": "create", "checkpoint_id": "before_bad_edit"}, ArgsContains: []string{"scratch.txt"}},
		{ToolName: "write_file", ArgumentEquals: map[string]string{"path": "checkpoint_result.txt"}},
	}
	if got := missingRequiredToolCalls(required, messages); len(got) != 0 {
		t.Fatalf("expected no missing tool calls, got %+v", got)
	}

	missing := missingRequiredToolCalls([]evalharness.ToolCallRequirement{
		{ToolName: "checkpoint", ArgumentEquals: map[string]string{"action": "restore", "checkpoint_id": "before_bad_edit"}},
	}, messages)
	if len(missing) != 1 || missing[0] != "checkpoint action=restore checkpoint_id=before_bad_edit" {
		t.Fatalf("unexpected missing tool calls: %+v", missing)
	}
}

func TestMissingRequiredToolSequence(t *testing.T) {
	messages := []providers.ChatMessage{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{Name: "checkpoint", Arguments: `{"action":"create","checkpoint_id":"before_bad_edit","paths":["target.txt","scratch.txt"]}`},
				{Name: "apply_patch", Arguments: "*** Update File: target.txt\n*** Add File: scratch.txt\n"},
			},
		},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{Name: "checkpoint", Arguments: `{"action":"restore","checkpoint_id":"before_bad_edit"}`},
				{Name: "apply_patch", Arguments: "*** Add File: checkpoint_result.txt\n"},
			},
		},
	}
	required := []evalharness.ToolCallRequirement{
		{ToolName: "checkpoint", ArgumentEquals: map[string]string{"action": "create", "checkpoint_id": "before_bad_edit"}},
		{ToolName: "apply_patch", ArgsContains: []string{"target.txt", "scratch.txt"}},
		{ToolName: "checkpoint", ArgumentEquals: map[string]string{"action": "restore", "checkpoint_id": "before_bad_edit"}},
		{ToolName: "apply_patch", ArgsContains: []string{"checkpoint_result.txt"}},
	}
	if got := missingRequiredToolSequence(required, messages); len(got) != 0 {
		t.Fatalf("expected no missing tool sequence, got %+v", got)
	}

	outOfOrder := []providers.ChatMessage{{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{
			{Name: "checkpoint", Arguments: `{"action":"restore","checkpoint_id":"before_bad_edit"}`},
			{Name: "checkpoint", Arguments: `{"action":"create","checkpoint_id":"before_bad_edit"}`},
		},
	}}
	missing := missingRequiredToolSequence(required[:2], outOfOrder)
	if len(missing) != 1 || missing[0] != "apply_patch contains=target.txt contains=scratch.txt" {
		t.Fatalf("unexpected missing sequence: %+v", missing)
	}
}

func TestEvalSafePreviewRedactsSecretsAndTruncates(t *testing.T) {
	got := evalSafePreview("access_token=secret-value-1234567890 keep", 200)
	if strings.Contains(got, "secret-value") {
		t.Fatalf("secret leaked in preview: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %q", got)
	}
	truncated := evalSafePreview(strings.Repeat("x", 40), 20)
	if !strings.Contains(truncated, "truncated") {
		t.Fatalf("expected truncation marker: %q", truncated)
	}
}

func TestEvalToolObservationsAreMetadataOnly(t *testing.T) {
	records := []tools.ToolExecutionRecord{{
		Name:                 "run_shell",
		CallID:               "call_1",
		ArgumentsSHA256:      strings.Repeat("c", 64),
		ResultAction:         "restore",
		Kind:                 tools.ToolKindShell,
		Exposure:             tools.ToolExposureDirect,
		Risk:                 tools.ToolRiskHigh,
		ClassificationReason: "destructive shell command",
		PolicyAction:         tools.ToolPolicyAllow,
		PolicyReason:         "risk policy",
		ReadOnly:             false,
		ConcurrencySafe:      false,
		DurationMS:           42,
		RevisionBefore:       "git:before:worktree:aaa",
		RevisionAfter:        "git:after:worktree:bbb",
		Success:              false,
		Error:                "authorization: bearer abc123",
		ErrorKind:            "boundary_denied",
		RawOutputBytes:       1024,
		ReturnedOutputBytes:  256,
		ResultBudgeted:       true,
		ResultRef:            "/tmp/wuu/tool-results/call_1.txt",
		ArtifactRefs:         []string{"/tmp/wuu/tool-results/call_1.txt", "/tmp/wuu/tool-results/run-test-logs/call_1.log"},
		PatchRiskSummary:     &tools.ToolPatchRisk{FileCount: 2, HunkCount: 2, AddedLines: 8, DeletedLines: 3, Actions: map[string]int{"update": 2}, MultiFile: true, RiskLevel: "medium"},
	}}

	got := evalToolObservations(records)
	if len(got) != 1 {
		t.Fatalf("expected one observation, got %+v", got)
	}
	if got[0].Name != "run_shell" || got[0].Kind != "shell" || got[0].RawOutputBytes != 1024 || !got[0].ResultBudgeted {
		t.Fatalf("metadata not preserved: %+v", got[0])
	}
	if got[0].ArgumentsSHA256 != records[0].ArgumentsSHA256 {
		t.Fatalf("argument fingerprint not preserved: %+v", got[0])
	}
	if got[0].ResultAction != "restore" {
		t.Fatalf("result action not preserved: %+v", got[0])
	}
	if got[0].PolicyReason != "risk policy" {
		t.Fatalf("policy reason not preserved: %+v", got[0])
	}
	if got[0].ClassificationReason != "destructive shell command" {
		t.Fatalf("classification reason not preserved: %+v", got[0])
	}
	if got[0].RevisionBefore != records[0].RevisionBefore || got[0].RevisionAfter != records[0].RevisionAfter {
		t.Fatalf("revisions not preserved: %+v", got[0])
	}
	if strings.Contains(got[0].Error, "abc123") {
		t.Fatalf("error secret leaked: %q", got[0].Error)
	}
	if got[0].ErrorKind != "boundary_denied" {
		t.Fatalf("error kind not preserved: %+v", got[0])
	}
	if got[0].ResultRef != records[0].ResultRef {
		t.Fatalf("result ref not preserved: %+v", got[0])
	}
	if !reflect.DeepEqual(got[0].ArtifactRefs, records[0].ArtifactRefs) {
		t.Fatalf("artifact refs not preserved: %+v", got[0])
	}
	if got[0].PatchRiskSummary == nil ||
		got[0].PatchRiskSummary.RiskLevel != "medium" ||
		got[0].PatchRiskSummary.Actions["update"] != 2 ||
		!got[0].PatchRiskSummary.MultiFile {
		t.Fatalf("patch risk summary not preserved: %+v", got[0].PatchRiskSummary)
	}
	if got[0].ResultEnvelope == nil || got[0].ResultEnvelope.DataRef != records[0].ResultRef {
		t.Fatalf("result envelope missing ref: %+v", got[0].ResultEnvelope)
	}
	if got[0].ResultEnvelope.Revision != records[0].RevisionAfter {
		t.Fatalf("result envelope missing revision: %+v", got[0].ResultEnvelope)
	}
	artifactRefs, ok := got[0].ResultEnvelope.Data["artifact_refs"].([]string)
	if !ok || !reflect.DeepEqual(artifactRefs, records[0].ArtifactRefs) {
		t.Fatalf("result envelope missing artifact refs: %+v", got[0].ResultEnvelope)
	}
	if got[0].ResultEnvelope.Data["error_kind"] != records[0].ErrorKind {
		t.Fatalf("result envelope missing error kind: %+v", got[0].ResultEnvelope)
	}
	if got[0].ResultEnvelope.Data["arguments_sha256"] != records[0].ArgumentsSHA256 {
		t.Fatalf("result envelope missing argument fingerprint: %+v", got[0].ResultEnvelope)
	}
	if got[0].ResultEnvelope.Data["result_action"] != records[0].ResultAction {
		t.Fatalf("result envelope missing result action: %+v", got[0].ResultEnvelope)
	}
	rawEnvelope, err := json.Marshal(got[0].ResultEnvelope)
	if err != nil {
		t.Fatalf("marshal result envelope: %v", err)
	}
	if strings.Contains(string(rawEnvelope), "abc123") || strings.Contains(string(rawEnvelope), "authorization") {
		t.Fatalf("result envelope leaked raw error: %s", string(rawEnvelope))
	}
}

func TestEvalToolInventoryObservationsAreSchemaFree(t *testing.T) {
	infos := []tools.ToolInfo{{
		Name:            "read_file",
		Kind:            tools.ToolKindFile,
		Exposure:        tools.ToolExposureDirect,
		Risk:            tools.ToolRiskLow,
		ReadOnly:        true,
		ConcurrencySafe: true,
		Reason:          "safe metadata without schema",
	}}

	got := evalToolInventoryObservations(infos)
	if len(got) != 1 {
		t.Fatalf("expected one tool inventory item, got %+v", got)
	}
	if got[0].Name != "read_file" || got[0].Kind != "file" || got[0].Exposure != "direct" || got[0].Risk != "low" || !got[0].ReadOnly || !got[0].ConcurrencySafe {
		t.Fatalf("tool inventory metadata not preserved: %+v", got[0])
	}
	raw, err := json.Marshal(got[0])
	if err != nil {
		t.Fatalf("marshal tool inventory: %v", err)
	}
	for _, forbidden := range []string{"description", "input_schema", "parameters", "properties"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("tool inventory leaked schema-like field %q: %s", forbidden, string(raw))
		}
	}
}

func TestEvalModelProfileObservation(t *testing.T) {
	got := evalModelProfileObservation(&runtime.Session{
		ProviderName: "openai",
		Model:        "gpt-5-codex",
	})

	if got == nil {
		t.Fatal("expected model profile observation")
	}
	if got.ProviderName != "openai" || got.Model != "gpt-5-codex" || got.Family != "codex" {
		t.Fatalf("unexpected model profile identity: %+v", got)
	}
	if got.DefaultWriteMode != "patch" || !got.FreeformTool || !got.AllowParallelReadOnly {
		t.Fatalf("unexpected model profile strategy: %+v", got)
	}
}

func TestEvalContextBlockObservationsSummarizeRuntimeBlocks(t *testing.T) {
	t.Setenv(wuucontext.DerivedContextLedgersEnvVar, "on")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/eval\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nconst token = \"super-secret-token\"\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	kit, err := tools.New(root)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"main.go","offset":1,"limit":3}`,
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}

	got := evalContextBlockObservations(&runtime.Session{
		RootDir: root,
		Toolkit: kit,
	})
	byKind := make(map[string]evalharness.ContextBlockObservation)
	for _, block := range got {
		if block.Kind == "" {
			t.Fatalf("context block kind missing: %+v", block)
		}
		if block.ContentBytes <= 0 || strings.TrimSpace(block.ContentPreview) == "" {
			t.Fatalf("context block missing preview metadata: %+v", block)
		}
		if strings.Contains(block.ContentPreview, "super-secret-token") {
			t.Fatalf("context block preview leaked file body: %+v", block)
		}
		byKind[block.Kind] = block
	}
	for _, kind := range []string{"ACTIVE_FILES", "TOOL_RESULT_SUMMARY"} {
		if _, ok := byKind[kind]; !ok {
			t.Fatalf("missing context block kind %s in %+v", kind, got)
		}
	}
	if _, ok := byKind["ENVIRONMENT"]; ok {
		t.Fatalf("stable environment should not be reported as runtime context block: %+v", got)
	}
	active := byKind["ACTIVE_FILES"]
	if active.Source != "read_file" || active.TokenBudget == 0 || !strings.Contains(active.ContentPreview, "files: current=1") {
		t.Fatalf("active files block missing read metadata: %+v", active)
	}
}

func TestPersistEvalTraceWritesSessionArtifact(t *testing.T) {
	sessionDir := t.TempDir()
	result := evalharness.Result{
		TaskID:   "task-1",
		TaskName: "Task One",
		Observability: &evalharness.Observability{
			SessionDir:         sessionDir,
			FinalAnswerPreview: "done",
			ModelProfile:       &evalharness.ModelProfileObservation{ProviderName: "openai", Model: "gpt-5-codex", Family: "codex"},
		},
	}

	persistEvalTrace(&result)

	if result.Observability.TracePath == "" {
		t.Fatalf("trace path not recorded: %+v", result.Observability)
	}
	data, err := os.ReadFile(result.Observability.TracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if !strings.Contains(string(data), `"type":"model_profile"`) || !strings.Contains(string(data), `"type":"final"`) {
		t.Fatalf("trace missing expected events:\n%s", string(data))
	}
}

func TestRunEvalReplayTraceJSON(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "eval-trace.jsonl")
	if err := evalharness.WriteTrace(tracePath, evalharness.Result{
		TaskID:             "task-1",
		TaskName:           "Task One",
		Success:            true,
		VerificationReason: "passed",
		Observability: &evalharness.Observability{
			SessionID:          "eval-task-1",
			SessionDir:         filepath.Dir(tracePath),
			TracePath:          tracePath,
			FinalAnswerPreview: "done",
			ModelProfile:       &evalharness.ModelProfileObservation{ProviderName: "openai", Model: "gpt-5-codex", Family: "codex"},
			ContextBlocks: []evalharness.ContextBlockObservation{{
				Kind:           "TOOL_RESULT_SUMMARY",
				Source:         "tool_telemetry",
				TokenBudget:    800,
				ContentPreview: "recent_tool_calls:",
			}},
			ToolRecords: []evalharness.ToolObservation{{
				Name:            "read_file",
				ArgumentsSHA256: strings.Repeat("c", 64),
				Success:         true,
			}, {
				Name:            "read_file",
				ArgumentsSHA256: strings.Repeat("c", 64),
				Success:         true,
			}},
		},
	}); err != nil {
		t.Fatalf("write trace: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "replay.json")

	output := captureStdout(t, func() {
		if err := run([]string{"eval", "--replay-trace", tracePath, "--json", "--output", outputPath}); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})

	var stdoutSummary evalharness.TraceReplaySummary
	if err := json.Unmarshal([]byte(output), &stdoutSummary); err != nil {
		t.Fatalf("expected replay JSON output, got %q: %v", output, err)
	}
	if stdoutSummary.Task == nil || stdoutSummary.Task.ID != "task-1" || stdoutSummary.Final == nil || !stdoutSummary.Final.Success {
		t.Fatalf("unexpected stdout replay summary: %+v", stdoutSummary)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read replay output: %v", err)
	}
	var fileSummary evalharness.TraceReplaySummary
	if err := json.Unmarshal(data, &fileSummary); err != nil {
		t.Fatalf("parse replay output file: %v", err)
	}
	if fileSummary.Mode != "deterministic_trace_replay" || len(fileSummary.ToolNames) != 2 || fileSummary.ToolNames[0] != "read_file" || fileSummary.ToolNames[1] != "read_file" {
		t.Fatalf("unexpected replay output file: %+v", fileSummary)
	}
	if len(fileSummary.ContextBlocks) != 1 ||
		fileSummary.ContextBlocks[0].Kind != "TOOL_RESULT_SUMMARY" ||
		fileSummary.ContextBlocks[0].ContentPreview != "recent_tool_calls:" {
		t.Fatalf("replay output should include context block observations: %+v", fileSummary.ContextBlocks)
	}
	if fileSummary.ToolSummary == nil ||
		len(fileSummary.ToolSummary.RepeatedArguments) != 1 ||
		fileSummary.ToolSummary.RepeatedArguments[0].ToolName != "read_file" ||
		fileSummary.ToolSummary.RepeatedArguments[0].ArgumentsSHA256 != strings.Repeat("c", 64) ||
		fileSummary.ToolSummary.RepeatedArguments[0].Count != 2 {
		t.Fatalf("replay output should include repeated argument summary: %+v", fileSummary.ToolSummary)
	}
}

func TestRunEvalReplayTraceTextPrintsPolicyBlocks(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "eval-trace.jsonl")
	if err := evalharness.WriteTrace(tracePath, evalharness.Result{
		TaskID:             "task-1",
		TaskName:           "Task One",
		Success:            true,
		ForbiddenToolsUsed: []string{"deprecated_tool"},
		VerificationEvidence: []evalharness.VerificationEvidence{{
			Check:   "go tests",
			Passed:  true,
			Command: "go test ./...",
		}},
		Observability: &evalharness.Observability{
			ToolRecords: []evalharness.ToolObservation{{
				Name:                 "bash",
				CallID:               "call-test",
				ResultAction:         "run",
				ClassificationReason: "local verification command",
				Success:              true,
			}, {
				Name:            "bash",
				CallID:          "call-process",
				ResultAction:    "start_background",
				Kind:            "shell",
				Risk:            "high",
				PolicyAction:    "deny",
				ErrorKind:       "boundary_denied",
				ArgumentsSHA256: strings.Repeat("e", 64),
				Success:         false,
			}},
			Attention: []evalharness.AttentionObservation{{
				Source:  "harness_report",
				ID:      "report-1",
				Status:  "partial",
				Message: "tests failed",
				Path:    "/tmp/wuu/harness/report-1.md",
			}},
		},
	}); err != nil {
		t.Fatalf("write trace: %v", err)
	}

	output := captureStdout(t, func() {
		if err := run([]string{"eval", "--replay-trace", tracePath}); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})

	if !strings.Contains(output, "policy_blocks: bash:deny:boundary_denied:call_id=call-process") {
		t.Fatalf("replay text output missing policy blocks:\n%s", output)
	}
	if !strings.Contains(output, "attention: harness_report:id=report-1:status=partial:path=/tmp/wuu/harness/report-1.md:message=tests failed") {
		t.Fatalf("replay text output missing attention:\n%s", output)
	}
	if !strings.Contains(output, "forbidden_tools: deprecated_tool") {
		t.Fatalf("replay text output missing forbidden tools:\n%s", output)
	}
	if !strings.Contains(output, "validation: status=incomplete tools=1 evidence=1 missing=2 failures=0") {
		t.Fatalf("replay text output missing validation summary:\n%s", output)
	}
	if !strings.Contains(output, "validation_evidence: go tests:passed:command=go test ./...") {
		t.Fatalf("replay text output missing validation evidence:\n%s", output)
	}
	if !strings.Contains(output, "validation_missing: forbidden_tool:deprecated_tool") {
		t.Fatalf("replay text output missing validation missing requirements:\n%s", output)
	}
	if !strings.Contains(output, "attention_issue:harness_report:report-1:status=partial:path=/tmp/wuu/harness/report-1.md") {
		t.Fatalf("replay text output missing attention validation issue:\n%s", output)
	}
	if strings.Contains(output, strings.Repeat("e", 64)) {
		t.Fatalf("replay text output should not print argument fingerprints by default:\n%s", output)
	}
}

func TestApplyEvalAttentionIssuesFailsResult(t *testing.T) {
	result := evalharness.Result{
		TaskID:  "task-1",
		Success: true,
		Observability: &evalharness.Observability{
			Attention: []evalharness.AttentionObservation{{
				Source:  "harness_report",
				ID:      "report-1",
				Status:  "partial",
				Message: "tests failed",
			}},
		},
	}

	applyEvalAttentionIssues(&result)

	if result.Success {
		t.Fatalf("harness attention should fail eval result: %+v", result)
	}
}

func TestRunEvalReplaySessionTraceJSON(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "session-trace.jsonl")
	if err := sessiontrace.AppendTurn(tracePath,
		sessiontrace.TurnRecord{
			ThreadID:     "thread-1",
			TurnID:       "turn-1",
			Status:       "completed",
			ProviderName: "openai",
			Model:        "gpt-test",
		},
		sessiontrace.FinalRecord{Status: "completed", FinalAnswerPreview: "done"},
		[]tools.ToolInfo{{Name: "grep", Kind: tools.ToolKindSearch, Risk: tools.ToolRiskLow, ReadOnly: true}},
		[]tools.ToolExecutionRecord{{
			Name:            "grep",
			ArgumentsSHA256: strings.Repeat("d", 64),
			Kind:            tools.ToolKindSearch,
			Success:         true,
		}, {
			Name:            "grep",
			ArgumentsSHA256: strings.Repeat("d", 64),
			Kind:            tools.ToolKindSearch,
			Success:         true,
		}},
		nil,
		nil,
		nil,
	); err != nil {
		t.Fatalf("write session trace: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "session-replay.json")

	output := captureStdout(t, func() {
		if err := run([]string{"eval", "--replay-trace", tracePath, "--json", "--output", outputPath}); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})

	var stdoutSummary sessiontrace.ReplaySummary
	if err := json.Unmarshal([]byte(output), &stdoutSummary); err != nil {
		t.Fatalf("expected session replay JSON output, got %q: %v", output, err)
	}
	if stdoutSummary.Mode != "session_trace_replay" || stdoutSummary.LatestTurn == nil || stdoutSummary.LatestTurn.ThreadID != "thread-1" {
		t.Fatalf("unexpected stdout session replay summary: %+v", stdoutSummary)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read session replay output: %v", err)
	}
	var fileSummary sessiontrace.ReplaySummary
	if err := json.Unmarshal(data, &fileSummary); err != nil {
		t.Fatalf("parse session replay output file: %v", err)
	}
	if len(fileSummary.ToolNames) != 2 || fileSummary.ToolNames[0] != "grep" || fileSummary.ToolNames[1] != "grep" || fileSummary.Final == nil || fileSummary.Final.Status != "completed" {
		t.Fatalf("unexpected session replay output file: %+v", fileSummary)
	}
	if fileSummary.ToolSummary == nil ||
		len(fileSummary.ToolSummary.RepeatedArguments) != 1 ||
		fileSummary.ToolSummary.RepeatedArguments[0].ToolName != "grep" ||
		fileSummary.ToolSummary.RepeatedArguments[0].ArgumentsSHA256 != strings.Repeat("d", 64) ||
		fileSummary.ToolSummary.RepeatedArguments[0].Count != 2 {
		t.Fatalf("session replay output should include repeated argument summary: %+v", fileSummary.ToolSummary)
	}
}

func TestRunEvalReplaySessionTraceTextPrintsPolicyBlocks(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "session-trace.jsonl")
	if err := sessiontrace.AppendTurn(tracePath,
		sessiontrace.TurnRecord{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Status:   "completed",
		},
		sessiontrace.FinalRecord{Status: "completed", FinalAnswerPreview: "done"},
		nil,
		[]tools.ToolExecutionRecord{{
			Name:            "run_shell",
			CallID:          "call-shell",
			Kind:            tools.ToolKindShell,
			Risk:            tools.ToolRiskHigh,
			PolicyAction:    tools.ToolPolicyDeny,
			ErrorKind:       "policy_denied",
			ArgumentsSHA256: strings.Repeat("f", 64),
			Success:         false,
		}},
		nil,
		nil,
		nil,
	); err != nil {
		t.Fatalf("write session trace: %v", err)
	}

	output := captureStdout(t, func() {
		if err := run([]string{"eval", "--replay-trace", tracePath}); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})

	if !strings.Contains(output, "policy_blocks: run_shell:deny:policy_denied:call_id=call-shell") {
		t.Fatalf("session replay text output missing policy blocks:\n%s", output)
	}
	if strings.Contains(output, strings.Repeat("f", 64)) {
		t.Fatalf("session replay text output should not print argument fingerprints by default:\n%s", output)
	}
}

func TestSetTemporaryEnvRestoresPreviousValue(t *testing.T) {
	t.Setenv("WUU_HOME", "/tmp/original-wuu-home")
	restore := setTemporaryEnv("WUU_HOME", "/tmp/eval-wuu-home")
	if got := os.Getenv("WUU_HOME"); got != "/tmp/eval-wuu-home" {
		t.Fatalf("WUU_HOME = %q, want temporary value", got)
	}
	restore()
	if got := os.Getenv("WUU_HOME"); got != "/tmp/original-wuu-home" {
		t.Fatalf("WUU_HOME = %q, want original value", got)
	}
}

func TestSetTemporaryEnvUnsetsMissingValue(t *testing.T) {
	t.Setenv("WUU_HOME", "placeholder")
	os.Unsetenv("WUU_HOME")
	restore := setTemporaryEnv("WUU_HOME", "/tmp/eval-wuu-home")
	if got := os.Getenv("WUU_HOME"); got != "/tmp/eval-wuu-home" {
		t.Fatalf("WUU_HOME = %q, want temporary value", got)
	}
	restore()
	if _, ok := os.LookupEnv("WUU_HOME"); ok {
		t.Fatal("WUU_HOME should be unset after restore")
	}
}

func TestResolveContextWindow_PrefersProviderContextOverride(t *testing.T) {
	provider := config.ProviderConfig{
		ContextWindow: 777,
		Models: map[string]config.ProviderModelConfig{
			"gpt-5.4": {
				Limit: &config.ProviderModelLimitConfig{Context: 1_050_000},
			},
		},
	}
	if got := runtime.ResolveContextWindow("gpt-5.4", provider, 555); got != 777 {
		t.Fatalf("expected provider context override, got %d", got)
	}
}

func TestResolveContextWindow_FallsBackToAgentOverride(t *testing.T) {
	if got := runtime.ResolveContextWindow("gpt-5.4", config.ProviderConfig{}, 555); got != 555 {
		t.Fatalf("expected agent override, got %d", got)
	}
}

func TestResolveContextWindow_UnknownWithoutProviderMetadata(t *testing.T) {
	if got := runtime.ResolveContextWindow("gpt-5.4", config.ProviderConfig{}, 0); got != 0 {
		t.Fatalf("expected unknown context window without provider metadata, got %d", got)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	defer r.Close()

	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}

	return strings.TrimSpace(buf.String())
}

func withStdin[T any](t *testing.T, text string, fn func() T) T {
	t.Helper()

	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdin pipe: %v", err)
	}
	if _, err := io.WriteString(w, text); err != nil {
		t.Fatalf("write stdin pipe: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	os.Stdin = r
	defer func() {
		os.Stdin = oldStdin
		_ = r.Close()
	}()
	return fn()
}

type cliExecFakeController struct {
	initResult appserver.InitializeResult
	thread     appserver.Thread
	run        appserver.Run
	events     []wuuexec.Notification

	startedThread  bool
	startEphemeral bool
	resumedThread  string
	forkedThread   string
	startedPrompt  string
	startedImages  []appserver.TurnStartImage
	startedFiles   []appserver.TurnStartFile
}

func newCLIExecFakeController(events ...wuuexec.Notification) *cliExecFakeController {
	return &cliExecFakeController{
		initResult: appserver.InitializeResult{
			ProtocolVersion: appserver.ProtocolVersion,
			Provider:        "test-provider",
			Model:           "test-model",
			WorkspaceRoot:   "/repo",
			Permissions: appserver.PermissionSummary{
				Mode: "standard",
			},
		},
		thread: appserver.Thread{ID: "thread-1", ModelProvider: "test-provider", Model: "test-model", CWD: "/repo"},
		run: appserver.Run{
			ID: "run-1", Status: execution.StatusRunning, ThreadID: "thread-1",
			Turns: []execution.TurnRef{{TurnID: "turn-1", ThreadID: "thread-1"}},
		},
		events: events,
	}
}

func (f *cliExecFakeController) Initialize(context.Context) (appserver.InitializeResult, error) {
	return f.initResult, nil
}

func (f *cliExecFakeController) StartThread(_ context.Context, ephemeral bool) (appserver.Thread, error) {
	f.startedThread = true
	f.startEphemeral = ephemeral
	f.thread.Ephemeral = ephemeral
	return f.thread, nil
}

func (f *cliExecFakeController) ResumeThread(_ context.Context, id string) (appserver.Thread, error) {
	f.resumedThread = id
	return f.thread, nil
}

func (f *cliExecFakeController) ForkThread(_ context.Context, id string) (appserver.Thread, error) {
	f.forkedThread = id
	f.thread.ID = "fork-thread-1"
	f.thread.ForkedFromID = id
	return f.thread, nil
}

func (f *cliExecFakeController) StartRun(_ context.Context, params appserver.RunStartParams) (appserver.Run, error) {
	f.startedPrompt = params.Prompt
	f.startedImages = append([]appserver.TurnStartImage(nil), params.Images...)
	f.startedFiles = append([]appserver.TurnStartFile(nil), params.Files...)
	return f.run, nil
}

func (f *cliExecFakeController) InterruptRun(_ context.Context, _ string, _ string) (appserver.Run, error) {
	return f.run, nil
}

func (f *cliExecFakeController) Shutdown(context.Context) error {
	return nil
}

// Notifications mirrors the in-process app-server: turn/started, the
// scripted events, then a synthesized completed run/updated unless the
// script already settles the Run.
func (f *cliExecFakeController) Notifications() <-chan wuuexec.Notification {
	hasTurnStarted := false
	hasRunUpdated := false
	lastTurnID := "turn-1"
	lastTrace := ""
	for _, ev := range f.events {
		switch ev.Method {
		case appserver.NotificationTurnStarted:
			hasTurnStarted = true
		case appserver.NotificationRunUpdated:
			hasRunUpdated = true
		case appserver.NotificationTurnCompleted:
			var params appserver.TurnCompletedNotification
			if err := json.Unmarshal(ev.Params, &params); err == nil {
				lastTurnID = params.Turn.ID
				lastTrace = params.TracePath
			}
		}
	}
	events := make([]wuuexec.Notification, 0, len(f.events)+2)
	if !hasTurnStarted {
		events = append(events, cliExecNotification(appserver.NotificationTurnStarted, appserver.TurnStartedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}}))
	}
	events = append(events, f.events...)
	if !hasRunUpdated {
		events = append(events, cliExecNotification(appserver.NotificationRunUpdated, appserver.RunUpdatedNotification{Run: appserver.Run{
			ID: "run-1", ThreadID: "thread-1", Status: execution.StatusCompleted,
			Result: &execution.Result{FinalTurnID: lastTurnID, TracePath: lastTrace},
		}}))
	}
	ch := make(chan wuuexec.Notification, len(events))
	for _, event := range events {
		ch <- event
	}
	return ch
}

func installExecControllerOverride(t *testing.T, controller wuuexec.Controller) func() {
	t.Helper()
	previous := execControllerOverride
	execControllerOverride = controller
	return func() {
		execControllerOverride = previous
	}
}

type fakeDebugAppServerClient struct {
	opts          debugAppServerOptions
	calls         []fakeDebugCall
	results       map[string]json.RawMessage
	resultQueues  map[string][]json.RawMessage
	notifications chan wuuexec.Notification
	shutdown      bool
}

type fakeDebugCall struct {
	method string
	params json.RawMessage
}

func (f *fakeDebugAppServerClient) Call(_ context.Context, method string, params any, result any) error {
	var rawParams json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return err
		}
		rawParams = append(json.RawMessage(nil), data...)
	}
	f.calls = append(f.calls, fakeDebugCall{method: method, params: rawParams})

	data := f.results[method]
	if queued := f.resultQueues[method]; len(queued) > 0 {
		data = queued[0]
		f.resultQueues[method] = queued[1:]
	}
	if len(data) == 0 {
		data = json.RawMessage(`null`)
	}
	if result == nil {
		return nil
	}
	if rawResult, ok := result.(*json.RawMessage); ok {
		*rawResult = append(json.RawMessage(nil), data...)
		return nil
	}
	return json.Unmarshal(data, result)
}

func (f *fakeDebugAppServerClient) Shutdown(context.Context) error {
	f.shutdown = true
	return nil
}

func (f *fakeDebugAppServerClient) Notifications() <-chan wuuexec.Notification {
	return f.notifications
}

func (f *fakeDebugAppServerClient) SandboxDir() string {
	return "/sandbox/wuu-home"
}

func installDebugAppServerClientOverride(t *testing.T, client *fakeDebugAppServerClient) func() {
	t.Helper()
	previous := debugAppServerClientOverride
	debugAppServerClientOverride = func(_ context.Context, opts debugAppServerOptions) (debugAppServerClient, error) {
		client.opts = opts
		return client, nil
	}
	return func() {
		debugAppServerClientOverride = previous
	}
}

func cliExecNotification(method string, params any) wuuexec.Notification {
	data, err := json.Marshal(params)
	if err != nil {
		panic(err)
	}
	return wuuexec.Notification{Method: method, Params: data}
}

func parseCLIJSONLines(t *testing.T, text string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(text), "\n")
	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func TestRunSessionShowReturnsCreatedSessionJSON(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	sessDir := statepath.SessionsDir(home)
	if sessDir == "" {
		t.Fatal("statepath.SessionsDir returned empty path")
	}

	const id = "cli-thread-1"
	sess, err := session.CreateWithMetadata(sessDir, id, "/tmp/workdir")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.AppendHistoryRecord(sessDir, sess.ID, session.HistoryRecord{
		Role:    "user",
		Content: "hello from CLI",
	}); err != nil {
		t.Fatalf("append history: %v", err)
	}

	output := captureStdout(t, func() {
		if err := run([]string{"session-show", "--thread", sess.ID, "--json"}); err != nil {
			t.Fatalf("run session-show: %v", err)
		}
	})

	var payload struct {
		ThreadID string                  `json:"thread_id"`
		Session  session.Session         `json:"session"`
		History  []session.HistoryRecord `json:"history"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse JSON: %v\noutput: %s", err, output)
	}
	if payload.ThreadID != sess.ID {
		t.Errorf("expected thread_id %q, got %q", sess.ID, payload.ThreadID)
	}
	if len(payload.History) != 1 || payload.History[0].Content != "hello from CLI" {
		t.Errorf("unexpected history: %+v", payload.History)
	}
}

func TestRunSessionListJSONFiltersCurrentWorkspace(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	workdir := t.TempDir()
	otherWorkdir := t.TempDir()
	t.Chdir(workdir)

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	sessDir := statepath.SessionsDir(home)
	if _, err := session.CreateWithMetadata(sessDir, "current-thread", workdir); err != nil {
		t.Fatalf("create current session: %v", err)
	}
	if _, err := session.CreateWithMetadata(sessDir, "other-thread", otherWorkdir); err != nil {
		t.Fatalf("create other session: %v", err)
	}

	output := captureStdout(t, func() {
		if err := run([]string{"session", "list", "--json"}); err != nil {
			t.Fatalf("run session list: %v", err)
		}
	})

	var payload struct {
		Sessions []session.Session `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse JSON: %v\noutput: %s", err, output)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "current-thread" {
		t.Fatalf("unexpected sessions: %+v", payload.Sessions)
	}
}

func TestRunSessionShowSubcommandReturnsHistoryJSON(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	workdir := t.TempDir()

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	sessDir := statepath.SessionsDir(home)
	sess, err := session.CreateWithMetadata(sessDir, "show-thread", workdir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.AppendHistoryRecord(sessDir, sess.ID, session.HistoryRecord{
		Role:    "assistant",
		Content: "visible answer",
	}); err != nil {
		t.Fatalf("append history: %v", err)
	}

	output := captureStdout(t, func() {
		if err := run([]string{"session", "show", "--json", sess.ID}); err != nil {
			t.Fatalf("run session show: %v", err)
		}
	})

	var payload struct {
		ThreadID string                  `json:"thread_id"`
		History  []session.HistoryRecord `json:"history"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse JSON: %v\noutput: %s", err, output)
	}
	if payload.ThreadID != sess.ID || len(payload.History) != 1 || payload.History[0].Content != "visible answer" {
		t.Fatalf("unexpected session show payload: %+v", payload)
	}
}

func TestRunSessionShowLastReturnsMostRecentForWorkspace(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	workdir := t.TempDir()

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	sessDir := statepath.SessionsDir(home)
	if _, err := session.CreateWithMetadata(sessDir, "older-thread", workdir); err != nil {
		t.Fatalf("create older session: %v", err)
	}
	latest, err := session.CreateWithMetadata(sessDir, "latest-thread", workdir)
	if err != nil {
		t.Fatalf("create latest session: %v", err)
	}
	if err := session.AppendHistoryRecord(sessDir, latest.ID, session.HistoryRecord{
		Role:    "assistant",
		Content: "latest answer",
	}); err != nil {
		t.Fatalf("append history: %v", err)
	}

	output := captureStdout(t, func() {
		if err := run([]string{"session", "show", "--json", "--last", "--workdir", workdir}); err != nil {
			t.Fatalf("run session show --last: %v", err)
		}
	})

	var payload struct {
		ThreadID string                  `json:"thread_id"`
		History  []session.HistoryRecord `json:"history"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse JSON: %v\noutput: %s", err, output)
	}
	if payload.ThreadID != latest.ID || len(payload.History) != 1 || payload.History[0].Content != "latest answer" {
		t.Fatalf("unexpected session show payload: %+v", payload)
	}
}

func TestRunSessionSearchJSONMatchesHistoryAndFiltersWorkspace(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	workdir := t.TempDir()
	otherWorkdir := t.TempDir()
	t.Chdir(workdir)

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	sessDir := statepath.SessionsDir(home)
	current, err := session.CreateWithMetadata(sessDir, "search-current", workdir)
	if err != nil {
		t.Fatalf("create current session: %v", err)
	}
	if err := session.AppendHistoryRecord(sessDir, current.ID, session.HistoryRecord{
		Role:    "user",
		Content: "please fix the orion cache regression",
	}); err != nil {
		t.Fatalf("append current history: %v", err)
	}
	other, err := session.CreateWithMetadata(sessDir, "search-other", otherWorkdir)
	if err != nil {
		t.Fatalf("create other session: %v", err)
	}
	if err := session.AppendHistoryRecord(sessDir, other.ID, session.HistoryRecord{
		Role:    "user",
		Content: "orion cache but another workspace",
	}); err != nil {
		t.Fatalf("append other history: %v", err)
	}

	output := captureStdout(t, func() {
		if err := run([]string{"session", "search", "--json", "orion cache"}); err != nil {
			t.Fatalf("run session search: %v", err)
		}
	})

	var payload struct {
		Query   string `json:"query"`
		Results []struct {
			ThreadID string          `json:"thread_id"`
			Session  session.Session `json:"session"`
			Snippet  string          `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse JSON: %v\noutput: %s", err, output)
	}
	if payload.Query != "orion cache" {
		t.Fatalf("query = %q", payload.Query)
	}
	if len(payload.Results) != 1 || payload.Results[0].ThreadID != current.ID {
		t.Fatalf("unexpected search results: %+v", payload.Results)
	}
	if !strings.Contains(payload.Results[0].Snippet, "orion cache") {
		t.Fatalf("snippet should contain match context: %+v", payload.Results[0])
	}
}

func TestRunSessionArchiveHidesSessionFromList(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	workdir := t.TempDir()
	t.Chdir(workdir)

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	sessDir := statepath.SessionsDir(home)
	sess, err := session.CreateWithMetadata(sessDir, "archive-thread", workdir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	archiveOutput := captureStdout(t, func() {
		if err := run([]string{"session", "archive", "--json", sess.ID}); err != nil {
			t.Fatalf("run session archive: %v", err)
		}
	})
	var archivePayload struct {
		ThreadID string          `json:"thread_id"`
		Session  session.Session `json:"session"`
		Archived bool            `json:"archived"`
	}
	if err := json.Unmarshal([]byte(archiveOutput), &archivePayload); err != nil {
		t.Fatalf("parse archive JSON: %v\noutput: %s", err, archiveOutput)
	}
	if archivePayload.ThreadID != sess.ID || !archivePayload.Archived || archivePayload.Session.ArchivedAt == nil {
		t.Fatalf("unexpected archive payload: %+v", archivePayload)
	}

	listOutput := captureStdout(t, func() {
		if err := run([]string{"session", "list", "--json"}); err != nil {
			t.Fatalf("run session list: %v", err)
		}
	})
	var listPayload struct {
		Sessions []session.Session `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(listOutput), &listPayload); err != nil {
		t.Fatalf("parse list JSON: %v\noutput: %s", err, listOutput)
	}
	if len(listPayload.Sessions) != 0 {
		t.Fatalf("archived session should be hidden from default list: %+v", listPayload.Sessions)
	}

	includeArchivedOutput := captureStdout(t, func() {
		if err := run([]string{"session", "list", "--json", "--include-archived"}); err != nil {
			t.Fatalf("run session list include archived: %v", err)
		}
	})
	if err := json.Unmarshal([]byte(includeArchivedOutput), &listPayload); err != nil {
		t.Fatalf("parse include archived JSON: %v\noutput: %s", err, includeArchivedOutput)
	}
	if len(listPayload.Sessions) != 1 || listPayload.Sessions[0].ID != sess.ID {
		t.Fatalf("include archived should return archived session: %+v", listPayload.Sessions)
	}
}

func TestRunSessionDeleteRemovesSessionAndArtifacts(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	workdir := t.TempDir()
	t.Chdir(workdir)

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	sessDir := statepath.SessionsDir(home)
	sess, err := session.CreateWithMetadata(sessDir, "delete-thread", workdir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.AppendHistoryRecord(sessDir, sess.ID, session.HistoryRecord{Role: "user", Content: "temporary task"}); err != nil {
		t.Fatalf("append history: %v", err)
	}
	workspaceStateDir, err := statepath.WorkspaceDir(wuuHome, workdir)
	if err != nil {
		t.Fatalf("WorkspaceDir: %v", err)
	}
	artifactDir := statepath.SessionArtifactDir(workspaceStateDir, sess.ID)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "trace.jsonl"), []byte(`{"type":"turn"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write trace: %v", err)
	}

	output := captureStdout(t, func() {
		if err := run([]string{"session", "delete", "--json", sess.ID}); err != nil {
			t.Fatalf("run session delete: %v", err)
		}
	})

	var payload struct {
		ThreadID         string          `json:"thread_id"`
		Session          session.Session `json:"session"`
		Deleted          bool            `json:"deleted"`
		ArtifactPath     string          `json:"artifact_path"`
		ArtifactsDeleted bool            `json:"artifacts_deleted"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse delete JSON: %v\noutput: %s", err, output)
	}
	if payload.ThreadID != sess.ID || payload.Session.ID != sess.ID || !payload.Deleted || payload.ArtifactPath != artifactDir || !payload.ArtifactsDeleted {
		t.Fatalf("unexpected delete payload: %+v", payload)
	}
	if _, ok, err := session.Find(sessDir, sess.ID); err != nil || ok {
		t.Fatalf("deleted session should not be found, ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(artifactDir); !os.IsNotExist(err) {
		t.Fatalf("artifact dir should be removed, err=%v", err)
	}
}

func TestRunDebugAppServerInitializeUsesClient(t *testing.T) {
	client := &fakeDebugAppServerClient{
		results: map[string]json.RawMessage{
			appserver.MethodInitialize: json.RawMessage(`{"protocol_version":"test/v1","provider":"p","model":"m"}`),
		},
	}
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"debug", "app-server", "initialize", "--workdir", "/tmp/repo", "--provider", "p", "--model", "m", "--no-tools"}); err != nil {
			t.Fatalf("run debug app-server initialize: %v", err)
		}
	})

	if len(client.calls) != 1 || client.calls[0].method != appserver.MethodInitialize {
		t.Fatalf("unexpected calls: %+v", client.calls)
	}
	if client.opts.workdir != "/tmp/repo" || client.opts.provider != "p" || client.opts.model != "m" || !client.opts.noTools {
		t.Fatalf("options not passed to debug client: %+v", client.opts)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, output)
	}
	if payload["protocol_version"] != "test/v1" || payload["provider"] != "p" {
		t.Fatalf("unexpected initialize output: %+v", payload)
	}
	if !client.shutdown {
		t.Fatal("debug client should be shut down")
	}
}

func TestRunDebugAppServerSendForwardsMethodAndParams(t *testing.T) {
	client := &fakeDebugAppServerClient{
		results: map[string]json.RawMessage{
			appserver.MethodThreadResume: json.RawMessage(`{"thread":{"id":"thread-1"}}`),
		},
	}
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"debug", "app-server", "send", appserver.MethodThreadResume, `{"session_id":"thread-1"}`}); err != nil {
			t.Fatalf("run debug app-server send: %v", err)
		}
	})

	if len(client.calls) != 1 || client.calls[0].method != appserver.MethodThreadResume {
		t.Fatalf("unexpected calls: %+v", client.calls)
	}
	var params map[string]any
	if err := json.Unmarshal(client.calls[0].params, &params); err != nil {
		t.Fatalf("parse params: %v", err)
	}
	if params["session_id"] != "thread-1" {
		t.Fatalf("unexpected params: %+v", params)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse output: %v\n%s", err, output)
	}
	thread := payload["thread"].(map[string]any)
	if thread["id"] != "thread-1" {
		t.Fatalf("unexpected output: %+v", payload)
	}
}

func TestRunDebugAppServerRegistryUsesClient(t *testing.T) {
	client := &fakeDebugAppServerClient{
		results: map[string]json.RawMessage{
			appserver.MethodPluginRegistryIntrospect: json.RawMessage(`{"generation":3,"services":[{"service":"registry.introspect","version":"1.0.0","provider":"kernel","kernel":true,"methods":["call"]}]}`),
		},
	}
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"debug", "app-server", "registry", "--workdir", "/tmp/repo"}); err != nil {
			t.Fatalf("run debug app-server registry: %v", err)
		}
	})

	if len(client.calls) != 1 || client.calls[0].method != appserver.MethodPluginRegistryIntrospect {
		t.Fatalf("unexpected calls: %+v", client.calls)
	}
	var payload struct {
		Generation uint64 `json:"generation"`
		Services   []struct {
			Service  string `json:"service"`
			Provider string `json:"provider"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, output)
	}
	if payload.Generation != 3 || len(payload.Services) != 1 || payload.Services[0].Service != "registry.introspect" || payload.Services[0].Provider != "kernel" {
		t.Fatalf("unexpected registry output: %+v", payload)
	}
	if !client.shutdown {
		t.Fatal("debug client should be shut down")
	}
}

func TestRunDebugChannelInspectResolvesRoomNameAndListsMessages(t *testing.T) {
	client := &fakeDebugAppServerClient{results: map[string]json.RawMessage{
		appserver.MethodChannelBootstrap:   json.RawMessage(`{"agents":[{"id":"agent-1","name":"Alpha","memory_dir":"/tmp/alpha","avatar_key":"","autostart":true,"created_at":"2026-07-28T00:00:00Z"}],"rooms":[{"id":"room-1","kind":"group","name":"Review","created_by":"local-user","created_at":"2026-07-28T00:00:00Z","members":[]}]}`),
		appserver.MethodChannelMessageList: json.RawMessage(`{"messages":[{"id":"message-1","room_id":"room-1","seq":4,"author_type":"human","author_id":"local-user","kind":"text","body":"status?","mentions":[],"created_at":"2026-07-28T00:00:00Z"}]}`),
	}}
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"debug", "channel", "inspect", "--room", "review", "--after", "3"}); err != nil {
			t.Fatalf("run debug channel inspect: %v", err)
		}
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse output: %v\n%s", err, output)
	}
	if payload["room"].(map[string]any)["id"] != "room-1" || len(payload["messages"].([]any)) != 1 {
		t.Fatalf("unexpected inspect output: %+v", payload)
	}
	if len(client.calls) != 2 || client.calls[1].method != appserver.MethodChannelMessageList {
		t.Fatalf("unexpected calls: %+v", client.calls)
	}
}

func TestRunDebugChannelSendWaitsForAgentReply(t *testing.T) {
	client := &fakeDebugAppServerClient{
		results: map[string]json.RawMessage{
			appserver.MethodChannelBootstrap:   json.RawMessage(`{"agents":[],"rooms":[{"id":"room-1","kind":"group","name":"Review","created_by":"local-user","created_at":"2026-07-28T00:00:00Z","members":[]}]}`),
			appserver.MethodChannelMessageSend: json.RawMessage(`{"message":{"id":"message-1","room_id":"room-1","seq":5,"author_type":"human","author_id":"local-user","kind":"text","body":"@Alpha review","mentions":["agent-1"],"created_at":"2026-07-28T00:00:00Z"}}`),
		},
		resultQueues: map[string][]json.RawMessage{
			appserver.MethodChannelMessageList: {
				json.RawMessage(`{"messages":[]}`),
				json.RawMessage(`{"messages":[{"id":"message-2","room_id":"room-1","seq":6,"author_type":"agent","author_id":"agent-1","kind":"text","body":"done","mentions":[],"created_at":"2026-07-28T00:00:01Z"}]}`),
			},
		},
	}
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"debug", "channel", "send", "--room", "room-1", "--wait", "2s", "@Alpha", "review"}); err != nil {
			t.Fatalf("run debug channel send: %v", err)
		}
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse output: %v\n%s", err, output)
	}
	if payload["reply_count"] != float64(1) || payload["timed_out"] != false {
		t.Fatalf("unexpected send output: %+v", payload)
	}
	if !client.shutdown {
		t.Fatal("debug channel client should be shut down")
	}
}

func TestRunDebugChannelSendRejectsNamedAgentIdentity(t *testing.T) {
	t.Setenv(channels.NamedAgentIDEnv, "agent-andy")
	client := &fakeDebugAppServerClient{}
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	err := runDebugChannelSend([]string{"--room", "room-1", "completed"})
	if wuuexec.ExitCode(err) != wuuexec.ExitInvalidInput || !strings.Contains(err.Error(), "use chat_send") {
		t.Fatalf("runDebugChannelSend() error = %v", err)
	}
	if len(client.calls) != 0 {
		t.Fatalf("human channel RPC was called from named-agent context: %+v", client.calls)
	}
}

func TestRunDebugAppServerSendRejectsNamedAgentHumanMessage(t *testing.T) {
	t.Setenv(channels.NamedAgentIDEnv, "agent-andy")
	client := &fakeDebugAppServerClient{}
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	err := runDebugAppServerSend([]string{appserver.MethodChannelMessageSend, `{"room_id":"room-1","body":"completed"}`})
	if wuuexec.ExitCode(err) != wuuexec.ExitInvalidInput || !strings.Contains(err.Error(), "use chat_send") {
		t.Fatalf("runDebugAppServerSend() error = %v", err)
	}
	if len(client.calls) != 0 {
		t.Fatalf("human channel RPC was called from named-agent context: %+v", client.calls)
	}
}

func TestRunDebugChannelSendPrintsTimeoutResult(t *testing.T) {
	client := &fakeDebugAppServerClient{results: map[string]json.RawMessage{
		appserver.MethodChannelBootstrap:   json.RawMessage(`{"agents":[],"rooms":[{"id":"room-1","kind":"group","name":"Review","created_by":"local-user","created_at":"2026-07-28T00:00:00Z","members":[]}]}`),
		appserver.MethodChannelMessageSend: json.RawMessage(`{"message":{"id":"message-1","room_id":"room-1","seq":5,"author_type":"human","author_id":"local-user","kind":"text","body":"@Alpha review","mentions":["agent-1"],"created_at":"2026-07-28T00:00:00Z"}}`),
		appserver.MethodChannelMessageList: json.RawMessage(`{"messages":[]}`),
	}}
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	var runErr error
	output := captureStdout(t, func() {
		runErr = run([]string{"debug", "channel", "send", "--room", "room-1", "--wait", "1ms", "@Alpha", "review"})
	})
	if wuuexec.ExitCode(runErr) != wuuexec.ExitTimeout {
		t.Fatalf("exit code = %d, err = %v", wuuexec.ExitCode(runErr), runErr)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse output: %v\n%s", err, output)
	}
	if payload["timed_out"] != true || payload["reply_count"] != float64(0) {
		t.Fatalf("unexpected timeout output: %+v", payload)
	}
}

func TestRunDebugChannelE2EPassesWithMatchingAgentReply(t *testing.T) {
	client := fakeDebugChannelE2EClient("E2E_OK", true)
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"debug", "channel", "e2e", "--sandbox", "--provider", "test", "--message", "回复 E2E_OK", "--expect", "E2E_OK"}); err != nil {
			t.Fatalf("run debug channel e2e: %v", err)
		}
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse output: %v\n%s", err, output)
	}
	if payload["status"] != "passed" || payload["matched"] != true || payload["provider"] != "test-provider" {
		t.Fatalf("unexpected e2e output: %+v", payload)
	}
	if !client.opts.sandbox || client.opts.provider != "test" {
		t.Fatalf("unexpected debug options: %+v", client.opts)
	}
	var sendParams appserver.ChannelMessageSendParams
	for _, call := range client.calls {
		if call.method == appserver.MethodChannelMessageSend {
			if err := json.Unmarshal(call.params, &sendParams); err != nil {
				t.Fatal(err)
			}
		}
	}
	if sendParams.Body != "@E2EAgent 回复 E2E_OK" {
		t.Fatalf("send body = %q, want explicit message with direct agent mention", sendParams.Body)
	}
}

func TestRunDebugChannelE2EResumesNamedAgentAndRoom(t *testing.T) {
	client := fakeDebugChannelE2EClient("ROUND_TWO_OK", true)
	client.results[appserver.MethodChannelBootstrap] = json.RawMessage(`{
		"agents":[
			{"id":"agent-1","name":"Andy","memory_dir":"/sandbox/memory-1","autostart":true,"created_at":"2026-07-28T00:00:00Z"},
			{"id":"agent-2","name":"Other","memory_dir":"/sandbox/memory-2","autostart":true,"created_at":"2026-07-28T00:00:00Z"}
		],
		"rooms":[{"id":"room-1","kind":"channel","name":"Experiment","created_by":"local-user","created_at":"2026-07-28T00:00:00Z","members":[
			{"room_id":"room-1","member_type":"agent","member_id":"agent-1","joined_at":"2026-07-28T00:00:00Z"},
			{"room_id":"room-1","member_type":"agent","member_id":"agent-2","joined_at":"2026-07-28T00:00:00Z"}
		]}]
	}`)
	client.resultQueues = map[string][]json.RawMessage{
		appserver.MethodChannelMessageList: {
			json.RawMessage(`{"messages":[{"id":"other-reply","room_id":"room-1","seq":2,"author_type":"agent","author_id":"agent-2","kind":"text","body":"OTHER_REPLY","mentions":[],"created_at":"2026-07-28T00:00:01Z"}]}`),
			client.results[appserver.MethodChannelMessageList],
		},
	}
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{
			"debug", "channel", "e2e", "--sandbox-name", "group-chat-exp",
			"--agent", "Andy", "--room", "Experiment",
			"--message", "continue", "--expect", "ROUND_TWO_OK",
		}); err != nil {
			t.Fatalf("resume named e2e: %v", err)
		}
	})
	var payload debugChannelE2EResult
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse output: %v\n%s", err, output)
	}
	if payload.Status != "passed" || payload.SandboxName != "group-chat-exp" || payload.Room.ID != "room-1" || payload.Agent.ID != "agent-1" {
		t.Fatalf("unexpected resumed e2e output: %+v", payload)
	}
	if client.opts.sandbox || client.opts.sandboxName != "group-chat-exp" {
		t.Fatalf("unexpected sandbox options: %+v", client.opts)
	}
	for _, call := range client.calls {
		if call.method == appserver.MethodChannelAgentCreate || call.method == appserver.MethodChannelRoomCreate {
			t.Fatalf("resumed e2e unexpectedly created scenario state: %+v", call)
		}
	}
}

func TestRunDebugChannelE2EFailsWhenCompletedReplyMissesExpectation(t *testing.T) {
	client := fakeDebugChannelE2EClient("not the expected result", true)
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	var runErr error
	output := captureStdout(t, func() {
		runErr = run([]string{"debug", "channel", "e2e", "--sandbox", "--expect", "E2E_OK", "--timeout", "1s"})
	})
	if wuuexec.ExitCode(runErr) != wuuexec.ExitTurnFailed {
		t.Fatalf("exit code = %d, err = %v", wuuexec.ExitCode(runErr), runErr)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse output: %v\n%s", err, output)
	}
	if payload["status"] != "expectation_failed" || payload["phase"] != "validate" {
		t.Fatalf("unexpected e2e mismatch output: %+v", payload)
	}
}

func TestRunDebugChannelE2ETimesOutWithoutAgentReply(t *testing.T) {
	client := fakeDebugChannelE2EClient("", false)
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	var runErr error
	output := captureStdout(t, func() {
		runErr = run([]string{"debug", "channel", "e2e", "--sandbox", "--timeout", "1ms"})
	})
	if wuuexec.ExitCode(runErr) != wuuexec.ExitTimeout {
		t.Fatalf("exit code = %d, err = %v", wuuexec.ExitCode(runErr), runErr)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse output: %v\n%s", err, output)
	}
	if payload["status"] != "timed_out" || payload["matched"] != false {
		t.Fatalf("unexpected e2e timeout output: %+v", payload)
	}
}

func TestRunDebugChannelE2EReportsProviderFailureWithoutWaitingForTimeout(t *testing.T) {
	client := fakeDebugChannelE2EClient("", false)
	client.results[appserver.MethodChannelAgentStart] = json.RawMessage(`{"agent":{"id":"agent-1"},"thread_id":"agent-thread-1","wake_state":{"agent_id":"agent-1","outstanding":true}}`)
	client.results[appserver.MethodChannelAgentList] = json.RawMessage(`{"agents":[{"id":"agent-1","activity_status":"idle"}]}`)
	client.results[appserver.MethodThreadResume] = json.RawMessage(`{"thread":{"id":"agent-thread-1","status":"idle","turns":[{"id":"turn-1","items":[],"items_view":"full","status":"failed","error":{"message":"provider authentication failed","category":"auth","provider":"test-provider"}}]}}`)
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	var runErr error
	output := captureStdout(t, func() {
		runErr = run([]string{"debug", "channel", "e2e", "--sandbox", "--timeout", "1m"})
	})
	if wuuexec.ExitCode(runErr) != wuuexec.ExitTurnFailed {
		t.Fatalf("exit code = %d, err = %v", wuuexec.ExitCode(runErr), runErr)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse output: %v\n%s", err, output)
	}
	if payload["status"] != "turn_failed" || payload["phase"] != "provider" || payload["error"] != "provider authentication failed" {
		t.Fatalf("unexpected provider failure output: %+v", payload)
	}
}

func TestHydrateDebugSandboxCredentialsKeepsSecretsInMemory(t *testing.T) {
	realWuuHome := filepath.Join(t.TempDir(), "real-wuu-home")
	t.Setenv("WUU_HOME", realWuuHome)
	store, err := authstorage.ForHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("test-provider", authstorage.Credentials{APIKey: "secret-key", AuthToken: "secret-token"}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"test-provider": {Type: "openai-compatible"}}}
	hydrateDebugSandboxCredentials(&cfg, t.TempDir())
	provider := cfg.Providers["test-provider"]
	if provider.APIKey != "secret-key" || provider.AuthToken != "secret-token" {
		t.Fatalf("hydrated provider = %+v", provider)
	}
}

func TestLocalDebugAppServerSandboxIsolatesChannelState(t *testing.T) {
	realWuuHome := filepath.Join(t.TempDir(), "real-wuu-home")
	t.Setenv("WUU_HOME", realWuuHome)
	client, err := newLocalDebugAppServerClient(context.Background(), debugAppServerOptions{
		workdir: t.TempDir(), noTools: true, sandbox: true,
	})
	if err != nil {
		t.Fatalf("new sandbox debug client: %v", err)
	}
	t.Cleanup(func() {
		if client != nil {
			shutdownDebugClient(client)
		}
	})
	sandboxHome := client.rt.WuuHome
	if sandboxHome == realWuuHome || !strings.Contains(sandboxHome, "wuu-channel-e2e-") {
		t.Fatalf("sandbox WUU_HOME = %q, real = %q", sandboxHome, realWuuHome)
	}
	var bootstrap appserver.ChannelBootstrapResult
	if err := client.Call(context.Background(), appserver.MethodChannelBootstrap, nil, &bootstrap); err != nil {
		t.Fatalf("sandbox bootstrap: %v", err)
	}
	var created appserver.ChannelRoomCreateResult
	if err := client.Call(context.Background(), appserver.MethodChannelRoomCreate, appserver.ChannelRoomCreateParams{Name: "Sandbox"}, &created); err != nil {
		t.Fatalf("create sandbox room: %v", err)
	}
	if _, err := os.Stat(filepath.Join(realWuuHome, "channels")); !os.IsNotExist(err) {
		t.Fatalf("real channel state was touched: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown sandbox debug client: %v", err)
	}
	client = nil
	if got := os.Getenv("WUU_HOME"); got != realWuuHome {
		t.Fatalf("WUU_HOME after shutdown = %q, want %q", got, realWuuHome)
	}
	if _, err := os.Stat(filepath.Dir(sandboxHome)); !os.IsNotExist(err) {
		t.Fatalf("sandbox was not removed: %v", err)
	}
}

func TestNormalizeDebugSandboxArgsSupportsTemporaryAndNamedForms(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "bare remains temporary", args: []string{"--sandbox", "--agent", "Andy"}, want: []string{"--temp-sandbox", "--agent", "Andy"}},
		{name: "name consumes value", args: []string{"--sandbox", "group-chat-exp", "--room", "Test"}, want: []string{"--sandbox-name", "group-chat-exp", "--room", "Test"}},
		{name: "equals name", args: []string{"--sandbox=group-chat-exp"}, want: []string{"--sandbox-name=group-chat-exp"}},
		{name: "equals true remains temporary", args: []string{"--sandbox=true"}, want: []string{"--temp-sandbox=true"}},
		{name: "equals false remains disabled", args: []string{"--sandbox=false"}, want: []string{"--temp-sandbox=false"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeDebugSandboxArgs(tt.args, true)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalized args = %#v, want %#v", got, tt.want)
			}
		})
	}
	got, err := normalizeDebugSandboxArgs([]string{"--sandbox", "legacy positional message"}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--temp-sandbox", "legacy positional message"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backward-compatible E2E args = %#v, want %#v", got, want)
	}
	for _, name := range []string{"../escape", ".", "name/child", "two words", "-leading"} {
		if err := validateDebugSandboxName(name); err == nil {
			t.Fatalf("validateDebugSandboxName(%q) unexpectedly passed", name)
		}
	}
}

func TestNamedDebugSandboxPersistsStateAndCanBeDeleted(t *testing.T) {
	realWuuHome := filepath.Join(t.TempDir(), "real-wuu-home")
	t.Setenv("WUU_HOME", realWuuHome)
	workdir := t.TempDir()
	const sandboxName = "multi-round"

	client, err := newLocalDebugAppServerClient(context.Background(), debugAppServerOptions{
		workdir: workdir, noTools: true, sandboxName: sandboxName,
	})
	if err != nil {
		t.Fatalf("new named sandbox client: %v", err)
	}
	t.Cleanup(func() {
		if client != nil {
			shutdownDebugClient(client)
		}
	})
	sandboxHome := client.rt.WuuHome
	wantHome := filepath.Join(realWuuHome, "debug", "sandboxes", sandboxName)
	if sandboxHome != wantHome {
		t.Fatalf("sandbox WUU_HOME = %q, want %q", sandboxHome, wantHome)
	}
	var bootstrap appserver.ChannelBootstrapResult
	if err := client.Call(context.Background(), appserver.MethodChannelBootstrap, nil, &bootstrap); err != nil {
		t.Fatalf("sandbox bootstrap: %v", err)
	}
	var created appserver.ChannelRoomCreateResult
	if err := client.Call(context.Background(), appserver.MethodChannelRoomCreate, appserver.ChannelRoomCreateParams{Name: "Sandbox"}, &created); err != nil {
		t.Fatalf("create sandbox room: %v", err)
	}
	var sent appserver.ChannelMessageSendResult
	if err := client.Call(context.Background(), appserver.MethodChannelMessageSend, appserver.ChannelMessageSendParams{
		RoomID: created.Room.ID, Body: "first round",
	}, &sent); err != nil {
		t.Fatalf("send first round: %v", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := client.Shutdown(shutdownCtx); err != nil {
		cancel()
		t.Fatalf("shutdown first client: %v", err)
	}
	cancel()
	client = nil

	client, err = newLocalDebugAppServerClient(context.Background(), debugAppServerOptions{
		workdir: workdir, noTools: true, sandboxName: sandboxName,
	})
	if err != nil {
		t.Fatalf("resume named sandbox client: %v", err)
	}
	var messages appserver.ChannelMessageListResult
	if err := client.Call(context.Background(), appserver.MethodChannelMessageList, appserver.ChannelMessageListParams{
		RoomID: created.Room.ID, Limit: 100,
	}, &messages); err != nil {
		t.Fatalf("list resumed messages: %v", err)
	}
	if len(messages.Messages) != 1 || messages.Messages[0].Body != "first round" {
		t.Fatalf("resumed messages = %+v", messages.Messages)
	}
	shutdownCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	if err := client.Shutdown(shutdownCtx); err != nil {
		cancel()
		t.Fatalf("shutdown resumed client: %v", err)
	}
	cancel()
	client = nil
	output := captureStdout(t, func() {
		if err := runDebugChannelSend([]string{
			"--sandbox", sandboxName, "--workdir", workdir, "--no-tools",
			"--room", created.Room.ID, "second", "round",
		}); err != nil {
			t.Fatalf("send through named sandbox CLI: %v", err)
		}
	})
	var sendResult debugChannelSendResult
	if err := json.Unmarshal([]byte(output), &sendResult); err != nil {
		t.Fatalf("parse send output: %v", err)
	}
	if sendResult.SandboxName != sandboxName || sendResult.Sent.Body != "second round" {
		t.Fatalf("send result = %+v", sendResult)
	}
	output = captureStdout(t, func() {
		if err := runDebugChannelInspect([]string{
			"--sandbox", sandboxName, "--workdir", workdir, "--no-tools",
			"--room", created.Room.ID,
		}); err != nil {
			t.Fatalf("inspect through named sandbox CLI: %v", err)
		}
	})
	var inspectResult debugChannelInspectResult
	if err := json.Unmarshal([]byte(output), &inspectResult); err != nil {
		t.Fatalf("parse inspect output: %v", err)
	}
	if inspectResult.SandboxName != sandboxName || len(inspectResult.Messages) != 2 {
		t.Fatalf("inspect result = %+v", inspectResult)
	}

	if _, err := os.Stat(filepath.Join(realWuuHome, "channels")); !os.IsNotExist(err) {
		t.Fatalf("normal channel state was touched: %v", err)
	}
	output = captureStdout(t, func() {
		if err := runDebugSandbox([]string{"list"}); err != nil {
			t.Fatalf("list named sandboxes: %v", err)
		}
	})
	var listed debugSandboxListResult
	if err := json.Unmarshal([]byte(output), &listed); err != nil {
		t.Fatalf("parse sandbox list output: %v", err)
	}
	if len(listed.Sandboxes) != 1 || listed.Sandboxes[0].Name != sandboxName || listed.Sandboxes[0].Dir != sandboxHome {
		t.Fatalf("sandbox list = %+v", listed.Sandboxes)
	}
	output = captureStdout(t, func() {
		if err := runDebugSandbox([]string{"delete", sandboxName}); err != nil {
			t.Fatalf("delete named sandbox: %v", err)
		}
	})
	var deleted debugSandboxDeleteResult
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("parse delete output: %v", err)
	}
	if !deleted.Deleted {
		t.Fatalf("delete output = %+v", deleted)
	}
	if _, err := os.Stat(sandboxHome); !os.IsNotExist(err) {
		t.Fatalf("named sandbox still exists after delete: %v", err)
	}
}

func TestDebugSandboxManagementRejectsSymlinkedBase(t *testing.T) {
	realWuuHome := filepath.Join(t.TempDir(), "real-wuu-home")
	t.Setenv("WUU_HOME", realWuuHome)
	baseDir := filepath.Join(realWuuHome, "debug", "sandboxes")
	if err := os.MkdirAll(filepath.Dir(baseDir), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "experiment"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outside, "experiment", "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, baseDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := runDebugSandbox([]string{"list"}); err == nil {
		t.Fatal("list unexpectedly followed a symlinked sandbox base")
	}
	if err := runDebugSandbox([]string{"delete", "experiment"}); err == nil {
		t.Fatal("delete unexpectedly followed a symlinked sandbox base")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("outside marker was touched: %v", err)
	}
}

func fakeDebugChannelE2EClient(reply string, completed bool) *fakeDebugAppServerClient {
	messageList := json.RawMessage(`{"messages":[]}`)
	if reply != "" {
		messageList = json.RawMessage(fmt.Sprintf(`{"messages":[{"id":"reply-1","room_id":"room-1","seq":2,"author_type":"agent","author_id":"agent-1","kind":"text","body":%q,"mentions":[],"created_at":"2026-07-28T00:00:01Z"}]}`, reply))
	}
	client := &fakeDebugAppServerClient{results: map[string]json.RawMessage{
		appserver.MethodInitialize:         json.RawMessage(`{"status":"ready","protocol_version":"test/v1","provider":"test-provider","model":"test-model","workspace_root":"/workspace","core":{},"runtime_host":{},"permissions":{},"extension_trust":{},"advanced_settings":{},"general_settings":{},"features":{},"max_parallel":1}`),
		appserver.MethodChannelAgentCreate: json.RawMessage(`{"agent":{"id":"agent-1","name":"E2EAgent","memory_dir":"/sandbox/memory","avatar_key":"","autostart":true,"created_at":"2026-07-28T00:00:00Z"}}`),
		appserver.MethodChannelRoomCreate:  json.RawMessage(`{"room":{"id":"room-1","kind":"channel","name":"E2E","created_by":"local-user","created_at":"2026-07-28T00:00:00Z","members":[]}}`),
		appserver.MethodChannelMessageSend: json.RawMessage(`{"message":{"id":"message-1","room_id":"room-1","seq":1,"author_type":"human","author_id":"local-user","kind":"text","body":"@E2EAgent test","mentions":["agent-1"],"created_at":"2026-07-28T00:00:00Z"}}`),
		appserver.MethodChannelMessageList: messageList,
	}}
	if completed {
		events := make(chan wuuexec.Notification, 1)
		events <- cliExecNotification(appserver.NotificationTurnCompleted, map[string]any{"thread_id": "agent-thread-1"})
		close(events)
		client.notifications = events
	}
	return client
}

func TestRunDebugProtocolEventsJSONReadsTraceEvents(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	workdir := t.TempDir()

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	sessDir := statepath.SessionsDir(home)
	sess, err := session.CreateWithMetadata(sessDir, "debug-thread", workdir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	workspaceStateDir, err := statepath.WorkspaceDir(wuuHome, workdir)
	if err != nil {
		t.Fatalf("WorkspaceDir: %v", err)
	}
	tracePath := sessiontrace.Path(statepath.SessionArtifactDir(workspaceStateDir, sess.ID))
	if err := sessiontrace.AppendTurn(
		tracePath,
		sessiontrace.TurnRecord{ThreadID: sess.ID, TurnID: "turn-1", Status: "completed"},
		sessiontrace.FinalRecord{Status: "completed", FinalAnswerPreview: "done"},
		nil,
		nil,
		nil,
		nil,
		nil,
	); err != nil {
		t.Fatalf("append trace: %v", err)
	}

	output := captureStdout(t, func() {
		if err := run([]string{"debug", "protocol", "events", "--json", sess.ID}); err != nil {
			t.Fatalf("run debug protocol events: %v", err)
		}
	})

	var payload struct {
		ThreadID  string            `json:"thread_id"`
		TracePath string            `json:"trace_path"`
		Events    []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, output)
	}
	if payload.ThreadID != sess.ID || payload.TracePath != tracePath || len(payload.Events) != 2 {
		t.Fatalf("unexpected protocol events payload: %+v", payload)
	}
	var first struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload.Events[0], &first); err != nil {
		t.Fatalf("parse first event: %v", err)
	}
	if first.Type != "turn" {
		t.Fatalf("first event type = %q", first.Type)
	}
}

func TestRunSessionTraceJSONReplaysTrace(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	workdir := t.TempDir()

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	sessDir := statepath.SessionsDir(home)
	sess, err := session.CreateWithMetadata(sessDir, "trace-thread", workdir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	workspaceStateDir, err := statepath.WorkspaceDir(wuuHome, workdir)
	if err != nil {
		t.Fatalf("WorkspaceDir: %v", err)
	}
	tracePath := sessiontrace.Path(statepath.SessionArtifactDir(workspaceStateDir, sess.ID))
	if err := sessiontrace.AppendTurn(
		tracePath,
		sessiontrace.TurnRecord{ThreadID: sess.ID, TurnID: "turn-1", Status: "completed", InputTokens: 3, OutputTokens: 4},
		sessiontrace.FinalRecord{Status: "completed", FinalAnswerPreview: "done"},
		nil,
		nil,
		nil,
		nil,
		nil,
	); err != nil {
		t.Fatalf("append trace: %v", err)
	}

	output := captureStdout(t, func() {
		if err := run([]string{"session", "trace", "--json", sess.ID}); err != nil {
			t.Fatalf("run session trace: %v", err)
		}
	})

	var payload struct {
		ThreadID  string                     `json:"thread_id"`
		TracePath string                     `json:"trace_path"`
		Summary   sessiontrace.ReplaySummary `json:"summary"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse JSON: %v\noutput: %s", err, output)
	}
	if payload.ThreadID != sess.ID || payload.TracePath != tracePath || !payload.Summary.Complete || payload.Summary.LatestTurn == nil || payload.Summary.LatestTurn.OutputTokens != 4 {
		t.Fatalf("unexpected trace payload: %+v", payload)
	}
}

func TestRunSessionTraceLastReplaysMostRecentTrace(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	workdir := t.TempDir()

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	sessDir := statepath.SessionsDir(home)
	if _, err := session.CreateWithMetadata(sessDir, "older-trace-thread", workdir); err != nil {
		t.Fatalf("create older session: %v", err)
	}
	latest, err := session.CreateWithMetadata(sessDir, "latest-trace-thread", workdir)
	if err != nil {
		t.Fatalf("create latest session: %v", err)
	}
	workspaceStateDir, err := statepath.WorkspaceDir(wuuHome, workdir)
	if err != nil {
		t.Fatalf("WorkspaceDir: %v", err)
	}
	tracePath := sessiontrace.Path(statepath.SessionArtifactDir(workspaceStateDir, latest.ID))
	if err := sessiontrace.AppendTurn(
		tracePath,
		sessiontrace.TurnRecord{ThreadID: latest.ID, TurnID: "turn-1", Status: "completed", InputTokens: 1, OutputTokens: 2},
		sessiontrace.FinalRecord{Status: "completed", FinalAnswerPreview: "latest"},
		nil,
		nil,
		nil,
		nil,
		nil,
	); err != nil {
		t.Fatalf("append trace: %v", err)
	}

	output := captureStdout(t, func() {
		if err := run([]string{"session", "trace", "--json", "--last", "--workdir", workdir}); err != nil {
			t.Fatalf("run session trace --last: %v", err)
		}
	})

	var payload struct {
		ThreadID  string                     `json:"thread_id"`
		TracePath string                     `json:"trace_path"`
		Summary   sessiontrace.ReplaySummary `json:"summary"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse JSON: %v\noutput: %s", err, output)
	}
	if payload.ThreadID != latest.ID || payload.TracePath != tracePath || !payload.Summary.Complete {
		t.Fatalf("unexpected trace payload: %+v", payload)
	}
}

func TestRunSessionShowNotFoundReturnsError(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)

	err := run([]string{"session-show", "--thread", "missing-id"})
	if err == nil {
		t.Fatal("expected error for missing thread id")
	}
	if !strings.Contains(err.Error(), "session not found") {
		t.Errorf("expected session-not-found, got: %v", err)
	}
}

func TestRunDebugAppServerExecutionsUsesClient(t *testing.T) {
	client := &fakeDebugAppServerClient{
		results: map[string]json.RawMessage{
			appserver.MethodPluginExecutionsList: json.RawMessage(`{"executions":[{"id":"exec-1","plugin_id":"memory","message":"waiting"}]}`),
		},
	}
	restore := installDebugAppServerClientOverride(t, client)
	defer restore()

	output := captureStdout(t, func() {
		if err := run([]string{"debug", "app-server", "executions", "--workdir", "/tmp/repo"}); err != nil {
			t.Fatalf("run debug app-server executions: %v", err)
		}
	})

	if len(client.calls) != 1 || client.calls[0].method != appserver.MethodPluginExecutionsList {
		t.Fatalf("unexpected calls: %+v", client.calls)
	}
	var payload struct {
		Executions []struct {
			ID       string `json:"id"`
			PluginID string `json:"plugin_id"`
			Message  string `json:"message"`
		} `json:"executions"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, output)
	}
	if len(payload.Executions) != 1 || payload.Executions[0].ID != "exec-1" || payload.Executions[0].Message != "waiting" {
		t.Fatalf("payload = %+v", payload)
	}
}
