package appserver

// Production Personal bundle, user-host RPC, enrollment and task grants are
// exercised with a deterministic model and no external platform credentials.
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	"github.com/blueberrycongee/wuu/internal/personalim"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	runtimepkg "github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/version"
)

type channelTaskModel struct {
	t *testing.T
}

func (m *channelTaskModel) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	all := ""
	for _, v := range req.Messages {
		all += "\n" + v.Content
	}
	seen := func(id string) bool {
		for _, v := range req.Messages {
			if v.Role == "tool" && v.ToolCallID == id {
				return true
			}
		}
		return false
	}
	call := func(id, suffix string, args any) (providers.ChatResponse, error) {
		for _, tool := range req.Tools {
			if strings.Contains(tool.Name, "_"+suffix+"_") {
				raw, _ := json.Marshal(args)
				return providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: id, Name: tool.Name, Arguments: string(raw)}}, StopReason: "tool_calls", FinishReason: providers.FinishReasonToolCalls}, nil
			}
		}
		names := []string{}
		for _, tool := range req.Tools {
			names = append(names, tool.Name)
		}
		m.t.Logf("fixture tool missing %s: %v", suffix, names)
		return providers.ChatResponse{}, fmt.Errorf("channel fixture tool unavailable: %s", suffix)
	}
	match := regexp.MustCompile(`Work on Personal task (task-[a-f0-9]+), requirements revision (\d+)`).FindStringSubmatch(all)
	if len(match) > 0 {
		rev, _ := strconv.Atoi(match[2])
		if rev == 1 {
			if !seen("channel-question") {
				return call("channel-question", "report_task", map[string]any{"task_id": match[1], "requirements_revision": rev, "state": "waiting_user", "progress": "CHANNEL_QUESTION: which color?"})
			}
			return providersResponse("Waiting for the owner answer."), nil
		}
		if !seen("channel-evidence") {
			return call("channel-evidence", "list_tasks", map[string]any{})
		}
		if !seen("channel-complete") {
			return call("channel-complete", "complete_task", map[string]any{"task_id": match[1], "requirements_revision": rev, "summary": "CHANNEL_RESULT: blue selected; task status was checked.", "evidence": []any{map[string]any{"tool_call_id": "channel-evidence", "description": "Read current task status"}}})
		}
		return providersResponse("Worker finished."), nil
	}
	if strings.Contains(all, "has a verified submission") {
		return providersResponse("CHANNEL_RESULT: blue selected."), nil
	}
	if strings.Contains(all, "CHANNEL_ANSWER blue") {
		if !seen("channel-list") {
			return call("channel-list", "list_tasks", map[string]any{})
		}
		if !seen("channel-resume") {
			for _, v := range req.Messages {
				if v.Role == "tool" && v.ToolCallID == "channel-list" {
					var listed struct {
						Tasks []struct {
							ID       string `json:"task_id"`
							Revision int    `json:"revision"`
						} `json:"tasks"`
					}
					if err := json.Unmarshal([]byte(v.Content), &listed); err != nil || len(listed.Tasks) != 1 {
						return providers.ChatResponse{}, fmt.Errorf("task lookup result: %s", v.Content)
					}
					return call("channel-resume", "resume_task", map[string]any{"task_id": listed.Tasks[0].ID, "expected_revision": listed.Tasks[0].Revision, "answer": "blue"})
				}
			}
		}
		return providersResponse("Continuing the same task."), nil
	}
	if !seen("channel-create") {
		return call("channel-create", "create_task", map[string]any{"request_id": "channel-task", "title": "Channel task", "goal": "Ask which color, then inspect current task status and report the answer."})
	}
	if strings.Contains(all, "needs the user's answer") {
		return providersResponse("CHANNEL_QUESTION: which color?"), nil
	}
	return providersResponse("Background task started."), nil
}

func TestPersonalChannelEnrolledTaskQuestionResumeE2E(t *testing.T) {
	if os.Getenv("WUU_MESSAGING_E2E") != "1" {
		t.Skip("set WUU_MESSAGING_E2E=1 with built bundled plugins")
	}
	previousVersion := version.Version
	version.Version = "2026.11.1"
	t.Cleanup(func() { version.Version = previousVersion })
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "wuu"))
	for _, key := range []string{"HOME", "USERPROFILE", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(key, filepath.Join(home, key))
	}
	model := &channelTaskModel{t: t}
	endpoint := newTestOpenAIEndpoint(t, "channel-fixture", "channel-title", model.Chat)
	cfg := config.Config{DefaultProvider: "channel-fixture", Agent: config.AgentConfig{ToolLoading: config.ToolLoadingFlat}, Providers: map[string]config.ProviderConfig{"channel-fixture": {Type: "openai-compatible", BaseURL: endpoint.URL + "/v1", APIKey: "synthetic-unused", Model: "fixture"}}}
	cfg.Agent.ModelRoles.Title = config.ModelRoleConfig{Provider: "channel-fixture", Model: "channel-title"}
	cfgPath := filepath.Join(home, "config.json")
	encoded, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	bindings := NewRuntimeBindings()
	credentials := runtimepkg.NativeProviderFactory{ID: credentialstore.OSModule, DefaultEnabled: true, Build: func(runtimepkg.NativeProviderContext) (pluginhost.Client, error) {
		return credentialstore.NewService(credentialstore.OSModule, func() (credentialstore.Store, error) {
			return credentialstore.NewFileStore(filepath.Join(home, "credentials.json")), nil
		}), nil
	}}
	rt, err := runtimepkg.NewSession(runtimepkg.Options{RootDir: filepath.Join(home, "unused"), HomeDir: home, ConfigPath: cfgPath, ConfigLoadMode: runtimepkg.ConfigLoadFile, Config: cfg, Overrides: runtimepkg.ConfigOverrides{CompositionProfile: "personal"}, UserSpaceID: "local", ProductDomain: "personal", NonInteractive: true, NativeProviders: append(bindings.NativeProviders(), credentials), MCPOAuth: mcp.NewOAuthManager(credentialstore.NewFileStore(filepath.Join(home, "mcp.json")), nil)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Cleanup()
	host, err := NewUserHost(rt, personalhost.Options{}, WithRuntimeBindings(bindings))
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	state, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	identity := principal.Identity{Kind: "user", Issuer: "wuu:im:slack", Tenant: "T1", Subject: "U1"}
	enrollment := executionauth.OwnerEnrollment{ID: "channel-e2e", Principal: identity, UserSpaceID: "local", ProductDomain: "personal", ConversationID: "D1"}
	raw, _ := json.Marshal(enrollment)
	admission := pluginhost.ServiceAdmission{Version: 1, Principal: principal.LocalOwner().Identity(), Authority: "local-installation", Action: "execution/owner/enroll-owner", InputDigest: executionauth.RawRequestDigest(raw)}
	if _, e := state.Host.CallService(pluginhost.WithServiceAdmission(ctx, executionauth.ConsumerModule, executionauth.ServiceName, "enroll-owner", admission), executionauth.ConsumerModule, executionauth.ServiceName, "enroll-owner", raw); e != nil {
		t.Fatal(e)
	}
	owner, err := principal.EnrolledOwner(identity, enrollment.ID, func() bool { return ctx.Err() == nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewPersonalChannelClient(ctx, host, owner, host.runtime.ExtensionSnapshot().PluginHost.ServiceRegistry())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { client.Close() }()
	invoke := func(method string, input any) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(input)
		var out json.RawMessage
		var e error
		if method == "agents.send" {
			var native struct {
				RequestID string `json:"request_id"`
				Prompt    string `json:"prompt"`
			}
			json.Unmarshal(raw, &native)
			source := personalim.Incoming{Platform: "slack", TenantID: "T1", SenderID: "U1", ConversationID: "D1", MessageID: native.RequestID, Text: native.Prompt}
			var fields map[string]any
			json.Unmarshal(raw, &fields)
			fields["request_id"] = fmt.Sprintf("im-%x", sha256.Sum256([]byte("slack\x00T1\x00D1\x00"+native.RequestID)))
			fields["channel_source"] = source
			raw, _ = json.Marshal(fields)
			request, _ := json.Marshal(PersonalChannelRequest{Binding: personalim.Binding{Platform: "slack", EnrollmentID: enrollment.ID, TenantID: "T1", SenderID: "U1", ConversationID: "D1"}, Method: method, Input: raw})
			if _, denied := host.HandlePersonalChannel(ctx, "unselected-adapter", request); denied == nil {
				t.Fatal("unselected adapter admitted native input")
			}
			out, e = host.HandlePersonalChannel(ctx, PersonalMessagingModule, request)
		} else {
			out, e = client.Call(ctx, method, raw)
		}
		if e != nil {
			t.Fatalf("%s: %v", method, e)
		}
		return out
	}
	var setup struct {
		Agent struct {
			ID      string `json:"agent_id"`
			Session string `json:"main_session_id"`
		} `json:"agent"`
	}
	if err = json.Unmarshal(invoke("agents.ensure_default", map[string]any{}), &setup); err != nil {
		t.Fatal(err)
	}
	send := map[string]any{"agent_id": setup.Agent.ID, "request_id": "slack-native-1", "prompt": "CHANNEL_START: start the background task and ask me which color."}
	invoke("agents.send", send)
	// Closing the short transport connection must not revoke the enrollment that
	// the accepted user turn carries into root task grant issuance.
	client.Close()
	client, err = NewPersonalChannelClient(ctx, host, owner, host.runtime.ExtensionSnapshot().PluginHost.ServiceRegistry())
	if err != nil {
		t.Fatal(err)
	}
	invoke("agents.send", send)
	var task struct {
		ID       string `json:"task_id"`
		Revision int    `json:"revision"`
		State    string `json:"state"`
		Waiting  string `json:"waiting_reason"`
		Delivery struct {
			Status string `json:"status"`
		} `json:"delivery"`
	}
	wait := func(completed bool) {
		t.Helper()
		for ctx.Err() == nil {
			var list struct {
				Tasks []json.RawMessage `json:"tasks"`
			}
			json.Unmarshal(invoke("tasks.list", map[string]any{}), &list)
			if len(list.Tasks) > 1 {
				t.Fatal("duplicate input created replacement tasks")
			}
			if len(list.Tasks) == 1 {
				task.ID, task.State, task.Waiting, task.Delivery.Status = "", "", "", ""
				task.Revision = 0
				json.Unmarshal(list.Tasks[0], &task)
				if (!completed && task.Waiting == "user" && task.Delivery.Status == "delivered") || (completed && task.State == "completed" && task.Delivery.Status == "delivered") {
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("task not settled: %+v", task)
	}
	wait(false)
	original := task.ID
	invoke("agents.send", map[string]any{"agent_id": setup.Agent.ID, "request_id": "slack-native-2", "prompt": "CHANNEL_ANSWER blue"})
	wait(true)
	if task.ID != original {
		t.Fatal("answer replaced the task")
	}
	history, err := rt.Sessions.ReadHistoryPage(setup.Agent.Session, 1, 200)
	if err != nil {
		t.Fatal(err)
	}
	questions, results, originalInputs := 0, 0, 0
	for _, record := range history.Records {
		if record.Role == "assistant" {
			if strings.Contains(record.Content, "CHANNEL_QUESTION") {
				questions++
			}
			if strings.Contains(record.Content, "CHANNEL_RESULT") {
				results++
			}
		}
		if record.Role == "user" && strings.HasPrefix(record.Content, "CHANNEL_START:") {
			originalInputs++
		}
	}
	if questions != 1 || results != 1 || originalInputs != 1 {
		t.Fatalf("question/result/input counts=%d/%d/%d", questions, results, originalInputs)
	}
	t.Log("PASS native owner input → enrolled root grant → worker question → same-task resume → one asynchronous result; duplicate input and selected gateway preserved identity")
}
