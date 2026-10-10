// Real Electron/main/preload/Go journey with a local synthetic provider.
// No installed profile, external account, user credentials, or replacement preload.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { createHash } = require('node:crypto');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app } = require('electron');
const desktop = path.resolve(__dirname, '..');
const buildDir = path.join(desktop, 'out');
const annotationAcceptance = process.env.WUU_ANNOTATION_ACCEPTANCE === '1';
const output = path.resolve(process.env.WUU_DOCUMENT_OUTPUT || path.join(desktop, 'out/e2e/document-context'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-document-context-'));
const home = path.join(fixture, 'wuu-home'), userHome = path.join(fixture, 'user-home'), project = path.join(fixture, 'project');
for (const dir of [output, home, userHome, project]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
process.env.HOME = userHome;
process.env.WUU_DESKTOP_CORE = process.env.WUU_DOCUMENT_CORE || process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core');
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '1';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;

function pdf(lines) {
  const objects = ['<< /Type /Catalog /Pages 2 0 R >>',
    `<< /Type /Pages /Count ${lines.length} /Kids [${lines.map((_, i) => `${4 + i * 2} 0 R`).join(' ')}] >>`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'];
  lines.forEach((line, i) => {
    const content = `BT /F1 16 Tf 28 200 Td (${line.replace(/[\\()]/g, '\\$&')}) Tj ET`;
    objects.push(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 420 260] /Resources << /Font << /F1 3 0 R >> >> /Contents ${5 + i * 2} 0 R >>`,
      `<< /Length ${Buffer.byteLength(content)} >>\nstream\n${content}\nendstream`);
  });
  let text = '%PDF-1.4\n', offsets = [0];
  objects.forEach((object, i) => { offsets.push(Buffer.byteLength(text)); text += `${i + 1} 0 obj\n${object}\nendobj\n`; });
  const xref = Buffer.byteLength(text);
  text += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n${offsets.slice(1).map(n => `${String(n).padStart(10, '0')} 00000 n \n`).join('')}trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(text);
}
const firstQuote = 'First PDF page: keep this original excerpt.';
const secondQuote = 'Second PDF page: review this paragraph.';
const originalPDF = pdf([firstQuote, secondQuote, 'Third PDF page: the source remains unchanged.']);
const originalSHA = createHash('sha256').update(originalPDF).digest('hex');
fs.writeFileSync(path.join(project, 'guide.pdf'), originalPDF);
fs.writeFileSync(path.join(project, 'large.txt'), 'Preview content remains readable.\n'.repeat(20000));
const requests = [], errors = [], screenshots = [], checks = [], resizeEvidence = [];
const server = http.createServer((req, res) => {
  let raw = '';
  req.on('data', chunk => raw += chunk);
  req.on('end', () => {
    if (req.method === 'GET') { res.writeHead(200, { 'Content-Type': 'application/json' }); return res.end(JSON.stringify({ data: [{ id: 'fixture', object: 'model' }] })); }
    const body = JSON.parse(raw);
    requests.push(body);
    const messages = body.messages || [];
    const lastUser = messages.findLastIndex(message => message.role === 'user'
      && !(typeof message.content === 'string' && message.content.trimStart().startsWith('<system-reminder>')));
    const text = JSON.stringify(messages[lastUser]?.content || '');
    if (!body.stream) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      return res.end(JSON.stringify({ id: 'title', choices: [{ index: 0, message: { role: 'assistant', content: 'PDF review' }, finish_reason: 'stop' }] }));
    }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const chunk = (delta, finish_reason = null) => res.write(`data: ${JSON.stringify({ id: 'fixture', model: 'fixture', choices: [{ index: 0, delta, finish_reason }] })}\n\n`);
    // Title generation can also stream and includes the original prompt. It
    // has no tools; only a conversation request may trigger fixture delivery.
    if (!body.tools?.length) {
      chunk({ role: 'assistant', content: 'PDF review' }); chunk({}, 'stop');
      return res.end('data: [DONE]\n\n');
    }
    if (text.includes('Deliver the PDF fixture') && !messages.slice(lastUser + 1).some(message => message.role === 'tool')) {
      const available = (body.tools || []).some(tool => tool.function?.name === 'present_artifact');
      if (!available) errors.push('The real provider request did not expose present_artifact.');
      chunk({ role: 'assistant', tool_calls: [{ index: 0, id: `delivery-${requests.length}`, type: 'function', function: {
        name: 'present_artifact', arguments: JSON.stringify({ path: 'guide.pdf' }),
      } }] });
      chunk({}, 'tool_calls');
    } else {
      chunk({ role: 'assistant', content: text.includes('Deliver the PDF fixture')
        ? 'The PDF is ready. Workspace files: [large text](large.txt), [working PDF](guide.pdf).'
        : 'The selected excerpt was received.' });
      chunk({}, 'stop');
    }
    res.end('data: [DONE]\n\n');
  });
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
let main, threadID, selectionDiagnostics, selectionInput, phase = 'startup';
app.on('browser-window-created', (_event, win) => { main ||= win; });
const evaluate = (fn, arg) => main.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`, true);
async function waitFor(fn, arg, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { if (await evaluate(fn, arg)) return; await delay(30); }
  throw new Error(`Timed out at ${phase}: ${fn}`);
}
async function click(selector) { await evaluate(selector => { const el = document.querySelector(selector); if (!el) throw new Error(`Missing ${selector}`); el.click(); }, selector); }
async function focusWindow() {
  main.focus(); main.webContents.focus();
  await waitFor(() => document.hasFocus());
}
async function nativeText(selector, text) {
  await focusWindow();
  await evaluate(selector => { const el = document.querySelector(selector); if (!el) throw new Error(`Missing ${selector}`); el.focus(); }, selector);
  await main.webContents.insertText(text);
}
async function capture(name) {
  await evaluate(() => Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime))
    .map(animation => animation.finished.catch(() => {}))));
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  fs.writeFileSync(path.join(output, `${name}.png`), (await main.webContents.capturePage()).toPNG());
  screenshots.push(`${name}.png`);
}
function pdfGeometry() {
  const root = document.querySelector('[data-workspace-pdf-preview]')?.shadowRoot;
  const container = root?.querySelector('.workspace-pdf-container');
  const page = root?.querySelector('.page[data-page-number="1"]');
  return { containerWidth: container?.clientWidth || 0, pageWidth: page?.getBoundingClientRect().width || 0,
    zoom: root?.querySelector('.workspace-pdf-zoom-value')?.textContent };
}
async function fittedPdf() {
  await evaluate(() => Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime))
    .map(animation => animation.finished.catch(() => {}))));
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await waitFor(() => {
    const root = document.querySelector('[data-workspace-pdf-preview]')?.shadowRoot;
    const width = root?.querySelector('.workspace-pdf-container')?.clientWidth || 0;
    const pageWidth = root?.querySelector('.page[data-page-number="1"]')?.getBoundingClientRect().width || 0;
    return width > 0 && pageWidth > 0 && Math.abs(pageWidth - width) <= 4;
  });
  return evaluate(pdfGeometry);
}
async function selectPages(first = 1, last = first) {
  await evaluate(() => Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime))
    .map(animation => animation.finished.catch(() => {}))));
  await waitFor(({ first, last }) => {
    const root = document.querySelector('[data-workspace-pdf-preview]')?.shadowRoot;
    return [first, last].every(page => {
      const span = root?.querySelector(`.page[data-page-number="${page}"] .textLayer span`);
      const bounds = span?.getBoundingClientRect();
      return span?.textContent && bounds.width > 0 && bounds.height > 0;
    });
  }, { first, last });
  return evaluate(({ first, last }) => {
    const root = document.querySelector('[data-workspace-pdf-preview]').shadowRoot;
    const from = root.querySelector(`.page[data-page-number="${first}"] .textLayer span`);
    const to = root.querySelector(`.page[data-page-number="${last}"] .textLayer span`);
    const range = document.createRange(); range.setStart(from.firstChild, 0); range.setEnd(to.firstChild, to.firstChild.textContent.length);
    const selection = root.getSelection(); selection.removeAllRanges(); selection.addRange(range);
    root.dispatchEvent(new Event('selectionchange'));
    return selection.toString();
  }, { first, last });
}
async function dragFirstPage() {
  await evaluate(() => Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime))
    .map(animation => animation.finished.catch(() => {}))));
  await waitFor(() => {
    const span = document.querySelector('[data-workspace-pdf-preview]')?.shadowRoot?.querySelector('.page[data-page-number="1"] .textLayer span');
    const bounds = span?.getBoundingClientRect();
    return span?.textContent && bounds.width > 0 && bounds.height > 0;
  });
  const bounds = await evaluate(() => {
    const span = document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.page[data-page-number="1"] .textLayer span');
    const r = span.getBoundingClientRect();
    window.__pdfDragEvents = [];
    window.__pdfDragAbort = new AbortController();
    for (const type of ['pointerdown', 'pointermove', 'pointerup']) document.addEventListener(type, event => {
      const target = event.composedPath()[0];
      if (window.__pdfDragEvents.length < 32) window.__pdfDragEvents.push({ type, buttons: event.buttons,
        trusted: event.isTrusted, x: event.clientX, y: event.clientY, target: `${target.nodeName}.${target.className || ''}` });
    }, { capture: true, signal: window.__pdfDragAbort.signal });
    // Start and end inside the glyph span, rather than on the surrounding PDF layer.
    return { start: Math.ceil(r.left), end: Math.floor(r.right), y: Math.round((r.top + r.bottom) / 2) };
  });
  await focusWindow();
  const ranges = [];
  main.webContents.sendInputEvent({ type: 'mouseMove', x: bounds.start, y: bounds.y });
  main.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, x: bounds.start, y: bounds.y });
  for (let step = 1; step <= 12; step++) {
    main.webContents.sendInputEvent({ type: 'mouseMove', button: 'left', modifiers: ['leftbuttondown'],
      x: Math.round(bounds.start + (bounds.end - bounds.start) * step / 12), y: bounds.y });
    ranges.push(await evaluate(() => new Promise(resolve => requestAnimationFrame(() => {
      const selection = document.querySelector('[data-workspace-pdf-preview]').shadowRoot.getSelection();
      resolve({ text: selection.toString(), collapsed: selection.isCollapsed, anchorOffset: selection.anchorOffset, focusOffset: selection.focusOffset });
    }))));
  }
  main.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, x: bounds.end, y: bounds.y });
  const result = await evaluate(() => {
    window.__pdfDragAbort.abort();
    const host = document.querySelector('[data-workspace-pdf-preview]'), root = host.shadowRoot;
    return { text: root.getSelection().toString(), events: window.__pdfDragEvents,
      selectability: { host: getComputedStyle(host).userSelect,
        text: getComputedStyle(root.querySelector('.textLayer span')).userSelect,
        toolbar: getComputedStyle(root.querySelector('.workspace-pdf-toolbar')).userSelect } };
  });
  selectionInput = { bounds, events: result.events, ranges, selectability: result.selectability };
  return result.text;
}
async function quoteSelection() {
  await waitFor(() => document.querySelector('.pdf-selection-action-menu'));
  await focusWindow();
  const point = await evaluate(() => { const r = document.querySelector('.pdf-selection-action-menu button').getBoundingClientRect(); return { x: Math.round((r.left + r.right) / 2), y: Math.round((r.top + r.bottom) / 2) }; });
  main.webContents.sendInputEvent({ type: 'mouseMove', ...point });
  main.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...point });
  main.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...point });
  await waitFor(() => document.querySelector('.composer-file-selection-card'));
}
async function snapshot() { return evaluate(async id => (await window.wuu.resumeThread(id)).thread, threadID); }
async function currentParts() {
  const thread = await snapshot();
  return thread.turns.flatMap(turn => turn.items).filter(item => item.type === 'user_message').flatMap(item => item.content_parts || []);
}
function writeEvidence(passed) {
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ passed, phase, checks, errors, screenshots,
    boundary: 'Production Electron main/preload/renderer and Go core with a local synthetic provider and disposable related-session history. The initial PDF selection uses native mouse drag and Quote clicks; later ranges are programmatically established in the rendered text layer. PNGs are review evidence, not user visual acceptance.',
    core: process.env.WUU_DESKTOP_CORE, buildDir, annotationAcceptance, sourceCommit: process.env.WUU_DOCUMENT_COMMIT || null,
    originalSHA, requests: requests.length, resizeEvidence, selectionInput, selectionDiagnostics }, null, 2));
  fs.writeFileSync(path.join(output, 'provider-requests.json'), JSON.stringify(requests, null, 2));
}
const timeout = setTimeout(() => { errors.push('Timed out'); writeEvidence(false); app.exit(1); }, annotationAcceptance ? 420000 : 180000);
module.exports = (async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', engines,
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'synthetic-not-a-credential', model: 'fixture' } } }));
  const stamp = '2026-01-01T00:00:00Z';
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'repo', name: 'document-fixture', path: project, created_at: stamp, updated_at: stamp }], active_context: { kind: 'project', project_id: 'repo', cwd: project } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en-US', theme: 'light' }));
  assert.equal(app.isReady(), false, 'The ESM entry point must finish production setup before Electron readiness.');
  await import(pathToFileURL(path.join(buildDir, 'main/index.js')).href);
  void run().catch(fail);
})().catch(fail);

async function run() {
  while (!main) await delay(20);
  main.setSize(1380, 1000); main.show(); main.focus(); main.webContents.setBackgroundThrottling(false);
  await waitFor(() => document.querySelector('.composer textarea'));
  await evaluate(async () => { await fetch('wuu-plugin://module/fixture-missing.js', { mode: 'no-cors' }); });
  checks.push('plugin scheme-support smoke resolves alongside document protocols');
  await nativeText('.composer textarea', 'Deliver the PDF fixture'); await click('.composer-send-button');
  phase = 'real artifact delivery';
  await waitFor(() => document.querySelector('[data-workspace-pdf-preview]')?.shadowRoot?.querySelector('.textLayer span'), undefined, 60000);
  threadID = await evaluate(async () => (await window.wuu.listThreads()).threads[0].id);
  const delivered = (await snapshot()).turns.flatMap(turn => turn.items).flatMap(item => item.result_detail?.content || []).find(part => part.mime_type === 'application/pdf');
  assert.ok(delivered?.uri?.startsWith('wuu-artifact://'), 'The real core must create a managed delivery.');
  assert.equal(delivered.artifact.sha256, originalSHA);
  if (await evaluate(() => document.querySelector('.workspace-panel-globalize')?.getAttribute('aria-pressed') === 'false')) await click('.workspace-panel-globalize');
  await waitFor(() => document.querySelector('.workspace-document-composer textarea'));
  checks.push('real tool delivery produces a verified PDF snapshot');

  phase = 'fit width follows the panel while manual zoom survives resize';
  const wideFit = await fittedPdf(); resizeEvidence.push({ state: 'wide-fit', ...wideFit });
  await click('.workspace-panel-globalize');
  await waitFor(width => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.workspace-pdf-container').clientWidth < width, wideFit.containerWidth);
  const dockedFit = await fittedPdf(); resizeEvidence.push({ state: 'docked-fit', ...dockedFit });
  assert.ok(dockedFit.pageWidth < wideFit.pageWidth);
  await evaluate(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('button[aria-label="Zoom in"]').click());
  await waitFor(width => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.page[data-page-number="1"]').getBoundingClientRect().width > width, dockedFit.pageWidth);
  const manualZoom = await evaluate(pdfGeometry); resizeEvidence.push({ state: 'manual-zoom', ...manualZoom });
  await click('.workspace-panel-globalize');
  await waitFor(width => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.workspace-pdf-container').clientWidth > width, dockedFit.containerWidth);
  await capture('00-manual-zoom-undocked');
  const undockedManual = await evaluate(pdfGeometry); resizeEvidence.push({ state: 'manual-undocked', ...undockedManual });
  assert.ok(Math.abs(undockedManual.pageWidth - manualZoom.pageWidth) <= 1);
  main.setSize(1120, 1000);
  await waitFor(width => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.workspace-pdf-container').clientWidth < width, undockedManual.containerWidth);
  await capture('00-manual-zoom-resized');
  const resizedManual = await evaluate(pdfGeometry); resizeEvidence.push({ state: 'manual-resized', ...resizedManual });
  assert.ok(Math.abs(resizedManual.pageWidth - manualZoom.pageWidth) <= 1);
  await evaluate(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.workspace-pdf-zoom-value').click());
  const resetFit = await fittedPdf(); resizeEvidence.push({ state: 'reset-fit', ...resetFit });
  main.setSize(1380, 1000);
  await waitFor(width => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.workspace-pdf-container').clientWidth > width, resetFit.containerWidth);
  const restoredFit = await fittedPdf(); resizeEvidence.push({ state: 'restored-fit', ...restoredFit });
  assert.ok(restoredFit.pageWidth > resetFit.pageWidth);
  await capture('00-fit-width-restored');
  checks.push('PDF fit tracks docked and resized viewport width, preserves manual zoom, and resumes after reset');

  if (annotationAcceptance) {
    phase = 'production annotation acceptance matrix';
    await require('./e2e/annotation-acceptance/journey.cjs')({
      main, output, evaluate, waitFor, focusWindow, nativeText, dragFirstPage, selectPages,
      capture, snapshot, fittedPdf, firstQuote, originalSHA, checks,
    });
  }

  phase = 'quote stays in draft';
  const beforeTurns = (await snapshot()).turns.length;
  const quote = await dragFirstPage(); assert.equal(quote, firstQuote);
  await quoteSelection();
  await waitFor(() => !document.querySelector('.pdf-selection-action-menu'));
  assert.equal((await snapshot()).turns.length, beforeTurns, 'Adding a quote must not send.');
  checks.push('native PDF text drag and Quote click retain an unsent draft');
  await capture('01-quote-draft');
  await click('.composer-file-selection-card .pdf-quote-tile-main');
  await waitFor(() => document.querySelector('.composer-file-selection-card-popover blockquote'));
  assert.equal(await evaluate(() => document.querySelector('.composer-file-selection-card-popover blockquote').textContent), quote);
  const sourceMeta = await evaluate(() => [...document.querySelectorAll('.pdf-quote-preview-source > span')]
    .map(span => span.textContent).join(' · '));
  assert.equal(sourceMeta, 'guide.pdf · p. 1');
  await click('.pdf-quote-source-action');
  await waitFor(() => !document.querySelector('.composer-file-selection-card-popover'));
  checks.push('quote card retains exact selected text and reopens the original snapshot');

  phase = 'quote reaches provider and history';
  await nativeText('.workspace-document-composer textarea', 'Review only the quoted passage.');
  await click('.workspace-document-composer .composer-send-button');
  await waitFor(async id => (await window.wuu.resumeThread(id)).thread.turns.some(turn => turn.status === 'completed' && turn.items.some(item => item.text?.includes('The selected excerpt was received.'))), threadID, 60000);
  const saved = (await currentParts()).find(part => part.source?.pdf);
  assert.equal(saved.source.quote, firstQuote); assert.equal(saved.source.pdf.start_page, 1); assert.equal(saved.source.pdf.end_page, 1);
  assert.equal(saved.source.pdf.artifact_uri, delivered.uri); assert.equal(saved.source.pdf.artifact_sha256, originalSHA);
  assert.equal(saved.source.revision, `sha256:${originalSHA}`); assert.equal(saved.source.start_line, 0);
  assert.ok(requests.some(request => JSON.stringify(request.messages).includes(originalSHA) && JSON.stringify(request.messages).includes(firstQuote)));
  checks.push('real turn persists quote/page/version metadata and forwards reference data to the provider');

  phase = 'comments, Escape, and responsive menu';
  for (const variant of [{ theme: 'light', size: 14, width: 1380 }, { theme: 'dark', size: 20, width: 820 }]) {
    main.setSize(variant.width, 1000);
    await evaluate(({ theme, size }) => { document.documentElement.dataset.theme = theme; document.documentElement.style.setProperty('--conversation-message-font-size', `${size}px`); document.documentElement.style.setProperty('--appearance-scale', String(size / 14)); }, variant);
    await selectPages(); await waitFor(() => document.querySelector('.pdf-selection-action-menu'));
    await evaluate(() => [...document.querySelectorAll('.pdf-selection-action-menu button')].find(button => button.textContent === 'Comment').click());
    await waitFor(() => document.activeElement?.matches('.pdf-selection-action-menu textarea'));
    await main.webContents.insertText('Keep the saved version intact.');
    await capture(`02-comment-${variant.theme}-${variant.size}`);
    const bounds = await evaluate(() => { const r = document.querySelector('.pdf-selection-action-menu').getBoundingClientRect(); return { left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: innerWidth, height: innerHeight }; });
    assert.ok(bounds.left >= 0 && bounds.top >= 0 && bounds.right <= bounds.width && bounds.bottom <= bounds.height, JSON.stringify(bounds));
    main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'ESC' }); main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'ESC' });
    await waitFor(() => !document.querySelector('.pdf-selection-action-menu textarea'));
    main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'ESC' }); main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'ESC' });
    await waitFor(() => !document.querySelector('.pdf-selection-action-menu'));
  }
  main.setSize(1380, 1000);
  await evaluate(() => { document.documentElement.dataset.theme = 'light'; document.documentElement.style.setProperty('--conversation-message-font-size', '14px'); document.documentElement.style.setProperty('--appearance-scale', '1'); });
  await evaluate(() => {
    const host = document.querySelector('[data-workspace-pdf-preview]');
    const span = host.shadowRoot.querySelector('.page[data-page-number="1"] .textLayer span');
    span.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, composed: true, button: 0 }));
    document.body.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, button: 0 }));
  });
  await selectPages(); await waitFor(() => document.querySelector('.pdf-selection-action-menu'));
  await click('.pdf-selection-action-menu button[aria-label="Close"]');
  checks.push('comment focus, Escape dismissal, outside drag release, and bounded light/dark large-font menus');

  phase = 'comment and multi-page range';
  // Render the second page through the real PDF viewer, retaining page one in its buffer.
  await evaluate(() => { const root = document.querySelector('[data-workspace-pdf-preview]').shadowRoot; const page = root.querySelector('.page[data-page-number="2"]'); page.scrollIntoView(); });
  await waitFor(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.page[data-page-number="2"] .textLayer span'));
  await evaluate(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.page[data-page-number="1"]').scrollIntoView());
  const multiQuote = await selectPages(1, 2);
  await waitFor(() => document.querySelector('.pdf-selection-action-menu'));
  await evaluate(() => [...document.querySelectorAll('.pdf-selection-action-menu button')].find(button => button.textContent === 'Comment').click());
  await waitFor(() => document.activeElement?.matches('.pdf-selection-action-menu textarea'));
  await main.webContents.insertText('Compare both selected pages.');
  const turnsBeforeComment = (await snapshot()).turns.length;
  await click('.pdf-selection-comment__submit');
  assert.equal((await snapshot()).turns.length, turnsBeforeComment, 'Adding a comment must not send.');
  await nativeText('.workspace-document-composer textarea', 'Use this comment.'); await click('.workspace-document-composer .composer-send-button');
  await waitFor(async id => (await window.wuu.resumeThread(id)).thread.turns.filter(turn => turn.status === 'completed').length >= 3, threadID, 60000);
  const multi = (await currentParts()).find(part => part.source?.pdf?.end_page === 2);
  assert.equal(multi.source.quote, multiQuote); assert.equal(multi.comment, 'Compare both selected pages.');
  assert.equal(multi.source.pdf.start_page, 1); assert.equal(multi.source.pdf.end_page, 2);
  checks.push('multi-page quote and authored comment survive the full application path');

  phase = 'second-page quote returns to its source after send';
  await evaluate(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.page[data-page-number="2"]').scrollIntoView());
  assert.equal(await selectPages(2), secondQuote);
  await quoteSelection();
  await click('.composer-file-selection-card .pdf-quote-tile-main');
  await waitFor(() => document.querySelector('.pdf-quote-source-action'));
  await click('.pdf-quote-source-action');
  await waitFor(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.workspace-pdf-page-count')?.textContent === '2 / 3');
  await nativeText('.workspace-document-composer textarea', 'Return to the second page.');
  await click('.workspace-document-composer .composer-send-button');
  await waitFor(async id => (await window.wuu.resumeThread(id)).thread.turns.filter(turn => turn.status === 'completed').length >= 4, threadID, 60000);
  const secondPagePart = (await currentParts()).filter(part => part.source?.pdf).at(-1);
  assert.equal(secondPagePart.source.quote, secondQuote);
  assert.equal(secondPagePart.source.pdf.start_page, 2);
  assert.equal(secondPagePart.source.pdf.end_page, 2);
  if (await evaluate(() => document.querySelector('.workspace-panel-globalize')?.getAttribute('aria-pressed') === 'true')) await click('.workspace-panel-globalize');
  await waitFor(() => [...document.querySelectorAll('.pdf-quote-pill')].some(button => !button.closest('[inert]')));
  await evaluate(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.page[data-page-number="1"]').scrollIntoView());
  await waitFor(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.workspace-pdf-page-count')?.textContent === '1 / 3');
  await evaluate(() => [...document.querySelectorAll('.pdf-quote-pill')].filter(button => !button.closest('[inert]')).at(-1).click());
  await waitFor(() => document.querySelector('.pdf-quote-entry blockquote')?.textContent === 'Second PDF page: review this paragraph.');
  await click('.pdf-quote-entry .pdf-quote-source-link');
  await waitFor(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.workspace-pdf-page-count')?.textContent === '2 / 3');
  await capture('03-second-page-sent-source-return');
  main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'ESC' }); main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'ESC' });
  await waitFor(() => !document.querySelector('.pdf-quote-entry'));
  if (await evaluate(() => document.querySelector('.workspace-panel-globalize')?.getAttribute('aria-pressed') === 'false')) await click('.workspace-panel-globalize');
  await evaluate(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.page[data-page-number="1"]').scrollIntoView());
  await waitFor(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.workspace-pdf-page-count')?.textContent === '1 / 3');
  checks.push('a second-page quote survives send and opens page two from both its draft and sent reference');

  phase = 'same filename cannot replace the snapshot';
  fs.writeFileSync(path.join(project, 'guide.pdf'), pdf(['Replacement PDF with the same filename.']));
  assert.equal(await evaluate(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.page[data-page-number="1"] .textLayer span').textContent), firstQuote);
  await selectPages(); await quoteSelection();
  await click('.composer-file-selection-card .pdf-quote-tile-main');
  await waitFor(() => document.querySelector('.pdf-quote-source-action'));
  await click('.pdf-quote-source-action');
  await waitFor(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot.querySelector('.page[data-page-number="1"] .textLayer span')?.textContent === 'First PDF page: keep this original excerpt.');
  await capture('03-immutable-snapshot');
  // Remove the unsent quote before closing the viewer.
  await click('.composer-file-selection-card .pdf-quote-tile-remove');
  await click('.artifact-preview-actions button[aria-label="Close"]');
  checks.push('editing a workspace PDF does not change a delivered selection or its navigation');

  phase = 'accurate truncated workspace preview';
  if (await evaluate(() => Boolean(document.querySelector('.workspace-conversation-tab button')))) await click('.workspace-conversation-tab button');
  await waitFor(() => [...document.querySelectorAll('.rich-file-link')].some(link => link.textContent === 'large text' && !link.closest('[inert]')));
  await evaluate(() => [...document.querySelectorAll('.rich-file-link')].find(link => link.textContent === 'large text' && !link.closest('[inert]')).click());
  await waitFor(() => document.querySelector('.workspace-file-preview-notice'));
  const file = await evaluate(() => window.wuu.readWorkspaceFile('large.txt'));
  assert.equal(file.truncated, true); assert.equal(file.binary, false); assert.ok(file.size_bytes > 512 * 1024);
  assert.ok(await evaluate(() => document.querySelector('.workspace-file-preview-notice').textContent.includes('512 KiB')));
  await capture('04-truncated-workspace-file');
  checks.push('real bounded file read visibly discloses truncation');

  phase = 'workspace replacement reloads the selected bytes';
  if (await evaluate(() => Boolean(document.querySelector('.workspace-conversation-tab button')))) await click('.workspace-conversation-tab button');
  await waitFor(() => [...document.querySelectorAll('.rich-file-link')].some(link => link.textContent === 'working PDF' && !link.closest('[inert]')));
  await evaluate(() => [...document.querySelectorAll('.rich-file-link')].find(link => link.textContent === 'working PDF' && !link.closest('[inert]')).click());
  const workingQuote = 'Replacement PDF with the same filename.';
  await waitFor(text => document.querySelector('[data-workspace-pdf-preview]')?.shadowRoot?.querySelector('.textLayer span')?.textContent === text, workingQuote);
  const beforeReplacement = await evaluate(() => window.wuu.readWorkspaceFile('guide.pdf'));
  const revision = file => `workspace-pdf:${file.size_bytes}:${file.mtime_ms}:${file.sha256}`;
  assert.equal(await selectPages(), workingQuote); await quoteSelection();
  // Keep a comment open while navigating away, so neither selection nor authored
  // draft can be attached to the next load's source metadata.
  await selectPages(); await waitFor(() => document.querySelector('.pdf-selection-action-menu'));
  await evaluate(() => [...document.querySelectorAll('.pdf-selection-action-menu button')].find(button => button.textContent === 'Comment').click());
  await waitFor(() => document.activeElement?.matches('.pdf-selection-action-menu textarea'));
  await main.webContents.insertText('Discard this selection comment on navigation.');
  const refreshedQuote = 'Updated PDF: a different revision.';
  fs.writeFileSync(path.join(project, 'guide.pdf'), pdf([refreshedQuote]));
  await evaluate(() => [...document.querySelectorAll('.workspace-tool-tab[data-wuu-tab-kind="file"] .workspace-tool-tab-main')].find(button => button.textContent === 'large.txt').click());
  await waitFor(() => !document.querySelector('.pdf-selection-action-menu'));
  await evaluate(() => [...document.querySelectorAll('.workspace-tool-tab[data-wuu-tab-kind="file"] .workspace-tool-tab-main')].find(button => button.textContent === 'guide.pdf').click());
  await waitFor(text => document.querySelector('[data-workspace-pdf-preview]')?.shadowRoot?.querySelector('.textLayer span')?.textContent === text, refreshedQuote);
  assert.equal(await evaluate(() => Boolean(document.querySelector('.pdf-selection-action-menu'))), false);
  const afterReplacement = await evaluate(() => window.wuu.readWorkspaceFile('guide.pdf'));
  assert.notEqual(revision(beforeReplacement), revision(afterReplacement));
  assert.equal(await selectPages(), refreshedQuote); await quoteSelection();
  await waitFor(() => document.querySelectorAll('.composer-file-selection-card').length === 2);
  await nativeText('.workspace-document-composer textarea', 'Compare these two observed workspace revisions.');
  await click('.workspace-document-composer .composer-send-button');
  await waitFor(async id => (await window.wuu.resumeThread(id)).thread.turns.filter(turn => turn.status === 'completed').length >= 5, threadID, 60000);
  const workspaceParts = (await currentParts()).filter(part => part.source?.pdf && !part.source.pdf.artifact_uri);
  assert.equal(workspaceParts.length, 2);
  assert.deepEqual(workspaceParts.map(part => [part.source.quote, part.source.revision, part.comment || '']), [
    [workingQuote, revision(beforeReplacement), ''], [refreshedQuote, revision(afterReplacement), ''],
  ]);
  checks.push('workspace refresh reloads PDF bytes before capture and preserves distinct quote revisions');

  phase = 'sent workspace quote reports a changed source';
  if (await evaluate(() => document.querySelector('.workspace-panel-globalize')?.getAttribute('aria-pressed') === 'true')) await click('.workspace-panel-globalize');
  if (await evaluate(() => Boolean(document.querySelector('.workspace-conversation-tab button')))) await click('.workspace-conversation-tab button');
  await waitFor(() => [...document.querySelectorAll('.pdf-quote-pill')].some(button => !button.closest('[inert]')));
  await evaluate(() => [...document.querySelectorAll('.pdf-quote-pill')].filter(button => !button.closest('[inert]')).at(-1).click());
  await waitFor(() => document.querySelector('.pdf-quote-entry .pdf-quote-source-link'));
  await click('.pdf-quote-entry .pdf-quote-source-link');
  await waitFor(() => document.querySelector('.pdf-quote-entry .pdf-quote-notice')?.textContent.includes('source location changed'));
  assert.equal(await evaluate(() => document.querySelector('.pdf-quote-entry blockquote').textContent), workingQuote);
  await capture('05-sent-quote-changed-source');
  checks.push('sent workspace quotes retain their excerpt and visibly disclose changed source locations');

  phase = 'persisted provenance after renderer reload';
  await main.loadFile(path.join(buildDir, 'renderer/index.html'));
  await waitFor(() => document.querySelector('.composer textarea'));
  const restored = (await currentParts()).filter(part => part.source?.pdf);
  assert.equal(restored.length, 5); assert.equal(restored[0].source.pdf.artifact_sha256, originalSHA);
  assert.equal(restored[1].source.pdf.end_page, 2);
  assert.equal(restored[2].source.quote, secondQuote); assert.equal(restored[2].source.pdf.start_page, 2);
  assert.deepEqual(errors, []);
  assert.equal(restored[4].source.revision, revision(afterReplacement));
  checks.push('renderer reload retains delivered and workspace PDF selections');

  phase = 'covered split keeps selection in the intended draft';
  // Seed never-loaded disposable threads, as the session-switch fixture does.
  // Existing core thread caches are untouched; the normal sidebar loads this
  // history through Go before the message action opens the split.
  const splitID = 'document-split-fixture';
  const relatedID = 'document-related-fixture';
  const seed = spawnSync('python3', ['-c', `
import sqlite3, sys
from pathlib import Path
home, main_id, related_id, cwd = sys.argv[1:]
db = sqlite3.connect(Path(home)/'sessions/sessions.sqlite3')
stamp = '2026-01-01T00:00:00Z'
for sid, title, entries in [(main_id,'Split PDF fixture',2),(related_id,'Related PDF fixture',0)]:
    db.execute('INSERT INTO sessions (id,created_at,updated_at,title,cwd,workspace_id,provider,model,entries) VALUES (?,?,?,?,?,?,?,?,?)',
        (sid,stamp,stamp,title,cwd,'repo','fixture','fixture',entries))
db.execute('INSERT INTO session_messages (session_id,seq,role,content,origin,presentation_kind,related_session_id,read_only,name,at) VALUES (?,?,?,?,?,?,?,?,?,?)',
    (main_id,1,'user','Related PDF context fixture','host','session_message',related_id,1,'Related PDF fixture',stamp))
db.execute('INSERT INTO session_messages (session_id,seq,role,content,at) VALUES (?,?,?,?,?)',
    (main_id,2,'assistant','Workspace file: [working PDF](guide.pdf).',stamp))
db.commit()
`, home, splitID, relatedID, project], { encoding: 'utf8', timeout: 10000 });
  assert.equal(seed.status, 0, seed.stderr);
  main.setSize(1380, 1000);
  await main.loadFile(path.join(buildDir, 'renderer/index.html'));
  await waitFor(() => document.querySelector('[data-section-id="repo"] .project-row[aria-expanded]'));
  if (await evaluate(() => document.querySelector('[data-section-id="repo"] .project-row').getAttribute('aria-expanded') === 'false')) {
    await click('[data-section-id="repo"] .project-row');
  }
  await waitFor(() => [...document.querySelectorAll('.thread-row-title')].some(title => title.textContent === 'Split PDF fixture'));
  await evaluate(() => [...document.querySelectorAll('.thread-row')].find(row => row.querySelector('.thread-row-title')?.textContent === 'Split PDF fixture').querySelector('.thread-row-main').click());
  await waitFor(id => document.querySelector(`.cached-conversation-pane[data-active="true"][data-thread-id="${id}"] .session-message-source:not(:disabled)`), splitID);
  threadID = splitID;
  if (await evaluate(() => document.querySelector('[data-wuu-component="right-sidebar-toggle"]')?.getAttribute('aria-expanded') === 'true')) await click('[data-wuu-component="right-sidebar-toggle"]');
  await click('.session-message-source:not(:disabled)');
  await waitFor(() => document.querySelectorAll('.conversation-split-pane').length === 2);
  assert.deepEqual(await evaluate(() => [...document.querySelectorAll('.conversation-split-pane')].map(pane => pane.dataset.threadId)), [threadID, relatedID]);
  await evaluate(() => document.querySelectorAll('.conversation-split-pane')[1].dispatchEvent(new PointerEvent('pointerdown', { bubbles: true })));
  await waitFor(id => document.querySelector('.conversation-split-pane.active')?.dataset.threadId === id, relatedID);
  await nativeText('.conversation-split-pane.active textarea', 'Other pane draft remains separate.');
  await evaluate(() => document.querySelector('.conversation-split-pane').dispatchEvent(new PointerEvent('pointerdown', { bubbles: true })));
  await waitFor(id => document.querySelector('.conversation-split-pane.active')?.dataset.threadId === id, threadID);
  await nativeText('.conversation-split-pane.active textarea', 'Keep this PDF quote in this conversation.');
  const splitTurnsBefore = (await snapshot()).turns.length;
  await evaluate(() => [...document.querySelectorAll('.conversation-split-pane.active .rich-file-link')].find(link => link.textContent === 'working PDF').click());
  if (await evaluate(() => document.querySelector('.workspace-panel-globalize')?.getAttribute('aria-pressed') === 'false')) await click('.workspace-panel-globalize');
  await waitFor(() => document.querySelector('.conversation-pane[inert]') && document.querySelector('[data-workspace-pdf-preview]')?.shadowRoot?.querySelector('.textLayer span'));
  // An unfinished selection comment belongs to its original conversation. A
  // normal pane switch must discard it without attaching or sending it anywhere.
  await click('.workspace-panel-globalize');
  await waitFor(() => !document.querySelector('.conversation-pane[inert]'));
  await selectPages(); await waitFor(() => document.querySelector('.pdf-selection-action-menu'));
  await click('.pdf-selection-menu__comment-toggle');
  await waitFor(() => document.activeElement?.matches('.pdf-selection-action-menu textarea'));
  await main.webContents.insertText('Never move this unfinished comment to another conversation.');
  await evaluate(() => document.querySelectorAll('.conversation-split-pane')[1].dispatchEvent(new PointerEvent('pointerdown', { bubbles: true })));
  await waitFor(id => document.querySelector('.conversation-split-pane.active')?.dataset.threadId === id
    && !document.querySelector('.pdf-selection-action-menu'), relatedID);
  assert.equal(await evaluate(() => document.querySelector('.conversation-split-pane.active textarea').value), 'Other pane draft remains separate.');
  assert.equal(await evaluate(() => document.querySelectorAll('.composer-file-selection-card').length), 0);
  await capture('06-stale-owner-comment-dismissed');
  await evaluate(() => document.querySelector('.conversation-split-pane').dispatchEvent(new PointerEvent('pointerdown', { bubbles: true })));
  await waitFor(id => document.querySelector('.conversation-split-pane.active')?.dataset.threadId === id, threadID);
  assert.equal(await evaluate(() => document.querySelector('.conversation-split-pane.active textarea').value), 'Keep this PDF quote in this conversation.');
  assert.equal((await snapshot()).turns.length, splitTurnsBefore);
  const relatedBefore = await evaluate(async id => (await window.wuu.resumeThread(id)).thread, relatedID);
  assert.equal(relatedBefore.turns.length, 0);
  await click('.workspace-panel-globalize');
  await waitFor(() => document.querySelector('.conversation-pane[inert]'));
  checks.push('switching conversation owner dismisses an unfinished PDF comment and preserves both separate drafts without sending');
  assert.equal(await selectPages(), refreshedQuote);
  await waitFor(() => document.querySelector('.pdf-selection-action-menu'));
  await click('.pdf-selection-action-menu button');
  await waitFor(() => !document.querySelector('.pdf-selection-action-menu'));
  assert.equal((await snapshot()).turns.length, splitTurnsBefore, 'Covered split selection must stay unsent.');
  await click('[data-wuu-component="right-sidebar-toggle"]');
  await waitFor(() => !document.querySelector('.conversation-pane[inert]'));
  await waitFor(() => document.querySelector('.conversation-split-pane.active .composer-file-selection-card'));
  assert.equal(await evaluate(() => Boolean(document.querySelector('.conversation-split-pane:not(.active) .composer-file-selection-card'))), false);
  await click('.conversation-split-close');
  await waitFor(() => !document.querySelector('.conversation-split-pane'));
  await waitFor(() => document.querySelector('[data-main-conversation-composer] .composer-file-selection-card'));
  assert.ok(await evaluate(() => document.querySelector('[data-main-conversation-composer] textarea').value.includes('Keep this PDF quote in this conversation.')));
  await capture('06-covered-split-draft-survives');
  await click('[data-main-conversation-composer] .composer-send-button');
  await waitFor(async ({ id, before }) => (await window.wuu.resumeThread(id)).thread.turns.filter(turn => turn.status === 'completed').length > before, { id: threadID, before: splitTurnsBefore }, 60000);
  const splitPart = (await currentParts()).filter(part => part.source?.pdf).at(-1);
  assert.equal(splitPart.source.quote, refreshedQuote); assert.equal(splitPart.source.revision, revision(afterReplacement));
  const otherThread = await evaluate(async id => (await window.wuu.resumeThread(id)).thread, relatedID);
  assert.equal(otherThread.turns.length, 0);
  checks.push('covered workspace PDF selection survives closing the panel and other split pane, then sends only to its intended conversation');
  phase = 'completed'; writeEvidence(true); clearTimeout(timeout); server.close(); app.quit();
}

async function fail(error) {
  errors.push(error.stack || String(error));
  if (main && !main.isDestroyed()) { try {
    selectionDiagnostics = await evaluate(() => {
      const host = document.querySelector('[data-workspace-pdf-preview]'), root = host?.shadowRoot;
      const span = root?.querySelector('.page[data-page-number="1"] .textLayer span');
      if (!span) return { hostPresent: Boolean(host) };
      const bounds = span.getBoundingClientRect();
      const describe = node => node ? `${node.nodeName}.${node.className || ''}` : null;
      const summarize = selection => ({ text: selection?.toString(), count: selection?.rangeCount, collapsed: selection?.isCollapsed,
        anchor: describe(selection?.anchorNode), focus: describe(selection?.focusNode),
        rangeText: selection?.rangeCount ? selection.getRangeAt(0).toString() : null });
      const ancestors = [];
      for (let node = span; node instanceof Element; node = node.parentElement || node.getRootNode().host) {
        const style = getComputedStyle(node);
        ancestors.push({ node: describe(node), display: style.display, visibility: style.visibility,
          userSelect: style.userSelect, pointerEvents: style.pointerEvents, inert: node.inert, hidden: node.hidden });
      }
      return { documentFocused: document.hasFocus(), bounds: bounds.toJSON(), ancestors, shadowSelection: summarize(root.getSelection()),
        windowSelection: summarize(window.getSelection()), textLayer: span.parentElement.outerHTML.slice(0, 3000),
        hit: describe(root.elementFromPoint((bounds.left + bounds.right) / 2, (bounds.top + bounds.bottom) / 2)) };
    });
    selectionDiagnostics.windowFocused = main.isFocused();
    selectionDiagnostics.webContentsFocused = main.webContents.isFocused();
  } catch {} }
  if (main && !main.isDestroyed()) { try { await capture('failure'); } catch {} }
  if (main && !main.isDestroyed() && phase === 'quote stays in draft') { try {
    const probe = await evaluate(async () => {
      const root = document.querySelector('[data-workspace-pdf-preview]')?.shadowRoot;
      const span = root?.querySelector('.page[data-page-number="1"] .textLayer span');
      if (!span) return null;
      const range = document.createRange(); range.selectNodeContents(span);
      const selection = root.getSelection(); selection.removeAllRanges(); selection.addRange(range);
      const beforeFrame = selection.toString();
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      const afterFrame = selection.toString(); selection.removeAllRanges();
      return { beforeFrame, afterFrame };
    });
    selectionDiagnostics = { ...selectionDiagnostics, programmaticProbe: probe };
  } catch {} }
  writeEvidence(false); clearTimeout(timeout); server.close(); console.error(error); app.exit(1);
}
