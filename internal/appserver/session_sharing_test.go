package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/sessionshare"
)

// This adapter is the privacy boundary before provider replacement: no raw
// input, reasoning, tool arguments, paths, or unrelated attachment bytes.
func TestSessionShareSourceAllowlistAndIndependentRanges(t *testing.T) {
	at := time.Now().UTC()
	thread := Thread{ID: "s", Title: "private title", CWD: "/private/path", Turns: []Turn{{ID: "t", StartedAt: &at, Items: []ThreadItem{
		{ID: "m", Type: ThreadItemUserMessage, Text: "review me", InputText: "hidden prompt", Images: []ThreadItemImage{{Data: "aGVsbG8=", MediaType: "image/png"}}},
		{ID: "r", Type: ThreadItemReasoning, Text: "hidden reasoning"},
		{ID: "tool", Type: ThreadItemToolCall, Arguments: "hidden arguments", Result: "explicit tool result"},
	}}}}
	source, err := sessionShareSource(thread, sessionshare.Selection{MessageIDs: []string{"m"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(source)
	for _, secret := range []string{"hidden", "private", "aGVsbG8=", "explicit tool result"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("source leaked %s: %s", secret, raw)
		}
	}
	if len(source.Items) != 1 {
		t.Fatal(source)
	}
	source, err = sessionShareSource(thread, sessionshare.Selection{ToolResultIDs: []string{"tool"}, AttachmentIDs: []string{"m/image/0"}})
	if err != nil || len(source.Items) != 2 {
		t.Fatal(source, err)
	}
	after := at.Add(time.Second)
	source, err = sessionShareSource(thread, sessionshare.Selection{MessageIDs: []string{"m"}, After: &after})
	if err != nil || len(source.Items) != 0 {
		t.Fatal("time range not enforced", source, err)
	}
}

// Authenticated recipient calls exercise the real dispatcher and response gate,
// not direct projection helpers. Raw history, self-asserted actors, cross-session
// reads, and every post-revocation fetch remain inaccessible.
func TestSessionSharingAuthenticatedEndpoints(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, &fakeClient{})
	sharing := &sharingEndpointProvider{Service: sessionshare.NewService(rt.Documents)}
	consumer := &sharingEndpointConsumer{}
	var registry *pluginhost.ServiceRegistry
	policy := access.NewService("fixture.resource-policy", access.PolicyFunc(func(ctx context.Context, r access.Request) (access.Decision, error) {
		if r.Owner {
			return access.Decision{Allow: true}, nil
		}
		raw, _ := json.Marshal(r)
		result, err := registry.Call(ctx, "fixture.resource-policy", pluginhost.ServiceCallParams{Service: sessionshare.ServiceName, Method: "authorize", Params: raw})
		if err != nil {
			return access.Decision{}, err
		}
		var out access.Decision
		decodeErr := json.Unmarshal(result, &out)
		return out, decodeErr
	}), pluginhost.ServiceRequirement{Name: sessionshare.ServiceName, MajorVersion: 1})
	clients := []pluginhost.Client{sharing, consumer, policy}
	built, conflicts := pluginhost.BuildServiceRegistry(clients...)
	registry = built
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	host := pluginhost.New(clients...)
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer host.Close(ctx)
	rt.PluginHost = host
	client := access.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := registry.Call(ctx, consumer.ID(), pluginhost.ServiceCallParams{Service: access.ServiceName, Method: method, Params: raw})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	if _, err := client.Authorize(ctx, access.Request{Principal: principal.LocalOwner().Identity(), Owner: true, Action: "session/share/preview", Resources: []access.Resource{{Kind: "session", ID: "shared"}}}); err != nil {
		t.Fatal("selected policy unavailable", err)
	}
	output := &lockedBuffer{}
	srv := New(rt, output, WithPrincipal(principal.LocalOwner()), WithAccessPolicy(client))
	defer srv.Close()
	at := time.Now().UTC()
	thread := newThreadState("shared", nil, rt.ProviderName, rt.Model, rt.RootDir, false, at)
	thread.state = testRuntimeState(t, rt)
	thread.Turns = []Turn{{ID: "turn", StartedAt: &at, Items: []ThreadItem{{ID: "message", Type: ThreadItemUserMessage, Text: "visible", InputText: "secret-input", Images: []ThreadItemImage{{Data: "aGVsbG8=", MediaType: "image/png"}}}, {ID: "reasoning", Type: ThreadItemReasoning, Text: "secret-reasoning"}, {ID: "tool", Type: ThreadItemToolCall, Arguments: "secret-arguments", Result: "secret-tool"}}}}
	srv.threads[thread.ID] = thread
	request := func(ctx context.Context, id, method string, params any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err := srv.handleLine(ctx, raw); err != nil {
			t.Fatal(err)
		}
		return responseByID(t, parseOutput(t, output.String()), id)
	}
	selection := sessionshare.Selection{MessageIDs: []string{"message"}, AttachmentIDs: []string{"message/image/0"}}
	response := request(ctx, "preview", "session/share/preview", map[string]any{"thread_id": thread.ID, "selection": selection})
	if response["error"] != nil {
		t.Fatal(response)
	}
	preview := remarshal[sessionshare.Preview](t, response["result"])
	reader, err := principal.Authenticated(principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "one", Subject: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	response = request(ctx, "create", "session/share/create", map[string]any{"thread_id": thread.ID, "selection": selection, "mode": "snapshot", "operation_id": "endpoint-create", "recipients": []principal.Identity{reader.Identity()}, "expires_at": at.Add(time.Hour), "reviewed": true, "review_digest": preview.ReviewDigest})
	if response["error"] != nil {
		t.Fatal(response)
	}
	grant := remarshal[sessionshare.Grant](t, response["result"])
	delete(srv.threads, thread.ID)
	response = request(ctx, "recover", "session/share/create", map[string]any{"thread_id": thread.ID, "selection": selection, "mode": "snapshot", "operation_id": "endpoint-create", "recipients": []principal.Identity{reader.Identity()}, "expires_at": at.Add(time.Hour), "reviewed": true, "review_digest": preview.ReviewDigest, "recover_only": true})
	if response["error"] != nil || remarshal[sessionshare.Grant](t, response["result"]).ID != grant.ID {
		t.Fatal("lost response recovery required source history", response)
	}
	srv.threads[thread.ID] = thread
	readerCtx := principal.WithContext(ctx, reader)
	response = request(readerCtx, "read", "session/share/read", map[string]any{"thread_id": thread.ID, "share_id": grant.ID})
	if response["error"] != nil {
		t.Fatal(response)
	}
	document := remarshal[sessionshare.Document](t, response["result"])
	if len(document.Items) != 2 || document.Items[0].Text != "visible" {
		t.Fatal(document)
	}
	if strings.Contains(output.String(), "secret-") {
		t.Fatal("secret source field reached a share endpoint")
	}
	for i, method := range []string{"thread/resume", "thread/textSnapshot", "thread/content/read", "turn/start", "session/share/create"} {
		if response := request(readerCtx, fmt.Sprintf("denied-%d", i), method, map[string]any{"thread_id": thread.ID, "share_id": grant.ID}); response["error"] == nil {
			t.Fatalf("share escalated to %s", method)
		}
	}
	response = request(readerCtx, "cross-session", "session/share/read", map[string]any{"thread_id": "other", "share_id": grant.ID})
	if response["error"] == nil {
		t.Fatal("cross-session read")
	}
	wrong, _ := principal.Authenticated(principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "two", Subject: "reader"})
	response = request(principal.WithContext(ctx, wrong), "forged", "session/share/read", map[string]any{"thread_id": thread.ID, "share_id": grant.ID, "actor": reader.Identity(), "owner": true})
	if response["error"] == nil {
		t.Fatal("self-asserted identity accepted")
	}
	response = request(readerCtx, "attachment", "session/share/attachment/read", map[string]any{"thread_id": thread.ID, "share_id": grant.ID, "attachment_id": "message/image/0"})
	if response["error"] != nil {
		t.Fatal(response)
	}
	response = request(ctx, "revoke", "session/share/revoke", map[string]any{"thread_id": thread.ID, "share_id": grant.ID})
	if response["error"] != nil {
		t.Fatal(response)
	}
	for i, method := range []string{"session/share/read", "session/share/attachment/read"} {
		if response := request(readerCtx, fmt.Sprintf("revoked-%d", i), method, map[string]any{"thread_id": thread.ID, "share_id": grant.ID, "attachment_id": "message/image/0"}); response["error"] == nil {
			t.Fatal("revoked access", response)
		}
	}
	t.Log("Authenticated RPC evidence: explicit preview/create; safe snapshot; recipient/tenant/session checks; raw history and execution denied; attachment fetch and revoke enforced")
}

type sharingEndpointProvider struct{ *sessionshare.Service }

// This fixture explicitly supplies the temporary store rather than replacing
// state.documents; production declares and retains its selected dependency.
func (*sharingEndpointProvider) RequiredServices() []pluginhost.ServiceRequirement { return nil }

type sharingEndpointConsumer struct{}

func (*sharingEndpointConsumer) ID() string { return sessionshare.ConsumerModule }
func (c *sharingEndpointConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (*sharingEndpointConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*sharingEndpointConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: sessionshare.ServiceName, MajorVersion: 1, Required: true}, {Name: access.ServiceName, MajorVersion: 1, Required: true}}
}
func (*sharingEndpointConsumer) Close(context.Context) error { return nil }
