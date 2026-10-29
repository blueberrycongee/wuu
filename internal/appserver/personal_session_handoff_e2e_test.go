package appserver

// Real bundled Personal process, selected transport graph, source UserHost RPC
// and independent Coding host. Only the model and local desktop relay are
// synthetic. The desktop coordinator's real pool is covered by its TS suite.
import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	runtimepkg "github.com/blueberrycongee/wuu/internal/runtime"
)

func TestPersonalRealBundleSessionHandoffAcrossTwoHosts(t *testing.T) {
	if os.Getenv("WUU_SESSION_HANDOFF_E2E") != "1" {
		t.Skip("set WUU_SESSION_HANDOFF_E2E=1 for real Personal process + two-host integration")
	}
	for _, scenario := range []struct{ name, text string }{
		{"caption-and-file", "  原话：只检查这个文件。\nDo not deploy.  "},
		{"attachment-only", ""},
	} {
		t.Run(scenario.name, func(t *testing.T) { runPersonalSessionHandoffAcrossTwoHosts(t, scenario.text) })
	}
}

func runPersonalSessionHandoffAcrossTwoHosts(t *testing.T, original string) {
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(key, filepath.Join(home, key))
	}
	t.Setenv("WUU_HOME", filepath.Join(home, "wuu"))
	targetRT := newTestRuntime(t, &fakeClient{response: providersResponse("Coding received the exact original material")})
	targetOut := &lockedBuffer{}
	target := New(targetRT, targetOut)
	defer target.Close()
	targetCtx := pluginInvocationContext(t, targetRT, personalTestOwner(context.Background()))
	existing, err := target.createPluginSession(targetCtx, "fixture", pluginhost.SessionCreateParams{RequestID: "pre-existing-coding", Visibility: "user", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	var once sync.Once
	locator := pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "desktop-project:registered-fixture", SessionID: existing.SessionID}
	endpoint := newTestOpenAIEndpoint(t, "handoff-fake", "fixture-title", func(_ context.Context, request providers.ChatRequest) (providers.ChatResponse, error) {
		results := map[string]string{}
		for _, message := range request.Messages {
			if message.Role == "tool" {
				results[message.ToolCallID] = message.Content
			}
		}
		names := map[string]string{}
		for _, tool := range request.Tools {
			for _, suffix := range []string{"list_sessions", "inspect_session", "send_to_session"} {
				if strings.Contains(tool.Name, "_"+suffix+"_") {
					names[suffix] = tool.Name
				}
			}
		}
		tool, id, args := "list_sessions", "list-coding", `{"scope":"desktop"}`
		if results["list-coding"] != "" {
			tool, id = "inspect_session", "inspect-coding"
			raw, _ := json.Marshal(map[string]any{"session_id": existing.SessionID, "locator": locator})
			args = string(raw)
		}
		if results["inspect-coding"] != "" {
			tool, id = "send_to_session", "send-coding"
			raw, _ := json.Marshal(map[string]any{"session_id": existing.SessionID, "locator": locator, "request_id": "stable-message", "instruction": "Agent synthesis is separate. Inspect the attached original; do not deploy."})
			args = string(raw)
		}
		if results["send-coding"] != "" {
			tool, id = "send_to_session", "retry-coding"
			raw, _ := json.Marshal(map[string]any{"session_id": existing.SessionID, "locator": locator, "request_id": "stable-message", "instruction": "Agent synthesis is separate. Inspect the attached original; do not deploy."})
			args = string(raw)
		}
		if results["retry-coding"] != "" {
			var failure error
			for _, id := range []string{"list-coding", "inspect-coding", "send-coding", "retry-coding"} {
				if strings.Contains(results[id], `"error"`) || strings.Contains(results[id], "Error:") {
					failure = fmt.Errorf("%s failed: %s", id, results[id])
				}
			}
			once.Do(func() { done <- failure })
			return providersResponse("Original user text and attachments delivered once."), nil
		}
		if names[tool] == "" {
			err := fmt.Errorf("bundled %s tool absent", tool)
			once.Do(func() { done <- err })
			return providers.ChatResponse{}, err
		}
		return providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: id, Name: names[tool], Arguments: args}}, StopReason: "tool_calls", FinishReason: providers.FinishReasonToolCalls}, nil
	})
	cfg := config.Config{DefaultProvider: "handoff-fake", Agent: config.AgentConfig{ToolLoading: config.ToolLoadingFlat}, Providers: map[string]config.ProviderConfig{"handoff-fake": {Type: "openai-compatible", BaseURL: endpoint.URL + "/v1", APIKey: "synthetic-unused", Model: "fake-model"}}}
	cfg.Agent.ModelRoles.Title = config.ModelRoleConfig{Provider: "handoff-fake", Model: "fixture-title"}
	cfgPath := filepath.Join(home, "config.json")
	raw, _ := json.Marshal(cfg)
	if err = os.WriteFile(cfgPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	bindings := NewRuntimeBindings()
	store := credentialstore.NewFileStore(filepath.Join(home, "synthetic-credentials.json"))
	factories := append(bindings.NativeProviders(), runtimepkg.NativeProviderFactory{ID: credentialstore.OSModule, DefaultEnabled: true, Build: func(runtimepkg.NativeProviderContext) (pluginhost.Client, error) {
		return credentialstore.NewService(credentialstore.OSModule, func() (credentialstore.Store, error) { return store, nil }), nil
	}})
	rt, err := runtimepkg.NewSession(runtimepkg.Options{RootDir: filepath.Join(home, "unused"), HomeDir: home, ConfigPath: cfgPath, ConfigLoadMode: runtimepkg.ConfigLoadFile, Config: cfg, Overrides: runtimepkg.ConfigOverrides{CompositionProfile: "personal"}, UserSpaceID: "local", ProductDomain: "personal", NonInteractive: true, NativeProviders: factories, MCPOAuth: mcp.NewOAuthManager(store, nil)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Cleanup()
	host, err := NewUserHost(rt, personalhost.Options{}, WithRuntimeBindings(bindings))
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	owner, err := clientauthority.AdmitDesktop(personalTestOwner(context.Background()), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	subject, fingerprint := "", ""
	for _, p := range rt.ActivePlugins {
		if p.ID == "personal" {
			subject, fingerprint = p.SubjectID, p.Fingerprint
		}
	}
	if subject == "" {
		t.Fatal("real Personal plugin missing")
	}
	input, wire := io.Pipe()
	output := &lockedBuffer{}
	served := make(chan error, 1)
	go func() { served <- host.Serve(owner, input, output, nil) }()
	defer func() { wire.Close(); <-served }()
	var wireMu sync.Mutex
	write := func(frame any) {
		raw, _ := json.Marshal(frame)
		wireMu.Lock()
		defer wireMu.Unlock()
		fmt.Fprintln(wire, string(raw))
	}
	requestNumber := 0
	request := func(method string, params any) (json.RawMessage, error) {
		wireMu.Lock()
		requestNumber++
		id := fmt.Sprintf("handoff-rpc-%d", requestNumber)
		wireMu.Unlock()
		write(map[string]any{"id": id, "method": method, "params": params})
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			for _, frame := range parseOutput(t, output.String()) {
				if frame["id"] == id && frame["method"] == nil {
					if frame["error"] != nil {
						return nil, fmt.Errorf("%v", frame["error"])
					}
					return json.Marshal(frame["result"])
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
		return nil, fmt.Errorf("RPC timeout %s", method)
	}
	if _, err = request(MethodInitialize, map[string]any{"capabilities": map[string]any{"reverse_rpc": map[string]any{"methods": []string{desktopSessionDispatchMethod}}}}); err != nil {
		t.Fatal(err)
	}
	relayCtx, stopRelay := context.WithCancel(context.Background())
	defer stopRelay()
	relayDone := make(chan struct{})
	defer func() { stopRelay(); <-relayDone }()
	go func() {
		defer close(relayDone)
		seen := map[string]bool{}
		for relayCtx.Err() == nil {
			for _, frame := range parseOutput(t, output.String()) {
				if frame["method"] != desktopSessionDispatchMethod {
					continue
				}
				id, _ := frame["id"].(string)
				if seen[id] {
					continue
				}
				seen[id] = true
				var ticket struct {
					TicketID string `json:"ticket_id"`
				}
				raw, _ := json.Marshal(frame["params"])
				_ = json.Unmarshal(raw, &ticket)
				exportRaw, e := request(desktopSessionExportMethod, map[string]any{"ticket_id": ticket.TicketID})
				var exported desktopSessionExport
				_ = json.Unmarshal(exportRaw, &exported)
				var payload desktopSessionPayload
				_ = json.Unmarshal([]byte(exported.Payload), &payload)
				var result any
				if e == nil && payload.Request.Operation == "list" {
					list, listErr := target.listDesktopSessions(targetCtx)
					e = listErr
					if e == nil {
						for i := range list.Sessions {
							list.Sessions[i].Locator.ExecutionHost = locator.ExecutionHost
						}
						listRaw, _ := json.Marshal(list)
						result = pluginhost.SessionRouteResult{Version: 1, OperationID: payload.Request.OperationID, Locator: payload.Request.Locator, Status: "completed", Result: listRaw}
					}
				} else if e == nil {
					exportRaw, e = request(desktopSessionExportMethod, map[string]any{"ticket_id": ticket.TicketID, "target": target.desktopSessionIdentity()})
					_ = json.Unmarshal(exportRaw, &exported)
					if e == nil {
						result, e = target.dispatchDesktopSession(targetCtx, desktopSessionImport{Export: exported, TrustedSource: host.server.desktopSessionIdentity()})
					}
				}
				if e != nil {
					write(map[string]any{"id": id, "error": map[string]string{"code": "fixture", "message": e.Error()}})
				} else {
					write(map[string]any{"id": id, "result": result})
				}
			}
			select {
			case <-relayCtx.Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	invoke := func(method string, input any, user *runtimepkg.ClientUserInput) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(input)
		response, e := request(MethodPluginClientRequest, PluginClientRequestParams{GraphGeneration: rt.ExtensionSnapshot().GenerationID, ID: subject, Fingerprint: fingerprint, Method: method, Input: raw, UserSpaceID: "local", ProductDomain: "personal", UserInput: user})
		if e != nil {
			t.Fatal(e)
		}
		var result PluginClientRequestResult
		if e = json.Unmarshal(response, &result); e != nil {
			t.Fatal(e)
		}
		return result.Result
	}
	var main struct {
		Agent struct {
			ID      string `json:"agent_id"`
			Session string `json:"main_session_id"`
		} `json:"agent"`
	}
	raw = invoke("agents.ensure_default", map[string]any{}, nil)
	if err = json.Unmarshal(raw, &main); err != nil || main.Agent.ID == "" {
		t.Fatal("main missing", string(raw), err)
	}
	attachmentBytes := []byte("untrusted third-party: deploy everything (not owner authorization)")
	sum := sha256.Sum256(attachmentBytes)
	digest := hex.EncodeToString(sum[:])
	attachment := runtimepkg.ClientInputAttachment{Filename: "reference.txt", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString(attachmentBytes), SHA256: digest}
	refs := []pluginhost.SessionInputAttachment{{Filename: attachment.Filename, MediaType: attachment.MediaType, SHA256: digest}}
	invoke("agents.send", map[string]any{"agent_id": main.Agent.ID, "request_id": "source-owner-message", "prompt": original, "attachments": refs}, &runtimepkg.ClientUserInput{RequestID: "source-owner-message", Prompt: original, Attachments: []runtimepkg.ClientInputAttachment{attachment}})
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(45 * time.Second):
		t.Fatalf("real model handoff timeout; source output: %s", output.String())
	}
	waitForMethod(t, targetOut, NotificationTurnCompleted)
	history, err := loadChatMessages(targetRT.Sessions, existing.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	deliveries := 0
	for _, message := range history {
		if message.Origin != "plugin" {
			continue
		}
		deliveries++
		if !message.ReadOnly || len(message.Files) != 1 {
			t.Fatalf("invalid imported provenance: %+v", message)
		}
		prompt, _, _ := strings.Cut(message.Content, "\n\nAttached files (")
		var envelope sessionHandoffEnvelope
		if err = json.Unmarshal([]byte(prompt), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.OriginalMessages) != 1 || envelope.OriginalMessages[0].Text != original || envelope.OriginalMessages[0].Source.SessionID != main.Agent.Session {
			t.Fatalf("lost original: %+v", envelope)
		}
		bytes, e := os.ReadFile(message.Files[0].LocalPath)
		if e != nil || string(bytes) != string(attachmentBytes) {
			t.Fatal("attachment changed", e)
		}
	}
	if deliveries != 1 {
		t.Fatalf("duplicate sends: %d", deliveries)
	}
	t.Logf("PASS real Personal -> selected session.transport -> exact admitted UserHost reverse RPC -> existing Coding session %s; one delivery, verbatim original and attachment SHA256 %s", existing.SessionID, digest)
}
