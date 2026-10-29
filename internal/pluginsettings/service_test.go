package pluginsettings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// CAS must preserve settings written by concurrent shells, fingerprints and
// legacy files, and must not retry uncertain provider outcomes.
func TestSettingsServicePreservesLegacyAndConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	home, project, subject := t.TempDir(), t.TempDir(), "plugin:project:example"
	if _, err := Update(home, project, subject, ScopeWorkspace, "old", func(values map[string]json.RawMessage) error {
		values["legacy"] = json.RawMessage(`{"nested":true}`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService("test.settings", home, project)
	client := settingsServiceClient(service)
	if _, err := client.Update(ctx, subject, ScopeWorkspace, "new", func(map[string]json.RawMessage) error { return nil }); err == nil {
		t.Fatal("prepared provider write")
	}
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, key := range []string{"a", "b", "c", "d"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Update(ctx, subject, ScopeWorkspace, "new", func(values map[string]json.RawMessage) error { values[key] = json.RawMessage(`true`); return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := Read(home, project, subject, ScopeWorkspace)
	if err != nil || len(got.Values) != 5 || got.Fingerprint != "new" {
		t.Fatalf("persisted settings: %+v %v", got, err)
	}
	if _, err := client.UpdateState(ctx, subject, ScopeUser, func(values map[string]string) error { values["task"] = "complete"; return nil }); err != nil {
		t.Fatal(err)
	}
	state, err := ReadState(home, project, subject, ScopeUser)
	if err != nil || state.Values["task"] != "complete" {
		t.Fatalf("persisted state: %+v %v", state, err)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(ctx, subject, ScopeUser); err == nil {
		t.Fatal("closed provider read")
	}
	reopened := NewService("next.settings", home, project)
	if err := reopened.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = settingsServiceClient(reopened).Read(ctx, subject, ScopeWorkspace)
	if err != nil || len(got.Values) != 5 {
		t.Fatalf("reopen: %+v %v", got, err)
	}
}
func TestSettingsClientDoesNotReplayUncertainUpdate(t *testing.T) {
	attempts := 0
	client := NewClient(func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "read" {
			return json.Marshal(SettingsView{Document: emptyDocument("plugin:test"), Revision: "old"})
		}
		attempts++
		return nil, errors.New("outcome unknown")
	})
	_, err := client.Update(context.Background(), "plugin:test", ScopeUser, "fingerprint", func(values map[string]json.RawMessage) error { values["a"] = json.RawMessage(`1`); return nil })
	if err == nil || attempts != 1 {
		t.Fatalf("uncertain update attempts=%d error=%v", attempts, err)
	}
}
func settingsServiceClient(service *Service) *Client {
	return NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	})
}

// Selected-provider transfers must preserve ownership journals through ordinary
// CAS writes, reject stale empty-prefix views, and leave configuration shared
// while business state remains in the explicitly selected data root.
func TestSettingsServiceTransferPreservesFencesAndReceipts(t *testing.T) {
	ctx := context.Background()
	home, stateRoot, project := t.TempDir(), t.TempDir(), t.TempDir()
	transfer := testStateTransfer()
	seedState(t, stateRoot, transfer.SourceSubjectID, map[string]string{"records.changed": "old", "records.deleted": "old", "other.source": "keep"})
	service := NewServiceWithStateRoot("test.settings", home, stateRoot, project)
	client := settingsServiceClient(service)
	if err := client.MoveStatePrefix(ctx, transfer); err == nil {
		t.Fatal("prepared provider transferred state")
	}
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.MoveStatePrefix(ctx, transfer); err != nil {
		t.Fatal(err)
	}
	source, err := client.ReadState(ctx, transfer.SourceSubjectID, ScopeUser)
	if err != nil || len(source.Values) != 1 || source.Values["other.source"] != "keep" {
		t.Fatalf("source visibility: %+v %v", source, err)
	}
	if _, err := client.UpdateState(ctx, transfer.TargetSubjectID, ScopeUser, func(values map[string]string) error {
		delete(values, "records.deleted")
		values["records.changed"] = "new"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateState(ctx, transfer.SourceSubjectID, ScopeUser, func(values map[string]string) error {
		values["other.source"] = "updated"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateState(ctx, transfer.SourceSubjectID, ScopeUser, func(values map[string]string) error {
		values["records.new"] = "not owned"
		return nil
	}); err == nil {
		t.Fatal("fenced prefix became writable through selected service")
	}
	if err := client.MoveStatePrefix(ctx, transfer); err != nil {
		t.Fatal(err)
	}
	target, err := client.ReadState(ctx, transfer.TargetSubjectID, ScopeUser)
	if err != nil || target.Values["records.changed"] != "new" {
		t.Fatalf("target update lost: %+v %v", target, err)
	}
	if _, ok := target.Values["records.deleted"]; ok {
		t.Fatal("receipt lost: deleted state replayed")
	}
	if backup := rawStateValues(t, stateRoot, transfer.SourceSubjectID); backup["records.changed"] != "old" || backup["other.source"] != "updated" {
		t.Fatalf("source backup or unrelated data lost: %+v", backup)
	}
	if _, err := client.Update(ctx, transfer.TargetSubjectID, ScopeUser, "settings", func(values map[string]json.RawMessage) error { values["shared"] = json.RawMessage(`true`); return nil }); err != nil {
		t.Fatal(err)
	}
	shared, err := Read(home, project, transfer.TargetSubjectID, ScopeUser)
	if err != nil || string(shared.Values["shared"]) != "true" {
		t.Fatalf("configuration not shared: %+v %v", shared, err)
	}
	implicit, err := ReadState(home, project, transfer.TargetSubjectID, ScopeUser)
	if err != nil || len(implicit.Values) != 0 {
		t.Fatalf("business state copied into config home: %+v %v", implicit, err)
	}
}

func TestSettingsServiceTransferInvalidatesUnchangedVisibleState(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	service := NewService("test.settings", home, "")
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	client := settingsServiceClient(service)
	first := testStateTransfer()
	if err := client.MoveStatePrefix(ctx, first); err != nil {
		t.Fatal(err)
	}
	var before StateView
	if err := client.invoke(ctx, "read-state", ReadRequest{Subject: first.SourceSubjectID, Scope: ScopeUser}, &before); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.KeyPrefix = "second-transfer", "empty."
	if err := client.MoveStatePrefix(ctx, second); err != nil {
		t.Fatal(err)
	}
	var after StateView
	err := client.invoke(ctx, "update-state", UpdateStateRequest{ReadRequest: ReadRequest{Subject: first.SourceSubjectID, Scope: ScopeUser}, ExpectedRevision: before.Revision, Values: map[string]string{"unrelated": "new"}}, &after)
	var hostErr *pluginhost.HostServiceError
	if !errors.As(err, &hostErr) || hostErr.Code != "settings_conflict" {
		t.Fatalf("ownership-only transfer did not invalidate CAS: %v", err)
	}
}

func TestSettingsClientTransferDoesNotFallbackOrRetryUnknownOutcome(t *testing.T) {
	for _, code := range []string{"method_not_found", "service_unavailable", "settings_unavailable"} {
		t.Run(code, func(t *testing.T) {
			attempts := 0
			client := NewClient(func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				attempts++
				var got StateTransfer
				if method != "transfer-state" || json.Unmarshal(raw, &got) != nil || got != testStateTransfer() {
					t.Fatalf("unexpected transfer request: %s %s", method, raw)
				}
				return nil, &pluginhost.HostServiceError{Code: code, Message: "provider outcome"}
			})
			err := client.MoveStatePrefix(context.Background(), testStateTransfer())
			var hostErr *pluginhost.HostServiceError
			if !errors.As(err, &hostErr) || attempts != 1 {
				t.Fatalf("transfer attempts=%d error=%v", attempts, err)
			}
			expected := code
			if code == "method_not_found" {
				expected = "storage_transfer_unsupported"
			}
			if hostErr.Code != expected {
				t.Fatalf("error=%v, want %s", err, expected)
			}
		})
	}
}

func TestStateInspectionFencesCASAndTransferAcrossServiceRestart(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	request := InspectionRequest{ID: "guard", ReadRequest: ReadRequest{Subject: "source", Scope: ScopeUser}}
	service := NewService("settings", home, "")
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	call := func(method string, input any) (json.RawMessage, error) {
		raw, _ := json.Marshal(input)
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "host", Service: ServiceName, Method: method, Params: raw})
	}
	if _, err := call("acquire-inspection", request); err != nil {
		t.Fatal(err)
	}
	service = NewService("settings", home, "")
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "host", Service: ServiceName, Method: method, Params: raw})
	})
	if _, err := client.UpdateState(ctx, "source", ScopeUser, func(values map[string]string) error { values["p.key"] = "new"; return nil }); err == nil {
		t.Fatal("CAS bypassed inspection")
	}
	if err := client.MoveStatePrefix(ctx, StateTransfer{ID: "move", SourceSubjectID: "source", TargetSubjectID: "target", Scope: ScopeUser, KeyPrefix: "p."}); err == nil {
		t.Fatal("transfer bypassed inspection")
	}
	if _, err := call("release-inspection", request); err != nil {
		t.Fatal(err)
	}
	if _, err := call("release-inspection", request); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateState(ctx, "source", ScopeUser, func(values map[string]string) error { values["p.key"] = "new"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := call("acquire-inspection", request); err == nil {
		t.Fatal("released inspection revived")
	}
}

// A completed ownership transfer is replayed on every graph activation. A live
// inspection may reject new transfers, but must not disable the replacement
// runtime merely for confirming an already committed receipt.
func TestStateTransferReplayPreservesActiveInspection(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "committed"}[completed], func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			transfer := testStateTransfer()
			seedState(t, home, transfer.SourceSubjectID, map[string]string{"records.value": "source"})
			interrupted := errors.New("interrupted before receipt")
			err := moveStatePrefix(home, "", transfer, func(stage stateTransferStage) error {
				if !completed && stage == afterStateTransferFence {
					return interrupted
				}
				return nil
			})
			if completed && err != nil || !completed && !errors.Is(err, interrupted) {
				t.Fatal(err)
			}
			path, _, err := stateDocumentPath(home, "", transfer.TargetSubjectID, transfer.Scope)
			if err != nil {
				t.Fatal(err)
			}
			var original []byte
			var originalFile os.FileInfo
			if completed {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var record map[string]json.RawMessage
				if err := json.Unmarshal(raw, &record); err != nil {
					t.Fatal(err)
				}
				record["provider_metadata"] = json.RawMessage(`{"opaque":true}`)
				original, err = json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := writeStateFile(path, original); err != nil {
					t.Fatal(err)
				}
				originalFile, err = os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			old := NewService("old.settings", home, "")
			if err := old.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			oldClient := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				return old.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "host", Service: ServiceName, Method: method, Params: raw})
			})
			guard := InspectionRequest{ID: "admitted-memory", ReadRequest: ReadRequest{Subject: transfer.TargetSubjectID, Scope: ScopeUser}}
			before, err := oldClient.AcquireInspection(ctx, guard)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := oldClient.ReleaseInspection(ctx, guard); err != nil {
					t.Error(err)
				}
				if err := old.Close(ctx); err != nil {
					t.Error(err)
				}
			}()
			next := NewService("next.settings", home, "")
			if err := next.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			defer next.Close(ctx)
			client := settingsServiceClient(next)
			err = client.MoveStatePrefix(ctx, transfer)
			if completed && err != nil {
				t.Fatalf("committed replay disabled replacement under active inspection: %v", err)
			}
			if !completed && err == nil {
				t.Fatal("pending transfer changed inspected state")
			}
			if completed {
				raw, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(raw, original) {
					t.Fatalf("completed replay rewrote inspected bytes: %v", err)
				}
				afterFile, err := os.Stat(path)
				if err != nil || !os.SameFile(originalFile, afterFile) {
					t.Fatalf("completed replay replaced inspected file: %v", err)
				}
			}
			var after StateView
			if err := client.invoke(ctx, "read-state", guard.ReadRequest, &after); err != nil || after.Revision != before.Revision {
				t.Fatalf("replay changed inspected revision: before=%s after=%s error=%v", before.Revision, after.Revision, err)
			}
			if _, err := client.UpdateState(ctx, transfer.TargetSubjectID, ScopeUser, func(values map[string]string) error {
				values["other.value"] = "changed"
				return nil
			}); err == nil {
				t.Fatal("replay released the active write fence")
			}
			other := transfer
			other.ID, other.KeyPrefix = "another-transfer", "other."
			if err := client.MoveStatePrefix(ctx, other); err == nil {
				t.Fatal("new ownership transfer bypassed active inspection")
			}
		})
	}
}

func TestStateInspectionMaintenanceRequiresHostAdmission(t *testing.T) {
	ctx := context.Background()
	service := NewService("settings", t.TempDir(), "")
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	raw := json.RawMessage(`{"scope":"user"}`)
	p := pluginhost.ServiceInvokeParams{Caller: "ordinary", Service: ServiceName, Method: "list-inspections", Params: raw}
	if _, err := service.InvokeService(ctx, p); err == nil {
		t.Fatal("ordinary consumer accessed maintenance")
	}
	digest := sha256.Sum256(raw)
	p.Admission = &pluginhost.ServiceAdmission{Version: 1, Principal: principal.LocalOwner().Identity(), Action: "state/inspections/list", InputJSON: string(raw), InputDigest: hex.EncodeToString(digest[:])}
	if _, err := service.InvokeService(ctx, p); err != nil {
		t.Fatal(err)
	}
}

func TestStateInspectionRejectedOwnerDoesNotRetainLifecycle(t *testing.T) {
	ctx := context.Background()
	service := NewService("settings", t.TempDir(), "")
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(InspectionRequest{ID: "same-id", ReadRequest: ReadRequest{Subject: "source", Scope: ScopeUser}})
	call := func(caller, method string) error {
		_, err := service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: caller, Service: ServiceName, Method: method, Params: raw})
		return err
	}
	if err := call("owner", "acquire-inspection"); err != nil {
		t.Fatal(err)
	}
	if err := call("other", "acquire-inspection"); err == nil {
		t.Fatal("foreign acquisition accepted")
	}
	if err := call("owner", "release-inspection"); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := service.Close(cancelled); err != nil {
		t.Fatalf("definitely rejected acquire retained provider: %v", err)
	}
}
