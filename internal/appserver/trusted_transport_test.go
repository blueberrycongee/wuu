package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// A trusted hop changes request provenance, never the shared runtime owner.
func TestTrustedTransportUsesSharedSessionAndRechecksReceipt(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	var revoked atomic.Bool
	identity := principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "tenant", Subject: "reader"}
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		if r.Owner {
			return access.Decision{Allow: true}, nil
		}
		return access.Decision{Allow: !revoked.Load() && r.Principal == identity && (r.Action == MethodThreadResume || r.Action == "event:thread/updated")}, nil
	})
	s := New(rt, out, WithAccessPolicy(policy), WithTrustedLocalTransport())
	defer s.Close()
	call := func(id, method string, params any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(Request{ID: mustJSON(id), Method: method, Params: mustJSON(params)})
		if err := s.handleLine(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		return responseByID(t, parseOutput(t, out.String()), id)
	}
	created := call("create", MethodThreadStart, map[string]any{})
	thread := remarshal[ThreadStartResult](t, created["result"]).Thread.ID
	validation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer validation.Close()
	envelope := map[string]any{"validation": map[string]string{"address": strings.TrimPrefix(validation.URL, "http://"), "token": strings.Repeat("a", 32)}, "version": 1, "peer_id": "fixture-1", "principal": identity, "owner": false, "method": MethodThreadResume, "params": map[string]any{"session_id": thread}}
	result := call("read", "transport/dispatch", envelope)
	frame := remarshal[trustedTransportFrame](t, result["result"])
	if len(frame.Receipt) == 0 || frame.Message.Error != nil {
		t.Fatalf("read failed: %v", result)
	}
	if !strings.Contains(string(mustJSON(frame.Message.Result)), thread) {
		t.Fatal("shared session missing")
	}
	check := map[string]any{"version": 1, "peer_id": "fixture-1", "receipt": frame.Receipt}
	if call("check", "transport/check", check)["error"] != nil {
		t.Fatal("live receipt rejected")
	}
	check["peer_id"] = "other"
	if call("stolen", "transport/check", check)["error"] == nil {
		t.Fatal("cross-peer receipt accepted")
	}
	check["peer_id"] = "fixture-1"
	envelope["owner"] = true
	if call("rebind", "transport/dispatch", envelope)["error"] == nil {
		t.Fatal("old peer changed owner provenance")
	}
	envelope["owner"] = false
	revoked.Store(true)
	if call("revoked", "transport/check", check)["error"] == nil {
		t.Fatal("revoked receipt accepted")
	}
	revoked.Store(false)
	validation.Close()
	if call("expired-auth", "transport/check", check)["error"] == nil {
		t.Fatal("receipt survived authentication validator loss")
	}
	envelope["method"] = "transport/dispatch"
	envelope["params"] = map[string]any{"owner": true}
	if call("nested", "transport/dispatch", envelope)["error"] == nil {
		t.Fatal("nested transport accepted")
	}
	if call("local", MethodConfigRead, nil)["error"] != nil {
		t.Fatal("delegation changed local principal")
	}
}

func TestTrustedTransportRequiresExplicitLocalPipe(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	id, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "tenant", Subject: "reader"})
	for _, opts := range [][]ServerOption{{WithAccessPolicy(access.OwnerOnly{})}, {WithPrincipal(id), WithTrustedLocalTransport()}} {
		out := &lockedBuffer{}
		s := New(rt, out, opts...)
		raw := []byte(`{"id":"attack","method":"transport/dispatch","params":{"version":1,"peer_id":"forged","owner":true,"principal":{"kind":"user","issuer":"wuu:local","tenant":"local","subject":"owner"},"method":"config/read"}}`)
		err := s.handleLine(context.Background(), raw)
		if err == nil && !strings.Contains(out.String(), `"error"`) {
			t.Fatal("untrusted transport gained owner")
		}
		s.Close()
	}
}

// A new controller cannot revoke another run merely by exhausting the peer
// table. Release is explicit; durable command receipts remain independent.
func TestTrustedTransportCapacityDoesNotEvictActivePeers(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	s := New(rt, out, WithTrustedLocalTransport())
	defer s.Close()
	validation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer validation.Close()
	input := trustedTransportEnvelope{Version: 1, Principal: principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "tenant", Subject: "controller"}, Validation: trustedTransportValidator{Address: strings.TrimPrefix(validation.URL, "http://"), Token: strings.Repeat("a", 32)}}
	var first principal.Verified
	for i := 0; i < 256; i++ {
		input.PeerID = fmt.Sprintf("live-%d", i)
		p, err := s.bindTrustedTransport(input)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = p
		}
	}
	input.PeerID = "overflow"
	if _, err := s.bindTrustedTransport(input); err == nil {
		t.Fatal("capacity overflow admitted")
	}
	if err := first.Check(context.Background()); err != nil {
		t.Fatalf("overflow revoked existing live run: %v", err)
	}
	raw, _ := json.Marshal(Request{ID: mustJSON("release"), Method: "transport/release", Params: mustJSON(map[string]any{"version": 1, "peer_id": "live-1"})})
	if err := s.handleLine(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := s.bindTrustedTransport(input); err != nil {
		t.Fatalf("released slot did not become available: %v", err)
	}
}
