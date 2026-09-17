import { type CSSProperties, useEffect, useLayoutEffect, useRef, useState } from "react";
import * as React from "react";
import { createRoot } from "react-dom/client";
import { ArrowUp, Folder, Plus, Search } from "lucide-react";
import { SelectMenu } from "../../src/renderer/SelectMenu";
import { TurnView } from "../../src/renderer/TurnView";
import { applyMeasuredScrollbarWidth } from "../../src/renderer/ScrollbarMetrics";
import { SidebarNameDialog } from "../../src/renderer/SidebarNameDialog";
import type { Turn } from "../../src/shared/protocol";
import { SettingsRow } from "../../src/renderer/SettingsRow";
import { TurnEditSummaryCard } from "../../src/renderer/TurnEditSummaryCard";
import { Modal } from "../../src/renderer/Modal";
import { createPluginUIKit } from "../../src/shared/workbench";
import { RichContent } from "../../src/renderer/RichContent";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import "../../src/renderer/styles.css";
import "./fixture.css";

const options = [
  { value: "local", label: "本地工作区 / Local workspace", hint: "文件保存在当前电脑，可随时切换工作区。" },
  { value: "remote", label: "远程开发环境 / Remote development", hint: "通过现有连接访问远端文件与工具。" },
  { value: "disabled", label: "暂不可用 / Unavailable", hint: "此项用于检查禁用状态。", disabled: true },
];
const answer = `一套界面需要同时照顾阅读和操作。输入框、回复正文与过程摘要共享一组左右边界；屏幕变宽后，内容仍然保持集中。

## 清楚的工作顺序

- **理解任务。** 信息按重要程度展开，保留必要的上下文。
- **查看进展。** 中间过程可以展开，需要时再检查详细日志。
- **确认结果。** 正文、代码和文件保持可读，操作出现在相关内容附近。

This paragraph checks mixed-script reading, **emphasis**, a [reference link](https://example.com), and an inline path: \`desktop/src/renderer/styles/session.css\`.

\`\`\`ts
const result = await runTask({ workspace: "example", mode: "review" });
\`\`\`
`;

function Fixture() {
  const params = new URLSearchParams(location.search);
  const [theme, setTheme] = useState(params.get("theme") || "light");
  const [size, setSize] = useState(Number(params.get("size")) || 14);
  const [surface, setSurface] = useState(params.get("surface") || "controls");
  const [density, setDensity] = useState(Number(params.get("density")) || 1);
  const [sync, setSync] = useState(true);
  const [dialog, setDialog] = useState(false);
  const [name, setName] = useState("项目笔记");
  const [value, setValue] = useState("local");
  useEffect(() => { document.documentElement.dataset.theme = theme; }, [theme]);
  useEffect(() => { applyMessageFlowFontSize(size); }, [size]);
  useEffect(() => { document.documentElement.style.setProperty("--wuu-space-density", String(density)); }, [density]);
  useLayoutEffect(() => { applyMeasuredScrollbarWidth(); }, []);
  return <WuuUIRoot><ImagePreviewProvider>
    <header className="review-toolbar">
      <strong>生产组件对照 · 示例数据</strong>
      <label>主题<select value={theme} onChange={e => setTheme(e.target.value)}><option value="light">亮色</option><option value="dark">暗色</option></select></label>
      <label>字号<select value={size} onChange={e => setSize(Number(e.target.value))}>{[13, 14, 20].map(n => <option key={n} value={n}>{n}px</option>)}</select></label>
      <label>密度<select value={density} onChange={e => setDensity(Number(e.target.value))}><option value={1}>标准</option><option value={0.75}>紧凑</option><option value={1.25}>宽松</option></select></label>
      <label>页面<select value={surface} onChange={e => setSurface(e.target.value)}><option value="controls">设置与菜单</option><option value="conversation">对话与输入</option><option value="messages">各模式消息</option><option value="extension">插件控件</option><option value="spacing">间距与文件变更</option></select></label>
    </header>
    {surface === "spacing" ? <SpacingExample theme={theme} size={size} density={density} /> : surface === "controls" ? <main className="settings-page review-settings">
      <header className="settings-page-header"><h1 className="settings-page-title">常规</h1></header>
      <section className="settings-section"><h2 className="settings-section-title">工作环境</h2><div className="settings-group">
        <SettingsRow title="工作环境" description="同一设置行同时检查长标签、说明与选择控件。"><div className="review-menu"><SelectMenu ariaLabel="工作环境" value={value} onChange={setValue} options={options} triggerClassName="settings-select-trigger" searchable searchPlaceholder="搜索环境" /></div></SettingsRow>
        <SettingsRow title="名称" description="输入、菜单和按钮应在同一行保持一致的高度。"><input className="settings-input" aria-label="名称" placeholder="输入名称" /></SettingsRow>
        <SettingsRow title="自动同步"><button className="settings-switch" role="switch" aria-checked={sync} aria-label="自动同步" onClick={() => setSync(!sync)}><span className="settings-switch-thumb" /></button></SettingsRow>
      </div></section>
      <section className="settings-section"><h2 className="settings-section-title">共享表单控件</h2><div className="review-controls"><input className="settings-input" aria-label="项目名称" placeholder="项目名称" /><SelectMenu ariaLabel="普通选择器" value={value} onChange={setValue} options={options} /><button className="settings-button">取消</button><button className="settings-button settings-button-primary">保存</button><button className="settings-button" disabled>不可用</button></div></section>
      <section className="settings-section"><h2 className="settings-section-title">搜索与键盘操作</h2><label className="menu-search"><Search className="icon"/><input aria-label="搜索示例" placeholder="搜索项目、会话或模型" /></label></section>
      <section className="settings-section"><h2 className="settings-section-title">说明与反馈</h2><p className="settings-section-description">次要说明需要清楚可读；焦点、悬停和选中应能区分。这里的内容只在验收页展示，不会写入设置或发送消息。</p><div className="review-controls"><button className="settings-button settings-button-danger">移除</button><button className="settings-button" onClick={() => setDialog(true)}>重命名示例</button><button className="icon-button" aria-label="新建"><Plus className="icon" /></button></div></section>
    </main> : surface === "messages" ? <main className="review-page review-messages">
      <h2>同一消息在三种视图中的配色</h2>
      <section><h3>主对话</h3><article className="message user-message"><RichContent text={answer}/></article></section>
      <section><h3>聊天</h3><article className="chat-bubble chat-bubble--user"><RichContent text={answer}/></article></section>
      <section><h3>协作</h3><article className="channel-message own"><div className="channel-message-bubble"><RichContent text={answer}/></div></article></section>
    </main> : surface === "extension" ? <main className="settings-page review-settings">
      <header className="settings-page-header"><h1 className="settings-page-title">插件设置</h1></header>
      <section className="settings-section"><h2 className="settings-section-title">工作笔记</h2><div className="settings-group">
        <SettingsRow title="笔记目录" description="较长说明会换行，控件保持自己的边界与点击空间。"><input className="plugin-ui-input" aria-label="笔记目录" placeholder="选择目录" /></SettingsRow>
        <SettingsRow title="自动保存" description="此开关与主应用设置共用视觉定义。"><label className="plugin-ui-checkbox"><input type="checkbox" checked={sync} aria-label="插件自动保存" onChange={e => setSync(e.target.checked)}/></label></SettingsRow>
        <SettingsRow title="附加说明" block><textarea className="plugin-ui-textarea" aria-label="附加说明" placeholder="输入说明" /></SettingsRow>
      </div></section><section className="settings-section"><div className="review-controls"><button className="plugin-ui-button">取消</button><button className="plugin-ui-button" data-wuu-variant="primary">保存</button><button className="plugin-ui-button" disabled>不可用</button></div></section>
    </main> : <ConversationExample/>}
    <SidebarNameDialog open={dialog} title={name} onTitleChange={setName} onSubmit={() => setDialog(false)} onClose={() => setDialog(false)} dialogTitle="重命名项目" dialogTitleId="review-dialog-title" fieldLabel="项目名称" fieldAriaLabel="弹层项目名称" placeholder="项目名称" icon={Folder} submitLabel="保存" cancelLabel="取消"/>

  </ImagePreviewProvider></WuuUIRoot>;
}

const ui = createPluginUIKit(React);
const editTurn = (paths: string[]): Turn => ({
  id: "spacing-edits", status: "completed", items_view: "full",
  items: paths.map((path, index) => ({
    id: `edit-${index}`, type: "tool_call", name: "edit_file", status: "completed",
    result: JSON.stringify({ path, diff: { hunks: [{ old_start: 1, new_start: 1, lines: [
      { op: "delete", content: "const oldValue = true;" },
      { op: "insert", content: "const newValue = true;" },
    ] }] } }),
  })),
});

function SpacingExample({ theme, size, density }: { theme: string; size: number; density: number }) {
  const [dialog, setDialog] = useState(false);
  const [value, setValue] = useState("local");
  const [metrics, setMetrics] = useState("");
  const page = useRef<HTMLElement>(null);
  useEffect(() => {
    const element = page.current!;
    const measure = () => {
      const height = (selector: string) => element.querySelector(selector)!.getBoundingClientRect().height;
      const inset = (selector: string) => getComputedStyle(element.querySelector(selector)!).paddingLeft;
      setMetrics(`宿主 / 插件输入高度 ${height('.settings-input').toFixed(1)} / ${height('.plugin-ui-input').toFixed(1)}；单文件卡片 ${height('.is-single').toFixed(1)}；插件标准 / 紧凑卡片内边距 ${inset('.review-plugin-standard .plugin-ui-card')} / ${inset('.review-plugin-compact .plugin-ui-card')}`);
    };
    const frame = requestAnimationFrame(measure);
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => { cancelAnimationFrame(frame); observer.disconnect(); };
  }, [theme, size, density]);
  return <main ref={page} className="settings-page review-settings review-spacing">
    <output className="review-metrics">{metrics}</output>
    <section className="settings-section"><h2>文件变更</h2>
      <TurnEditSummaryCard turn={editTurn(["src/session.css"])} onOpenFile={() => setDialog(true)} />
      <TurnEditSummaryCard turn={editTurn(["src/components/a-very-long-directory-name/another-directory/LongFileName.tsx", "src/settings.css", "src/empty.ts"])} onOpenFile={() => setDialog(true)} />
    </section>
    <section className="settings-section"><h2>宿主与插件表单</h2><div className="review-controls">
      <label className="plugin-ui-field"><span className="plugin-ui-field-label">宿主输入</span><input className="settings-input" aria-label="宿主输入" placeholder="宿主输入" /></label>
      <ui.TextInput label="插件输入" placeholder="插件输入" />
      <SelectMenu ariaLabel="间距验收菜单" value={value} onChange={setValue} options={options} searchable />
      <button className="settings-button" onClick={() => setDialog(true)}>打开弹窗</button><ui.Button disabled>不可用</ui.Button>
    </div></section>
    <section className="settings-section"><h2>局部密度</h2>
      <ui.Page className="review-plugin-standard"><ui.Card><ui.Stack><span>跟随主题密度</span><ui.TextInput label="标准密度输入" placeholder="标准密度输入" /><ui.Button>操作</ui.Button></ui.Stack></ui.Card></ui.Page>
      <ui.Page density="compact" className="review-plugin-compact"><ui.Card><ui.Stack><span>局部紧凑密度</span><ui.TextInput label="紧凑密度输入" placeholder="紧凑密度输入" /><ui.Button>操作</ui.Button></ui.Stack></ui.Card></ui.Page>
    </section>
    {dialog ? <Modal title="检查留白" ariaLabel="间距验收弹窗" onClose={() => setDialog(false)} footer={<button className="settings-button" onClick={() => setDialog(false)}>关闭</button>}><p>正文随窗口换行，面板负责外沿，内容不重复增加内边距。</p><input className="settings-input" aria-label="弹窗输入" placeholder="名称" /></Modal> : null}
  </main>;
}


const turns: Turn[] = [{
  id: "review-turn", status: "completed", items_view: "full", duration_ms: 14000,
  items: [
    { id: "request", type: "user_message", status: "completed", text: "让消息流、设置和工作区拥有一致的阅读节奏，放大字号后依然清楚。" },
    { id: "answer", type: "agent_message", status: "completed", terminal: true, text: answer },
  ],
}];

function ConversationExample() {
  const dock = useRef<HTMLElement>(null);
  const [height, setHeight] = useState(0);
  useLayoutEffect(() => {
    const element = dock.current;
    if (!element) return;
    const update = () => setHeight(element.getBoundingClientRect().height);
    update();
    const observer = new ResizeObserver(update);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return <main className="conversation-pane review-conversation" style={{ "--dock-composer-height": `${height}px` } as CSSProperties}>
    <header className="titlebar"><span>视觉系统 · 示例对话</span></header>
    <div className="scroll-region"><div className="conversation-width session-flow">
      {turns.map(turn => <TurnView key={turn.id} turn={turn} onStreamFrame={() => {}} isLatestTurn latestAgentMessageID="answer" />)}
    </div></div>
    <footer ref={dock} className="composer-wrap dock-composer-wrap"><div className="composer-stack"><div className="composer-shell"><div className="composer-frame-shell"><div className="composer-frame"><div className="composer">
      <textarea aria-label="示例消息" placeholder="即刻开始" />
      <div className="composer-bar"><div className="composer-bar-left"><button className="composer-tool-button" aria-label="附件"><Plus className="icon"/></button></div><div className="composer-bar-right"><button className="codex-runtime-trigger">Wuu · 示例模型</button><button className="composer-send-button" disabled aria-label="发送"><ArrowUp className="icon"/></button></div></div>
    </div></div></div></div></div></footer>
  </main>;
}

if (import.meta.env.DEV) createRoot(document.getElementById("root")!).render(<Fixture/>);
