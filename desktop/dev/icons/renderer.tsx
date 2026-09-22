import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import * as Icons from "../../src/renderer/WuuIcons";
import { iconArtwork, type IconName } from "../../src/shared/iconArtwork";
import { SidePanelToggleIcon } from "../../src/renderer/SidePanelToggleIcon";
import { CapabilityMark } from "../../src/renderer/CapabilityMark";
import { EngineIcon } from "../../src/renderer/EngineIcons";
import { AccountConnectionOnboarding } from "../../src/renderer/AccountConnectionOnboarding";
import { ToolActivityMarker } from "../../src/renderer/ToolActivityMarker";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import "../../src/renderer/styles.css";
import "./style.css";

const names = Object.keys(iconArtwork) as IconName[];
const morandiAssets = Object.entries(import.meta.glob<string>(
  "../../src/renderer/assets/morandi/*.svg", { eager: true, query: "?url", import: "default" },
)).map(([path, url]) => ({ name: path.split("/").at(-1)!.replace(".svg", ""), url }));

function IconReview() {
  const params = new URLSearchParams(location.search);
  const [theme, setTheme] = useState(params.get("theme") ?? "light");
  const [font, setFont] = useState(Number(params.get("font")) || 14);
  const [size, setSize] = useState(Number(params.get("size")) || 16);
  const [open, setOpen] = useState(true);
  const [disabled, setDisabled] = useState(false);
  const [onboarding, setOnboarding] = useState(false);
  useEffect(() => { document.documentElement.dataset.theme = theme; }, [theme]);
  useEffect(() => { applyMessageFlowFontSize(font); }, [font]);
  return <main className="icon-review">
    <header className="icon-review-controls">
      <h1>Wuu 图标校准</h1>
      <label>主题<select value={theme} onChange={e => setTheme(e.target.value)}><option value="light">亮色</option><option value="dark">暗色</option></select></label>
      <label>界面字号<select value={font} onChange={e => setFont(Number(e.target.value))}><option value={14}>14 px</option><option value={20}>20 px</option></select></label>
      <label>图标尺寸<select value={size} onChange={e => setSize(Number(e.target.value))}>{[12, 14, 16, 20, 24, 32].map(n => <option key={n} value={n}>{n} px</option>)}</select></label>
      <label><input type="checkbox" checked={disabled} onChange={e => setDisabled(e.target.checked)} />禁用</label>
    </header>
    <section className="icon-review-context" aria-label="真实控件尺寸对照">
      <div className="titlebar icon-review-toolbar">
        <button className="icon-button" aria-label="切换侧栏" aria-pressed={open} disabled={disabled} onClick={() => setOpen(!open)}><SidePanelToggleIcon side="left" open={open} /></button>
        <button className="icon-button session-tab-new" aria-label="新对话" disabled={disabled}><Icons.SquarePen /></button>
        <button className="icon-button" aria-label="新建" disabled={disabled}><Icons.Plus /></button>
        <button className="icon-button" aria-label="设置" disabled={disabled}><Icons.Settings /></button>
        <button className="icon-button" aria-label="关闭" disabled={disabled}><Icons.X /></button>
        <button className="composer-action-button composer-send-button" aria-label="发送" disabled={disabled}><Icons.ArrowUp /></button>
      </div>
      <div className="icon-review-inline" aria-label="技能和工具">
        <CapabilityMark motif="inspect" /><CapabilityMark motif="module" /><CapabilityMark name="bot" />
        <ToolActivityMarker kind="command" /><ToolActivityMarker kind="edit" /><ToolActivityMarker kind="read" />
        <EngineIcon engine="wuu" /><EngineIcon engine="codex" />
      </div>
    </section>
    <section className="icon-review-colors" aria-label="莫兰迪彩色备用图标">
      <h2>莫兰迪 · 彩色备用</h2>
      <div className="icon-review-grid" style={{ "--review-icon-size": `${size}px` } as React.CSSProperties}>
        {morandiAssets.map(({ name, url }) => <figure key={name}>
          <div className="icon-review-glyph"><img src={url} alt="" width={size} height={size} /></div>
          <figcaption>{name}</figcaption>
        </figure>)}
      </div>
    </section>
    <h2>单色 · 完整图标库</h2>
    <div className="icon-review-grid" style={{ "--review-icon-size": `${size}px` } as React.CSSProperties}>
      {names.map(name => {
        const Icon = Icons[name];
        return <figure key={name} data-artwork={name}><div className="icon-review-glyph"><Icon size={size} /></div><figcaption>{name}</figcaption></figure>;
      })}
    </div>
    <section className="icon-review-onboarding">
      <button className="settings-button" onClick={() => setOnboarding(!onboarding)}>引导插图</button>
      {onboarding ? <AccountConnectionOnboarding onContinue={() => {}} error="" onError={() => {}} /> : null}
    </section>
  </main>;
}

if (import.meta.env.DEV) createRoot(document.getElementById("root")!).render(<WuuUIRoot><IconReview /></WuuUIRoot>);
