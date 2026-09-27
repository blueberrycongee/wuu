import { useState } from "react";
import { createRoot } from "react-dom/client";
import { Folder, ChevronDown, Plus } from "../../src/renderer/WuuIcons";
import { WorkspacePickerMenu } from "../../src/renderer/ComposerRuntimeMenus";
import { startFocusModality } from "../../src/renderer/FocusModality";
import "../../src/renderer/styles.css";
import "./fixture.css";

startFocusModality();

const projects = ["soda", "PilotDeck", "wuu"].map((name) => ({
  id: name, name, path: `/projects/${name}`, created_at: "", updated_at: "",
}));

function Fixture() {
  const [query, setQuery] = useState("");
  const [workspace, setWorkspace] = useState("wuu");
  const [open, setOpen] = useState(true);
  const [shadowsDisabled, setShadowsDisabled] = useState(false);
  return <main className="elevation-review">
    <header>
      <h1>阴影层级</h1>
      <p>工作区菜单、输入框、提示与弹窗 · 生产样式</p>
      <nav>
        <button onClick={() => { document.documentElement.dataset.theme = "light"; }}>浅色</button>
        <button onClick={() => { document.documentElement.dataset.theme = "dark"; }}>深色</button>
        <button aria-pressed={shadowsDisabled} onClick={() => {
          const disabled = !shadowsDisabled;
          setShadowsDisabled(disabled);
          for (const token of ["--wuu-elevation-panel", "--wuu-elevation-overlay"]) {
            const style = document.documentElement.style;
            if (disabled) style.setProperty(token, "none");
            else style.removeProperty(token);
          }
        }}>{shadowsDisabled ? "阴影：关闭（主题覆盖）" : "阴影：开启"}</button>
      </nav>
    </header>
    <div className="elevation-grid">
      <section className="elevation-composer-example">
        <h2>菜单 / 输入框</h2>
        <div className="elevation-project-slot">
          {open && <WorkspacePickerMenu projects={projects}
            activeContext={{ kind: "project", project_id: workspace, cwd: `/projects/${workspace}` }}
            query={query} setQuery={setQuery} onSelectWorkspace={setWorkspace}
            onSelectNoProject={() => setWorkspace("")} onCreateWorkspace={() => {}} onOpenWorkspace={() => {}} />}
        </div>
        <div className="composer-workspace-bar">
          <button className="hero-project-pill" onClick={() => setOpen(!open)}><Folder size={16} />{workspace || "无工作区"}<ChevronDown size={14} /></button>
        </div>
        <div className="composer-frame">
          <textarea className="elevation-input" placeholder="即刻开始" aria-label="消息" />
          <div className="elevation-input-actions"><Plus size={18} /><span>本地 · main</span></div>
        </div>
      </section>
      <section className="elevation-other-examples">
        <h2>模态弹窗</h2>
        <div className="environment-dialog">
          <div className="environment-dialog-header"><strong>新建工作区</strong></div>
          <p>弹窗保留更高一层的阴影，边框负责清楚地界定轮廓。</p>
          <input className="settings-input" placeholder="工作区名称" aria-label="工作区名称" />
        </div>
        <h2>悬浮状态 / 提示</h2>
        <div className="elevation-small-surfaces">
          <button className="jump-to-latest-pill">回到最新消息</button>
          <div className="tooltip-layer">打开侧边栏</div>
        </div>
        <h2>附着抽屉</h2>
        <div className="elevation-tray-stage">
          <div className="workspace-document-turn-drawer"><p>正在处理工作区中的文件</p></div>
        </div>
      </section>
    </div>
  </main>;
}

if (import.meta.env.DEV) createRoot(document.getElementById("root")!).render(<Fixture />);
