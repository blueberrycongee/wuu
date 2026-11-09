package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// Maintenance must remain a selected, authenticated operator path. Neither a
// legacy file nor a declared document service permits importing into another
// store, replacing data, or acting while a user host is alive.
func TestPersonalReviewMigrationSelectedAuthorityAndSourceBinding(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain, rt.DataRoot = "migration-space", "personal", t.TempDir()
	source := filepath.Join(rt.DataRoot, "personal-review.sqlite")
	legacy, err := personalreview.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.GetSettings(context.Background(), rt.UserSpaceID); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	owner := personalTestOwner(context.Background())
	if _, err := migratePersonalReview(context.Background(), rt, PersonalReviewMigrationRequest{}); err == nil {
		t.Fatal("anonymous migration inspection allowed")
	}
	preview, err := migratePersonalReview(owner, rt, PersonalReviewMigrationRequest{})
	if err != nil || preview.SourceDigest == "" || preview.Provider != "fixture.documents" || preview.Status != "migration-required" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err := migratePersonalReview(owner, rt, PersonalReviewMigrationRequest{Apply: true, ExpectedSourceDigest: "wrong"}); !errors.Is(err, personalreview.ErrLegacySourceChanged) {
		t.Fatalf("stale source accepted: %v", err)
	}
	result, err := migratePersonalReview(owner, rt, PersonalReviewMigrationRequest{Apply: true, ExpectedSourceDigest: preview.SourceDigest})
	if err != nil || result.Status != "migrated" || result.SourceDigest != preview.SourceDigest {
		t.Fatalf("apply: %+v %v", result, err)
	}
	repeat, err := migratePersonalReview(owner, rt, PersonalReviewMigrationRequest{})
	if err != nil || repeat.Status != "already-migrated" {
		t.Fatalf("marker not inspectable: %+v %v", repeat, err)
	}
	// A new invocation cannot borrow the old owner's successful policy decision.
	rt.PluginHost.RetirePlugin(context.Background(), access.NativeModule, errors.New("policy revoked"))
	if _, err := migratePersonalReview(owner, rt, PersonalReviewMigrationRequest{}); err == nil {
		t.Fatal("revoked selected policy still permitted inspection")
	}
}

func TestPersonalReviewMigrationRejectsLiveHostBeforeProviderStartup(t *testing.T) {
	home := t.TempDir()
	binding := userspace.Binding{UserSpaceID: "live", ProductDomain: "personal"}
	dir := statepath.SessionsDir(binding.State(home))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lease, first, err := session.AcquireAppServerPresence(dir)
	if err != nil || !first {
		t.Fatal("presence", err)
	}
	defer lease.Release()
	if err := lease.FinalizeStartup(); err != nil {
		t.Fatal(err)
	}
	_, err = RunPersonalReviewMigration(personalTestOwner(context.Background()), runtime.Options{WuuHome: home, UserSpaceID: binding.UserSpaceID, ProductDomain: binding.ProductDomain}, PersonalReviewMigrationRequest{})
	if err == nil || !strings.Contains(err.Error(), "stop all") {
		t.Fatalf("live host did not block maintenance: %v", err)
	}
}

type sharedMigrationDocuments struct{ *persistence.Service }

func (d *sharedMigrationDocuments) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if p.Method == "authority" {
		return json.Marshal(persistence.Authority{Version: 1, Mode: persistence.ProviderAuthority, StoreID: "shared", StorageTopology: persistence.MultiHost, ExecutionTopology: persistence.MultiHost, Coordinator: "remote-cas", CAS: "store", DurableReceipts: true})
	}
	return d.Service.InvokeService(ctx, p)
}
func TestPersonalReviewMigrationRefusesSharedWriterAuthority(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain, rt.DataRoot = "shared-space", "personal", t.TempDir()
	provider := &sharedMigrationDocuments{persistence.NewService("shared.documents", t.TempDir(), "sqlite")}
	attachTestModelGeneration(t, rt, provider)
	_, err := migratePersonalReview(personalTestOwner(context.Background()), rt, PersonalReviewMigrationRequest{})
	if err == nil || !strings.Contains(err.Error(), "offline provider maintenance") {
		t.Fatalf("shared topology accepted local stopped-host proof: %v", err)
	}
}
