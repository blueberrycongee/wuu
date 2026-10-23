package credentialstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Exercise selection, revocation, durable reopen, concurrent CAS updates and
// ambiguous transport outcomes through the same registry used by processes.
func TestCredentialServiceSelectionPersistenceAndGeneration(t *testing.T) {
	ctx := context.Background()
	native := NewService("native.credentials", func() (Store, error) { return NewFileStore(filepath.Join(t.TempDir(), "unused.json")), nil })
	path := filepath.Join(t.TempDir(), "credentials.json")
	selected := NewService("external.credentials", func() (Store, error) { return NewFileStore(path), nil })
	client, registry := bindCredentialTest(t, selected, native)
	if _, err := client.Get(ctx, "oauth", "account"); err == nil {
		t.Fatal("prepared provider accepted reads")
	}
	if err := selected.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := native.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "oauth", "account", []byte("0")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := client.Update(ctx, "oauth", "account", func(raw []byte) ([]byte, error) {
				var n int
				if _, err := fmt.Sscan(string(raw), &n); err != nil {
					return nil, err
				}
				return []byte(fmt.Sprint(n + 1)), nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	actual, err := NewFileStore(path).Get(ctx, "oauth", "account")
	if err != nil || string(actual) != "12" {
		t.Fatalf("durable update: %q, %v", actual, err)
	}
	next := NewService("external.credentials", func() (Store, error) { return NewFileStore(filepath.Join(t.TempDir(), "next.json")), nil })
	nextClient, _ := bindCredentialTest(t, next)
	if err := next.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := nextClient.Set(ctx, "oauth", "account", []byte("next")); err != nil {
		t.Fatal(err)
	}
	old, err := client.Get(ctx, "oauth", "account")
	if err != nil || string(old) != "12" {
		t.Fatalf("old binding changed: %q, %v", old, err)
	}
	registry.RevokePlugin(ctx, selected.ID())
	if _, err := client.Get(ctx, "oauth", "account"); err == nil {
		t.Fatal("revoked selected provider fell back")
	}
	if err := selected.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := selected.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "get", Params: json.RawMessage(`{"namespace":"oauth","key":"account"}`)}); err == nil {
		t.Fatal("closed provider served credentials")
	}
}

func TestCredentialClientDoesNotReplayUnknownWrite(t *testing.T) {
	attempts := 0
	client := NewClient(func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "get" {
			return json.RawMessage(`{"value":"b2xk"}`), nil
		}
		attempts++
		return nil, errors.New("transport outcome unknown")
	})
	err := client.Update(context.Background(), "oauth", "account", func([]byte) ([]byte, error) { return []byte("next"), nil })
	if err == nil || attempts != 1 {
		t.Fatalf("uncertain write replayed: attempts=%d err=%v", attempts, err)
	}
}

func TestCredentialServiceSanitizesBackendErrorsAndEmptyValues(t *testing.T) {
	ctx := context.Background()
	secret := "DO_NOT_RETURN_THIS_SECRET"
	provider := NewService("test.credentials", func() (Store, error) { return &failingCredentialStore{secret: secret}, nil })
	if err := provider.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "get", Params: json.RawMessage(`{"namespace":"oauth","key":"account"}`)})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("backend error was not sanitized: %v", err)
	}
	file := NewService("file.credentials", func() (Store, error) { return NewFileStore(filepath.Join(t.TempDir(), "credentials.json")), nil })
	client, _ := bindCredentialTest(t, file)
	if err := file.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "oauth", "empty", []byte{}); err != nil {
		t.Fatal(err)
	}
	if value, err := client.Get(ctx, "oauth", "empty"); err != nil || value == nil || !bytes.Equal(value, []byte{}) {
		t.Fatalf("empty entry lost: %v %v", value, err)
	}
	if err := client.Update(ctx, "oauth", "empty", func(value []byte) ([]byte, error) {
		if value == nil {
			t.Fatal("empty entry treated as absent")
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(ctx, "oauth", "empty"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete: %v", err)
	}
}

type failingCredentialStore struct {
	Store
	secret string
}

func (s *failingCredentialStore) Get(context.Context, string, string) ([]byte, error) {
	return nil, errors.New(s.secret)
}

type credentialConsumer struct{}

func (*credentialConsumer) ID() string { return "test.consumer" }
func (c *credentialConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (*credentialConsumer) Close(context.Context) error                      { return nil }
func (*credentialConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*credentialConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: ServiceName, MajorVersion: 1, Required: true}}
}
func bindCredentialTest(t *testing.T, selected *Service, others ...pluginhost.Client) (*Client, *pluginhost.ServiceRegistry) {
	t.Helper()
	registry, err := pluginhost.NewServiceRegistryWithBindings([]composition.Binding{{Service: ServiceName, Major: 1, Provider: selected.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	clients := append([]pluginhost.Client{selected, &credentialConsumer{}}, others...)
	if conflicts := registry.RegisterClients(clients...); len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	registry.Activate()
	t.Cleanup(func() { registry.Close(context.Background()); selected.Close(context.Background()) })
	return NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := registry.Call(ctx, "test.consumer", pluginhost.ServiceCallParams{Service: ServiceName, Method: method, Params: raw})
		if err != nil {
			return nil, err
		}
		return result, nil
	}), registry
}
