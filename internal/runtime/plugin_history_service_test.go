package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func TestHistoryServiceReadsVisibleSessionWithoutNotePlugin(t *testing.T) {
	home := t.TempDir()
	sessDir := statepath.SessionsDir(home)
	sessions := testSessionStore(t, sessDir)
	if _, err := sessions.CreateWithMetadata("source", t.TempDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sessions.AppendHistoryRecord("source", session.HistoryRecord{Role: "user", Content: "keep this fact"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := sessions.AppendHistoryRecord("source", session.HistoryRecord{Role: "user", Content: "durable plugin context", Hidden: true, ReadOnly: true, Origin: "plugin"}); err != nil {
		t.Fatalf("append plugin context: %v", err)
	}
	item := serviceTestPlugin("demo", "plugin:user:demo", "generation")
	handler := newPluginHostServices(item, t.TempDir(), home, nil)
	handler.sessions = sessions
	raw, err := handler.HandleHostService(context.Background(), pluginhost.HostServiceSessionHistoryRead, json.RawMessage(`{"session_id":"source","start_seq":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var page pluginhost.SessionHistoryPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if page.SessionID != "source" || len(page.Records) != 2 || page.Records[0].Content != "keep this fact" || page.Records[1].Content != "durable plugin context" {
		t.Fatalf("page = %+v", page)
	}
	var provenance struct {
		Records []struct {
			Seq      int    `json:"seq"`
			Hidden   bool   `json:"hidden"`
			ReadOnly bool   `json:"read_only"`
			Origin   string `json:"origin"`
		} `json:"records"`
	}
	if err := json.Unmarshal(raw, &provenance); err != nil {
		t.Fatal(err)
	}
	if record := provenance.Records[1]; record.Seq != 2 || !record.Hidden || !record.ReadOnly || record.Origin != "plugin" {
		t.Fatalf("raw audit history lost its presentation provenance: %+v", record)
	}
}

func TestHistoryServiceHidesForeignPluginSessions(t *testing.T) {
	home := t.TempDir()
	sessDir := statepath.SessionsDir(home)
	sessions := testSessionStore(t, sessDir)
	if _, err := sessions.CreateManagedWithMetadata("hidden", t.TempDir(), session.ManagedMetadata{
		Owner: "plugin:other", Visibility: pluginhost.SessionVisibilityPlugin,
	}); err != nil {
		t.Fatalf("create hidden: %v", err)
	}
	item := serviceTestPlugin("demo", "plugin:user:demo", "generation")
	handler := newPluginHostServices(item, t.TempDir(), home, nil)
	handler.sessions = sessions
	_, err := handler.HandleHostService(context.Background(), pluginhost.HostServiceSessionHistoryRead, json.RawMessage(`{"session_id":"hidden","start_seq":1}`))
	if err == nil {
		t.Fatal("expected hidden session to be unavailable")
	}
}

// Provider and native engine identities are useful for reconciling stream items,
// but neither may be relabeled as a host item/turn ID. Older rows omit both.
func TestHistoryServicePreservesProviderAndEngineIdentities(t *testing.T) {
	home := t.TempDir()
	sessDir := statepath.SessionsDir(home)
	sessions := testSessionStore(t, sessDir)
	if _, err := sessions.CreateWithMetadata("source", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, record := range []session.HistoryRecord{{Role: "user", Content: "question"}, {Role: "assistant", Content: "answer", ProviderItemID: "provider-output", EngineTurnID: "native-turn"}} {
		if err := sessions.AppendHistoryRecord("source", record); err != nil {
			t.Fatal(err)
		}
	}
	page, err := readPluginSessionHistory(context.Background(), sessions, "demo", userspace.Binding{}, pluginhost.SessionHistoryReadParams{SessionID: "source", StartSeq: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 2 || page.Records[1].Seq != 2 || page.Records[1].ProviderItemID != "provider-output" || page.Records[1].EngineTurnID != "native-turn" {
		t.Fatalf("lost persisted identities: %+v", page)
	}
	raw, err := json.Marshal(page.Records)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatal(err)
	}
	if records[0]["provider_item_id"] != nil || records[0]["engine_turn_id"] != nil {
		t.Fatalf("legacy row gained invented identities: %s", raw)
	}
	if records[1]["item_id"] != nil || records[1]["turn_id"] != nil {
		t.Fatalf("provider identity mislabeled as host identity: %s", raw)
	}
}

func TestHistoryReplaysExactReceiptIdentityAfterSettlement(t *testing.T) {
	home := t.TempDir()
	dir := statepath.SessionsDir(home)
	sessions := testSessionStore(t, dir)
	_, err := sessions.CreateInitialized(session.Session{ID: "main", CWD: t.TempDir(), Owner: "plugin:personal", Visibility: "plugin", UserSpaceID: "alice", ProductDomain: "personal"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		row, _, err := sessions.AcceptClientInput(session.AcceptedClientInput{Receipt: providers.ClientInputReceipt{RequestID: id, HostRequestID: "host-" + id, ReceiptID: "receipt-" + id, ClientID: "client-" + id, QueueID: "queue-" + id, SessionID: "main", Owner: "plugin:personal", PluginID: "personal", UserSpaceID: "alice", ProductDomain: "personal"}, Fingerprint: "same-payload", MessageJSON: []byte(`{}`), RuntimeJSON: []byte(`{"ExecutionControlBaseline":{"session_id":"main"}}`), LiveOwner: "owner"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = sessions.AppendAcceptedClientHistoryRecord("main", session.HistoryRecord{Role: "user", Origin: "user", ClientID: row.Receipt.ClientID, Content: "identical text"}, row, "turn-"+id, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := sessions.SettleClientInputExecution(session.ClientInputScope{Binding: userspace.Binding{UserSpaceID: "alice", ProductDomain: "personal"}, SessionID: "main", PluginID: "personal"}, "host-"+id, "turn-"+id, "completed", ""); err != nil {
			t.Fatal(err)
		}
	}
	page, err := readPluginSessionHistory(context.Background(), sessions, "personal", userspace.Binding{UserSpaceID: "alice", ProductDomain: "personal"}, pluginhost.SessionHistoryReadParams{SessionID: "main", StartSeq: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 2 {
		t.Fatal(page)
	}
	for i, id := range []string{"one", "two"} {
		r := page.Records[i].Receipt
		if r == nil || r.RequestID != id || r.MessageSeq != page.Records[i].Seq || r.HostTurnID != "turn-"+id || r.ExecutionState != "completed" {
			t.Fatalf("identity lost: %+v", page.Records[i])
		}
	}

	// Exercise the host-service boundary: binding comes from the host generation.
	handler := newPluginHostServices(serviceTestPlugin("personal", "plugin:user:personal", "generation"), t.TempDir(), home, nil)
	defer handler.close()
	handler.sessions = sessions
	handler.binding = userspace.Binding{UserSpaceID: "alice", ProductDomain: "personal"}
	handler.turnRouter = NewPluginSessionRouter()
	unbind := handler.turnRouter.BindSessionResourceAccess(func(ctx context.Context, pluginID, id string, ref *pluginhost.ExecutionAuthorizationReference, operation string) (context.Context, error) {
		caller, ok := principal.FromContext(ctx)
		if !ok || !caller.IsOwner() || pluginID != "personal" || id != "main" || ref != nil || operation != "history.read" {
			return ctx, errors.New("synthetic history policy denied")
		}
		return ctx, caller.Check(ctx)
	})
	defer unbind()
	if _, err := handler.HandleHostService(context.Background(), pluginhost.HostServiceSessionHistoryRead, json.RawMessage(`{"session_id":"main","start_seq":1}`)); err == nil {
		t.Fatal("unverified receipt history read passed resource policy")
	}
	owner := principal.WithContext(context.Background(), principal.LocalOwner())
	raw, err := handler.HandleHostService(owner, pluginhost.HostServiceSessionHistoryRead, json.RawMessage(`{"session_id":"main","start_seq":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var viaHost pluginhost.SessionHistoryPage
	if err = json.Unmarshal(raw, &viaHost); err != nil || len(viaHost.Records) != 2 || viaHost.Records[0].Receipt == nil {
		t.Fatalf("host binding lost receipts: %s %v", raw, err)
	}
	handler.binding = userspace.Binding{UserSpaceID: "bob", ProductDomain: "personal"}
	if _, err = handler.HandleHostService(owner, pluginhost.HostServiceSessionHistoryRead, json.RawMessage(`{"session_id":"main","start_seq":1}`)); err == nil {
		t.Fatal("foreign host read Personal receipt history")
	}
}

func TestCodingHistoryDoesNotReadClientReceipts(t *testing.T) {
	home := t.TempDir()
	dir := statepath.SessionsDir(home)
	sessions := testSessionStore(t, dir)
	if _, err := sessions.CreateWithMetadata("coding", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := sessions.AppendHistoryRecord("coding", session.HistoryRecord{Role: "user", Origin: "user", ClientID: "legacy", Content: "coding"}); err != nil {
		t.Fatal(err)
	}
	provider := session.NewService("test.sessions", testSessionDocuments(t, dir))
	if err := provider.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(context.Background())
	sessions = session.NewClient(context.Background(), func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method == "ClientInputHistoryReceipts" {
			return nil, errors.New("ordinary history must not request Personal receipts")
		}
		return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "test", Method: method, Params: raw})
	})
	page, err := readPluginSessionHistory(context.Background(), sessions, "fixture", userspace.Binding{}, pluginhost.SessionHistoryReadParams{SessionID: "coding", StartSeq: 1})
	if err != nil || len(page.Records) != 1 || page.Records[0].Receipt != nil {
		t.Fatalf("ordinary history changed: %+v %v", page, err)
	}
}
