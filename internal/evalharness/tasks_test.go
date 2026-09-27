package evalharness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
)

func TestCatalogHasStableTaskIDs(t *testing.T) {
	tasks := Catalog()
	seen := map[string]bool{}
	for _, task := range tasks {
		if task.ID == "" {
			t.Fatal("task id must not be empty")
		}
		if seen[task.ID] {
			t.Fatalf("duplicate task id %q", task.ID)
		}
		seen[task.ID] = true
		if task.Prompt == "" || task.Setup == nil || task.Verify == nil {
			t.Fatalf("task %q is incomplete: %+v", task.ID, task)
		}
		if len(task.RequiredTools) == 0 {
			t.Fatalf("task %q should declare required tools", task.ID)
		}
	}
}

func TestTestFailureFixVerification(t *testing.T) {
	task, ok := ByID("test_failure_fix")
	if !ok {
		t.Fatal("missing test_failure_fix task")
	}
	root := t.TempDir()
	if err := SetupTask(task, root); err != nil {
		t.Fatalf("SetupTask: %v", err)
	}

	failed, err := VerifyTask(context.Background(), task, root, "")
	if err != nil {
		t.Fatalf("VerifyTask failed module: %v", err)
	}
	if failed.Passed {
		t.Fatal("buggy fixture should fail verification")
	}

	fixed := `package evaltask

func Add(a, b int) int {
	return a + b
}
`
	if err := os.WriteFile(filepath.Join(root, "calc.go"), []byte(fixed), 0o644); err != nil {
		t.Fatalf("write fixed file: %v", err)
	}
	passed, err := VerifyTask(context.Background(), task, root, "")
	if err != nil {
		t.Fatalf("VerifyTask fixed module: %v", err)
	}
	if !passed.Passed {
		t.Fatalf("fixed fixture should pass verification: %s", passed.Reason)
	}
	if len(passed.Evidence) == 0 || passed.Evidence[0].Command != "go test ./..." || !passed.Evidence[0].Passed {
		t.Fatalf("fixed fixture should include passing command evidence: %+v", passed.Evidence)
	}
}

func TestGitTestFailureFixVerification(t *testing.T) {
	task, ok := ByID("git_test_failure_fix")
	if !ok {
		t.Fatal("missing git_test_failure_fix task")
	}
	root := t.TempDir()
	if err := SetupTask(task, root); err != nil {
		t.Fatalf("SetupTask: %v", err)
	}

	status := exec.Command("git", "status", "--porcelain")
	status.Dir = root
	if out, err := status.CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("expected clean git fixture, err=%v out=%s", err, string(out))
	}
	failed, err := VerifyTask(context.Background(), task, root, "")
	if err != nil {
		t.Fatalf("VerifyTask failed module: %v", err)
	}
	if failed.Passed {
		t.Fatal("buggy git fixture should fail verification")
	}

	fixed := `package evaltask

func Add(a, b int) int {
	return a + b
}
`
	if err := os.WriteFile(filepath.Join(root, "calc.go"), []byte(fixed), 0o644); err != nil {
		t.Fatalf("write fixed file: %v", err)
	}
	passed, err := VerifyTask(context.Background(), task, root, "")
	if err != nil {
		t.Fatalf("VerifyTask fixed module: %v", err)
	}
	if !passed.Passed {
		t.Fatalf("fixed git fixture should pass verification: %s", passed.Reason)
	}
	if len(passed.Evidence) < 2 || passed.Evidence[1].Check != "git diff files" || !passed.Evidence[1].Passed {
		t.Fatalf("fixed fixture should include passing git diff evidence: %+v", passed.Evidence)
	}

	if err := os.WriteFile(filepath.Join(root, "calc_test.go"), []byte("package evaltask\n"), 0o644); err != nil {
		t.Fatalf("write changed test file: %v", err)
	}
	failed, err = VerifyTask(context.Background(), task, root, "")
	if err != nil {
		t.Fatalf("VerifyTask changed test: %v", err)
	}
	if failed.Passed {
		t.Fatal("changing tests should fail git diff verification")
	}
}

func TestPatchReviewRiskVerification(t *testing.T) {
	task, ok := ByID("patch_review_risk")
	if !ok {
		t.Fatal("missing patch_review_risk task")
	}
	root := t.TempDir()
	if err := SetupTask(task, root); err != nil {
		t.Fatalf("SetupTask: %v", err)
	}

	failed, err := VerifyTask(context.Background(), task, root, "")
	if err != nil {
		t.Fatalf("VerifyTask failed module: %v", err)
	}
	if failed.Passed {
		t.Fatal("buggy patch review risk fixture should fail verification")
	}

	subtotal := `package pricing

func Subtotal(cents []int) int {
	total := 0
	for _, item := range cents {
		total += item
	}
	return total
}
`
	if err := os.WriteFile(filepath.Join(root, "pricing", "subtotal.go"), []byte(subtotal), 0o644); err != nil {
		t.Fatalf("write subtotal: %v", err)
	}
	tax := `package pricing

func TotalWithTax(cents []int, taxBasisPoints int) int {
	subtotal := Subtotal(cents)
	return subtotal + subtotal*taxBasisPoints/10000
}
`
	if err := os.WriteFile(filepath.Join(root, "pricing", "tax.go"), []byte(tax), 0o644); err != nil {
		t.Fatalf("write tax: %v", err)
	}
	passed, err := VerifyTask(context.Background(), task, root, "")
	if err != nil {
		t.Fatalf("VerifyTask fixed module: %v", err)
	}
	if !passed.Passed {
		t.Fatalf("fixed patch review risk fixture should pass verification: %s", passed.Reason)
	}
	if len(passed.Evidence) < 2 || passed.Evidence[1].Check != "git diff files" || !passed.Evidence[1].Passed {
		t.Fatalf("fixed fixture should include passing git diff evidence: %+v", passed.Evidence)
	}

	if err := os.WriteFile(filepath.Join(root, "pricing", "pricing_test.go"), []byte("package pricing\n"), 0o644); err != nil {
		t.Fatalf("write changed test file: %v", err)
	}
	failed, err = VerifyTask(context.Background(), task, root, "")
	if err != nil {
		t.Fatalf("VerifyTask changed test: %v", err)
	}
	if failed.Passed {
		t.Fatal("changing tests should fail patch review risk verification")
	}
}

func TestMCPReadOnlyConcurrencyVerification(t *testing.T) {
	task, ok := ByID("mcp_readonly_concurrency")
	if !ok {
		t.Fatal("missing mcp_readonly_concurrency task")
	}
	root := t.TempDir()
	if err := SetupTask(task, root); err != nil {
		t.Fatalf("SetupTask: %v", err)
	}

	failed, err := VerifyTask(context.Background(), task, root, "")
	if err != nil {
		t.Fatalf("VerifyTask missing marker: %v", err)
	}
	if failed.Passed {
		t.Fatal("missing MCP marker should fail verification")
	}

	if err := os.WriteFile(filepath.Join(root, "mcp_readonly_result.txt"), []byte("MCP_READONLY_CONCURRENT\n"), 0o644); err != nil {
		t.Fatalf("write marker file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "mcp_max_concurrency.txt"), []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write concurrency file: %v", err)
	}
	failed, err = VerifyTask(context.Background(), task, root, "")
	if err != nil {
		t.Fatalf("VerifyTask serial MCP calls: %v", err)
	}
	if failed.Passed {
		t.Fatal("serial MCP calls should fail verification")
	}

	if err := os.WriteFile(filepath.Join(root, "mcp_max_concurrency.txt"), []byte("2\n"), 0o644); err != nil {
		t.Fatalf("write concurrency file: %v", err)
	}
	passed, err := VerifyTask(context.Background(), task, root, "")
	if err != nil {
		t.Fatalf("VerifyTask concurrent MCP calls: %v", err)
	}
	if !passed.Passed {
		t.Fatalf("concurrent MCP calls should pass verification: %s", passed.Reason)
	}
}

func TestMCPReadOnlyConcurrencyConfiguresLocalServer(t *testing.T) {
	task, ok := ByID("mcp_readonly_concurrency")
	if !ok {
		t.Fatal("missing mcp_readonly_concurrency task")
	}
	if task.Configure == nil {
		t.Fatal("mcp_readonly_concurrency should configure an MCP server")
	}

	root := t.TempDir()
	base := config.Config{
		MCPServers: map[string]config.MCPServerConfig{
			"existing": {Command: "existing-mcp"},
		},
	}
	cfg := task.Configure(root, base)
	if cfg.MCPServers["existing"].Command != "existing-mcp" {
		t.Fatalf("existing MCP server not preserved: %+v", cfg.MCPServers)
	}
	if _, ok := cfg.MCPServers["eval"]; !ok {
		t.Fatalf("eval MCP server not configured: %+v", cfg.MCPServers)
	}
	if _, mutated := base.MCPServers["eval"]; mutated {
		t.Fatal("Configure must not mutate the base config map")
	}
}

func TestMCPReadOnlyConcurrencyServerSourceCompiles(t *testing.T) {
	task, ok := ByID("mcp_readonly_concurrency")
	if !ok {
		t.Fatal("missing mcp_readonly_concurrency task")
	}
	root := t.TempDir()
	if err := SetupTask(task, root); err != nil {
		t.Fatalf("SetupTask: %v", err)
	}

	cmd := exec.Command("go", "test", filepath.Join(root, "mcp_eval_server.go"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("MCP eval server source should compile: %v\n%s", err, output)
	}
}
