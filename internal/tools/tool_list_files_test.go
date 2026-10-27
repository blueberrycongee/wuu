package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestListFilesContinuationTracksOnlyDirectoryEntries(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := range defaultMaxEntries + 1 {
		mustWriteFile(t, filepath.Join(kit.RootDir(), "docs", fmt.Sprintf("%04d.txt", i)), "same size")
	}
	read := func(args string) (string, error) {
		return kit.Execute(context.Background(), providers.ToolCall{Name: "list_files", Arguments: args})
	}
	first, err := read(`{"path":"docs"}`)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Page struct {
			Next map[string]any `json:"next"`
		} `json:"page"`
	}
	if err := json.Unmarshal([]byte(first), &page); err != nil || len(page.Page.Next) == 0 {
		t.Fatalf("first page: %s, %v", first, err)
	}
	page.Page.Next["path"] = "docs"
	next := mustMarshalMap(page.Page.Next)
	mustWriteFile(t, filepath.Join(kit.RootDir(), "unrelated", "changed.txt"), "not part of the directory listing")
	if _, err := read(next); err != nil {
		t.Fatalf("unrelated content invalidated directory continuation: %v", err)
	}
	mustWriteFile(t, filepath.Join(kit.RootDir(), "docs", "0000.txt"), "next size")
	if _, err := read(next); err != nil {
		t.Fatalf("content-only edit invalidated unchanged directory metadata: %v", err)
	}
	inserted := filepath.Join(kit.RootDir(), "docs", "0000-added.txt")
	mustWriteFile(t, inserted, "insert before cursor")
	if _, err := read(next); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("inserted directory entry accepted stale continuation: %v", err)
	}
	if err := os.Remove(inserted); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(kit.RootDir(), "docs", "0000.txt"), "changed size")
	if _, err := read(next); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("changed directory entry accepted stale continuation: %v", err)
	}
}

func TestListFilesCanceledBeforeExecution(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := kit.Execute(ctx, providers.ToolCall{Name: "list_files", Arguments: `{}`}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list_files returned %v", err)
	}
	records := kit.ToolTelemetry()
	if len(records) != 1 || records[0].Success || records[0].Error != context.Canceled.Error() {
		t.Fatalf("canceled listing telemetry: %+v", records)
	}
}

type listingResultRecorder struct {
	*Toolkit
	result chan toolresult.Result
}

func (r listingResultRecorder) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	result, err := r.Toolkit.ExecuteResult(ctx, call)
	if call.Name == "run_code" {
		r.result <- result
	}
	return result, err
}

func TestPTCParallelListFilesCancellationSettles(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	var mu sync.Mutex
	admitted := 0
	kit.SetPermissionRequestHook(func(ctx context.Context, _ *Toolkit, info ToolInfo, _ providers.ToolCall) error {
		if info.Name != "list_files" {
			return nil
		}
		mu.Lock()
		admitted++
		if admitted == 2 {
			close(entered)
		}
		mu.Unlock()
		<-ctx.Done()
		// Authorization can finish concurrently with owner cancellation. A
		// successful check must not turn the canceled listing into a success.
		return nil
	})
	recorder := listingResultRecorder{Toolkit: kit, result: make(chan toolresult.Result, 1)}
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: recorder, RunContext: ctx, Gate: agent.NewToolExecutionGate(2)})
	defer runtime.Close()
	done := make(chan struct{})
	go func() {
		_, _ = runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "parallel-list", Name: "run_code", Arguments: `{"code":"await Promise.all([tools.list_files({}), tools.list_files({})]);"}`}}, nil)
		runtime.Close()
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("parallel listings were not admitted")
	}
	cancel()
	select {
	case <-done:
		result := <-recorder.result
		if !result.IsError || !strings.Contains(result.TextProjection(), `"list_files": interrupted`) {
			t.Fatalf("cancellation receipt: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled listings kept run_code stopping")
	}
	listings := 0
	for _, record := range kit.ToolTelemetry() {
		if record.Name == "run_code" && (record.Success || !strings.Contains(record.Error, context.Canceled.Error())) {
			t.Errorf("program telemetry contradicts interrupted receipt: %+v", record)
		}
		if record.Name == "list_files" {
			listings++
			if record.Success || record.Error != context.Canceled.Error() {
				t.Errorf("nested listing contradicts interrupted receipt: %+v", record)
			}
		}
	}
	if listings != 2 {
		t.Fatalf("got %d nested listing records, want 2", listings)
	}
}
