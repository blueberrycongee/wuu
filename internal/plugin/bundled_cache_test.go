package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func discoverCacheTestPlugins(home string) []Plugin {
	return DiscoverWithOptions("", home, DiscoverOptions{
		GOOS: "darwin", WuuVersion: "1.0.0",
		LookPath:  func(command string) (string, error) { return command, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
	})
}

func cacheTestPluginIDs(home string) []string {
	var ids []string
	for _, item := range discoverCacheTestPlugins(home) {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestBundledCacheConcurrentProcesses(t *testing.T) {
	const childHomeEnv = "WUU_TEST_BUNDLED_CACHE_HOME"
	if home := os.Getenv(childHomeEnv); home != "" {
		fmt.Println("ready")
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(cacheTestPluginIDs(home)); err != nil {
			t.Fatal(err)
		}
		return
	}
	want := cacheTestPluginIDs(t.TempDir())
	if len(want) == 0 {
		t.Fatal("serial discovery found no bundled plugins")
	}
	home := t.TempDir()
	// Model an upgrade from an existing cache, with enough old assets that
	// competing removals overlap as they do when workspace backends start.
	legacy := filepath.Join(home, "cache", "plugins", ".bundled")
	for i := 0; i < 128; i++ {
		asset := filepath.Join(legacy, fmt.Sprintf("old-plugin-%03d", i), "desktop.js")
		if err := os.MkdirAll(filepath.Dir(asset), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(asset, []byte("previous generation"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	type child struct {
		cmd    *exec.Cmd
		input  io.WriteCloser
		output *bufio.Reader
		stderr bytes.Buffer
	}
	children := make([]child, 6)
	for i := range children {
		c := &children[i]
		c.cmd = exec.CommandContext(ctx, executable, "-test.run=^TestBundledCacheConcurrentProcesses$")
		c.cmd.Env = append(os.Environ(), childHomeEnv+"="+home)
		c.cmd.Stderr = &c.stderr
		c.input, err = c.cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		output, err := c.cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		c.output = bufio.NewReader(output)
		if err := c.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = c.input.Close()
			if c.cmd.ProcessState == nil {
				_ = c.cmd.Wait()
			}
		})
	}
	// All processes reach the same starting line before any can unpack. No
	// sleeps or dependency on subprocess startup speed are needed.
	for i := range children {
		if line, err := children[i].output.ReadString('\n'); err != nil || line != "ready\n" {
			t.Fatalf("child %d not ready: %q, %v", i, line, err)
		}
	}
	for i := range children {
		if err := children[i].input.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for i := range children {
		c := &children[i]
		var got []string
		if err := json.NewDecoder(c.output).Decode(&got); err != nil {
			t.Errorf("child %d inventory: %v", i, err)
		} else if !reflect.DeepEqual(got, want) {
			t.Errorf("child %d plugins = %v, want %v", i, got, want)
		}
		if err := c.cmd.Wait(); err != nil {
			t.Errorf("child %d: %v\n%s", i, err, c.stderr.String())
		}
	}
	if got := cacheTestPluginIDs(home); !reflect.DeepEqual(got, want) {
		t.Fatalf("subsequent discovery = %v, want %v", got, want)
	}
}

func TestBundledCacheRepairsDamageWithMatchingFingerprint(t *testing.T) {
	for _, damage := range []string{"missing-plugin", "truncated-asset"} {
		t.Run(damage, func(t *testing.T) {
			home := t.TempDir()
			want := cacheTestPluginIDs(home)
			root, err := materializeBundled(home)
			if err != nil {
				t.Fatal(err)
			}
			asset := filepath.Join(root, "todo", "desktop.js")
			original, err := os.ReadFile(asset)
			if err != nil {
				t.Fatal(err)
			}
			if damage == "missing-plugin" {
				err = os.RemoveAll(filepath.Join(root, "todo"))
			} else {
				err = os.WriteFile(asset, []byte("truncated"), 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := cacheTestPluginIDs(home); !reflect.DeepEqual(got, want) {
				t.Errorf("recovered inventory = %v, want %v", got, want)
			}
			if got, err := os.ReadFile(asset); err != nil || !bytes.Equal(got, original) {
				t.Fatalf("runtime asset was not restored: %v", err)
			}
		})
	}
}

func TestBundledCachePreservesOtherRunningBuilds(t *testing.T) {
	home := t.TempDir()
	// Old binaries still delete/rewrite .bundled. New builds must neither use
	// that directory nor evict the asset paths held by another generation.
	for _, relative := range []string{".bundled", filepath.Join(".bundled-generations", "older-build")} {
		asset := filepath.Join(home, "cache", "plugins", relative, "todo", "desktop.js")
		if err := os.MkdirAll(filepath.Dir(asset), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(asset, []byte("running build asset"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plugins := discoverCacheTestPlugins(home)
	if len(plugins) == 0 {
		t.Fatal("new build found no plugins")
	}
	for _, relative := range []string{".bundled", filepath.Join(".bundled-generations", "older-build")} {
		asset := filepath.Join(home, "cache", "plugins", relative, "todo", "desktop.js")
		if got, err := os.ReadFile(asset); err != nil || string(got) != "running build asset" {
			t.Fatalf("other build asset changed at %s: %v", asset, err)
		}
	}
	if err := os.RemoveAll(filepath.Join(home, "cache", "plugins", ".bundled")); err != nil {
		t.Fatal(err)
	}
	for _, item := range plugins {
		if _, err := os.ReadFile(item.ManifestPath); err != nil {
			t.Errorf("legacy cache replacement broke %s: %v", item.ID, err)
		}
	}
}
