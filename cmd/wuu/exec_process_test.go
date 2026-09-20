//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func TestExecProcessHelper(t *testing.T) {
	if os.Getenv("WUU_EXEC_PROCESS_TEST") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			err := run(os.Args[i+1:])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(wuuexec.ExitCode(err))
		}
	}
	t.Fatal("missing subprocess arguments")
}

func execProcess(t *testing.T, root string, args ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, bin, append([]string{"-test.run=^TestExecProcessHelper$", "--", "exec"}, args...)...)
	cmd.Dir = root
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + root,
		"WUU_HOME=" + filepath.Join(root, "state"), "WUU_SAFE_MODE=1", "WUU_EXEC_PROCESS_TEST=1",
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	t.Cleanup(func() {
		if cmd.Process != nil && cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd, stdout, stderr
}

func waitExecProcess(t *testing.T, cmd *exec.Cmd, stderr *bytes.Buffer, code int) {
	t.Helper()
	err := cmd.Wait()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != code {
		t.Fatalf("process exit: %v; want %d; stderr: %s", err, code, stderr)
	}
}

func TestExecProcessTimeoutIncludesOpenStdin(t *testing.T) {
	for _, args := range [][]string{
		{"--timeout=150ms", "-"},
		{"--timeout=150ms", "--input-json"},
		{"resume", "--timeout=150ms", "thread-id", "-"},
		{"fork", "--timeout=150ms", "thread-id", "-"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd, _, stderr := execProcess(t, t.TempDir(), args...)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			// Leave the producer open: the command must not wait for EOF.
			waitExecProcess(t, cmd, stderr, wuuexec.ExitTimeout)
		})
	}
}

func stalledExecProvider(t *testing.T, root string, delta string) (string, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"role": "assistant", "content": delta},
		}}})
		fmt.Fprintf(w, "data: %s\n\n", payload)
		w.(http.Flusher).Flush()
		once.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); server.Close() })
	configPath := filepath.Join(root, "config.json")
	config := fmt.Sprintf(`{"default_provider":"test","providers":{"test":{"type":"openai-compatible","base_url":%q,"api_key":"synthetic","model":"gpt-test"}},"agent":{"permission_mode":"read_only"}}`, server.URL)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, started
}

func assertSettledExecRun(t *testing.T, root string) {
	t.Helper()
	store, err := execution.NewStore(statepath.SessionsDir(filepath.Join(root, "state")))
	if err != nil {
		t.Fatal(err)
	}
	runs, err := store.List(context.Background(), execution.ListOptions{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("stored runs: %+v, %v", runs, err)
	}
	if !runs[0].Status.Terminal() {
		t.Fatalf("process left an active Run: %+v", runs[0])
	}
}

func TestExecProcessSignalsSettleRun(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			root := t.TempDir()
			config, started := stalledExecProvider(t, root, "hello")
			cmd, stdout, stderr := execProcess(t, root, "--config", config, "--no-tools", "--json", "work")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(15 * time.Second):
				t.Fatal("provider was not reached")
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			waitExecProcess(t, cmd, stderr, wuuexec.ExitInterrupted)
			var result struct{ Type, Status string }
			for _, line := range bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n")) {
				if err := json.Unmarshal(line, &result); err != nil {
					t.Fatalf("invalid JSONL: %s: %v", line, err)
				}
			}
			if result.Type != "result" || result.Status != "interrupted" {
				t.Fatalf("missing interruption result: %s", stdout)
			}
			assertSettledExecRun(t, root)
		})
	}
}

func TestExecProcessBusyResumeWithModelSelection(t *testing.T) {
	root := t.TempDir()
	config, started := stalledExecProvider(t, root, "hello")
	active, _, activeStderr := execProcess(t, root, "--config", config, "--no-tools", "--json", "work")
	active.Stdout = io.Discard
	if err := active.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("provider was not reached")
	}
	store, err := execution.NewStore(statepath.SessionsDir(filepath.Join(root, "state")))
	if err != nil {
		t.Fatal(err)
	}
	runs, err := store.List(context.Background(), execution.ListOptions{})
	if err != nil || len(runs) != 1 || runs[0].Status.Terminal() {
		t.Fatalf("expected one active Run: %+v, %v", runs, err)
	}
	for _, selection := range [][]string{nil, {"--model", "gpt-test"}} {
		t.Run(fmt.Sprint(selection), func(t *testing.T) {
			args := []string{"resume", "--config", config, "--no-tools", "--json"}
			args = append(args, selection...)
			args = append(args, runs[0].ThreadID, "work")
			cmd, _, stderr := execProcess(t, root, args...)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waitExecProcess(t, cmd, stderr, wuuexec.ExitConflict)
		})
	}
	if err := active.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waitExecProcess(t, active, activeStderr, wuuexec.ExitInterrupted)
	assertSettledExecRun(t, root)
}

func TestExecProcessTimeoutWithUnreadStdout(t *testing.T) {
	root := t.TempDir()
	config, started := stalledExecProvider(t, root, strings.Repeat("x", 2<<20))
	// Include runtime initialization under race instrumentation in the budget;
	// the assertion is that the process exits without draining, not startup speed.
	cmd, _, stderr := execProcess(t, root, "--config", config, "--no-tools", "--json", "--timeout=10s", "work")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	cmd.Stdout = writer
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("provider was not reached")
	}
	// Do not drain stdout until after exit; a large delta fills the OS pipe.
	waitExecProcess(t, cmd, stderr, wuuexec.ExitTimeout)
	assertSettledExecRun(t, root)
}

func TestExecProcessDisconnectedStdoutSettlesRun(t *testing.T) {
	root := t.TempDir()
	config, started := stalledExecProvider(t, root, strings.Repeat("x", 2<<20))
	cmd, _, stderr := execProcess(t, root, "--config", config, "--no-tools", "--json", "work")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	cmd.Stdout = writer
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("provider was not reached")
	}
	// Disconnect only after execution starts, exercising cancellation as well
	// as the process's SIGPIPE disposition.
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	waitExecProcess(t, cmd, stderr, wuuexec.ExitProtocol)
	assertSettledExecRun(t, root)
}
