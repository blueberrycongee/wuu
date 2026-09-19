import { createRoot } from "react-dom/client";
import type { ThreadItem } from "../../src/shared/protocol";
import type { InspectorTodoItemSnapshotV1 } from "../../src/shared/workbench";
import { ProcessSurface } from "../../src/renderer/ProcessSurface";
import { I18nProvider } from "../../src/renderer/i18n";
import { desktopPluginHost, desktopWorkbenchController } from "../../src/renderer/plugins/DesktopPluginRuntime";
import { PluginInspectorSections } from "../../src/renderer/plugins/PluginInspector";
// @ts-expect-error Bundled desktop extensions are plain JavaScript modules.
import { activate } from "../../../internal/plugin/bundled/todo/desktop.js";
import "../../src/renderer/styles.css";

const query = new URLSearchParams(location.search);
document.documentElement.dataset.theme = query.get("theme") || "light";
document.documentElement.dataset.platform = "darwin";
document.documentElement.style.setProperty("--conversation-message-font-size", `${query.get("size") || 14}px`);

const todos: InspectorTodoItemSnapshotV1[] = [
  { status: "completed", content: "定位工具明细与 TODO 的对齐规则" },
  { status: "in_progress", content: "追踪补丁、命令、权限和工具执行协议，确定最小融合方案并验证长文本换行后的首行图标位置" },
  { status: "pending", content: "运行验证、审查改动并提交独立分支" },
  { status: "pending", content: "检查连续路径：" + "long_directory_name/".repeat(8) },
];

function tool(id: string, name: string, args: unknown, status = "completed"): ThreadItem {
  return { id, type: "tool_call", name, arguments: JSON.stringify(args), status } as ThreadItem;
}

const items = [
  tool("git", "bash", { command: "git status --short" }),
  tool("workspace", "set_session_workspace", { root: "/repo" }),
  { ...tool("todo", "update_todo", { todos }), display: { capability: "todo" } },
  tool("search", "grep", { pattern: "ToolActivity", path: "desktop/src" }),
  tool("read", "read_file", { path: "docs/en/project/development.md" }),
  tool("long", "long_tool_name_".repeat(12), {}, "in_progress"),
  tool("failed", "read_file", { path: "missing.md" }, "failed"),
] as ThreadItem[];

await desktopPluginHost.activateGeneration({ pluginId: "todo", generation: "preview", register: activate });

createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <main className="conversation-pane" style={{ padding: 24, height: "100vh", overflow: "auto" }}>
      <div className="turn-process-entry" style={{ maxWidth: 1000, margin: "auto" }}>
        <ProcessSurface processItems={items} streaming={false} />
        <div className="environment-panel" style={{ position: "static", width: "min(328px, 100%)", marginTop: 32 }}>
          <PluginInspectorSections
            host={desktopPluginHost}
            controller={desktopWorkbenchController}
            snapshot={{ contractVersion: 1, session: { status: "idle" }, todo: { completed: 1, total: todos.length, items: todos } }}
          />
        </div>
      </div>
    </main>
  </I18nProvider>,
);
