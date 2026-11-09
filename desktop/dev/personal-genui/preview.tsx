import * as React from "react";
import { createRoot } from "react-dom/client";
import { createPluginUIKit } from "../../src/shared/workbench";
import { PersonalGenUISurface, type SurfaceTransport } from "../../src/renderer/plugins/personal-genui/PersonalGenUISurface";
import { fixtures } from "../../../plugins/personal/src/genui/fixtures";
import { boundedJSON, exact, record, initialState, persistentState, validateState } from "../../../plugins/personal/src/genui/schema";
import { decodeSnapshot } from "../../src/renderer/plugins/personal-genui/snapshot";
import type { ActionResult, ActionRequest } from "../../../plugins/personal/src/genui/service";
import "../../src/renderer/styles/base.css";
import "../../src/renderer/styles/spacing.css";
import "../../src/renderer/styles/workbench.css";
import "../../src/renderer/styles/fields.css";
import "../../src/renderer/styles/focus.css";
import "../../src/renderer/styles/theme.css";
import "./preview.css";

const ui = createPluginUIKit(React);
const serverMode = new URLSearchParams(location.search).get("service") === "fixture" && location.hostname === "127.0.0.1";
const storageKey = "wuu-synthetic-genui-preview-v1";
const initial = (): Record<string, ActionResult> => Object.fromEntries(Object.entries(fixtures).map(([id, spec]) => [id, { surface: { schemaVersion: 1, surfaceId: id, messageId: `example-${id}`, spec, state: persistentState(spec, initialState(spec)), revision: 0, submitted: false }, acceptedRevision: 0 }]));
function decodeSurfaces(value: unknown, standalone: boolean): Record<string, ActionResult> {
  boundedJSON(value, 1024 * 1024); const entries = record(value); exact(entries, Object.keys(fixtures));
  return Object.fromEntries(Object.keys(fixtures).map(id => {
    const snapshot = decodeSnapshot(entries[id]);
    if (standalone && (snapshot.surface.surfaceId !== id || snapshot.surface.messageId !== `example-${id}` || JSON.stringify(snapshot.surface.spec) !== JSON.stringify(fixtures[id]))) throw new Error("stale fixture cache");
    return [id, snapshot];
  }));
}
let local = initial(), cacheNotice = "";
try {
  const saved = localStorage.getItem(storageKey);
  if (saved) {
    try { local = decodeSurfaces(JSON.parse(saved), true); }
    catch { localStorage.removeItem(storageKey); cacheNotice = "示例缓存损坏或已过期，已恢复初始示例。"; }
  }
} catch { cacheNotice = "浏览器未允许本地缓存，本次预览不会持久保存。"; }
async function api(path: string, body?: unknown) {
  const response = await fetch(`/fixture/${path}`, { method: body ? "POST" : "GET", headers: body ? { "Content-Type": "application/json" } : {}, ...(body ? { body: JSON.stringify(body) } : {}) });
  const result = await response.json(); if (!response.ok) throw new Error(result.error); return result;
}
function saveLocal() { try { localStorage.setItem(storageKey, JSON.stringify(local)); } catch { /* No persistence promise in a restricted HTML viewer. */ } }
function transport(id: string): SurfaceTransport {
  return serverMode ? {
    dispatch: request => api("action", request), read: () => api(`read?id=${encodeURIComponent(id)}`), retry: () => api("retry", { id }),
  } : {
    async read() { return structuredClone(local[id]); },
    async retry() { return structuredClone(local[id]); },
    async dispatch(request: ActionRequest) {
      const current = local[id];
      if (current.surface.revision !== request.expectedRevision) throw new Error("genui_revision_conflict");
      const next = structuredClone(current), action = next.surface.spec.actions.find(a => a.id === request.actionId);
      if (!action) throw new Error("unknown_action");
      next.surface.state = persistentState(next.surface.spec, validateState(next.surface.spec, request.state)); next.surface.revision++; next.acceptedRevision = next.surface.revision;
      if (action.kind === "submit") { next.surface.submitted = true; next.delivery = "accepted"; }
      local[id] = next; saveLocal(); return structuredClone(next);
    },
  };
}
function Preview() {
  const [surfaces, setSurfaces] = React.useState<Record<string, ActionResult>>(), [error, setError] = React.useState("");
  const [theme, setTheme] = React.useState("light"), [large, setLarge] = React.useState(false), [epoch, setEpoch] = React.useState(0);
  React.useEffect(() => { document.documentElement.dataset.theme = theme; }, [theme]);
  React.useEffect(() => { document.documentElement.dataset.large = String(large); }, [large]);
  React.useEffect(() => { (serverMode ? api("bootstrap") : Promise.resolve(local)).then(value => {
    value = decodeSurfaces(value, !serverMode);
    if (new URLSearchParams(location.search).has("invalid_fixture")) { value = structuredClone(value); Reflect.set(value.checklist.surface.spec, "schemaVersion", 99); }
    setSurfaces(value);
  }).catch(e => setError(String(e))); }, [epoch]);
  return <main className="genui-preview">
    <header><p className="preview-eyebrow">TWIN · 私有组件预览</p><h1>在回答里，继续动手</h1><p>以下均为示例数据。不会调用模型、创建真实任务或发送外部消息。</p>
      <p className="preview-mode">{serverMode ? "本地服务 fixture：真实校验与文件持久化；会话发送由测试替身记录。" : "独立 HTML fixture：本浏览器演示状态；“提交”仅模拟确认，不会送达 Twin。"}</p>
      <ui.Row><ui.Button onClick={() => setTheme(theme === "light" ? "dark" : "light")}>切换明暗</ui.Button><ui.Button onClick={() => setLarge(!large)}>切换大字体</ui.Button><ui.Button disabled={serverMode} onClick={() => { local = initial(); cacheNotice = ""; setError(""); saveLocal(); setSurfaces(local); setEpoch(value => value + 1); }}>重置示例</ui.Button></ui.Row>
    </header>
    {cacheNotice && <p role="status">{cacheNotice}</p>}
    {error && <p role="alert">{error}</p>}
    {surfaces && Object.entries(surfaces).map(([id, initial]) => <article className="preview-answer" key={`${id}-${epoch}`} data-example={id}><div className="preview-byline">Twin · 示例回答</div><PersonalGenUISurface initial={initial} transport={transport(id)} ui={ui} submittedNotice="示例选择已记录，没有发送给 Twin。" submissionHelp="测试替身：点击解释只模拟记录选择，不会发送给 Twin 或调用任何模型、工具。" /></article>)}
    <footer>拖动、勾选和编辑只改变本地草稿。保存才写入界面状态。临时展开状态在刷新后恢复默认。</footer>
  </main>;
}
document.documentElement.dataset.focusModality = "keyboard";
createRoot(document.getElementById("root")!).render(<Preview />);
