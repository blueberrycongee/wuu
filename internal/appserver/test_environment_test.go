package appserver

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAppserverTestsPreserveInheritedWuuHome(t *testing.T) {
	protectedHome := t.TempDir()
	files := map[string][]byte{
		"config.json": []byte("{\"default_provider\":\"protected\",\"providers\":{}}\n"),
		"auth.json":   []byte("{\"version\":1,\"providers\":{}}\n"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(protectedHome, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// Exercise the real config and credential writers that escaped test isolation.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^(TestProviderHasAuthChecksCodexOAuthAvailability|TestPluginPackageUpdateWaitsForExactApprovalBeforePromotion)$", "-test.count=1")
	t.Setenv("WUU_HOME", protectedHome)
	cmd.Env = os.Environ()
	output, runErr := cmd.CombinedOutput()
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(protectedHome, name))
		if err != nil {
			t.Error(err)
		} else if !bytes.Equal(got, want) {
			t.Errorf("child tests modified inherited WUU_HOME/%s", name)
		}
	}
	entries, err := os.ReadDir(protectedHome)
	if err != nil {
		t.Error(err)
	} else if len(entries) != len(files) {
		t.Errorf("child tests created state in inherited WUU_HOME: %v", entries)
	}
	if runErr != nil {
		t.Fatalf("child tests failed: %v\n%s", runErr, output)
	}
}
