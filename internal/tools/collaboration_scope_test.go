package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestCollaborationRoleChangesFenceWritesAtExecution(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("anthropic", "claude-sonnet-4-5"), true)
	call := providers.ToolCall{Name: "write_file", Arguments: `{"path":"result.txt","content":"candidate"}`}
	for _, role := range []channels.CollaborationSessionPurpose{channels.CollaborationSessionConversation, channels.CollaborationSessionCoordination, channels.CollaborationSessionVerification} {
		kit.SetCollaborationScope(role, "room", "work")
		assertDefinitionMissing(t, kit.Definitions(), "write_file")
		if _, err := kit.Execute(context.Background(), call); err == nil {
			t.Fatalf("%s wrote a file", role)
		}
		if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: "bash", Arguments: `{"command":"touch escaped.txt"}`}); err == nil {
			t.Fatalf("%s executed a shell command", role)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "result.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied write changed disk: %v", err)
	}
	kit.SetCollaborationScope(channels.CollaborationSessionWork, "room", "work")
	if _, err := kit.Execute(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	kit.SetCollaborationScope(channels.CollaborationSessionConversation, "room", "work")
	if _, err := kit.Execute(context.Background(), call); err == nil {
		t.Fatal("previous executor role retained write access")
	}
	body, err := os.ReadFile(filepath.Join(root, "result.txt"))
	if err != nil || string(body) != "candidate" {
		t.Fatalf("execution result %q: %v", body, err)
	}
}
