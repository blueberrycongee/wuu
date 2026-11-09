package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/principal"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestOwnedNamespacesBindActualCallerAndSurvivePortableSnapshot(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			service := NewService("selected", t.TempDir(), kind)
			if err := service.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			defer service.Close(ctx)
			client := func(owner string) *Client {
				return NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
					return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: owner, Service: ServiceName, Method: method, Params: raw})
				})
			}
			owner, other := client("authority-owner"), client("ordinary-plugin")
			if err := ClaimNamespaces(ctx, owner, []string{"authority"}); err != nil {
				t.Fatal(err)
			}
			if err := Update(ctx, owner, func(tx *Tx) error { return tx.Put("authority", "grant", map[string]any{"active": true}) }); err != nil {
				t.Fatal(err)
			}
			if _, err := other.Read(ctx, Query{Namespace: "authority"}); !errors.Is(err, ErrNamespaceOwnership) {
				t.Fatalf("foreign authority read: %v", err)
			}
			if err := Update(ctx, other, func(tx *Tx) error { return tx.Put("authority", "grant", false) }); !errors.Is(err, ErrNamespaceOwnership) {
				t.Fatalf("foreign authority write: %v", err)
			}
			if _, err := other.Export(ctx); !errors.Is(err, ErrNamespaceOwnership) {
				t.Fatalf("export exposed owned namespace: %v", err)
			}
			if _, err := other.AcquireInspection(ctx, InspectionRequest{ID: "foreign", Namespaces: []string{"authority"}}); !errors.Is(err, ErrNamespaceOwnership) {
				t.Fatalf("foreign guard admitted: %v", err)
			}
			if err := other.ClaimNamespaces(ctx, []string{"authority"}); !errors.Is(err, ErrNamespaceOwnership) {
				t.Fatalf("foreign claim admitted: %v", err)
			}
			snapshot, err := owner.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			target, err := Open(ctx, t.TempDir(), kind)
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			ownerCtx := withNamespaceCaller(ctx, "authority-owner")
			if _, err := target.Import(ownerCtx, snapshot); err != nil {
				t.Fatal(err)
			}
			if _, err := target.Read(withNamespaceCaller(ctx, "ordinary-plugin"), Query{Namespace: "authority"}); !errors.Is(err, ErrNamespaceOwnership) {
				t.Fatalf("snapshot lost ownership: %v", err)
			}
			if err := Update(ctx, owner, func(tx *Tx) error {
				return tx.Put(namespaceOwnershipNamespace, "authority", map[string]any{"version": 1, "namespace": "authority", "owner": "ordinary-plugin"})
			}); !errors.Is(err, ErrNamespaceOwnership) {
				t.Fatalf("raw commit changed ownership: %v", err)
			}
			if err := Update(ctx, other, func(tx *Tx) error { return tx.Put("legacy", "key", true) }); err != nil {
				t.Fatal(err)
			}
			if err := owner.ClaimNamespaces(ctx, []string{"legacy"}); !errors.Is(err, ErrMigrationRequired) {
				t.Fatalf("adopted existing unowned data: %v", err)
			}
		})
	}
}

func TestNamespaceClaimPlanMatchesDurableMutation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	steward := context.WithValue(ctx, namespaceIdentityKey{}, namespaceIdentity{caller: "host", steward: true})
	first, err := PrepareNamespaceClaim(steward, s, "provider", []string{"b", "a", "a"})
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := PrepareNamespaceClaim(steward, s, "provider", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(repeat)
	if string(a) != string(b) {
		t.Fatal("claim plan depends on request ordering")
	}
	before, err := s.Checkpoint(steward)
	if err != nil {
		t.Fatal(err)
	}
	after, err := NextCheckpoint(steward, s, before, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimNamespaces(withNamespaceCaller(ctx, "provider"), []string{"b", "a"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Checkpoint(steward)
	if err != nil || got != after {
		t.Fatalf("planned checkpoint differs: got=%+v want=%+v err=%v", got, after, err)
	}
	retry, err := PrepareNamespaceClaim(steward, s, "provider", []string{"a", "b"})
	if err != nil || len(retry.Writes) != 0 {
		t.Fatalf("lost reply did not reconcile: %+v %v", retry, err)
	}
}

func TestOwnedNamespacePreparationAndStewardship(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := NewService("selected", dir, "jsonl")
	if err := s.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	owner := withNamespaceCaller(ctx, "owner")
	if err := s.store.ClaimNamespaces(owner, []string{"secret"}); err != nil {
		t.Fatal(err)
	}
	if err := Update(owner, s.store, func(tx *Tx) error { return tx.Put("secret", "key", true) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s = NewService("selected", dir, "jsonl")
	defer s.Close(ctx)
	for _, method := range []string{"read", "export", "checkpoint"} {
		raw := json.RawMessage(`{}`)
		if method == "read" {
			raw = json.RawMessage(`{"namespace":"secret"}`)
		}
		p := pluginhost.ServiceInvokeParams{Caller: "ordinary", Method: method, Params: raw}
		if _, err := s.InvokeService(ctx, p); err == nil {
			t.Fatalf("prepared %s exposed ownership", method)
		}
		digest := sha256.Sum256(raw)
		p.StorageStewardship = &pluginhost.StorageStewardship{Version: 1, Authority: "configured", Provider: "selected", Operation: "verify", InputJSON: string(raw), InputDigest: hex.EncodeToString(digest[:])}
		if _, err := s.InvokeService(ctx, p); err != nil {
			t.Fatalf("steward %s: %v", method, err)
		}
		p.StorageStewardship.Provider = "foreign"
		if _, err := s.InvokeService(ctx, p); err == nil {
			t.Fatal("foreign stewardship accepted")
		}
	}
}

func TestInspectionMaintenanceRequiresHostAttestation(t *testing.T) {
	ctx := context.Background()
	s := NewService("selected", t.TempDir(), "jsonl")
	if err := s.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	p := pluginhost.ServiceInvokeParams{Caller: "ordinary", Method: "list-inspections", Params: json.RawMessage(`{}`)}
	if _, err := s.InvokeService(ctx, p); err == nil {
		t.Fatal("ordinary consumer reached operator maintenance")
	}
	p.Admission = inspectionTestAdmission(p.Params, "state/inspections/list")
	if _, err := s.InvokeService(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Params = json.RawMessage(`{ "unexpected":true }`)
	if _, err := s.InvokeService(ctx, p); err == nil {
		t.Fatal("maintenance proof not bound to bytes")
	}
}
func inspectionTestAdmission(raw []byte, action string) *pluginhost.ServiceAdmission {
	digest := sha256.Sum256(raw)
	return &pluginhost.ServiceAdmission{Version: 1, Principal: principal.LocalOwner().Identity(), Action: action, InputJSON: string(raw), InputDigest: hex.EncodeToString(digest[:])}
}

func TestMigrationExportCannotDiscardCrashedInspection(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	request := InspectionRequest{ID: "crashed", Namespaces: []string{"authority"}}
	if _, err := store.AcquireInspection(ctx, request); err != nil {
		t.Fatal(err)
	}
	store.Close()
	s := NewService("selected", dir, "jsonl")
	if err := s.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	raw := json.RawMessage(`{}`)
	digest := sha256.Sum256(raw)
	p := pluginhost.ServiceInvokeParams{Caller: "host", Method: "export", Params: raw, StorageStewardship: &pluginhost.StorageStewardship{Version: 1, Authority: "configured", Provider: "selected", Operation: "verify", InputJSON: string(raw), InputDigest: hex.EncodeToString(digest[:])}}
	if _, err := s.InvokeService(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.StorageStewardship.Operation = "migration"
	if _, err := s.InvokeService(ctx, p); err == nil {
		t.Fatal("migration discarded durable guard")
	}
	if err := s.store.ReleaseInspection(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InvokeService(ctx, p); err != nil {
		t.Fatal(err)
	}
}
