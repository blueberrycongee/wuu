package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Legacy providers must fail capability admission before sending an unbounded
// export request; the host must not discover incompatibility by loading a store.
type legacyExportProvider struct {
	*persistence.Service
	exports int
}

func (p *legacyExportProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	descriptors := p.Service.ProvidedServices()
	for i := range descriptors {
		for j := range descriptors[i].Methods {
			m := &descriptors[i].Methods[j]
			if m.Name == "export" {
				m.InputSchema = "state.documents.export.input.v1"
				m.OutputSchema = "state.documents.export.output.v1"
			}
		}
	}
	return descriptors
}
func (p *legacyExportProvider) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if params.Method == "export" {
		p.exports++
	}
	return p.Service.InvokeService(ctx, params)
}
func TestStateAuthorityRejectsLegacyExportBeforeReading(t *testing.T) {
	ctx := context.Background()
	provider := &legacyExportProvider{Service: persistence.NewService("legacy", t.TempDir(), "sqlite")}
	defer provider.Close(ctx)
	gate, err := NewStateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close(ctx)
	guarded := gate.Wrap(provider)
	documents := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return guarded.InvokeService(ctx, pluginhost.ServiceInvokeParams{Method: method, Params: raw})
	})
	if err := gate.Preflight(ctx, documents); !errors.Is(err, persistence.ErrMigrationRequired) {
		t.Fatalf("legacy provider admitted: %v", err)
	}
	if provider.exports != 0 {
		t.Fatalf("legacy provider exported %d times", provider.exports)
	}
}

// An already admitted export page is one bounded read, not an independent full
// checkpoint traversal. Complete validation belongs to the collecting caller.
func TestStateAuthorityPageDoesNotRecaptureWholeStore(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			f := newAuthorityFixture(t, t.TempDir(), t.TempDir(), kind)
			admitAuthority(t, f)
			f.provider.mu.Lock()
			f.provider.exports = 0
			f.provider.mu.Unlock()
			raw, err := f.host.CallService(ctx, "third-party", persistence.ServiceName, "export", json.RawMessage(`{"page":{"section":"documents","target_bytes":1024,"max_items":1}}`))
			if err != nil {
				t.Fatal(err)
			}
			var page struct {
				Format     string                 `json:"format"`
				Generation string                 `json:"generation"`
				Documents  []persistence.Document `json:"documents"`
			}
			if err := json.Unmarshal(raw, &page); err != nil || page.Format != "page.v1" || page.Generation == "" || len(page.Documents) != 1 {
				t.Fatalf("invalid guarded page: %s %v", raw, err)
			}
			f.provider.mu.Lock()
			calls := f.provider.exports
			f.provider.mu.Unlock()
			if calls != 1 {
				t.Fatalf("one guarded page exported %d times", calls)
			}
		})
	}
}

func TestStateAuthorityPreparedContinuationDoesNotRecapture(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			seed, err := persistence.Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = seed.Commit(ctx, persistence.Transaction{ID: "seed", Writes: []persistence.Write{{Namespace: "n", Key: "a", Value: json.RawMessage(`true`)}, {Namespace: "n", Key: "b", Value: json.RawMessage(`false`)}}}); err != nil {
				t.Fatal(err)
			}
			if err = seed.Close(); err != nil {
				t.Fatal(err)
			}
			f := makeAuthorityFixture(t, t.TempDir(), dir, kind, false)
			f.consumer.prepare = func(ctx context.Context) error {
				q := persistence.ExportQuery{Section: "documents", TargetBytes: 1024, MaxItems: 1}
				first, err := f.documents.ExportPage(ctx, q)
				if err != nil {
					return err
				}
				if len(first.Documents) != 1 || first.Done {
					return errors.New("first page did not preserve continuation")
				}
				f.provider.mu.Lock()
				before := f.provider.exports
				f.provider.mu.Unlock()
				last := first.Documents[0]
				q.AtRevision = &first.Revision
				q.Generation = first.Generation
				q.After = &persistence.ExportCursor{Namespace: last.Namespace, Key: last.Key}
				second, err := f.documents.ExportPage(ctx, q)
				if err != nil {
					return err
				}
				if len(second.Documents) != 1 || second.Documents[0].Key != "b" {
					return errors.New("wrong continuation")
				}
				f.provider.mu.Lock()
				after := f.provider.exports
				f.provider.mu.Unlock()
				if after-before != 1 {
					return errors.New("prepared continuation recaptured entire checkpoint")
				}
				return nil
			}
			if err = f.host.Prepare(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The authority must forward the validated page encoding. Re-encoding a valid
// external page with Go HTML escaping can violate the caller's byte target.
func TestStateAuthorityPreservesExportPageWireBudget(t *testing.T) {
	value := json.RawMessage(`"` + strings.Repeat("<", 80) + `"`)
	page := persistence.ExportPage{Format: "page.v1", Revision: 2, Generation: "external", Section: "documents", Documents: []persistence.Document{{Namespace: "n", Key: "a", Value: value}, {Namespace: "n", Key: "b", Value: value}}, Receipts: []persistence.CommitRecord{}, Done: true}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(page); err != nil {
		t.Fatal(err)
	}
	raw := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	if len(raw) > 512 {
		t.Fatalf("fixture does not fit actual target: %d", len(raw))
	}
	provider := persistence.NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return raw, nil })
	guarded := persistence.NewClient(func(ctx context.Context, _ string, request json.RawMessage) (json.RawMessage, error) {
		return readAuthority(ctx, provider, persistence.Checkpoint{Revision: 2}, pluginhost.ServiceInvokeParams{Method: "export", Params: request})
	})
	got, err := guarded.ExportPage(context.Background(), persistence.ExportQuery{Section: "documents", TargetBytes: 512, MaxItems: 2})
	if err != nil || len(got.Documents) != 2 {
		t.Fatalf("authority expanded valid external page: %v", err)
	}
}
