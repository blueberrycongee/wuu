package appserver

// Full production composition: runtime, bundled Personal process, host services,
// real owner admission and durable connector broker. Only the model and remote
// HTTP/store are synthetic; no external account, OAuth grant or email is used.
import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	runtimepkg "github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type connectorChainModel struct {
	background bool
	done       chan string
	once       sync.Once
	faults     chan string
}

func (c *connectorChainModel) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	if c.background {
		return c.backgroundChat(req)
	}

	for _, m := range req.Messages {
		if m.Role == "tool" && m.ToolCallID == "connector-chain-write" {
			c.once.Do(func() { c.done <- m.Content })
			return providersResponse("Synthetic connector receipt returned to the original main conversation."), nil
		}
	}
	name := ""
	for _, tool := range req.Tools {
		properties, _ := tool.InputSchema["properties"].(map[string]any)
		if strings.Contains(tool.Name, "personal_connector") && properties["connection_id"] != nil && properties["operation"] != nil && properties["arguments"] != nil && properties["request_id"] != nil {
			name = tool.Name
		}
		if strings.Contains(tool.Name, "connector_decide") {
			return providers.ChatResponse{}, fmt.Errorf("model exposed owner-only confirmation")
		}
	}
	if name == "" {
		names := []string{}
		for _, tool := range req.Tools {
			names = append(names, tool.Name)
		}
		err := fmt.Errorf("real bundled connector tool absent from admitted model tools: %v", names)
		select {
		case c.faults <- err.Error():
		default:
		}
		return providers.ChatResponse{}, err
	}
	return providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: "connector-chain-write", Name: name, Arguments: `{"connection_id":"chain-gmail","operation":"gmail.send","request_id":"same-exact-write","arguments":{"to":["synthetic@example.invalid"],"subject":"Synthetic chain","body":"No actual email is sent."}}`}}, StopReason: "tool_calls", FinishReason: providers.FinishReasonToolCalls}, nil
}

type connectorChainHTTP func(*http.Request) (*http.Response, error)

func (f connectorChainHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPersonalConnectorRealBundleOwnerReceiptChain(t *testing.T) {
	if os.Getenv("WUU_CONNECTOR_CHAIN_E2E") != "1" {
		t.Skip("set WUU_CONNECTOR_CHAIN_E2E=1 for real bundled process integration")
	}
	for _, scenario := range []string{"approve", "reject", "cancel", "unknown", "cold_pending", "background"} {
		t.Run(scenario, func(t *testing.T) { connectorChain(t, scenario) })
	}
}
func connectorChain(t *testing.T, scenario string) {
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(key, filepath.Join(home, key))
	}
	t.Setenv("WUU_HOME", filepath.Join(home, "wuu"))
	cfg := config.Config{DefaultProvider: "connector-fake", Agent: config.AgentConfig{ToolLoading: config.ToolLoadingFlat}, Providers: map[string]config.ProviderConfig{"connector-fake": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:1/v1", APIKey: "synthetic-unused", Model: "fake-model"}}}
	cfgPath := filepath.Join(home, "config.json")
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	store := credentialstore.NewFileStore(filepath.Join(home, "synthetic-secure-store.json"))
	var effects atomic.Int32
	transport := &http.Client{Transport: connectorChainHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "gmail.googleapis.com" || r.URL.Path != "/gmail/v1/users/me/messages/send" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic-connector-secret" {
			return nil, fmt.Errorf("fixture rejected unexpected provider request")
		}
		effects.Add(1)
		status := 200
		body := `{"id":"fixture-message","threadId":"fixture-thread"}`
		if scenario == "unknown" {
			status = 503
			body = `{"error":"synthetic unknown; secret must not leak"}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	newRuntime := func() (*runtimepkg.Session, *UserHost, *personalconnectors.Broker, *connectorChainModel) {
		rt, e := runtimepkg.NewSession(runtimepkg.Options{RootDir: filepath.Join(home, "unused"), HomeDir: home, ConfigPath: cfgPath, ConfigLoadMode: runtimepkg.ConfigLoadFile, Config: cfg, UserSpaceID: "local", ProductDomain: "personal", NonInteractive: true, MCPOAuth: mcp.NewOAuthManager(credentialstore.NewFileStore(filepath.Join(home, "mcp-fixture.json")), nil)})
		if e != nil {
			t.Fatal(e)
		}
		model := &connectorChainModel{done: make(chan string, 1), faults: make(chan string, 1), background: scenario == "background"}
		rt.StreamRunner.Client = providers.AdaptStreamClient(model)
		rt.WorkerClient = providers.AdaptStreamClient(model)
		rt.TitleClient = &fakeClient{response: providersResponse("Connector test")}
		broker := personalconnectors.New(personalconnectors.Options{Store: store, HTTPClient: transport})
		host, e := NewUserHost(rt, personalhost.Options{Connectors: broker})
		if e != nil {
			rt.Cleanup()
			t.Fatal(e)
		}
		return rt, host, broker, model
	}
	rt, host, broker, model := newRuntime()
	defer func() { host.Close(); rt.Cleanup() }()
	if err := broker.RegisterVerified(context.Background(), personalconnectors.Actor{ProductDomain: "personal", UserSpaceID: "local", SessionID: "trusted-test-setup"}, personalconnectors.Grant{Connection: personalconnectors.Connection{ID: "chain-gmail", Provider: "gmail", AccountID: "me", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/gmail.send"}, Resources: []string{"me"}}, AccessToken: "synthetic-connector-secret", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	owner, err := clientauthority.AdmitDesktop(context.Background(), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
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
		t.Fatal("real bundled Personal absent")
	}
	incoming, wire := io.Pipe()
	output := &lockedBuffer{}
	served := make(chan error, 1)
	go func() { served <- host.Serve(owner, incoming, output, nil) }()
	wireOpen := true
	defer func() {
		if wireOpen {
			wire.Close()
			<-served
		}
	}()
	number := 0
	invoke := func(method string, input any) (json.RawMessage, error) {
		number++
		id := fmt.Sprintf("connector-chain-%d", number)
		raw, _ := json.Marshal(input)
		params := PluginClientRequestParams{ID: subject, Fingerprint: fingerprint, Method: method, Input: raw, UserSpaceID: "local", ProductDomain: "personal"}
		if method == "agents.send" {
			var in struct {
				RequestID string `json:"request_id"`
				Prompt    string `json:"prompt"`
			}
			json.Unmarshal(raw, &in)
			params.UserInput = &runtimepkg.ClientUserInput{RequestID: in.RequestID, Prompt: in.Prompt}
		}
		frame, _ := json.Marshal(map[string]any{"id": id, "method": MethodPluginClientRequest, "params": params})
		if _, e := fmt.Fprintln(wire, string(frame)); e != nil {
			return nil, e
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			for _, message := range parseOutput(t, output.String()) {
				if message["id"] == id && message["method"] == nil {
					if message["error"] != nil {
						return nil, fmt.Errorf("%v", message["error"])
					}
					return remarshal[PluginClientRequestResult](t, message["result"]).Result, nil
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		return nil, fmt.Errorf("wire timeout: %s", method)
	}
	must := func(method string, input any) json.RawMessage {
		t.Helper()
		v, e := invoke(method, input)
		if e != nil {
			t.Fatalf("%s: %v", method, e)
		}
		return v
	}
	setup := must("agents.ensure_default", map[string]any{})
	var main struct {
		Agent struct {
			Revision int    `json:"revision"`
			ID       string `json:"agent_id"`
			Session  string `json:"main_session_id"`
		} `json:"agent"`
	}
	if e := json.Unmarshal(setup, &main); e != nil || main.Agent.ID == "" {
		t.Fatalf("main=%s err=%v", setup, e)
	}
	must("agents.send", map[string]any{"agent_id": main.Agent.ID, "request_id": "original-owner-turn", "prompt": "Send the exact synthetic test message to synthetic@example.invalid, subject Synthetic chain and body No actual email is sent. Ask for confirmation first."})
	var pending personalconnectors.Result
	deadline := time.Now().Add(35 * time.Second)
	actor := personalconnectors.Actor{ProductDomain: "personal", UserSpaceID: "local", SessionID: main.Agent.Session}
	if scenario == "background" {
		actor.SessionID = "owner-ui:fixture-inspection"
	}
	for time.Now().Before(deadline) {
		rows, e := broker.Actions(context.Background(), actor)
		if e != nil {
			t.Fatal(e)
		}
		if len(rows) > 0 {
			pending = rows[0]
			break
		}
		select {
		case fault := <-model.faults:
			t.Fatal(fault)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pending.State != "awaiting_confirmation" || effects.Load() != 0 {
		t.Fatalf("before owner: action=%+v effects=%d output=%s", pending, effects.Load(), output.String())
	}
	if scenario == "cold_pending" {
		wire.Close()
		<-served
		wireOpen = false
		host.Close()
		rt.Cleanup()
		rt, host, broker, model = newRuntime()
		owner, err = clientauthority.AdmitDesktop(context.Background(), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range rt.ActivePlugins {
			if p.ID == "personal" {
				subject, fingerprint = p.SubjectID, p.Fingerprint
			}
		}
		incoming, wire = io.Pipe()
		output = &lockedBuffer{}
		served = make(chan error, 1)
		wireOpen = true
		go func() { served <- host.Serve(owner, incoming, output, nil) }()
		rows, e := broker.Actions(context.Background(), actor)
		if e != nil || len(rows) != 1 || rows[0].State != "awaiting_confirmation" || rows[0].ActionID != pending.ActionID || rows[0].Digest != pending.Digest {
			t.Fatalf("cold pending lost identity: %+v error=%v", rows, e)
		}
		t.Log("cold pending retains exact action, digest and user source; no automatic model continuation is asserted")
	}
	taskID := ""
	if scenario == "background" {
		raw := must("tasks.list", map[string]any{})
		var listed struct {
			Tasks []struct {
				ID        string `json:"task_id"`
				Execution struct {
					SessionID string `json:"session_id"`
				} `json:"execution"`
			} `json:"tasks"`
		}
		if e := json.Unmarshal(raw, &listed); e != nil || len(listed.Tasks) != 1 {
			t.Fatalf("background task=%s error=%v", raw, e)
		}
		taskID = listed.Tasks[0].ID
		actor.SessionID = listed.Tasks[0].Execution.SessionID
	}
	decision := map[string]any{"action_id": pending.ActionID, "digest": pending.Digest, "approve": scenario != "reject"}
	bad, _ := json.Marshal(decision)
	if _, e := host.personal.Call(context.Background(), "connectors.decide", bad, ""); e == nil {
		t.Fatal("unadmitted caller approved effect")
	}
	if scenario == "cancel" {
		must("agents.pause", map[string]any{"agent_id": main.Agent.ID, "expected_revision": main.Agent.Revision})
		result := must("connectors.decide", decision)
		if !strings.Contains(string(result), `"ok":false`) {
			t.Fatalf("cancelled authority unexpectedly accepted: %s", result)
		}
		if effects.Load() != 0 {
			t.Fatal("cancelled pending action mutated provider")
		}
		t.Log("PASS cancelled main invalidates exact pending connector authority")
		return
	}
	decided := must("connectors.decide", decision)
	if strings.Contains(string(decided), "synthetic-connector-secret") {
		t.Fatal("host credential escaped")
	}
	if scenario != "cold_pending" {
		select {
		case receipt := <-model.done:
			expected := "completed"
			if scenario == "reject" {
				expected = "rejected"
			}
			if scenario == "unknown" {
				expected = "uncertain"
			}
			if !strings.Contains(receipt, expected) {
				t.Fatalf("original model resumed with wrong receipt %q wanted %s", receipt, expected)
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("original model never received tool receipt; output=%s", output.String())
		}
	}
	if scenario == "background" {
		deadline := time.Now().Add(25 * time.Second)
		var final json.RawMessage
		completed := false
		for time.Now().Before(deadline) {
			final = must("tasks.get", map[string]any{"task_id": taskID})
			var task struct {
				Task struct {
					State  string `json:"state"`
					Result struct {
						Evidence []struct {
							ID string `json:"action_id"`
						} `json:"evidence"`
					} `json:"result"`
					Delivery struct {
						Status string `json:"status"`
					} `json:"delivery"`
				} `json:"task"`
			}
			json.Unmarshal(final, &task)
			if task.Task.State == "completed" && task.Task.Delivery.Status == "delivered" && len(task.Task.Result.Evidence) == 1 && task.Task.Result.Evidence[0].ID == pending.ActionID {
				completed = true
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if !completed {
			t.Fatalf("background result did not close with exact connector evidence: %s", final)
		}
		history, e := session.ReadHistoryPage(context.Background(), rt.SessionDir, main.Agent.Session, 1, 100)
		if e != nil {
			t.Fatal(e)
		}
		delivered := 0
		for _, record := range history.Records {
			if record.Role == "assistant" && strings.Contains(record.Content, "VERIFIED_CONNECTOR_DELIVERY") {
				delivered++
			}
		}
		if delivered != 1 {
			t.Fatalf("main received %d connector result messages", delivered)
		}
		t.Log("background task completed with host-verified action evidence and exactly one main-chat delivery")
	}
	expectedEffects := int32(1)
	if scenario == "reject" {
		expectedEffects = 0
	}
	if effects.Load() != expectedEffects {
		t.Fatalf("effect count=%d", effects.Load())
	}
	// Close the real host, reload the same durable store and plugin fingerprint,
	// then check the exact existing receipt cannot dispatch again.
	wire.Close()
	<-served
	wireOpen = false
	host.Close()
	rt.Cleanup()
	rt, host, broker, _ = newRuntime()
	rows, e := broker.Actions(context.Background(), actor)
	if e != nil || len(rows) != 1 || rows[0].State != pendingState(scenario) {
		t.Fatalf("restart receipt=%+v error=%v", rows, e)
	}
	original := personalconnectors.Request{ConnectionID: "chain-gmail", Operation: "gmail.send", RequestID: "same-exact-write", Arguments: json.RawMessage(`{"to":["synthetic@example.invalid"],"subject":"Synthetic chain","body":"No actual email is sent."}`)}
	again, e := broker.Call(context.Background(), actor, original)
	if scenario == "background" {
		if e == nil || effects.Load() != expectedEffects {
			t.Fatalf("terminal task replay was not fenced: %v effects=%d", e, effects.Load())
		}
		t.Log("PASS background: owner main → worker connector → exact confirmation → typed evidence → one main delivery; restart retained receipt and terminal authority fenced replay")
		return
	}
	if e != nil {
		t.Fatal(e)
	}
	if again.State != pendingState(scenario) || effects.Load() != expectedEffects {
		t.Fatalf("restart replay=%+v effects=%d", again, effects.Load())
	}
	if scenario == "cold_pending" {
		t.Logf("PASS cold pending: original awaiting approval survived host replacement, fresh admitted owner approved once, terminal receipt then survived second restart; HTTP effects=%d", effects.Load())
	} else {
		t.Logf("PASS %s: real owner main → registered bundled tool → host broker pending → exact owner decision → original tool receipt; host restart preserved %s without replay; HTTP effects=%d", scenario, again.State, effects.Load())
	}
}
func pendingState(scenario string) string {
	switch scenario {
	case "reject":
		return "rejected"
	case "unknown":
		return "uncertain"
	default:
		return "completed"
	}
}

func (c *connectorChainModel) backgroundChat(req providers.ChatRequest) (providers.ChatResponse, error) {
	all := ""
	for _, m := range req.Messages {
		all += "\n" + m.Content
	}
	seen := func(id string) bool {
		for _, m := range req.Messages {
			if m.Role == "tool" && m.ToolCallID == id {
				return true
			}
		}
		return false
	}
	call := func(id, suffix string, args any) (providers.ChatResponse, error) {
		name := ""
		for _, tool := range req.Tools {
			if strings.Contains(tool.Name, "_"+suffix+"_") {
				name = tool.Name
			}
			properties, _ := tool.InputSchema["properties"].(map[string]any)
			if suffix == "personal_connector_call" && strings.Contains(tool.Name, "personal_connector") && properties["operation"] != nil && properties["request_id"] != nil {
				name = tool.Name
			}
		}
		if name == "" {
			return providers.ChatResponse{}, fmt.Errorf("background tool absent %s", suffix)
		}
		raw, _ := json.Marshal(args)
		return providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: id, Name: name, Arguments: string(raw)}}, StopReason: "tool_calls", FinishReason: providers.FinishReasonToolCalls}, nil
	}
	task := regexp.MustCompile(`Work on Personal task (task-[a-f0-9]+), requirements revision (\d+)`).FindStringSubmatch(all)
	if len(task) > 1 {
		if !seen("connector-chain-write") {
			return call("connector-chain-write", "personal_connector_call", map[string]any{"connection_id": "chain-gmail", "operation": "gmail.send", "request_id": "same-exact-write", "arguments": map[string]any{"to": []string{"synthetic@example.invalid"}, "subject": "Synthetic chain", "body": "No actual email is sent."}})
		}
		for _, m := range req.Messages {
			if m.Role == "tool" && m.ToolCallID == "connector-chain-write" {
				c.once.Do(func() { c.done <- m.Content })
				if !seen("connector-chain-complete") {
					var receipt personalconnectors.Result
					if e := json.Unmarshal([]byte(m.Content), &receipt); e != nil || receipt.ActionID == "" {
						return providers.ChatResponse{}, fmt.Errorf("model receipt missing action identity: %s", m.Content)
					}
					return call("connector-chain-complete", "complete_task", map[string]any{"task_id": task[1], "requirements_revision": 1, "summary": "Synthetic message provider accepted, recipient delivery not verified.", "evidence": []any{map[string]any{"action_id": receipt.ActionID, "description": "Host connector receipt"}}})
				}
			}
		}
		return providersResponse("Synthetic background task closed with connector receipt."), nil
	}
	if !seen("connector-chain-create") {
		return call("connector-chain-create", "create_task", map[string]any{"request_id": "background-connector-chain", "title": "Synthetic connector background", "goal": "Send only the exact synthetic message to synthetic@example.invalid. Ask for confirmation; report with exact connector receipt."})
	}
	if strings.Contains(all, "has a verified submission") {
		return providersResponse("VERIFIED_CONNECTOR_DELIVERY: provider accepted; recipient delivery unverified."), nil
	}
	return providersResponse("Synthetic background work started."), nil
}
