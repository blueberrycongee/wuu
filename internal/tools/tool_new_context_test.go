package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestContextRequestsDoNotRequireWorkspaceMutations(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetContextWindowToolsEnabled(true)
	ctx := context.Background()
	before := workspaceRevision(ctx, kit.env.RootDir, kit.env.RuntimeHome)
	if before == "" {
		t.Fatal("test needs a stable workspace revision")
	}
	for i := 0; i < 4; i++ {
		result, err := kit.ExecuteResult(ctx, providers.ToolCall{ID: fmt.Sprintf("reset-%d", i), Name: newContextToolName, Arguments: `{}`})
		if err != nil || result.IsError {
			t.Fatalf("request %d: %+v %v", i, result, err)
		}
		var signal struct {
			Requested bool `json:"requested"`
		}
		if err := json.Unmarshal([]byte(result.TextProjection()), &signal); err != nil || !signal.Requested {
			t.Fatalf("request %d lost control signal: %+v %v", i, result, err)
		}
	}
	if after := workspaceRevision(ctx, kit.env.RootDir, kit.env.RuntimeHome); after != before {
		t.Fatal("context requests mutated the workspace")
	}
}
