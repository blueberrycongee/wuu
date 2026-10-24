import { createFixtureApplication, fixtureSlots } from "../preview-services";
import * as React from "react";
import { SettingsRow } from "../../src/renderer/SettingsRow";
import { SelectMenu } from "../../src/renderer/SelectMenu";
import { createRoot } from "react-dom/client";
import { PluginHost, type PluginGenerationApi } from "../../src/renderer/plugins/PluginHost";
import { DesktopWorkbench, PluginViewContent, WorkbenchController, visibleWorkbenchView } from "../../src/renderer/plugins/Workbench";
import { AppBackground } from "../../src/renderer/background/AppBackground";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { initialState } from "../../src/renderer/AppState";
import { AppSidebar } from "../../src/renderer/AppSidebar";
import { SIDEBAR_DEFAULT_WIDTH, SIDEBAR_RIBBON_WIDTH } from "../../src/renderer/AppLayoutState";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { I18nProvider } from "../../src/renderer/i18n";
import { ConversationTitleActions, ConversationTitleContent } from "../../src/renderer/ConversationShellRenderers";
import { usePrimaryPluginViewCover } from "../../src/renderer/PrimaryPluginViewCover";
import source from "../../../internal/plugin/bundled/automation-client/desktop.js?raw";
import manifest from "../../../internal/plugin/bundled/automation-client/plugin.json";
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
import "../../src/renderer/styles.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") === "dark" ? "dark" : "light";
startFocusModality();
applyMessageFlowFontSize(Number(params.get("font")) || 15);
const workspace = { id: "wuu", name: "wuu", root: "/projects/wuu", available: true };
const emptyWorkspace = { id: "empty", name: "Empty project", root: "/projects/empty", available: true };
// `empty` starts with no automations; `long` gives one a name and prompt that wrap.
let tasks = params.has("empty") ? [] : [
  ...(params.has("long") ? [{ id: "long", title: "每个工作日早上汇总这个项目最近的变更、待办工作和需要我关注的评审请求", prompt: "汇总这个项目最近二十四小时的提交、合并请求和未解决的评审意见，按紧急程度排序，并标出需要我今天决定的事项。", cron: "30 8 * * 1-5", timezone: "Asia/Shanghai", mode: "new_thread", recurring: true, paused: false, workspace_mode: "shared", next_run_at: "2026-09-16T00:30:00Z" }] : []),
  { id: "brief", title: "每日简报", prompt: "汇总这个项目最近的变更、待办工作，以及今天需要我关注的事项。", cron: "0 8 * * 1-5", timezone: "Asia/Shanghai", mode: "new_thread", recurring: true, paused: false, workspace_mode: "shared", next_run_at: "2026-09-16T00:00:00Z" },
  { id: "review", title: "每周回顾", prompt: "回顾本周进展，整理已完成的工作、尚未解决的问题和下周重点。", cron: "0 16 * * 5", timezone: "Asia/Shanghai", mode: "new_thread", recurring: true, paused: true, workspace_mode: "worktree", next_run_at: "2026-09-18T08:00:00Z" },
];
// A failed run on a recurring task and a finished one-shot task, so the list
// shows both a run problem and the completed section.
const runs: Array<{ id: string; task_id: string; status: string; triggered_at: string; completed_at?: string; error?: string; task?: Omit<(typeof tasks)[number], "next_run_at"> }> = params.has("empty") ? [] : [
  { id: "run-brief", task_id: "brief", status: "failed", triggered_at: "2026-09-15T00:00:00Z", error: "Provider rate limit reached" },
  { id: "run-once", task_id: "release", status: "completed", triggered_at: "2026-09-14T09:00:00Z", completed_at: "2026-09-14T09:03:00Z", task: { id: "release", title: "发布前检查", prompt: "检查发布分支的构建、测试和发布说明。", cron: "0 17 14 9 *", timezone: "Asia/Shanghai", mode: "new_thread", recurring: false, paused: false, workspace_mode: "shared" } },
];
if (params.has("active") && !params.has("empty")) {
  for (const [id, title, status] of [["running", "检查发布分支", "running"], ["queued", "整理评审意见", "queued"], ["starting", "准备每日汇总", "starting"]]) {
    runs.push({ id: `run-${id}`, task_id: id, status, triggered_at: "2026-09-15T09:00:00Z", task: { id, title, prompt: "检查项目并整理结果。", cron: "0 17 15 9 *", timezone: "Asia/Shanghai", mode: "new_thread", recurring: false, paused: false, workspace_mode: "shared" } });
  }
}
if (params.has("many") && tasks.length) {
  tasks.push(...Array.from({ length: 30 }, (_, index) => ({ ...tasks[0], id: `extra-${index}`, title: `项目检查 ${index + 1}`, next_run_at: new Date(Date.UTC(2026, 8, 17 + index, 1)).toISOString() })));
}
const tasksByWorkspace = new Map([[workspace.id, tasks], [emptyWorkspace.id, [] as typeof tasks]]);
const host = new PluginHost({ react: React,
  listWorkspaces: async () => ({ activeWorkspaceId: "wuu", workspaces: [workspace, emptyWorkspace] }),
  listThreads: async () => ["检查自动化插件的执行与恢复流程", "统一侧栏与编辑面板的交互", "整理本周项目进展", "检查长标题在不同窗口宽度下的显示与截断", "排查会话恢复", "更新开发文档", "核验任务运行记录", "改进菜单的键盘操作", "检查桌面与移动端布局", "整理下一周的工作计划", "检查插件生命周期", "回顾最近一次发布"].map((title, index) => ({ id: `chat-${index}`, title, pinned: index === 0, updatedAt: "2026-09-15T12:00:00Z" })),
  invokeRuntime: async ({ service, method: serviceMethod, input, workspaceId = workspace.id }) => {
    if (service !== "automation.scheduler" || serviceMethod !== "request") throw new Error("Unsupported preview service");
    const { method, input: requestInput } = input as { method: string; input?: unknown };
    if (params.has("error")) throw new Error("Schedule service unavailable");
    const value = requestInput as Record<string, any>;
    const current = tasksByWorkspace.get(workspaceId) || [];
    if (method === "automation.list") return { tasks: current, workspace: workspaceId === workspace.id ? workspace : emptyWorkspace };
    if (method === "automation.run.list") return { runs: workspaceId === workspace.id ? runs : [] };
    if (method === "automation.remove") { tasksByWorkspace.set(workspaceId, current.filter(task => task.id !== value.id)); return { ok: true }; }
    const task = { ...(current.find(task => task.id === value.id) || {}), ...value, id: value.id || crypto.randomUUID(), next_run_at: "2026-09-16T01:00:00Z", ...(value.schedule ? { cron: value.schedule } : {}), ...(value.workspace ? { workspace_mode: value.workspace } : {}) };
    tasksByWorkspace.set(workspaceId, [...current.filter(item => item.id !== task.id), task] as typeof tasks);
    return task;
  },
});
// The manifest's navigation entry is what makes Automations a sidebar destination.
await host.activateGeneration({ pluginId: "automation-client", generation: "preview", graphGeneration: "preview", contributions: { navigation: manifest.contributes.navigation.map(({ id, title, view, order }) => ({ id, title, view, order, icon: { name: "clock" as const } })) }, register: activate });
const controller = new WorkbenchController(host, {}, { getItem: () => null, setItem: () => {}, removeItem: () => {} });
const region = params.get("region") || (params.has("sidebar") ? "primary" : "workspace");
const portalRegion = region === "primary" || region === "overlay" || region === "auxiliary";
if (portalRegion) {
  await controller.openPluginView("automation-client", "automation.catalog", { region });
  if (params.has("multiple")) await controller.openPluginView("automation-client", "automation.catalog", { region, reveal: false });
}
const style = document.createElement("style");
style.textContent = "html,body,#root { width:100%; height:100%; margin:0; } .plugin-view-content {height:100%;} body {background:var(--paper);} ";
document.head.append(style);
const noop = () => {};
// Portal regions keep the production titlebar, so its Back and page actions
// are inspected against the real plugin page rather than a mock header.
function Titlebar() {
  const [rightPanelOpen, setRightPanelOpen] = React.useState(false);
  const pluginPageVisible = usePrimaryPluginViewCover(controller);
  const [title, setTitle] = React.useState("检查自动化插件的执行与恢复流程");
  const workbench = React.useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const application = React.useMemo(() => {
    const base = createFixtureApplication();
    const snapshot = base.getSnapshot();
    const active = visibleWorkbenchView(workbench, "primary");
    const view = active?.view;
    const preview = createFixtureApplication({
      tabs: {...snapshot.tabs, activeTabId:"draft:automation",items:[{id:"draft:automation",kind:"draft",title,workspaceRoot:workspace.root,project:false,draftText:"",hasAttachments:false}]},
      conversationWorkspace: {...snapshot.conversationWorkspace,primaryThreadId:undefined,threads:[]},
      navigationWorkspace: {...snapshot.navigationWorkspace, activeContextKind:"project",selectedWorkspaceId:workspace.id,
        activePluginView:view ? {id:view.id,pluginId:view.pluginId,title:active.definition.title,webPage:workbench.webPageViewIds.includes(view.id),closable:!host.getNavigationEntries().some(entry => entry.pluginId === view.pluginId && entry.view === view.viewTypeId) || workbench.views.filter(item => item.region === "primary" && item.pluginId === view.pluginId && item.viewTypeId === view.viewTypeId).length > 1} : undefined},
    });
    return {...preview, invoke: ((action, input, options) => {
      if (action === "navigation.show-native") { controller.deactivateRegion("primary"); return Promise.resolve(); }
      if (action === "navigation.close-plugin") return controller.closeView((input as {viewId:string}).viewId);
      if (action === "draft.rename") { setTitle((input as {title:string}).title); return Promise.resolve(); }
      if (action === "workspace.start-draft") return Promise.resolve();
      return preview.invoke(action, input, options);
    }) as typeof preview.invoke};
  }, [workbench, title]);
  const state = { ...initialState, activeContext: { kind: "project" as const, project_id: "wuu", cwd: workspace.root } };
  return <header className="titlebar">
    <div className="title-block"><ConversationTitleContent application={application} slots={fixtureSlots} slotOwner="preview-fixture" /></div>
    <ConversationTitleActions thread={state.activePane === "secondary" ? state.secondaryThread : state.thread} pluginPageVisible={pluginPageVisible}
      environmentToggleRef={React.createRef()} environmentPanelVisible={false} onToggleEnvironmentPanel={noop}
      rightPanelOpen={rightPanelOpen} onToggleRightPanel={() => setRightPanelOpen(!rightPanelOpen)} />
  </header>;
}
function Sidebar() {
  return <AppSidebar
    initialized activeSessionTabID={initialState.activeSessionTabID} lastViewedTurnByThreadID={{}} sidebarWorkspaces={[]} pinnedThreads={[]}
    collapsedSidebarSectionIDs={new Set()} collapsedFolderIDs={new Set()} setCollapsedFolderIDs={noop}
    expandedSidebarSectionIDs={new Set()} workspaceThreadsByWorkspaceID={{}}
    workspaceMenuOpen={false} workspaceMenuRef={React.createRef()} searchOpen={false} sectionOrder={[]}
    onStartNewThread={noop} onOpenSkillsTab={noop} onToggleConversationSearch={noop} onSelectThread={noop}
    onTogglePinned={noop} onArchiveThread={noop} onDeleteThread={noop} onRenameThread={noop}
    onToggleWorkspaceMenu={noop} onCreateWorkspace={noop} onOpenWorkspaceFolder={noop}
    onToggleSidebarSectionCollapsed={noop} onLoadWorkspaceThreads={async () => {}} onStartNewThreadInWorkspace={noop}
    onSelectWorkspaceThread={noop} onRemoveWorkspace={noop} onRelocateWorkspace={noop} onOpenSettings={noop}
    onMarkThreadsViewed={noop} unreadViewOpen={false} onToggleUnreadView={noop}
    pluginHost={host} workbenchController={controller}
  />;
}
function Preview() {
  const [value, setValue] = React.useState("daily");
  const pluginPageVisible = usePrimaryPluginViewCover(controller);
  const content = <PluginViewContent controller={controller} pluginId="automation-client" viewTypeId="automation.catalog" />;
  return <><AppBackground />{params.has("reference") ? <div style={{ padding: "16px 24px", borderBottom: "1px solid var(--hairline)" }}>
    <SettingsRow title="Wuu 原生设置行"><SelectMenu ariaLabel="原生计划" value={value} onChange={setValue} options={[{ value: "daily", label: "每天" }, { value: "weekdays", label: "工作日" }]} /></SettingsRow>
  </div> : null}{portalRegion ? <>
    <div className={params.has("sidebar") ? "app-shell navigation-ribbon-layout" : undefined} style={params.has("sidebar") ? {
      flex: 1, "--sidebar-width": `${SIDEBAR_DEFAULT_WIDTH}px`, "--sidebar-open-width": `${SIDEBAR_DEFAULT_WIDTH}px`,
      "--sidebar-ribbon-width": `${SIDEBAR_RIBBON_WIDTH}px`, "--sidebar-ribbon-compact-width": `${SIDEBAR_RIBBON_WIDTH}px`, "--sidebar-ribbon-extra": "0px",
    } as React.CSSProperties : { display: "flex", flex: 1, minHeight: 0 }}>
      {params.has("sidebar") ? <Sidebar /> : null}
      <main className="conversation-pane" style={{ flex: 1 }} data-primary-plugin-view={pluginPageVisible ? "" : undefined}><Titlebar /></main>
      <DesktopWorkbench host={host} controller={controller} />
    </div>
  </> : region === "settings" ? <div className="settings-shell" style={{ flex: 1, gridTemplateColumns: "0 minmax(0, 1fr)" }}><div className="settings-main"><div className="settings-page" style={{ gridRow: 2, height: "100%" }}>{content}</div></div></div>
    : <div className="workspace-panel-body" style={{ minHeight: 0, flex: 1 }}>{content}</div>}</>;
}
style.textContent += "#root {display:flex; flex-direction:column;}";
createRoot(document.getElementById("root")!).render(
  <I18nProvider preferenceStore={{ get: () => params.get("locale") === "en-US" ? "en-US" : "zh-CN", set: async () => {}, subscribe: () => () => {} }}>
    <WuuUIRoot><Preview /></WuuUIRoot>
  </I18nProvider>,
);
