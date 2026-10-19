package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	proc "github.com/blueberrycongee/wuu/internal/process"
	pluginsdk "github.com/blueberrycongee/wuu/packages/plugin-sdk"
)

func singleFilePluginID(source string) string {
	// Authorizing one file must not authorize a different same-named file.
	digest := sha256.Sum256([]byte(source))
	return "local-" + hex.EncodeToString(digest[:12])
}

// prepareSingleFilePlugin adapts a default-export RuntimePlugin to the existing
// process host. All code is snapshotted into the candidate; source edits never
// mutate the last published generation. Authors need no manifest or build.
func prepareSingleFilePlugin(ctx context.Context, source string) (string, func(), error) {
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", func() {}, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", func() {}, err
	}
	if !info.Mode().IsRegular() {
		return "", func() {}, fmt.Errorf("single-file development source must be a regular file, not a symlink: %s", abs)
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return "", func() {}, err
	}
	root, err := os.MkdirTemp("", "wuu-plugin-source-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	fail := func(err error) (string, func(), error) { cleanup(); return "", func() {}, err }
	id := singleFilePluginID(abs)
	manifest := map[string]any{
		"schema_version": 1, "id": id, "name": strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)),
		"runtime": map[string]any{"protocol": "wuu-plugin-v1", "command": "node", "args": []string{"--experimental-transform-types", "loader.mjs"}},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return fail(err)
	}
	files := map[string][]byte{
		"plugin.json":  encoded,
		"package.json": []byte(`{"type":"module"}`),
		"extension.ts": content,
		"loader.mjs":   []byte(singleFileRuntimeLoader),
		"resolve.mjs":  []byte(singleFileSDKResolver),
	}
	for _, name := range []string{"index.ts", "theme-contract.generated.ts", "bundle-contract.ts", "model.ts", "process.ts"} {
		data, err := pluginsdk.Sources.ReadFile("src/" + name)
		if err != nil {
			return fail(err)
		}
		files["sdk/"+name] = data
	}
	for name, data := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fail(err)
		}
	}
	// Importing executes source code. Keep its Wuu state disposable and own its
	// process group; this is not filesystem or network containment. initialize()
	// may use kernel services, so only the real generation host can prepare it.
	validationHome, err := os.MkdirTemp("", "wuu-plugin-validation-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(validationHome)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	runtime := pluginhost.RuntimeCommand("node", []string{"--experimental-transform-types", "loader.mjs", "--validate"}, map[string]string{"WUU_HOME": validationHome})
	command := exec.CommandContext(ctx, runtime.Path, runtime.Args[1:]...)
	command.Env = runtime.Env
	command.Dir = root
	proc.PrepareCommand(command)
	command.Cancel = func() error { return proc.ProcessTreeFromID(command.Process.Pid).Kill() }
	command.WaitDelay = 2 * time.Second
	output := &singleFileValidationOutput{}
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		return fail(fmt.Errorf("load TypeScript plugin (Node.js 22.7+ required): %w", err))
	}
	// A successful parent can leave children with redirected stdio behind, so
	// clean the process group on every exit, not only on context cancellation.
	if err := errors.Join(command.Wait(), ctx.Err(), proc.ProcessTreeFromID(command.Process.Pid).Kill()); err != nil {
		return fail(fmt.Errorf("load TypeScript plugin (Node.js 22.7+ required): %w: %s", err, output.String()))
	}
	if _, err := pluginpkg.InspectPackage(root); err != nil {
		return fail(err)
	}
	return root, cleanup, nil
}

type singleFileValidationOutput struct {
	data      []byte
	truncated bool
}

func (b *singleFileValidationOutput) Write(data []byte) (int, error) {
	n := len(data)
	if remaining := 32*1024 - len(b.data); len(data) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	b.data = append(b.data, data...)
	return n, nil
}

func (b *singleFileValidationOutput) String() string {
	text := strings.TrimSpace(string(b.data))
	if b.truncated {
		text += "\n[validation output truncated]"
	}
	return text
}

const singleFileRuntimeLoader = `import { register } from "node:module";
register("./resolve.mjs", import.meta.url);
const { runJSONLRuntime } = await import("./sdk/index.ts");
const { default: plugin } = await import("./extension.ts");
if (!plugin || typeof plugin.initialize !== "function") {
  throw new TypeError("A Wuu TypeScript plugin must default-export a RuntimePlugin with initialize().");
}
if (!process.argv.includes("--validate")) {
  await runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout });
}
`

// The SDK's public source uses emitted .js import names for normal package
// builds. Resolve only that owned import to its embedded TypeScript source.
const singleFileSDKResolver = `import { isBuiltin } from "node:module";
export async function resolve(specifier, context, nextResolve) {
  if (specifier === "@wuu/plugin-sdk") {
    return { url: new URL("./sdk/index.ts", import.meta.url).href, shortCircuit: true };
  }
  if (["./theme-contract.generated.js", "./bundle-contract.js", "./model.js", "./process.js"].includes(specifier) && context.parentURL === new URL("./sdk/index.ts", import.meta.url).href) {
    return { url: new URL("./sdk/" + specifier.slice(2, -3) + ".ts", import.meta.url).href, shortCircuit: true };
  }
  if (context.parentURL === new URL("./extension.ts", import.meta.url).href && !isBuiltin(specifier)) {
    throw new Error("Single-file plugins may import Node builtins and @wuu/plugin-sdk only; use a plugin directory for other dependencies.");
  }
  return nextResolve(specifier, context);
}
`
