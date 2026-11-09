package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
)

type stateCleanupConsumer struct {
	close func(context.Context) error
}

func (*stateCleanupConsumer) ID() string { return "test.state-cleanup" }
func (c *stateCleanupConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (c *stateCleanupConsumer) Close(ctx context.Context) error                { return c.close(ctx) }
func (*stateCleanupConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*stateCleanupConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	var result []pluginhost.ServiceRequirement
	for _, name := range []string{credentialstore.ServiceName, pluginsettings.ServiceName, config.ServiceName, ArtifactServiceName} {
		result = append(result, pluginhost.ServiceRequirement{Name: name, MajorVersion: 1, Required: true})
	}
	return result
}

// Retirement rejects ordinary state operations. The host-owned close callback
// can still flush through its dependencies before those providers close.
func TestStateMethodsRequireAdmissionButShutdownCanPersist(t *testing.T) {
	ctx := context.Background()
	home, project := t.TempDir(), t.TempDir()
	path := filepath.Join(home, "credentials.json")
	credentials := credentialstore.NewService(credentialstore.FileModule, func() (credentialstore.Store, error) { return credentialstore.NewFileStore(path), nil })
	settings := pluginsettings.NewService(pluginsettings.Module, home, project)
	configuration := config.NewMemoryService(config.Module, config.Default())
	artifacts := NewArtifactService(ArtifactModule, home, filepath.Join(home, "workspaces", "test"))
	consumer := &stateCleanupConsumer{}
	clients := []pluginhost.Client{consumer, credentials, settings, configuration, artifacts}
	registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	host := pluginhost.New(clients...)
	host.AttachServiceRegistry(registry, conflicts)
	call := func(service string) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			result, err := registry.Call(ctx, consumer.ID(), pluginhost.ServiceCallParams{Service: service, Method: method, Params: raw})
			if err != nil {
				return nil, err
			}
			return result, nil
		}
	}
	flushed := false
	consumer.close = func(ctx context.Context) error {
		if err := credentialstore.NewClient(call(credentialstore.ServiceName)).Set(ctx, "cleanup", "token", []byte("saved")); err != nil {
			return err
		}
		if _, err := pluginsettings.NewClient(call(pluginsettings.ServiceName)).UpdateState(ctx, "plugin:cleanup", pluginsettings.ScopeUser, func(values map[string]string) error { values["status"] = "saved"; return nil }); err != nil {
			return err
		}
		client := config.NewClient(call(config.ServiceName))
		view, err := client.Read(ctx)
		if err != nil {
			return err
		}
		view, err = client.CompareExchange(ctx, view.Revision, view.Source)
		if err != nil {
			return err
		}
		if !view.Committed {
			return fmt.Errorf("cleanup configuration was not saved")
		}
		flushed = true
		return nil
	}
	defer host.Close(ctx)
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	host.StopBackground()
	for _, provider := range []interface {
		ProvidedServices() []pluginhost.ServiceDescriptor
	}{credentials, settings, configuration, artifacts} {
		for _, service := range provider.ProvidedServices() {
			for _, method := range service.Methods {
				_, err := registry.Call(ctx, consumer.ID(), pluginhost.ServiceCallParams{Service: service.Name, Method: method.Name, Params: json.RawMessage(`{}`)})
				if method.Cleanup {
					// Declared cleanup is routable after retirement, but an empty
					// request cannot forge a held handle or maintenance admission.
					if err == nil {
						t.Errorf("retired %s/%s accepted missing cleanup authority", service.Name, method.Name)
					}
				} else if err == nil || err.Code != "generation_closed" {
					t.Errorf("retired %s/%s admitted: %v", service.Name, method.Name, err)
				}
			}
		}
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !flushed {
		t.Fatal("shutdown did not flush state")
	}
	value, err := credentialstore.NewFileStore(path).Get(ctx, "cleanup", "token")
	if err != nil || string(value) != "saved" {
		t.Fatalf("cleanup credential missing: %v", err)
	}
	state, err := pluginsettings.ReadState(home, project, "plugin:cleanup", pluginsettings.ScopeUser)
	if err != nil || state.Values["status"] != "saved" {
		t.Fatalf("cleanup plugin state missing: %v", err)
	}
}
