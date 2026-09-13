package appserver

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/plugins/notecompaction"
)

type toolLabelTestClient struct {
	tools []pluginhost.ToolRegistration
}

func (*toolLabelTestClient) ID() string { return "note-compaction" }
func (*toolLabelTestClient) Status() pluginhost.Status {
	return pluginhost.Status{State: pluginhost.StateActive}
}
func (*toolLabelTestClient) Close(context.Context) error            { return nil }
func (c *toolLabelTestClient) Tools() []pluginhost.ToolRegistration { return c.tools }
func (*toolLabelTestClient) ExecuteTool(context.Context, pluginhost.ToolExecuteParams) (pluginhost.ToolExecuteResult, error) {
	return pluginhost.ToolExecuteResult{}, nil
}

func TestPluginToolLabelsSurvivePersistenceAndRestoreLegacyCalls(t *testing.T) {
	// Exercise the bundled Go plugin's wire declaration through the host schema.
	raw, err := json.Marshal(notecompaction.Handler().Definition.Tools)
	if err != nil {
		t.Fatal(err)
	}
	client := &toolLabelTestClient{}
	if err := json.Unmarshal(raw, &client.tools); err != nil {
		t.Fatal(err)
	}
	host := pluginhost.New(client)
	name := host.ToolDefinitions()[0].Name
	registered, _ := host.Tool(name)
	label := registered.Registration.Display
	if label == nil || label.Label == "" || len(label.LabelTranslations) == 0 {
		t.Fatal("the bundled tool did not publish a localized name")
	}
	saved := label.Clone()
	saved.Label = "Saved name"
	saved.LabelTranslations["zh-CN"] = "历史名称"
	rt := newTestRuntime(t, &fakeClient{})
	rt.PluginHost = host
	sess, err := session.CreateWithMetadata(rt.SessionDir, "localized-tool-history", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []providers.ChatMessage{
		{Role: "user", Content: "inspect notes"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "legacy", Name: name, Arguments: `{"action":"list"}`},
			{ID: "saved", Name: name, Arguments: `{"action":"list"}`, Display: saved},
		}},
		{Role: "tool", ToolCallID: "legacy", Content: "[]"},
		{Role: "tool", ToolCallID: "saved", Content: "[]"},
		{Role: "assistant", Content: "done"},
	} {
		if _, err := appendChatMessage(rt.SessionDir, sess.ID, message); err != nil {
			t.Fatal(err)
		}
	}
	srv := New(rt, &lockedBuffer{})
	th, err := srv.loadPersistedThreadState(sess.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var calls []ThreadItem
	for _, item := range th.Turns[0].Items {
		if item.Type == ThreadItemToolCall {
			calls = append(calls, item)
		}
	}
	if len(calls) != 2 || calls[0].Name != name || !reflect.DeepEqual(calls[0].Display, label) || !reflect.DeepEqual(calls[1].Display, saved) {
		t.Fatalf("unexpected restored tool labels: %+v", calls)
	}
	assertLegacyUnchanged := func(history []providers.ChatMessage) {
		t.Helper()
		for _, message := range history {
			for _, call := range message.ToolCalls {
				if call.ID == "legacy" {
					if call.Name != name || call.Display != nil {
						t.Fatal("display restoration changed the original call")
					}
					return
				}
			}
		}
		t.Fatal("legacy call missing from history")
	}
	assertLegacyUnchanged(th.History)
	history, err := loadChatMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyUnchanged(history)
	rt.PluginHost = nil
	th, err = srv.loadPersistedThreadState(sess.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range th.Turns[0].Items {
		if item.SourceID == "saved" && reflect.DeepEqual(item.Display, saved) {
			return
		}
	}
	t.Fatal("saved tool labels required an installed plugin")
}
