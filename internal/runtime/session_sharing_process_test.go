package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/sessionshare"
)

// Run through the packed public SDK and independent process with the native
// sharing module absent. Policies and persistence must not bypass composition.
func TestSessionSharingExternalProcess(t *testing.T) {
	fixture := os.Getenv("WUU_SHARING_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/session-sharing-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root, home := t.TempDir(), t.TempDir()
	provider := testRuntimePlugin("example.restricted-sharing")
	provider.Root = fixture
	provider.Runtime.Command = node
	provider.Runtime.Args = []string{"runner.ts"}
	consumerID := "fixture.sharing-consumer"
	consumer := NativeProviderFactory{ID: consumerID, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return &runtimeServiceConsumer{id: consumerID, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: sessionshare.ServiceName, MajorVersion: 1, Required: true}}}, nil
	}}
	start := func() (*pluginhost.Host, *sessionshare.Client) {
		modules := []string{persistence.JSONLModule, provider.ID, consumerID, access.NativeModule, access.ConsumerModule}
		host, _, err := buildPluginHost([]pluginpkg.Plugin{provider}, root, "sharing", home, t.TempDir(), nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "", consumer)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = host.Close(context.Background()) })
		if err := host.Activate(ctx); err != nil {
			t.Fatal(err)
		}
		id, ok := host.ServiceRegistry().ProviderPluginID(sessionshare.ServiceName, 1)
		if !ok || id != provider.ID {
			t.Fatalf("native sharing fallback: %s %v", id, ok)
		}
		return host, sessionshare.NewClient(boundServiceCall(host, consumerID, sessionshare.ServiceName))
	}
	host, client := start()
	actor := principal.LocalOwner().Identity()
	recipient := principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "one", Subject: "reader"}
	source := sessionshare.Source{SessionID: "s", Items: []sessionshare.Item{{ID: "m", Kind: "message", Role: "assistant", Text: "visible sk-12345678901234567890", At: time.Now().UTC()}, {ID: "tool", Kind: "tool_result", Text: "hidden tool payload"}}}
	selection := sessionshare.Selection{MessageIDs: []string{"m"}}
	preview, err := client.Preview(ctx, sessionshare.PreviewRequest{Source: source, Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Document.Items) != 1 || preview.Document.Items[0].Text != "visible [redacted by example policy]" {
		t.Fatal("independent provider's format not selected", preview)
	}
	create := sessionshare.CreateRequest{OperationID: "external-create", Actor: actor, Source: source, Selection: selection, Mode: "snapshot", Recipients: []principal.Identity{recipient}, ExpiresAt: time.Now().Add(time.Hour), Reviewed: true, ReviewDigest: preview.ReviewDigest}
	call := boundServiceCall(host, consumerID, sessionshare.ServiceName)
	lost := sessionshare.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := call(ctx, method, raw)
		if err == nil && method == "create" {
			return nil, errors.New("synthetic acknowledgement loss")
		}
		return result, err
	})
	if _, err := lost.Create(ctx, create); err == nil {
		t.Fatal("fixture did not lose create acknowledgement")
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	host, client = start()
	grant, err := client.Create(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	recovery := create
	recovery.Source.Items = nil
	retry, err := client.Create(ctx, recovery)
	if err != nil || retry.ID != grant.ID {
		t.Fatal("duplicate grant after replay", retry, err)
	}
	changed := create
	changed.ExpiresAt = changed.ExpiresAt.Add(time.Minute)
	if _, err := client.Create(ctx, changed); err == nil {
		t.Fatal("operation intent conflict not enforced")
	}
	read := sessionshare.ReadRequest{Actor: recipient, SessionID: "s", ShareID: grant.ID}
	if _, err := client.Read(ctx, read); err != nil {
		t.Fatal(err)
	}
	policy := access.NewClient(boundServiceCall(host, access.ConsumerModule, access.ServiceName))
	decision, err := policy.Authorize(ctx, access.Request{Principal: recipient, Action: "session/share/read", Resources: []access.Resource{{Kind: "session-share", ID: grant.ID}}})
	if err != nil || !decision.Allow {
		t.Fatal("common selected auth did not delegate", decision, err)
	}
	decision, err = policy.Authorize(ctx, access.Request{Principal: recipient, Action: "turn/start", Resources: []access.Resource{{Kind: "session", ID: "s"}}})
	if err != nil || decision.Allow {
		t.Fatal("read escalated to execution", decision, err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	host, client = start()
	if _, err := client.Read(ctx, read); err != nil {
		t.Fatal("grant lost on restart", err)
	}
	if err := client.Revoke(ctx, sessionshare.RevokeRequest{Actor: actor, SessionID: "s", ShareID: grant.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(ctx, read); err == nil {
		t.Fatal("revoked read allowed")
	}
	if _, found := host.RetirePlugin(ctx, provider.ID, errors.New("removed")); !found {
		t.Fatal("provider not retired")
	}
	raw, _ := json.Marshal(sessionshare.PreviewRequest{Source: source, Selection: selection})
	if _, err := host.ServiceRegistry().Call(ctx, consumerID, pluginhost.ServiceCallParams{Service: sessionshare.ServiceName, Method: "preview", Params: raw}); err == nil {
		t.Fatal("removed provider silently fell back")
	}
	t.Log("Public SDK process: independent format/policy, recipient grant, common authorization, no execution escalation, durable restart, revocation, default absent and removal fail-closed")
}
