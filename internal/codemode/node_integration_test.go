package codemode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type nodeExecutor func(context.Context, providers.ToolCall) (toolresult.Result, error)

func (f nodeExecutor) Invoke(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	return f(ctx, call)
}

func nodeService(t *testing.T) *Service {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for PTC integration tests:", err)
	}
	s := NewService(ServiceConfig{NodeExecutable: node})
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// These cases exercise the real process and wire: curated output, fresh state,
// binding failures, non-JSON data, process deadlines, and filesystem authority.
func TestNodeProgramLifecycle(t *testing.T) {
	s := nodeService(t)
	opts := RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(_ context.Context, call providers.ToolCall) (toolresult.Result, error) {
		if call.Name == "media" {
			return toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: "eA=="}}}, nil
		}
		if call.Name == "fail" {
			return toolresult.FromErrorText("denied by policy"), nil
		}
		return toolresult.FromText("private intermediate"), nil
	})}
	request := RunRequest{Code: `const n: number = 7; const raw = await tools.read({}); try { await tools.fail({}); } catch(e) { console.log(e.name, e.toolName); } globalThis.saved = 1; return n;`,
		Tools: []ToolDefinition{{Name: "read"}, {Name: "fail"}}}
	result, err := s.Run(context.Background(), request, opts)
	if err != nil || result.Error != "" || string(result.Value) != "7" || !strings.Contains(strings.Join(result.Logs, ""), "ToolCallError fail") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if strings.Contains(strings.Join(result.Logs, ""), "private intermediate") {
		t.Fatal("intermediate result leaked")
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `return {saved: typeof globalThis.saved, env: Object.keys(process.env)}`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `{"saved":"undefined","env":[]}` {
		t.Fatalf("fresh run=%+v %v", result, err)
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `return 1n`}, opts)
	if err != nil || !strings.Contains(result.Error, "JSON") {
		t.Fatalf("lossy output=%+v %v", result, err)
	}

	result, err = s.Run(context.Background(), RunRequest{Code: `let rejected = false; for (let i=0; i<128; i++) { try { await tools.media({}); } catch(e) { rejected = true; } } return rejected;`, Tools: []ToolDefinition{{Name: "media"}}}, opts)
	if err != nil || result.Error != "" || string(result.Value) != "true" || len(result.Media) != toolresult.MaxContentParts-1 {
		t.Fatalf("media bound=%+v %v", result, err)
	}
}

func TestNodeDeadlineAndOutputLimit(t *testing.T) {
	s := nodeService(t)
	for _, code := range []string{`console.log("started"); for (;;) {}`, `await new Promise(() => {})`} {
		result, err := s.Run(context.Background(), RunRequest{Code: code, TimeoutMS: 500}, RunOptions{CWD: t.TempDir()})
		if err != nil || !strings.Contains(result.Error, "deadline") {
			t.Fatalf("deadline=%+v %v", result, err)
		}
	}
	result, err := s.Run(context.Background(), RunRequest{Code: `console.log("x".repeat(20000))`}, RunOptions{CWD: t.TempDir(), MaxOutputBytes: 4096})
	if err != nil || !strings.Contains(result.Error, "output") {
		t.Fatalf("output limit=%+v %v", result, err)
	}
}

func TestNodeDirectFilesystemConfinement(t *testing.T) {
	if !processsandbox.Supported() {
		t.Skip("platform has no built-in filesystem sandbox")
	}
	s := nodeService(t)
	root := t.TempDir()
	target, _ := json.Marshal(filepath.Join(root, "written.txt"))
	result, err := s.Run(context.Background(), RunRequest{Code: `await (await import('node:fs/promises')).writeFile(` + string(target) + `, 'no');`},
		RunOptions{CWD: root, Sandbox: &processsandbox.Policy{Mode: processsandbox.ModeReadOnly}})
	if err != nil || result.Error == "" {
		t.Fatalf("read-only run=%+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "written.txt")); !os.IsNotExist(err) {
		t.Fatal("read-only process wrote a file")
	}
}

func TestNodeCancellationStopsBindings(t *testing.T) {
	s := nodeService(t)
	entered := make(chan struct{})
	stopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	opts := RunOptions{CWD: t.TempDir(), Executor: nodeExecutor(func(ctx context.Context, _ providers.ToolCall) (toolresult.Result, error) {
		close(entered)
		<-ctx.Done()
		close(stopped)
		return toolresult.Result{}, ctx.Err()
	})}
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(ctx, RunRequest{Code: `await tools.wait({})`, Tools: []ToolDefinition{{Name: "wait"}}}, opts)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("binding never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not stop process")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("binding outlived run")
	}
}
