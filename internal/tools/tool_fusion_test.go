package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestFusionUnsettledHandoffBlocksLeadWritesUntilRecovery(t *testing.T) {
	for _, recovery := range []string{"delegation", "next turn cleanup"} {
		t.Run(recovery, func(t *testing.T) {
			t.Setenv("WUU_HOME", t.TempDir())
			root := t.TempDir()
			kit, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			settled := false
			kit.SetFusionDelegate(func(context.Context, string) (string, error) {
				if !settled {
					return "", &FusionHandoffError{Err: errors.New("process still active")}
				}
				return `{"outcome":"completed","summary":"stopped"}`, nil
			})
			delegate := providers.ToolCall{Name: "fusion_delegate", Arguments: `{"brief":"finish work"}`}
			if _, err := kit.Execute(context.Background(), delegate); err == nil {
				t.Fatal("unsafe handoff succeeded")
			}
			write := providers.ToolCall{Name: "write_file", Arguments: `{"path":"result.txt","content":"lead write"}`}
			if _, err := kit.Execute(context.Background(), write); err == nil {
				t.Fatal("Lead wrote while Sidekick was unsettled")
			}
			if _, err := os.Stat(filepath.Join(root, "result.txt")); !os.IsNotExist(err) {
				t.Fatal("Lead write escaped handoff gate")
			}
			settled = true
			if recovery == "delegation" {
				if _, err := kit.Execute(context.Background(), delegate); err != nil {
					t.Fatal(err)
				}
			} else {
				kit.ClearFusionHandoff()
			}
			if _, err := kit.Execute(context.Background(), write); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(root, "result.txt"))
			if err != nil || string(content) != "lead write" {
				t.Fatalf("Lead could not take over: %q, %v", content, err)
			}
		})
	}
}
