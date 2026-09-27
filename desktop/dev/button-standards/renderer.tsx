import { useState } from "react";
import { createRoot } from "react-dom/client";
import { Plus, X, Info, Maximize2, Minimize2, RotateCw, Copy, Paperclip, Settings2, Trash2 } from "../../src/renderer/WuuIcons";
import { SidePanelToggleIcon } from "../../src/renderer/SidePanelToggleIcon";
import { startFocusModality } from "../../src/renderer/FocusModality";
import "../../src/renderer/styles.css";
import "./fixture.css";

startFocusModality();

function Fixture() {
  const [theme, setTheme] = useState("light");
  const [scale, setScale] = useState("1");
  const [open, setOpen] = useState(false);
  const [info, setInfo] = useState(false);
  const [tools, setTools] = useState(false);
  const [full, setFull] = useState(false);
  const [narrow, setNarrow] = useState(false);
  return <main className="button-standard">
    <header className="standard-heading"><h1>按钮标准</h1><nav>
      <label>主题<select value={theme} onChange={event => {setTheme(event.target.value); document.documentElement.dataset.theme = event.target.value;}}><option value="light">浅色</option><option value="dark">深色</option></select></label>
      <label>缩放<select value={scale} onChange={event => {setScale(event.target.value); document.documentElement.style.setProperty("--appearance-scale", event.target.value);}}><option value="0.85">85%</option><option value="1">100%</option><option value="1.2">120%</option></select></label>
      <button className="settings-button" aria-pressed={narrow} onClick={() => setNarrow(!narrow)}>{narrow ? "恢复宽布局" : "窄布局"}</button>
    </nav></header>
    <section><h2>顶栏 · 30px 操作区</h2><div className={`standard-shell${narrow ? " narrow" : ""}`}>
      <header className="titlebar">
        <div className="standard-tabs"><button className="icon-button sidebar-toggle-button side-panel-toggle-button" aria-label="左侧栏" aria-pressed={open} onClick={() => setOpen(!open)}><SidePanelToggleIcon side="left" open={open}/></button><span className="standard-tab"><span>已完成第一轮优化：菜单文字与布局</span><button className="session-tab-close" aria-label="关闭标签"><X className="icon-xs"/></button></span><button className="icon-button workspace-panel-add session-tab-new" aria-label="新建对话"><Plus className="icon-lg"/></button></div>
        <div className="title-actions"><button className={`icon-button environment-toggle-button${info ? " active" : ""}`} aria-label="环境信息" aria-pressed={info} onClick={() => setInfo(!info)}><Info className="icon-lg"/></button><button className="icon-button side-panel-toggle-button" aria-label="右侧栏" aria-pressed={open} onClick={() => setOpen(!open)}><SidePanelToggleIcon side="right" open={open}/></button></div>
      </header>
      <div className="workspace-panel-tabbar"><span className="standard-panel-space"/><button className={`icon-button workspace-panel-add${tools ? " active" : ""}`} aria-label="添加工具" aria-pressed={tools} onClick={() => setTools(!tools)}><Plus className="icon-lg"/></button><button className={`icon-button workspace-panel-globalize${full ? " active" : ""}`} aria-label="铺满面板" aria-pressed={full} onClick={() => setFull(!full)}>{full ? <Minimize2 className="icon"/> : <Maximize2 className="icon"/>}</button><button className="icon-button workspace-panel-close" aria-label="关闭面板"><X className="icon"/></button></div>
    </div></section>
    <section><h2>状态与轮廓</h2><p>悬停查看底色，Tab 检查焦点；已选中和禁用保留独立语义。</p><div className="standard-states">{["默认", "已选中", "禁用"].map(state => <div className="standard-state" key={state}><span>{state}</span><div className="standard-controls">{[Plus, Info, Maximize2, X, Copy, RotateCw].map((Icon, index) => <button key={index} className={`icon-button${state === "已选中" ? " active" : ""}`} disabled={state === "禁用"} aria-label={`${state} ${index + 1}`}><Icon className="icon"/></button>)}</div></div>)}</div></section>
    <section><h2>其他按钮</h2><div className="standard-examples">
      <div><h3>行内 · 24px</h3><div className="standard-controls"><button className="sidebar-row-icon-button" aria-label="添加工作区"><Plus className="icon-sm"/></button><button className="sidebar-row-icon-button" aria-label="工作区设置"><Settings2 className="icon-sm"/></button><button className="workspace-tool-tab-close" aria-label="关闭工具标签"><X className="icon-xs"/></button></div></div>
      <div><h3>输入工具</h3><div className="standard-controls"><button className="composer-tool-button" aria-label="更多输入工具"><Plus className="icon"/></button><button className="composer-tool-button" aria-label="附件"><Paperclip className="icon"/></button></div></div>
      <div><h3>表单 · 与输入框等高</h3><div className="standard-controls"><button className="settings-button settings-icon-button" aria-label="刷新"><RotateCw className="icon"/></button><button className="settings-button">取消</button><button className="settings-button settings-button-primary">保存</button><button className="settings-button settings-button-danger"><Trash2 className="icon"/>删除</button></div></div>
    </div></section>
    <section><h2>尺寸与描边</h2><p>标准图标 16px，行内图标 14px，24 单位画布的描边为 1.75。加号放大 10/9、叉号放大 7/6，并反向补偿线宽；方框比圆形收小一圈。图标随外观缩放，触控顶栏的操作区至少 44px。</p></section>
  </main>;
}
createRoot(document.getElementById("root")!).render(<Fixture/>);
