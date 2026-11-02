package plugin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestPrepareSingleFileDesktopAndFullStack(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}

	t.Run("desktop-only single file with SDK value import", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "desktop-only.ts")
		code := `import { KERNEL_SERVICE_METHOD, type PluginGenerationApi } from "@wuu/plugin-sdk";

export const manifest = {
  name: "Desktop Only Demo",
  version: "1.2.0",
  description: "Single-file desktop UI plugin",
};

export async function activate(api: PluginGenerationApi): Promise<string> {
  const { KERNEL_SERVICE_METHOD: dynamicMethod } = await import("@wuu/plugin-sdk");
  const text = "from 'node:fs'";
  return "activated:" + api.pluginId + ":" + KERNEL_SERVICE_METHOD + ":" + dynamicMethod + ":" + text;
}
`
		if err := os.WriteFile(source, []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
		prepared, cleanup, err := PrepareSingleFile(context.Background(), source)
		if err != nil {
			t.Fatalf("PrepareSingleFile desktop-only: %v", err)
		}
		defer cleanup()

		item, err := LoadManifest(filepath.Join(prepared, ManifestFilename), "user")
		if err != nil {
			t.Fatalf("LoadManifest: %v", err)
		}
		if item.ID != SingleFileID(source) || item.Name != "Desktop Only Demo" || item.Version != "1.2.0" {
			t.Fatalf("manifest identity = %+v", item.Manifest)
		}
		if item.Runtime != nil {
			t.Fatalf("desktop-only single file should not declare a runtime process: %+v", item.Runtime)
		}
		if item.Desktop == nil || item.Desktop.Entry != "desktop.mjs" {
			t.Fatalf("desktop entry = %+v", item.Desktop)
		}

		eval := exec.Command("node", "--input-type=module", "-e", `
import { activate } from "./desktop.mjs";
console.log(await activate({ pluginId: "user:demo" }));
`)
		eval.Dir = prepared
		output, err := eval.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "activated:user:demo:call:call:from 'node:fs'") {
			t.Fatalf("desktop.mjs execution = %q, %v", output, err)
		}
	})

	t.Run("unified full-stack single file with runtime and desktop exports", func(t *testing.T) {
		dir := t.TempDir()
		notePath := filepath.Join(dir, "note.txt")
		if err := os.WriteFile(notePath, []byte("from-node-fs"), 0o600); err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(dir, "fullstack.ts")
		code := `import { readFileSync } from "node:fs";
import { KERNEL_SERVICE_METHOD, type PluginGenerationApi, type RuntimePlugin } from "@wuu/plugin-sdk";

export const manifest = {
  name: "Unified Fullstack",
  version: "0.5.0",
  contributes: {
    presenters: [{ id: "preview-card", target: "content.preview", mode: "replace", priority: 5 }],
  },
};

export default {
  initialize() {
    return {
      tools: [{ id: "read_note", description: "Read note", input_schema: { type: "object" } }],
    };
  },
  executeTool(params) {
    const file = String((params.arguments as { file?: string })?.file ?? "");
    return {
      result: {
        content: [{ type: "text", text: readFileSync(file, "utf8") + ":" + KERNEL_SERVICE_METHOD }],
      },
    };
  },
} satisfies RuntimePlugin;

export function activate(api: PluginGenerationApi): string {
  return "ui-ready:" + api.pluginId + ":" + KERNEL_SERVICE_METHOD;
}
`
		if err := os.WriteFile(source, []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
		prepared, cleanup, err := PrepareSingleFile(context.Background(), source)
		if err != nil {
			t.Fatalf("PrepareSingleFile fullstack: %v", err)
		}
		defer cleanup()

		item, err := LoadManifest(filepath.Join(prepared, ManifestFilename), "user")
		if err != nil {
			t.Fatalf("LoadManifest: %v", err)
		}
		if item.Runtime == nil || item.Desktop == nil || item.Desktop.Entry != "desktop.mjs" {
			t.Fatalf("expected both runtime and desktop entries: runtime=%+v desktop=%+v", item.Runtime, item.Desktop)
		}
		if len(item.Presenters) != 1 || item.Presenters[0].ID != "preview-card" {
			t.Fatalf("manifest presenters = %+v", item.Presenters)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client, err := pluginhost.Start(ctx, pluginhost.ProcessConfig{
			ID:         item.ID,
			Command:    item.Runtime.Command,
			Args:       item.Runtime.Args,
			PluginRoot: prepared,
			WuuHome:    t.TempDir(),
			Timeout:    5 * time.Second,
		})
		if err != nil {
			t.Fatalf("start fullstack runtime: %v", err)
		}
		defer client.Close(ctx)

		argsJSON, _ := json.Marshal(map[string]string{"file": notePath})
		res, err := client.ExecuteTool(ctx, pluginhost.ToolExecuteParams{
			ToolID: "read_note",
			ToolExecuteInput: pluginhost.ToolExecuteInput{
				CallID:    "call-1",
				Tool:      "read_note",
				CWD:       dir,
				Arguments: argsJSON,
			},
		})
		if err != nil {
			t.Fatalf("ExecuteTool: %v", err)
		}
		encoded, _ := json.Marshal(res)
		if !strings.Contains(string(encoded), "from-node-fs:call") {
			t.Fatalf("unexpected tool output: %s", encoded)
		}

		eval := exec.Command("node", "--input-type=module", "-e", `
import mod, { activate } from "./desktop.mjs";
console.log(activate({ pluginId: "user:fullstack" }));
try {
  mod.executeTool({ arguments: { file: "ignored" } });
  process.exit(1);
} catch (error) {
  console.log("stub-error:" + error.message);
}
`)
		eval.Dir = prepared
		output, err := eval.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "ui-ready:user:fullstack:call") || !strings.Contains(string(output), "Node builtin \"node:fs\" is only available in the plugin runtime") {
			t.Fatalf("desktop.mjs fullstack execution = %q, %v", output, err)
		}

		if os.Getenv("CHROMIUM_BIN") != "" {
			probe := exec.Command("node", "../../desktop/scripts/single-file-plugin-csp.cjs", filepath.Join(prepared, "desktop.mjs"))
			output, err := probe.CombinedOutput()
			if err != nil {
				t.Fatalf("desktop renderer CSP: %s: %v", output, err)
			}
			t.Logf("desktop renderer CSP: %s", output)
		}
	})

	t.Run("rejects invalid single-file exports and manifest overrides", func(t *testing.T) {
		for name, content := range map[string]string{
			"empty module":           `export const x = 1;`,
			"non-object manifest":    `export const manifest = "bad"; export function activate() {}`,
			"id override":            `export const manifest = { id: "custom-id" }; export function activate() {}`,
			"runtime override":       `export const manifest = { runtime: { protocol: "wuu-plugin-v1", command: "node" } }; export function activate() {}`,
			"desktop entry override": `export const manifest = { desktop: { entry: "other.js" } }; export function activate() {}`,
		} {
			source := filepath.Join(t.TempDir(), "invalid.ts")
			if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, cleanup, err := PrepareSingleFile(context.Background(), source); err == nil {
				cleanup()
				t.Fatalf("%s: expected error", name)
			}
		}
	})
}

func TestSingleFilePackageInspectInstallAndSameSourceUpdate(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "quick-tool.ts")
	v1 := `import type { PluginGenerationApi, RuntimePlugin } from "@wuu/plugin-sdk";
export const manifest = { name: "Quick Tool", version: "1.0.0" };
export default {
  initialize() { return { tools: [{ id: "ping", description: "Ping", input_schema: { type: "object" } }] }; },
  executeTool() { return { result: { content: [{ type: "text", text: "v1" }] } }; },
} satisfies RuntimePlugin;
export function activate(api: PluginGenerationApi) {
  api.registerStatusItem({ id: "status", label: "v1" });
}
`
	if err := os.WriteFile(source, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolvePackageSource(context.Background(), source, DistributionOptions{})
	if err != nil {
		t.Fatalf("ResolvePackageSource(.ts): %v", err)
	}
	defer resolved.Close()
	if resolved.Inspection.SourceKind != PackageSourceFile || resolved.Inspection.ID != SingleFileID(source) || resolved.Inspection.Version != "1.0.0" {
		t.Fatalf("resolved inspection = %+v", resolved.Inspection)
	}

	installed, err := InstallPackage(home, resolved.Path)
	if err != nil {
		t.Fatalf("InstallPackage(.ts): %v", err)
	}
	if installed.Package.SourceKind != PackageSourceFile || installed.Plugin.ID != SingleFileID(source) {
		t.Fatalf("installed = %+v", installed)
	}
	if installed.Plugin.Runtime == nil || installed.Plugin.Desktop == nil {
		t.Fatalf("installed plugin missing runtime or desktop: %+v", installed.Plugin.Manifest)
	}

	v2 := strings.ReplaceAll(v1, "1.0.0", "2.0.0")
	v2 = strings.ReplaceAll(v2, `"v1"`, `"v2"`)
	if err := os.WriteFile(source, []byte(v2), 0o600); err != nil {
		t.Fatal(err)
	}
	resolvedV2, err := ResolvePackageSource(context.Background(), source, DistributionOptions{})
	if err != nil {
		t.Fatalf("ResolvePackageSource v2: %v", err)
	}
	defer resolvedV2.Close()
	if resolvedV2.Identity != resolved.Identity {
		t.Fatalf("same-path .ts update changed source identity: %q vs %q", resolvedV2.Identity, resolved.Identity)
	}
	pending, err := StagePackageUpdateFromSource(home, resolvedV2.Path, resolvedV2.Identity)
	if err != nil {
		t.Fatalf("StagePackageUpdateFromSource(.ts): %v", err)
	}
	promoted, err := PromotePendingUpdate(home, installed.Plugin.ID, pending.Package.Fingerprint)
	if err != nil {
		t.Fatalf("PromotePendingUpdate(.ts): %v", err)
	}
	if promoted.Plugin.Version != "2.0.0" || promoted.Plugin.Fingerprint != pending.Package.Fingerprint {
		t.Fatalf("promoted plugin = %+v", promoted.Plugin)
	}
}
