package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestRoomCoordinatorCanReadEvidenceButCannotExecuteOrPublish(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "evidence.txt"), "verified evidence")
	for _, profile := range []modelprofile.Profile{{}, modelprofile.Resolve("openai", "gpt-5.5"), modelprofile.Resolve("anthropic", "claude-sonnet-4-5")} {
		kit, err := New(root)
		if err != nil {
			t.Fatal(err)
		}
		kit.setActiveProfileForSurface(profile, modelprofile.SurfaceRoomAgent)
		result, err := kit.Execute(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"evidence.txt"}`})
		if err != nil || !strings.Contains(result, "verified evidence") {
			t.Fatalf("read evidence: %s %v", result, err)
		}
		for _, name := range []string{"write_file", "edit_file", "apply_patch", "bash", "git", "wuu_browser", "chat_send", "chat_draft", "thread_get", "create_agent_profile", "exec"} {
			if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: `{}`}); err == nil {
				t.Fatalf("%s escaped coordinator surface: %v", name, err)
			}
		}
	}
}
