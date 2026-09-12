package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
	subagentplugin "github.com/blueberrycongee/wuu/plugins/subagent"
)

func TestIdentitySubagentPluginHelper(t *testing.T) {
	if os.Getenv("WUU_IDENTITY_SUBAGENT_HELPER") != "1" {
		return
	}
	if err := pluginapi.Serve(context.Background(), subagentplugin.Handler()); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

type identityPluginRouter struct {
	server *Server
	mu     sync.Mutex
	stored map[string]string
}

func (r *identityPluginRouter) RouteServiceCall(ctx context.Context, owner string, call pluginhost.ServiceCallParams) (json.RawMessage, *pluginhost.HostServiceError) {
	var value any = struct{}{}
	var err error
	switch call.Service {
	case pluginapi.HostServiceSessionCreate:
		var p pluginhost.SessionCreateParams
		err = json.Unmarshal(call.Params, &p)
		if err == nil {
			value, err = r.server.createPluginSession(ctx, owner, p)
		}
	case pluginapi.HostServiceSessionSend:
		var p pluginhost.SessionSendParams
		err = json.Unmarshal(call.Params, &p)
		if err == nil {
			value, err = r.server.sendPluginSession(ctx, owner, p)
		}
	case pluginapi.HostServiceSessionList:
		var p pluginhost.SessionListParams
		err = json.Unmarshal(call.Params, &p)
		if err == nil {
			value, err = r.server.listPluginSessions(ctx, owner, p)
		}
	case pluginapi.HostServiceSessionInspect:
		var p pluginhost.SessionInspectParams
		err = json.Unmarshal(call.Params, &p)
		if err == nil {
			value, err = r.server.inspectPluginSession(ctx, owner, p)
		}
	case pluginapi.HostServiceSessionCancel:
		var p pluginhost.SessionCancelParams
		err = json.Unmarshal(call.Params, &p)
		if err == nil {
			value, err = r.server.cancelPluginSession(ctx, owner, p)
		}
	case pluginapi.HostServiceStorageGet, pluginapi.HostServiceStorageSet, pluginapi.HostServiceStorageKeys, pluginapi.HostServiceStorageDelete:
		var p struct{ Key, Value string }
		err = json.Unmarshal(call.Params, &p)
		r.mu.Lock()
		switch call.Service {
		case pluginapi.HostServiceStorageGet:
			if stored, ok := r.stored[p.Key]; ok {
				value = pluginapi.StorageGetResult{Value: &stored}
			} else {
				value = pluginapi.StorageGetResult{}
			}
		case pluginapi.HostServiceStorageSet:
			r.stored[p.Key] = p.Value
		case pluginapi.HostServiceStorageDelete:
			delete(r.stored, p.Key)
		case pluginapi.HostServiceStorageKeys:
			keys := []string{}
			for key := range r.stored {
				keys = append(keys, key)
			}
			value = map[string]any{"keys": keys}
		}
		r.mu.Unlock()
	default:
		err = fmt.Errorf("unexpected service %s", call.Service)
	}
	if err != nil {
		return nil, &pluginhost.HostServiceError{Code: "test_host_error", Message: err.Error()}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, &pluginhost.HostServiceError{Code: "encode", Message: err.Error()}
	}
	return raw, nil
}

func TestIdentitySubagentReturnsToOriginalRoomAndMainHistory(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	router := &identityPluginRouter{server: f.server, stored: map[string]string{}}
	plugin, err := pluginhost.Start(ctx, pluginhost.ProcessConfig{ID: "subagent", Command: os.Args[0], Args: []string{"-test.run=^TestIdentitySubagentPluginHelper$"}, Env: map[string]string{"WUU_IDENTITY_SUBAGENT_HELPER": "1"}, PluginRoot: t.TempDir(), ProjectRoot: f.server.rt.RootDir, WuuHome: f.server.rt.WuuHome, Timeout: time.Second, ServiceRouter: router})
	if err != nil {
		t.Fatal(err)
	}
	f.server.rt.PluginHost = pluginhost.New(plugin)
	t.Cleanup(func() { _ = plugin.Close(context.Background()) })
	var main ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Investigate the first room's reconnect problem", RequestID: "main"}, &main)
	call := provider.next(t)
	toolName := ""
	for _, def := range call.request.Tools {
		if strings.Contains(def.Name, "spawn_agent") {
			toolName = def.Name
		}
	}
	if toolName == "" {
		t.Fatal("identity cannot discover the subagent plugin")
	}
	coordinatorModelTool(call, "spawn", toolName, map[string]any{"description": "Inspect transport", "prompt": "Inspect a bounded part of the reconnect problem", "run_in_background": true})
	continuation, child := splitCoordinatorCalls(t, provider, "spawn")
	continuation.response <- providers.ChatResponse{Content: "Investigating transport"}
	f.waitForCompletion(t)
	waitForThreadLeaseRelease(t, f.server.rt.SessionDir, main.Session.SessionRef)
	room2, err := f.server.channelService.CreateRoom(ctx, channels.CreateRoomParams{Kind: channels.RoomChannel, Name: "Other room", CreatedBy: "human-1", Members: []channels.RoomMember{{MemberType: channels.MemberAgent, MemberID: f.identity.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	var second ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: room2.ID, Prompt: "Handle the other room briefly", RequestID: "other"}, &second)
	other := provider.next(t)
	child.response <- providers.ChatResponse{Content: "CHILD EVIDENCE: stale transport cursor"}
	// The plugin completion is asynchronous; wait for its durable inbox delivery,
	// not an arbitrary sleep or a particular scheduling order.
	deadline := time.Now().Add(collaborationTestWaitTimeout)
	for {
		pending, e := f.server.channelService.PendingCollaborationRoomIDs(ctx, f.identity.ID)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, room := range pending {
			if room == f.room.ID {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child result did not reach the durable identity inbox")
		}
		time.Sleep(5 * time.Millisecond)
	}
	other.response <- providers.ChatResponse{Content: "Other room done"}
	f.waitForCompletion(t)
	returned := provider.next(t)
	text := collaborationRequestText(returned.request)
	if !strings.Contains(text, "CHILD EVIDENCE") || !strings.Contains(text, "Investigate the first room") {
		t.Fatal("child lost the main conversation context")
	}
	binding, err := f.server.channelService.LookupCollaborationSession(ctx, main.Session.SessionRef)
	if err != nil || binding.RoomID != f.room.ID {
		t.Fatalf("child result returned to the wrong room: %+v %v", binding, err)
	}
	returned.response <- providers.ChatResponse{Content: "Integrated the transport evidence"}
	f.waitForCompletion(t)
	bindings, err := f.server.channelService.ListAllCollaborationSessions(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("temporary child became another named conversation: %+v %v", bindings, err)
	}
}

func TestPluginChildInheritsParentModelWithFreshContext(t *testing.T) {
	f := newCollaborationRPCFixture(t)
	parent, call := f.create(t, "Parent-only private conversation")
	close(call.release)
	f.waitForCompletion(t)
	if _, err := session.SetRuntimeSelection(f.server.rt.SessionDir, parent.SessionRef, session.RuntimeSelection{Provider: "parent-provider", Model: "parent-model", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	child, err := f.server.createPluginSession(context.Background(), "subagent", pluginhost.SessionCreateParams{RequestID: "inherit", ParentSessionID: parent.SessionRef, Visibility: "plugin", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	metadata, found, err := session.Find(f.server.rt.SessionDir, child.SessionID)
	if err != nil || !found || metadata.Provider != "parent-provider" || metadata.Model != "parent-model" || metadata.Effort != "high" {
		t.Fatalf("child did not inherit selection: %+v %v", metadata, err)
	}
	records, err := session.LoadHistoryRecords(f.server.rt.SessionDir, child.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if strings.Contains(record.Content, "Parent-only private") {
			t.Fatal("fresh child copied parent chat history")
		}
	}
}
