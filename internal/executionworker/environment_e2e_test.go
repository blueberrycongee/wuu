package executionworker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// This suite starts real isolated containers. Its verbose output is the
// repeatable execution receipt; no inference service or user data is needed.
func TestDockerEnvironmentEndToEnd(t *testing.T) {
	image := os.Getenv("WUU_EXECUTION_E2E_IMAGE")
	if image == "" {
		t.Skip("set WUU_EXECUTION_E2E_IMAGE to an image containing the current worker")
	}
	profile := executionenv.Profile{Backend: "docker", Image: image, Workspace: "/workspace", Network: "none"}
	testEnvironmentEndToEnd(t, profile)
}

func TestSingularityEnvironmentEndToEnd(t *testing.T) {
	image := os.Getenv("WUU_EXECUTION_E2E_SIF")
	if image == "" {
		t.Skip("set WUU_EXECUTION_E2E_SIF to the current worker image")
	}
	testEnvironmentEndToEnd(t, executionenv.Profile{Backend: "singularity", Image: image, Workspace: "/workspace"})
}

func TestSSHEnvironmentEndToEnd(t *testing.T) {
	raw := os.Getenv("WUU_EXECUTION_E2E_SSH")
	if raw == "" {
		t.Skip("set WUU_EXECUTION_E2E_SSH to an SSH profile JSON")
	}
	var profile executionenv.Profile
	if err := json.Unmarshal([]byte(raw), &profile); err != nil {
		t.Fatal(err)
	}
	testEnvironmentEndToEnd(t, profile)
}

func testEnvironmentEndToEnd(t *testing.T, profile executionenv.Profile) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store := t.TempDir()
	environment := executionenv.NewEnvironment(profile, executionenv.Identity(store, "a", "test", profile), "a", store)
	defer environment.Close()
	run := func(name, args, mode string) string {
		t.Helper()
		result, err := environment.Execute(ctx, executionenv.ToolRequest{Call: providers.ToolCall{Name: name, Arguments: args}, Actor: "a", PermissionMode: mode})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.IsError {
			t.Fatalf("%s: %s", name, result.TextProjection())
		}
		if len(result.StructuredContent) > 0 {
			return string(result.StructuredContent)
		}
		return result.TextProjection()
	}
	run("write_file", `{"path":"marker.txt","content":"environment marker"}`, "standard")
	if text := run("bash", `{"command":"cat marker.txt"}`, "standard"); !strings.Contains(text, "environment marker") {
		t.Fatal(text)
	}
	if text := run("read_file", `{"path":"marker.txt"}`, "read_only"); !strings.Contains(text, "environment marker") {
		t.Fatal(text)
	}
	if _, err := environment.Execute(ctx, executionenv.ToolRequest{Call: providers.ToolCall{Name: "write_file", Arguments: `{"path":"denied.txt","content":"no"}`}, Actor: "a", PermissionMode: "read_only"}); err == nil {
		t.Fatal("read-only mutation was accepted")
	}
	run("bash", `{"command":"git init -q && git status --porcelain"}`, "standard")
	second := executionenv.NewEnvironment(profile, executionenv.Identity(store, "b", "test", profile), "b", store)
	defer second.Close()
	if _, err := second.Execute(ctx, executionenv.ToolRequest{Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"marker.txt"}`}, Actor: "a", PermissionMode: "standard"}); err == nil {
		t.Fatal("isolated session read another session's file")
	}
	events := make(chan process.Event, 8)
	environment.ProcessManager().Subscribe(events)
	defer environment.ProcessManager().Unsubscribe(events)
	started := run("bash", `{"command":"sleep 120","run_in_background":true}`, "standard")
	select {
	case <-events:
	case <-ctx.Done():
		t.Fatal("missing process event")
	}
	var record struct {
		ProcessID string `json:"process_id"`
		ID        string `json:"id"`
		Process   struct {
			ID string `json:"id"`
		} `json:"process"`
	}
	if err := json.Unmarshal([]byte(started), &record); err != nil {
		t.Fatal(err)
	}
	id := record.ProcessID
	if id == "" {
		id = record.ID
	}
	if id == "" {
		id = record.Process.ID
	}
	if id == "" {
		t.Fatalf("missing process identity: %s", started)
	}
	remoteProcesses := environment.ProcessManager()
	records, err := remoteProcesses.List()
	if err != nil || len(records) != 1 || records[0].OwnerID != "a" || records[0].OwnerKind != "main_agent" {
		t.Fatalf("process panel: %v %+v", err, records)
	}
	args, _ := json.Marshal(map[string]string{"action": "stop", "process_id": id})
	run("process", string(args), "standard")

	code, err := environment.RunCode(ctx, executionenv.CodeRequest{Actor: "a", PermissionMode: "standard", Program: codemode.RunRequest{
		Code:  `await tools.write_file({path:"program.txt",content:"program output"}); return await tools.read_file({path:"marker.txt"});`,
		Tools: []codemode.ToolDefinition{{Name: "write_file"}, {Name: "read_file", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}},
	}}, callbackExecutor{invoke: func(callCtx context.Context, call providers.ToolCall) (toolresult.Result, error) {
		return environment.Execute(callCtx, executionenv.ToolRequest{Call: call, Actor: "a", PermissionMode: "standard"})
	}})
	if err != nil || code.Error != "" {
		t.Fatalf("remote program: %v %s", err, code.Error)
	}
	if text := run("read_file", `{"path":"program.txt"}`, "standard"); !strings.Contains(text, "program output") {
		t.Fatal(text)
	}
	artifact := run("present_artifact", `{"path":"program.txt"}`, "standard")
	var descriptor struct {
		Token string `json:"execution_export"`
		Size  int64  `json:"size"`
	}
	if err = json.Unmarshal([]byte(artifact), &descriptor); err != nil {
		t.Fatal(err)
	}
	var download strings.Builder
	if err = environment.Download(ctx, descriptor.Token, descriptor.Size, &download); err != nil {
		t.Fatal(err)
	}
	if download.String() != "program output" {
		t.Fatal("artifact bytes differ from remote source")
	}
	t.Log("verified: file/command consistency, read-only denial, git, session isolation, background process panel and start/stop, remote program with host-routed nested tool, artifact transfer")
}

func TestDockerReconnectEndToEnd(t *testing.T) {
	image := os.Getenv("WUU_EXECUTION_E2E_IMAGE")
	if image == "" {
		t.Skip("set WUU_EXECUTION_E2E_IMAGE")
	}
	p := executionenv.Profile{Backend: "docker", Image: image, Persistent: true, Workspace: "/workspace"}
	store := t.TempDir()
	identity := executionenv.Identity(store, "recovery", "test", p)
	first := executionenv.NewEnvironment(p, identity, "recovery", store)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	call := executionenv.ToolRequest{Actor: "recovery", PermissionMode: "standard", Call: providers.ToolCall{Name: "write_file", Arguments: `{"path":"retained","content":"retained bytes"}`}}
	if _, err := first.Execute(ctx, call); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	p.Persistent = false
	second := executionenv.NewEnvironment(p, identity, "recovery", store)
	defer second.Close()
	call.Call = providers.ToolCall{Name: "read_file", Arguments: `{"path":"retained"}`}
	result, err := second.Execute(ctx, call)
	if err != nil || !strings.Contains(result.TextProjection(), "retained bytes") {
		t.Fatalf("reconnect: %v %s", err, result.TextProjection())
	}
	t.Log("verified: reconnect reuses authenticated worker and retained filesystem")
}

func TestDockerMountAndEnvironmentBoundaryEndToEnd(t *testing.T) {
	image := os.Getenv("WUU_EXECUTION_E2E_IMAGE")
	if image == "" {
		t.Skip("set WUU_EXECUTION_E2E_IMAGE")
	}
	base := os.Getenv("WUU_EXECUTION_E2E_MOUNT_ROOT")
	if base == "" {
		base = t.TempDir()
	}
	mount, err := os.MkdirTemp(base, "execution-mount-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mount)
	if err := os.WriteFile(filepath.Join(mount, "input.txt"), []byte("mounted input"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WUU_E2E_VISIBLE", "explicit-value")
	t.Setenv("WUU_E2E_HIDDEN", "must-stay-on-host")
	p := executionenv.Profile{Backend: "docker", Image: image, Workspace: "/workspace", HostWorkspace: mount, MountReadOnly: true, Network: "none", ForwardEnv: []string{"WUU_E2E_VISIBLE"}}
	store := t.TempDir()
	identity := executionenv.Identity(store, "boundary", "test", p)
	environment := executionenv.NewEnvironment(p, identity, "boundary", store)
	defer environment.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	run := func(name, args string) (toolresult.Result, error) {
		return environment.Execute(ctx, executionenv.ToolRequest{Actor: "boundary", PermissionMode: "standard", Call: providers.ToolCall{Name: name, Arguments: args}})
	}
	result, err := run("read_file", `{"path":"input.txt"}`)
	if err != nil || !strings.Contains(result.TextProjection(), "mounted input") {
		t.Fatalf("read mount: %v %s", err, result.TextProjection())
	}
	if _, err := run("write_file", `{"path":"denied.txt","content":"no"}`); err == nil {
		t.Fatal("read-only mount accepted a write")
	}
	result, err = run("bash", `{"command":"printf '%s/%s' \"$WUU_E2E_VISIBLE\" \"${WUU_E2E_HIDDEN-unset}\""}`)
	if err != nil || !strings.Contains(result.TextProjection(), "explicit-value/unset") {
		t.Fatalf("environment boundary: %v %s", err, result.TextProjection())
	}
	unauthorized := executionenv.NewClient([]string{"docker", "exec", "-i", identity, "wuu", "execution-connect", "--socket", identity + "-boundary"}, os.Environ())
	defer unauthorized.Close()
	if _, err := unauthorized.Call(ctx, "initialize", executionenv.Init{Version: executionenv.ProtocolVersion, Root: "/workspace", Session: "boundary"}); err == nil {
		t.Fatal("worker accepted an unauthenticated connection")
	}
	t.Log("verified: explicit host mount, read-only mount rejection, allowlisted variable forwarding, authenticated worker boundary")
}

// The CLI fixture exercises a real terminal and worker; it does not emulate a
// cloud account or claim provider provisioning coverage.
func TestPTYTransportEndToEnd(t *testing.T) {
	worker := os.Getenv("WUU_EXECUTION_E2E_WORKER")
	if worker == "" || runtime.GOOS == "windows" {
		t.Skip("set WUU_EXECUTION_E2E_WORKER to the current Unix worker")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
 create|stop|remove) exit 0 ;;
 exec) shift; while [ "$1" != "--" ]; do shift; done; shift; exec "$@" ;;
 *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "sandbox"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := executionenv.Profile{Backend: "vercel_sandbox", Image: "fixture", Worker: worker, Workspace: t.TempDir(), LifetimeSeconds: 1}
	store := t.TempDir()
	id := executionenv.Identity(store, "terminal", "test", p)
	environment := executionenv.NewEnvironment(p, id, "terminal", store)
	defer environment.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	content := strings.Repeat("payload", 32768)
	args, _ := json.Marshal(map[string]string{"path": "large.txt", "content": content})
	_, err := environment.Execute(ctx, executionenv.ToolRequest{Actor: "terminal", PermissionMode: "standard", Call: providers.ToolCall{Name: "write_file", Arguments: string(args)}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(p.Workspace, "large.txt"))
	if err != nil || string(data) != content {
		t.Fatalf("terminal frame was truncated: %v, bytes=%d", err, len(data))
	}
	if err := environment.Close(); err != nil {
		t.Fatal(err)
	}
	t.Log("verified: terminal readiness, raw mode, large authenticated request and clean shutdown")
}

func TestDockerSharedProvisioningEndToEnd(t *testing.T) {
	image := os.Getenv("WUU_EXECUTION_E2E_IMAGE")
	if image == "" {
		t.Skip("set WUU_EXECUTION_E2E_IMAGE")
	}
	p := executionenv.Profile{Backend: "docker", Image: image, Shared: true, Workspace: "/workspace"}
	store := t.TempDir()
	identity := executionenv.Identity(store, "first", "test", p)
	defer func() {
		if out, err := exec.Command("docker", "rm", "--force", identity).CombinedOutput(); err != nil {
			t.Errorf("cleanup shared container: %v %s", err, out)
		}
	}()
	first := executionenv.NewEnvironment(p, identity, "first", store)
	defer first.Close()
	second := executionenv.NewEnvironment(p, identity, "second", store)
	defer second.Close()
	start := make(chan struct{})
	results := make(chan error, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for i, environment := range []*executionenv.Environment{first, second} {
		go func(i int, environment *executionenv.Environment) {
			<-start
			args, _ := json.Marshal(map[string]string{"path": fmt.Sprintf("shared-%d", i), "content": "shared"})
			_, err := environment.Execute(ctx, executionenv.ToolRequest{Actor: "writer", PermissionMode: "standard", Call: providers.ToolCall{Name: "write_file", Arguments: string(args)}})
			results <- err
		}(i, environment)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	result, err := first.Execute(ctx, executionenv.ToolRequest{Actor: "reader", PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"shared-1"}`}})
	if err != nil || !strings.Contains(result.TextProjection(), "shared") {
		t.Fatalf("shared workspace: %v %s", err, result.TextProjection())
	}
	t.Log("verified: concurrent shared provisioning and cross-conversation file visibility")
}
