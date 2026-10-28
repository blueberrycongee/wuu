import { randomUUID } from "node:crypto";
import { BrowserWindow, desktopCapturer, nativeTheme, type DesktopCapturerSource } from "electron";
import { getMessageFlowFontSize, getThemePreference } from "./desktopSettings";
import { getMainLocale, mainTranslate } from "./i18n";

function html(value: string): string {
  return value.replace(/[&<>"']/g, character => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]!);
}

/** Previews belong to isolated native chrome, never to the requesting website. */
export async function chooseBrowserDisplaySource(
  parent: BrowserWindow,
  origin: string,
  current: () => boolean,
): Promise<DesktopCapturerSource | null> {
  // PipeWire opens its portal during enumeration. Its selected source still
  // needs our explicit confirmation and current ownership/navigation checks.
  const sources = await desktopCapturer.getSources({
    types: ["screen", "window"], thumbnailSize: { width: 320, height: 180 }, fetchWindowIcons: false,
  });
  if (!current() || !sources.length) return null;
  const nonce = randomUUID();
  const theme = getThemePreference();
  const dark = theme === "dark" || (theme === "system" && nativeTheme.shouldUseDarkColors);
  const items = (prefix: string) => sources.map((source, index) => {
    if (!source.id.startsWith(prefix)) return "";
    const preview = source.thumbnail.isEmpty() ? "" : source.thumbnail.toDataURL();
    return `<button class="source" type="button" role="radio" aria-checked="false" tabindex="-1" data-source="${index}" title="${html(source.name)}">
      <span class="preview">${preview ? `<img src="${preview}" alt="">` : `<span class="unavailable">${html(mainTranslate("displaySharePreviewUnavailable"))}</span>`}</span>
      <span class="source-name">${html(source.name)}</span></button>`;
  }).join("");
  const groups = [
    [mainTranslate("displayShareScreens"), items("screen:")],
    [mainTranslate("displayShareWindows"), items("window:")],
  ].filter(([, markup]) => markup).map(([name, markup]) => `<section><h2>${html(name)}</h2><div class="sources">${markup}</div></section>`).join("");
  const document = `<!doctype html><html lang="${getMainLocale()}" style="color-scheme:${dark ? "dark" : "light"}"><head>
    <meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'nonce-${nonce}'; base-uri 'none'; form-action 'none'">
    <title>${html(mainTranslate("displayShareTitle"))}</title><style>
      *{box-sizing:border-box}html,body{height:100%;margin:0}
      body{--paper:light-dark(#fff,#1c1c1d);--ink:light-dark(#1b1b1d,#e7e7e9);--muted:light-dark(#5f5f61,#b4b4b6);
        --surface:light-dark(#f1f1f4,#2b2b2d);--line:light-dark(#d9d9dc,#474749);--focus:light-dark(#145bd1,#8eb8ff);
        background:var(--paper);color:var(--ink);font:${getMessageFlowFontSize()}px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;
        display:flex;flex-direction:column}
      header{padding:22px 24px 16px}h1{font-size:1.2em;font-weight:600;margin:0 0 4px}p{margin:0}
      .origin{color:var(--muted);overflow-wrap:anywhere}.origin strong{color:var(--ink);font-weight:500}
      main{overflow:auto;flex:1;padding:0 24px 24px;scrollbar-color:var(--line) transparent}
      section+section{margin-top:24px}h2{font-size:1em;font-weight:500;margin:0 0 10px}
      .sources{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px}
      button{font:inherit;color:inherit;cursor:pointer}button:focus-visible{outline:2px solid var(--focus);outline-offset:3px}
      .source{padding:4px;border:2px solid transparent;background:transparent;border-radius:10px;text-align:left;min-width:0;scroll-margin-block:8px}
      .source:hover{background:var(--surface)}.source[aria-checked=true]{border-color:var(--focus);background:var(--surface)}
      .preview{display:flex;align-items:center;justify-content:center;aspect-ratio:16/10;overflow:hidden;background:var(--surface);border-radius:5px}
      .preview img{width:100%;height:100%;object-fit:contain}.unavailable{font-size:.9em;color:var(--muted);padding:10px;text-align:center}
      .source-name{display:block;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;padding:8px 4px 2px}
      footer{display:flex;align-items:center;gap:12px;border-top:1px solid var(--line);padding:16px 24px}
      .note{color:var(--muted);font-size:.9em;flex:1;max-width:42ch}
      .action{border:1px solid var(--line);background:var(--paper);border-radius:7px;padding:6px 18px;white-space:nowrap}
      .action:hover{background:var(--surface)}.primary{background:var(--ink);color:var(--paper);border-color:transparent}
      .primary:hover{background:var(--ink);opacity:.9}.primary:disabled{background:var(--surface);color:var(--muted);cursor:default;opacity:1}
      ::selection{background:var(--focus);color:var(--paper)}
      @media(max-width:620px){.sources{grid-template-columns:repeat(2,minmax(0,1fr))}footer{flex-wrap:wrap}.note{flex-basis:100%;max-width:none}}
    </style></head><body><header><h1>${html(mainTranslate("displayShareTitle"))}</h1>
    <p class="origin">${html(mainTranslate("displayShareWith"))} <strong>${html(origin)}</strong></p></header>
    <main role="radiogroup" aria-label="${html(mainTranslate("displayShareSource"))}">${groups}</main>
    <footer><p class="note">${html(mainTranslate("displayShareVideoOnly"))}</p>
    <button class="action" id="cancel">${html(mainTranslate("displayShareCancel"))}</button>
    <button class="action primary" id="share" disabled>${html(mainTranslate("displayShareConfirm"))}</button></footer>
    <script nonce="${nonce}">
      const choices=[...document.querySelectorAll('[data-source]')],share=document.querySelector('#share');let selected=null;
      const choose=button=>{selected=button.dataset.source;for(const item of choices){item.setAttribute('aria-checked',String(item===button));item.tabIndex=item===button?0:-1}share.disabled=false};
      if(choices[0])choices[0].tabIndex=0;
      for(const button of choices){button.onclick=()=>choose(button);button.onkeydown=event=>{
        if(!['ArrowRight','ArrowLeft','ArrowDown','ArrowUp','Home','End'].includes(event.key))return;
        event.preventDefault();let index=choices.indexOf(button);
        const columns=getComputedStyle(button.parentElement).gridTemplateColumns.split(' ').length;
        index=event.key==='Home'?0:event.key==='End'?choices.length-1:(index+({ArrowRight:1,ArrowLeft:-1,ArrowDown:columns,ArrowUp:-columns}[event.key])+choices.length)%choices.length;
        choices[index].focus();choose(choices[index]);};}
      document.querySelector('#cancel').onclick=()=>location.href='wuu-display-capture://cancel';
      share.onclick=()=>{if(selected!==null)location.href='wuu-display-capture://select?index='+selected};
    </script></body></html>`;
  return new Promise(resolve => {
    const picker = new BrowserWindow({
      parent, modal: true, show: false, width: 780, height: 620, minWidth: 480, minHeight: 420,
      title: mainTranslate("displayShareTitle"), backgroundColor: dark ? "#1c1c1d" : "#ffffff",
      autoHideMenuBar: true,
      webPreferences: { partition: "wuu-display-picker", sandbox: true, contextIsolation: true, nodeIntegration: false, webviewTag: false },
    });
    let settled = false;
    const finish = (source: DesktopCapturerSource | null) => {
      if (settled) return;
      settled = true;
      parent.removeListener("closed", cancel);
      if (!picker.isDestroyed()) picker.destroy();
      resolve(source && current() ? source : null);
    };
    const cancel = () => finish(null);
    parent.once("closed", cancel);
    picker.once("closed", cancel);
    picker.webContents.session.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
    picker.webContents.session.setPermissionCheckHandler(() => false);
    picker.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    picker.webContents.on("will-attach-webview", event => event.preventDefault());
    picker.webContents.on("will-navigate", (event, target) => {
      event.preventDefault();
      const url = new URL(target);
      if (url.protocol !== "wuu-display-capture:") return;
      if (url.hostname === "cancel") { cancel(); return; }
      const raw = url.searchParams.get("index");
      const index = raw === null ? -1 : Number(raw);
      if (url.hostname === "select" && Number.isInteger(index) && index >= 0 && index < sources.length) finish(sources[index]);
    });
    picker.webContents.on("will-redirect", event => event.preventDefault());
    picker.webContents.on("before-input-event", (event, input) => {
      if (input.type === "keyDown" && (input.key === "Escape" || ((input.meta || input.control) && input.key.toLowerCase() === "w"))) {
        event.preventDefault(); cancel();
      }
    });
    void picker.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(document)}`).then(() => {
      if (!picker.isDestroyed() && current()) { picker.show(); picker.focus(); }
      else cancel();
    }, cancel);
  });
}
