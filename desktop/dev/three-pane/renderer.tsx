import { type CSSProperties, useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { ArrowUp, MessageSquarePlus, PanelLeft, Plus, Search, Settings } from "../../src/renderer/WuuIcons";
import { ProjectGroup } from "../../src/renderer/ThreadSidebar";
import { TurnView } from "../../src/renderer/TurnView";
import { WorkspaceRightPanel, type WorkspacePanelView } from "../../src/renderer/WorkspacePanels";
import type { WorkspaceViewTab } from "../../src/renderer/WorkspaceViewTabs";
import type { ThreadSummary } from "../../src/renderer/AppState";
import type { WuuDesktopApi, Turn } from "../../src/shared/protocol";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { AppBackground } from "../../src/renderer/background/AppBackground";
import { BackgroundSettings } from "../../src/renderer/background/BackgroundSettings";
import { EmptyConversationHome } from "../../src/renderer/LoadingViews";
import "../../src/renderer/styles.css";
import "./fixture.css";

const date = "2026-09-17T00:00:00Z";
const noop = () => {};
const answer = `这次调整以三栏布局为基准，让导航、阅读与工作区各自清楚，又属于同一套界面。

## 清晰的阅读顺序

消息正文与输入文字保持同一条对齐轴。段落之间留出呼吸空间，过程记录以次要层级呈现，操作靠近对应内容。

- 左栏保留紧凑的项目与会话层级。
- 中间集中呈现任务进展与最终结果。
- 右栏使用居中的工具列表，减少无意义的卡片。

### 放大后依然可读

同一套 UI 字体覆盖两侧导航和正文；代码继续使用独立字号。长标题正常省略，文件名不会与状态或操作重叠。

\`\`\`ts
const layout = { navigation: true, reading: true };
\`\`\`

这是一组用于排版验收的示例内容。`;
const turn: Turn = { id: "sample-turn", status: "completed", duration_ms: 9000, items_view: "full", items: [
  { id: "sample-user", type: "user_message", status: "completed", text: "参考三栏布局，统一消息流与两侧栏的排版。" },
  { id: "sample-answer", type: "agent_message", terminal: true, status: "completed", text: answer },
] };
const titles = ["优化软件排版问题", "检查长标题、运行中与未读状态的组合", "消息流阅读节奏与输入框对齐", "修复导航节点重复", "检查分叉会话与项目层级"];
const threads: ThreadSummary[] = titles.map((preview, i) => ({ id: `sample-${i}`, preview, model_provider: "preview", model: "preview", cwd: "/preview", status: i === 1 ? "in_progress" : "idle", created_at: date, updated_at: date, turn_count: 1, latest_completed_turn_id: "sample-turn", turns: [], ...(i === 4 ? { forked_from_id: "sample-0" } : {}) }));

function Fixture() {
  const params = new URLSearchParams(location.search);
  const [size, setSize] = useState(Number(params.get("size")) || 14);
  const [theme, setTheme] = useState(params.get("theme") || "light");
  const [active, setActive] = useState("sample-0");
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set(["sample-project"]));
  const [tabs, setTabs] = useState<WorkspaceViewTab[]>([]);
  const [activeTab, setActiveTab] = useState<string>();
  const [rightOpen, setRightOpen] = useState(true);
  const dock = useRef<HTMLElement>(null);
  const [dockHeight, setDockHeight] = useState(0);
  useLayoutEffect(() => { applyMessageFlowFontSize(size); document.documentElement.dataset.theme = theme; }, [size, theme]);
  useLayoutEffect(() => {
    if (!dock.current) return;
    const element = dock.current;
    const update = () => setDockHeight(element.getBoundingClientRect().height);
    const observer = new ResizeObserver(update); observer.observe(element); update();
    return () => observer.disconnect();
  }, []);
  function openTool(kind: WorkspacePanelView) { setTabs(current => current.some(tab => tab.id === kind) ? current : [...current, { id: kind, kind }]); setActiveTab(kind); }
  return <WuuUIRoot><ImagePreviewProvider>
    <AppBackground />
    <div className="sample-controls"><span>当前源码 · 示例数据</span><label>主题 <select value={theme} onChange={e => setTheme(e.target.value)}><option value="light">亮色</option><option value="dark">暗色</option></select></label><label>字号 <select value={size} onChange={e => setSize(Number(e.target.value))}>{[13,14,20].map(n => <option key={n}>{n}</option>)}</select></label><button onClick={() => setRightOpen(value => !value)}>切换右栏</button></div>
    <div className={`app-shell sample-shell${rightOpen ? " right-panel-open" : ""}`}>
      <aside className="sidebar"><div className="sidebar-content">
        <div className="sample-brand">Wuu</div>
        <nav className="primary-nav"><button className="nav-item"><MessageSquarePlus/><span>新对话</span></button><button className="nav-item"><Search/><span>搜索会话</span></button></nav>
        <div className="sidebar-main"><section className="sidebar-functional-group"><div className="sidebar-functional-heading"><span className="sidebar-functional-heading-label">工作区</span></div>
          <div className="project-section"><ProjectGroup project={{ id: "sample-project", name: "wuu", path: "/preview", created_at: date, updated_at: date }} expandedSidebarSectionIDs={expanded} threadsByProjectID={{ "sample-project": threads }} activeThreadID={active} lastViewedTurnByThreadID={{ "sample-0": "sample-turn", "sample-2": "sample-turn", "sample-3": "sample-turn", "sample-4": "sample-turn" }} scratchPseudoProjectID="scratch" scratchPseudoActive={false} onToggleSidebarSectionCollapsed={id => setExpanded(current => current.has(id) ? new Set() : new Set([id]))} onStartNewThread={noop} onSelectThread={(_project, id) => setActive(id)} onToggleThreadPinned={noop} onArchiveThread={noop} onDeleteThread={noop}/></div>
        </section></div><button className="nav-item"><Settings/><span>设置</span></button>
      </div></aside>
      <main className="conversation-pane" style={{ "--dock-composer-height": `${dockHeight}px` } as CSSProperties}>
        <header className="titlebar"><div className="title-block"><PanelLeft className="icon"/><span>优化软件排版问题</span></div></header>
        <div className={`scroll-region${params.has("empty") ? " empty-scroll-region" : ""}`}>
          {params.has("empty") ? <EmptyConversationHome title="晚上好，今天还想在 wuu 里处理什么？" />
            : <div className="conversation-width session-flow">{params.has("background") ? <div className="sample-background-settings"><BackgroundSettings /></div> : <TurnView turn={turn} onStreamFrame={noop} isLatestTurn latestAgentMessageID="sample-answer"/>}</div>}
        </div>
        <footer ref={dock} className="composer-wrap dock-composer-wrap"><div className="composer-stack"><div className="composer-shell"><div className="composer-frame-shell"><div className="composer-frame"><div className="composer"><textarea aria-label="示例输入" placeholder="即刻开始"/><div className="composer-bar"><div className="composer-bar-left"><button className="composer-tool-button" aria-label="附件"><Plus className="icon"/></button></div><div className="composer-bar-right"><button className="codex-runtime-trigger">Wuu · 示例模型</button><button className="composer-action-button composer-send-button" disabled aria-label="发送"><ArrowUp className="icon"/></button></div></div></div></div></div></div></div></footer>
      </main>
      <WorkspaceRightPanel open={rightOpen} present={rightOpen} tabs={tabs} activeTabID={activeTab} activeContext={{ kind: "no_project", cwd: "/preview" }} workspaceContext={{ kind: "no_project", cwd: "/preview" }} onSelectTab={setActiveTab} onOpenTool={openTool} onShowTools={() => setActiveTab(undefined)} onCloseTab={id => { setTabs(current => current.filter(tab => tab.id !== id)); setActiveTab(undefined); }} onReorderTabs={noop} onOpenFile={noop} onClose={() => setRightOpen(false)} globalized={false} onToggleGlobalize={noop}/>
    </div>
  </ImagePreviewProvider></WuuUIRoot>;
}

if (import.meta.env.DEV) {
  // This standalone entry uses synthetic files only; no product preload or core.
  window.wuu = {
    listWorkspaceDirectory: async (path = "") => ({ root: "/preview", path, truncated: false, entries: path ? [] : ["README.md", "排版验收说明.md", "long-file-name-for-truncation-review.ts", ...Array.from({ length: 40 }, (_, i) => `component-${i}.tsx`)].map(name => ({ name, path: name, kind: "file" })) }),
  } as unknown as WuuDesktopApi;
  createRoot(document.getElementById("root")!).render(<Fixture/>);
}
