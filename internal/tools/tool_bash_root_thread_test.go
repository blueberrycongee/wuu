package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func startBackgroundForRootThreadTest(t *testing.T, arguments string, bind func(kit *Toolkit)) ([]string, proc.Process) {
	t.Helper()
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.CleanupSession() })
	kit.SetProcessManager(manager)
	bind(kit)

	startResponse, err := kit.Execute(context.Background(), providers.ToolCall{Name: "process", Arguments: arguments})
	if err != nil {
		t.Fatalf("start background: %v", err)
	}

	list, err := manager.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected exactly one command session, got %d", len(list))
	}
	listResponse, err := kit.Execute(context.Background(), providers.ToolCall{Name: "process", Arguments: `{"action":"list"}`})
	if err != nil {
		t.Fatalf("list background: %v", err)
	}
	readResponse, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "process",
		Arguments: `{"action":"read","process_id":"` + list[0].ID + `"}`,
	})
	if err != nil {
		t.Fatalf("read background: %v", err)
	}
	return []string{startResponse, listResponse, readResponse}, list[0]
}

// The durable record names the conversation that owns the command without
// claiming lifecycle cleanup behavior in this step.
func TestStartBackgroundStampsBoundThreadOnRecord(t *testing.T) {
	_, got := startBackgroundForRootThreadTest(t,
		`{"action":"start","command":"sleep 60"}`,
		func(kit *Toolkit) { kit.SetSessionID("thread-abc") },
	)
	if got.RootThreadID != "thread-abc" {
		t.Fatalf("RootThreadID = %q, want thread-abc", got.RootThreadID)
	}
}

// root_thread_id is host state, not a model argument. A model that invents one
// must not be able to point durable ownership at another conversation.
func TestStartBackgroundIgnoresModelSuppliedRootThread(t *testing.T) {
	_, got := startBackgroundForRootThreadTest(t,
		`{"action":"start","command":"sleep 60","root_thread_id":"someone-elses-thread"}`,
		func(kit *Toolkit) { kit.SetSessionID("thread-abc") },
	)
	if got.RootThreadID != "thread-abc" {
		t.Fatalf("RootThreadID = %q, want the bound thread-abc, not the model-supplied value", got.RootThreadID)
	}
}

func TestStartBackgroundRejectsUnboundOrPendingSession(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	kit.SetProcessManager(manager)

	for _, sessionID := range []string{"", "session-pending"} {
		kit.SetSessionID(sessionID)
		_, err = kit.Execute(context.Background(), providers.ToolCall{
			Name:      "process",
			Arguments: `{"action":"start","command":"sleep 60","root_thread_id":"model-invented"}`,
		})
		if err == nil || !strings.Contains(err.Error(), "bound session ID") {
			t.Fatalf("session %q process start error = %v, want bound session requirement", sessionID, err)
		}
	}
	list, listErr := manager.List()
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if len(list) != 0 {
		t.Fatalf("unbound model command created records: %+v", list)
	}
}

func TestBashResponsesOmitInternalCommandIdentityFields(t *testing.T) {
	responses, stored := startBackgroundForRootThreadTest(t,
		`{"action":"start","command":"sleep 60"}`,
		func(kit *Toolkit) { kit.SetSessionID("thread-redaction") },
	)
	for i, response := range responses {
		if strings.Contains(response, "root_thread_id") || strings.Contains(response, "host_generation_id") {
			t.Fatalf("response %d exposed internal command identity: %s", i, response)
		}
	}
	if stored.RootThreadID != "thread-redaction" || stored.HostGenerationID == "" {
		t.Fatalf("persisted record lost internal command identity: %+v", stored)
	}
}
