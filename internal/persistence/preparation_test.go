package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type preparationReader struct {
	registry *pluginhost.ServiceRegistry
	view     View
	snapshot Snapshot
}

func (p *preparationReader) ID() string { return "prepared-consumer" }
func (p *preparationReader) Status() pluginhost.Status {
	return pluginhost.Status{ID: p.ID(), State: pluginhost.StatePrepared}
}
func (p *preparationReader) Close(context.Context) error                      { return nil }
func (p *preparationReader) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (p *preparationReader) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: ServiceName, MajorVersion: 1, Required: true}}
}
func (p *preparationReader) Prepare(ctx context.Context) error {
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := p.registry.Call(ctx, p.ID(), pluginhost.ServiceCallParams{Service: ServiceName, Method: method, Params: raw})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	var err error
	p.view, err = client.Read(ctx, Query{Namespace: "n", Limit: 1})
	if err != nil {
		return err
	}
	p.snapshot, err = client.Export(ctx)
	if err != nil {
		return err
	}
	if _, err := client.Commit(ctx, Transaction{ID: "forbidden", Writes: []Write{{Namespace: "n", Key: "bad", Value: json.RawMessage(`true`)}}}); err == nil {
		return errors.New("prepared consumer wrote business state")
	}
	return nil
}

// Real Host.Prepare must read the selected provider without starting it or
// creating an empty store. Write methods remain unavailable in that chain.
func TestPreparedDocumentProviderReadsWithoutInitializing(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "candidate")
			provider := NewService("candidate", dir, kind)
			consumer := &preparationReader{}
			host := pluginhost.New(provider, consumer)
			registry, conflicts := pluginhost.BuildServiceRegistry(provider, consumer)
			consumer.registry = registry
			host.AttachServiceRegistry(registry, conflicts)
			defer host.Close(context.Background())
			if err := host.Prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			if consumer.view.Revision != 0 || len(consumer.view.Documents) != 0 || consumer.snapshot.Revision != 0 {
				t.Fatalf("nonempty prepared view: %+v", consumer.view)
			}
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("prepare initialized candidate files: %v", err)
			}
		})
	}
}

// Prepared reads must follow current commits for the shared-authority probe,
// while a JSONL partial tail remains untouched until actual activation.
func TestPreparedDocumentProviderFollowsLiveSourceWithoutRepair(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			writer, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			provider := NewService("candidate", dir, kind)
			if err := provider.Prepare(ctx); err != nil {
				t.Fatal(err)
			}
			defer provider.Close(ctx)
			client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Method: method, Params: raw})
			})
			if view, err := client.Read(ctx, Query{Namespace: "n"}); err != nil || view.Revision != 0 {
				t.Fatalf("initial prepared read: %+v %v", view, err)
			}
			if _, err := writer.Commit(ctx, Transaction{ID: "probe", Writes: []Write{{Namespace: "n", Key: "probe", Value: json.RawMessage(`"fresh"`)}}}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := writer.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "jsonl" {
				binding, err := readBinding(dir)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, binding.Generation, "state.jsonl")
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString(`{"payload":`); err != nil {
					t.Fatal(err)
				}
				file.Close()
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := client.Export(ctx)
				if err != nil || !reflect.DeepEqual(snapshot, actual) {
					t.Fatalf("prepared snapshot: %+v %v", actual, err)
				}
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(before) {
					t.Fatalf("prepare repaired journal: %v", err)
				}
			} else {
				actual, err := client.Export(ctx)
				if err != nil || !reflect.DeepEqual(snapshot, actual) {
					t.Fatalf("prepared snapshot: %+v %v", actual, err)
				}
			}
		})
	}
}
