package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type wakeTestReviewer struct{ reason string }

func (r wakeTestReviewer) Review(context.Context, approvefor.Request) (approvefor.Decision, error) {
	return approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: r.reason}, nil
}

func TestWorkerWakeRefreshesApproveForMe(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	parent, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ConfigureToolkitPermissions(parent, config.ResolvedPermissions{Mode: "unconfined"})
	worker, err := parent.CloneForRoot(parent.RootDir())
	if err != nil {
		t.Fatal(err)
	}
	ConfigureToolkitPermissions(parent, config.ResolvedPermissions{Mode: "standard"})
	parent.SetApproveForMe(true)
	call := providers.ToolCall{Name: "probe", Arguments: `{}`}
	metadata := agent.ToolMetadata{Risk: "high", Destructive: true}
	for _, reason := range []string{"first turn reviewer", "next turn reviewer"} {
		parent.SetReviewer(wakeTestReviewer{reason: reason})
		workerWakeAuthority(parent)(worker)
		if !worker.ApproveForMe() {
			t.Fatal("worker approval stayed disabled")
		}
		err := worker.AuthorizeTool(context.Background(), call, metadata)
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Fatalf("worker used stale authority: %v", err)
		}
	}
	parent.SetApproveForMe(false)
	parent.SetReviewer(nil)
	workerWakeAuthority(parent)(worker)
	if worker.ApproveForMe() {
		t.Fatal("worker approval stayed enabled")
	}
	if err := worker.AuthorizeTool(context.Background(), call, metadata); err != nil {
		t.Fatalf("disabled reviewer still runs: %v", err)
	}
}
