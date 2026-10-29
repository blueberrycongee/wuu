package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestArchivedResultRecoveryUsesSessionAndReadPermission(t *testing.T) {
	root, session := t.TempDir(), t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionDir(session)
	original := strings.Repeat("中文 🌱 output\n", 18000)
	raw := toolresult.FromText(original)
	settled := kit.FinalizeToolResult(providers.ToolCall{ID: "long", Name: "fixture"}, raw)
	page := parseOut(t, settled.TextProjection())
	cursor := page["continuation"].(map[string]any)["next"].(map[string]any)["continuation"].(string)
	call := func(k *Toolkit, ctx context.Context, token string, name string) (toolresult.Result, error) {
		arg, _ := json.Marshal(map[string]any{"continuation": token})
		return k.ExecuteResult(ctx, providers.ToolCall{ID: "recover", Name: name, Arguments: string(arg)})
	}
	recovered, err := call(kit, context.Background(), cursor, "recover_tool_result")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(recovered.TextProjection()), &payload); err != nil {
		t.Fatal(err)
	}
	first := page["content"].(string)
	if !strings.HasPrefix(original, first+payload["content"].(string)) {
		t.Fatalf("recovery did not continue exact original bytes: first=%q next=%q initial=%q", first[len(first)-min(40, len(first)):], payload["content"].(string)[:min(40, len(payload["content"].(string)))], original[:min(40, len(original))])
	}
	legacy, err := call(kit, context.Background(), cursor, "read_file")
	if err != nil {
		t.Fatalf("legacy cursor failed: %v", err)
	}
	var legacyPage map[string]any
	if err := json.Unmarshal([]byte(legacy.TextProjection()), &legacyPage); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(legacyPage["content"].(string), payload["content"].(string)) {
		t.Fatal("legacy cursor returned different content")
	}
	if payload["continuation"].(map[string]any)["recovery_tool"] != "recover_tool_result" {
		t.Fatal("recovered page lacks next-tool guidance")
	}
	if _, ok := legacyPage["continuation"].(map[string]any)["recovery_tool"]; ok {
		t.Fatal("legacy read_file behavior changed")
	}
	nextCursor := payload["continuation"].(map[string]any)["next"].(map[string]any)["continuation"].(string)
	nextPage, err := call(kit, context.Background(), nextCursor, "recover_tool_result")
	if err != nil {
		t.Fatal(err)
	}
	var nextPayload map[string]any
	if err := json.Unmarshal([]byte(nextPage.TextProjection()), &nextPayload); err != nil {
		t.Fatal(err)
	}
	if nextPayload["continuation"].(map[string]any)["recovery_tool"] != "recover_tool_result" {
		t.Fatal("later recovered page lacks next-tool guidance")
	}
	other, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	other.SetSessionDir(t.TempDir())
	if _, err = call(other, context.Background(), cursor, "recover_tool_result"); err == nil {
		t.Fatal("cross-session token accepted")
	}
	kit.DisableTools("read_file")
	if _, err = call(kit, context.Background(), cursor, "recover_tool_result"); err == nil {
		t.Fatal("read_file restriction bypassed")
	}
	kit.EnableTools("read_file")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = call(kit, canceled, cursor, "recover_tool_result"); err == nil {
		t.Fatal("cancelled recovery accepted")
	}
	line := encodeReadFileContinuation(filepath.Join(root, "x"), 1, 10, "hash")
	if _, err = call(kit, context.Background(), line, "recover_tool_result"); err == nil {
		t.Fatal("workspace line cursor accepted")
	}
	stale := page["artifact_ref"].(string)
	if err := os.WriteFile(stale, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = call(kit, context.Background(), cursor, "recover_tool_result"); err == nil {
		t.Fatal("stale result accepted")
	}
	if got := fmt.Sprint(page["continuation"].(map[string]any)["recovery_tool"]); got != "recover_tool_result" {
		t.Fatalf("recovery guidance: %q", got)
	}
}

type denyReadRecoveryAuthorizer struct{}

func (denyReadRecoveryAuthorizer) Authorize(_ context.Context, request AuthorizationRequest) (AuthorizationDecision, error) {
	if request.Tool.Name == "read_file" {
		return AuthorizationDecision{Outcome: "deny", Reason: "read denied"}, nil
	}
	return AuthorizationDecision{Outcome: "allow"}, nil
}
func TestResultRecoveryRetainsReadAuthorizationAndScope(t *testing.T) {
	root, session := t.TempDir(), t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionDir(session)
	other := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(other, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	token := encodeReadFileByteContinuation(other, 1, 5, 7, sha256Hex([]byte("private")))
	args, _ := json.Marshal(map[string]any{"continuation": token})
	call := providers.ToolCall{ID: "forged", Name: "recover_tool_result", Arguments: string(args)}
	if _, err := kit.ExecuteResult(context.Background(), call); err == nil {
		t.Fatal("forged workspace path accepted")
	}
	path := filepath.Join(session, "tool-results", strings.Repeat("a", 64)+".txt")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("result"), 0600); err != nil {
		t.Fatal(err)
	}
	token = encodeReadFileByteContinuation(path, 1, 5, 6, sha256Hex([]byte("result")))
	args, _ = json.Marshal(map[string]any{"continuation": token})
	call.Arguments = string(args)
	legacy := call
	legacy.Name = "read_file"
	original, err := kit.ExecuteResult(context.Background(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := kit.ExecuteResult(context.Background(), call)
	if err != nil {
		t.Fatalf("ordinary session result failed: %v", err)
	}
	var nativePage, recoveryPage map[string]any
	if json.Unmarshal([]byte(original.TextProjection()), &nativePage) != nil || json.Unmarshal([]byte(recovered.TextProjection()), &recoveryPage) != nil || nativePage["content"] != recoveryPage["content"] {
		t.Fatal("recovery content differs from read_file")
	}
	kit.SetAuthorizer(denyReadRecoveryAuthorizer{})
	if _, err := kit.ExecuteResult(context.Background(), call); err == nil {
		t.Fatal("read_file authorization bypassed")
	}
}

func TestRecoveryIsUnavailableWhenReadFileDisabled(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	kit.DisableTools("read_file")
	if kit.SupportsTool("recover_tool_result") {
		t.Fatal("recovery remained available with read_file disabled")
	}
	for _, d := range kit.Definitions() {
		if d.Name == "recover_tool_result" {
			t.Fatal("recovery remained advertised with read_file disabled")
		}
	}
	if _, found := kit.ActiveSurface().Tools["recover_tool_result"]; found {
		t.Fatal("prompt surface advertises disabled recovery")
	}
}
