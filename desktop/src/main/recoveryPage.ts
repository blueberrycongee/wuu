// This document deliberately has no application imports, remote resources or page scripts.
// The mark mirrors assets/app-icon-source.svg; keep it inline so the recovery
// document remains self-contained and can render without any external files.
export const recoveryPage = `<!doctype html><html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><title>Wuu Recovery</title><style>
:root {
  color-scheme: light dark;
  --canvas: light-dark(#fbfbfc, #1b1b1d);
  --surface: light-dark(#ffffff, #232326);
  --surface-muted: light-dark(#f4f4f5, #29292c);
  --ink: light-dark(#1a1a1b, #ededee);
  --ink-soft: light-dark(#646467, #a4a4a8);
  --ink-faint: light-dark(#8c8c90, #85858a);
  --line: light-dark(rgba(25, 25, 28, 0.09), rgba(255, 255, 255, 0.10));
  --line-strong: light-dark(rgba(25, 25, 28, 0.15), rgba(255, 255, 255, 0.16));
  --accent: light-dark(#1d1d1f, #f1f1f2);
  --accent-ink: light-dark(#ffffff, #1b1b1d);
  --accent-hover: light-dark(#37373a, #dededf);
  --warning: light-dark(#8a5b10, #d9a84e);
  --success: light-dark(#137a3d, #4cc38a);
  --danger: light-dark(#b42318, #f0705f);
  --focus: light-dark(#2f6bcc, #7fb0f5);
  --shadow-card: 0 1px 2px light-dark(rgba(20, 20, 24, 0.04), rgba(0, 0, 0, 0.2)), 0 14px 34px -26px light-dark(rgba(20, 20, 24, 0.22), rgba(0, 0, 0, 0.8));
  font-family: "vivo Sans SC", -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif;
  font-size: 15px;
  line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}
* { box-sizing: border-box; }
[hidden] { display: none !important; }
body {
  display: flex;
  min-height: 100vh;
  margin: 0;
  background: var(--canvas);
  color: var(--ink);
}
.shell {
  width: min(100% - 64px, 620px);
  margin: auto;
  padding: 48px 0 40px;
  animation: wuu-shell-in 360ms cubic-bezier(0.2, 0.7, 0.3, 1) both;
}
@keyframes wuu-shell-in {
  from { opacity: 0; transform: translateY(10px) scale(0.99); }
}
.hero {
  display: flex;
  align-items: center;
  gap: 18px;
  padding: 0 4px;
}
.hero:has(.lead:not([hidden])) { align-items: flex-start; }
.mark {
  position: relative;
  width: 56px;
  height: 56px;
  flex: none;
  border-radius: 15px;
  background: #ffffff;
  box-shadow: 0 0 0 1px light-dark(rgba(25, 25, 28, 0.10), rgba(255, 255, 255, 0.16)), 0 1px 3px light-dark(rgba(20, 20, 24, 0.08), rgba(0, 0, 0, 0.28));
}
.mark > svg { display: block; width: 100%; height: 100%; border-radius: inherit; }
.mark-badge {
  position: absolute;
  right: -6px;
  bottom: -6px;
  display: grid;
  width: 20px;
  height: 20px;
  place-items: center;
  border-radius: 50%;
  background: var(--danger);
  color: #ffffff;
  box-shadow: 0 0 0 2.5px var(--canvas);
}
body[data-mode="diagnostic"] .mark-badge { background: var(--warning); }
.mark-badge svg { display: block; width: 11px; height: 11px; }
.hero-copy { min-width: 0; padding-top: 2px; }
h1 {
  margin: 0;
  color: var(--ink);
  font-size: 26px;
  font-weight: 600;
  line-height: 1.25;
  text-wrap: balance;
  user-select: none;
}
.lead {
  margin: 8px 0 0;
  color: var(--ink-soft);
  font-size: 15px;
  line-height: 1.6;
}
.panel {
  overflow: hidden;
  margin-top: 32px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--surface);
  box-shadow: var(--shadow-card);
}
.row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 24px;
  padding: 18px 20px;
}
.row:not([hidden]) + .row { border-top: 1px solid var(--line); }
.row-copy { min-width: 0; }
.row-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 20px;
}
h2 {
  margin: 0;
  color: var(--ink);
  font-size: 15px;
  font-weight: 600;
  line-height: 1.35;
  user-select: none;
}
.status-dot {
  width: 6px;
  height: 6px;
  flex: none;
  border-radius: 50%;
  background: var(--ink-faint);
}
.status-dot[data-phase="loading"] { background: var(--warning); animation: wuu-pulse 1.6s ease-in-out infinite; }
.status-dot[data-phase="loaded"] { background: var(--success); }
.status-dot[data-phase="failed"],
.status-dot[data-phase="timed-out"] { background: var(--warning); }
.detail {
  margin: 5px 0 0;
  color: var(--ink-soft);
  font-size: 13px;
  line-height: 1.55;
  text-wrap: pretty;
}
.row-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.button {
  display: inline-flex;
  min-height: 36px;
  align-items: center;
  justify-content: center;
  gap: 7px;
  padding: 0 14px;
  border: 1px solid transparent;
  border-radius: 9px;
  appearance: none;
  font: inherit;
  font-size: 15px;
  font-weight: 500;
  line-height: 1;
  white-space: nowrap;
  cursor: pointer;
  user-select: none;
  transition: background-color 140ms ease, border-color 140ms ease, opacity 140ms ease, transform 100ms ease;
}
.button:active:not(:disabled) { transform: scale(0.98); }
.button:disabled { cursor: default; opacity: 0.42; }
.button.primary:disabled {
  border-color: var(--line);
  background: var(--surface-muted);
  color: var(--ink-faint);
  opacity: 1;
}
.button .icon { width: 15px; height: 15px; flex: none; }
.button.primary { background: var(--accent); color: var(--accent-ink); }
.button.primary:hover:not(:disabled) { background: var(--accent-hover); }
.button.secondary {
  border-color: var(--line-strong);
  background: light-dark(#ffffff, #2a2a2e);
  color: var(--ink);
  box-shadow: 0 1px 2px light-dark(rgba(20, 20, 24, 0.04), rgba(0, 0, 0, 0.16));
}
.button.secondary:hover:not(:disabled) { background: var(--surface-muted); }
.button.ghost { background: transparent; color: var(--ink-soft); }
.button.ghost:hover:not(:disabled) { background: var(--surface-muted); color: var(--ink); }
.button.link {
  min-height: 32px;
  padding: 0 7px;
  border-radius: 7px;
  background: transparent;
  color: var(--ink-soft);
  font-size: 13px;
}
.button.link:hover:not(:disabled) { background: var(--surface-muted); color: var(--ink); }
:is(.button, .link):focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
#retry[data-pending="true"] .icon { animation: wuu-spin 1s linear infinite; }
pre {
  max-height: 42vh;
  margin: 0;
  padding: 16px 20px;
  overflow: auto;
  border-top: 1px solid var(--line);
  background: var(--surface-muted);
  color: var(--ink);
  font: 12px/1.65 ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  user-select: text;
}
.notice {
  min-height: 20px;
  margin: 12px 4px 0;
  color: var(--ink-soft);
  font-size: 13px;
  line-height: 1.5;
}
.notice[data-tone="success"] { color: var(--success); }
.notice[data-tone="danger"] { color: var(--danger); }
@keyframes wuu-spin { to { transform: rotate(360deg); } }
@keyframes wuu-pulse { 50% { opacity: 0.35; } }
@media (max-width: 560px) {
  .shell { width: min(100% - 40px, 620px); padding-block: 36px; }
  .hero { gap: 14px; }
  .mark { width: 48px; height: 48px; border-radius: 13px; }
  .mark-badge { right: -5px; bottom: -5px; width: 18px; height: 18px; box-shadow: 0 0 0 2px var(--canvas); }
  h1 { font-size: 23px; }
  .row { grid-template-columns: minmax(0, 1fr); gap: 14px; padding: 16px; }
  .row-actions { justify-content: flex-start; }
  pre { padding: 14px 16px; }
}
@media (prefers-reduced-motion: reduce) {
  .shell,
  .status-dot[data-phase="loading"],
  #retry[data-pending="true"] .icon { animation: none; }
  .button { transition: none; }
}
@media (prefers-contrast: more) {
  :root { --ink-soft: var(--ink); }
  .panel, .button.secondary { border-color: var(--ink-faint); }
}
</style></head><body>
<main class="shell">
  <header class="hero">
    <div class="mark" aria-hidden="true"><svg viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg" focusable="false"><rect width="1024" height="1024" fill="#ffffff"/><g transform="translate(718 718) scale(11.538) translate(-51.22200724110007 -49.9707875601016)"><defs><radialGradient id="wuu-volume-b8f2fc-0.55" gradientUnits="userSpaceOnUse" cx="41.472" cy="32.421" r="58.5"><stop offset="0" stop-color="#454647"/><stop offset="0.52" stop-color="#353637"/><stop offset="1" stop-color="#242526"/></radialGradient></defs><path fill="url(#wuu-volume-b8f2fc-0.55)" d="M90.22 49.97C90.22 70.8 72.05 88.97 51.22 88.97C30.39 88.97 12.22 70.8 12.22 49.97C12.22 29.14 30.39 10.97 51.22 10.97C72.05 10.97 90.22 29.14 90.22 49.97Z"/></g><g fill="#ffffff" transform="translate(580.75 560.5) rotate(28)"><rect x="-135.5" y="-92" width="86" height="184" rx="43"/><rect x="49.5" y="-92" width="86" height="184" rx="43"/></g><g fill="#101112"><rect x="-46" y="-19" width="92" height="38" rx="19" transform="translate(246.882 446) rotate(209)"/><rect x="-64" y="-20.5" width="128" height="41" rx="20.5" transform="translate(333.919 291.435) rotate(229)"/><rect x="-51" y="-19" width="102" height="38" rx="19" transform="translate(493.888 214.636) rotate(249)"/></g></svg><span class="mark-badge"><svg viewBox="0 0 16 16" fill="none" focusable="false"><path d="M8 4.9v4.1" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><circle cx="8" cy="11.4" r="1.1" fill="currentColor"/></svg></span></div>
    <div class="hero-copy">
      <h1 id="title">Wuu Recovery</h1>
      <p class="lead" id="lead" hidden></p>
    </div>
  </header>

  <div class="panel">
    <section class="row" id="retry-row" aria-labelledby="retry-title">
      <div class="row-copy">
        <div class="row-title"><span class="status-dot" id="status-dot" data-phase="idle" aria-hidden="true"></span><h2 id="retry-title"></h2></div>
        <p class="detail" id="status" role="status" aria-live="polite"></p>
      </div>
      <div class="row-actions">
        <button type="button" class="button primary" id="retry"><svg class="icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M13.25 8a5.25 5.25 0 1 1-1.54-3.71M13.25 3.5v3.25H10"/></svg><span id="retry-label"></span></button>
        <button type="button" class="button ghost" id="cancel" hidden></button>
      </div>
    </section>

    <section class="row" id="restart-row" aria-labelledby="restart-title">
      <div class="row-copy">
        <h2 id="restart-title"></h2>
        <p class="detail" id="effects"></p>
      </div>
      <div class="row-actions">
        <button type="button" class="button secondary" id="safe"></button>
        <button type="button" class="button secondary" id="normal"></button>
      </div>
    </section>

    <section class="row" aria-labelledby="diagnostics-title">
      <div class="row-copy">
        <h2 id="diagnostics-title"></h2>
        <p class="detail" id="privacy"></p>
      </div>
      <div class="row-actions">
        <button type="button" class="button link" id="preview-toggle" aria-expanded="false" aria-controls="preview"></button>
        <button type="button" class="button secondary" id="export"></button>
      </div>
    </section>
    <pre id="preview" hidden></pre>
  </div>

  <p class="notice" id="notice" role="status" aria-live="polite"></p>
</main>
</body></html>`;
