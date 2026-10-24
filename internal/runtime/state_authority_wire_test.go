package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type authorityWireObserver struct {
	pluginhost.ServiceInvoker
	inspect func(context.Context, pluginhost.ServiceInvokeParams)
}

func (p *authorityWireObserver) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	p.inspect(ctx, params)
	return p.ServiceInvoker.InvokeService(ctx, params)
}

// Forwarded requests must retain authenticated metadata and durable admission,
// including a retry whose original revision is now older than the host head.
func TestStateAuthorityCommitWireAdmission(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			f := newAuthorityFixture(t, home, filepath.Join(home, "state"), kind)
			admitAuthority(t, f)
			revision := authorityHead(t, f.documents).Revision
			raw := json.RawMessage(fmt.Sprintf(` { "ID":"wire", "expected_revision":"%d", "writes":[{"namespace":"business","key":"wire","value": { "number":9007199254740993,"html":"<>&" }}] } `, revision))
			var expected persistence.Transaction
			if err := json.Unmarshal(raw, &expected); err != nil {
				t.Fatal(err)
			}
			scope := &pluginhost.ExecutionScope{ID: "execution", PluginID: "third-party", Context: context.Background()}
			seen := 0
			f.gate.provider = &authorityWireObserver{ServiceInvoker: f.gate.provider, inspect: func(ctx context.Context, p pluginhost.ServiceInvokeParams) {
				if p.Method != "commit" {
					return
				}
				seen++
				if p.Caller != "third-party" || p.ExecutionID != "execution" || p.CallerScope != scope || p.Service != persistence.ServiceName {
					t.Fatalf("caller metadata changed: %#v", p)
				}
				if ctx.Value(authorityLockKey{}) != nil || ctx.Value(authorityPreflightKey{}) != nil || ctx.Value(authorityActivationKey{}) != nil {
					t.Fatal("coordination context leaked")
				}
				if _, err := f.gate.invoke(ctx, p); err == nil {
					t.Fatal("provider recursion admitted")
				}
				var got persistence.Transaction
				if err := json.Unmarshal(p.Params, &got); err != nil {
					t.Fatal(err)
				}
				// RawMessage whitespace may be normalized, but its document is unchanged.
				gotJSON, _ := json.Marshal(got)
				expectedJSON, _ := json.Marshal(expected)
				if string(gotJSON) != string(expectedJSON) {
					t.Fatal("forwarded intent changed")
				}
				record, err := f.gate.readRecord()
				if err != nil {
					t.Fatal(err)
				}
				if seen == 1 && (record.Pending == nil || record.Pending.Transaction.ID != "wire") {
					t.Fatal("provider entered before durable pending intent")
				}
				if seen == 2 && record.Pending != nil {
					t.Fatal("receipt retry created new pending intent")
				}
			}}
			params := pluginhost.ServiceInvokeParams{Service: persistence.ServiceName, Method: "commit", Caller: "third-party", ExecutionID: "execution", CallerScope: scope, Params: raw}
			var first persistence.Receipt
			for i := 0; i < 2; i++ {
				result, err := f.gate.invoke(context.Background(), params)
				if err != nil {
					t.Fatal(err)
				}
				var receipt persistence.Receipt
				if err := json.Unmarshal(result, &receipt); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					first = receipt
				} else if !reflect.DeepEqual(receipt, first) {
					t.Fatal("retry lost exact receipt")
				}
			}
			if seen != 2 {
				t.Fatalf("provider commit calls=%d", seen)
			}
			params.Params = json.RawMessage(`{"id":"bad","unexpected":true}`)
			if _, err := f.gate.invoke(context.Background(), params); !errors.Is(err, persistence.ErrInvalid) {
				t.Fatalf("unknown request admitted: %v", err)
			}
			if seen != 2 {
				t.Fatal("invalid request reached provider")
			}
		})
	}
}
