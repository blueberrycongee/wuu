package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestConversationImageSurvivesSendHistoryReloadAndCompaction(t *testing.T) {
	image := encodeTestJPEG(t, 8, 8, 90)
	encoded := base64.StdEncoding.EncodeToString(image)
	client := &fakeClient{response: providers.ChatResponse{Content: "saw the photo"}}
	rt := newTestRuntime(t, client)
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	payload, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{
			ThreadID: threadID,
			Prompt:   "look at this",
			Images:   []TurnStartImage{{MediaType: "image/jpeg", Data: encoded}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	started := waitForMethod(t, out, NotificationTurnCompleted)
	turn := remarshal[TurnStartResult](t, responseByID(t, started, "2")["result"]).Turn
	if len(turn.Items) != 1 || turn.Items[0].Text != "look at this" {
		t.Fatalf("display text changed: %+v", turn.Items)
	}
	if strings.Contains(turn.Items[0].Text, "wuu-conversation-images") || turn.Items[0].InputText != "" {
		t.Fatalf("image path leaked into the user bubble: %+v", turn.Items[0])
	}

	client.mu.Lock()
	if len(client.requests) != 1 || len(client.requests[0].Messages) < 2 {
		t.Fatalf("provider request missing user turn: %+v", client.requests)
	}
	sent := client.requests[0].Messages[1]
	client.mu.Unlock()
	if len(sent.Images) != 1 || sent.Images[0].Data == "" || sent.Images[0].Path == "" {
		t.Fatalf("model lost the image payload or path: %+v", sent.Images)
	}
	if !strings.Contains(sent.Content, "look at this") || !strings.Contains(sent.Content, strconvQuote(sent.Images[0].Path)) {
		t.Fatalf("model content = %q, want prompt plus quoted path", sent.Content)
	}
	assertConversationImageFile(t, threadID, sent.Images[0].Path, image)

	reloaded, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	visible := visibleMessagesForTest(reloaded)
	if len(visible) < 1 || visible[0].DisplayContent != "look at this" || len(visible[0].Images) != 1 {
		t.Fatalf("reloaded history lost display text or image: %+v", visible)
	}
	if visible[0].Images[0].Path != sent.Images[0].Path || !strings.Contains(visible[0].Content, sent.Images[0].Path) {
		t.Fatalf("reloaded history lost the image reference: %+v", visible[0])
	}
	reread, err := os.ReadFile(visible[0].Images[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(reread) != string(image) {
		t.Fatal("reloaded path does not contain the accepted image")
	}

	history := []providers.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "first reply"},
		visible[0],
		{Role: "assistant", Content: "second reply"},
		{Role: "user", Content: "latest"},
		{Role: "assistant", Content: "latest reply"},
	}
	compacted, err := compact.CompactWithContextWindow(context.Background(), history, &fakeClient{response: providers.ChatResponse{Content: "summary"}}, "test", 100_000)
	if err != nil {
		t.Fatal(err)
	}
	var kept providers.ChatMessage
	found := false
	for _, msg := range compacted {
		if strings.Contains(msg.Content, "look at this") {
			kept = msg
			found = true
		}
	}
	if !found {
		t.Fatalf("compacted history dropped the image turn: %+v", compacted)
	}
	if len(kept.Images) != 0 {
		t.Fatalf("historical image bytes were retained after compaction: %+v", kept.Images)
	}
	if !strings.Contains(kept.Content, "path="+strconvQuote(sent.Images[0].Path)) {
		t.Fatalf("compacted note lost the readable path: %q", kept.Content)
	}
	if strings.Contains(kept.Content, encoded) {
		t.Fatal("compacted history copied image bytes into text")
	}
}

func TestConversationImageSlashCommandKeepsPathOutOfInputText(t *testing.T) {
	image := encodeTestJPEG(t, 2, 2, 80)
	client := &fakeClient{response: providers.ChatResponse{Content: "done"}}
	rt := newTestRuntime(t, client)
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	payload, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{
			ThreadID: threadID,
			Prompt:   "/debug inspect this image",
			Images:   []TurnStartImage{{MediaType: "image/jpeg", Data: base64.StdEncoding.EncodeToString(image)}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	_ = waitForMethod(t, out, NotificationTurnCompleted)

	started := remarshal[TurnStartResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])
	if len(started.Turn.Items) != 1 {
		t.Fatalf("unexpected turn items: %+v", started.Turn.Items)
	}
	item := started.Turn.Items[0]
	if item.Text != "/debug inspect this image" || !strings.Contains(item.InputText, "inspect this image") {
		t.Fatalf("slash command display or delivered prompt lost: %+v", item)
	}
	client.mu.Lock()
	sent := client.requests[0].Messages[1]
	client.mu.Unlock()
	if !strings.Contains(sent.Content, sent.Images[0].Path) {
		t.Fatalf("model prompt lost image path: %+v", sent)
	}
	assertConversationImageFile(t, threadID, sent.Images[0].Path, image)
	if strings.Contains(item.InputText, sent.Images[0].Path) || strings.Contains(item.InputText, "<image name=") {
		t.Fatalf("public input_text exposed the server-added image path: %q", item.InputText)
	}

	loaded, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	visible := visibleMessagesForTest(loaded)
	if len(visible) == 0 || strings.Contains(chatMessageItem("reloaded", visible[0]).InputText, sent.Images[0].Path) {
		t.Fatalf("reloaded item exposed the server-added image path: %+v", visible)
	}
}

func TestConversationImageWriteIsIdempotentAndIsolated(t *testing.T) {
	image := encodeTestJPEG(t, 4, 4, 80)
	encoded := base64.StdEncoding.EncodeToString(image)
	first, err := normalizeTurnStartImages([]TurnStartImage{{MediaType: "image/jpeg", Data: encoded}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalizeTurnStartImages([]TurnStartImage{{MediaType: "image/jpeg", Data: encoded}})
	if err != nil {
		t.Fatal(err)
	}
	if err := attachConversationImagePaths("thread-a", first); err != nil {
		t.Fatal(err)
	}
	if err := attachConversationImagePaths("thread-a", second); err != nil {
		t.Fatal(err)
	}
	if first[0].Path == "" || first[0].Path != second[0].Path {
		t.Fatalf("retry wrote a different file: %q vs %q", first[0].Path, second[0].Path)
	}
	entries, err := os.ReadDir(filepath.Dir(first[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			files++
		}
	}
	if files != 1 {
		t.Fatalf("retry created %d image files, want 1", files)
	}

	other, err := normalizeTurnStartImages([]TurnStartImage{{MediaType: "image/jpeg", Data: encoded}})
	if err != nil {
		t.Fatal(err)
	}
	if err := attachConversationImagePaths("thread-b", other); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(other[0].Path) == filepath.Dir(first[0].Path) {
		t.Fatalf("conversations share an image directory: %s", other[0].Path)
	}
	if strings.Contains(first[0].Path, "thread-b") || !filepath.IsAbs(first[0].Path) {
		t.Fatalf("image path is not an isolated absolute path: %s", first[0].Path)
	}
	info, err := os.Stat(first[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("image mode = %o, want 0600", info.Mode().Perm())
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Dir(first[0].Path))
		_ = os.RemoveAll(filepath.Dir(other[0].Path))
	})
}

func TestConversationImageWriteFailureRejectsTurn(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	blocker := filepath.Join(root, "wuu-conversation-images")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	images, err := normalizeTurnStartImages([]TurnStartImage{{
		MediaType: "image/jpeg",
		Data:      base64.StdEncoding.EncodeToString(encodeTestJPEG(t, 2, 2, 80)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = attachConversationImagePaths("thread-fail", images)
	if err == nil {
		t.Fatal("expected image storage failure")
	}
	if images[0].Path != "" {
		t.Fatalf("failed write recorded path %q", images[0].Path)
	}
	msg, err := userMessageFromPrompt("keep this", images, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "keep this" || strings.TrimSpace(msg.DisplayContent) != "" {
		t.Fatalf("failed image storage changed the message: %+v", msg)
	}
}

func TestConversationImageRejectsPublicTempRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	imageRoot := filepath.Join(root, "wuu-conversation-images")
	if err := os.Mkdir(imageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(imageRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	images := []providers.InputImage{{Data: base64.StdEncoding.EncodeToString(encodeTestJPEG(t, 2, 2, 80)), MediaType: "image/jpeg"}}
	if err := attachConversationImagePaths("private-thread", images); err == nil {
		t.Fatal("expected a public temp root to be rejected")
	}
	if _, err := os.Stat(filepath.Join(imageRoot, "private-thread")); !os.IsNotExist(err) {
		t.Fatalf("private image directory created inside public root: %v", err)
	}
}

func TestConversationImageOnlyDisplayStaysMarker(t *testing.T) {
	images := []providers.InputImage{{Path: "/tmp/wuu-conversation-images/thread/photo.png", MediaType: "image/png", Data: "aaaa"}}
	msg, err := userMessageFromPrompt("", images, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.DisplayContent != "[Image #1]" || !strings.Contains(msg.Content, strconvQuote(images[0].Path)) {
		t.Fatalf("image-only message = content %q display %q", msg.Content, msg.DisplayContent)
	}
	if chatMessageDisplayContent(msg) != "[Image #1]" || threadPreview([]providers.ChatMessage{msg}) != "[Image #1]" {
		t.Fatal("image-only preview exposed the local path")
	}
}

func TestConversationImagePathIsReadableByFileTools(t *testing.T) {
	image := encodeTestJPEG(t, 3, 3, 80)
	images, err := normalizeTurnStartImages([]TurnStartImage{{
		MediaType: "image/jpeg",
		Data:      base64.StdEncoding.EncodeToString(image),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := attachConversationImagePaths("thread-read", images); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(images[0].Path)) })
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetFileScopeRoots([]string{kit.RootDir(), os.TempDir()})
	output, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":` + strconvQuote(images[0].Path) + `}`,
	})
	if err != nil {
		t.Fatalf("read stored image: %v", err)
	}
	if output == "" {
		t.Fatal("file tool returned an empty image read")
	}
}

func TestConversationImageReferenceEscapesPath(t *testing.T) {
	reference := conversationImagePathReference([]providers.InputImage{{
		Path: `/tmp/wuu-conversation-images/thread/a"b.png`,
	}})
	want := `<image name=[Image #1] path="/tmp/wuu-conversation-images/thread/a\"b.png">`
	if reference != want {
		t.Fatalf("reference = %q, want %q", reference, want)
	}
	content := appendConversationImageReference(`say "hello"`, reference)
	if !strings.HasPrefix(content, `say "hello"`) || !strings.Contains(content, want) {
		t.Fatalf("appended content changed user text: %q", content)
	}
}

func assertConversationImageFile(t *testing.T, threadID, path string, want []byte) {
	t.Helper()
	if !filepath.IsAbs(path) || !strings.Contains(path, filepath.Join("wuu-conversation-images", threadID)) {
		t.Fatalf("path %q is not in the conversation temp directory", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("stored image bytes differ from the accepted image")
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })
}

func strconvQuote(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}
