package appserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

// Exercise the provider loop, real file tools, command execution, persisted
// history and client projection. The provider is scripted so failures isolate
// the runtime contract rather than model output quality or network availability.
func TestServerUnifiedEditingResumesHistoricalPatch(t *testing.T) {
	for _, model := range []string{"gpt-5-codex", "gpt-5.5", "claude-sonnet-4-5", "portable-coder"} {
		t.Run(model, func(t *testing.T) {
			calls := []providers.ToolCall{
				{ID: "read", Name: "read_file", Arguments: `{"path":"value.txt"}`},
				{ID: "missing", Name: "edit_file", Arguments: `{"path":"value.txt","old_text":"missing","new_text":"wrong"}`},
				{ID: "ambiguous", Name: "edit_file", Arguments: `{"path":"value.txt","old_text":"repeat","new_text":"wrong"}`},
				{ID: "edit", Name: "edit_file", Arguments: `{"path":"value.txt","old_text":"before","new_text":"after"}`},
				{ID: "write", Name: "write_file", Arguments: `{"path":"ready.txt","content":"ready\n","create_only":true}`},
				{ID: "verify", Name: "bash", Arguments: `{"command":"test \"$(cat value.txt)\" = \"$(printf 'after\\nrepeat\\nrepeat')\" && test \"$(cat ready.txt)\" = ready && printf EDIT_FLOW_OK","timeout_seconds":30}`},
			}
			client := &fakeClient{response: providersResponse("done")}
			for _, call := range calls {
				client.responses = append(client.responses, providers.ChatResponse{ToolCalls: []providers.ToolCall{call}})
			}
			rt := newTestRuntime(t, client)
			rt.Model, rt.StreamRunner.Model, rt.StreamRunner.APIModel = model, model, model
			kit, err := tools.New(rt.RootDir)
			require.NoError(t, err)
			kit.ConfigureSurfaceForProviderModel("custom", model, true)
			// Shell sandbox support is tested separately; run this fixture on every OS.
			kit.SetBoundary(tools.UnconfinedBoundary())
			rt.Toolkit = kit
			require.NoError(t, os.WriteFile(filepath.Join(rt.RootDir, "value.txt"), []byte("before\nrepeat\nrepeat\n"), 0600))
			var changed []string
			kit.SetOnFileChanged(func(path string) { changed = append(changed, filepath.Base(path)) })

			sess, err := session.CreateWithMetadata(rt.SessionDir, "historical-patch", rt.RootDir)
			require.NoError(t, err)
			oldArgs := `{"patchText":"*** Begin Patch\n*** Add File: value.txt\n+before\n+repeat\n+repeat\n*** End Patch"}`
			oldResult := `{"files":[{"path":"value.txt","action":"add","diff":"+before\n+repeat\n+repeat"}]}`
			history := []providers.ChatMessage{
				{Role: "user", Content: "Create value.txt"},
				{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "historical-call", Name: "apply_patch", Arguments: oldArgs}}},
				{Role: "tool", Name: "apply_patch", ToolCallID: "historical-call", Content: oldResult},
				{Role: "assistant", Content: "Created value.txt"},
			}
			require.NoError(t, rewriteChatHistory(rt.SessionDir, sess.ID, history))
			out := &lockedBuffer{}
			srv := New(rt, out)
			require.NoError(t, srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"resume","method":"thread/resume","params":{"session_id":%q}}`, sess.ID))))
			resumed := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "resume")["result"])
			require.Len(t, resumed.Thread.Turns, 1)
			var historicalItem *ThreadItem
			for _, item := range resumed.Thread.Turns[0].Items {
				if item.Name == "apply_patch" {
					copy := item
					historicalItem = &copy
				}
			}
			require.NotNil(t, historicalItem)
			require.JSONEq(t, oldArgs, historicalItem.Arguments)
			require.JSONEq(t, oldResult, historicalItem.Result)

			require.NoError(t, srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"Update value.txt and create ready.txt, then verify both files."}}`, sess.ID))))
			messages := waitForMethod(t, out, NotificationTurnCompleted)
			completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, messages, NotificationTurnCompleted)["params"])
			require.Equal(t, TurnStatusCompleted, completed.Turn.Status)
			results := map[string]ThreadItem{}
			for _, item := range completed.Turn.Items {
				if item.Type == ThreadItemToolCall {
					results[item.SourceID] = item
				}
			}
			require.Len(t, results, len(calls))
			require.Contains(t, results["missing"].Result, "old_text_not_found")
			require.Contains(t, results["ambiguous"].Result, "ambiguous_old_text")
			for _, id := range []string{"read", "edit", "write", "verify"} {
				require.Empty(t, results[id].Error, "%s: %+v", id, results[id])
				require.NotContains(t, results[id].Result, `"ok":false`)
			}
			require.Contains(t, results["edit"].Result, "diff")
			require.Contains(t, results["write"].Result, "new_file_sha")
			require.Contains(t, results["verify"].Result, "EDIT_FLOW_OK")
			require.ElementsMatch(t, []string{"value.txt", "ready.txt"}, changed)
			data, err := os.ReadFile(filepath.Join(rt.RootDir, "value.txt"))
			require.NoError(t, err)
			require.Equal(t, "after\nrepeat\nrepeat\n", string(data))

			client.mu.Lock()
			requests := append([]providers.ChatRequest(nil), client.requests...)
			client.mu.Unlock()
			require.Len(t, requests, len(calls)+1)
			for _, request := range requests {
				names := toolDefinitionNames(request.Tools)
				require.True(t, names["edit_file"] && names["write_file"])
				require.False(t, names["apply_patch"])
			}
			persisted, err := loadChatMessages(rt.SessionDir, sess.ID)
			require.NoError(t, err)
			var oldCall, oldReply bool
			for _, msg := range persisted {
				for _, call := range msg.ToolCalls {
					if call.ID == "historical-call" {
						require.Equal(t, oldArgs, call.Arguments)
						oldCall = true
					}
				}
				if msg.ToolCallID == "historical-call" {
					require.Equal(t, oldResult, msg.Content)
					oldReply = true
				}
			}
			require.True(t, oldCall && oldReply, "continuation must preserve historical patch records")
			// Log actual runtime outcomes for a repeatable go test -v acceptance artifact.
			for _, call := range calls {
				item := results[call.ID]
				t.Logf("%s %s error=%q result=%s", model, call.Name, item.Error, strings.TrimSpace(item.Result))
			}
		})
	}
}
