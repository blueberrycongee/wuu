package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// The public delegation boundary must reject pairing-only authority, prevent
// replay under changed intent, re-check revocation, and narrow execution policy.
func TestSessionFederationAuthorityAndDurableReplay(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	identity, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "cloud", Subject: "personal"})
	ctx := principal.WithContext(context.Background(), identity)
	enabled := true
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		if r.Owner && strings.HasPrefix(r.Action, "session/federation.") {
			t.Fatal("delegation inherited whole owner permission")
		}
		return access.Decision{Allow: enabled && strings.HasPrefix(r.Action, "session/federation.")}, nil
	})
	limits := SessionFederationConfig{HostID: "laptop", Models: map[string][]string{"fake-provider": {"fake-model"}}, ToolAllow: []string{"read"}}
	s := New(rt, io.Discard, WithAccessPolicy(policy), WithSessionFederation(limits))
	defer s.Close()
	request := pluginhost.SessionRouteRequest{Version: 1, Operation: "create", OperationID: "create-one", Locator: pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "laptop"}, Params: json.RawMessage(`{"visibility":"user","context_source":"fresh","provider":"fake-provider","model":"fake-model"}`)}
	first, err := s.dispatchSessionFederation(ctx, request)
	if err != nil || first.Status != "completed" || first.Error != nil || first.Locator.SessionID == "" {
		t.Fatalf("create=%+v err=%v", first, err)
	}
	second, err := s.dispatchSessionFederation(ctx, request)
	if err != nil || string(second.Result) != string(first.Result) {
		t.Fatalf("replay=%+v err=%v", second, err)
	}
	redisplayed := request
	redisplayed.Locator.DisplayHost = "another-viewer"
	moved, moveErr := s.dispatchSessionFederation(ctx, redisplayed)
	if moveErr != nil || moved.Locator.SessionID != first.Locator.SessionID || moved.Locator.DisplayHost != "another-viewer" {
		t.Fatalf("presentation changed operation identity: %+v %v", moved, moveErr)
	}
	metadata, found, err := rt.Sessions.Find(first.Locator.SessionID)
	if err != nil || !found || metadata.PermissionMode != "read_only" {
		t.Fatalf("execution policy=%+v err=%v", metadata, err)
	}
	request.Params = json.RawMessage(`{"visibility":"user","context_source":"fresh","provider":"fake-provider","model":"fake-model","permission_mode":"unconfined"}`)
	if _, err = s.dispatchSessionFederation(ctx, request); err == nil {
		t.Fatal("broader permission/reused operation accepted")
	}
	request.OperationID = "create-two"
	if _, err = s.dispatchSessionFederation(ctx, request); err == nil {
		t.Fatal("unconfined delegation accepted")
	}
	request.Params = json.RawMessage(`{"visibility":"user","context_source":"fresh","provider":"fake-provider","model":"fake-model","workspace_root":"/"}`)
	if _, err = s.dispatchSessionFederation(ctx, request); err == nil {
		t.Fatal("caller changed workspace authority")
	}
	request.Params = json.RawMessage(`{"visibility":"user","context_source":"fresh","provider":"fake-provider","model":"fake-model"}`)
	request.OperationID = "create-one"
	enabled = false
	if _, err = s.dispatchSessionFederation(ctx, request); err == nil {
		t.Fatal("revocation did not block cached receipt")
	}
	enabled = true
	s.sessionFederation = nil
	if _, err = s.dispatchSessionFederation(ctx, request); err == nil {
		t.Fatal("disabled receiver accepted route")
	}
}

// A provider may block durable admission or lose the final receipt response.
// Neither event may bypass revocation or repeat the native session effect.
func TestSessionFederationAdmissionFenceAndUncertainReceipt(t *testing.T) {
	for _, scenario := range []string{"revoke-during-admission", "lost-final-receipt"} {
		t.Run(scenario, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			var allowed atomic.Bool
			allowed.Store(true)
			documents := &federationBarrierDocuments{Documents: rt.Documents, entered: make(chan struct{}), release: make(chan struct{}), failFinal: scenario == "lost-final-receipt"}
			rt.Documents = documents
			identity, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "cloud", Subject: "bounded"})
			ctx, cancel := context.WithTimeout(principal.WithContext(context.Background(), identity), 10*time.Second)
			defer cancel()
			s := New(rt, io.Discard, WithAccessPolicy(access.PolicyFunc(func(context.Context, access.Request) (access.Decision, error) {
				return access.Decision{Allow: allowed.Load()}, nil
			})), WithSessionFederation(SessionFederationConfig{HostID: "fixture", Models: map[string][]string{"fake-provider": {"fake-model"}}, ToolAllow: []string{"read"}}))
			defer s.Close()
			request := pluginhost.SessionRouteRequest{Version: 1, Operation: "create", OperationID: "fenced", Locator: pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "fixture"}, Params: json.RawMessage(`{"visibility":"user","context_source":"fresh","provider":"fake-provider","model":"fake-model"}`)}
			type reply struct {
				result pluginhost.SessionRouteResult
				err    error
			}
			done := make(chan reply, 1)
			go func() { r, e := s.dispatchSessionFederation(ctx, request); done <- reply{r, e} }()
			select {
			case <-documents.entered:
			case premature := <-done:
				t.Fatalf("admission completed before barrier: %+v %v", premature.result, premature.err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if scenario == "revoke-during-admission" {
				allowed.Store(false)
			}
			close(documents.release)
			var first reply
			select {
			case first = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if first.err != nil || first.result.Error == nil {
				t.Fatalf("missing bounded failure: %+v %v", first.result, first.err)
			}
			wanted := 1
			if scenario == "revoke-during-admission" {
				wanted = 0
				if first.result.Status != "completed" {
					t.Fatalf("nonexecuted outcome=%+v", first.result)
				}
			} else if first.result.Status != "unknown" {
				t.Fatalf("uncertain outcome=%+v", first.result)
			}
			allowed.Store(true)
			// A fresh receiver has no active table; the durable receipt must still fence retry.
			restarted := New(rt, io.Discard, WithAccessPolicy(access.PolicyFunc(func(context.Context, access.Request) (access.Decision, error) {
				return access.Decision{Allow: true}, nil
			})), WithSessionFederation(SessionFederationConfig{HostID: "fixture", Models: map[string][]string{"fake-provider": {"fake-model"}}, ToolAllow: []string{"read"}}))
			defer restarted.Close()
			retry, err := restarted.dispatchSessionFederation(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "lost-final-receipt" && retry.Status != "unknown" {
				t.Fatalf("retry=%+v", retry)
			}
			sessions, err := rt.Sessions.List(0)
			if err != nil || len(sessions) != wanted {
				t.Fatalf("effects=%d want=%d err=%v", len(sessions), wanted, err)
			}
		})
	}
}

type federationBarrierDocuments struct {
	persistence.Documents
	entered, release chan struct{}
	receipts         int
	failFinal        bool
}

func (d *federationBarrierDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	for _, write := range tx.Writes {
		if write.Namespace == federationReceiptNamespace {
			d.receipts++
			if d.receipts == 1 {
				close(d.entered)
				select {
				case <-d.release:
				case <-ctx.Done():
					return persistence.Receipt{}, ctx.Err()
				}
			}
			if d.receipts == 2 && d.failFinal {
				return persistence.Receipt{}, errors.New("fixture lost final receipt")
			}
			break
		}
	}
	return d.Documents.Commit(ctx, tx)
}

func TestSessionFederationListReplayRechecksEachSession(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	identity, err := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "cloud", Subject: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(principal.WithContext(context.Background(), identity), 10*time.Second)
	defer cancel()
	var inspectRevoked atomic.Bool
	out := &federationGuardWriter{frames: make(chan capturedFrame, 128)}
	s := New(rt, out, WithPrincipal(identity), WithAccessPolicy(access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		return access.Decision{Allow: r.Action != "session/federation.inspect" || !inspectRevoked.Load()}, nil
	})), WithSessionFederation(SessionFederationConfig{HostID: "fixture", Models: map[string][]string{"fake-provider": {"fake-model"}}, ToolAllow: []string{"read"}}))
	defer s.Close()
	create := pluginhost.SessionRouteRequest{Version: 1, Operation: "create", OperationID: "row", Locator: pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "fixture"}, Params: json.RawMessage(`{"visibility":"user","context_source":"fresh","provider":"fake-provider","model":"fake-model"}`)}
	created, err := s.dispatchSessionFederation(ctx, create)
	if err != nil || created.Error != nil {
		t.Fatalf("create=%+v %v", created, err)
	}
	list := create
	list.Operation = "list"
	list.OperationID = "list"
	list.Params = json.RawMessage(`{}`)
	raw, _ := json.Marshal(Request{ID: json.RawMessage(`"list"`), Method: "session/federation", Params: mustJSON(list)})
	if err = s.handleLine(ctx, raw); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case frame := <-out.frames:
			var response Response
			if json.Unmarshal(frame.payload, &response) != nil || string(response.ID) != `"list"` {
				continue
			}
			if !strings.Contains(string(frame.payload), created.Locator.SessionID) {
				t.Fatalf("missing authorized row: %s", frame.payload)
			}
			if frame.guard == nil || frame.guard() != nil {
				t.Fatal("authorized list guard missing")
			}
			inspectRevoked.Store(true)
			if frame.guard() == nil {
				t.Fatal("workspace list grant replayed a revoked session row")
			}
			return
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

type federationGuardWriter struct{ frames chan capturedFrame }

func (*federationGuardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *federationGuardWriter) WriteAuthorized(p []byte, guard func() error) (int, error) {
	w.frames <- capturedFrame{payload: append([]byte(nil), p...), guard: guard}
	return len(p), nil
}
