package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// PersonalReviewMigrationRequest is a local operator instruction, not an RPC
// authority claim. Applying requires the digest returned by a prior inspection.
type PersonalReviewMigrationRequest struct {
	Apply                bool
	ExpectedSourceDigest string
}

type PersonalReviewMigrationResult struct {
	personalreview.LegacyMigrationInspection
	Status   string `json:"status"`
	Source   string `json:"source"`
	Provider string `json:"provider"`
	StoreID  string `json:"store_id,omitempty"`
}

// RunPersonalReviewMigration starts the configured graph only after obtaining
// exclusive stopped-host presence. It never starts UserHost, imports at startup,
// changes the legacy source, or replaces a missing selected provider.
func RunPersonalReviewMigration(ctx context.Context, opts runtime.Options, request PersonalReviewMigrationRequest) (result PersonalReviewMigrationResult, err error) {
	caller, ok := principal.FromContext(ctx)
	if !ok || !caller.IsOwner() || caller.DurableAuthority() != "local-installation" {
		return result, errors.New("review migration requires a verified local owner")
	}
	if err = caller.Check(ctx); err != nil {
		return result, err
	}
	binding := userspace.Binding{UserSpaceID: strings.TrimSpace(opts.UserSpaceID), ProductDomain: strings.TrimSpace(opts.ProductDomain)}
	if binding.Validate() != nil || !binding.IsUser() || binding.Domain() != "personal" {
		return result, errors.New("review migration requires an explicit Personal user space")
	}
	host, err := runtime.ResolveHost(opts.Host)
	if err != nil || host.Kind != runtime.HostLocal {
		return result, errors.New("review migration requires the stopped local host")
	}
	home := opts.WuuHome
	if home == "" {
		home, err = statepath.Home(opts.HomeDir)
		if err != nil {
			return result, err
		}
	}
	dir := statepath.SessionsDir(binding.State(home))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return result, err
	}
	presence, exclusive, err := session.AcquireAppServerPresence(dir)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, presence.Release()) }()
	if !exclusive {
		return result, errors.New("stop all Personal hosts for this user space before review migration")
	}
	// Do not FinalizeStartup: the exclusive presence and startup gate must cover
	// provider activation, snapshot inspection/import, and physical cleanup.
	opts.WuuHome, opts.RootDir, opts.StartupPresence = home, binding.Root(home), presence
	rt, err := runtime.NewSession(opts)
	if err != nil {
		return result, err
	}
	defer func() { _, cleanupErr := rt.Cleanup(); err = errors.Join(err, cleanupErr) }()
	return migratePersonalReview(ctx, rt, request)
}

func migratePersonalReview(ctx context.Context, rt *runtime.Session, request PersonalReviewMigrationRequest) (result PersonalReviewMigrationResult, err error) {
	caller, ok := principal.FromContext(ctx)
	if !ok || !caller.IsOwner() || caller.DurableAuthority() != "local-installation" {
		return result, errors.New("review migration requires a verified local owner")
	}
	if err = caller.Check(ctx); err != nil {
		return result, err
	}
	if request.Apply && strings.TrimSpace(request.ExpectedSourceDigest) == "" {
		return result, errors.New("apply requires --expect-source-digest from a prior inspection")
	}
	state, err := rt.RetainState()
	if err != nil {
		return result, err
	}
	defer state.Close()
	if state.Host == nil || state.Host.ServiceRegistry() == nil {
		return result, errors.New("selected review document provider is unavailable")
	}
	provider, ok := state.Host.ServiceRegistry().ProviderPluginIDFor(personalActionsConsumerID, persistence.ServiceName, 1)
	if !ok {
		return result, errors.New("selected review document provider is unavailable")
	}
	result.Provider, result.Source = provider, filepath.Join(rt.ProductDataRoot(), "personal-review.sqlite")
	action := "state/personal-review/inspect"
	if request.Apply {
		action = "state/personal-review/migrate"
	}
	authorize := func() error {
		if err := caller.Check(ctx); err != nil {
			return err
		}
		decision, err := rt.AuthorizeResource(ctx, access.Request{Principal: caller.Identity(), Owner: true, Action: action, Resources: []access.Resource{{Kind: "user-space", ID: rt.UserBinding().UserSpaceID}, {Kind: "state-provider", ID: provider}}})
		if err != nil {
			return err
		}
		if !decision.Allow {
			return &access.Denied{Action: action, Reason: decision.Reason}
		}
		return caller.Check(ctx)
	}
	if err = authorize(); err != nil {
		return result, err
	}
	scope, err := state.Host.OpenServiceScope(ctx, personalActionsConsumerID, persistence.ServiceName)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, scope.Close()) }()
	ctx = scope.Context
	documents := persistence.NewClient(func(callCtx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		// Source decoding and CAS retries cannot carry a cached allow decision
		// into a later destination mutation.
		if method == "commit" || method == "claim-namespaces" {
			if err := authorize(); err != nil {
				return nil, err
			}
		}
		output, callErr := state.Host.CallService(callCtx, personalActionsConsumerID, persistence.ServiceName, method, raw)
		if callErr != nil {
			return nil, callErr
		}
		return output, nil
	})
	authority, err := documents.Authority(ctx)
	if err != nil {
		return result, err
	}
	if err = requireSingleHostInspection(persistence.InspectionTopology{Version: authority.Version, StorageTopology: authority.StorageTopology, ExecutionTopology: authority.ExecutionTopology}); err != nil {
		return result, err
	}
	result.StoreID = authority.StoreID
	result.LegacyMigrationInspection, err = personalreview.InspectLegacyMigration(ctx, result.Source, documents)
	if err != nil {
		return result, err
	}
	result.Status = "migration-required"
	if result.MigrationRecorded {
		result.Status = "already-migrated"
	}
	if !request.Apply {
		return result, authorize()
	}
	if result.SourceDigest != request.ExpectedSourceDigest {
		return result, personalreview.ErrLegacySourceChanged
	}
	if result.MigrationRecorded {
		return result, authorize()
	}
	if err = authorize(); err != nil {
		return result, err
	}
	result.LegacyMigrationInspection, err = personalreview.MigrateLegacyExpected(ctx, result.Source, documents, request.ExpectedSourceDigest)
	if err != nil {
		return result, fmt.Errorf("review migration failed or commit outcome is unknown; preserve the source and inspect the selected marker before retrying: %w", err)
	}
	if err = personalreview.CheckLegacyMigration(ctx, result.Source, documents); err != nil {
		return result, fmt.Errorf("review import returned but marker verification failed; preserve source and inspect before retrying: %w", err)
	}
	result.MigrationRecorded, result.Status = true, "migrated"
	if err = authorize(); err != nil {
		return result, fmt.Errorf("review import committed; inspect its marker through an authorized maintenance connection: %w", err)
	}
	return result, nil
}
