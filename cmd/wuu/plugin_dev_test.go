package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestPluginCreateProducesDistinctStandaloneTemplates(t *testing.T) {
	for _, pluginType := range []string{"agent", "desktop", "full"} {
		t.Run(pluginType, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "sample")
			if err := runPluginCreate([]string{"--type", pluginType, "--output", dir, "sample"}); err != nil {
				t.Fatalf("create: %v", err)
			}
			packageData, err := os.ReadFile(filepath.Join(dir, "package.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(packageData), "workspace:") || !strings.Contains(string(packageData), `"@wuu/plugin-sdk": "^0.1.0"`) {
				t.Fatalf("package is not standalone-installable:\n%s", packageData)
			}
			for _, relative := range map[string][]string{
				"agent": {"src/index.ts"}, "desktop": {"src/index.ts"}, "full": {"src/runtime.ts", "src/renderer.ts"},
			}[pluginType] {
				data, err := os.ReadFile(filepath.Join(dir, relative))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), `from "@wuu/plugin-sdk"`) {
					t.Fatalf("%s does not use the public SDK", relative)
				}
			}
		})
	}
}

func TestPluginCreateRejectsUnknownTypeAndExistingOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sample")
	if err := runPluginCreate([]string{"--type", "unknown", "--output", dir, "sample"}); err == nil {
		t.Fatal("unknown type succeeded")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("unknown type created output: %v", err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "keep")
	writePluginDevTestFile(t, marker, "keep")
	if err := runPluginCreate([]string{"--output", dir, "sample"}); err == nil {
		t.Fatal("existing output was overwritten")
	}
	if data, _ := os.ReadFile(marker); string(data) != "keep" {
		t.Fatal("existing output changed")
	}
}

func TestPluginBuildExecutesScriptAndRequiresArtifact(t *testing.T) {
	dir := t.TempDir()
	writePluginDevTestFile(t, filepath.Join(dir, "plugin.json"), `{"schema_version":1,"id":"build-test","version":"1.0.0","desktop":{"entry":"dist/index.js"}}`)
	writePluginDevTestFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"build":"custom-build"}}`)
	manager := writePluginDevTestManager(t, `mkdir -p dist; printf 'export {};' > dist/index.js; printf ran > build.marker`)
	if err := runPluginBuild([]string{"--package-manager", manager, dir}); err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "build.marker")); err != nil {
		t.Fatalf("build script did not run: %v", err)
	}
	manager = writePluginDevTestManager(t, `rm -rf dist`)
	if err := runPluginBuild([]string{"--package-manager", manager, dir}); err == nil || !strings.Contains(err.Error(), "artifact") {
		t.Fatalf("missing artifact error = %v", err)
	}
}

func TestPluginTestRunsExecutableRuntimeContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}
	dir := t.TempDir()
	writePluginDevTestFile(t, filepath.Join(dir, "plugin.json"), `{"schema_version":1,"id":"runtime-test","version":"1.0.0","runtime":{"protocol":"wuu-plugin-v1","command":"node","args":["runtime.js"]}}`)
	writePluginDevTestFile(t, filepath.Join(dir, "runtime.js"), `
import readline from "node:readline";
const lines = readline.createInterface({input: process.stdin});
lines.on("line", line => { const request = JSON.parse(line); process.stdout.write(JSON.stringify({id: request.id, result: request.method === "initialize" ? {hooks: [], protocol_version: 2, capabilities: [{id: "agent.request.transform", kind: "transform", version: 1}], tools: [{id: "echo", description: "Echo input", input_schema: {type: "object"}}]} : null}) + "\n"); });
`)
	diagnostics := testPluginPackage(dir, 30*time.Second)
	checks := make(map[string]pluginDiagnostic, len(diagnostics))
	for _, diagnostic := range diagnostics {
		checks[diagnostic.Check] = diagnostic
		if diagnostic.Level == "fail" {
			t.Fatalf("runtime contract failed: %+v", diagnostics)
		}
	}
	for _, check := range []string{"runtime.initialize", "runtime.protocol", "runtime.capabilities", "runtime.tools"} {
		if checks[check].Level != "pass" {
			t.Fatalf("missing acceptance check %s: %+v", check, diagnostics)
		}
	}
	if !strings.Contains(checks["runtime.capabilities"].Message, "1 capability") || !strings.Contains(checks["runtime.tools"].Message, "echo") {
		t.Fatalf("descriptor and tool details were not reported: %+v", diagnostics)
	}
	writePluginDevTestFile(t, filepath.Join(dir, "runtime.js"), `process.stdout.write("not-json\n")`)
	diagnostics = testPluginPackage(dir, 5*time.Second)
	if diagnostics[len(diagnostics)-1].Level != "fail" {
		t.Fatalf("invalid runtime false-positive: %+v", diagnostics)
	}
	var runErr error
	output := captureStdout(t, func() {
		runErr = runPluginTest([]string{"--json", "--timeout", "5s", dir})
	})
	if runErr == nil {
		t.Fatal("plugin test returned success for a fail diagnostic")
	}
	var result pluginTestOutput
	if err := json.Unmarshal([]byte(output), &result); err != nil || result.OK {
		t.Fatalf("structured failure output = %q, %v", output, err)
	}
}

func TestDevRefreshIsAtomicAndSeparateFromNormalInstall(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	writePluginDevTestFile(t, filepath.Join(dir, "plugin.json"), `{"schema_version":1,"id":"dev-test","version":"1.0.0","desktop":{"entry":"dist/index.js"}}`)
	writePluginDevTestFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"build":"custom-build"}}`)
	writePluginDevTestFile(t, filepath.Join(dir, "src.js"), "export const value = 'old';")
	writePluginDevTestFile(t, filepath.Join(dir, "node_modules", "dev-only", "index.js"), "not packaged")
	devAuthorizationDir := filepath.Join(home, "dev", "plugins")
	if err := os.MkdirAll(devAuthorizationDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := authorizeDevDirectory(devAuthorizationDir, "dev-test", dir); err != nil {
		t.Fatal(err)
	}
	initialLease, acquired, err := session.TryAcquirePluginGenerationExecutionLease(home)
	if err != nil || !acquired {
		t.Fatalf("acquire initial generation: %v, %v", acquired, err)
	}
	initialEpoch := initialLease.Epoch()
	if err := initialLease.Release(); err != nil {
		t.Fatal(err)
	}
	manager := writePluginDevTestManager(t, `mkdir -p dist; cp src.js dist/index.js`)
	if diagnostic, err := refreshDevGeneration(context.Background(), home, dir, manager); err != nil || diagnostic.Level != "pass" {
		t.Fatalf("first refresh = %+v, %v", diagnostic, err)
	}
	devArtifact := filepath.Join(home, "dev", "generations", "dev-test", "package", "dist", "index.js")
	before, err := os.ReadFile(devArtifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "plugins", "dev-test")); !os.IsNotExist(err) {
		t.Fatalf("dev generation leaked into normal installs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "dev", "generations", "dev-test", "package", "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("development dependencies leaked into generation: %v", err)
	}
	execution, acquired, err := session.TryAcquirePluginGenerationExecutionLease(home)
	if err != nil || !acquired {
		t.Fatalf("acquire execution generation: %v, %v", acquired, err)
	}
	if execution.Epoch() != initialEpoch+1 {
		t.Fatalf("published generation epoch = %d, want %d", execution.Epoch(), initialEpoch+1)
	}
	writePluginDevTestFile(t, filepath.Join(dir, "src.js"), "export const value = 'live';")
	if diagnostic, err := refreshDevGeneration(context.Background(), home, dir, manager); err != nil || diagnostic.Level != "pass" {
		t.Fatalf("live refresh while execution owns generation = %+v, %v", diagnostic, err)
	}
	if err := execution.Release(); err != nil {
		t.Fatal(err)
	}
	blocked, err := os.ReadFile(devArtifact)
	if err != nil || string(blocked) == string(before) {
		t.Fatalf("execution-compatible refresh did not publish generation: %q, %v", blocked, err)
	}
	before = blocked
	failingManager := writePluginDevTestManager(t, `exit 7`)
	if diagnostic, err := refreshDevGeneration(context.Background(), home, dir, failingManager); err == nil || diagnostic.Level != "fail" {
		t.Fatalf("failing refresh = %+v, %v", diagnostic, err)
	}
	after, err := os.ReadFile(devArtifact)
	if err != nil || string(after) != string(before) {
		t.Fatalf("previous generation was not preserved: %q, %v", after, err)
	}
}

func TestDevAuthorizationReusesDirectoryWithoutExposingToken(t *testing.T) {
	devDir := t.TempDir()
	pluginDir := t.TempDir()
	if err := authorizeDevDirectory(devDir, "auth-test", pluginDir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(devDir, "auth-test.json")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var auth DevAuthorization
	if err := json.Unmarshal(first, &auth); err != nil || auth.Token == "" {
		t.Fatalf("authorization was not persisted securely: %+v, %v", auth, err)
	}
	if err := authorizeDevDirectory(devDir, "auth-test", pluginDir); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("same directory authorization was regenerated")
	}
}

func writePluginDevTestManager(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manager")
	writePluginDevTestFile(t, path, "#!/bin/sh\n"+body+"\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writePluginDevTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginDevRejectsInvalidPollInterval(t *testing.T) {
	for _, interval := range []string{"0s", "-1s"} {
		err := runPluginDevMode([]string{"--poll", interval, t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "poll interval must be positive") {
			t.Fatalf("poll %s: %v", interval, err)
		}
	}
}

func TestSingleTypeScriptDevPublishesAndPreservesLastGood(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "hello.ts")
	code := `import { KERNEL_SERVICE_METHOD } from "@wuu/plugin-sdk";
import type { RuntimePlugin } from "@wuu/plugin-sdk";
const message: string = "hello";
export default {
 initialize() { return { tools: [{ id: "hello", description: "Say hello", input_schema: { type: "object" } }] }; },
 activate() { if (KERNEL_SERVICE_METHOD !== "call") throw new Error("SDK import failed"); },
 executeTool() { return { result: { content: [{ type: "text", text: message }] } }; }
} satisfies RuntimePlugin;
`
	writePluginDevTestFile(t, source, code)
	id, err := readPluginIdentity(source)
	if err != nil {
		t.Fatal(err)
	}
	authDir := filepath.Join(home, "dev", "plugins")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := authorizeDevDirectory(authDir, id, source); err != nil {
		t.Fatal(err)
	}
	if diagnostic, err := refreshDevGeneration(context.Background(), home, source, ""); err != nil {
		t.Fatalf("publish one TS file: %+v, %v", diagnostic, err)
	}
	packageRoot := filepath.Join(home, "dev", "generations", id, "package")
	manifest, err := pluginpkg.LoadManifest(filepath.Join(packageRoot, "plugin.json"), "dev")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := pluginhost.Start(ctx, pluginhost.ProcessConfig{ID: id, Command: manifest.Runtime.Command, Args: manifest.Runtime.Args, PluginRoot: packageRoot, WuuHome: home, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ExecuteTool(ctx, pluginhost.ToolExecuteParams{ToolID: "hello", ToolExecuteInput: pluginhost.ToolExecuteInput{CallID: "hello-call", Tool: "hello", CWD: t.TempDir(), Arguments: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), "hello") {
		t.Fatalf("tool result = %s, %v", encoded, err)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(packageRoot, "extension.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, broken := range []string{"export default {", "export default {}", `import "./untracked.ts"; export default { initialize() { return {}; } }`} {
		writePluginDevTestFile(t, source, broken)
		if _, err := refreshDevGeneration(context.Background(), home, source, ""); err == nil {
			t.Fatalf("accepted broken TS: %s", broken)
		}
		after, err := os.ReadFile(filepath.Join(packageRoot, "extension.ts"))
		if err != nil || string(after) != string(before) {
			t.Fatalf("failed reload changed last-good code: %s, %v", after, err)
		}
	}
	// Initialization belongs to the real host: it may read kernel services that
	// are unavailable to this source loader. Publication only validates loading.
	writePluginDevTestFile(t, source, `export default { initialize() { throw new Error("actual host must validate initialization"); } };`)
	if _, err := refreshDevGeneration(context.Background(), home, source, ""); err != nil {
		t.Fatalf("source loader ran initialization without a host: %v", err)
	}
	writePluginDevTestFile(t, source, strings.Replace(code, `"hello";`, `"reloaded";`, 1))
	if _, err := refreshDevGeneration(context.Background(), home, source, ""); err != nil {
		t.Fatalf("repair-save did not recover: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(packageRoot, "extension.ts"))
	if err != nil || !strings.Contains(string(after), "reloaded") {
		t.Fatalf("updated source not published: %s, %v", after, err)
	}
}

// This subprocess exercises the CLI's initial-failure path and the real source
// watcher, rather than only calling the publication helper directly.
func TestPluginDevWatchRepairsInitialLoadFailure(t *testing.T) {
	if os.Getenv("WUU_DEV_WATCH_TEST_HELPER") == "1" {
		if err := runPlugin([]string{"dev", "--poll", "20ms", os.Getenv("WUU_DEV_WATCH_TEST_SOURCE")}); err != nil {
			t.Fatal(err)
		}
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("subprocess interrupt requires Unix signals")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}
	source := filepath.Join(t.TempDir(), "watch.ts")
	writePluginDevTestFile(t, source, "export default {")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPluginDevWatchRepairsInitialLoadFailure$")
	cmd.Env = append(os.Environ(), "WUU_DEV_WATCH_TEST_HELPER=1", "WUU_DEV_WATCH_TEST_SOURCE="+source, "WUU_HOME="+t.TempDir())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	diagnostics := make(chan pluginDiagnostic)
	go func() {
		defer close(diagnostics)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var diagnostic pluginDiagnostic
			if json.Unmarshal(scanner.Bytes(), &diagnostic) == nil && diagnostic.Check != "" {
				select {
				case diagnostics <- diagnostic:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	expect := func(level string) {
		t.Helper()
		select {
		case diagnostic, ok := <-diagnostics:
			if !ok || diagnostic.Level != level {
				t.Fatalf("want %s diagnostic; got %+v, stream open=%v", level, diagnostic, ok)
			}
		case <-ctx.Done():
			t.Fatalf("watcher did not report %s: %v", level, ctx.Err())
		}
	}
	expect("fail")
	writePluginDevTestFile(t, source, `export default { initialize() { return {}; } };`)
	expect("pass")
	// An atomic editor save replaces the watched inode. The parent-directory
	// watch and reconciliation must still notice the next source revision.
	replacement := source + ".saving"
	writePluginDevTestFile(t, replacement, `export default { initialize() { return { tools: [] }; } };`)
	if err := os.Rename(replacement, source); err != nil {
		t.Fatal(err)
	}
	expect("pass")
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("watch command exit: %v\n%s", err, stderr.String())
	}
}

func TestSingleTypeScriptDevRejectsSymlinkSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.ts")
	link := filepath.Join(root, "link.ts")
	writePluginDevTestFile(t, source, `export default { initialize() { return {}; } };`)
	if err := os.Symlink(source, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := readPluginIdentity(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink authorization = %v", err)
	}
	if _, cleanup, err := prepareSingleFilePlugin(context.Background(), link); err == nil {
		cleanup()
		t.Fatal("symlink candidate was prepared")
	}
}

func TestSingleTypeScriptDevRejectsSourceKindReplacementBeforeBuild(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "source.ts")
	writePluginDevTestFile(t, source, `export default { initialize() { return {}; } };`)
	id, err := readPluginIdentity(source)
	if err != nil {
		t.Fatal(err)
	}
	authDir := filepath.Join(home, "dev", "plugins")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := authorizeDevDirectory(authDir, id, source); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	writePluginDevTestFile(t, filepath.Join(source, "plugin.json"), fmt.Sprintf(`{"schema_version":1,"id":%q,"desktop":{"entry":"dist/index.js"}}`, id))
	writePluginDevTestFile(t, filepath.Join(source, "package.json"), `{"scripts":{"build":"custom-build"}}`)
	marker := filepath.Join(t.TempDir(), "build-ran")
	manager := writePluginDevTestManager(t, fmt.Sprintf("touch %q; mkdir -p dist; echo 'export {}' > dist/index.js", marker))
	if _, err := refreshDevGeneration(context.Background(), home, source, manager); err == nil || !strings.Contains(err.Error(), "source kind") {
		t.Fatalf("source kind replacement = %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("build ran before source kind validation: %v", err)
	}
	if err := authorizeDevDirectory(authDir, id, source); err == nil {
		t.Fatal("rerunning silently broadened file authorization into directory authorization")
	}
}
