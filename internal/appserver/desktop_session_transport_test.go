package appserver

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// The selected adapter must never borrow the desktop connection's owner
// authority, invent a route, or dispatch without a live source principal.
func TestDesktopSessionTransportRejectsMissingAuthorityAndConnection(t *testing.T) {
	provider := &desktopSessionTransport{bridge: newDesktopSessionTransportBridge()}
	if err := provider.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(context.Background())
	input := pluginhost.ServiceInvokeParams{Service: pluginhost.SessionTransportService, Method: "dispatch", Params: json.RawMessage(`{"version":1,"operation":"inspect","operation_id":"inspect-run","locator":{"version":1,"execution_host":"coding-host","session_id":"existing"},"params":{"session_id":"existing","request_id":"run"}}`)}
	if _, err := provider.InvokeService(context.Background(), input); err == nil {
		t.Fatal("missing source authority reached desktop transport")
	}
	if _, err := provider.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Method: "list-targets", Params: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("unauthenticated catalog exposed registered sessions")
	}
}

type registeredTransportConsumer struct{}

func (registeredTransportConsumer) ID() string { return "registered-transport-fixture" }
func (c registeredTransportConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (registeredTransportConsumer) Close(context.Context) error                      { return nil }
func (registeredTransportConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (registeredTransportConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: pluginhost.SessionTransportService, MajorVersion: 1, Required: true}}
}

// Spawned by the desktop acceptance test: real source provider, target receiver,
// session store, admission, model turn and response protocol; no custom wire
// transport implementation substitutes for either production boundary.
func TestRegisteredSessionTransportProcess(t *testing.T) {
	role := os.Getenv("WUU_REGISTERED_SESSION_ROLE")
	if role == "" {
		return
	}
	if role == "personal-source" {
		runRegisteredPersonalSource(t)
		return
	}
	bridge := newDesktopSessionTransportBridge()
	p := &desktopSessionTransport{bridge: bridge, validators: make(map[string]*desktopSessionValidator)}
	var additional []pluginhost.Client
	if role == "source" {
		additional = []pluginhost.Client{p, registeredTransportConsumer{}}
	}
	targetGate := make(chan struct{})
	var releaseOnce sync.Once
	releaseTarget := func() { releaseOnce.Do(func() { close(targetGate) }) }
	model := &fakeClient{response: providers.ChatResponse{Content: "existing Coding result"}}
	if role == "target-owner" || role == "target-delayed-federation" {
		model.onChat = func(call int, _ providers.ChatRequest) {
			if call == 1 {
				<-targetGate
			}
		}
	}
	rt := newTestRuntime(t, model, additional...)
	identity := principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "registered", Subject: "personal-controller"}
	var revoked atomic.Bool
	caller, _ := principal.AuthenticatedWithValidity(identity, func() bool { return !revoked.Load() })
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		return access.Decision{Allow: r.Owner || (!revoked.Load() && (r.Principal == identity || r.Principal == principal.LocalOwner().Identity()) && strings.HasPrefix(r.Action, "session/federation."))}, nil
	})
	var options = []ServerOption{WithTrustedLocalTransport()}
	if role != "target-owner" {
		options = append(options, WithAccessPolicy(policy))
	}
	if role == "target" || role == "target-delayed-federation" {
		options = append(options, WithSessionFederation(SessionFederationConfig{HostID: "registered-coding", Models: map[string][]string{"fake-provider": {"fake-model"}}, ToolAllow: []string{"read"}}))
	}
	s := New(rt, os.Stdout, options...)
	bridge.bind(s)
	defer func() { releaseTarget(); s.Close() }()
	defer p.Close(context.Background())
	var existing string
	if strings.HasPrefix(role, "target") {
		binding, err := rt.RetainState()
		if err != nil {
			t.Fatal(err)
		}
		defer binding.Close()
		create := pluginhost.SessionCreateParams{RequestID: "seed-existing", Name: "Existing Coding workspace", Visibility: "user", ContextSource: "fresh", Provider: "fake-provider", Model: "fake-model", PermissionMode: "standard"}
		if role == "target" || role == "target-delayed-federation" {
			create.PermissionMode = "read_only"
			create.ToolPolicy = &pluginhost.SessionToolPolicy{Allow: []string{"read"}}
		}
		created, err := s.createPluginSession(runtime.WithInvocationState(principal.WithContext(context.Background(), principal.LocalOwner()), binding), "fixture-coding-owner", create)
		if err != nil {
			t.Fatal(err)
		}
		existing = created.SessionID
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 64*1024*1024)
	var calls sync.WaitGroup
	for scanner.Scan() {
		raw := append([]byte(nil), scanner.Bytes()...)
		var req Request
		_ = json.Unmarshal(raw, &req)
		switch req.Method {
		case "fixture/existing":
			_ = s.writeResponse(req.ID, map[string]string{"session_id": existing}, nil)
		case "fixture/release":
			releaseTarget()
			_ = s.writeResponse(req.ID, map[string]bool{"released": true}, nil)
		case "fixture/revoke":
			revoked.Store(true)
			_ = s.writeResponse(req.ID, map[string]bool{"revoked": true}, nil)
		case "fixture/route", "fixture/catalog":
			calls.Add(1)
			go func(req Request) {
				defer calls.Done()
				binding, err := rt.RetainState()
				if err != nil {
					_ = s.writeResponse(req.ID, nil, err)
					return
				}
				defer binding.Close()
				ctx := runtime.WithInvocationState(principal.WithContext(context.Background(), caller), binding)
				ctx = access.WithDelegation(ctx, access.Delegation{Provider: "fixture", Grant: "explicit-existing-run", Scope: json.RawMessage(`{}`)})
				method := "dispatch"
				if req.Method == "fixture/catalog" {
					method = "list-targets"
					req.Params = json.RawMessage(`{}`)
				}
				result, callErr := binding.Host.CallService(ctx, "registered-transport-fixture", pluginhost.SessionTransportService, method, req.Params)
				if callErr != nil {
					_ = s.writeResponse(req.ID, nil, callErr)
				} else {
					_ = s.writeResponse(req.ID, json.RawMessage(result), nil)
				}
			}(req)
		default:
			if err := s.handleLine(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
		}
		if req.Method == "shutdown" {
			break
		}
	}
	calls.Wait()
}

func TestDesktopSessionDeliveryBindsExactFrameAndLiveAuthority(t *testing.T) {
	s := &Server{}
	payload := json.RawMessage(`{"version":1,"request":{"operation":"send"}}`)
	var revoked atomic.Bool
	marker := desktopSessionDelivery{server: s, method: desktopSessionDispatch, digest: sha256.Sum256(payload), check: func() error {
		if revoked.Load() {
			return errors.New("revoked")
		}
		return nil
	}}
	ctx := context.WithValue(context.Background(), desktopSessionDeliveryKey{}, marker)
	req := Request{Method: desktopSessionDispatch, Params: payload}
	guard, ok := s.authorizeDesktopSessionDelivery(ctx, req)
	if !ok || guard() != nil {
		t.Fatal("exact native frame denied")
	}
	for _, bad := range []Request{{Method: desktopSessionTargets, Params: payload}, {Method: desktopSessionDispatch, Params: json.RawMessage(`{}`)}, {Method: "browser/open", Params: payload}} {
		if _, ok := s.authorizeDesktopSessionDelivery(ctx, bad); ok {
			t.Fatal("different frame accepted")
		}
	}
	if _, ok := (&Server{}).authorizeDesktopSessionDelivery(ctx, req); ok {
		t.Fatal("different connection accepted")
	}
	if _, ok := s.authorizeDesktopSessionDelivery(context.Background(), req); ok {
		t.Fatal("wire-only request accepted")
	}
	revoked.Store(true)
	if guard() == nil {
		t.Fatal("revoked authority survived replay")
	}
}

func TestDesktopSessionTerminalRequiresExactRunReceipt(t *testing.T) {
	route := pluginhost.SessionRouteRequest{Version: 1, Operation: "inspect", OperationID: "inspect", Locator: pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "receiver", SessionID: "session"}, Params: json.RawMessage(`{"session_id":"session","request_id":"run"}`)}
	good := pluginhost.SessionRouteResult{Version: 1, OperationID: "inspect", Locator: route.Locator, Status: "completed", Result: json.RawMessage(`{"session":{"session_id":"session"},"turn":{"request_id":"run","state":"completed"}}`)}
	if !registeredSessionTerminal(route, good) {
		t.Fatal("exact terminal receipt not recognized")
	}
	for _, mutate := range []func(*pluginhost.SessionRouteResult){func(r *pluginhost.SessionRouteResult) { r.OperationID = "other" }, func(r *pluginhost.SessionRouteResult) { r.Locator.ExecutionHost = "other" }, func(r *pluginhost.SessionRouteResult) {
		r.Result = json.RawMessage(`{"session":{"session_id":"session"},"turn":{"request_id":"other","state":"completed"}}`)
	}, func(r *pluginhost.SessionRouteResult) {
		r.Result = json.RawMessage(`{"session":{"session_id":"other"},"turn":{"request_id":"run","state":"completed"}}`)
	}, func(r *pluginhost.SessionRouteResult) { r.Status = "unknown" }} {
		bad := good
		mutate(&bad)
		if registeredSessionTerminal(route, bad) {
			t.Fatal("unrelated terminal receipt would release live run")
		}
	}
}

// Unlike fixture/route, this process runs the generated Personal package and
// real native grant issuer/lineage/restore gateway through UserHost's client API.
func runRegisteredPersonalSource(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(key, filepath.Join(home, key))
	}
	t.Setenv("WUU_HOME", filepath.Join(home, "wuu"))
	var discovered, created atomic.Bool
	publicTools := map[string]string{}
	reviewed := map[string]bool{}
	var reviewRecords []map[string]any
	var modelMu sync.Mutex
	invoke := func(id, local string, args any) providers.ChatResponse {
		raw, _ := json.Marshal(args)
		return providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: id, Name: publicTools[local], Arguments: string(raw)}}, StopReason: "tool_calls", FinishReason: providers.FinishReasonToolCalls}
	}
	var catalogValue func(any) map[string]any
	catalogValue = func(value any) map[string]any {
		if text, ok := value.(string); ok {
			var decoded any
			if json.Unmarshal([]byte(text), &decoded) == nil {
				return catalogValue(decoded)
			}
			return nil
		}
		switch item := value.(type) {
		case map[string]any:
			if _, ok := item["targets"].([]any); ok {
				return item
			}
			for _, entry := range item {
				if result := catalogValue(entry); result != nil {
					return result
				}
			}
		case []any:
			for _, entry := range item {
				if result := catalogValue(entry); result != nil {
					return result
				}
			}
		}
		return nil
	}
	endpoint := newTestOpenAIEndpoint(t, "registered-model", "registered-title", func(ctx context.Context, request providers.ChatRequest) (providers.ChatResponse, error) {

		modelMu.Lock()
		defer modelMu.Unlock()
		latestUser := ""
		for _, message := range request.Messages {
			if message.Role == "user" && !strings.HasPrefix(message.Name, "wuu_") && !strings.HasPrefix(message.Content, "Personal identity revision") {
				latestUser = message.Content
			}
		}
		if strings.Contains(latestUser, "Check normal tools after review") {
			names := []string{}
			for _, tool := range request.Tools {
				names = append(names, tool.Name)
			}
			raw, _ := json.Marshal(names)
			_ = os.WriteFile(os.Getenv("WUU_REGISTERED_PERSONAL_DESCRIPTOR")+".normal-tools", raw, 0600)
			return providers.ChatResponse{Content: "Normal human conversation tools restored"}, nil
		}
		match := regexp.MustCompile(`Review only Personal task ([^,]+), requirements revision ([0-9]+), worker request ([^.]+)\.`).FindStringSubmatch(latestUser)
		if len(match) == 4 {
			if len(request.Tools) != 1 || request.Tools[0].Name != publicTools["review_task_response"] {
				return providers.ChatResponse{}, errors.New("review must expose only its resolved direct decision tool, without run_code")
			}
			if reviewed[match[3]] {
				return providers.ChatResponse{Content: "Recorded controller decision; awaiting its committed application"}, nil
			}
			decision := "accept"
			if os.Getenv("WUU_REGISTERED_REVIEW_FOLLOWUP") == "1" && len(reviewed) == 0 {
				decision = "follow_up"
			}
			revision, _ := strconv.Atoi(match[2])
			args := map[string]any{"task_id": match[1], "requirements_revision": revision, "worker_request_id": match[3], "decision": decision, "summary": "Controller reviewed the existing Coding response", "evidence": []string{"Existing Coding response returned from the selected registered session"}}
			if decision == "follow_up" {
				args["follow_up"] = "Confirm the existing result and return the final summary within the original objective"
			}
			raw, _ := json.Marshal(args)
			reviewed[match[3]] = true
			reviewRecords = append(reviewRecords, map[string]any{"tool": request.Tools[0].Name, "arguments": args, "tools_count": len(request.Tools)})
			evidence, _ := json.Marshal(reviewRecords)
			_ = os.WriteFile(os.Getenv("WUU_REGISTERED_PERSONAL_DESCRIPTOR")+".reviews", evidence, 0600)
			return providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: "review-" + match[3], Name: request.Tools[0].Name, Arguments: string(raw)}}, StopReason: "tool_calls", FinishReason: providers.FinishReasonToolCalls}, nil
		}
		var observedCatalog map[string]any
		for _, message := range request.Messages {
			if message.Role == "tool" && strings.Contains(message.Content, `"actual-personal-task"`) && strings.Contains(message.Content, `"task_id"`) {
				if err := os.WriteFile(os.Getenv("WUU_REGISTERED_PERSONAL_DESCRIPTOR")+".created", []byte(message.Content), 0600); err != nil {
					return providers.ChatResponse{}, err
				}
			}
			if message.Role == "tool" && strings.Contains(message.Content, `"targets"`) {
				if err := os.WriteFile(os.Getenv("WUU_REGISTERED_PERSONAL_DESCRIPTOR")+".catalog", []byte(message.Content), 0600); err != nil {
					return providers.ChatResponse{}, err
				}
				observedCatalog = catalogValue(message.Content)
			}
		}
		if observedCatalog != nil && !created.Load() {
			var selected map[string]any
			for _, value := range observedCatalog["targets"].([]any) {
				entry, _ := value.(map[string]any)
				if entry["route_kind"] == os.Getenv("WUU_REGISTERED_TEST_ROUTE_KIND") && entry["title"] == "Existing Coding workspace" {
					if selected != nil {
						return providers.ChatResponse{}, errors.New("ambiguous Coding workspace")
					}
					selected = entry
				}
			}
			if selected != nil {
				created.Store(true)
				return invoke("delegate-existing", "create_task", map[string]any{"request_id": "actual-personal-task", "title": "Use existing Coding session", "goal": "Read existing work and report the result", "existing_session": selected["locator"]}), nil
			}
		}
		if !discovered.Load() && strings.Contains(latestUser, "Use my existing Coding workspace") {
			discovered.Store(true)
			return invoke("discover-existing", "list_sessions", map[string]any{}), nil
		}
		if os.Getenv("WUU_REGISTERED_HOLD_MAIN") == "1" && len(reviewed) == 0 && created.Load() {
			for {
				if _, err := os.Stat(os.Getenv("WUU_REGISTERED_PERSONAL_DESCRIPTOR") + ".release-main"); err == nil {
					break
				}
				select {
				case <-ctx.Done():
					return providers.ChatResponse{}, ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
			}
		}
		return providers.ChatResponse{Content: "Personal received the existing Coding result"}, nil
	})
	cfg := config.Config{PTC: config.PTCConfig{Enabled: os.Getenv("WUU_REGISTERED_REVIEW_PTC") == "1"}, DefaultProvider: "registered-model", Agent: config.AgentConfig{ToolLoading: config.ToolLoadingFlat}, Providers: map[string]config.ProviderConfig{"registered-model": {Type: "openai-compatible", BaseURL: endpoint.URL + "/v1", APIKey: "synthetic-unused", Model: "fake-model"}}}
	cfg.Agent.ModelRoles.Title = config.ModelRoleConfig{Provider: "registered-model", Model: "registered-title"}
	cfgPath := filepath.Join(home, "config.json")
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	bindings := NewRuntimeBindings()
	store := credentialstore.NewFileStore(filepath.Join(home, "fixture-credentials.json"))
	credentials := runtime.NativeProviderFactory{ID: credentialstore.OSModule, DefaultEnabled: true, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
		return credentialstore.NewService(credentialstore.OSModule, func() (credentialstore.Store, error) { return store, nil }), nil
	}}
	rt, err := runtime.NewSession(runtime.Options{RootDir: filepath.Join(home, "unused"), HomeDir: home, ConfigPath: cfgPath, ConfigLoadMode: runtime.ConfigLoadFile, Config: cfg, Overrides: runtime.ConfigOverrides{CompositionProfile: "personal"}, UserSpaceID: "local", ProductDomain: "personal", NonInteractive: true, NativeProviders: append(bindings.NativeProviders(), credentials)})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Cleanup()
	host, err := NewUserHost(rt, personalhost.Options{}, WithRuntimeBindings(bindings))
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	admission := os.NewFile(3, "desktop-owner")
	if admission == nil {
		t.Fatal("desktop owner admission pipe absent")
	}
	defer admission.Close()
	owner, err := clientauthority.AdmitDesktop(personalTestOwner(context.Background()), rt.UserBinding(), admission)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor map[string]string
	for _, p := range rt.ActivePlugins {
		if p.ID == "personal" {
			descriptor = map[string]string{"id": p.SubjectID, "fingerprint": p.Fingerprint, "graph_generation": rt.ExtensionSnapshot().GenerationID}
		}
	}
	if descriptor == nil {
		t.Fatal("generated Personal package absent")
	}
	for _, definition := range rt.PluginHost.ToolDefinitions() {
		registered, found := rt.PluginHost.Tool(definition.Name)
		if !found {
			continue
		}
		for _, local := range []string{"list_sessions", "create_task", "review_task_response"} {
			if registered.Registration.ID == local {
				publicTools[local] = registered.PublicName
			}
		}
	}
	for _, local := range []string{"list_sessions", "create_task", "review_task_response"} {
		if publicTools[local] == "" {
			t.Fatalf("Personal tool missing: %s", local)
		}
	}
	ready, _ := json.Marshal(descriptor)
	if err := os.WriteFile(os.Getenv("WUU_REGISTERED_PERSONAL_DESCRIPTOR"), ready, 0600); err != nil {
		t.Fatal(err)
	}
	if err = host.ServeTrustedLocal(owner, os.Stdin, os.Stdout); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopSessionConnectionSelectionAndRunOrigin(t *testing.T) {
	bridge := newDesktopSessionTransportBridge()
	connection := func() *Server {
		s := &Server{access: connectionAccess{principal: principal.LocalOwner()}}
		s.trustedTransport.enabled = true
		s.setClientMethods([]string{desktopSessionDispatch})
		bridge.bind(s)
		return s
	}
	first := connection()
	if got, err := bridge.connection(desktopSessionDispatch); err != nil || got != first {
		t.Fatal("single explicit connection not selected")
	}
	second := connection()
	if _, err := bridge.connection(desktopSessionDispatch); err == nil {
		t.Fatal("ambiguous Desktop silently selected")
	}
	first.closed.Store(true)
	if got, err := bridge.connection(desktopSessionDispatch); err != nil || got != second {
		t.Fatal("live connection not selected")
	}
	provider := &desktopSessionTransport{origins: map[string]desktopSessionOrigin{"accepted-run": {server: first}}, validators: map[string]*desktopSessionValidator{}}
	if _, err := provider.validator(context.Background(), principal.LocalOwner(), "new-inspect-action", "accepted-run", second, true); err == nil || !strings.Contains(err.Error(), "another transport connection") {
		t.Fatal("new action rebound accepted run after original Desktop closed")
	}
}

func TestDesktopSessionConnectionReplyIsolation(t *testing.T) {
	firstReply := make(chan clientResponse, 1)
	secondReply := make(chan clientResponse, 1)
	first := &Server{clientCalls: map[string]chan clientResponse{"srv-1": firstReply}}
	second := &Server{clientCalls: map[string]chan clientResponse{"srv-1": secondReply}}
	second.deliverClientResponse([]byte(`{"id":"srv-1","result":{"receiver":"second"}}`))
	select {
	case <-firstReply:
		t.Fatal("another connection resolved the first request")
	default:
	}
	select {
	case <-secondReply:
	default:
		t.Fatal("own connection did not receive response")
	}
	first.failPendingClientCalls()
	select {
	case reply := <-firstReply:
		if reply.Error == nil {
			t.Fatal("closed connection manufactured successful receipt")
		}
	default:
		t.Fatal("connection close left pending callback")
	}
}
