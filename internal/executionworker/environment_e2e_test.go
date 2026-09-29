package executionworker

import (
	"context"
	"encoding/json"
	"os"
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
		Code:  `const fs=await import("node:fs/promises"); await fs.writeFile("program.txt","program output"); return await tools.read_file({path:"marker.txt"});`,
		Tools: []codemode.ToolDefinition{{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}},
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
