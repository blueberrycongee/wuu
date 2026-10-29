// Production Personal renderer + synthetic transport. DOM execution, not pixel evidence.
// Recovery contracts: stale revision, failed/pending writes, cancelled edits,
// late history responses, duplicate submission and keyboard focus restoration.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM, VirtualConsole } = require('jsdom');
const target = path.resolve(__dirname, '../out/personal-fixture/index.html');
const output = path.resolve(__dirname, '../out/routines-e2e');
fs.mkdirSync(output, { recursive: true });
const report = { scope: 'Production renderer with synthetic transport; DOM only', cases: [] };
let dom;
const wait = predicate => {
  if (predicate()) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const observer = new dom.window.MutationObserver(() => { if (predicate()) { clearTimeout(timer); observer.disconnect(); resolve(); } });
    const timer = setTimeout(() => { observer.disconnect(); reject(new Error(`Condition did not settle: ${predicate}`)); }, 5000);
    observer.observe(dom.window.document, { childList: true, subtree: true, attributes: true });
  });
};
const node = selector => dom.window.document.querySelector(selector);
const click = async selector => { await wait(() => node(selector)); node(selector).dispatchEvent(new dom.window.MouseEvent("pointerdown", { bubbles: true })); node(selector).click(); };
const set = async (selector, value) => {
  const element = node(selector), proto = element.tagName === 'TEXTAREA' ? dom.window.HTMLTextAreaElement.prototype : element.tagName === 'SELECT' ? dom.window.HTMLSelectElement.prototype : dom.window.HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value').set.call(element, value);
  element.dispatchEvent(new dom.window.Event(element.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }));
  await new Promise(resolve => dom.window.requestAnimationFrame(resolve));
};
const key = async (value, shiftKey = false) => { dom.window.document.activeElement.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: value, shiftKey, bubbles: true, cancelable: true })); await new Promise(resolve => dom.window.requestAnimationFrame(resolve)); };
async function open(query = '') {
  dom?.window.close();
  const errors = [], console = new VirtualConsole();
  console.on('jsdomError', error => { if (error.type !== 'css parsing' && !String(error).includes('HTMLCanvasElement.prototype.getContext')) errors.push(String(error)); });
  dom = new JSDOM(fs.readFileSync(target, 'utf8'), { url: `https://synthetic.invalid/${query}`, runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole: console, beforeParse(window) {
    window.matchMedia = media => ({ matches: false, media, addEventListener() {}, removeEventListener() {} });
    window.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
    window.structuredClone = structuredClone; window.TextEncoder = TextEncoder; window.TextDecoder = TextDecoder;
  }});
  await click('[data-wuu-plugin="personal"]'); await click('[data-action="toggle-personal-panel"]'); await click('[data-routine-id="weekly"]'); await wait(() => node('[data-routine-detail]')); assert.deepEqual(errors, []);
}
(async () => {
  await open(); assert(node('.mosaic-routine-instruction').textContent); assert(node('.mosaic-routine-schedule').textContent.includes('Asia/Shanghai'));
  node(".mosaic-routine-body").scrollTop = 120; await click('[data-action="edit-routine"]'); await set('[name="title"]', 'Unsaved'); assert.equal(node(".mosaic-routine-body").scrollTop, 0); await click('[data-action="cancel-routine-edit"]'); await wait(() => node('[data-action="edit-routine"]')); assert.notEqual(node('.mosaic-routine-head h2').textContent, 'Unsaved'); assert.equal(dom.window.__routineFixture.calls.filter(x => x.method === 'routines.update').length, 0); report.cases.push('Cancel discards edit without external write');
  await click('[data-action="edit-routine"]'); await set('[name="title"]', 'Updated'); await set('[name="prompt"]', 'Full updated instruction\nSecond paragraph'); await set('[name="schedule_type"]', 'daily'); await set('[name="time"]', '14:35'); const save = node('[data-action="save-routine"]'); save.click(); save.click(); await wait(() => node('[data-action="edit-routine"]')); assert.equal(node('.mosaic-routine-instruction').textContent, 'Full updated instruction\nSecond paragraph'); assert.equal(dom.window.__routineFixture.routines[0].cron, '35 14 * * *'); assert.equal(dom.window.__routineFixture.calls.filter(x => x.method === 'routines.update').length, 1); report.cases.push('Full instruction/schedule saved; duplicate writes suppressed');
  await click('[data-action="toggle-routine"]'); await wait(() => node('[data-action="toggle-routine"]').getAttribute('aria-checked') === 'false'); await click('[data-action="toggle-routine"]'); await wait(() => node('[data-action="toggle-routine"]').getAttribute('aria-checked') === 'true'); report.cases.push('Pause and resume use current revision');
  await click('[data-action="routine-history"]'); await wait(() => node('[data-run-state="completed"]')); assert(node('[data-run-state="dispatched"]')); assert(node('[data-run-state="failed"]')); report.cases.push('Run history separates actual completion, failure and dispatch');
  await key('Escape'); await wait(() => node('[data-action="edit-routine"]')); await key('Escape'); await wait(() => !node('[data-routine-detail]')); assert.equal(dom.window.document.activeElement.dataset.routineId, 'weekly'); report.cases.push('History back and dialog close restore focus');
  await open('?routineSave=conflict'); await click('[data-action="edit-routine"]'); await set('[name="prompt"]', 'Keep draft'); await click('[data-action="save-routine"]'); await wait(() => node('.mosaic-routine-error')); assert.equal(node('[name="prompt"]').value, 'Keep draft'); await click('[data-action="save-routine"]'); await wait(() => !node('[data-action="save-routine"]').disabled); assert.equal(node('[name="prompt"]').value, 'Keep draft'); assert.notEqual(dom.window.__routineFixture.routines[0].prompt, 'Keep draft'); await click('[data-action="reload-routine"]'); await wait(() => node('[data-action="edit-routine"]')); report.cases.push('Stale revision preserves draft, prevents overwrite and supports explicit reload');
  await open('?routineSave=error'); await click('[data-action="edit-routine"]'); await set('[name="title"]', 'Failure draft'); await click('[data-action="save-routine"]'); await wait(() => node('.mosaic-routine-error')); assert.equal(node('[name="title"]').value, 'Failure draft'); await click('[data-action="cancel-routine-edit"]'); await wait(() => node('[data-action="edit-routine"]')); report.cases.push('Save error preserves draft and cancel remains usable');
  await open('?routineSave=pending'); await click('[data-action="edit-routine"]'); await click('[data-action="save-routine"]'); await wait(() => node('[data-action="save-routine"]').disabled); await key('Escape'); await wait(() => !node('[data-routine-detail]')); report.cases.push('Pending save blocks duplicates but can be dismissed');
  await open('?routineHistory=error'); await click('[data-action="routine-history"]'); await wait(() => node('.mosaic-routine-error')); dom.window.__routineFixture.faults.history = 'ready'; await click('[data-action="refresh-routine-history"]'); await wait(() => node('[data-run-state="completed"]')); report.cases.push('History error can be retried');
  await open('?routineHistory=pending'); await click('[data-action="routine-history"]'); await key('Escape'); await wait(() => node('[data-action="edit-routine"]')); report.cases.push('Pending history can be dismissed');
  await open(); await click('[data-action="edit-routine"]'); assert(!node('[data-routine-advanced]').open); await click('[data-routine-advanced] > summary'); assert(node('[data-routine-advanced]').open); await set('[name="timezone"]', ''); await click('[data-routine-advanced] > summary'); assert(node("#mosaic-routine-editor").checkValidity()); await click('[data-action="save-routine"]'); await wait(() => node(".mosaic-routine-error")); assert.equal(dom.window.__routineFixture.calls.filter(x => x.method === "routines.update").length, 0); await click('[data-routine-advanced] > summary'); await set('[name="timezone"]', 'Invalid/Zone'); await click('[data-action="save-routine"]'); await wait(() => node('.mosaic-routine-error')); assert.equal(dom.window.__routineFixture.calls.filter(x => x.method === 'routines.update').length, 0); await set('[name="timezone"]', 'Asia/Shanghai'); await set('[name="schedule_type"]', 'once'); await set('[name="run_at"]', '2026-12-01T09:00'); await set('[name="utc_offset"]', 'bad'); await click('[data-action="save-routine"]'); assert.equal(dom.window.__routineFixture.calls.filter(x => x.method === 'routines.update').length, 0); await set('[name="utc_offset"]', '+08:00'); await click('[data-action="save-routine"]'); await wait(() => node('[data-action="edit-routine"]')); assert.equal(dom.window.__routineFixture.routines[0].run_at, '2026-12-01T01:00:00.000Z'); report.cases.push('Invalid zone/ambiguous instant rejected; explicit offset stored accurately');
  await click('[data-action="edit-routine"]'); node('[data-action="back-routine"]').focus(); await key('Tab', true); assert.equal(dom.window.document.activeElement.dataset.action, 'save-routine'); await key('Tab'); assert.equal(dom.window.document.activeElement.dataset.action, 'back-routine'); report.cases.push('Keyboard focus trap cycles in both directions');
  await open('?routineHistory=paged'); await click('[data-action="routine-history"]'); await wait(() => node('[data-action="more-routine-history"]')); const firstCount = dom.window.document.querySelectorAll('[data-run-state]').length; await click('[data-action="more-routine-history"]'); await wait(() => !node('[data-action="more-routine-history"]')); assert(dom.window.document.querySelectorAll('[data-run-state]').length > firstCount); report.cases.push('Manual history pagination appends records without silently truncating');
  await click('[data-routine-task-id]'); await wait(() => node('[data-task-detail]')); assert(!node('[data-routine-detail]')); assert.equal(node('.mosaic').dataset.panelOpen, 'true'); assert(node('[data-action="reply-task"]')); await click('[data-action="close-detail"]'); await wait(() => !node('[data-task-detail]')); await new Promise(resolve => dom.window.requestAnimationFrame(resolve)); assert.equal(dom.window.document.activeElement.dataset.routineId, 'weekly'); report.cases.push('Historical task opens existing task detail and returns to routine trigger');
  report.status = 'passed';
})().catch(error => { report.status = 'failed'; report.error = error.stack; process.exitCode = 1; }).finally(() => { dom?.window.close(); fs.writeFileSync(path.join(output, 'dom-report.json'), JSON.stringify(report, null, 2)); console.log(JSON.stringify(report, null, 2)); });
