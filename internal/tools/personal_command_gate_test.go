package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Permission contracts: gate inheritance, worker entry, process-start bypass,
// changed environment, changed authority, and stdin cannot inherit spawn consent.
func TestPersonalCommandGateCoversNativeAndHostProcessEntries(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionID("thread")
	kit.SetAgentIdentity("actor", "root")
	kit.SetBoundary(UnconfinedBoundary())
	kit.SetPermissionMode("unconfined")
	kit.SetAuthorizer(&processScopeAuthorizer{})
	manager, err := proc.NewManager(kit.RootDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetProcessManager(manager)
	calls := []string{}
	kit.SetCommandActionGate(func(_ context.Context, _ *Toolkit, call providers.ToolCall) (toolresult.Result, error) {
		calls = append(calls, call.Name)
		return toolresult.Result{}, errors.New("needs owner")
	})
	clone, err := kit.CloneForRoot(kit.RootDir())
	if err != nil {
		t.Fatal(err)
	}
	clone.SetApproveForMe(false)
	for _, name := range []string{"bash", "process"} {
		args := `{"command":"printf denied > marker"}`
		if name == "process" {
			args = `{"action":"write","process_id":"unstarted","input":"printf injected\\n"}`
		}
		_, err = clone.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: args})
		if err == nil || !strings.Contains(err.Error(), "needs owner") {
			t.Fatalf("%s bypass: %v", name, err)
		}
	}
	_, err = clone.ExecuteEnvironmentResult(context.Background(), providers.ToolCall{Name: "bash", Arguments: `{"command":"printf denied > marker"}`})
	if err == nil || !strings.Contains(err.Error(), "needs owner") {
		t.Fatalf("worker bypass: %v", err)
	}
	_, _, err = clone.StartPluginProcess(context.Background(), "printf denied > marker", "")
	if err == nil || !strings.Contains(err.Error(), "needs owner") {
		t.Fatalf("host process bypass: %v", err)
	}
	if strings.Join(calls, ",") != "bash,process,bash,process_start" {
		t.Fatalf("gated calls: %v", calls)
	}
	if _, err = os.Stat(filepath.Join(kit.RootDir(), "marker")); !os.IsNotExist(err) {
		t.Fatalf("executed before approval: %v", err)
	}
}

func TestPersonalCommandActionBindsEnvironmentAndAuthority(t *testing.T) {
	for _, change := range []string{"none", "environment", "permission", "read-only", "cancel", "hard-deny"} {
		t.Run(change, func(t *testing.T) {
			kit, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			kit.SetGitAttributionEnabled(false)
			kit.SetPermissionMode("unconfined")
			kit.SetBoundary(UnconfinedBoundary())
			kit.SetAuthorizer(&fencedFileAuthorizer{mode: "allow"})
			call := providers.ToolCall{ID: "command", Name: "bash", Arguments: `{"command":"printf exact > result.txt"}`}
			if change == "hard-deny" {
				call.Arguments = `{"command":"cat $WUU_HOME/auth.json"}`
			}
			action, err := kit.PrepareCommandAction(context.Background(), call)
			if change == "hard-deny" {
				if err == nil {
					t.Fatal("credential command admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			switch change {
			case "environment":
				t.Setenv("PERSONAL_COMMAND_FIXTURE", "changed")
			case "permission":
				kit.SetPermissionMode("read_only")
			case "read-only":
				kit.SetBoundary(ReadOnlyBoundary())
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err = kit.ExecuteCommandAction(ctx, action)
			data, readErr := os.ReadFile(filepath.Join(kit.RootDir(), "result.txt"))
			if change == "none" {
				if err != nil || readErr != nil || string(data) != "exact" {
					t.Fatalf("execution: %s %v %v", data, err, readErr)
				}
			} else if err == nil || !os.IsNotExist(readErr) {
				t.Fatalf("changed authority executed: %s %v %v", data, err, readErr)
			}
		})
	}
}

func TestPersonalCommandApprovalBindsResolvedLocalRunner(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetGitAttributionEnabled(false)
	kit.SetBoundary(UnconfinedBoundary())
	kit.SetPermissionMode("unconfined")
	runner := filepath.Join(kit.RootDir(), "node_modules", ".bin", "vitest")
	if err = os.MkdirAll(filepath.Dir(runner), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(runner, []byte("#!/bin/sh\nprintf ran > runner-result.txt\n"), 0700); err != nil {
		t.Fatal(err)
	}
	action, err := kit.PrepareCommandAction(context.Background(), providers.ToolCall{Name: "bash", Arguments: `{"command":"npx vitest run"}`})
	if err != nil {
		t.Fatal(err)
	}
	if action.ResolvedCommand != "./node_modules/.bin/vitest run" {
		t.Fatalf("actual command not presented: %+v", action)
	}
	if _, err = kit.ExecuteCommandAction(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(kit.RootDir(), "runner-result.txt"))
	if err != nil || string(data) != "ran" {
		t.Fatalf("reviewed command did not run: %s %v", data, err)
	}
	if err = os.Remove(runner); err != nil {
		t.Fatal(err)
	}
	if _, err = kit.ExecuteCommandAction(context.Background(), action); err == nil {
		t.Fatal("missing reviewed runner allowed fallback")
	}
}
