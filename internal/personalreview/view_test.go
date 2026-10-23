package personalreview

import (
	"context"
	"encoding/json"
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
