package host

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/remote/phone"
	"github.com/blueberrycongee/wuu/internal/remote/relay"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
)

// Explicit acceptance command: node desktop/scripts/trusted-desktop-e2e.mjs.
// The fixture is the production desktop bridge and AppServerClientPool, with a
// real core executable and a policy compiled only from the public SDK package.
func TestEncryptedDelegatedDesktopSharedCore(t *testing.T) {
	fixture := os.Getenv("WUU_TRUSTED_DESKTOP_FIXTURE")
	if fixture == "" {
		t.Skip("requires isolated real-desktop fixture; run trusted-desktop-e2e.mjs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Getenv("WUU_TRUSTED_NODE"), fixture)
	child.Stderr = os.Stderr
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = input.Close()
		if err := child.Wait(); err != nil {
			t.Logf("fixture exit: %v", err)
		}
	}()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64*1024), 32<<20)
	if !scanner.Scan() {
		t.Fatal("desktop fixture did not start")
	}
	var ready struct {
		Endpoint struct {
			Address string `json:"address"`
			Token   string `json:"token"`
		} `json:"endpoint"`
		First     string `json:"first"`
		Second    string `json:"second"`
		Processes int    `json:"core_processes"`
	}
	if err = json.Unmarshal(scanner.Bytes(), &ready); err != nil {
		t.Fatal(err)
	}
	nextID := 0
	ownerCall := func(method string, params any) json.RawMessage {
		t.Helper()
		nextID++
		if err := json.NewEncoder(input).Encode(map[string]any{"id": nextID, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		if !scanner.Scan() {
			t.Fatal("fixture exited during " + method)
		}
		var r struct {
			Result json.RawMessage `json:"result"`
			Error  string          `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &r) != nil || r.Error != "" {
			t.Fatalf("local %s: %s", method, scanner.Text())
		}
		return r.Result
	}
	registry, err := relay.OpenRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	relayServer := httptest.NewServer(relay.New(relay.Options{Registry: registry}).Handler())
	defer relayServer.Close()
	url := "ws" + strings.TrimPrefix(relayServer.URL, "http") + "/v1/connect"
	store, err := LoadOrCreateStore(filepath.Join(t.TempDir(), "remote.json"), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetRelayURL(url); err != nil {
		t.Fatal(err)
	}
	device, err := secure.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AddDelegatedDevice(device.Public(), "agent", time.Now()); err != nil {
		t.Fatal(err)
	}
	pub := secure.EncodeKey(device.Public())
	if err := registry.AddDevice(secure.EncodeKey(store.Identity().Public()), pub, "agent", time.Now()); err != nil {
		t.Fatal(err)
	}
	caller, err := store.DevicePrincipal(pub)
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]any{"grants": []any{
		map[string]any{"principal": caller.Identity(), "actions": []string{"initialize", "thread/listAll"}, "resources": []any{map[string]any{"kind": "global"}}},
		map[string]any{"principal": caller.Identity(), "actions": []string{"thread/resume", "thread/listAll", "thread/rename", "event:thread/resumed", "event:thread/updated"}, "resources": []any{map[string]any{"kind": "session", "id": ready.First}}},
		map[string]any{"principal": caller.Identity(), "actions": []string{"thread/start"}, "resources": []any{map[string]any{"kind": "global"}, map[string]any{"kind": "workspace", "id": os.Getenv("WUU_TRUSTED_WORKSPACE")}}, "permission_modes": []string{"read_only"}, "models": []string{"budget-only"}, "model_providers": []string{"wuu"}},
	}}
	save := func() {
		t.Helper()
		raw, _ := json.Marshal(state)
		path := os.Getenv("WUU_TRUSTED_POLICY")
		if err := os.WriteFile(path+".tmp", raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			t.Fatal(err)
		}
	}
	save()
	bridge, err := DesktopAppServer(ready.Endpoint.Address, ready.Endpoint.Token)
	if err != nil {
		t.Fatal(err)
	}
	connected := make(chan struct{}, 1)
	h, err := New(Options{Store: store, AppServer: bridge, OnConnected: func() {
		select {
		case connected <- struct{}{}:
		default:
		}
	}, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	hostCtx, stopHost := context.WithCancel(ctx)
	hostDone := make(chan struct{})
	go func() { defer close(hostDone); _ = h.Run(hostCtx) }()
	defer func() { stopHost(); <-hostDone }()
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	credentials := &phone.Credentials{V: 1, DeviceSeed: base64.RawURLEncoding.EncodeToString(device.Seed()), HostPub: secure.EncodeKey(store.Identity().Public()), RelayURL: url}
	client, err := phone.NewClient(credentials, phone.Options{})
	if err != nil {
		t.Fatal(err)
	}
	run := func() (context.CancelFunc, chan struct{}) {
		t.Helper()
		c, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); _ = client.Run(c) }()
		wait, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := client.WaitAttached(wait); err != nil {
			t.Fatal(err)
		}
		return stop, done
	}
	stopClient, clientDone := run()
	defer func() { stopClient(); <-clientDone }()
	call := func(method string, params any, result any) error {
		t.Helper()
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return client.Protocol().Call(c, method, params, result)
	}
	if err := call("initialize", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	var resumed appserver.ThreadResumeResult
	if err := call("thread/resume", map[string]any{"session_id": ready.First}, &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Thread.ID != ready.First {
		t.Fatal("did not reach original local session")
	}
	if err := call("thread/resume", map[string]any{"session_id": ready.Second}, new(any)); err == nil {
		t.Fatal("ungranted local session leaked")
	}
	var list appserver.ThreadListResult
	if err := call("thread/listAll", map[string]any{"summary_only": true}, &list); err != nil || len(list.Threads) != 1 || list.Threads[0].ID != ready.First {
		t.Fatalf("list boundary: %+v, %v", list, err)
	}
	var bounded appserver.ThreadStartResult
	if err := call("thread/start", map[string]any{"provider": "wuu", "model": "budget-only", "engine": "wuu", "permission_mode": "read_only"}, &bounded); err != nil {
		t.Fatalf("bounded create rejected: %v", err)
	}
	if bounded.Thread.Model != "budget-only" || bounded.Thread.PermissionMode != "read_only" {
		t.Fatalf("effective execution bounds changed: %+v", bounded.Thread)
	}
	if err := call("thread/resume", map[string]any{"session_id": bounded.Thread.ID}, new(any)); err == nil {
		t.Fatal("creation silently promoted resource access")
	}
	for _, request := range []struct {
		method string
		params any
	}{{"config/read", map[string]any{"owner": true, "principal": map[string]any{"subject": "owner"}}}, {"thread/start", map[string]any{"provider": "wuu", "model": "budget-only", "permission_mode": "unconfined"}}, {"thread/start", map[string]any{"provider": "wuu", "model": "premium-forbidden", "permission_mode": "read_only"}}, {"thread/start", map[string]any{"permission_mode": "read_only"}}, {"thread/start", map[string]any{"provider": "wuu", "model": "budget-only", "permission_mode": "read_only", "cwd": t.TempDir()}}, {"desktop/file/read", map[string]any{"path": "/etc/passwd"}}} {
		if err := call(request.method, request.params, new(any)); err == nil {
			t.Fatalf("delegated privilege escalation accepted: %s", request.method)
		}
	}
	var allCreated appserver.ThreadListResult
	if err := json.Unmarshal(ownerCall("thread/listAll", map[string]any{"summary_only": true}), &allCreated); err != nil || len(allCreated.Threads) != 3 {
		t.Fatalf("denied create persisted a session: %d, %v", len(allCreated.Threads), err)
	}
	t.Log("PASS encrypted device reaches original shared core; bounded model create succeeds; premium/omitted effects denied without persistence or auto-grant")
	// Stop the controller, not the shared core. A local edit is spooled with its
	// original receipt; policy revocation must invalidate that exact saved frame.
	stopClient()
	<-clientDone
	ownerCall("thread/rename", map[string]any{"thread_id": ready.First, "title": "offline-private-update"})
	var savedGuard func() error
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
waitSpool:
	for {
		h.sessMu.Lock()
		session := h.sessions[pub]
		h.sessMu.Unlock()
		if session != nil {
			session.mu.Lock()
			if session.app != nil {
				for _, entry := range session.app.spool.entries {
					if strings.Contains(string(entry.line), "offline-private-update") {
						savedGuard = entry.guard
						break
					}
				}
			}
			session.mu.Unlock()
		}
		if savedGuard != nil {
			break waitSpool
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("scoped offline event never reached replay spool")
		}
	}
	if err := savedGuard(); err != nil {
		t.Fatalf("live replay guard: %v", err)
	}
	state["grants"] = []any{}
	save()
	if err := savedGuard(); err == nil {
		t.Fatal("spooled desktop receipt survived current policy revocation")
	}
	stopClient, clientDone = run()
	if err := call("thread/resume", map[string]any{"session_id": ready.First}, new(any)); err == nil {
		t.Fatal("revoked read survived reconnect")
	}
	t.Log("PASS original event replay receipt denied after policy revocation on existing bridge")
	// A separately enrolled owner remains an owner; delegation never mutates the
	// shared pipe's principal or creates another core process.
	ownerID, _ := secure.NewIdentity()
	if err := store.AddDevice(ownerID.Public(), "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := registry.AddDevice(secure.EncodeKey(store.Identity().Public()), secure.EncodeKey(ownerID.Public()), "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	ownerCreds := &phone.Credentials{V: 1, DeviceSeed: base64.RawURLEncoding.EncodeToString(ownerID.Seed()), HostPub: credentials.HostPub, RelayURL: url}
	ownerClient, err := phone.NewClient(ownerCreds, phone.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx, stopOwner := context.WithCancel(ctx)
	ownerDone := make(chan struct{})
	go func() { defer close(ownerDone); _ = ownerClient.Run(ownerCtx) }()
	defer func() { stopOwner(); <-ownerDone }()
	if err := ownerClient.WaitAttached(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ownerClient.Protocol().Call(ctx, "config/read", nil, new(any)); err != nil {
		t.Fatal("owner pairing regressed: " + err.Error())
	}
	attackCtx, stopAttack := context.WithTimeout(ctx, 2*time.Second)
	attackErr := ownerClient.Protocol().Call(attackCtx, "transport/dispatch", map[string]any{"version": 1, "peer_id": "forged", "owner": true, "principal": map[string]string{"kind": "user", "issuer": "wuu:local", "tenant": "local", "subject": "owner"}, "method": "config/read"}, new(any))
	stopAttack()
	if attackErr == nil {
		t.Fatal("raw encrypted transport method regained owner stdio authority")
	}
	t.Log("PASS raw encrypted transport injection rejected even for owner-paired socket")
	if count := string(ownerCall("fixture/process-count", nil)); count != "1" {
		t.Fatalf("delegation created duplicate core: %s", count)
	}
	if err := store.RemoveDevice(pub); err != nil {
		t.Fatal(err)
	}
	if err := savedGuard(); err == nil {
		t.Fatal("receipt survived enrollment removal")
	}
	t.Log("PASS owner remains functional; exactly one core process; revoked key loses cached authority")

}
