package personalconnectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// These cases protect provider protocol and resource boundaries, not UI labels.
func TestProviderPlansRejectExpansion(t *testing.T) {
	c := Connection{Provider: "github", Domain: "personal", Resources: []string{"acme/project"}, Scopes: []string{"pull_requests:read"}}
	for _, raw := range []string{`{"repository":"acme/project","number":1,"url":"https://evil.test"}`, `{"repository":"../project","number":1}`, `{"repository":"acme/project","number":0}`} {
		if _, err := buildGitHub(c, "github.pr", json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid input %s", raw)
		}
	}
	if _, err := buildSlack(Connection{Domain: "personal"}, "slack.history", json.RawMessage(`{"channel_id":"C123"}`)); err == nil {
		t.Fatal("work data admitted to personal domain")
	}
	if _, err := buildFeishu(Connection{Domain: "personal"}, "feishu.history", json.RawMessage(`{"chat_id":"oc_123"}`)); err == nil {
		t.Fatal("work data admitted to personal domain")
	}
	if _, err := buildNotion(Connection{}, "notion.save_report", json.RawMessage(`{"page_id":"0123456789abcdef0123456789abcdef","text":"report","replace":true}`)); err == nil {
		t.Fatal("accepted destructive report flag")
	}
}
func TestProviderReadProtocolPlans(t *testing.T) {
	c := Connection{Provider: "github", Domain: "personal", Scopes: []string{"checks:read"}}
	p, err := buildGitHub(c, "github.checks", json.RawMessage(`{"repository":"acme/project","ref":"feature/topic","page":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Write || p.Method != "GET" || !strings.Contains(p.URL, "feature%2Ftopic/check-runs") || p.Resource != "acme/project" {
		t.Fatalf("bad GitHub plan: %+v", p)
	}
	p, err = buildNotion(Connection{}, "notion.query", json.RawMessage(`{"data_source_id":"0123456789abcdef0123456789abcdef","cursor":"opaque"}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Write || p.Method != "POST" || !strings.Contains(p.URL, "/data_sources/") || p.Headers["Notion-Version"] != "2025-09-03" {
		t.Fatalf("bad Notion read plan: %+v", p)
	}
	p, err = buildDrive(Connection{Scopes: []string{"https://www.googleapis.com/auth/drive.file"}}, "drive.export", json.RawMessage(`{"file_id":"selected_file"}`))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(p.URL)
	if p.Write || u.Query().Get("mimeType") != "text/plain" {
		t.Fatalf("bad Docs export: %+v", p)
	}
}
func TestNotionAppendOnlyReport(t *testing.T) {
	p, err := buildNotion(Connection{}, "notion.save_report", json.RawMessage(`{"page_id":"0123456789abcdef0123456789abcdef","text":"first\nsecond"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Write || p.Method != "PATCH" || !strings.HasSuffix(p.URL, "/children") {
		t.Fatalf("not append-only: %+v", p)
	}
	var body map[string]any
	if json.Unmarshal(p.Body, &body) != nil || body["children"] == nil {
		t.Fatal("missing report blocks")
	}
	if _, ok := body["archived"]; ok {
		t.Fatal("archive mutation")
	}
}
func TestWorkChatSemanticFailuresAndPageFiltering(t *testing.T) {
	c := Connection{Domain: "work", Scopes: []string{"channels:history"}, Resources: []string{"C123"}}
	p, err := buildSlack(c, "slack.search", json.RawMessage(`{"channel_id":"C123","query":"launch"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Filter([]byte(`{"ok":false,"error":"private token details"}`)); err == nil {
		t.Fatal("HTTP 200 semantic failure accepted")
	}
	filtered, err := p.Filter([]byte(`{"ok":true,"messages":[{"text":"launch now"},{"text":"other"}],"has_more":true,"response_metadata":{"next_cursor":"cursor"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(filtered), "other") || !strings.Contains(string(filtered), "cursor") || !strings.Contains(string(filtered), "selected_channel_current_page") {
		t.Fatalf("bad filtered page: %s", filtered)
	}
	p, err = buildFeishu(Connection{Domain: "work"}, "feishu.mentions", json.RawMessage(`{"chat_id":"oc_chat","mention_user_id":"ou_user"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Filter([]byte(`{"code":999,"msg":"private token details"}`)); err == nil {
		t.Fatal("Feishu API failure accepted")
	}
	filtered, err = p.Filter([]byte(`{"code":0,"data":{"items":[{"message_id":"one","mentions":[{"id":"ou_user"}]},{"message_id":"two","mentions":[]}],"has_more":false}}`))
	if err != nil || strings.Contains(string(filtered), "two") {
		t.Fatalf("bad mentions: %s %v", filtered, err)
	}
}
func TestSelectedSearchFiltersDoNotLeakUnselectedResources(t *testing.T) {
	c := Connection{Resources: []string{"0123456789abcdef0123456789abcdef"}}
	p, err := buildNotion(c, "notion.search", json.RawMessage(`{"resource":"0123456789abcdef0123456789abcdef","query":"report"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Filter([]byte(`{"results":[{"id":"01234567-89ab-cdef-0123-456789abcdef","title":"selected"},{"id":"ffffffffffffffffffffffffffffffff","title":"private-other"}],"has_more":true,"next_cursor":"next"}`))
	if err != nil || strings.Contains(string(b), "private-other") || !strings.Contains(string(b), "selected") {
		t.Fatalf("selection leak %s %v", b, err)
	}
	c = Connection{Scopes: []string{"https://www.googleapis.com/auth/drive.file"}, Resources: []string{"selected"}}
	p, err = buildDrive(c, "drive.search", json.RawMessage(`{"resource":"selected","query":"doc"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err = p.Filter([]byte(`{"files":[{"id":"selected"},{"id":"other"}],"nextPageToken":"next"}`))
	if err != nil || strings.Contains(string(b), "other") {
		t.Fatalf("drive selection leak %s %v", b, err)
	}
}
func TestNotionWriteReadbackContract(t *testing.T) {
	p, err := buildNotion(Connection{}, "notion.save_report", json.RawMessage(`{"page_id":"0123456789abcdef0123456789abcdef","text":"approved report"}`))
	if err != nil {
		t.Fatal(err)
	}
	checks, err := p.Verify(json.RawMessage(`{"results":[{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`))
	if err != nil || len(checks) != 1 {
		t.Fatalf("missing readback %v", err)
	}
	if checks[0].Write || checks[0].Resource != "0123456789abcdef0123456789abcdef" {
		t.Fatal("readback expanded scope")
	}
	if _, err = checks[0].Filter([]byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","type":"paragraph","parent":{"type":"page_id","page_id":"0123456789abcdef0123456789abcdef"},"paragraph":{"rich_text":[{"text":{"content":"different"}}]}}`)); err == nil {
		t.Fatal("wrong readback accepted")
	}
	if _, err = checks[0].Filter([]byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","type":"paragraph","parent":{"type":"page_id","page_id":"0123456789abcdef0123456789abcdef"},"paragraph":{"rich_text":[{"text":{"content":"approved report"}}]}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Verify(json.RawMessage(`{"results":[{"id":"https://evil.test"}]}`)); err == nil {
		t.Fatal("untrusted readback URL accepted")
	}
}

func TestNotionBrokerAppendAndReadbackEndToEnd(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprint("mismatch=", mismatch), func(t *testing.T) {
			a := Actor{"personal", "owner-space", "main-session"}
			ctx := context.Background()
			store := &memoryStore{values: map[string][]byte{}}
			writes, reads := 0, 0
			client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.notion.com" || r.Header.Get("Authorization") != "Bearer synthetic" {
					t.Fatal("host credential/endpoint boundary")
				}
				data := `{"results":[{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`
				if r.Method == "PATCH" {
					writes++
					raw, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(raw), "approved report") {
						t.Fatal("approved body missing")
					}
				} else if r.Method == "GET" {
					reads++
					text := "approved report"
					if mismatch {
						text = "changed"
					}
					data = string(providerJSON(map[string]any{"id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "type": "paragraph", "parent": map[string]string{"type": "page_id", "page_id": "0123456789abcdef0123456789abcdef"}, "paragraph": map[string]any{"rich_text": []any{map[string]any{"text": map[string]string{"content": text}}}}}))
				} else {
					t.Fatal("unexpected method")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data)), Header: http.Header{}}, nil
			})}
			b := newTestBroker(Options{Store: store, HTTPClient: client, Authorization: func(context.Context, Actor) (string, error) { return "host-turn-1", nil }})
			defer b.Close()
			err := b.RegisterVerified(ctx, a, Grant{Connection: Connection{ID: "notion", Provider: "notion", AccountID: "bot", Domain: "personal", Scopes: []string{"read_content", "insert_content"}, Resources: []string{"0123456789abcdef0123456789abcdef"}}, AccessToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			request := Request{ConnectionID: "notion", Operation: "notion.save_report", RequestID: "report-1", Arguments: json.RawMessage(`{"page_id":"0123456789abcdef0123456789abcdef","text":"approved report"}`)}
			pending, err := b.Call(ctx, a, request)
			if err != nil || pending.State != "awaiting_confirmation" || writes != 0 {
				t.Fatalf("bad pending %#v %v", pending, err)
			}
			done, err := b.Decide(ctx, a, pending.ActionID, pending.Digest, true)
			if err != nil || writes != 1 || reads != 1 {
				t.Fatalf("bad execution %#v %v", done, err)
			}
			if mismatch {
				if done.State != "uncertain" {
					t.Fatalf("mismatch not uncertain %#v", done)
				}
			} else if done.State != "completed" || done.Verification != "read_back_verified" {
				t.Fatalf("no verification %#v", done)
			}
			replay, err := b.Call(ctx, a, request)
			if err != nil || replay.State != done.State || writes != 1 || reads != 1 {
				t.Fatal("repeated effect or lost durable receipt")
			}
		})
	}
}
func TestDrivePDFTransportAndSelectionEndToEnd(t *testing.T) {
	ctx := context.Background()
	a := Actor{"personal", "space", "session"}
	calls := 0
	b := newTestBroker(Options{Store: &memoryStore{values: map[string][]byte{}}, HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("alt") != "media" {
			t.Fatal("not media endpoint")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("%PDF-1.7 fixture-document")), Header: http.Header{"Content-Type": []string{"application/pdf"}}}, nil
	})}})
	defer b.Close()
	err := b.RegisterVerified(ctx, a, Grant{Connection: Connection{ID: "drive", Provider: "drive", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/drive.file"}, Resources: []string{"selected"}}, AccessToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Call(ctx, a, Request{ConnectionID: "drive", Operation: "drive.pdf", Arguments: json.RawMessage(`{"file_id":"unselected"}`)}); err == nil || calls != 0 {
		t.Fatal("unselected file reached network")
	}
	result, err := b.Call(ctx, a, Request{ConnectionID: "drive", Operation: "drive.pdf", Arguments: json.RawMessage(`{"file_id":"selected"}`)})
	if err != nil || calls != 1 || !strings.Contains(string(result.Data), "base64") {
		t.Fatalf("PDF unsupported %#v %v", result, err)
	}
}
