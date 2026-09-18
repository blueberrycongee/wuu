// From desktop/: vite --config dev/channel-stability/vite.config.ts
// Then: electron scripts/channel-stability-e2e.cjs
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/channel-stability");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-state"));
const origin = process.env.WUU_STABILITY_URL || "http://127.0.0.1:5207";
app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1100, height: 800, webPreferences: { backgroundThrottling: false } });
  const run = source => win.webContents.executeJavaScript(source);
  const wait = condition => run(`new Promise((resolve, reject) => {
    const deadline = performance.now() + 10000;
    function frame() { if (${condition}) resolve(); else if (performance.now() > deadline) reject(new Error('Timed out: ${condition}')); else requestAnimationFrame(frame); } frame();
  })`);
  const settle = () => run(`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))`);
  const type = text => run(`(() => {
    const input = document.querySelector('.channel-composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, ${JSON.stringify(text)});
    input.dispatchEvent(new Event('input', { bubbles: true }));
  })()`);
  const geometry = () => run(`(() => {
    const stream = document.querySelector('.agent-onboarding-scroll, .channel-message-stream');
    return { scrollTop: stream.scrollTop, scrollHeight: stream.scrollHeight, clientHeight: stream.clientHeight, overflow: stream.scrollWidth - stream.clientWidth,
      rows: [...stream.querySelectorAll('[data-message-id]')].map(n => { const r = n.getBoundingClientRect(); return { id: n.dataset.messageId, x:r.x, y:r.y, width:r.width, height:r.height }; }),
      times: [...stream.querySelectorAll('time')].map(n => n.dateTime) };
  })()`);
  const stableRows = (before, after, label) => {
    assert.equal(after.overflow, 0, `${label}: horizontal overflow`);
    assert.equal(before.rows.length, after.rows.length, label);
    for (let i = 0; i < before.rows.length; i++) for (const prop of ["x", "y", "width", "height"]) {
      assert.ok(Math.abs(before.rows[i][prop] - after.rows[i][prop]) <= 1, `${label}: row ${i} ${prop}: ${before.rows[i][prop]} -> ${after.rows[i][prop]}`);
    }
  };
  const results = [];
  for (const theme of ["light", "dark"]) for (const width of [1100, 390]) for (const font of [14, 20]) {
    win.setContentSize(width, 800);
    await win.loadURL(`${origin}/dev/channel-stability/index.html?theme=${theme}&font=${font}`);
    await wait(`document.querySelector('[data-action="confirm-model"]')`);
    await run(`document.querySelector('[data-action="confirm-model"]').click()`);
    await wait(`document.querySelector('.channel-composer textarea')`);
    await type("阿铁打");
    await settle();
    await run(`document.querySelector('.composer-send-button').click()`);
    await wait(`window.stability.canOpen`);
    const onboarding = await geometry();
    await run(`window.stability.open()`);
    await wait(`document.querySelector('.channel-message-stream [data-message-id="answer"]')`);
    await settle();
    const opened = await geometry();
    stableRows(onboarding, opened, `${theme}/${width}/${font} onboarding`);
    await type("首条正文：确认时不应补时间条，也不应替换消息节点。");
    await settle();
    await run(`document.querySelector('.composer-send-button').click()`);
    await wait(`window.stability.canAcknowledge && document.querySelector('.channel-message-pending')`);
    await run(`Promise.all([...document.querySelector('.channel-message-pending').getAnimations()].map(a => a.finished.catch(() => {})))`);
    await settle();
    await run(`window.pendingNode = document.querySelector('.channel-message-pending'); window.pollBaseline = window.stability.polls;`);
    const pending = await geometry();
    assert.equal(pending.times.length, 1, "Timestamp is present before acknowledgement");
    await wait(`window.stability.polls > window.pollBaseline`);
    assert.equal(await run(`document.querySelectorAll('.channel-message-stream .channel-message.own').length`), 2, "A poll must not duplicate the pending message");
    await run(`window.stability.acknowledge()`);
    await wait(`!document.querySelector('.channel-message-pending')`);
    await settle();
    const acknowledged = await geometry();
    stableRows(pending, acknowledged, `${theme}/${width}/${font} acknowledgement`);
    assert.deepEqual(pending.times, acknowledged.times);
    assert.equal(await run(`window.pendingNode === document.querySelector('[data-message-id^="sent-"]')`), true);
    fs.writeFileSync(path.join(output, `${theme}-${width}-${font}.png`), (await win.webContents.capturePage()).toPNG());
    // Follow new content at bottom, but preserve the user's reading position.
    await run(`window.pollBaseline = window.stability.polls; window.stability.append(35)`);
    await wait(`window.stability.polls > window.pollBaseline && document.querySelectorAll('[data-message-id^="history-"]').length === 35`);
    await settle();
    const bottom = await geometry();
    assert.ok(Math.abs(bottom.scrollHeight - bottom.clientHeight - bottom.scrollTop) <= 2, "Follow latest after append");
    await run(`(() => { const s = document.querySelector('.channel-message-stream'); s.dispatchEvent(new WheelEvent('wheel', { deltaY: -200, bubbles: true })); s.scrollTop = 100; s.dispatchEvent(new Event('scroll')); })()`);
    await settle();
    const reading = await geometry();
    await run(`window.stability.append(1)`);
    await wait(`document.querySelectorAll('[data-message-id^="history-"]').length === 36`);
    await settle();
    const updated = await geometry();
    assert.equal(updated.scrollTop, reading.scrollTop, "Do not drag readers to latest");
    results.push({ theme, width, font, onboarding, opened, pending, acknowledged, readingScrollTop: reading.scrollTop, updatedScrollTop: updated.scrollTop });
  }
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(results, null, 2));
  console.log("PASS: 8 light/dark × wide/narrow × 14/20px cases; stable onboarding and acknowledgement geometry, node identity, timestamp, poll race and reading anchor.");
  win.destroy();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
