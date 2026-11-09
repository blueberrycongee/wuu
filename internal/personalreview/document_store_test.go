package personalreview

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// The selected provider owns durability: CAS and audit are atomic, retired
// wrappers cannot reopen local files, and an uncertain dispatch claim never
// permits an external effect or automatic retry.
func TestSelectedReviewStorePreservesCASAuditAndRecovery(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			docs, err := persistence.Open(ctx, t.TempDir(), kind)
			if err != nil {
				t.Fatal(err)
			}
			defer docs.Close()
			store, err := NewDocumentStore(docs)
			if err != nil {
				t.Fatal(err)
			}
			settings, err := store.GetSettings(ctx, "space/a")
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			winners := make(chan Settings, 8)
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					changed, err := store.SetSettings(ctx, "space/a", true, settings.Revision, "owner")
					if err == nil {
						winners <- changed
					} else if !errors.Is(err, ErrConflict) {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			close(winners)
			if len(winners) != 1 {
				t.Fatalf("CAS winners=%d", len(winners))
			}
			events, err := store.SettingsEvents(ctx, "space/a")
			if err != nil || len(events) != 1 || events[0].Actor != "owner" {
				t.Fatalf("audit=%+v err=%v", events, err)
			}
			other, err := store.SettingsEvents(ctx, "space")
			if err != nil || len(other) != 0 {
				t.Fatalf("scope leaked: %+v %v", other, err)
			}
			f := newFixture(t)
			f.store = store
			f.engine = f.makeEngine()
			r := f.approve(f.propose("synthetic.send"))
			r.Status = Executing
			if _, err = store.save(ctx, r, r.Revision, "host", f.now); err != nil {
				t.Fatal(err)
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err = store.GetSettings(ctx, "space/a"); err == nil {
				t.Fatal("retired wrapper remained usable")
			}
			reopened, err := NewDocumentStore(docs)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			f.store = reopened
			f.engine = f.makeEngine()
			if err = f.engine.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := f.engine.Execute(ctx, r.Action.Key())
			if err != nil || got.Status != Uncertain || f.calls != 0 {
				t.Fatalf("claim replayed: %+v %v calls=%d", got, err, f.calls)
			}
			audit, err := reopened.Events(ctx, r.Action.Key())
			if err != nil || len(audit) < 3 {
				t.Fatalf("action audit=%+v %v", audit, err)
			}
		})
	}
}

type uncertainReviewDocuments struct {
	persistence.Documents
	loseClaim bool
}

func (d *uncertainReviewDocuments) ClaimNamespaces(ctx context.Context, namespaces []string) error {
	return persistence.ClaimNamespaces(ctx, d.Documents, namespaces)
}
func (d *uncertainReviewDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	r, err := d.Documents.Commit(ctx, tx)
	if err == nil && d.loseClaim {
		d.loseClaim = false
		return persistence.Receipt{}, errors.New("synthetic lost durable response")
	}
	return r, err
}
func TestSelectedReviewUncertainClaimDoesNotReachEffect(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	transport := &uncertainReviewDocuments{Documents: docs}
	store, err := NewDocumentStore(transport)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	f.store = store
	f.engine = f.makeEngine()
	r := f.approve(f.propose("synthetic.send"))
	transport.loseClaim = true
	if _, err = f.engine.Execute(ctx, r.Action.Key()); err == nil {
		t.Fatal("unknown commit response ignored")
	}
	if f.calls != 0 {
		t.Fatal("effect executed after unknown dispatch claim")
	}
	if err = f.engine.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := f.engine.Execute(ctx, r.Action.Key())
	if err != nil || got.Status != Uncertain || f.calls != 0 {
		t.Fatalf("uncertain claim replayed: %+v %v", got, err)
	}
}

func TestLegacyReviewMigrationRetainsSettingsActionsAndAudit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := f.approve(f.propose("synthetic.save"))
	settings, err := f.store.GetSettings(ctx, "personal-space")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.SetSettings(ctx, "personal-space", true, settings.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	docs, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "selected"), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	if err = MigrateLegacy(ctx, f.storePath, docs); err != nil {
		t.Fatal(err)
	}
	selected, err := NewDocumentStore(docs)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	got, err := selected.get(ctx, r.Action.Key())
	if err != nil || got.Revision != r.Revision || got.Status != Approved {
		t.Fatalf("record changed: %+v %v", got, err)
	}
	events, err := selected.SettingsEvents(ctx, "personal-space")
	if err != nil || len(events) != 1 || events[0].Actor != "owner" {
		t.Fatalf("audit lost: %+v %v", events, err)
	}
	if err = MigrateLegacy(ctx, f.storePath, docs); err == nil {
		t.Fatal("nonempty target silently overwritten")
	}
}

func TestLegacyMigrationGateRequiresCommittedMatchingSource(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	if err = CheckLegacyMigration(ctx, filepath.Join(t.TempDir(), "absent.sqlite"), docs); err != nil {
		t.Fatal(err)
	}
	if err = CheckLegacyMigration(ctx, f.storePath, docs); !errors.Is(err, ErrLegacyMigrationRequired) {
		t.Fatalf("legacy ledger silently ignored: %v", err)
	}
	if err = MigrateLegacy(ctx, f.storePath, docs); err != nil {
		t.Fatal(err)
	}
	if err = CheckLegacyMigration(ctx, f.storePath, docs); err != nil {
		t.Fatal("committed migration not recognized", err)
	}
	_, err = f.store.GetSettings(ctx, "new-source-change")
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckLegacyMigration(ctx, f.storePath, docs); !errors.Is(err, ErrLegacyMigrationRequired) {
		t.Fatalf("changed legacy source hidden: %v", err)
	}
}

func TestSelectedPolicyInspectionFencesMutationNotActionReceipts(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	store, err := NewDocumentStore(docs)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := store.GetSettings(ctx, "space")
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithPolicyInspection(ctx, func() error {
		if _, err := store.SetSettings(ctx, "space", true, settings.Revision, "owner"); !errors.Is(err, persistence.ErrInspectionActive) {
			t.Fatalf("policy mutation not fenced: %v", err)
		}
		return persistence.Update(ctx, docs, func(tx *persistence.Tx) error {
			return tx.Put(reviewEvents, "receipt", map[string]string{"status": "uncertain"})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetSettings(ctx, "space", true, settings.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
}

type unownedReviewDocuments struct{ persistence.Documents }

func TestReviewRefusesUnownedSelectedAuthorityNamespaces(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	store, err := NewDocumentStore(unownedReviewDocuments{docs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetSettings(ctx, "space"); err == nil {
		t.Fatal("unprotected selected provider became review authority")
	}
	view, err := docs.Read(ctx, persistence.Query{Namespace: reviewSettings})
	if err != nil || len(view.Documents) != 0 {
		t.Fatalf("unsupported authority wrote rows: %+v %v", view, err)
	}
}

type reviewInspectionRecovery struct {
	persistence.Documents
	calls int
}

func (d *reviewInspectionRecovery) ListInspections(context.Context) (persistence.InspectionList, error) {
	return persistence.InspectionList{Inspections: []persistence.InspectionStatus{{ID: "review", Namespaces: []string{reviewSettings}}, {ID: "other", Namespaces: []string{"other"}}, {ID: "mixed", Namespaces: []string{reviewSettings, "other"}}}}, nil
}
func (d *reviewInspectionRecovery) ReconcileInspection(_ context.Context, r persistence.InspectionResolution) (persistence.InspectionStatus, error) {
	d.calls++
	return persistence.InspectionStatus{ID: r.ID, Namespaces: r.Namespaces}, nil
}
func (d *reviewInspectionRecovery) Authority(context.Context) (persistence.Authority, error) {
	return persistence.LegacyAuthority(), nil
}
func TestReviewMaintenanceCannotReconcileForeignNamespaces(t *testing.T) {
	docs := &reviewInspectionRecovery{}
	store, err := NewDocumentStore(docs)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListPolicyInspections(context.Background())
	if err != nil || len(listed.Inspections) != 1 || listed.Inspections[0].ID != "review" {
		t.Fatalf("foreign guard leaked %+v %v", listed, err)
	}
	if _, err := store.ReconcilePolicyInspection(context.Background(), persistence.InspectionResolution{InspectionRequest: persistence.InspectionRequest{ID: "mixed", Namespaces: []string{reviewSettings, "other"}}}); err == nil || docs.calls != 0 {
		t.Fatal("foreign reconciliation forwarded")
	}
	topology, err := store.PolicyInspectionAuthority(context.Background())
	if err != nil || topology.StorageTopology != persistence.SingleHost {
		t.Fatalf("selected topology lost %+v %v", topology, err)
	}
}
