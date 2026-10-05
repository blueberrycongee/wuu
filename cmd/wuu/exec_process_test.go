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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func TestExecProcessHelper(t *testing.T) {
	if os.Getenv("WUU_EXEC_PROCESS_TEST") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			if value := os.Getenv("WUU_EXEC_TEST_UMASK"); value != "" {
				mask, err := strconv.ParseInt(value, 8, 32)
				if err != nil {
					t.Fatal(err)
				}
				syscall.Umask(int(mask))
			}
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	t.Fatal("missing subprocess arguments")
}

func TestExecProcessPreservesWorkspacePermissionsAndPrivateState(t *testing.T) {
	// Exercise startup, Code Mode file tools and a real child shell. Private
	// state must stay private even when the caller's umask permits sharing.
	for _, mask := range []int{0o000, 0o022, 0o077} {
		t.Run(fmt.Sprintf("umask-%03o", mask), func(t *testing.T) {
			root := t.TempDir()
			for path, mode := range map[string]os.FileMode{"private.txt": 0o600, "executable.sh": 0o755} {
				path = filepath.Join(root, path)
				if err := os.WriteFile(path, []byte("before\n"), mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			code := `await tools.write_file({path:"tool/new.txt",content:"created\n"});
for (const path of ["private.txt","executable.sh"]) {
  await tools.read_file({path});
  await tools.edit_file({path,old_text:"before",new_text:"after"});
}
text(await tools.bash({command:"mkdir shell; printf created > shell/new.txt"}));`
			arguments, _ := json.Marshal(map[string]string{"code": code})
			ready, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				var delta map[string]any
				finish := "tool_calls"
				if !bytes.Contains(body, []byte(`"tool_call_id"`)) {
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
						"index": 0, "id": "permission-call", "type": "function",
						"function": map[string]any{"name": "run_code", "arguments": string(arguments)},
					}}}
				} else {
					once.Do(func() { close(ready) })
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					delta, finish = map[string]any{"role": "assistant", "content": "done"}, "stop"
				}
				payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
			}))
			t.Cleanup(server.Close)
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			configPath := filepath.Join(root, "config.json")
			config := fmt.Sprintf(`{"default_provider":"test","providers":{"test":{"type":"openai-compatible","base_url":%q,"api_key":"synthetic","model":"gpt-test"}},"agent":{"permission_mode":"unconfined"},"skills":{"enabled":false}}`, server.URL)
			if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd, stdout, stderr := execProcess(t, root, "--config", configPath, "--json", "create the fixtures")
			cmd.Env = append(cmd.Env, fmt.Sprintf("WUU_EXEC_TEST_UMASK=%03o", mask))
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-ready:
			case <-time.After(20 * time.Second):
				t.Fatal("tools did not reach the following model request")
			}
			want := map[string]os.FileMode{
				"tool/new.txt": 0o644 &^ os.FileMode(mask), "tool": 0o755 &^ os.FileMode(mask),
				"shell/new.txt": 0o666 &^ os.FileMode(mask), "shell": 0o777 &^ os.FileMode(mask),
				"private.txt": 0o600, "executable.sh": 0o755,
			}
			for path, mode := range want {
				info, err := os.Stat(filepath.Join(root, path))
				if err != nil {
					t.Errorf("workspace file %s: %v", path, err)
				} else if info.Mode().Perm() != mode {
					t.Errorf("workspace %s mode = %o, want %o", path, info.Mode().Perm(), mode)
				}
			}
			for _, path := range []string{"private.txt", "executable.sh"} {
				content, err := os.ReadFile(filepath.Join(root, path))
				if err != nil || string(content) != "after\n" {
					t.Errorf("edit %s: content = %q, error = %v", path, content, err)
				}
			}
			// Hold the provider response so the database sidecars are still live.
			state := filepath.Join(root, "state")
			for _, suffix := range []string{"", "-wal", "-shm"} {
				path := session.DBPath(statepath.SessionsDir(state)) + suffix
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Errorf("private database %s: %v, %v", path, info, err)
				}
			}
			unblock()
			waitExecProcess(t, cmd, stderr, 0)
			if bytes.Contains(stdout.Bytes(), []byte("PTC failed:")) {
				t.Errorf("program failed: %s", stdout)
			}
			if err := filepath.WalkDir(state, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				// Bundled plugin source is public package content, not session state.
				if path == filepath.Join(state, "cache", "plugins", ".bundled-generations") {
					return filepath.SkipDir
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if info.Mode().Perm()&0o077 != 0 {
					t.Errorf("private state %s mode = %o", path, info.Mode().Perm())
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
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
