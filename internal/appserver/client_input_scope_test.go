package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// The selected document provider is the tripwire; tests cannot accidentally pass
// by opening a second local SQLite ledger or by hydrating another scope's payload.
type receiptScopeDocuments struct {
	persistence.Documents
	sessions       *session.Store
	rejectReceipts atomic.Bool
	payloadSession atomic.Pointer[string]
}

func (d *receiptScopeDocuments) Read(ctx context.Context, query persistence.Query) (persistence.View, error) {
	if d.rejectReceipts.Load() && query.Namespace == "wuu.client_input_receipts.v1" {
		return persistence.View{}, errors.New("receipt-read tripwire")
	}
	if sessionID := d.payloadSession.Load(); sessionID != nil && query.Namespace == "wuu.client_input_payloads.v1" {
		prefix := base64.RawURLEncoding.EncodeToString([]byte(*sessionID)) + "/"
		if len(query.Keys) == 0 {
			return persistence.View{}, errors.New("unbounded attachment-read tripwire")
		}
		for _, key := range query.Keys {
			if !strings.HasPrefix(key, prefix) {
				return persistence.View{}, errors.New("foreign attachment-read tripwire")
			}
		}
	}
	return d.Documents.Read(ctx, query)
}
func receiptScopeStore(t *testing.T, rt *runtime.Session) *receiptScopeDocuments {
	t.Helper()
	documents := &receiptScopeDocuments{Documents: rt.Documents}
	documents.sessions = session.NewStore(context.Background(), documents)
	rt.Documents, rt.Sessions = documents, documents.sessions
	return documents
}
func seedScopedReceipt(t *testing.T, documents *receiptScopeDocuments, space, domain, owner, state string) providers.ClientInputReceipt {
	t.Helper()
	sess, err := documents.sessions.CreateInitialized(session.Session{ID: session.NewID(), Owner: "plugin:fixture", Visibility: "plugin", UserSpaceID: space, ProductDomain: domain, AgentID: "assistant"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt := providers.ClientInputReceipt{RequestID: "client-" + sess.ID, HostRequestID: "host-" + sess.ID, ReceiptID: "receipt-" + sess.ID, ClientID: "input-" + sess.ID, QueueID: "queue-" + sess.ID, SessionID: sess.ID, PluginID: "fixture", Owner: "plugin:fixture", UserSpaceID: space, ProductDomain: domain, AgentID: "assistant", ReceiptState: "accepted", ExecutionState: state, Revision: 1, AcceptedAt: "2026-10-23T00:00:00Z"}
	key := base64.RawURLEncoding.EncodeToString([]byte(sess.ID)) + "/" + receipt.ClientID
	// Seed intentionally foreign historical rows through the fixture's provider,
	// not AcceptClientInput: production admission correctly rejects Coding rows.
	err = persistence.Update(context.Background(), documents.Documents, func(tx *persistence.Tx) error {
		if err := tx.Put("wuu.client_input_receipts.v1", key, struct {
			Receipt                providers.ClientInputReceipt
			Fingerprint, LiveOwner string
			Order                  int64
		}{receipt, "fingerprint", owner, 1}); err != nil {
			return err
		}
		return tx.Put("wuu.client_input_payloads.v1", key, struct{ MessageJSON, RuntimeJSON, SelectionJSON []byte }{[]byte(`{"role":"user","content":"scope"}`), []byte(`{}`), []byte(`[]`)})
	})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}
func scopedReceiptState(t *testing.T, documents *receiptScopeDocuments, receipt providers.ClientInputReceipt) string {
	t.Helper()
	key := base64.RawURLEncoding.EncodeToString([]byte(receipt.SessionID)) + "/" + receipt.ClientID
	view, err := documents.Documents.Read(context.Background(), persistence.Query{Namespace: "wuu.client_input_receipts.v1", Keys: []string{key}})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Documents) != 1 {
		t.Fatal("receipt not found")
	}
	var row struct{ Receipt providers.ClientInputReceipt }
	if err := json.Unmarshal(view.Documents[0].Value, &row); err != nil {
		t.Fatal(err)
	}
	return row.Receipt.ExecutionState
}
func assertScopedState(t *testing.T, documents *receiptScopeDocuments, receipt providers.ClientInputReceipt, want string) {
	t.Helper()
	if got := scopedReceiptState(t, documents, receipt); got != want {
		t.Errorf("%s/%s/%s state=%s want=%s", receipt.ProductDomain, receipt.UserSpaceID, receipt.SessionID, got, want)
	}
}

func TestCodingReceiptEntrypointsLeavePersonalRowsUntouched(t *testing.T) {
	for _, phase := range []string{"startup", "inspect", "close"} {
		t.Run(phase, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			db := receiptScopeStore(t, rt)
			personal := seedScopedReceipt(t, db, "alice", "personal", "dead-personal-owner", "queued")
			coding := seedScopedReceipt(t, db, "", "coding", "", "held")
			switch phase {
			case "startup":
				peer := New(rt, &lockedBuffer{})
				defer peer.Close()
				if peer.startupErr != nil {
					t.Fatal(peer.startupErr)
				}
			case "inspect":
				if _, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: coding.SessionID}); err != nil {
					t.Fatal(err)
				}
			case "close":
				srv.Close()
			}
			assertScopedState(t, db, personal, "queued")
		})
	}
}

func TestCodingReceiptEntrypointsDoNotReadReceiptProvider(t *testing.T) {
	for _, phase := range []string{"startup", "inspect", "send", "cancel", "lifecycle", "history_append"} {
		t.Run(phase, func(t *testing.T) {
			client := &fakeClient{}
			rt := newTestRuntime(t, client)
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			db := receiptScopeStore(t, rt)
			target := seedScopedReceipt(t, db, "", "coding", "", "held")
			db.rejectReceipts.Store(true)
			switch phase {
			case "startup":
				peer := New(rt, &lockedBuffer{})
				defer peer.Close()
				if peer.startupErr != nil {
					t.Fatal(peer.startupErr)
				}
			case "inspect":
				if _, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: target.SessionID}); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				if _, err := srv.cancelPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionCancelParams{SessionID: target.SessionID, QueueID: "missing"}); err != nil {
					t.Fatal(err)
				}
			case "lifecycle":
				if _, err := srv.persistPluginTurnLifecycle(testRuntimeState(t, srv.rt), "fixture", pluginhost.AgentTurnLifecycleInput{ThreadID: target.SessionID, RequestID: "ordinary-plugin", TurnID: "turn", State: "completed"}); err != nil {
					t.Fatal(err)
				}
			case "history_append":
				if err := rt.Sessions.AppendHistoryRecord(target.SessionID, session.HistoryRecord{Role: "user", Origin: "user", ClientID: "coding-client", Content: "coding history"}); err != nil {
					t.Fatal(err)
				}
			case "send":
				th, err := srv.ensureThreadLoaded(testRuntimeState(t, srv.rt), target.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				th.mu.Lock()
				th.running = true
				th.mu.Unlock()
				defer func() { th.mu.Lock(); th.running = false; th.mu.Unlock() }()
				proof := runtime.ClientUserInput{RequestID: "coding-client", Prompt: "coding input"}
				result, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "fixture", proof)), "fixture", pluginhost.SessionSendParams{SessionID: target.SessionID, RequestID: "coding-host", UserInputRequestID: proof.RequestID, Input: pluginhost.SessionInput{Prompt: proof.Prompt}})
				if err != nil {
					t.Fatal(err)
				}
				if result.Receipt != nil || result.State != "queued" {
					t.Fatalf("Coding acquired receipt behavior: %+v", result)
				}
			}
			assertFakeClientRequestCount(t, client, 0)
		})
	}
}

func TestPersonalReceiptRecoveryIsBindingAndSessionScoped(t *testing.T) {
	for _, phase := range []string{"startup", "inspect", "close"} {
		t.Run(phase, func(t *testing.T) {
			client := &fakeClient{}
			rt := newTestRuntime(t, client)
			rt.UserSpaceID, rt.ProductDomain = "alice", "personal"
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			db := receiptScopeStore(t, rt)
			owner := "dead-owner"
			if phase == "close" {
				var err error
				owner, err = srv.ensureClientInputOwner(testRuntimeState(t, srv.rt))
				if err != nil {
					t.Fatal(err)
				}
			}
			a := seedScopedReceipt(t, db, "alice", "personal", owner, "queued")
			b := seedScopedReceipt(t, db, "alice", "personal", "dead-other-owner", "queued")
			foreign := seedScopedReceipt(t, db, "bob", "personal", "dead-bob", "queued")
			coding := seedScopedReceipt(t, db, "alice", "coding", "dead-coding", "queued")
			switch phase {
			case "startup":
				peer := New(rt, &lockedBuffer{})
				defer peer.Close()
				if peer.startupErr != nil {
					t.Fatal(peer.startupErr)
				}
			case "inspect":
				if _, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: a.SessionID, RequestID: a.HostRequestID}); err != nil {
					t.Fatal(err)
				}
			case "close":
				srv.Close()
			}
			assertScopedState(t, db, a, "held")
			wantB := "queued"
			if phase == "startup" {
				wantB = "held"
			}
			assertScopedState(t, db, b, wantB)
			assertScopedState(t, db, foreign, "queued")
			assertScopedState(t, db, coding, "queued")
			assertFakeClientRequestCount(t, client, 0)
		})
	}
}

func TestPersonalReceiptScanDoesNotHydrateOtherSessionAttachments(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "alice", "personal"
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	db := receiptScopeStore(t, rt)
	a := seedScopedReceipt(t, db, "alice", "personal", "", "held")
	lease, acquired, err := rt.Sessions.TryAcquireThreadExecutionLease(rt.SessionDir, "live-other-session")
	if err != nil || !acquired {
		t.Fatal("lease", err)
	}
	defer lease.Release()
	_ = seedScopedReceipt(t, db, "alice", "personal", "live-other-session", "queued")
	// A projection tripwire: unrelated payload columns must never be selected.
	db.payloadSession.Store(&a.SessionID)
	peer := New(rt, &lockedBuffer{})
	defer peer.Close()
	if peer.startupErr != nil {
		t.Fatal(peer.startupErr)
	}
	result, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: a.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PendingInputs) != 1 {
		t.Fatalf("pending=%+v", result.PendingInputs)
	}
}

func TestPersonalReceiptInspectRejectsForeignBindingBeforeHydration(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "alice", "personal"
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	db := receiptScopeStore(t, rt)
	foreign := seedScopedReceipt(t, db, "bob", "personal", "", "held")
	allowed := "not-the-foreign-session"
	db.payloadSession.Store(&allowed)
	_, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: foreign.SessionID, RequestID: foreign.HostRequestID})
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("foreign binding must be rejected before payload query: %v", err)
	}
}

func TestBoundCodingHostDoesNotRecoverPersonalReceipts(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "alice", "coding"
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	db := receiptScopeStore(t, rt)
	personal := seedScopedReceipt(t, db, "alice", "personal", "dead-owner", "queued")
	peer := New(rt, &lockedBuffer{})
	defer peer.Close()
	if peer.startupErr != nil {
		t.Fatal(peer.startupErr)
	}
	assertScopedState(t, db, personal, "queued")
	if srv.clientInputReceiptsEnabled() {
		t.Fatal("Coding enabled receipt entrypoints")
	}
}
