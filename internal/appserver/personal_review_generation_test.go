package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type reviewDocumentGeneration struct {
	*persistence.Service
	reads      atomic.Int64
	omitClaims bool
}

func (p *reviewDocumentGeneration) ProvidedServices() []pluginhost.ServiceDescriptor {
	services := p.Service.ProvidedServices()
	if p.omitClaims {
		methods := services[0].Methods[:0]
		for _, method := range services[0].Methods {
			if method.Name != "claim-namespaces" {
				methods = append(methods, method)
			}
		}
		services[0].Methods = methods
	}
	return services
}

func (p *reviewDocumentGeneration) InvokeService(ctx context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	var q persistence.Query
	if input.Method == "read" && json.Unmarshal(input.Params, &q) == nil && q.Namespace == "wuu.personal_review_settings.v1" {
		p.reads.Add(1)
	}
	return p.Service.InvokeService(ctx, input)
}

// Publishing a compatible implementation does not move storage authority or
// retarget an admitted operation. Only fresh owner requests select the new graph.
func TestPersonalReviewRequestsFollowSelectedGeneration(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default()
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", APIKey: "synthetic", Model: "fixture"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{personalActionsConsumerID: true, PersonalCoordinationConsumer: true, PersonalEffectModule: true}}}}
	var providers []*reviewDocumentGeneration
	factory := runtime.NativeProviderFactory{ID: persistence.SQLiteModule, DefaultEnabled: true, Build: func(in runtime.NativeProviderContext) (pluginhost.Client, error) {
		p := &reviewDocumentGeneration{Service: persistence.NewService(persistence.SQLiteModule, filepath.Join(in.DataRoot, "sessions", "documents"), "sqlite"), omitClaims: len(providers) >= 2}
		providers = append(providers, p)
		return p, nil
	}}
	bindings := NewRuntimeBindings()
	plugins := []pluginpkg.Plugin{}
	rt, err := runtime.NewSession(runtime.Options{RootDir: t.TempDir(), WuuHome: home, HomeDir: home, UserSpaceID: "review-space", ProductDomain: "personal", Config: cfg, DiscoveredPlugins: &plugins, NativeProviders: append(bindings.NativeProviders(), factory), NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := rt.Cleanup(); err != nil {
			t.Error(err)
		}
	}()
	host, err := NewUserHost(rt, personalhost.Options{}, WithRuntimeBindings(bindings))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := host.Close(); err != nil {
			t.Error(err)
		}
	}()
	owner, err := clientauthority.AdmitDesktop(principal.WithContext(context.Background(), principal.LocalOwner()), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"review-space","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.personal.Call(owner, "review.list", nil, ""); err != nil {
		t.Fatal(err)
	}
	core := host.actionsForTest(t)
	admitted, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer admitted.Close()
	candidate, err := rt.PreflightExtensionPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = rt.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers=%d", len(providers))
	}
	before0, before1 := providers[0].reads.Load(), providers[1].reads.Load()
	if _, err = host.personal.Call(owner, "review.list", nil, ""); err != nil {
		t.Fatal(err)
	}
	if host.actionsForTest(t) != core {
		t.Fatal("compatible successor replaced pending admission coordinator")
	}
	if providers[0].reads.Load() != before0 || providers[1].reads.Load() <= before1 {
		t.Fatalf("fresh review request calls: original=%d replacement=%d", providers[0].reads.Load()-before0, providers[1].reads.Load()-before1)
	}
	before0, before1 = providers[0].reads.Load(), providers[1].reads.Load()
	if _, err = host.personal.Call(runtime.WithInvocationState(owner, admitted), "review.list", nil, ""); err != nil {
		t.Fatal(err)
	}
	if providers[0].reads.Load() <= before0 || providers[1].reads.Load() != before1 {
		t.Fatalf("admitted review request changed generation: original=%d replacement=%d", providers[0].reads.Load()-before0, providers[1].reads.Load()-before1)
	}
	if err := admitted.Close(); err != nil {
		t.Fatal(err)
	}
	if core.ctx.Err() != nil {
		t.Fatal("old generation cleanup closed shared replacement coordinator")
	}
	before1 = providers[1].reads.Load()
	if _, err := host.personal.Call(runtime.WithInvocationState(owner, admitted), "review.list", nil, ""); err == nil {
		t.Fatal("released invocation silently selected the current generation")
	}
	if providers[1].reads.Load() != before1 {
		t.Fatal("released invocation reached the replacement provider")
	}
	// Matching durable data does not imply that a replacement supports the
	// owned-namespace contract. A cached startup claim must not authorize it.
	candidate, err = rt.PreflightExtensionPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = rt.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := host.personal.Call(owner, "review.list", nil, ""); !errors.Is(err, persistence.ErrNamespaceOwnershipUnsupported) {
		t.Fatalf("replacement without namespace ownership was admitted: %v", err)
	}
	if providers[2].reads.Load() != 0 {
		t.Fatal("replacement without namespace ownership reached the review ledger")
	}
}
