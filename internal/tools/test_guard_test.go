package tools

import (
	"strings"
	"testing"
)

func TestTestContractWarningFlagsRemovedGoTest(t *testing.T) {
	oldContent := "package x\n\nfunc TestKeepsContract(t *testing.T) {}\n\nfunc TestOther(t *testing.T) {}\n"
	newContent := "package x\n\nfunc TestOther(t *testing.T) {}\n"
	warning := testContractWarning("internal/appserver/server_test.go", oldContent, newContent)
	if !strings.Contains(warning, "TestKeepsContract") {
		t.Fatalf("warning should name the removed test, got %q", warning)
	}
}

func TestTestContractWarningFlagsRenamedGoTest(t *testing.T) {
	oldContent := "func TestServerFailedTurnDoesNotPersistPartialToolHistory(t *testing.T) {}\n"
	newContent := "func TestServerFailedTurnPersistsPartialToolHistory(t *testing.T) {}\n"
	warning := testContractWarning("server_test.go", oldContent, newContent)
	if !strings.Contains(warning, "TestServerFailedTurnDoesNotPersistPartialToolHistory") {
		t.Fatalf("rename must be flagged as removal of the old name, got %q", warning)
	}
}

func TestTestContractWarningSilentOnAdditiveEdit(t *testing.T) {
	oldContent := "func TestA(t *testing.T) {}\n"
	newContent := "func TestA(t *testing.T) {}\n\nfunc TestB(t *testing.T) {}\n"
	if warning := testContractWarning("x_test.go", oldContent, newContent); warning != "" {
		t.Fatalf("additive edit must not warn, got %q", warning)
	}
}

func TestTestContractWarningSilentOnNonTestFile(t *testing.T) {
	oldContent := "func TestLooking(t *testing.T) {}\n"
	if warning := testContractWarning("internal/agent/loop.go", oldContent, ""); warning != "" {
		t.Fatalf("non-test files must not warn, got %q", warning)
	}
}

func TestTestContractWarningFlagsRemovedTSTest(t *testing.T) {
	oldContent := "test(\"durably closes partial text\", () => {});\nit(\"keeps order\", () => {});\n"
	newContent := "it(\"keeps order\", () => {});\n"
	warning := testContractWarning("packages/core/test/session.test.ts", oldContent, newContent)
	if !strings.Contains(warning, "durably closes partial text") {
		t.Fatalf("removed TS test must be flagged, got %q", warning)
	}
}
