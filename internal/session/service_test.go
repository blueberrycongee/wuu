package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/userspace"
	"sync/atomic"
	"testing"
	"time"

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

type journalCleanupContextKey struct{}
type cleanupContextDocuments struct {
	persistence.Documents
	require       atomic.Bool
	cleanupWrites atomic.Int32
}

func (d *cleanupContextDocuments) Read(ctx context.Context, q persistence.Query) (persistence.View, error) {
	if d.require.Load() && ctx.Value(journalCleanupContextKey{}) != true {
		return persistence.View{}, errors.New("synthetic provider is retiring: cleanup context missing")
	}
	return d.Documents.Read(ctx, q)
}
func (d *cleanupContextDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	if d.require.Load() {
		if ctx.Value(journalCleanupContextKey{}) != true {
			return persistence.Receipt{}, errors.New("cleanup context missing")
		}
		d.cleanupWrites.Add(1)
	}
	return d.Documents.Commit(ctx, tx)
}
func TestSessionJournalClosePropagatesCleanupContextToFinalFlush(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		documents := &cleanupContextDocuments{Documents: native.documents}
		service := NewService("cleanup-context", documents)
		if err := service.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		raw, err := service.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "test", Method: "NewInferenceJournalRuntime", Params: json.RawMessage(`{"args":["cleanup"]}`)})
		if err != nil {
			t.Fatal(err)
		}
		var response invocationResult
		_ = json.Unmarshal(raw, &response)
		var handle resourceHandle
		_ = json.Unmarshal(response.Values[0], &handle)
		runtime := service.resources[handle.Handle].value
		journal := runtime.ForOwner("owner")
		now := time.Now().UTC()
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{Operation: op, RequestHash: testInferenceHash("cleanup"), At: now}); err != nil {
			t.Fatal(err)
		}
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{OperationID: op.ID, AttemptID: op.AttemptID(1), Ordinal: 1, RequestHash: testInferenceHash("cleanup"), At: now}); err != nil {
			t.Fatal(err)
		}
		// Enqueue directly before shutdown so no timer is needed to schedule the flush.
		runtime.progressMu.Lock()
		runtime.progress = map[string]documentSubmissionProgress{"submission": {journal: journal.(*documentInferenceJournal), record: providers.InferenceSubmissionJournalRecord{ID: "submission", OperationID: op.ID, AttemptID: op.AttemptID(1), Ordinal: 1, AttemptOrdinal: 1, StartedAt: now, Outcome: providers.InferenceSubmissionInFlight, CostState: providers.InferenceCostUnknownBillable}}}
		runtime.progressMu.Unlock()
		documents.require.Store(true)
		ctx := context.WithValue(context.Background(), journalCleanupContextKey{}, true)
		params, _ := json.Marshal(map[string]any{"args": []any{handle.Handle}})
		if _, err := service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "test", Method: "Journal.Close", Params: params}); err != nil {
			t.Fatal(err)
		}
		if err := runtime.pendingProgressErr(); err != nil {
			t.Fatal(err)
		}
		if documents.cleanupWrites.Load() != 2 {
			t.Fatalf("expected progress and closed-runtime commits, got %d", documents.cleanupWrites.Load())
		}
		if err := service.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSessionDomainDescriptorPassesPublicNegotiation(t *testing.T) {
	service := NewService("session-contract", nil)
	if err := pluginhost.ValidateServiceNegotiation(pluginhost.CapabilityInitializeResult{ProtocolVersion: 3, ProvidedServices: service.ProvidedServices(), RequiredServices: service.RequiredServices()}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionServiceStreamingKeepsDurableWritesAtBoundaries(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		service := NewService("sessions", native.documents)
		if err := service.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer service.Close(context.Background())
		var upserts int
		client := NewClient(context.Background(), func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			if method == "Journal.UpsertSubmission" {
				upserts++
			}
			return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "test", Method: method, Params: raw})
		})
		journalRuntime := NewLazyInferenceJournalRuntime(func(context.Context) (*InferenceJournalRuntime, error) {
			return client.NewInferenceJournalRuntime("streaming")
		})
		defer journalRuntime.Close()
		ctx := providers.WithInferenceJournal(context.Background(), journalRuntime.ForOwner("thread"))
		request, err := providers.EnsureInferenceAttemptContext(ctx, providers.ChatRequest{Model: "fixture"}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
		if err != nil {
			t.Fatal(err)
		}
		submission, err := request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "fixture", Transport: "http", Mode: "stream"})
		if err != nil {
			t.Fatal(err)
		}
		for range 200 {
			submission.ObserveOutput("hello")
		}
		if upserts != 1 {
			t.Fatalf("streaming issued %d durable writes, want only the initial checkpoint", upserts)
		}
		submission.CompleteSuccess(&providers.TokenUsage{InputTokens: 10, OutputTokens: 250})
		if err := request.Attempt.Complete(providers.InferenceOutcomeSucceeded, providers.NormalizedFailure{}); err != nil {
			t.Fatal(err)
		}
		outputBytes, outcome, costState := readInferenceSubmissionProgress(t, native, submission.ID)
		if outputBytes != 1000 || outcome != string(providers.InferenceSubmissionSucceeded) || costState != string(providers.InferenceCostKnown) {
			t.Fatalf("terminal evidence lost: bytes=%d outcome=%s cost=%s", outputBytes, outcome, costState)
		}
		if upserts != 3 {
			t.Fatalf("checkpoint writes=%d, want initial, terminal observation, and completion barrier", upserts)
		}
	})
}

// Personal receipt admission crosses the selected serialized domain just like
// ordinary history. A stale envelope cannot append, and lost replies never replay.
func TestSessionServicePersonalReceiptAdmission(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		client := NewStore(context.Background(), native.documents)
		sess := Session{ID: "personal", UserSpaceID: "local", ProductDomain: "personal", AgentID: "agent", Owner: "plugin"}
		if _, err := client.CreateInitialized(sess, nil); err != nil {
			t.Fatal(err)
		}
		baseline := Control{SessionID: sess.ID}
		runtime, _ := json.Marshal(struct{ ExecutionControlBaseline *Control }{&baseline})
		input := AcceptedClientInput{Receipt: providers.ClientInputReceipt{SessionID: sess.ID, UserSpaceID: "local", ProductDomain: "personal", AgentID: "agent", Owner: "plugin", PluginID: "plugin", HostRequestID: "host", RequestID: "request", ClientID: "client", ReceiptID: "receipt", QueueID: "queue"}, Fingerprint: "fingerprint", MessageJSON: []byte(`{"text":"hello"}`), RuntimeJSON: runtime, SelectionJSON: []byte(`{"files":["original"]}`), LiveOwner: "owner"}
		accepted, fresh, err := client.AcceptClientInput(input, nil, &baseline)
		if err != nil || !fresh || accepted.Receipt.Revision != 1 {
			t.Fatalf("accept: %+v %v %v", accepted, fresh, err)
		}
		repeated, fresh, err := client.AcceptClientInput(input, nil, &baseline)
		if err != nil || fresh || repeated.Receipt.ReceiptID != accepted.Receipt.ReceiptID {
			t.Fatalf("repeated accept: %+v %v %v", repeated, fresh, err)
		}
		input.Fingerprint = "different"
		if _, _, err := client.AcceptClientInput(input, nil, &baseline); !errors.Is(err, ErrClientInputConflict) {
			t.Fatalf("changed request: %v", err)
		}
		held, err := client.ChangeClientInput(accepted, "held", "", nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		record := HistoryRecord{Role: "user", Origin: "user", Content: "hello", ClientID: "client"}
		if _, err := client.AppendAcceptedClientHistoryRecord(sess.ID, record, accepted, "turn", nil, &baseline); !errors.Is(err, ErrClientInputSettled) {
			t.Fatalf("stale append: %v", err)
		}
		queued, err := client.ChangeClientInput(held, "queued", "new-owner", runtime, nil, &baseline)
		if err != nil {
			t.Fatal(err)
		}
		seq, err := client.AppendAcceptedClientHistoryRecord(sess.ID, record, queued, "turn", nil, &baseline)
		if err != nil || seq != 1 {
			t.Fatalf("append: %d %v", seq, err)
		}
		if _, err := client.AppendAcceptedClientHistoryRecord(sess.ID, record, queued, "turn", nil, &baseline); !errors.Is(err, ErrClientInputSettled) {
			t.Fatalf("replayed append: %v", err)
		}
		scope := ClientInputScope{Binding: userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}, SessionID: sess.ID, PluginID: "plugin"}
		stored, found, err := client.ReadClientInput(scope, "host")
		if err != nil || !found || stored.Receipt.MessageSeq != seq || stored.Receipt.HostTurnID != "turn" || len(stored.MessageJSON) != 0 || string(stored.SelectionJSON) != string(queued.SelectionJSON) {
			t.Fatalf("settled receipt: %+v %v %v", stored, found, err)
		}
		if err := client.SettleClientInputExecution(scope, "host", "other-turn", "completed", ""); err != nil {
			t.Fatal(err)
		}
		if err := client.RecoverClientInputs(t.TempDir(), scope); err != nil {
			t.Fatal(err)
		}
		stored, _, err = client.ReadClientInput(scope, "host")
		if err != nil || stored.Receipt.ExecutionState != "unconfirmed" {
			t.Fatalf("unknown execution replayed: %+v %v", stored, err)
		}
		summaries, err := client.ClientInputHistoryReceipts(scope)
		if err != nil || summaries["client"].MessageSeq != 1 {
			t.Fatalf("history receipt: %+v %v", summaries, err)
		}
		foreign := scope
		foreign.Binding.UserSpaceID = "other"
		if _, found, err := client.ReadClientInput(foreign, "host"); err != nil || found {
			t.Fatalf("foreign binding read: %v %v", found, err)
		}
	})
}

// The serialized admission envelope cannot replace canonical receipt identity
// or original attachment bytes while consuming an otherwise current revision.
func TestSessionServicePersonalReceiptEnvelopeImmutable(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(*AcceptedClientInput)
	}{
		{"attachments", func(r *AcceptedClientInput) { r.SelectionJSON = []byte(`{"files":["replaced"]}`) }},
		{"receipt identity", func(r *AcceptedClientInput) { r.Receipt.HostRequestID = "other-host-request" }},
		{"fingerprint", func(r *AcceptedClientInput) { r.Fingerprint = "other-fingerprint" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			forEachSessionStore(t, func(t *testing.T, native *documentStore) {
				client := NewStore(context.Background(), native.documents)
				sess := Session{ID: "personal", UserSpaceID: "local", ProductDomain: "personal", AgentID: "agent", Owner: "plugin"}
				if _, err := client.CreateInitialized(sess, nil); err != nil {
					t.Fatal(err)
				}
				baseline := Control{SessionID: sess.ID}
				runtime, _ := json.Marshal(struct{ ExecutionControlBaseline *Control }{&baseline})
				input := AcceptedClientInput{Receipt: providers.ClientInputReceipt{SessionID: sess.ID, UserSpaceID: "local", ProductDomain: "personal", AgentID: "agent", Owner: "plugin", PluginID: "plugin", HostRequestID: "host", RequestID: "request", ClientID: "client", ReceiptID: "receipt", QueueID: "queue"}, Fingerprint: "fingerprint", MessageJSON: []byte(`{"text":"hello"}`), RuntimeJSON: runtime, SelectionJSON: []byte(`{"files":["original"]}`), LiveOwner: "owner"}
				accepted, _, err := client.AcceptClientInput(input, nil, &baseline)
				if err != nil {
					t.Fatal(err)
				}
				altered := accepted
				change.mutate(&altered)
				record := HistoryRecord{Role: "user", Origin: "user", Content: "hello", ClientID: "client"}
				if _, err := client.AppendAcceptedClientHistoryRecord(sess.ID, record, altered, "turn", nil, &baseline); !errors.Is(err, ErrClientInputSettled) {
					t.Fatalf("altered admission was not rejected: %v", err)
				}
				scope := ClientInputScope{Binding: userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}, SessionID: sess.ID, PluginID: "plugin"}
				stored, found, err := client.ReadClientInput(scope, "host")
				if err != nil || !found || stored.Receipt != accepted.Receipt || stored.Fingerprint != accepted.Fingerprint || string(stored.SelectionJSON) != string(accepted.SelectionJSON) {
					t.Fatalf("canonical admission changed: %+v %v %v", stored, found, err)
				}
				if _, err := client.AppendAcceptedClientHistoryRecord(sess.ID, record, accepted, "turn", nil, &baseline); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestSessionServiceInspectionFencesAuthorityAndSupportsRecovery(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		ctx := context.Background()
		service := NewService("inspection-session", native.documents)
		if err := service.Activate(ctx); err != nil {
			t.Fatal(err)
		}
		defer service.Close(ctx)
		allowMaintenance := false
		client := NewClient(ctx, func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			p := pluginhost.ServiceInvokeParams{Caller: "host", Method: method, Params: raw}
			if allowMaintenance && (method == "ListInspections" || method == "ReconcileInspection") {
				action := "state/inspections/list"
				if method == "ReconcileInspection" {
					action = "state/inspections/reconcile"
				}
				digest := sha256.Sum256(raw)
				p.Admission = &pluginhost.ServiceAdmission{Version: 1, Principal: principal.LocalOwner().Identity(), Action: action, InputJSON: string(raw), InputDigest: hex.EncodeToString(digest[:])}
			}
			return service.InvokeService(ctx, p)
		})
		if _, err := client.ListInspections(ctx); err == nil {
			t.Fatal("ordinary session caller reached maintenance")
		}
		allowMaintenance = true
		if _, err := client.Create("fenced"); err != nil {
			t.Fatal(err)
		}
		entered := false
		if err := client.WithInspection(ctx, func() error {
			entered = true
			if _, err := client.UpdateArchived("fenced", true); !errors.Is(err, persistence.ErrInspectionActive) {
				t.Fatalf("archive escaped or lost definite fence rejection: %v", err)
			}
			if err := client.AppendHistoryRecord("fenced", HistoryRecord{Role: "user", Origin: "user", Content: "new authority"}); !errors.Is(err, persistence.ErrInspectionActive) {
				t.Fatalf("newer turn escaped: %v", err)
			}
			return nil
		}); err != nil || !entered {
			t.Fatalf("inspection: %v %v", entered, err)
		}
		if _, err := client.UpdateArchived("fenced", true); err != nil {
			t.Fatal(err)
		}
		// Simulate a holder that lost the acquire response and cannot run cleanup.
		var guard persistence.InspectionGuard
		if err := client.invoke("AcquireInspection", []any{"lost-holder"}, []any{&guard}); err != nil {
			t.Fatal(err)
		}
		list, err := client.ListInspections(ctx)
		if err != nil || len(list.Inspections) != 1 {
			t.Fatalf("session guard is not discoverable: %+v %v", list, err)
		}
		status := list.Inspections[0]
		receipt, err := client.ReconcileInspection(ctx, persistence.InspectionResolution{InspectionRequest: persistence.InspectionRequest{ID: status.ID, Namespaces: status.Namespaces}, ExpectedOwner: status.Owner, Outcome: "not-dispatched", Reason: "confirmed callback never entered"})
		if err != nil || receipt.Reason == "" {
			t.Fatalf("reconciliation failed: %+v %v", receipt, err)
		}
	})
}
