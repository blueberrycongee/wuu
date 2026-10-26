package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprogram"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// TestPluginPackageInstallAllowedWhileTurnRunning is the install-while-active
// regression: installation publishes a trusted generation while the admitted
// turn retains its own host and model-facing surface.
func TestPluginPackageInstallAllowedWhileTurnRunning(t *testing.T) {
	stream := newBlockingStreamClient("eventually done")
	client := &fakeClient{response: providers.ChatResponse{Content: "eventually done"}, onChat: func(_ int, _ providers.ChatRequest) {
		stream.once.Do(func() { close(stream.started) })
		<-stream.release
	}}
	rt := newTestRuntime(t, client)
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	rt = newReloadBehaviorRuntime(t, rt, client)
	bindConfigSourceForTest(t, rt)
	activeHost := rt.PluginHost
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(func() {
		select {
		case <-stream.release:
		default:
			close(stream.release)
		}
		srv.Close()
	})

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	raw, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	select {
	case <-stream.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("turn did not start; output:\n%s", out.String())
	}

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), []byte(`{"id":"live-install","name":"Live Install","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	installReq, err := json.Marshal(map[string]any{
		"id":     "3",
		"method": MethodPluginPackageInstall,
		"params": PluginPackageInstallParams{Path: source},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), installReq); err != nil {
		t.Fatalf("plugin/package/install: %v", err)
	}
	if result := responseByID(t, parseOutput(t, out.String()), "3")["result"]; result == nil {
		t.Fatalf("install failed while a turn was running; output:\n%s", out.String())
	}
	thread := srv.threads[threadID]
	thread.mu.Lock()
	retainedHost := thread.execRuntime.PluginHost
	thread.mu.Unlock()
	if retainedHost != activeHost {
		t.Fatal("install replaced admitted turn plugin host")
	}

	found := false
	for _, record := range srv.currentExtensionInventory() {
		if record.ID == "plugin:user:live-install" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("installed package missing from catalog: %+v", srv.currentExtensionInventory())
	}
}

// A catalog publication replaces the selected State, not the authority of an
// accepted turn. Opening that conversation must not try to replace its owner.
func TestPluginPackageMutationKeepsRunningConversationReadable(t *testing.T) {
	for _, action := range []string{"install", "remove", "disable_deleted_source"} {
		t.Run(action, func(t *testing.T) {
			if action == "disable_deleted_source" {
				if _, err := exec.LookPath("node"); err != nil {
					t.Skip("Node.js is required for the single-file development plugin")
				}
			}
			entered, released := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(released) }) }
			client := &fakeClient{response: providers.ChatResponse{Content: "preserved answer"}, onChat: func(call int, _ providers.ChatRequest) {
				if call == 1 {
					close(entered)
					<-released
				}
			}}
			rt := newTestRuntime(t, client)
			rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
			rt = newReloadBehaviorRuntime(t, rt, client)
			// Drive publication through the public package RPCs, without racing
			// the initial filesystem watch against the first turn admission.
			home := rt.WuuHome
			rt.WuuHome = ""
			out := &lockedBuffer{}
			srv := New(rt, out)
			rt.WuuHome = home
			t.Cleanup(func() { unblock(); srv.Close(); _, _ = rt.Cleanup() })
			bindConfigSourceForTest(t, rt)
			source := t.TempDir()
			writePluginPackageFile(t, filepath.Join(source, "plugin.json"), `{"id":"readable-plugin","name":"Readable plugin","version":"1.0.0"}`)
			call := func(id, method string, params any) map[string]any {
				t.Helper()
				dispatchPayload(t, srv, id, method, params)
				response := responseByID(t, parseOutput(t, out.String()), id)
				if response["error"] != nil {
					t.Fatalf("%s: %+v", method, response)
				}
				return response
			}
			var development pluginpkg.Plugin
			var developmentSource string
			if action == "remove" {
				call("install", MethodPluginPackageInstall, PluginPackageInstallParams{Path: source})
			} else if action == "disable_deleted_source" {
				developmentSource = filepath.Join(source, "clock.ts")
				writePluginPackageFile(t, developmentSource, `import type { RuntimePlugin } from "@wuu/plugin-sdk";
export default {
 initialize() { return { tools: [{ id: "clock", description: "Read fixture time", input_schema: { type: "object" } }] }; },
 executeTool() { return { result: { content: [{ type: "text", text: "fixture time" }] } }; }
} satisfies RuntimePlugin;`)
				prepared, cleanup, err := pluginprogram.PrepareSingleFile(context.Background(), developmentSource)
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				authorization := pluginpkg.DevAuthorization{PluginID: pluginprogram.SingleFileID(developmentSource), Directory: developmentSource, SourceKind: "file", Token: "synthetic-development-authorization", CreatedAt: time.Now().UTC()}
				authorizationPath, err := pluginpkg.DevAuthorizationPath(home, authorization.PluginID)
				if err != nil {
					t.Fatal(err)
				}
				writePluginPackageFile(t, authorizationPath, string(mustJSON(authorization)))
				release, err := srv.beginPluginGenerationMutation("publish test development source", pluginGenerationMutationCatalog)
				if err != nil {
					t.Fatal(err)
				}
				development, err = pluginpkg.PublishDevGeneration(home, developmentSource, prepared, authorization)
				releaseErr := release()
				if err != nil || releaseErr != nil {
					t.Fatalf("publish development generation: %v / %v", err, releaseErr)
				}
				call("discover-dev", MethodExtensionCatalogRefresh, struct{}{})
				requireActiveRuntime(t, rt.PluginHost, development.ID)
				toolName := ""
				for _, tool := range rt.PluginHost.ToolDefinitions() {
					if registered, ok := rt.PluginHost.Tool(tool.Name); ok && registered.PluginID == development.ID {
						toolName = tool.Name
					}
				}
				result, err := rt.PluginHost.ExecuteTool(context.Background(), toolName, pluginhost.ToolExecuteInput{ThreadID: "fixture-call", TurnID: "fixture-turn", CallID: "before-deletion", CWD: rt.RootDir, Arguments: json.RawMessage(`{}`)})
				if err != nil || !strings.Contains(fmt.Sprint(result), "fixture time") {
					t.Fatalf("development plugin call: %+v %v", result, err)
				}
			}
			other := call("other-thread", MethodThreadStart, ThreadStartParams{})
			otherID := remarshal[ThreadStartResult](t, other["result"]).Thread.ID
			created := call("create", MethodThreadStart, ThreadStartParams{})
			id := remarshal[ThreadStartResult](t, created["result"]).Thread.ID
			call("start", MethodTurnStart, TurnStartParams{ThreadID: id, Prompt: "keep this accepted turn"})
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("accepted turn did not reach the synthetic provider")
			}
			owner := srv.thread(id)
			state := owner.state
			call("queue", MethodTurnQueue, TurnQueueParams{ThreadID: id, Prompt: "preserve queued input", ClientID: "reload-queued"})
			switch action {
			case "remove":
				call("remove", MethodPluginPackageRemove, PluginPackageRemoveParams{ID: "readable-plugin"})
			case "disable_deleted_source":
				call("disable", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{ID: development.SubjectID, Action: ExtensionPackageDisable})
				if err := os.Remove(developmentSource); err != nil {
					t.Fatal(err)
				}
				call("rediscover-deleted-source", MethodExtensionCatalogRefresh, struct{}{})
				record := pluginPackageRecord(t, srv.currentExtensionInventory(), development.ID)
				if record.PackageSource != "dev" || record.Enabled == nil || *record.Enabled {
					t.Fatalf("deleted source lost disabled development record: %+v", record)
				}
				for _, status := range rt.PluginHost.Statuses() {
					if status.ID == development.ID && status.State == pluginhost.StateActive {
						t.Fatal("disabled development plugin remained active after source deletion")
					}
				}
			default:
				call("install", MethodPluginPackageInstall, PluginPackageInstallParams{Path: source})
			}
			call("resume-other", MethodThreadResume, ThreadResumeParams{SessionID: otherID, ConversationHistory: true})
			current := testRuntimeState(t, rt)
			if current.Sessions == state.Sessions {
				t.Fatal("fixture did not replace the selected session authority")
			}
			before, err := loadChatMessages(state.Sessions, id)
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				response := call(fmt.Sprintf("resume-%d", attempt), MethodThreadResume, ThreadResumeParams{SessionID: id, ConversationHistory: true})
				resumed := remarshal[ThreadResumeResult](t, response["result"])
				if resumed.Thread.ID != id || resumed.Thread.Status != ThreadStatusInProgress || len(resumed.Thread.Turns) != 1 || resumed.Thread.Turns[0].Status != TurnStatusInProgress {
					t.Fatalf("resume lost accepted turn: %+v", resumed.Thread)
				}
				if len(resumed.PendingUserMessages) != 1 || resumed.PendingUserMessages[0].ID != "reload-queued" {
					t.Fatalf("resume lost queued input: %+v", resumed.PendingUserMessages)
				}
			}
			if srv.thread(id) != owner || owner.state != state {
				t.Fatal("read-only resume replaced the accepted execution owner")
			}
			after, err := loadChatMessages(state.Sessions, id)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("resume mutated durable history: %v", err)
			}
			lease, acquired, err := current.Sessions.TryAcquireThreadExecutionLease(rt.SessionDir, id)
			if lease != nil {
				defer lease.Release()
			}
			if err != nil || acquired {
				t.Fatalf("resume weakened accepted turn lease: acquired=%v err=%v", acquired, err)
			}
			unblock()
			waitForReloadTurnCompletions(t, out, id, 2)
			waitPluginGenerationWatchTest(t, func() bool { return !srv.hasQueuedUserWork(id) && !threadIsRunning(srv.thread(id)) })
			response := call("settled", MethodThreadResume, ThreadResumeParams{SessionID: id, ConversationHistory: true})
			settled := remarshal[ThreadResumeResult](t, response["result"])
			if len(settled.Thread.Turns) != 2 || len(settled.HeldUserMessages) != 0 {
				t.Fatalf("settlement lost history or queue acknowledgement: %+v", settled)
			}
			assertFakeClientRequestCount(t, client, 2)
		})
	}
}
