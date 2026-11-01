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
		return fail(fmt.Errorf("load TypeScript plugin (Node.js 22.7+ required): %w: %s", err, strings.ReplaceAll(strings.TrimSpace(string(output)), filepath.Join(root, "extension.ts"), abs)))
	}
	if _, err := InspectPackage(root); err != nil {
		return fail(err)
	}
	return root, cleanup, nil
}

const singleFileRuntimeLoader = `import fs from "node:fs";
import path from "node:path";
import { isBuiltin, register, stripTypeScriptTypes } from "node:module";

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
    const sdkCache = new Map();
    const buildSDKDataURL = (file = "index.ts") => {
      if (sdkCache.has(file)) return sdkCache.get(file);
      const src = fs.readFileSync(path.join("./sdk", file), "utf8");
      let js = stripTypeScriptTypes(src, { mode: "transform" });
      js = js.replace(/(from\s+["'])\.\/([^"']+)\.js(["'])/g, (_, pre, rel, post) => {
        return pre + buildSDKDataURL(rel + ".ts") + post;
      });
      const url = "data:text/javascript;base64," + Buffer.from(js).toString("base64");
      sdkCache.set(file, url);
      return url;
    };

    const buildBuiltinStubDataURL = async (specifier) => {
      const target = specifier.startsWith("node:") ? specifier : "node:" + specifier;
      const builtin = await import(target);
      const names = Object.keys(builtin).filter((name) => name !== "default" && /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name));
      const message = 'Node builtin "' + specifier + '" is only available in the plugin runtime, not in the desktop renderer';
      const lines = [
        "const fail = () => { throw new Error(" + JSON.stringify(message) + "); };",
        "const stub = new Proxy(fail, { get: () => stub, apply: () => fail(), construct: () => fail() });",
        "export default stub;",
        ...names.map((name) => "export const " + name + " = stub;"),
      ];
      return "data:text/javascript;base64," + Buffer.from(lines.join("\n")).toString("base64");
    };

    const tsSource = fs.readFileSync("./extension.ts", "utf8");
    let desktopJS = stripTypeScriptTypes(tsSource, { mode: "transform" });
    const specifiers = new Set();
    for (const match of desktopJS.matchAll(/(?:from|import)\s*["']([^"']+)["']/g)) {
      specifiers.add(match[1]);
    }
    const replacements = new Map();
    for (const specifier of specifiers) {
      if (specifier === "@wuu/plugin-sdk") {
        replacements.set(specifier, buildSDKDataURL("index.ts"));
      } else if (isBuiltin(specifier)) {
        replacements.set(specifier, await buildBuiltinStubDataURL(specifier));
      }
    }
    desktopJS = desktopJS.replace(/((?:from|import)\s*["'])([^"']+)(["'])/g, (full, pre, specifier, post) => {
      const replacement = replacements.get(specifier);
      return replacement ? pre + replacement + post : full;
    });
    fs.writeFileSync("./desktop.mjs", desktopJS, { mode: 0o600 });
    baseManifest.desktop = {
      ...(typeof baseManifest.desktop === "object" && baseManifest.desktop !== null ? baseManifest.desktop : {}),
      entry: "desktop.mjs",
    };
  }

  fs.writeFileSync("./plugin.json", JSON.stringify(baseManifest), { mode: 0o600 });
  if (!runtimePlugin) {
    fs.rmSync("./sdk", { recursive: true, force: true });
    fs.rmSync("./loader.mjs", { force: true });
    fs.rmSync("./resolve.mjs", { force: true });
  }
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
