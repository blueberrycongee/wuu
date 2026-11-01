// This document deliberately has no application imports, remote resources or page scripts.
export const recoveryPage = `<!doctype html><html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><title>Wuu Recovery</title><style>
:root {
  color-scheme: light dark;
  font: 15px/1.6 system-ui, sans-serif;
  background: light-dark(#f7f7f5, #1c1c1d);
  color: light-dark(#242424, #e5e5e5);
  --secondary: light-dark(#62625e, #b2b2ad);
  --line: light-dark(#deded9, #3d3d3a);
  --surface: light-dark(#fff, #252526);
  --accent: light-dark(#343b35, #d8dfd8);
  --on-accent: light-dark(#fff, #202620);
}
* { box-sizing: border-box; }
body { margin: 0; padding: 32px; }
main { max-width: 640px; margin: 0 auto; }
h1 { font-size: 1.47rem; line-height: 1.3; font-weight: 600; margin: 0 0 10px; }
h2 { font-size: 1rem; line-height: 1.5; font-weight: 600; margin: 0; }
p { margin: 0; }
header { margin-bottom: 24px; }
#summary { color: var(--secondary); }
#preserve { margin-top: 16px; padding-left: 14px; border-left: 2px solid var(--line); }
.supporting { font-size: .933rem; color: var(--secondary); }
section { padding: 22px 0; border-top: 1px solid var(--line); }
section > .supporting { margin-top: 8px; }
.actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-top: 14px; }
button { font: inherit; line-height: 1.4; padding: 8px 14px; min-height: 38px; border: 1px solid var(--line); border-radius: 8px; background: var(--surface); color: inherit; cursor: pointer; }
button:hover:not(:disabled) { background: light-dark(#eeeeeb, #343435); }
button.primary { background: var(--accent); border-color: var(--accent); color: var(--on-accent); }
button.primary:hover:not(:disabled) { filter: brightness(.92); background: var(--accent); }
button:disabled { opacity: .45; cursor: default; }
button:focus-visible, summary:focus-visible { outline: 2px solid light-dark(#47744d, #9bc3a0); outline-offset: 3px; }
#cancel { background: transparent; border-color: transparent; }
#status { margin-top: 8px; }
#status[data-tone="warning"] { color: light-dark(#8a5019, #e3b47e); }
#status[data-tone="warning"]::before { content: ""; display: inline-block; width: 6px; height: 6px; border-radius: 50%; background: currentColor; margin: 0 8px 2px 0; }
details { margin-top: 14px; border: 1px solid var(--line); border-radius: 8px; background: var(--surface); }
summary { display: flex; align-items: center; gap: 10px; padding: 10px 14px; cursor: pointer; list-style: none; font-size: .933rem; }
summary::-webkit-details-marker { display: none; }
summary::before { content: ""; width: 6px; height: 6px; border-right: 1.5px solid currentColor; border-bottom: 1.5px solid currentColor; transform: rotate(-45deg); flex-shrink: 0; }
details[open] summary::before { transform: rotate(45deg); }
pre { white-space: pre-wrap; overflow-wrap: anywhere; font: 13px/1.6 ui-monospace, monospace; margin: 0; padding: 12px 14px; border-top: 1px solid var(--line); }
#notice:not(:empty) { margin-top: 12px; }
@media (max-width: 480px) { body { padding: 24px 20px; } }
@media (forced-colors: active) { button:disabled { opacity: 1; color: GrayText; } }
</style></head><body><main>
<header><h1 id="title">Wuu Recovery</h1><p id="summary"></p><p id="preserve" class="supporting"></p></header>
<section aria-labelledby="retry-title"><h2 id="retry-title"></h2><p id="status" class="supporting" role="status" aria-live="polite"></p><div class="actions"><button id="retry"></button><button id="cancel"></button></div></section>
<section aria-labelledby="restart-title"><h2 id="restart-title"></h2><p id="restart-help" class="supporting"></p><div class="actions"><button id="safe"></button><button id="normal"></button></div></section>
<section aria-labelledby="diagnostics-title"><h2 id="diagnostics-title"></h2><p id="privacy" class="supporting"></p><details><summary id="preview-label"></summary><pre id="preview"></pre></details><div class="actions"><button id="export"></button></div><p id="notice" class="supporting" role="status" aria-live="polite"></p></section>
</main></body></html>`;
