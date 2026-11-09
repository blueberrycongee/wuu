package tools

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/processsandbox"
)

func newShellTestToolkit(t *testing.T, root string) *Toolkit {
	t.Helper()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	enableShellExecutionForTest(kit.env)
	return kit
}

func enableShellExecutionForTest(env *Env) {
	// These tests exercise shell behavior; sandbox enforcement has dedicated
	// coverage and must not make unrelated tests platform-dependent.
	if env != nil {
		env.Unconfined = true
	}
}

func newShellTestEnv(root string) *Env {
	env := &Env{RootDir: root}
	if !processsandbox.Supported() {
		env.Unconfined = true
	}
	return env
}
