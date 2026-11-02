package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Real UserHost, selected coordinator/effect providers and native shell. Only
// source-history and model transports are fixtures; no external accounts run.
// A pending/rejected/stale action must not spawn; duplicate approval and retry
// must return the original receipt instead of repeating the command's effect.
func TestPersonalCommandApprovalProductionEntry(t *testing.T) {
	for _, choice := range []string{"approve_once", "reject"} {
		t.Run(choice, func(t *testing.T) {
			f := newPersonalBoundaryFixture(t, nil)
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
			raw, _ := json.Marshal(map[string]any{"space_id": r.Action.Scope.SpaceID, "id": r.Action.ID, "digest": r.Action.Digest, "expected_revision": r.Revision, "choice": choice})
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
			if choice == "approve_once" {
				if retryErr != nil || readErr != nil || string(data) != "once" {
					t.Fatalf("receipt/effect: %s %v %v", data, retryErr, readErr)
				}
				if r = f.current(r.Action.Key()); r.Status != personalreview.Succeeded {
					t.Fatalf("status %s", r.Status)
				}
			} else if retryErr == nil || !os.IsNotExist(readErr) {
				t.Fatalf("rejected command ran: %s %v %v", data, retryErr, readErr)
			}
		})
	}
}
