import * as React from 'react';
import { createRoot } from 'react-dom/client';
import { flushSync } from 'react-dom';
import { createPreviewApplication } from './application';
import { preparePreviewGeneration } from './generation';
import type { ApplicationSnapshotV1 } from '../dist/index.js';

const initial = await (await fetch('/state')).json() as ApplicationSnapshotV1 | null;
const application = createPreviewApplication(initial ?? undefined);
const style = document.createElement('style');
style.textContent = `
:root {font-family:system-ui,sans-serif;color-scheme:light dark;font-size:15px;--wuu-color-canvas:#faf9f6;--wuu-color-surface:#fff;--wuu-color-text:#232a2c;--wuu-color-text-muted:#586267;--wuu-color-border-subtle:#d9dddb;--wuu-color-border-strong:#a2aea8;--wuu-color-surface-muted:#edf0ec;--wuu-color-accent:#276b61}
*{box-sizing:border-box}body{margin:0;background:var(--wuu-color-canvas);color:var(--wuu-color-text)}button,input,textarea,select{font:inherit;color:inherit}button{cursor:pointer;border:1px solid var(--wuu-color-border-subtle);border-radius:6px;padding:.45em .75em;background:var(--wuu-color-surface)}button:disabled{cursor:default;opacity:.6}button:focus-visible,input:focus-visible,textarea:focus-visible,select:focus-visible{outline:2px solid var(--wuu-color-accent);outline-offset:2px}input,textarea,select{background:var(--wuu-color-surface);border:1px solid var(--wuu-color-border-subtle);border-radius:5px;padding:.5em}textarea{resize:vertical}label{display:grid;gap:.35em}h1,h2,h3,p{margin:0}h1{font-size:1.4rem}h2{font-size:1.15rem}small{color:var(--wuu-color-text-muted)}pre{white-space:pre-wrap;overflow-wrap:anywhere}#preview{height:100dvh;display:flex;flex-direction:column;overflow:hidden}#preview-toolbar{flex-shrink:0;display:flex;align-items:center;gap:.7rem;flex-wrap:wrap;padding:.7rem 1rem;border-bottom:1px solid var(--wuu-color-border-subtle);background:var(--wuu-color-surface)}#preview-toolbar label{display:flex;align-items:center}#preview-diagnostics{margin:0;padding:1rem;background:#fff0ec;color:#822d21;white-space:pre-wrap}#preview-canvas{flex:1;min-height:0;overflow:auto}#preview-canvas>div{height:100%}[data-variant=primary]{background:var(--wuu-color-accent);color:#fff}button:hover{filter:brightness(.96)}
`;
document.head.append(style);
const root = createRoot(document.getElementById('preview')!);
let current: Awaited<ReturnType<typeof preparePreviewGeneration>> | undefined;
let selected = '';
let diagnostics = '';
let revision = '';
let requested = '';
let draw: () => void;
class RenderBoundary extends React.Component<{ children: React.ReactNode }, { error?: Error }> {
  state: { error?: Error } = {};
  static getDerivedStateFromError(error: Error) { return { error }; }
  render() { return this.state.error ? <pre role="alert">{this.state.error.stack}</pre> : this.props.children; }
}
function Preview() {
  const state = React.useSyncExternalStore(application.subscribe, application.getSnapshot);
  React.useEffect(() => {
    document.documentElement.style.fontSize = `${state.appearance.uiFontSize}px`;
    const dark = state.appearance.resolvedTheme === 'dark';
    const colors = dark ? ['#1c2224', '#252d30', '#e8eeeb', '#b2bfba', '#46514d', '#81c5b3'] : ['#faf9f6', '#ffffff', '#232a2c', '#586267', '#d9dddb', '#276b61'];
    for (const [index, token] of ['canvas','surface','text','text-muted','border-subtle','accent'].entries()) document.documentElement.style.setProperty(`--wuu-color-${token}`, colors[index]);
    document.documentElement.style.setProperty('--wuu-color-border-strong', dark ? '#71837a' : '#a2aea8');
    document.documentElement.style.setProperty('--wuu-color-surface-muted', dark ? '#303c36' : '#edf0ec');
    document.documentElement.style.colorScheme = dark ? 'dark' : 'light';
  }, [state.appearance]);
  const entry = current?.entries.find((item) => item.id === selected) ?? current?.entries[0];
  const Render = entry?.render;
  return <>
    <header id="preview-toolbar"><strong>Wuu preview</strong><small>Local fixture · no model or persisted effects</small>
      <label>Component <select aria-label="Preview component" value={entry?.id ?? ''} onChange={(event) => { selected = event.currentTarget.value; draw(); }}>
        {current?.entries.map((item) => <option key={item.id} value={item.id}>{item.title}</option>)}
      </select></label>
      <button onClick={() => void application.invoke('appearance.set', { theme: state.appearance.resolvedTheme === 'dark' ? 'light' : 'dark' })}>Toggle theme</button>
      <button onClick={() => void application.invoke('appearance.set', { uiFontSize: state.appearance.uiFontSize === 15 ? 20 : 15 })}>Text {state.appearance.uiFontSize}px</button>
    </header>
    {diagnostics && <pre id="preview-diagnostics" role="alert">{diagnostics}</pre>}
    <div id="preview-canvas"><RenderBoundary key={`${revision}/${entry?.id}`}>{Render && current && <Render application={application} slots={current.composition} />}</RenderBoundary></div>
  </>;
}
draw = () => { flushSync(() => root.render(<Preview />)); };
draw();
new EventSource('/events').onmessage = async ({ data }) => {
  const status = JSON.parse(data) as { revision: string | null; diagnostics: { file?: string; line?: number; column?: number; message: string; lineText?: string }[] };
  diagnostics = status.diagnostics.map((error) => `${error.file ?? 'Preview'}${error.line ? `:${error.line}:${error.column}` : ''}: ${error.message}${error.lineText ? `\n${error.lineText}` : ''}`).join('\n\n');
  if (!status.revision || status.revision === revision || status.diagnostics.length) { draw(); return; }
  requested = status.revision;
  let candidate: Awaited<ReturnType<typeof preparePreviewGeneration>> | undefined;
  try {
    const module = await import(`/module/${status.revision}.mjs`);
    candidate = await preparePreviewGeneration(module.default, application, status.revision, draw);
    if (requested !== status.revision) { candidate.dispose(); return; }
    // Unmount consumers before disposing their generation-owned dependencies.
    flushSync(() => root.render(null));
    current?.dispose();
    candidate.activate(); current = candidate; revision = status.revision;
    document.documentElement.dataset.previewRevision = revision;
  } catch (error) {
    candidate?.dispose();
    diagnostics = error instanceof Error ? error.stack ?? error.message : String(error);
  }
  draw();
};
