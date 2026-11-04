package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	pluginsdk "github.com/blueberrycongee/wuu/packages/plugin-sdk"
	"github.com/evanw/esbuild/pkg/api"
)

func SingleFileID(source string) string {
	// Authorizing one file must not authorize a different same-named file.
	digest := sha256.Sum256([]byte(source))
	return "local-" + hex.EncodeToString(digest[:12])
}

// PrepareSingleFile adapts a single-file TypeScript plugin (RuntimePlugin,
// Desktop UI module with activate/activatePresentation, or a unified full-stack
// file exporting both) to the package and process contracts. All code is
// snapshotted into the candidate; source edits never mutate the last published
// generation. Authors need no manifest or build step.
func PrepareSingleFile(ctx context.Context, source string) (string, func(), error) {
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
	id := SingleFileID(abs)
	manifest := map[string]any{
		"schema_version": 1,
		"id":             id,
		"name":           strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)),
		"runtime": map[string]any{
			"protocol": "wuu-plugin-v1",
			"command":  "node",
			"args":     []string{"--experimental-transform-types", "loader.mjs"},
		},
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
	sources, err := pluginsdk.Sources.ReadDir("src")
	if err != nil {
		return fail(err)
	}
	for _, entry := range sources {
		name := entry.Name()
		if strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".test.tsx") {
			continue
		}
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
	// Validate loading without inventing a host context. initialize() may use
	// read-phase kernel services, so only the real generation host can prepare it.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	runtime := pluginhost.RuntimeCommand("node", []string{"--experimental-transform-types", "loader.mjs", "--validate"}, nil)
	command := exec.CommandContext(ctx, runtime.Path, runtime.Args[1:]...)
	command.Env = runtime.Env
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		return fail(fmt.Errorf("load TypeScript plugin (Node.js 22.13+ required): %w: %s", err, strings.ReplaceAll(strings.TrimSpace(string(output)), filepath.Join(root, "extension.ts"), abs)))
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, ManifestFilename))
	if err != nil {
		return fail(err)
	}
	var generated struct {
		Runtime json.RawMessage `json:"runtime"`
		Desktop json.RawMessage `json:"desktop"`
	}
	if err := json.Unmarshal(manifestBytes, &generated); err != nil {
		return fail(err)
	}
	if len(generated.Desktop) != 0 {
		if err := bundleSingleFileDesktop(ctx, root); err != nil {
			return fail(err)
		}
	}
	if len(generated.Runtime) == 0 {
		for _, name := range []string{"sdk", "loader.mjs", "resolve.mjs"} {
			if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
				return fail(err)
			}
		}
	}
	if _, err := InspectPackage(root); err != nil {
		return fail(err)
	}
	return root, cleanup, nil
}

// Bundle with a JavaScript parser rather than rewriting import-looking text.
// The renderer admits only self and wuu-plugin: scripts, so dependencies must
// be in this module, not data: imports or a relaxed shell CSP.
func bundleSingleFileDesktop(ctx context.Context, root string) error {
	entry := filepath.Join(root, "desktop.mjs")
	source, err := os.ReadFile(entry)
	if err != nil {
		return err
	}
	builtinsPath := filepath.Join(root, "desktop-builtins.json")
	encoded, err := os.ReadFile(builtinsPath)
	if err != nil {
		return err
	}
	var names []string
	if err := json.Unmarshal(encoded, &names); err != nil {
		return err
	}
	builtins := make(map[string]bool, len(names)*2)
	for _, name := range names {
		builtins[name] = true
		builtins["node:"+name] = true
	}
	result := api.Build(api.BuildOptions{
		Stdin:  &api.StdinOptions{Contents: string(source), Sourcefile: "extension.ts", Loader: api.LoaderJS},
		Bundle: true, Format: api.FormatESModule, Platform: api.PlatformBrowser, Target: api.ES2022,
		LogLevel: api.LogLevelSilent,
		Plugins: []api.Plugin{{Name: "single-file-imports", Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				if args.Path == "@wuu/plugin-sdk" {
					return api.OnResolveResult{Path: "index.ts", Namespace: "wuu-sdk"}, nil
				}
				if args.Namespace == "wuu-sdk" && strings.HasPrefix(args.Path, "./") && strings.HasSuffix(args.Path, ".js") {
					return api.OnResolveResult{Path: strings.TrimSuffix(strings.TrimPrefix(args.Path, "./"), ".js") + ".ts", Namespace: "wuu-sdk"}, nil
				}
				if builtins[args.Path] || strings.HasPrefix(args.Path, "node:") {
					return api.OnResolveResult{Path: args.Path, Namespace: "node-stub"}, nil
				}
				return api.OnResolveResult{}, fmt.Errorf("single-file plugins may import Node builtins and @wuu/plugin-sdk only; use a plugin directory for other dependencies: %s", args.Path)
			})
			build.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "wuu-sdk"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				data, err := pluginsdk.Sources.ReadFile("src/" + args.Path)
				contents := string(data)
				return api.OnLoadResult{Contents: &contents, Loader: api.LoaderTS}, err
			})
			build.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "node-stub"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				message, _ := json.Marshal("Node builtin \"" + args.Path + "\" is only available in the plugin runtime, not in the desktop renderer")
				runtime := pluginhost.RuntimeCommand("node", []string{"--input-type=module", "-e", "console.log(JSON.stringify(Object.keys(await import(process.argv[1]))))", args.Path}, nil)
				command := exec.CommandContext(ctx, runtime.Path, runtime.Args[1:]...)
				command.Env = runtime.Env
				output, err := command.Output()
				if err != nil {
					return api.OnLoadResult{}, fmt.Errorf("inspect Node builtin %q: %w", args.Path, err)
				}
				var exports []string
				if err := json.Unmarshal(output, &exports); err != nil {
					return api.OnLoadResult{}, err
				}
				contents := "const fail = () => { throw new Error(" + string(message) + "); };\n" +
					"const stub = new Proxy(fail, { get: (_, key) => key === 'then' ? undefined : stub, apply: () => fail(), construct: () => fail() });\n" +
					"export default stub;\n"
				for _, name := range exports {
					if name != "default" {
						quoted, _ := json.Marshal(name)
						contents += "export { stub as " + string(quoted) + " };\n"
					}
				}
				return api.OnLoadResult{Contents: &contents, Loader: api.LoaderJS}, nil
			})
		}}},
	})
	if len(result.Errors) != 0 {
		return fmt.Errorf("bundle single-file desktop plugin: %s", strings.Join(api.FormatMessages(result.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage}), "\n"))
	}
	if err := os.WriteFile(entry, result.OutputFiles[0].Contents, 0o600); err != nil {
		return err
	}
	return os.Remove(builtinsPath)
}

const singleFileRuntimeLoader = `import fs from "node:fs";
import { builtinModules, register, stripTypeScriptTypes } from "node:module";

register("./resolve.mjs", import.meta.url);
const { runJSONLRuntime } = await import("./sdk/index.ts");
const mod = await import("./extension.ts");

const runtimePlugin =
  mod.default && typeof mod.default === "object" && typeof mod.default.initialize === "function"
    ? mod.default
    : mod.runtime && typeof mod.runtime === "object" && typeof mod.runtime.initialize === "function"
      ? mod.runtime
      : null;

const hasDesktop =
  typeof mod.activate === "function" ||
  typeof mod.activatePresentation === "function" ||
  (!runtimePlugin &&
    mod.default &&
    typeof mod.default === "object" &&
    (typeof mod.default.activate === "function" || typeof mod.default.activatePresentation === "function"));

if (!runtimePlugin && !hasDesktop) {
  throw new TypeError(
    "A Wuu TypeScript plugin must default-export a RuntimePlugin with initialize() or export activate(api) / activatePresentation(api).",
  );
}

if (process.argv.includes("--validate")) {
  const baseManifest = JSON.parse(fs.readFileSync("./plugin.json", "utf8"));
  const rawManifest = mod.manifest ?? (mod.default && typeof mod.default === "object" ? mod.default.manifest : undefined);
  if (rawManifest !== undefined) {
    if (typeof rawManifest !== "object" || rawManifest === null || Array.isArray(rawManifest)) {
      throw new TypeError("Single-file plugin manifest export must be an object.");
    }
    if (rawManifest.id !== undefined && rawManifest.id !== baseManifest.id) {
      throw new Error("Single-file plugin manifest id must match its derived file id or be omitted.");
    }
    if (rawManifest.runtime !== undefined) {
      throw new Error("Single-file plugin manifest cannot override runtime; export a RuntimePlugin with initialize() instead.");
    }
    if (
      rawManifest.desktop !== undefined &&
      (typeof rawManifest.desktop !== "object" ||
        rawManifest.desktop === null ||
        Array.isArray(rawManifest.desktop) ||
        rawManifest.desktop.entry !== undefined)
    ) {
      throw new Error("Single-file plugin manifest.desktop must be an object without entry; export activate(api) or activatePresentation(api) instead.");
    }
    Object.assign(baseManifest, rawManifest, { schema_version: 1, id: baseManifest.id });
  }

  if (runtimePlugin) {
    baseManifest.runtime = {
      protocol: "wuu-plugin-v1",
      command: "node",
      args: ["--experimental-transform-types", "loader.mjs"],
    };
  } else {
    delete baseManifest.runtime;
  }

  if (hasDesktop) {
    const tsSource = fs.readFileSync("./extension.ts", "utf8");
    fs.writeFileSync("./desktop.mjs", stripTypeScriptTypes(tsSource, { mode: "transform" }), { mode: 0o600 });
    fs.writeFileSync("./desktop-builtins.json", JSON.stringify(builtinModules), { mode: 0o600 });
    baseManifest.desktop = {
      ...(typeof baseManifest.desktop === "object" && baseManifest.desktop !== null ? baseManifest.desktop : {}),
      entry: "desktop.mjs",
    };
  }

  fs.writeFileSync("./plugin.json", JSON.stringify(baseManifest), { mode: 0o600 });
} else {
  if (!runtimePlugin) {
    throw new TypeError("A Wuu TypeScript runtime plugin must default-export a RuntimePlugin with initialize().");
  }
  await runJSONLRuntime(runtimePlugin, { input: process.stdin, output: process.stdout });
}
`

// The SDK's public source uses emitted .js import names for normal package
// builds. Resolve SDK-relative imports to the same embedded TypeScript modules.
const singleFileSDKResolver = `import { isBuiltin } from "node:module";
export async function resolve(specifier, context, nextResolve) {
  if (specifier === "@wuu/plugin-sdk") {
    return { url: new URL("./sdk/index.ts", import.meta.url).href, shortCircuit: true };
  }
  if (specifier.startsWith("./") && specifier.endsWith(".js") && context.parentURL?.startsWith(new URL("./sdk/", import.meta.url).href)) {
    return { url: new URL(specifier.slice(0, -3) + ".ts", context.parentURL).href, shortCircuit: true };
  }
  if (context.parentURL === new URL("./extension.ts", import.meta.url).href && !isBuiltin(specifier)) {
    throw new Error("Single-file plugins may import Node builtins and @wuu/plugin-sdk only; use a plugin directory for other dependencies.");
  }
  return nextResolve(specifier, context);
}
`
