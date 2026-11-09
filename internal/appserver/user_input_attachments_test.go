package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// A foreground file selection must reach the persisted user message without
// allowing a background plugin to impersonate that selection or reading the
// private memory state directory through a generated working-copy path.
func TestUserHostClientAttachmentsPersistAndRetryWithoutNewCopies(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("received")})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionCreateParams{RequestID: "attachment-main", Visibility: "plugin", ContextSource: "fresh", AgentID: "agent-main"})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("explicit user file")
	digest := sha256.Sum256(data)
	sum := hex.EncodeToString(digest[:])
	ref := pluginhost.SessionInputAttachment{SHA256: sum, Filename: "note.txt", MediaType: "text/plain"}
	params := pluginhost.SessionSendParams{RequestID: "attachment-send", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "", Attachments: []pluginhost.SessionInputAttachment{ref}}}
	if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", params); err == nil {
		t.Fatal("background plugin admitted user attachment")
	}
	params.UserInputRequestID = "selection"
	proof := runtime.ClientUserInput{RequestID: "selection", Prompt: "", Attachments: []runtime.ClientInputAttachment{{SHA256: sum, Filename: "note.txt", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString(data)}}}
	if err := runtime.ValidateClientUserInput(proof); err != nil {
		t.Fatal(err)
	}
	ctx := runtime.WithClientUserInput(context.Background(), "fixture", proof)
	first, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, ctx), "fixture", params)
	if err != nil {
		t.Fatal(err)
	}
	waitForMethod(t, out, NotificationTurnCompleted)
	history, err := loadChatMessages(rt.Sessions, created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	users := 0
	for _, message := range history {
		if len(message.Files) > 0 {
			users++
			if message.Origin != "user" || message.ReadOnly || message.DisplayContent != "" {
				t.Fatalf("wrong provenance: %+v", message)
			}
			path = message.Files[0].LocalPath
			bytes, err := os.ReadFile(path)
			if err != nil || string(bytes) != string(data) {
				t.Fatal("working copy changed", err)
			}
		}
	}
	if users != 1 || path == "" {
		t.Fatal("submitted file missing")
	}
	rel, err := filepath.Rel(rt.RootDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("working copy outside user files root: %s", path)
	}
	second, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, ctx), "fixture", params)
	if err != nil || second.TurnID != first.TurnID {
		t.Fatalf("retry changed turn: %+v %+v %v", first, second, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("retry recreated attachment: %d %v", len(entries), err)
	}
}

func TestUserHostAttachmentWorkingRootRejectsSymlinkEscape(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(rt.RootDir, ".input-files")); err != nil {
		t.Fatal(err)
	}
	srv := &Server{rt: rt}
	msg := providers.ChatMessage{Role: "user", Files: []providers.InputFile{{Filename: "note.txt", MediaType: "text/plain", Data: "eA=="}}}
	if _, err := srv.persistInputWorkingCopies(context.Background(), "main", msg, nil, false); err == nil {
		t.Fatal("host wrote selected bytes through a tool-created root symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside root changed: %d %v", len(entries), err)
	}
	if err := srv.maintainInputAttachments(time.Now()); err == nil {
		t.Fatal("maintenance accepted an escaping working-root symlink")
	}
}

// Agent byte envelopes must bind the target/run, validate content, and cannot
// combine with an authenticated human submission or bypass private ownership.
func TestAttachmentHandoffRejectsInvalidEnvelope(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	srv := New(rt, &lockedBuffer{})
	defer srv.Close()
	file := pluginhost.SessionHandoffAttachment{SHA256: "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881", Filename: "note.txt", MediaType: "text/plain", Data: "eA=="}
	for _, name := range []string{"target", "run", "digest", "path", "source", "size", "human"} {
		t.Run(name, func(t *testing.T) {
			handoff := &pluginhost.SessionAttachmentHandoff{Version: 1, SessionID: "target", RequestID: "run", Source: providers.MessageRef{SessionID: "main", Seq: 7}, Attachments: []pluginhost.SessionHandoffAttachment{file}}
			p := pluginhost.SessionSendParams{SessionID: "target", RequestID: "run", Input: pluginhost.SessionInput{Prompt: "read", AttachmentHandoff: handoff}}
			switch name {
			case "target":
				handoff.SessionID = "another"
			case "run":
				handoff.RequestID = "another"
			case "digest":
				handoff.Attachments[0].SHA256 = strings.Repeat("0", 64)
			case "path":
				handoff.Attachments[0].Filename = "../outside"
			case "source":
				handoff.Source.Seq = 0
			case "size":
				handoff.Attachments[0].Data = strings.Repeat("a", base64.StdEncoding.EncodedLen(pluginhost.MaxSessionHandoffBytes)+100)
			case "human":
				p.UserInputRequestID = "human"
			}
			if _, err := validateAttachmentHandoff(p); err == nil {
				t.Fatal("invalid envelope admitted")
			}
		})
	}
}

func TestTaskAttachmentHandoffLocalDelivery(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("received delegated file")})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	ctx := pluginInvocationContext(t, rt, context.Background())
	target, err := srv.createPluginSession(ctx, "fixture", pluginhost.SessionCreateParams{RequestID: "worker", Visibility: "plugin", ContextSource: "fresh", AgentID: "agent-main"})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the admitted upper bound through normalization and durable delivery.
	data := []byte(strings.Repeat("x", pluginhost.MaxSessionHandoffBytes))
	hash := sha256.Sum256(data)
	params := pluginhost.SessionSendParams{SessionID: target.SessionID, RequestID: "worker-run", Input: pluginhost.SessionInput{Prompt: "read attached file", AttachmentHandoff: &pluginhost.SessionAttachmentHandoff{Version: 1, SessionID: target.SessionID, RequestID: "worker-run", Source: providers.MessageRef{SessionID: "main", Seq: 7}, Attachments: []pluginhost.SessionHandoffAttachment{{SHA256: hex.EncodeToString(hash[:]), Filename: "notes.txt", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString(data)}}}}}
	first, err := srv.sendPluginSession(ctx, "fixture", params)
	if err != nil {
		t.Fatal(err)
	}
	waitForMethod(t, out, NotificationTurnCompleted)
	history, err := loadChatMessages(rt.Sessions, target.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	copies := 0
	for _, m := range history {
		if len(m.Files) > 0 {
			copies++
			actual, e := os.ReadFile(m.Files[0].LocalPath)
			if e != nil || string(actual) != string(data) || m.Origin == "user" || !m.ReadOnly {
				t.Fatalf("invalid delivered file/provenance: %v", e)
			}
		}
	}
	if copies != 1 {
		t.Fatalf("want one persisted file, got %d", copies)
	}
	replay, err := srv.sendPluginSession(ctx, "fixture", params)
	if err != nil || replay.TurnID != first.TurnID {
		t.Fatalf("repeated send changed run: %+v %v", replay, err)
	}
}
