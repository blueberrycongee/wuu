package sessionshare

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// Security contract: explicit review precedes publishing; read permission does
// not imply mutation, raw history, another recipient, or attachment access.
func TestRestrictedSharingLifecycle(t *testing.T) {
	ctx := context.Background()
	documents := persistence.NewService("fixture.documents", t.TempDir(), "jsonl")
	if err := documents.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer documents.Close(ctx)
	docs := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return documents.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: persistence.ServiceName, Caller: NativeModule, Method: method, Params: raw})
	})
	service := NewService(docs)
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Caller: "fixture.consumer", Method: method, Params: raw})
	})
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	recipient := principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "a", Subject: "reader"}
	owner := principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "a", Subject: "owner"}
	source := Source{SessionID: "s", Items: []Item{{ID: "m", Kind: "message", Role: "assistant", Text: "reviewed", At: now}, {ID: "secret", Kind: "tool_result", Text: "must not leak", At: now}, {ID: "image", Kind: "attachment", MediaType: "image/png", Data: "aGVsbG8=", At: now}}}
	selection := Selection{MessageIDs: []string{"m"}, AttachmentIDs: []string{"image"}}
	preview, err := client.Preview(ctx, PreviewRequest{Source: source, Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Document.Items) != 2 || strings.Contains(preview.ReviewDigest, "must not leak") {
		t.Fatal(preview)
	}
	create := CreateRequest{OperationID: "snapshot-operation", Actor: owner, Source: source, Selection: selection, Mode: "snapshot", Recipients: []principal.Identity{recipient}, ExpiresAt: now.Add(time.Hour), ReviewDigest: preview.ReviewDigest}
	if _, err := client.Create(ctx, create); err == nil {
		t.Fatal("unreviewed create succeeded")
	}
	create.Reviewed = true
	lost := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Caller: "fixture.consumer", Method: method, Params: raw})
		if err == nil && method == "create" {
			return nil, errors.New("synthetic lost acknowledgement")
		}
		return result, err
	})
	if _, err := lost.Create(ctx, create); err == nil {
		t.Fatal("fixture did not lose acknowledgement")
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	service = NewService(docs)
	service.now = func() time.Time { return now }
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Close(ctx)
	grant, err := client.Create(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := client.Create(ctx, create)
	if err != nil || retry.ID != grant.ID {
		t.Fatal("idempotent create changed identity", retry, err)
	}
	conflicting := create
	conflicting.Mode = "live"
	conflicting.LiveUpdates = true
	if _, err := client.Create(ctx, conflicting); !errors.Is(err, persistence.ErrOperationConflict) {
		t.Fatal("operation ID reused with changed intent", err)
	}
	read := ReadRequest{Actor: recipient, SessionID: "s", ShareID: grant.ID}
	projection, err := client.Read(ctx, read)
	if err != nil || projection.Items[0].Text != "reviewed" {
		t.Fatalf("read: %+v %v", projection, err)
	}
	raw, _ := json.Marshal(projection)
	if strings.Contains(string(raw), "aGVsbG8=") || strings.Contains(string(raw), "must not leak") {
		t.Fatalf("unsafe inline payload: %s", raw)
	}
	if _, err := client.Attachment(ctx, AttachmentRequest{ReadRequest: read, AttachmentID: "image"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Attachment(ctx, AttachmentRequest{ReadRequest: read, AttachmentID: "secret"}); err == nil {
		t.Fatal("ungranted attachment")
	}
	wrong := read
	wrong.Actor.Tenant = "other"
	if _, err := client.Read(ctx, wrong); err == nil {
		t.Fatal("cross-tenant read")
	}
	wrong = read
	wrong.SessionID = "another"
	if _, err := client.Read(ctx, wrong); err == nil {
		t.Fatal("cross-session read")
	}
	for _, action := range []string{"turn/start", "thread/resume", "session/share/comment", "session/share/execute", "thread/attachment/read"} {
		d, err := client.Authorize(ctx, access.Request{Principal: recipient, Action: action, Resources: []access.Resource{{Kind: "session-share", ID: grant.ID}}})
		if err != nil || d.Allow {
			t.Fatalf("mutation or raw path %s: %+v %v", action, d, err)
		}
	}
	d, err := client.Authorize(ctx, access.Request{Principal: recipient, Action: "session/share/read", Resources: []access.Resource{{Kind: "session-share", ID: grant.ID}}})
	if err != nil || !d.Allow {
		t.Fatal(d, err)
	}
	source.Items[0].Text = "changed"
	read.Source = &source
	projection, err = client.Read(ctx, read)
	if err != nil || projection.Items[0].Text != "reviewed" {
		t.Fatal("snapshot changed", projection, err)
	}
	create.OperationID = "live-operation"
	create.Source = source
	create.Mode = "live"
	create.LiveUpdates = true
	preview, err = client.Preview(ctx, PreviewRequest{Source: source, Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	create.ReviewDigest = preview.ReviewDigest
	live, err := client.Create(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	source.Items[0].Text = "live update"
	read.ShareID = live.ID
	projection, err = client.Read(ctx, read)
	if err != nil || projection.Items[0].Text != "live update" {
		t.Fatal(projection, err)
	}
	now = now.Add(time.Hour)
	if _, err := client.Read(ctx, read); err == nil {
		t.Fatal("expired live read")
	}
	if _, err := client.Attachment(ctx, AttachmentRequest{ReadRequest: read, AttachmentID: "image"}); err == nil {
		t.Fatal("expired attachment read")
	}
	now = now.Add(-time.Minute)
	read.ShareID = grant.ID
	if err := client.Revoke(ctx, RevokeRequest{Actor: owner, SessionID: "s", ShareID: grant.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(ctx, read); err == nil {
		t.Fatal("revoked read")
	}
	if _, err := client.Attachment(ctx, AttachmentRequest{ReadRequest: read, AttachmentID: "image"}); err == nil {
		t.Fatal("revoked attachment")
	}
}
