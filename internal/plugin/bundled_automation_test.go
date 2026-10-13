package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundledAutomationResolvesRuntimeHelper(t *testing.T) {
	plugins := discoverBundled(t.TempDir(), DiscoverOptions{
		GOOS:      "darwin",
		LookPath:  func(command string) (string, error) { return command, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	for _, item := range plugins {
		if item.ID != "automation" {
			continue
		}
		if item.Runtime == nil || item.Runtime.Command != "node" || len(item.Runtime.Args) != 1 || item.Runtime.Args[0] != "runtime.mjs" {
			t.Fatalf("automation runtime = %+v", item.Runtime)
		}
		if len(item.OfficialNativeHelper) != 0 {
			t.Fatal("TypeScript runtime must not require a privileged native helper")
		}
		if _, err := os.Stat(filepath.Join(item.Root, item.Runtime.Args[0])); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("bundled automation plugin was not discovered")
}
