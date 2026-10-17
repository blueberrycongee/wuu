import * as React from "react";
import { createRoot } from "react-dom/client";
import { ArrowLeft, ArrowUp, Check, ChevronDown, ChevronRight, Clock3, FileCode2, Folder, GitBranch, LoaderCircle, Moon, PanelLeft, PanelRight, Plus, Search, Sun, TriangleAlert, X } from "lucide-react";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import "../../src/renderer/styles.css";
import "./style.css";

type State = "running" | "queued" | "completed" | "failed";
type Task = { id: string; name: string; role: string; tone: number; state: State; brief: string; preview: string; result: string; time: string; files: string[] };
const baseTasks: Task[] = [
  { id: "routing", name: "Atlas", role: "检查路由与会话切换", tone: 0, state: "running", brief: "检查会话切换和异步请求返回后的视图归属。重点关注快速切换时，旧请求是否会更新新会话。请返回具体问题、代码位置和复现条件。", preview: "正在核对异步响应的会话归属", result: "已经梳理了会话切换的入口，正在检查请求返回时的视图归属判断。接下来会核对快速连续切换的恢复行为。", time: "2 分钟", files: ["App.tsx", "SessionNavigation.ts"] },
  { id: "layout", name: "Nova", role: "检查侧栏与布局", tone: 1, state: "running", brief: "检查长标题、大字体和窄窗口下的会话侧栏。记录文字溢出、状态图标和点击区域的问题。", preview: "正在检查大字体下的行高与对齐", result: "默认字号下的侧栏布局已经检查完毕，正在查看 20px 字号下的长标题和尾部操作是否重叠。", time: "2 分钟", files: ["ThreadSidebar.tsx", "workspace.css"] },
  { id: "regression", name: "Milo", role: "运行回归检查", tone: 2, state: "queued", brief: "运行现有的会话切换和侧栏回归检查，整理失败日志及复现条件。", preview: "等待测试环境就绪", result: "任务已排队，尚未开始执行。", time: "等待中", files: [] },
  { id: "protocol", name: "Iris", role: "核对消息协议", tone: 3, state: "completed", brief: "核对桌面端和服务端的会话消息协议，确认创建、发送和结果返回的状态映射一致。", preview: "消息协议与状态映射一致", result: "已核对会话创建、消息发送和结果返回的字段映射，检查范围内未发现协议不一致。\n\n会话标识会随请求返回，主会话可以据此判断结果归属。后续应结合快速切换场景验证 UI 是否正确使用这个标识。", time: "刚刚", files: ["protocol.ts", "session.go"] },
  { id: "docs", name: "Echo", role: "梳理文档影响", tone: 4, state: "completed", brief: "确认本次会话体验优化涉及的用户文档，给出需要更新的说明。", preview: "已整理两处需要更新的说明", result: "需要更新子任务状态查看的说明，以及从消息流打开子代理详情的操作示例。", time: "1 分钟前", files: ["subagents.md"] },
];
const labels: Record<State, string> = { running: "工作中", queued: "等待中", completed: "已完成", failed: "失败" };
const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") === "dark" ? "dark" : "light";
applyMessageFlowFontSize(params.get("font") === "20" ? 20 : 15);

function Avatar({ task, small = false }: { task: Task; small?: boolean }) {
  return <span className={`agent-avatar tone-${task.tone % 5} ${small ? "is-small" : ""}`} aria-hidden="true"><svg viewBox="0 0 24 24" fill="none"><path d={task.tone % 2 ? "M5 8Q5 3 12 4Q19 3 19 8L21 17Q20 21 12 21Q4 21 3 17Z" : "M3 12Q3 4 12 3Q21 4 21 12L20 19Q12 23 4 19Z"} fill="currentColor"/><circle cx="8.5" cy="12" r="1.35" fill="var(--avatar-eye)"/><circle cx="15.5" cy="12" r="1.35" fill="var(--avatar-eye)"/><path d="M9 16Q12 18 15 16" stroke="var(--avatar-eye)" strokeWidth="1.2" strokeLinecap="round"/></svg></span>;
}

function Status({ state }: { state: State }) {
  const Icon = state === "running" ? LoaderCircle : state === "queued" ? Clock3 : state === "failed" ? TriangleAlert : Check;
  return <span className={`task-status is-${state}`}><Icon size={14} aria-hidden="true"/>{labels[state]}</span>;
}

function Activity({ tasks, ending, onSelect, onOverview }: { tasks: Task[]; ending: string; onSelect: (id: string, element: HTMLElement) => void; onOverview: (element: HTMLElement) => void }) {
  if (!tasks.length) return null;
  const named = tasks.length > 3 ? tasks.slice(0, 2) : tasks;
  return <div className="inline-activity" aria-label="子代理活动">
    <span className="activity-avatars" aria-hidden="true">{tasks.slice(0, 4).map(task => <Avatar key={task.id} task={task} small/>)}</span>
    <span className="activity-sentence">{named.map((task, i) => <React.Fragment key={task.id}>{i > 0 ? i === named.length - 1 && tasks.length <= 3 ? " 和 " : "、" : ""}<button className="inline-agent-name" title={task.role} onClick={event => onSelect(task.id, event.currentTarget)}>{task.name}</button></React.Fragment>)}{tasks.length > 3 && <> 和 <button className="inline-agent-name" onClick={event => onOverview(event.currentTarget)}>另外 {tasks.length - 2} 个子代理</button></>}<span> {ending}</span></span>
  </div>;
}

function Preview() {
  const [scenario, setScenario] = React.useState(params.get("scenario") || "parallel");
  const [panel, setPanel] = React.useState<string | null>(params.get("panel"));
  const [showAll, setShowAll] = React.useState(false);
  const [dark, setDark] = React.useState(params.get("theme") === "dark");
  const [large, setLarge] = React.useState(params.get("font") === "20");
  const [draft, setDraft] = React.useState("");
  const lastTrigger = React.useRef<HTMLElement | null>(null);
  const panelHeading = React.useRef<HTMLHeadingElement>(null);
  const tasks = React.useMemo(() => {
    if (scenario === "empty") return [];
    if (scenario === "three") return baseTasks.slice(0, 3);
    if (scenario === "done") return baseTasks.map(task => ({ ...task, state: "completed" as State, time: task.state === "completed" ? task.time : "刚刚", preview: task.state === "completed" ? task.preview : "检查完成，结果已返回", result: task.state === "completed" ? task.result : "检查已完成，结果已返回主会话，等待进一步核对和整合。" }));
    if (scenario === "failure") return baseTasks.map(task => task.id === "regression" ? { ...task, state: "failed" as State, preview: "测试服务连接失败", result: "回归检查未能启动：测试服务连接失败。需要检查测试环境，然后重新运行。当前没有可用的检查结果。" } : task);
    if (scenario === "many") return [...baseTasks, ...["Sage", "Orion", "Luna", "Finn", "Arlo", "Vega", "Theo"].map((name, i): Task => ({ ...baseTasks[0], id: `extra-${i}`, name, tone: i % 5, role: ["检查移动端长标题与窄窗口下的会话导航及子代理状态展示", "核对插件生命周期", "检查输入框焦点恢复", "整理可访问性问题", "检查历史持久化", "审查资源清理", "汇总验证结果"][i], preview: i < 3 ? "正在检查相关代码路径" : "等待前置任务完成", state: i < 3 ? "running" : "queued", time: i < 3 ? "2 分钟" : "等待中" }))];
    return baseTasks;
  }, [scenario]);
  const selected = tasks.find(task => task.id === panel);
  const active = tasks.filter(task => task.state === "running" || task.state === "queued");
  const finished = tasks.filter(task => task.state === "completed" || task.state === "failed");
  const completed = tasks.filter(task => task.state === "completed");
  const failed = tasks.filter(task => task.state === "failed");
  React.useEffect(() => { document.documentElement.dataset.theme = dark ? "dark" : "light"; }, [dark]);
  React.useEffect(() => { applyMessageFlowFontSize(large ? 20 : 15); }, [large]);
  React.useEffect(() => { if (panel) panelHeading.current?.focus({ preventScroll: true }); }, [panel]);
  const openTask = (id: string, trigger: HTMLElement) => { lastTrigger.current = trigger; setPanel(id); };
  const openOverview = (trigger: HTMLElement) => { lastTrigger.current = trigger; setPanel("overview"); };
  const closePanel = () => { setPanel(null); requestAnimationFrame(() => lastTrigger.current?.focus({ preventScroll: true })); };
  const renderRow = (task: Task) => <button key={task.id} className="overview-agent" onClick={() => setPanel(task.id)} aria-label={`查看 ${task.name}：${task.role}`}>
    <Avatar task={task}/><span className="overview-agent-body"><span className="overview-agent-top"><span>{task.name}</span><span className={task.state === "failed" ? "failed-label" : ""}>{task.state === "failed" ? "失败" : task.time}</span></span><span className="overview-objective">{task.role}</span><span className="overview-preview">{task.preview}</span></span><ChevronRight className="overview-chevron" size={14}/>
  </button>;
  return <div className="preview-shell">
    <div className="preview-toolbar" aria-label="预览控制">
      <span className="preview-label">子代理 · 消息流预览 <span>模拟数据</span></span>
      <select aria-label="预览场景" value={scenario} onChange={event => { setScenario(event.target.value); setPanel(null); setShowAll(false); }}>
        <option value="three">3 个子代理</option><option value="parallel">5 个子代理</option><option value="many">12 个子代理</option><option value="failure">出现失败</option><option value="done">全部完成</option><option value="empty">没有子代理</option>
      </select>
      <button className="toolbar-icon" aria-label="切换大字体" aria-pressed={large} onClick={() => setLarge(!large)}>Aa</button>
      <button className="toolbar-icon" aria-label="切换主题" onClick={() => setDark(!dark)}>{dark ? <Sun size={17}/> : <Moon size={17}/>}</button>
    </div>
    <div className="preview-app">
      <aside className="preview-sidebar">
        <div className="brand-line"><span className="wordmark">wuu</span><PanelLeft size={17}/></div>
        <div className="nav-line"><Plus size={17}/>新会话</div><div className="nav-line"><Search size={17}/>搜索会话</div>
        <div className="workspace-line"><Folder size={16}/><span>wuu</span><ChevronDown size={14}/></div>
        <div className="session-item selected"><span className="session-dot"/>检查会话与侧栏体验</div>
        <div className="session-item">优化消息阅读体验</div><div className="session-item">整理发布说明</div>
        <div className="sidebar-bottom"><GitBranch size={15}/><span>main</span><span className="local-label">本地</span></div>
      </aside>
      <main className={`preview-main ${panel ? "has-agent-panel" : ""}`}>
        <section className="conversation-area" aria-label="主会话">
          <header className="conversation-header"><span>检查会话与侧栏体验</span><span className="conversation-header-actions"><span className="header-project"><Folder size={14}/>wuu</span>{tasks.length > 0 && <button className={`icon-button ${panel ? "is-active" : ""}`} aria-label="查看全部子代理" title="子代理" onClick={event => panel ? closePanel() : openOverview(event.currentTarget)}><PanelRight size={17}/></button>}</span></header>
          <div className="preview-stream"><div className="stream-inner">
            <div className="message-user">帮我检查一下会话和侧栏相关的改动，几个独立部分可以并行处理，最后汇总结果。</div>
            <article className="assistant-turn">
              <p>{tasks.length ? scenario === "three" ? "我会把会话切换、布局和回归检查交给三个子代理并行处理。" : "我会把会话切换、布局和回归检查分开处理，同时核对消息协议和文档影响。" : "我先检查会话切换和侧栏相关的代码，再运行现有检查。"}</p>
              <Activity tasks={tasks} ending="开始工作" onSelect={openTask} onOverview={openOverview}/>
              <p>我继续检查主会话的状态更新逻辑，重点看快速切换会话时，异步返回的结果是否会落到错误的页面。</p>
              <div className="process-lines"><div><Search size={15}/><span>搜索会话切换的调用位置</span><Check size={14}/></div><div><FileCode2 size={15}/><span>读取 App.tsx 和 SessionNavigation.ts</span><Check size={14}/></div></div>
              {completed.length > 0 && <>
                <Activity tasks={completed} ending="已完成任务" onSelect={openTask} onOverview={openOverview}/>
                <p>{scenario === "done" ? "子代理的检查结果已经返回。我会逐项核对发现，并把需要处理的问题整理在一起。" : "协议检查暂未发现字段映射问题，文档需要更新两处说明。我会把这些结果和会话切换的检查一起核对。"}</p>
              </>}
              {failed.length > 0 && <div className="failed-activity"><TriangleAlert size={15}/><button className="inline-agent-name" onClick={event => openTask(failed[0].id, event.currentTarget)}>{failed[0].name}</button><span>的回归检查未能启动</span></div>}
              <div className="working-line"><span className="working-dot"/><span>{active.length ? "正在核对会话状态…" : "正在整理检查结果…"}</span></div>
            </article>
          </div></div>
          <div className="preview-dock"><div className="preview-composer"><textarea aria-label="发送给主会话" placeholder="继续补充要求…" value={draft} onChange={event => setDraft(event.target.value)} rows={2}/><div className="composer-bottom"><span><Plus size={18}/><span>Codex</span><ChevronDown size={13}/></span><button className="send-preview" aria-label="预览中不发送消息" disabled><ArrowUp size={17}/></button></div></div><div className="composer-note">本地工作区 <span>·</span> main</div></div>
        </section>
        {panel && <aside className="agent-sidebar" aria-label="子代理侧栏" onKeyDown={event => { if (event.key === "Escape") { event.stopPropagation(); closePanel(); } }}>
          <header className="agent-sidebar-header"><h2 ref={panelHeading} tabIndex={-1}>子代理</h2><button className="icon-button" aria-label="关闭子代理侧栏" onClick={closePanel}><X size={18}/></button></header>
          {selected ? <>
            <div className="agent-detail-heading"><button className="icon-button" aria-label="返回全部子代理" title="返回全部子代理" onClick={() => setPanel("overview")}><ArrowLeft size={17}/></button><Avatar task={selected}/><span className="agent-detail-name">{selected.name}</span><Status state={selected.state}/></div>
            <div className="agent-detail-scroll" key={selected.id}>
              <h3>{selected.role}</h3><div className="agent-model">Codex <span>·</span> {selected.state === "queued" ? "尚未开始" : selected.state === "completed" ? "检查结果已返回" : selected.state === "failed" ? "执行已结束" : `已运行 ${selected.time}`}</div>
              <section className="detail-section"><h4>任务</h4><p>{selected.brief}</p></section>
              {selected.files.length > 0 && <section className="detail-section"><h4>工作记录</h4><div className="detail-file-list">{selected.files.map(file => <div key={file}><FileCode2 size={15}/><span>{file}</span><Check size={14}/></div>)}</div></section>}
              <section className="detail-section"><h4>{selected.state === "completed" ? "返回结果" : selected.state === "failed" ? "失败原因" : selected.state === "queued" ? "状态" : "最新进展"}</h4><p className={selected.state === "failed" ? "failure-copy" : ""}>{selected.result}</p></section>
              {selected.state === "completed" && <div className="detail-delivery"><Check size={14}/><span>结果已返回主会话</span></div>}
            </div>
          </> : <div className="agent-overview">
            <section><div className="overview-section-title"><h3>进行中 <span>{active.length}</span></h3>{active.some(task => task.state === "queued") && <span>{active.filter(task => task.state === "queued").length} 个等待中</span>}</div>
              {active.length ? (showAll ? active : active.slice(0, 4)).map(renderRow) : <p className="section-empty">当前没有正在工作的子代理</p>}
              {active.length > 4 && <button className="show-more" onClick={() => setShowAll(!showAll)}>{showAll ? "收起" : `显示另外 ${active.length - 4} 个`}<ChevronDown size={14} className={showAll ? "rotate" : ""}/></button>}
            </section>
            {finished.length > 0 && <section><div className="overview-section-title"><h3>已结束 <span>{finished.length}</span></h3></div>{finished.map(renderRow)}</section>}
          </div>}
        </aside>}
      </main>
    </div>
  </div>;
}
createRoot(document.getElementById("root")!).render(<Preview/>);
