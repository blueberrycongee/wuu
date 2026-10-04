package appserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Failure cases: resizing must not replace source bytes; invalid batches must
// not leave partial files; expiry must not touch history, moved destinations,
// or symlink targets; forks must own their paths independently of the source.
func TestInputImagesPreserveReceivedBytesAndForkOwnership(t *testing.T) {
	stateDir := t.TempDir()
	srv := &Server{rt: &runtime.Session{StateDir: stateDir}}
	raw := encodeTestJPEG(t, 2200, 1, 90)
	input := []TurnStartImage{{Data: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw)}}
	images, err := normalizeTurnStartImages(input)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := srv.userMessageWithInputImages("source", "edit this", images, nil, input)
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := filepath.Glob(filepath.Join(statepath.SessionArtifactDir(stateDir, "source"), "input-images", "*"))
	if len(paths) != 1 || !strings.Contains(msg.Content, paths[0]) {
		t.Fatalf("missing path: %+v %v", msg, paths)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil || !bytes.Equal(raw, data) {
		t.Fatalf("original bytes lost: %v", err)
	}
	if msg.Images[0].Width != 2048 || msg.DisplayContent != "edit this" {
		t.Fatalf("vision/display changed: %+v", msg)
	}
	history := []providers.ChatMessage{msg}
	if err := preserveForkArtifacts(stateDir, "source", "fork", history); err != nil {
		t.Fatal(err)
	}
	forkPath := filepath.Join(statepath.SessionArtifactDir(stateDir, "fork"), "input-images", filepath.Base(paths[0]))
	if !strings.Contains(history[0].Content, forkPath) || strings.Contains(history[0].Content, paths[0]) {
		t.Fatalf("fork retained source path: %s", history[0].Content)
	}
	if err := os.RemoveAll(statepath.SessionArtifactDir(stateDir, "source")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(forkPath); err != nil || !bytes.Equal(raw, data) {
		t.Fatalf("fork lost image: %v", err)
	}
	if err := maintainInputImageStorage(stateDir, time.Now().Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(forkPath); !os.IsNotExist(err) {
		t.Fatalf("expired image remains: %v", err)
	}
	if history[0].Images[0].Data != images[0].Data {
		t.Fatal("cleanup changed vision history")
	}
	if err := preserveForkArtifacts(stateDir, "fork", "expired-fork", history); err != nil {
		t.Fatalf("expired images must not block forks: %v", err)
	}
}

func TestInputImageCacheRejectsEscapeAndPartialBatch(t *testing.T) {
	stateDir := t.TempDir()
	srv := &Server{rt: &runtime.Session{StateDir: stateDir}}
	raw := encodeTestJPEG(t, 1, 1, 90)
	input := []TurnStartImage{{Data: base64.StdEncoding.EncodeToString(raw)}, {Data: "invalid"}}
	if _, err := srv.userMessageWithInputImages("batch", "", nil, nil, input); err == nil {
		t.Fatal("accepted invalid batch")
	}
	paths, _ := filepath.Glob(filepath.Join(statepath.SessionArtifactDir(stateDir, "batch"), "input-images", "*"))
	if len(paths) != 0 {
		t.Fatalf("partial batch leaked: %v", paths)
	}
	dir := statepath.SessionArtifactDir(stateDir, "escape")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "input-images")); err != nil {
		t.Skip(err)
	}
	if _, err := srv.userMessageWithInputImages("escape", "", nil, nil, input[:1]); err == nil {
		t.Fatal("accepted symlinked cache")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote outside cache: %v %v", entries, err)
	}
}

// Failure cases: raw text must stay out of model context; expiry must retain
// snapshots; forks must own their paths; resubmission must restore missing files.
func TestPastedTextAttachmentSubmissionRecoveryAndExpiry(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "done"}}
	rt := newTestRuntime(t, client)
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	stateDir, err := srv.workspaceStateDir()
	if err != nil {
		t.Fatal(err)
	}
	text := "  日志 😀\r\n" + strings.Repeat("reference-only payload\r\n", 80) + "\t\n"
	question := "Explain the attached log."
	parts := []providers.MessageContentPart{{Type: "pasted_text", Text: text, Title: "log.txt"}, {Type: "text", Text: question}}
	payload, err := json.Marshal(map[string]any{
		"id": "2", "method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: text + question, ContentParts: parts},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	_ = waitForMethod(t, out, NotificationTurnCompleted)
	client.mu.Lock()
	sent := client.requests[0].Messages[1]
	client.mu.Unlock()
	paths, _ := filepath.Glob(filepath.Join(statepath.SessionArtifactDir(stateDir, threadID), "input-attachments", "*.txt"))
	if len(paths) != 1 || !strings.Contains(sent.Content, paths[0]) || !strings.Contains(sent.Content, question) || strings.Contains(sent.Content, "reference-only payload") {
		t.Fatalf("text was not delivered as a file reference: content=%q paths=%v", sent.Content, paths)
	}
	if data, err := os.ReadFile(paths[0]); err != nil || string(data) != text {
		t.Fatalf("attachment bytes changed: %v", err)
	}
	readArgs, err := json.Marshal(map[string]string{"path": paths[0]})
	if err != nil {
		t.Fatal(err)
	}
	result, err := srv.thread(threadID).execRuntime.Toolkit.Execute(context.Background(), providers.ToolCall{Name: "read_file", Arguments: string(readArgs)})
	if err != nil || !strings.Contains(result, "reference-only payload") {
		t.Fatalf("model file tools cannot read the submitted attachment: %v (%s)", err, result)
	}
	item := remarshal[TurnStartResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"]).Turn.Items[0]
	if item.InputText != "" || item.ContentParts[0].Text != text || strings.Contains(item.Text, paths[0]) {
		t.Fatalf("public input lost its snapshot or exposed a working path: %+v", item)
	}
	loaded, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	history := visibleMessagesForTest(loaded)
	if history[0].ContentParts[0].Text != text || chatMessageItem("reloaded", history[0]).InputText != "" {
		t.Fatal("history lost the snapshot")
	}
	if err := preserveForkArtifacts(stateDir, threadID, "text-fork", history); err != nil {
		t.Fatal(err)
	}
	forkPaths, _ := filepath.Glob(filepath.Join(statepath.SessionArtifactDir(stateDir, "text-fork"), "input-attachments", "*.txt"))
	if len(forkPaths) != 1 || !strings.Contains(history[0].Content, forkPaths[0]) || strings.Contains(history[0].Content, paths[0]) {
		t.Fatalf("fork lost ownership: %v", forkPaths)
	}
	if err := maintainInputImageStorage(stateDir, time.Now().Add(6*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("working copy expired early: %v", err)
	}
	if err := maintainInputImageStorage(stateDir, time.Now().Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths[0], forkPaths[0]} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expired working copy remains: %v", err)
		}
	}
	if history[0].ContentParts[0].Text != text {
		t.Fatal("expiry changed the snapshot")
	}
	retry, err := srv.userMessageWithInputImages(threadID, strings.TrimSpace(text+question), nil, nil, nil, history[0].ContentParts)
	if err != nil {
		t.Fatal(err)
	}
	paths, _ = filepath.Glob(filepath.Join(statepath.SessionArtifactDir(stateDir, threadID), "input-attachments", "*.txt"))
	if len(paths) != 1 || !strings.Contains(retry.Content, paths[0]) || strings.Contains(retry.Content, "reference-only payload") {
		t.Fatalf("resubmission lost its file reference: %v", paths)
	}
}

func TestTextAttachmentCacheRejectsSymlinksAndMismatchedParts(t *testing.T) {
	stateDir := t.TempDir()
	srv := &Server{rt: &runtime.Session{StateDir: stateDir}}
	parts := []providers.MessageContentPart{{Type: "pasted_text", Text: "payload"}}
	msg, err := srv.userMessageWithInputImages("mismatch", "unrelated instruction", nil, nil, nil, parts)
	if err != nil || len(msg.ContentParts) != 0 {
		t.Fatalf("accepted mismatched metadata: %v", err)
	}
	dir := statepath.SessionArtifactDir(stateDir, "escape-text")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "input-attachments")); err != nil {
		t.Skip(err)
	}
	if _, err := srv.userMessageWithInputImages("escape-text", "payload", nil, nil, nil, parts); err == nil {
		t.Fatal("accepted symlinked attachment storage")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("attachment escaped storage: %v", err)
	}
}

func TestInputImagePathsStayOutOfInputText(t *testing.T) {
	for _, prompt := range []string{"", "inspect this image", "/debug inspect this image"} {
		t.Run(prompt, func(t *testing.T) {
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
					Prompt:   prompt,
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
			if item.Text != prompt {
				t.Fatalf("slash command display or delivered prompt lost: %+v", item)
			}
			if strings.HasPrefix(prompt, "/debug") {
				if !strings.Contains(item.InputText, "inspect this image") {
					t.Fatalf("expanded command input lost: %+v", item)
				}
			} else if item.InputText != "" {
				t.Fatalf("ordinary input contains synthetic text: %q", item.InputText)
			}
			client.mu.Lock()
			sent := client.requests[0].Messages[1]
			client.mu.Unlock()
			if len(sent.Images) != 1 || sent.Images[0].LocalPath == "" || !strings.Contains(sent.Content, sent.Images[0].LocalPath) {
				t.Fatalf("model prompt lost image path: %+v", sent)
			}
			if strings.Contains(item.InputText, sent.Images[0].LocalPath) {
				t.Fatalf("public input_text exposed the server-added image path: %q", item.InputText)
			}

			loaded, err := loadChatMessages(rt.SessionDir, threadID)
			if err != nil {
				t.Fatal(err)
			}
			visible := visibleMessagesForTest(loaded)
			if len(visible) == 0 || strings.Contains(chatMessageItem("reloaded", visible[0]).InputText, sent.Images[0].LocalPath) {
				t.Fatalf("reloaded item exposed the server-added image path: %+v", visible)
			}
		})
	}
}
