import * as React from "react";
import { SettingsRow } from "../../src/renderer/SettingsRow";
import { SelectMenu } from "../../src/renderer/SelectMenu";
import { createRoot } from "react-dom/client";
import { PluginHost, type PluginGenerationApi } from "../../src/renderer/plugins/PluginHost";
import { DesktopWorkbench, PluginViewContent, WorkbenchController } from "../../src/renderer/plugins/Workbench";
import { AppBackground } from "../../src/renderer/background/AppBackground";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { initialState } from "../../src/renderer/AppState";
import { ConversationTitleActions, ConversationTitleContent } from "../../src/renderer/ConversationShellRenderers";
import { usePrimaryPluginViewCover } from "../../src/renderer/PrimaryPluginViewCover";
import source from "../../../internal/plugin/bundled/automation/desktop.js?raw";
import manifest from "../../../internal/plugin/bundled/automation/plugin.json";
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
import "../../src/renderer/styles.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") === "dark" ? "dark" : "light";
startFocusModality();
applyMessageFlowFontSize(Number(params.get("font")) || 14);
const workspace = { id: "wuu", name: "wuu", root: "/projects/wuu", available: true };
// `empty` starts with no automations; `long` gives one a name and prompt that wrap.
let tasks = params.has("empty") ? [] : [
  ...(params.has("long") ? [{ id: "long", title: "每个工作日早上汇总这个项目最近的变更、待办工作和需要我关注的评审请求", prompt: "汇总这个项目最近二十四小时的提交、合并请求和未解决的评审意见，按紧急程度排序，并标出需要我今天决定的事项。", cron: "30 8 * * 1-5", timezone: "Asia/Shanghai", mode: "new_thread", recurring: true, paused: false, workspace_mode: "shared", next_run_at: "2026-09-16T00:30:00Z" }] : []),
  { id: "brief", title: "每日简报", prompt: "汇总这个项目最近的变更、待办工作，以及今天需要我关注的事项。", cron: "0 8 * * 1-5", timezone: "Asia/Shanghai", mode: "new_thread", recurring: true, paused: false, workspace_mode: "shared", next_run_at: "2026-09-16T00:00:00Z" },
  { id: "review", title: "每周回顾", prompt: "回顾本周进展，整理已完成的工作、尚未解决的问题和下周重点。", cron: "0 16 * * 5", timezone: "Asia/Shanghai", mode: "new_thread", recurring: true, paused: true, workspace_mode: "worktree", next_run_at: "2026-09-18T08:00:00Z" },
];
// A failed run on a recurring task and a finished one-shot task, so the list
// shows both a run problem and the completed section.
const runs = params.has("empty") ? [] : [
  ...(params.has("states") ? [
    { id: "run-long", task_id: "long", status: "running", triggered_at: "2026-09-15T00:30:00Z" },
    { id: "run-review", task_id: "review", status: "interrupted", triggered_at: "2026-09-14T08:00:00Z", error: "Execution interrupted; the schedule is paused." },
  ] : []),
  { id: "run-brief", task_id: "brief", status: "failed", triggered_at: "2026-09-15T00:00:00Z", error: "Provider rate limit reached" },
  { id: "run-once", task_id: "release", status: "completed", triggered_at: "2026-09-14T09:00:00Z", completed_at: "2026-09-14T09:03:00Z", task: { id: "release", title: "发布前检查", prompt: "检查发布分支的构建、测试和发布说明。", cron: "0 17 14 9 *", timezone: "Asia/Shanghai", mode: "new_thread", recurring: false, paused: false, workspace_mode: "shared" } },
];
const host = new PluginHost({ react: React,
  listWorkspaces: async () => ({ activeWorkspaceId: "wuu", workspaces: [workspace] }),
  listThreads: async () => ["检查自动化插件的执行与恢复流程", "统一侧栏与编辑面板的交互", "整理本周项目进展", "检查长标题在不同窗口宽度下的显示与截断", "排查会话恢复", "更新开发文档", "核验任务运行记录", "改进菜单的键盘操作", "检查桌面与移动端布局", "整理下一周的工作计划", "检查插件生命周期", "回顾最近一次发布"].map((title, index) => ({ id: `chat-${index}`, title, pinned: index === 0, updatedAt: "2026-09-15T12:00:00Z" })),
  invokeRuntime: async ({ method, input }) => {
    const value = input as Record<string, any>;
    if (method === "automation.list") return { tasks, workspace };
    if (method === "automation.run.list") return { runs };
    if (method === "automation.remove") { tasks = tasks.filter(task => task.id !== value.id); return { ok: true }; }
    const task = { ...(tasks.find(task => task.id === value.id) || {}), ...value, id: value.id || crypto.randomUUID(), ...(value.schedule ? { cron: value.schedule } : {}), ...(value.workspace ? { workspace_mode: value.workspace } : {}) };
    tasks = [...tasks.filter(item => item.id !== task.id), task] as typeof tasks;
    return task;
  },
});
// The manifest's navigation entry is what makes Automations a sidebar destination.
await host.activateGeneration({ pluginId: "automation", generation: "preview", contributions: { navigation: manifest.contributes.navigation.map(({ id, title, view, order }) => ({ id, title, view, order })) }, register: activate });
const controller = new WorkbenchController(host, {}, { getItem: () => null, setItem: () => {}, removeItem: () => {} });
const region = params.get("region") || "workspace";
const portalRegion = region === "primary" || region === "overlay" || region === "auxiliary";
if (portalRegion) await controller.openPluginView("automation", "automation.catalog", { region });
const style = document.createElement("style");
style.textContent = "html,body,#root { width:100%; height:100%; margin:0; } .plugin-view-content {height:100%;} body {background:var(--paper);} ";
document.head.append(style);
const noop = () => {};
// Portal regions keep the production titlebar, so its Back and page actions
// are inspected against the real plugin page rather than a mock header.
function Titlebar() {
  const [rightPanelOpen, setRightPanelOpen] = React.useState(false);
  const pluginPageVisible = usePrimaryPluginViewCover(controller);
  const state = { ...initialState, activeContext: { kind: "project" as const, project_id: "wuu", cwd: workspace.root } };
  return <header className="titlebar">
    <div className="title-block"><ConversationTitleContent state={state} activeTitle="检查自动化插件的执行与恢复流程" onStartNewThread={noop} pluginHost={host} workbenchController={controller} /></div>
    <ConversationTitleActions state={state} compactNavigation={params.has("compact")} pluginPageVisible={pluginPageVisible} onStartNewThread={noop}
      environmentToggleRef={React.createRef()} environmentPanelVisible={false} onToggleEnvironmentPanel={noop}
      rightPanelOpen={rightPanelOpen} onToggleRightPanel={() => setRightPanelOpen(!rightPanelOpen)} />
  </header>;
}
function Preview() {
  const [value, setValue] = React.useState("daily");
  const pluginPageVisible = usePrimaryPluginViewCover(controller);
  const content = <PluginViewContent controller={controller} pluginId="automation" viewTypeId="automation.catalog" />;
  return <><AppBackground />{params.has("reference") ? <div style={{ padding: "16px 24px", borderBottom: "1px solid var(--hairline)" }}>
    <SettingsRow title="Wuu 原生设置行"><SelectMenu ariaLabel="原生计划" value={value} onChange={setValue} options={[{ value: "daily", label: "每天" }, { value: "weekdays", label: "工作日" }]} /></SettingsRow>
  </div> : null}{portalRegion ? <>
    <main className="conversation-pane" style={{ flex: 1 }} data-primary-plugin-view={pluginPageVisible ? "" : undefined}><Titlebar /></main>
    <DesktopWorkbench host={host} controller={controller} />
  </> : region === "settings" ? <div className="settings-main" style={{ flex: 1 }}><div className="settings-page" style={{ height: "100%" }}>{content}</div></div>
    : <div className="workspace-panel-body" style={{ minHeight: 0, flex: 1 }}>{content}</div>}</>;
}
style.textContent += "#root {display:flex; flex-direction:column;}";
createRoot(document.getElementById("root")!).render(<Preview />);
