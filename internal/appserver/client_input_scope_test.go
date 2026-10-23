package appserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func receiptScopeDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func scopeExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func seedScopedReceipt(t *testing.T, db *sql.DB, dir, space, domain, owner, state string) providers.ClientInputReceipt {
	t.Helper()
	sess, err := session.Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	scopeExec(t, db, `UPDATE sessions SET owner='plugin:fixture',visibility='plugin',user_space_id=?,product_domain=?,agent_id='assistant' WHERE id=?`, space, domain, sess.ID)
	r := providers.ClientInputReceipt{RequestID: "client-" + sess.ID, HostRequestID: "host-" + sess.ID, ReceiptID: "receipt-" + sess.ID, ClientID: "input-" + sess.ID, QueueID: "queue-" + sess.ID, SessionID: sess.ID, PluginID: "fixture", Owner: "plugin:fixture", UserSpaceID: space, ProductDomain: domain, AgentID: "assistant", ReceiptState: "accepted", ExecutionState: state, Revision: 1, AcceptedAt: "2026-10-23T00:00:00Z"}
	raw, _ := json.Marshal(r)
	scopeExec(t, db, `INSERT INTO client_input_receipts(session_id,client_id,plugin_id,host_request_id,request_id,queue_id,summary_json,fingerprint,message_json,runtime_json,selection_json,live_owner) VALUES(?,?,?,?,?,?,?,'fingerprint','{"role":"user","content":"scope"}','{}','[]',?)`, r.SessionID, r.ClientID, r.PluginID, r.HostRequestID, r.RequestID, r.QueueID, string(raw), owner)
	return r
}
func scopedReceiptState(t *testing.T, db *sql.DB, r providers.ClientInputReceipt) string {
	t.Helper()
	var raw string
	if err := db.QueryRow(`SELECT summary_json FROM client_input_receipts WHERE session_id=?`, r.SessionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var got providers.ClientInputReceipt
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	return got.ExecutionState
}
func assertScopedState(t *testing.T, db *sql.DB, r providers.ClientInputReceipt, want string) {
	t.Helper()
	if got := scopedReceiptState(t, db, r); got != want {
		t.Errorf("%s/%s/%s state=%s want=%s", r.ProductDomain, r.UserSpaceID, r.SessionID, got, want)
	}
}

func TestCodingReceiptEntrypointsLeavePersonalRowsUntouched(t *testing.T) {
	for _, phase := range []string{"startup", "inspect", "close"} {
		t.Run(phase, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			db := receiptScopeDB(t, rt.SessionDir)
			personal := seedScopedReceipt(t, db, rt.SessionDir, "alice", "personal", "dead-personal-owner", "queued")
			coding := seedScopedReceipt(t, db, rt.SessionDir, "", "coding", "", "held")
			switch phase {
			case "startup":
				peer := New(rt, &lockedBuffer{})
				defer peer.Close()
				if peer.startupErr != nil {
					t.Fatal(peer.startupErr)
				}
			case "inspect":
				if _, err := srv.inspectPluginSession(context.Background(), "fixture", pluginhost.SessionInspectParams{SessionID: coding.SessionID}); err != nil {
					t.Fatal(err)
				}
			case "close":
				srv.Close()
			}
			assertScopedState(t, db, personal, "queued")
		})
	}
}

func TestCodingReceiptEntrypointsDoNotReadReceiptTable(t *testing.T) {
	for _, phase := range []string{"startup", "inspect", "send", "cancel", "lifecycle", "history_append"} {
		t.Run(phase, func(t *testing.T) {
			client := &fakeClient{}
			rt := newTestRuntime(t, client)
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			db := receiptScopeDB(t, rt.SessionDir)
			target := seedScopedReceipt(t, db, rt.SessionDir, "", "coding", "", "held")
			scopeExec(t, db, `ALTER TABLE client_input_receipts RENAME TO scope_receipts`)
			scopeExec(t, db, `CREATE VIEW client_input_receipts AS SELECT rowid AS rowid,session_id,client_id,plugin_id,host_request_id,request_id,queue_id,json_extract('not-json','$') AS summary_json,fingerprint,message_json,runtime_json,selection_json,live_owner FROM scope_receipts`)
			switch phase {
			case "startup":
				peer := New(rt, &lockedBuffer{})
				defer peer.Close()
				if peer.startupErr != nil {
					t.Fatal(peer.startupErr)
				}
			case "inspect":
				if _, err := srv.inspectPluginSession(context.Background(), "fixture", pluginhost.SessionInspectParams{SessionID: target.SessionID}); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				if _, err := srv.cancelPluginSession(context.Background(), "fixture", pluginhost.SessionCancelParams{SessionID: target.SessionID, QueueID: "missing"}); err != nil {
					t.Fatal(err)
				}
			case "lifecycle":
				if _, err := srv.persistPluginTurnLifecycle("fixture", pluginhost.AgentTurnLifecycleInput{ThreadID: target.SessionID, RequestID: "ordinary-plugin", TurnID: "turn", State: "completed"}); err != nil {
					t.Fatal(err)
				}
			case "history_append":
				if err := session.AppendHistoryRecord(rt.SessionDir, target.SessionID, session.HistoryRecord{Role: "user", Origin: "user", ClientID: "coding-client", Content: "coding history"}); err != nil {
					t.Fatal(err)
				}
			case "send":
				th, err := srv.ensureThreadLoaded(target.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				th.mu.Lock()
				th.running = true
				th.mu.Unlock()
				defer func() { th.mu.Lock(); th.running = false; th.mu.Unlock() }()
				proof := runtime.ClientUserInput{RequestID: "coding-client", Prompt: "coding input"}
				result, err := srv.sendPluginSession(runtime.WithClientUserInput(context.Background(), "fixture", proof), "fixture", pluginhost.SessionSendParams{SessionID: target.SessionID, RequestID: "coding-host", UserInputRequestID: proof.RequestID, Input: pluginhost.SessionInput{Prompt: proof.Prompt}})
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
			db := receiptScopeDB(t, rt.SessionDir)
			owner := "dead-owner"
			if phase == "close" {
				var err error
				owner, err = srv.ensureClientInputOwner()
				if err != nil {
					t.Fatal(err)
				}
			}
			a := seedScopedReceipt(t, db, rt.SessionDir, "alice", "personal", owner, "queued")
			b := seedScopedReceipt(t, db, rt.SessionDir, "alice", "personal", "dead-other-owner", "queued")
			foreign := seedScopedReceipt(t, db, rt.SessionDir, "bob", "personal", "dead-bob", "queued")
			coding := seedScopedReceipt(t, db, rt.SessionDir, "alice", "coding", "dead-coding", "queued")
			switch phase {
			case "startup":
				peer := New(rt, &lockedBuffer{})
				defer peer.Close()
				if peer.startupErr != nil {
					t.Fatal(peer.startupErr)
				}
			case "inspect":
				if _, err := srv.inspectPluginSession(context.Background(), "fixture", pluginhost.SessionInspectParams{SessionID: a.SessionID, RequestID: a.HostRequestID}); err != nil {
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
	db := receiptScopeDB(t, rt.SessionDir)
	a := seedScopedReceipt(t, db, rt.SessionDir, "alice", "personal", "", "held")
	lease, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, "live-other-session")
	if err != nil || !acquired {
		t.Fatal("lease", err)
	}
	defer lease.Release()
	_ = seedScopedReceipt(t, db, rt.SessionDir, "alice", "personal", "live-other-session", "queued")
	scopeExec(t, db, `ALTER TABLE client_input_receipts RENAME TO scope_receipts`)
	// A projection tripwire: unrelated payload columns must never be selected.
	scopeExec(t, db, `CREATE VIEW client_input_receipts AS SELECT rowid AS rowid,session_id,client_id,plugin_id,host_request_id,request_id,queue_id,summary_json,fingerprint,CASE WHEN session_id='`+a.SessionID+`' THEN message_json ELSE json_extract('payload-read','$') END AS message_json,runtime_json,CASE WHEN session_id='`+a.SessionID+`' THEN selection_json ELSE json_extract('attachment-read','$') END AS selection_json,live_owner FROM scope_receipts`)
	peer := New(rt, &lockedBuffer{})
	defer peer.Close()
	if peer.startupErr != nil {
		t.Fatal(peer.startupErr)
	}
	result, err := srv.inspectPluginSession(context.Background(), "fixture", pluginhost.SessionInspectParams{SessionID: a.SessionID})
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
	db := receiptScopeDB(t, rt.SessionDir)
	foreign := seedScopedReceipt(t, db, rt.SessionDir, "bob", "personal", "", "held")
	scopeExec(t, db, `ALTER TABLE client_input_receipts RENAME TO scope_receipts`)
	scopeExec(t, db, `CREATE VIEW client_input_receipts AS SELECT rowid AS rowid,session_id,client_id,plugin_id,host_request_id,request_id,queue_id,summary_json,fingerprint,json_extract('payload-read','$') AS message_json,runtime_json,json_extract('attachment-read','$') AS selection_json,live_owner FROM scope_receipts`)
	_, err := srv.inspectPluginSession(context.Background(), "fixture", pluginhost.SessionInspectParams{SessionID: foreign.SessionID, RequestID: foreign.HostRequestID})
	if err == nil || !strings.Contains(err.Error(), "outside this host user space") {
		t.Fatalf("foreign binding must be rejected before payload query: %v", err)
	}
}

func TestBoundCodingHostDoesNotRecoverPersonalReceipts(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "alice", "coding"
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	db := receiptScopeDB(t, rt.SessionDir)
	personal := seedScopedReceipt(t, db, rt.SessionDir, "alice", "personal", "dead-owner", "queued")
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
