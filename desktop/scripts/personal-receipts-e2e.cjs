// Production Personal renderer with synthetic transport; no live model.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const output = path.resolve(process.env.WUU_RECEIPT_E2E_OUTPUT || path.join(__dirname, "../out/personal-receipts-e2e"));
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
const report = { scope: "Electron renderer with synthetic receipts; no live model", cases: [] };
let win;
const evaluate = fn => win.webContents.executeJavaScript(`(${fn})()`, true);
async function until(fn) {
  const end = Date.now() + 10000;
  while (Date.now() < end) {
    if (await evaluate(fn)) return;
    await new Promise(resolve => setTimeout(resolve, 30));
  }
  throw new Error(`Timed out: ${fn}`);
}
async function capture(name) {
  await until(() => !document.querySelector('.mosaic-send-overlay, [data-personal-send-source]'));
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
}
async function send() {
  await evaluate(() => {
    const root = document.querySelector('.mosaic-replies') || document.querySelector('.mosaic-main');
    const input = root.querySelector('textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, `合成消息 ${window.__receiptScenario.attempts.length + 1}：检查送达和已读提示。`);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await evaluate(() => (document.querySelector('.mosaic-replies') || document.querySelector('.mosaic-main')).querySelector('.composer-send-button').click());
  await until(() => !document.querySelector('.mosaic-send-overlay, [data-personal-send-source]') && window.__receiptCheck().status === 'delivered');
}
function checkOverlay(state) {
  assert.equal(state.status, "delivered");
  assert(state.width > 0 && state.left >= 0 && state.right <= state.viewport);
  assert(state.top >= state.bodyBottom && state.bottom <= state.scrollBottom, JSON.stringify(state));
  assert(Math.abs(state.rowHeight - state.bodyHeight) < 1, "receipt must not increase message height");
  assert(state.pageWidth <= state.viewport, "receipt must not cause horizontal overflow");
}

app.whenReady().then(async () => {
  win = new BrowserWindow({ width: 1100, height: 900, show: true, webPreferences: { contextIsolation: true } });
  for (const scope of ["main", "reply"]) for (const [appearance, width, height, theme, font] of [["light-wide", 1100, 900, "light", "15"], ["dark-narrow-large", 620, 900, "dark", "20"]]) {
    const name = `${scope}-${appearance}`;
    win.setSize(width, height);
    await win.loadFile(path.resolve(__dirname, "../out/personal-fixture/index.html"), { query: { receipts: "1", sendScenario: "processing", theme, font } });
    await until(() => document.querySelector('[data-wuu-plugin="personal"]'));
    await evaluate(() => document.querySelector('[data-wuu-plugin="personal"]').click());
    await until(() => document.querySelector('[data-receipt-id="receipt-held"]'));
    assert.equal(await evaluate(() => document.querySelector('[data-receipt-id="receipt-done"], [data-receipt-id="receipt-waiting"]') === null), true, "old normal receipts must stay hidden on load");
    assert.equal(await evaluate(() => !!document.querySelector('[data-receipt-id="receipt-held"] [data-action="retry-message"]') && !document.querySelector('[data-receipt-state="failed"]')), true);
    await capture(`${name}-failed`);
    const failureIcon = await evaluate(() => {
      const button = document.querySelector('[data-receipt-id="receipt-held"] button');
      const r = button.getBoundingClientRect(), body = button.closest('.plugin-conversation-message').querySelector('.bubble-message-body').getBoundingClientRect();
      return { left: r.left, right: r.right, center: (r.top + r.bottom) / 2, bodyLeft: body.left, bodyCenter: (body.top + body.bottom) / 2 };
    });
    assert(failureIcon.left >= 0 && failureIcon.right <= failureIcon.bodyLeft && Math.abs(failureIcon.center - failureIcon.bodyCenter) < 1, JSON.stringify(failureIcon));
    await evaluate(() => document.querySelector('[data-receipt-id="receipt-held"] button').click());
    await until(() => !document.querySelector('[data-receipt-id="receipt-held"] [data-action="retry-message"]'));
    await evaluate(() => window.__receiptScenario.admit('held'));
    await until(() => document.querySelectorAll('.mosaic-main .plugin-conversation-message').length === 6);
    if (scope === "reply") {
      await evaluate(() => document.querySelector('.mosaic-main [data-action="reply-message"]').click());
      await until(() => document.querySelector('.mosaic-replies textarea'));
    }
    await evaluate(() => {
      window.__receiptCheck = () => {
        const root = document.querySelector('.mosaic-replies') || document.querySelector('.mosaic-main');
        const request = window.__receiptScenario.attempts.at(-1)?.request_id;
        const receipt = root.querySelector(`[data-receipt-id="receipt-${request}"]`);
        const row = [...root.querySelectorAll('.plugin-conversation-flow .plugin-conversation-message[data-role="user"]')].at(-1);
        const body = row?.querySelector('.bubble-message-body').getBoundingClientRect();
        const scroll = root.querySelector('.plugin-conversation-scroll');
        const r = receipt?.getBoundingClientRect();
        return { status: r?.width ? receipt.dataset.deliveryStatus : null, text: receipt?.textContent,
          width: r?.width, left: r?.left, right: r?.right, top: r?.top, bottom: r?.bottom,
          bodyBottom: body?.bottom, rowHeight: row?.getBoundingClientRect().height, bodyHeight: body?.height,
          scrollHeight: scroll.scrollHeight, scrollBottom: scroll.getBoundingClientRect().bottom,
          viewport: innerWidth, pageWidth: document.documentElement.scrollWidth };
      };
    });
    await send();
    await evaluate(() => { const root = document.querySelector('.mosaic-replies') || document.querySelector('.mosaic-main'); const scroll = root.querySelector('.plugin-conversation-scroll'); scroll.scrollTop = scroll.scrollHeight; });
    await capture(`${name}-delivered`);
    const delivered = await evaluate(() => window.__receiptCheck());
    checkOverlay(delivered);
    await evaluate(() => window.__receiptScenario.markRead(window.__receiptScenario.attempts.at(-1).request_id));
    await until(() => window.__receiptCheck().status === 'read');
    const read = await evaluate(() => window.__receiptCheck());
    assert.equal(read.rowHeight, delivered.rowHeight);
    assert.equal(read.scrollHeight, delivered.scrollHeight);
    await capture(`${name}-read`);
    // Polling the same durable receipt must not restart the display window.
    await evaluate(() => window.__receiptScenario.refresh());
    await until(() => window.__receiptCheck().status === null);
    const expired = await evaluate(() => window.__receiptCheck());
    assert.equal(expired.rowHeight, read.rowHeight);
    assert.equal(expired.scrollHeight, read.scrollHeight);
    await capture(`${name}-expired`);
    await evaluate(() => window.__receiptScenario.refresh());
    assert.equal(await evaluate(() => window.__receiptCheck().status), null);
    await send();
    await send();
    const consecutive = await evaluate(() => {
      const root = document.querySelector('.mosaic-replies') || document.querySelector('.mosaic-main');
      return [...root.querySelectorAll('[data-transient]')].filter(node => node.getBoundingClientRect().width > 0).length;
    });
    assert.equal(consecutive, 1, "only the last consecutive message shows a floating receipt");
    await evaluate(() => window.__receiptScenario.reply(window.__receiptScenario.attempts.at(-1).request_id));
    await until(() => (document.querySelector('.mosaic-replies') || document.querySelector('.mosaic-main')).textContent.includes('合成回复：这条消息已处理。'));
    assert.equal(await evaluate(() => window.__receiptCheck().status), null, "reply hides normal receipt immediately");
    report.cases.push({ name, delivered, read, expired, consecutive, replyHidesReceipt: true, historicalReceiptsHidden: true });
  }
  for (const scope of ["main", "reply"]) for (const scenario of ["retry-same-id", "ack-unknown", "reject-before-accept"]) {
    const name = `${scope}-${scenario}`;
    await win.loadFile(path.resolve(__dirname, "../out/personal-fixture/index.html"), { query: { sendScenario: scenario, theme: "dark", font: "20" } });
    await until(() => document.querySelector('[data-wuu-plugin="personal"]'));
    await evaluate(() => document.querySelector('[data-wuu-plugin="personal"]').click());
    await until(() => document.querySelector('.mosaic-main textarea'));
    if (scope === "reply") {
      await evaluate(() => document.querySelector('.mosaic-main [data-action="reply-message"]').click());
      await until(() => document.querySelector('.mosaic-replies textarea'));
    }
    await evaluate(() => {
      window.__receiptRoot = () => document.querySelector('.mosaic-replies') || document.querySelector('.mosaic-main');
      const input = window.__receiptRoot().querySelector('textarea');
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Original message with an attachment');
      input.dispatchEvent(new Event('input', { bubbles: true }));
      const files = new DataTransfer(); files.items.add(new File(['synthetic attachment'], 'retry-note.txt', { type: 'text/plain' }));
      const picker = window.__receiptRoot().querySelector('input[type="file"]');
      picker.files = files.files; picker.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await until(() => window.__receiptRoot().textContent.includes('retry-note.txt') && !window.__receiptRoot().querySelector('.composer-send-button').disabled);
    await evaluate(() => window.__receiptRoot().querySelector('.composer-send-button').click());
    await until(() => window.__receiptRoot().querySelector('[data-action="retry-message"]'));
    await capture(`${name}-failure`);
    await evaluate(() => {
      const input = window.__receiptRoot().querySelector('textarea');
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Newer draft must survive retry');
      input.dispatchEvent(new Event('input', { bubbles: true }));
      if (window.__receiptScenario.mode === 'ack-unknown') window.__receiptScenario.hidden.clear();
      else { window.__receiptScenario.statusOffline = true; window.__receiptScenario.mode = 'processing'; }
    });
    await evaluate(() => { const button = window.__receiptRoot().querySelector('[data-action="retry-message"]'); button.click(); button.click(); });
    await until(() => !window.__receiptRoot().querySelector('[data-action="retry-message"]') && window.__receiptRoot().querySelector('[data-delivery-status="delivered"]'));
    const retry = await evaluate(() => ({ attempts: window.__receiptScenario.attempts, proofs: window.__receiptScenario.proofs, accepted: window.__receiptScenario.accepted, draft: window.__receiptRoot().querySelector('textarea').value }));
    assert.equal(retry.accepted, 1);
    assert.equal(retry.draft, 'Newer draft must survive retry');
    assert.equal(retry.attempts.length, scenario === 'ack-unknown' ? 1 : 2);
    if (scenario === 'retry-same-id') { assert.deepEqual(retry.attempts[0], retry.attempts[1]); assert.deepEqual(retry.proofs[0], retry.proofs[1]); }
    if (scenario === 'reject-before-accept') { assert.notEqual(retry.attempts[0].request_id, retry.attempts[1].request_id); assert.equal(retry.attempts[0].prompt, retry.attempts[1].prompt); assert.deepEqual(retry.proofs[0].attachments, retry.proofs[1].attachments); }
    await capture(`${name}-retried`);
    report.cases.push({ name, attempts: retry.attempts.length, accepted: retry.accepted, preservedDraft: true, exactAttachment: true });
  }
  report.status = "passed";
}).catch(async error => { report.status = "failed"; report.error = error.stack; console.error(error); if (win) { report.failure = await evaluate(() => ({ state: window.__receiptCheck?.(), attempts: window.__receiptScenario?.attempts, messages: [...document.querySelectorAll('.mosaic-main .plugin-conversation-message')].map(node => ({ text: node.textContent, receipt: node.querySelector('[data-receipt-id]')?.dataset, height: node.getBoundingClientRect().height })) })); await capture('failure'); } }).finally(() => {
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report));
  app.exit(report.status === "passed" ? 0 : 1);
});
