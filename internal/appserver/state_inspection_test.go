package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestStateInspectionMaintenanceRequiresOwnerAndPolicy(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	out := &lockedBuffer{}
	server := &Server{rt: f.rt, personalActions: f.host.actions, out: out, access: localOwnerTestAccess()}
	allowed := true
	server.access.policy = access.PolicyFunc(func(context.Context, access.Request) (access.Decision, error) {
		return access.Decision{Allow: allowed}, nil
	})
	call := func(ctx context.Context, id, method, params string) map[string]any {
		t.Helper()
		raw := []byte(`{"id":"` + id + `","method":"` + method + `","params":` + params + `}`)
		if err := server.handleLine(ctx, raw); err != nil {
			t.Fatal(err)
		}
		return responseByID(t, parseOutput(t, out.String()), id)
	}
	owner := personalTestOwner(context.Background())
	row := call(owner, "owner", "state/inspections/list", `{"target":"settings"}`)
	if row["error"] != nil || row["result"] == nil {
		t.Fatalf("owner cannot inspect selected recovery state: %+v", row)
	}
	for _, target := range []string{"sessions", "review-policy"} {
		if row := call(owner, target, MethodStateInspectionsList, `{"target":"`+target+`"}`); row["error"] != nil || row["result"] == nil {
			t.Fatalf("owner cannot inspect selected %s recovery state: %+v", target, row)
		}
	}
	state, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, directErr := state.Host.CallService(owner, runtime.StateServicesConsumer, pluginsettings.ServiceName, "list-inspections", json.RawMessage(`{"scope":"user"}`)); directErr == nil {
		t.Fatal("declared state consumer bypassed host maintenance admission")
	}
	orphan := json.RawMessage(`{"id":"orphaned-fixture-claim","subject":"plugin:bundled:personal","scope":"user"}`)
	defer func() {
		_, _ = state.Host.CallService(owner, runtime.StateServicesConsumer, pluginsettings.ServiceName, "release-inspection", orphan)
	}()
	if _, acquireErr := state.Host.CallService(owner, runtime.StateServicesConsumer, pluginsettings.ServiceName, "acquire-inspection", orphan); acquireErr != nil {
		t.Fatal(acquireErr)
	}
	row = call(owner, "reconcile", MethodStateInspectionsReconcile, `{"target":"settings","id":"orphaned-fixture-claim","expected_owner":"`+runtime.StateServicesConsumer+`","subject":"plugin:bundled:personal","outcome":"not-dispatched","reason":"stopped test writer; receipt verified before dispatch","writers_stopped":true}`)
	if row["error"] != nil || row["result"] == nil {
		t.Fatalf("explicit owner reconciliation failed: %+v", row)
	}
	result := remarshal[struct {
		Manual bool                            `json:"manual_reconciliation_only"`
		Result pluginsettings.InspectionStatus `json:"result"`
	}](t, row["result"])
	if !result.Manual || result.Result.Outcome != "not-dispatched" || result.Result.ReconciledBy == "" {
		t.Fatalf("recovery lost explicit disposition: %+v", result)
	}
	if _, acquireErr := state.Host.CallService(owner, runtime.StateServicesConsumer, pluginsettings.ServiceName, "acquire-inspection", orphan); acquireErr == nil {
		t.Fatal("late acquisition resurrected reconciled effect")
	}
	guest, err := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "test", Tenant: "tenant", Subject: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if row := call(principal.WithContext(context.Background(), guest), "guest", "state/inspections/list", `{"target":"settings"}`); row["error"] == nil || row["result"] != nil {
		t.Fatalf("task principal read maintenance state: %+v", row)
	}
	allowed = false
	if row := call(owner, "denied", "state/inspections/list", `{"target":"sessions"}`); row["error"] == nil || row["result"] != nil {
		t.Fatalf("selected policy denial ignored: %+v", row)
	}
	allowed = true
	row = call(owner, "unconfirmed", "state/inspections/reconcile", `{"target":"settings","id":"unknown","expected_owner":"old-host","subject":"plugin:bundled:personal","outcome":"not-dispatched","reason":"operator inspected receipt"}`)
	encoded, _ := json.Marshal(row)
	if row["error"] == nil || !strings.Contains(string(encoded), "writers_stopped") {
		t.Fatalf("recovery did not require stopped-writer disposition: %s", encoded)
	}
}
