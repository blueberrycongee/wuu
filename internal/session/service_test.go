package session

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestSessionClientUsesProviderResultsAndFailsClosed(t *testing.T) {
	var calls int
	client := NewClient(context.Background(), func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != "Create" {
			return nil, &pluginhost.HostServiceError{Code: "session_not_found", Message: "external missing"}
		}
		return json.RawMessage(`{"values":[{"id":"external-created","entries":41}]}`), nil
	})
	created, err := client.Create()
	if err != nil || created.ID != "external-created" || created.Entries != 41 || calls != 1 {
		t.Fatalf("provider result: %+v %v calls=%d", created, err, calls)
	}
	if _, _, err := client.Find("missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("sentinel lost: %v", err)
	}
	broken := NewClient(context.Background(), func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"values":[]}`), nil
	})
	if _, err := broken.Create(); err == nil {
		t.Fatal("accepted incomplete result")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.WithContext(cancelled).Create(); !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatalf("cancelled call reached provider: %v %d", err, calls)
	}
}

func TestSessionServiceWirePreservesReceiptBytesAndDomainErrors(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		service := NewService("sessions", native.documents)
		call := func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "test", Method: method, Params: raw})
		}
		client := NewClient(context.Background(), call)
		if _, err := client.Create("s"); err == nil {
			t.Fatal("prepared service accepted mutation")
		}
		if err := service.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := service.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		if _, err := client.Create("s"); err != nil {
			t.Fatal(err)
		}
		raw := json.RawMessage("{ \"b\": 2, \"a\": 1 }")
		receipt := ModelInputReceiptRecord{OperationID: "op", ContractVersion: 1, DriverID: "d", DriverVersion: "1", Payload: raw}
		if err := client.SaveModelInputReceipt("s", receipt); err != nil {
			t.Fatal(err)
		}
		got, err := client.LoadModelInputReceipts("s")
		if err != nil || len(got) != 1 || string(got[0].Payload) != string(raw) {
			t.Fatalf("receipt bytes changed: %+v %v", got, err)
		}
		if _, err := client.DeleteArchived("s"); !errors.Is(err, ErrSessionNotArchived) {
			t.Fatalf("domain sentinel lost: %v", err)
		}
		if err := service.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, _, err := client.Find("s"); err == nil {
			t.Fatal("closed provider accepted call")
		}
	})
}

func TestSessionClientHostLeaseCannotBeBypassedByReplacement(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	call := func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != "AdmitThreadExecution" {
			t.Fatalf("unexpected %s", method)
		}
		return json.RawMessage(`{"values":[0]}`), nil
	}
	first := NewClient(context.Background(), call)
	replacement := NewClient(context.Background(), call)
	lease, acquired, err := first.TryAcquireThreadExecutionLease(dir, "one")
	if err != nil || !acquired {
		t.Fatalf("first admission: %v %v", acquired, err)
	}
	defer lease.Release()
	if _, acquired, err := replacement.TryAcquireThreadExecutionLease(dir, "one"); err != nil || acquired || calls != 1 {
		t.Fatalf("replacement bypassed host lease: %v %v calls=%d", acquired, err, calls)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	next, acquired, err := replacement.TryAcquireThreadExecutionLease(dir, "one")
	if err != nil || !acquired || calls != 2 {
		t.Fatalf("released admission: %v %v calls=%d", acquired, err, calls)
	}
	if err := next.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionServiceJournalOwnershipAndShutdown(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		service := NewService("journals", native.documents)
		if err := service.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer service.Close(context.Background())
		raw, err := service.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "owner", Method: "NewInferenceJournalRuntime", Params: json.RawMessage(`{"args":["synthetic"]}`)})
		if err != nil {
			t.Fatal(err)
		}
		var response invocationResult
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		var handle resourceHandle
		if err := json.Unmarshal(response.Values[0], &handle); err != nil {
			t.Fatal(err)
		}
		params, _ := json.Marshal(map[string]any{"args": []any{handle.Handle}})
		if _, err := service.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "foreign", Method: "Journal.Close", Params: params}); err == nil {
			t.Fatal("foreign caller released journal")
		}
		resource := service.resources[handle.Handle].value.(*InferenceJournalRuntime)
		if err := service.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := resource.beginUse(); err == nil {
			t.Fatal("provider shutdown left journal active")
		}
	})
}
