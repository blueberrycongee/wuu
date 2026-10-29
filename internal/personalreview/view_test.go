package personalreview

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestOwnerSnapshotPreservesCanonicalParameters(t *testing.T) {
	f := newFixture(t)
	r, err := f.engine.Propose(context.Background(), Proposal{ID: "exact-numbers", Scope: f.authority.Scope, Operation: "synthetic.save", Arguments: json.RawMessage(`{"target":"synthetic-target","integer":9007199254740993,"overflow":1e400,"fraction":0.123456789012345678901}`)})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.engine.OwnerSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Schema   string `json:"schema"`
		Requests []struct {
			Action struct {
				ArgumentsJSON string `json:"arguments_json"`
				Digest        string `json:"digest"`
			} `json:"action"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != "personal-review.v1" || len(decoded.Requests) != 1 || decoded.Requests[0].Action.ArgumentsJSON != string(r.Action.Arguments) || decoded.Requests[0].Action.Digest != r.Action.Digest {
		t.Fatalf("owner view changed canonical parameters or digest: %s", wire)
	}
	t.Logf("Lossless owner snapshot: %s", wire)
}

func TestOwnerSnapshotRejectsUntrustedOrUnrepresentableRecords(t *testing.T) {
	for _, invalid := range []string{"identity", "digest", "revision", "requirements"} {
		t.Run(invalid, func(t *testing.T) {
			f := newFixture(t)
			r := f.propose("synthetic.save")
			switch invalid {
			case "identity":
				f.identity.Channel = "genui"
			case "digest":
				r.Action.Digest = "mismatch"
			case "revision":
				r.Revision = 9007199254740992
			case "requirements":
				r.Action.Scope.RequirementsRevision = 9007199254740992
				r.Action.Digest = ""
				r.Action.Digest = digest(r.Action)
			}
			if invalid != "identity" {
				body, err := json.Marshal(r)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.db.Exec(`UPDATE personal_review_actions SET body=? WHERE space=? AND id=?`, body, r.Action.Scope.SpaceID, r.Action.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.engine.OwnerSnapshot(context.Background()); err == nil {
				t.Fatal("invalid record was exposed as an approvable owner snapshot")
			}
			if f.calls != 0 {
				t.Fatal("view caused an effect")
			}
		})
	}
}

func TestOwnerSnapshotProjectsOnlyBoundedReceiptNotToolPayload(t *testing.T) {
	for _, scenario := range []string{"envelope", "oversized-legacy", "oversized-receipt"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			var result json.RawMessage
			want := ""
			switch scenario {
			case "envelope":
				result = json.RawMessage(`{"receipt":{"sha256":"synthetic-hash","bytes":9007199254740993},"tool_result":{"text":"not an owner receipt"}}`)
				want = `{"sha256":"synthetic-hash","bytes":9007199254740993}`
			case "oversized-legacy":
				result, _ = json.Marshal(map[string]string{"body": strings.Repeat("x", 65537)})
			case "oversized-receipt":
				result, _ = json.Marshal(map[string]any{"receipt": map[string]string{"body": strings.Repeat("x", 65537)}, "tool_result": "private result"})
			}
			op := f.engine.operations["synthetic.save"]
			op.Execute = func(context.Context, Action) (json.RawMessage, error) { return result, nil }
			f.engine.operations["synthetic.save"] = op
			f.run(f.approve(f.propose("synthetic.save")))
			view, err := f.engine.OwnerSnapshot(context.Background())
			if err != nil || len(view.Requests) != 1 || view.Requests[0].ReceiptJSON != want {
				t.Fatalf("unexpected owner receipt for %s: error=%v requests=%d", scenario, err, len(view.Requests))
			}
		})
	}
}
