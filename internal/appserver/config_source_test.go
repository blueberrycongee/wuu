package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type configSourceConsumer struct{}

func (*configSourceConsumer) ID() string { return runtime.StateServicesConsumer }
func (c *configSourceConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (*configSourceConsumer) Close(context.Context) error { return nil }
func (*configSourceConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: config.ServiceName, MajorVersion: 1, Required: true}, {Name: credentialstore.ServiceName, MajorVersion: 1, Required: true}}
}
func (*configSourceConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }

type observedConfigSource struct {
	*config.Service
	writes      int
	outcome     string
	beforeWrite func()
}

func (p *observedConfigSource) InvokeService(ctx context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if input.Method == "compare-exchange" {
		p.writes++
		if p.beforeWrite != nil {
			p.beforeWrite()
		}
		if p.outcome == "unknown" {
			return nil, errors.New("private-provider-token")
		}
		if p.outcome == "conflict" {
			return nil, &pluginhost.HostServiceError{Code: "config_conflict", Message: "source changed"}
		}
	}
	raw, err := p.Service.InvokeService(ctx, input)
	if err == nil && input.Method == "compare-exchange" && p.outcome == "committed" {
		var view config.SourceView
		if err := json.Unmarshal(raw, &view); err != nil {
			return nil, err
		}
		view.ActivationError = "project_overlay_invalid"
		return json.Marshal(view)
	}
	return raw, err
}

func newSelectedConfigSourceServer(t *testing.T) (*Server, *observedConfigSource) {
	t.Helper()
	cfg := config.Default()
	cfg.Providers = map[string]config.ProviderConfig{"selected": {Type: "openai-compatible", Model: "original", BaseURL: "https://example.test/v1"}}
	cfg.DefaultProvider = "selected"
	provider := &observedConfigSource{Service: config.NewMemoryService("test.selected-config", cfg)}
	path := filepath.Join(t.TempDir(), "untouched", "config.json")
	consumer := &configSourceConsumer{}
	home := t.TempDir()
	credentials := credentialstore.NewService("test.selected-credentials", func() (credentialstore.Store, error) { return nativeCredentialFixture(t, home), nil })
	native := config.NewService(config.Module, path, "")
	registry, err := pluginhost.NewServiceRegistryWithBindings([]composition.Binding{{Service: config.ServiceName, Major: 1, Provider: provider.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	conflicts := registry.RegisterClients(provider, native, credentials, consumer)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	host := pluginhost.New(provider, credentials, consumer)
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	return newOwnerHandlerFixture(&runtime.Session{PluginHost: host, ConfigPath: path}, nil), provider
}

// Ordinary appserver writes must reach the selected provider even when its
// source has no file. Revocation must not recreate a native configuration.
func TestSelectedConfigurationEditsUseProviderAndFailClosed(t *testing.T) {
	srv, provider := newSelectedConfigSourceServer(t)
	edit := func(source []byte) ([]byte, error) {
		return config.UpdateProviderModelSource(source, "selected", "replacement")
	}
	view, err := srv.updateConfigSource(context.Background(), edit)
	if err != nil || !view.Committed || view.Config.Providers["selected"].Model != "replacement" || provider.writes != 1 {
		t.Fatalf("selected edit failed: writes=%d err=%v", provider.writes, err)
	}
	if _, err := os.Stat(filepath.Dir(srv.rt.ConfigPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unselected source accessed: %v", err)
	}
	if err := provider.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.updateConfigSource(context.Background(), edit); err == nil {
		t.Fatal("closed selection fell back")
	}
	if _, err := os.Stat(filepath.Dir(srv.rt.ConfigPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fallback created native files")
	}
}

func TestSelectedConfigurationCommitOutcomesAreNotReplayed(t *testing.T) {
	for _, outcome := range []string{"unknown", "conflict", "committed"} {
		t.Run(outcome, func(t *testing.T) {
			srv, provider := newSelectedConfigSourceServer(t)
			provider.outcome = outcome
			view, err := srv.updateConfigSource(context.Background(), func(source []byte) ([]byte, error) {
				return config.UpdateProviderModelSource(source, "selected", "replacement")
			})
			if err == nil || strings.Contains(err.Error(), "private-provider-token") || provider.writes != 1 {
				t.Fatalf("unsafe outcome: writes=%d err=%v", provider.writes, err)
			}
			if view.Committed != (outcome == "committed") {
				t.Fatalf("lost commit outcome: %+v", view)
			}
			if outcome == "committed" && !strings.Contains(err.Error(), "saved") {
				t.Fatal("committed failure reported as rollback")
			}
		})
	}
}

// Hand-built protocol fixtures select their file provider explicitly, just as
// production runtime composition does before admitting appserver requests.
func bindConfigSourceForTest(t *testing.T, rt *runtime.Session, additional ...pluginhost.Client) {
	t.Helper()
	if rt.PluginHost != nil && rt.PluginHost.ServiceRegistry() != nil && rt.PluginHost.ServiceRegistry().HasProvider(config.ServiceName, 1) {
		return
	}
	provider := config.NewService(config.Module, rt.ConfigPath, "")
	consumer := &configSourceConsumer{}
	home := os.Getenv("HOME")
	credentials := credentialstore.NewService("test.selected-credentials", func() (credentialstore.Store, error) { return nativeCredentialFixture(t, home), nil })
	attachTestModelGeneration(t, rt, append([]pluginhost.Client{provider, credentials, consumer}, additional...)...)
}

// A transport error cannot authorize restoring a stale credential over a
// concurrent rotation. Config must confirm removal before credential cleanup.
func TestProviderRemoveUnknownConfigDoesNotOverwriteCredentialRotation(t *testing.T) {
	srv, provider := newSelectedConfigSourceServer(t)
	out := &lockedBuffer{}
	srv.out = out
	services, release, err := srv.rt.RetainStateServices()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	view, err := services.Config.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg := view.Config
	cfg.Providers["removed"] = config.ProviderConfig{Type: "openai-compatible", BaseURL: "https://removed.example.test/v1", Model: "fixture"}
	source, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := services.Config.CompareExchange(context.Background(), view.Revision, source); err != nil {
		t.Fatal(err)
	}
	auth := authstorage.NewBound(context.Background(), services.Credentials)
	if err := auth.Set("removed", authstorage.Credentials{APIKey: "original"}); err != nil {
		t.Fatal(err)
	}
	provider.outcome = "unknown"
	provider.beforeWrite = func() {
		if err := auth.Set("removed", authstorage.Credentials{APIKey: "rotated"}); err != nil {
			t.Fatal(err)
		}
	}
	params, _ := json.Marshal(ConfigProviderRemoveParams{Provider: "removed"})
	if err := srv.handleConfigProviderRemove(Request{ID: json.RawMessage(`"remove"`), Params: params}); err != nil {
		t.Fatal(err)
	}
	if responseByID(t, parseOutput(t, out.String()), "remove")["error"] == nil {
		t.Fatal("unknown config outcome succeeded")
	}
	got, err := auth.Get("removed")
	if err != nil || got.APIKey != "rotated" {
		t.Fatalf("uncertain removal overwrote credential rotation: %v", err)
	}
}
