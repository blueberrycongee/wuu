package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
)

// Contract failures: a rejected write reports success, an absent key retains a
// discarded attempt's value, or a successful retry reports an earlier mismatch.
func TestPluginStorageCompareExchangeSettingsConflict(t *testing.T) {
	original, replacement, competitor, empty := "original", "replacement", "competitor", ""
	for _, tc := range []struct {
		name                                       string
		initial, expected, value, concurrent, want *string
		swapped                                    bool
	}{
		{"replacement loses to competitor", &original, &original, &replacement, &competitor, &competitor, false},
		{"replacement loses to deletion", &original, &original, &replacement, nil, nil, false},
		{"creation loses to empty value", nil, nil, &replacement, &empty, &empty, false},
		{"deletion loses to competitor", &original, &original, nil, &competitor, &competitor, false},
		{"replacement succeeds after unrelated change", &original, &original, &replacement, &original, &replacement, true},
		{"deletion succeeds after earlier mismatch", &competitor, &original, nil, &original, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			home, root := t.TempDir(), t.TempDir()
			item := serviceTestPlugin("cas", "plugin:user:cas", "one")
			provider := pluginsettings.NewService(pluginsettings.Module, home, root)
			if err := provider.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = provider.Close(ctx) })
			call := func(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
				return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: pluginsettings.ServiceName, Method: method, Params: input})
			}
			writer := pluginsettings.NewClient(call)
			if _, err := writer.UpdateState(ctx, item.SubjectID, pluginsettings.ScopeUser, func(values map[string]string) error {
				if tc.initial != nil {
					values["key"] = *tc.initial
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			attempts, conflicts := 0, 0
			s := newPluginHostServices(item, root, home, nil)
			s.settingsClient = pluginsettings.NewClient(func(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
				if method == "update-state" {
					attempts++
					if attempts > 2 {
						t.Fatal("unexpected extra retry")
					}
					if attempts == 1 {
						// Commit another writer after the callback has run but before its
						// revision is checked. The real provider must reject that attempt.
						if _, err := writer.UpdateState(ctx, item.SubjectID, pluginsettings.ScopeUser, func(values map[string]string) error {
							if tc.concurrent == nil {
								delete(values, "key")
							} else {
								values["key"] = *tc.concurrent
							}
							values["unrelated"] = "preserved"
							return nil
						}); err != nil {
							t.Fatal(err)
						}
					}
				}
				raw, err := call(ctx, method, input)
				var hostErr *pluginhost.HostServiceError
				if errors.As(err, &hostErr) && hostErr.Code == "settings_conflict" {
					conflicts++
				}
				return raw, err
			})
			s.activate()
			t.Cleanup(s.close)
			input, err := json.Marshal(pluginhost.StorageCompareExchangeParams{Scope: "user", Key: "key", Expected: tc.expected, Value: tc.value})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := s.invoke(ctx, pluginhost.HostServiceStorageCompareExchange, input)
			if err != nil {
				t.Fatal(err)
			}
			if attempts != 2 || conflicts != 1 {
				t.Fatalf("attempts=%d conflicts=%d; want 2 and 1", attempts, conflicts)
			}
			var result pluginhost.StorageCompareExchangeResult
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if result.Swapped != tc.swapped || (result.Value == nil) != (tc.want == nil) || (result.Value != nil && tc.want != nil && *result.Value != *tc.want) {
				t.Errorf("compare_exchange = %s; want swapped=%v value=%v", raw, tc.swapped, tc.want)
			}
			state, err := writer.ReadState(ctx, item.SubjectID, pluginsettings.ScopeUser)
			if err != nil {
				t.Fatal(err)
			}
			current, exists := state.Values["key"]
			if exists != (tc.want != nil) || (tc.want != nil && current != *tc.want) || state.Values["unrelated"] != "preserved" {
				t.Fatalf("persisted values = %#v", state.Values)
			}
		})
	}
}
