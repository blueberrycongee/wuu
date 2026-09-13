package appserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestHistoryPagesSurviveAppendsAndRejectRemovedBoundary(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	th := newThreadState("paged", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now())
	for i := 0; i < 63; i++ {
		th.Turns = append(th.Turns, Turn{ID: fmt.Sprint(i), Items: []ThreadItem{{ID: fmt.Sprint(i), Text: strings.Repeat("x", 20_000)}}})
	}
	srv.threads[th.ID] = th
	call := func(id, method string, params any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err := srv.handleLine(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		return responseByID(t, parseOutput(t, out.String()), id)
	}
	first := remarshal[ThreadResumeResult](t, call("first", "thread/resume", ThreadResumeParams{SessionID: th.ID, HistoryPage: true})["result"])
	if len(first.Thread.Turns) >= 63 || !first.Thread.HistoryPaged || first.Thread.HistoryCursor == "" {
		t.Fatal("unbounded first page")
	}
	th.Turns = append(th.Turns, Turn{ID: "appended"})
	all, cursor := first.Thread.Turns, first.Thread.HistoryCursor
	for i := 0; cursor != ""; i++ {
		page := remarshal[struct {
			Turns         []Turn
			HistoryCursor string `json:"history_cursor"`
		}](t, call(fmt.Sprint("page", i), "thread/history/read", map[string]any{"thread_id": th.ID, "cursor": cursor})["result"])
		all = append(page.Turns, all...)
		cursor = page.HistoryCursor
	}
	if len(all) != 63 {
		t.Fatalf("lost or repeated turns: %d", len(all))
	}
	for i := range all {
		if all[i].ID != fmt.Sprint(i) {
			t.Fatalf("wrong turn at %d", i)
		}
	}
	th.Turns = th.Turns[:1]
	if call("removed", "thread/history/read", map[string]any{"thread_id": th.ID, "cursor": first.Thread.HistoryCursor})["error"] == nil {
		t.Fatal("accepted removed history boundary")
	}
}

func TestThreadAttachmentReadsSourceAndThumbnailWithoutDeliveryCache(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	th := newThreadState("images", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now())
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 800, 400))); err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	th.Turns = []Turn{{ID: "turn", Items: []ThreadItem{{ID: "item", Images: []ThreadItemImage{{MediaType: "image/png", Data: data}}}}}}
	srv.threads[th.ID] = th
	hash := sha256.Sum256([]byte("image/png\x00" + data))
	for _, preview := range []bool{false, true} {
		id := fmt.Sprint(preview)
		raw, _ := json.Marshal(map[string]any{"id": id, "method": "thread/attachment/read", "params": map[string]any{"thread_id": th.ID, "turn_id": "turn", "item_id": "item", "index": 0, "sha256": hex.EncodeToString(hash[:]), "preview": preview}})
		if err := srv.handleLine(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), id)
		result := remarshal[struct {
			Data      string
			Total     int
			MediaType string `json:"media_type"`
		}](t, response["result"])
		if !preview {
			if result.Data != data {
				t.Fatal("original changed")
			}
			continue
		}
		bitmap, format, err := image.Decode(base64.NewDecoder(base64.StdEncoding, strings.NewReader(result.Data)))
		if err != nil || format != "jpeg" {
			t.Fatalf("thumbnail: %v", err)
		}
		if bitmap.Bounds().Dx() != 384 || bitmap.Bounds().Dy() != 192 || result.Total > 128*1024 {
			t.Fatal("unbounded thumbnail")
		}
	}
}

func TestHistoryBoundsSingleLongTurnAndRetainsCompleteContent(t *testing.T) {
	th := newThreadState("long", nil, "", "", "", false, time.Now())
	turn := Turn{ID: "turn", ItemsView: TurnItemsViewFull}
	for i := 0; i < 50; i++ {
		turn.Items = append(turn.Items, ThreadItem{ID: fmt.Sprint(i), Type: ThreadItemToolCall, Result: strings.Repeat("x", 20_000)})
	}
	display := &providers.ToolCallDisplay{Label: "Working notes", LabelTranslations: map[string]string{"zh": "工作笔记"}}
	turn.Items = append(turn.Items, ThreadItem{ID: "large", Type: ThreadItemToolCall, Display: display, Result: strings.Repeat("y", 2*1024*1024)})
	th.Turns = []Turn{turn}
	first := th.resumeSnapshotLocked(true)
	raw, _ := json.Marshal(first)
	if len(raw) > 270_000 || first.HistoryCursor == "" {
		t.Fatalf("unbounded page: %d", len(raw))
	}
	last := first.Turns[0].Items[len(first.Turns[0].Items)-1]
	if last.RemoteContentRef == "" || len(last.Result) > 2100 {
		t.Fatal("large result was not deferred")
	}
	if last.Display == nil || last.Display.Label != display.Label || last.Display.LabelTranslations["zh"] != display.LabelTranslations["zh"] {
		t.Fatal("paging a large tool result removed its display name")
	}
	last.Display.LabelTranslations["zh"] = "changed by reader"
	if display.LabelTranslations["zh"] == "changed by reader" {
		t.Fatal("page metadata aliases the stored label")
	}
	if len(th.Turns[0].Items[50].Result) != 2*1024*1024 {
		t.Fatal("projection mutated source")
	}
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	srv.threads[th.ID] = th
	refBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(last.RemoteContentRef, "content:"))
	if err != nil {
		t.Fatal(err)
	}
	var ref []string
	if err := json.Unmarshal(refBytes, &ref); err != nil {
		t.Fatal(err)
	}
	var encoded strings.Builder
	for offset, total := 0, 1; offset < total; {
		id := fmt.Sprint(offset)
		req, _ := json.Marshal(map[string]any{"id": id, "method": "thread/content/read", "params": map[string]any{"thread_id": ref[0], "turn_id": ref[1], "item_id": ref[2], "sha256": ref[3], "offset": offset}})
		if err := srv.handleLine(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		result := remarshal[struct {
			Data  string
			Total int
		}](t, responseByID(t, parseOutput(t, out.String()), id)["result"])
		if result.Data == "" {
			t.Fatal("missing content chunk")
		}
		encoded.WriteString(result.Data)
		offset += len(result.Data)
		total = result.Total
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded.String())
	if err != nil {
		t.Fatal(err)
	}
	var full ThreadItem
	if err := json.Unmarshal(decoded, &full); err != nil {
		t.Fatal(err)
	}
	if full.Result != th.Turns[0].Items[50].Result {
		t.Fatal("full content changed")
	}
}

func TestHistoryBoundsTranslatedToolLabels(t *testing.T) {
	translations := make(map[string]string, historyDisplayTranslations+1)
	for i := 0; i < historyDisplayTranslations+1; i++ {
		translations[fmt.Sprintf("locale-%d", i)] = strings.Repeat("z", 2_000)
	}
	got := historyItem("thread", "turn", ThreadItem{
		ID:   "tool",
		Type: ThreadItemToolCall,
		Display: &providers.ToolCallDisplay{
			Label:             "Working notes",
			LabelTranslations: translations,
		},
		Result: strings.Repeat("y", 2*1024*1024),
	})
	if len(got.Display.LabelTranslations) > historyDisplayTranslations {
		t.Fatalf("history page retained too many translated labels: %d", len(got.Display.LabelTranslations))
	}
	for locale, label := range got.Display.LabelTranslations {
		if len(locale) > historyDisplayLocaleBytes || len(label) > historyDisplayLabelBytes+len("…") {
			t.Fatalf("history page did not bound translated label %q", locale)
		}
	}
}

func BenchmarkHistoryPage(b *testing.B) {
	th := newThreadState("benchmark", nil, "", "", "", false, time.Now())
	for i := 0; i < 2000; i++ {
		th.Turns = append(th.Turns, Turn{ID: fmt.Sprint(i), Items: []ThreadItem{{ID: fmt.Sprint(i), Type: ThreadItemAgentMessage, Text: strings.Repeat("x", 20_000)}}})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = th.resumeSnapshotLocked(true)
	}
}

func TestHistoryRetainsStructuredImageReferencesAndReadsOriginalContentIndex(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	th := newThreadState("tool-images", nil, "", "", rt.RootDir, false, time.Now())
	data := strings.Repeat("a", 20_000)
	th.Turns = []Turn{{ID: "turn", Items: []ThreadItem{{ID: "tool", Type: ThreadItemToolCall, ResultDetail: &toolresult.Result{Content: []toolresult.ContentPart{{Type: "text", Text: "caption"}, {Type: "image", MIMEType: "image/png", Data: data}}}}}}}
	srv.threads[th.ID] = th
	projected := th.resumeSnapshotLocked(true).Turns[0].Items[0]
	if projected.RemoteContentRef != "" {
		t.Fatal("image triggered full-content deferral")
	}
	parts := projected.ResultDetail.Content
	if len(parts) != 2 || parts[0].Text != "caption" || parts[1].Data != "" || parts[1].RemoteRef == "" {
		t.Fatal("structured image was lost")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(parts[1].RemoteRef, "thread:"))
	var ref []any
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]any{"id": "image", "method": "thread/attachment/read", "params": map[string]any{"thread_id": ref[0], "turn_id": ref[1], "item_id": ref[2], "index": ref[3], "sha256": ref[4], "kind": ref[5]}})
	if err := srv.handleLine(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	result := remarshal[struct{ Data string }](t, responseByID(t, parseOutput(t, out.String()), "image")["result"])
	if result.Data != data {
		t.Fatal("wrong original tool-result image")
	}
}
