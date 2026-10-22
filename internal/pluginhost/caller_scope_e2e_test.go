package pluginhost

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A separate process retains a resource after service.invoke returns, then
// observes its owner's terminal frame without polling or another scoped call.
func TestProcessProviderCallerScopeLifetime(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for process-provider verification")
	}
	root := t.TempDir()
	script := filepath.Join(root, "provider.mjs")
	if err := os.WriteFile(script, []byte(`
import readline from 'node:readline';
const owners = new Map();
let lastOwner;
const send = (id, result) => process.stdout.write(JSON.stringify({id, result}) + '\n');
readline.createInterface({input: process.stdin}).on('line', line => {
  const request = JSON.parse(line);
  const p = request.params;
  if (request.method === 'initialize') {
    send(request.id, {protocol_version: 3, lifecycle_version: 2, provided_services: [{
      name: 'stream.provider', version: '1.0.0', methods: [
        {name: 'start', input_schema: 'stream.start.input.v1', output_schema: 'stream.start.output.v1'},
        {name: 'wait', input_schema: 'stream.wait.input.v1', output_schema: 'stream.wait.output.v1'}
      ]
    }]});
  } else if (request.method === 'execution.cancel') {
    const owner = owners.get(p.execution_id);
    if (owner) {
      owner.cancelled = true;
      for (const id of owner.waiters) send(id, {cancelled: true});
      owner.waiters = [];
    }
  } else if (request.method === 'service.invoke' && p.method === 'start') {
    const scope = p.caller_scope;
    if (!scope || scope.id === p.execution_id || scope.plugin_id !== p.caller) {
      process.stdout.write(JSON.stringify({id: request.id, error: {message: 'invalid caller scope'}}) + '\n');
      return;
    }
    lastOwner = scope.id;
    if (!owners.has(scope.id)) owners.set(scope.id, {cancelled: false, waiters: []});
    send(request.id, {alive: !owners.get(scope.id).cancelled, scope, callback: p.execution_id});
  } else if (request.method === 'service.invoke' && p.method === 'wait') {
    const owner = owners.get(lastOwner);
    if (owner.cancelled) send(request.id, {cancelled: true});
    else owner.waiters.push(request.id);
  } else {
    send(request.id, {});
  }
});
`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	provider, err := Start(ctx, ProcessConfig{ID: "stream", Command: node, Args: []string{script}, PluginRoot: root, ProjectRoot: root, WuuHome: root, PrepareOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	consumer := &fakeServiceProcess{id: "consumer", state: StateActive, required: []ServiceRequirement{{Name: "stream.provider", MajorVersion: 1, Required: true}}}
	registry, _ := BuildServiceRegistry(provider, consumer)
	host := New(provider, consumer)
	host.AttachServiceRegistry(registry, nil)
	defer host.Close(context.Background())
	if err := host.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	owner := host.executions.BeginTool("consumer", ctx, ToolExecuteInput{SessionID: "session", ThreadID: "thread", TurnID: "turn", CallID: "call"})
	defer host.executions.End(owner)
	var previousCallback string
	for range 2 {
		result, err := registry.Call(ctx, "consumer", ServiceCallParams{Service: "stream.provider", Method: "start", ExecutionID: owner})
		if err != nil {
			t.Fatal(err)
		}
		var state struct {
			Alive    bool              `json:"alive"`
			Scope    ExecutionSnapshot `json:"scope"`
			Callback string            `json:"callback"`
		}
		if err := json.Unmarshal(result, &state); err != nil {
			t.Fatal(err)
		}
		if !state.Alive || state.Scope.ID != owner || state.Scope.Kind != "tool" || state.Scope.SessionID != "session" || state.Callback == owner || state.Callback == previousCallback {
			t.Fatalf("invalid scoped response: %s", result)
		}
		previousCallback = state.Callback
	}
	host.executions.End(owner)
	result, callErr := registry.Call(ctx, "consumer", ServiceCallParams{Service: "stream.provider", Method: "wait"})
	if callErr != nil || string(result) != `{"cancelled":true}` {
		t.Fatalf("owner terminal signal: %s, %v", result, callErr)
	}
	if _, err := registry.Call(ctx, "consumer", ServiceCallParams{Service: "stream.provider", Method: "start", ExecutionID: owner}); err == nil || err.Code != "execution_not_found" {
		t.Fatalf("late invocation reopened owner: %v", err)
	}
	t.Log("independent Node provider retained one owner across two callback IDs, observed owner cancellation, and rejected late reuse")
}
