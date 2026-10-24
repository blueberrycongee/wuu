package session

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
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
		resource := service.resources[handle.Handle].value
		if err := service.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := resource.beginUse(); err == nil {
			t.Fatal("provider shutdown left journal active")
		}
	})
}

type blockedDomainDocuments struct {
	persistence.Documents
	entered chan struct{}
	resume  chan struct{}
}

func (d *blockedDomainDocuments) Read(ctx context.Context, q persistence.Query) (persistence.View, error) {
	close(d.entered)
	<-d.resume
	return d.Documents.Read(ctx, q)
}

func TestSessionServiceCloseDeadlineDoesNotWaitUnderStateLock(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		documents := &blockedDomainDocuments{Documents: native.documents, entered: make(chan struct{}), resume: make(chan struct{})}
		service := NewService("blocking", documents)
		if err := service.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			_, err := service.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "test", Method: "Find", Params: json.RawMessage(`{"args":["missing"]}`)})
			result <- err
		}()
		<-documents.entered
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := service.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("close did not honor caller deadline: %v", err)
		}
		if service.Status().State != pluginhost.StateStopped {
			t.Fatal("close did not stop admission")
		}
		close(documents.resume)
		<-result
		if err := service.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

type lostDomainCommit struct {
	persistence.Documents
	lose bool
}

func (d *lostDomainCommit) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	receipt, err := d.Documents.Commit(ctx, tx)
	if err == nil && d.lose {
		return persistence.Receipt{}, errors.New("synthetic lost reply")
	}
	return receipt, err
}
func TestSessionServiceReportsUnknownDeletionOutcome(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		documents := &lostDomainCommit{Documents: native.documents}
		client := NewStore(context.Background(), documents)
		if _, err := client.Create("delete"); err != nil {
			t.Fatal(err)
		}
		documents.lose = true
		if _, err := client.Delete("delete"); !errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("uncertain deletion lost its recovery category: %v", err)
		}
		documents.lose = false
		if _, found, err := native.Find("delete"); err != nil || found {
			t.Fatalf("expected committed deletion: %v %v", found, err)
		}
	})
}

type reentrantDomainDocuments struct {
	persistence.Documents
	status func() pluginhost.Status
}

func (d *reentrantDomainDocuments) Read(ctx context.Context, q persistence.Query) (persistence.View, error) {
	if d.status().State != pluginhost.StateActive {
		return persistence.View{}, errors.New("consumer is not active")
	}
	return d.Documents.Read(ctx, q)
}
func TestSessionServiceDependencyCanInspectConsumerStatus(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		documents := &reentrantDomainDocuments{Documents: native.documents}
		service := NewService("reentrant", documents)
		documents.status = service.Status
		if err := service.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer service.Close(context.Background())
		client := NewClient(context.Background(), func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
			return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "test", Method: method, Params: params})
		})
		if _, err := client.Create("reentrant"); err != nil {
			t.Fatal(err)
		}
	})
}
func TestSessionClientTransportLossPreservesUncertainty(t *testing.T) {
	for _, call := range []Call{
		func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, errors.New("transport disconnected")
		},
		func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "connection lost after admission"}
		},
		func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"values":[]}`), nil
		},
	} {
		client := NewClient(context.Background(), call)
		if _, err := client.Delete("delete"); !errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("ambiguous delete was treated as safe failure: %v", err)
		}
	}
}

func TestSessionServiceHeldMessageFenceCrossesWireButNotHistory(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		client := NewStore(context.Background(), native.documents)
		if _, err := client.Create("held"); err != nil {
			t.Fatal(err)
		}
		held := HeldUserWork{ID: "steer", Origin: HeldUserWorkOriginSteer, MessageJSON: []byte("{ \"Content\": \"keep\" }"), RuntimeJSON: []byte(`{}`)}
		if accepted, err := client.AcceptHeldUserWork("held", held); err != nil || !accepted {
			t.Fatalf("accept: %v %v", accepted, err)
		}
		record := HistoryRecord{Role: "user", Content: "keep", ClientID: "steer", ExpectedHeldMessage: json.RawMessage(`{"Content":"stale"}`)}
		if err := client.AppendHistoryRecord("held", record); err == nil {
			t.Fatal("stale fence accepted")
		}
		rows, err := client.LoadHistoryRecords("held", true)
		if err != nil || len(rows) != 0 {
			t.Fatalf("failed admission wrote history: %+v %v", rows, err)
		}
		record.ExpectedHeldMessage = held.MessageJSON
		if err := client.AppendHistoryRecord("held", record); err != nil {
			t.Fatal(err)
		}
		rows, err = client.LoadHistoryRecords("held", true)
		if err != nil || len(rows) != 1 || len(rows[0].ExpectedHeldMessage) != 0 {
			t.Fatalf("fence leaked into durable history: %+v %v", rows, err)
		}
		pending, err := client.LoadHeldUserWork("held")
		if err != nil || len(pending) != 0 {
			t.Fatalf("admitted input remained pending: %+v %v", pending, err)
		}
	})
}
