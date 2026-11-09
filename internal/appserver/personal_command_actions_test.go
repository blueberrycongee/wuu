package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Real UserHost, selected coordinator/effect providers and native shell. Only
// source-history and model transports are fixtures; no external accounts run.
// A pending/rejected/stale action must not spawn; duplicate approval and retry
// must return the original receipt instead of repeating the command's effect.
func TestPersonalCommandApprovalProductionEntry(t *testing.T) {
	for _, choice := range []string{"approve_once", "reject", "command-only", "file-only"} {
		t.Run(choice, func(t *testing.T) {
			f := newPersonalBoundaryFixture(t, nil)
			if choice == "command-only" || choice == "file-only" {
				f.cancelOwner()
				if err := f.host.Close(); err != nil {
					t.Fatal(err)
				}
				f.openHost(WithAccessPolicy(access.PolicyFunc(func(_ context.Context, request access.Request) (access.Decision, error) {
					denied := "personal/file.execute"
					if choice == "file-only" {
						denied = "personal/command.execute"
					}
					return access.Decision{Allow: request.Action != denied, Reason: "fixture operation capability"}, nil
				})))
			}
			before := f.snapshot()
			settings, _ := json.Marshal(map[string]any{"enabled": false, "expected_revision": before.Settings.Revision})
			if _, err := f.host.personal.Call(f.owner, "review.settings", settings, ""); err != nil {
				t.Fatal(err)
			}
			call := providers.ToolCall{ID: "command-once", Name: "bash", Arguments: `{"command":"printf once >> marker.txt"}`}
			if _, err := f.kit.Execute(f.owner, call); err == nil || !strings.Contains(err.Error(), "personal_action_pending") {
				t.Fatalf("not pending: %v", err)
			}
			if _, err := os.Stat(filepath.Join(f.rt.RootDir, "marker.txt")); !os.IsNotExist(err) {
				t.Fatal("command ran before approval")
			}
			records, err := f.host.actionsForTest(t).engine.Records(context.Background(), f.rt.UserSpaceID)
			if err != nil || len(records) != 1 {
				t.Fatalf("records %d: %v", len(records), err)
			}
			r := records[0]
			decision := choice
			if choice == "command-only" || choice == "file-only" {
				decision = "approve_once"
			}
			raw, _ := json.Marshal(map[string]any{"space_id": r.Action.Scope.SpaceID, "id": r.Action.ID, "digest": r.Action.Digest, "expected_revision": r.Revision, "choice": decision})
			if _, err = f.host.personal.Call(f.owner, "review.resolve", raw, ""); err != nil {
				t.Fatal(err)
			}
			if err = f.host.actionsForTest(t).drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err = f.host.personal.Call(f.owner, "review.resolve", raw, ""); err != nil {
				t.Fatal(err)
			}
			_, retryErr := f.kit.Execute(f.owner, call)
			data, readErr := os.ReadFile(filepath.Join(f.rt.RootDir, "marker.txt"))
			if choice == "approve_once" || choice == "command-only" {
				if retryErr != nil || readErr != nil || string(data) != "once" {
					t.Fatalf("receipt/effect: %s %v %v", data, retryErr, readErr)
				}
				if r = f.current(r.Action.Key()); r.Status != personalreview.Succeeded {
					t.Fatalf("status %s", r.Status)
				}
				if choice == "approve_once" {
					call.ID = "new-invocation-same-command"
					if _, err := f.kit.Execute(f.owner, call); err == nil || !strings.Contains(err.Error(), "personal_action_pending") {
						t.Fatalf("new invocation returned old receipt: %v", err)
					}
					records, err := f.host.actionsForTest(t).engine.Records(context.Background(), f.rt.UserSpaceID)
					if err != nil {
						t.Fatal(err)
					}
					var next personalreview.Record
					for _, record := range records {
						if record.Status == personalreview.AwaitingConfirmation {
							next = record
						}
					}
					if next.Action.ID == "" || next.Action.ID == r.Action.ID {
						t.Fatal("new invocation reused prior approval")
					}
					if _, err := f.host.personal.Call(f.owner, "review.resolve", f.command(next), ""); err != nil {
						t.Fatal(err)
					}
					if err := f.host.actionsForTest(t).drain(context.Background()); err != nil {
						t.Fatal(err)
					}
					data, err := os.ReadFile(filepath.Join(f.rt.RootDir, "marker.txt"))
					if err != nil || string(data) != "onceonce" {
						t.Fatalf("distinct invocation not executed: %q %v", data, err)
					}
				}
			} else if retryErr == nil || !os.IsNotExist(readErr) {
				t.Fatalf("rejected command ran: %s %v %v", data, retryErr, readErr)
			}
		})
	}
}

// This fixture deliberately has no protected-path policy. Production Personal
// keeps its existing refusal of detached processes with protected paths; this
// verifies the approval contract in hosts where such processes are supported.
func TestPersonalProcessInputRequiresIndependentApproval(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	before := f.snapshot()
	settings, _ := json.Marshal(map[string]any{"enabled": false, "expected_revision": before.Settings.Revision})
	if _, err := f.host.personal.Call(f.owner, "review.settings", settings, ""); err != nil {
		t.Fatal(err)
	}
	manager, err := proc.NewManager(f.rt.RootDir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.kit.SetProcessManager(manager)
	events := make(chan proc.Event, 16)
	manager.Subscribe(events)
	defer manager.Unsubscribe(events)
	decide := func(call providers.ToolCall, choice string) personalreview.Record {
		t.Helper()
		if _, err := f.kit.Execute(f.owner, call); err == nil || !strings.Contains(err.Error(), "personal_action_pending") {
			t.Fatalf("expected pending %s: %v", call.Name, err)
		}
		records, err := f.host.actionsForTest(t).engine.Records(context.Background(), f.rt.UserSpaceID)
		if err != nil {
			t.Fatal(err)
		}
		var pending personalreview.Record
		for _, r := range records {
			if r.Action.Operation == call.Name && r.Status == personalreview.AwaitingConfirmation {
				pending = r
			}
		}
		if pending.Action.ID == "" {
			t.Fatalf("no pending %s approval: %+v", call.Name, records)
		}
		raw, _ := json.Marshal(map[string]any{"space_id": pending.Action.Scope.SpaceID, "id": pending.Action.ID, "digest": pending.Action.Digest, "expected_revision": pending.Revision, "choice": choice})
		if _, err := f.host.personal.Call(f.owner, "review.resolve", raw, ""); err != nil {
			t.Fatal(err)
		}
		if err := f.host.actionsForTest(t).drain(context.Background()); err != nil {
			t.Fatal(err)
		}
		return f.current(pending.Action.Key())
	}
	start := providers.ToolCall{ID: "start-interactive", Name: "bash", Arguments: `{"command":"printf ready; read value; printf '%s' \"$value\" > stdin.txt","run_in_background":true}`}
	started := decide(start, "approve_once")
	if started.Status != personalreview.Succeeded {
		t.Fatalf("start status: %+v", started)
	}
	var receipt struct {
		ToolResult toolresult.Result `json:"tool_result"`
	}
	if err = json.Unmarshal(started.Result, &receipt); err != nil {
		t.Fatal(err)
	}
	var process proc.Process
	receipt.ToolResult.ModelText = nil
	if err = json.Unmarshal([]byte(receipt.ToolResult.TextProjection()), &process); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = manager.Stop(process.ID) })
	input := func(text string) providers.ToolCall {
		raw, _ := json.Marshal(map[string]any{"action": "write", "process_id": process.ID, "input": text})
		return providers.ToolCall{ID: "stdin", Name: "bash", Arguments: string(raw)}
	}
	rejected := decide(input("unapproved\n"), "reject")
	if rejected.Status != personalreview.Denied {
		t.Fatalf("input not denied: %+v", rejected)
	}
	if _, err = os.Stat(filepath.Join(f.rt.RootDir, "stdin.txt")); !os.IsNotExist(err) {
		t.Fatal("spawn approval admitted later stdin")
	}
	approved := decide(input("approved\n"), "approve_once")
	if approved.Status != personalreview.Succeeded {
		t.Fatalf("input not executed: %+v", approved)
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Process.ID == process.ID && event.Type == proc.EventStopped && event.Cause == proc.EventCauseNaturalExit {
				data, err := os.ReadFile(filepath.Join(f.rt.RootDir, "stdin.txt"))
				if err != nil || string(data) != "approved" {
					t.Fatalf("stdin result: %q %v", data, err)
				}
				return
			}
		case <-timer.C:
			t.Fatal("approved process input did not finish")
		}
	}
}
