// Production Twin renderer, synthetic transport and isolated Electron profile.
// Regressions: full-window reading columns, replayed sends, clone/row jumps,
// stolen reading position, and inaccessible messages after reduced-motion sends.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { app, BrowserWindow } = require('electron');
const output = path.resolve(process.env.WUU_MESSAGE_FLOW_OUTPUT || path.join(__dirname, '../out/personal-message-flow-e2e'));
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', fs.mkdtempSync(path.join(output, 'profile-')));
const report = { scope: 'Production Electron renderer with offscreen paint capture; synthetic conversation transport, no live model or physical-display claim', cases: [] };
let win;
let recordingActive = false;
let recordingTask;
const evaluate = fn => win.webContents.executeJavaScript(`(${fn})()`, true);
async function until(fn) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (await evaluate(fn)) return;
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  throw new Error(`Timed out: ${fn}`);
}
async function capture(name) {
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
}
async function load(query = {}) {
  await win.loadFile(path.resolve(__dirname, '../out/personal-fixture/index.html'), { query: { sendScenario: 'processing', messageFlow: '1', ...query } });
  await until(() => document.querySelector('[data-wuu-plugin="personal"]'));
  await evaluate(() => document.querySelector('[data-wuu-plugin="personal"]').click());
  await until(() => document.querySelector('.mosaic-main textarea'));
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}
async function submit(text) {
  await win.webContents.executeJavaScript(`(() => {
    const input = document.querySelector('.mosaic-main textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, ${JSON.stringify(text)});
    input.dispatchEvent(new Event('input', { bubbles: true }));
  })()`, true);
  await evaluate(() => document.querySelector('.mosaic-main .composer-send-button').click());
}
async function send(text) {
  await submit(text);
  await until(() => !document.querySelector('.mosaic-main textarea').value);
  await until(() => !document.querySelector('.mosaic-send-overlay, [data-personal-send-source]'));
}
app.whenReady().then(async () => {
  // Parallel hosted acceptance windows can occlude this synthetic renderer.
  // Offscreen paint keeps screenshots and the animation clock independent of them.
  win = new BrowserWindow({ width: 1128, height: 900, show: false, webPreferences: { contextIsolation: true, backgroundThrottling: false, offscreen: true } });
  for (const [name, width, theme, font] of [['wide-light',1128,'light','15'],['narrow-dark-large',620,'dark','19']]) {
    win.setSize(width, 900);
    await load({theme,font});
    await send('好');
    await send('这是一条足够长的合成消息，用于检查自然换行以及阅读列的上限。'.repeat(8));
    const layout = await evaluate(() => {
      const rows = [...document.querySelectorAll('.mosaic-main .plugin-conversation-flow .plugin-conversation-message')];
      const rect = node => node.getBoundingClientRect().toJSON();
      return { viewport: innerWidth, page: document.documentElement.scrollWidth,
        flow: rect(document.querySelector('.mosaic-main .plugin-conversation-flow')),
        short: rect(rows.at(-2).querySelector('.bubble-message-body')), long: rect(rows.at(-1).querySelector('.bubble-message-body')) };
    });
    assert(layout.page <= layout.viewport, JSON.stringify(layout));
    assert(layout.short.width < layout.long.width / 2, 'Short messages must shrink to their content');
    assert(layout.long.left >= layout.flow.left && layout.long.right <= layout.flow.right, JSON.stringify(layout));
    assert(layout.flow.width <= 820, 'Wide Twin reading column must not fill the window');
    await capture(name);
    report.cases.push({name,layout});
  }
  win.setSize(1128, 900);
  await load({ sendScenario: 'sending' });
  const recording = [];
  recordingActive = true;
  const recordStarted = Date.now();
  recordingTask = (async () => {
    while(recordingActive) {
      const file = `send-frame-${String(recording.length).padStart(3,'0')}.png`;
      await capture(file.slice(0,-4));
      recording.push({file,elapsed:Date.now()-recordStarted});
      await new Promise(resolve=>setTimeout(resolve,30));
    }
  })();
  await evaluate(() => {
    window.__flowFrames = [];
    window.__flowFrameStart = performance.now();
    const sample = () => {
      const overlay = document.querySelector('.mosaic-send-overlay');
      const source = document.querySelector('[data-personal-send-source]');
      const rect = node => node?.getBoundingClientRect().toJSON();
      window.__flowFrames.push({ time: performance.now() - window.__flowFrameStart, overlay: rect(overlay), source: rect(source), hidden: !!source });
      if (performance.now() - window.__flowFrameStart < 1100) requestAnimationFrame(sample);
    };
    requestAnimationFrame(sample);
    const input = document.querySelector('.mosaic-main textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Send motion continuity');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await evaluate(() => document.querySelector('.mosaic-main .composer-send-button').click());
  await until(() => document.querySelector('.mosaic-send-overlay'));
  const typing = await evaluate(() => {
    const input = document.querySelector('.mosaic-main textarea');
    input.dispatchEvent(new KeyboardEvent('keydown', {key:'a', bubbles:true}));
    return !!document.querySelector('.mosaic-send-overlay');
  });
  assert(typing, 'Typing the next draft must not cancel a message in flight');
  await evaluate(() => window.__receiptScenario.release());
  await until(() => !document.querySelector('.mosaic-send-overlay, [data-personal-send-source]'));
  await until(() => window.__flowFrames.at(-1)?.time >= 1100);
  recordingActive = false; await recordingTask;
  fs.writeFileSync(path.join(output,'send-recording.json'), JSON.stringify(recording,null,2));
  report.cases.push({name:'send-continuity', frames:await evaluate(() => window.__flowFrames)});
  const flights = report.cases.at(-1).frames.filter(frame => frame.overlay);
  assert(flights.length >= 5, 'Capture actual animation frames');
  const landing = flights.at(-1);
  assert(Math.abs(landing.overlay.top - landing.source.top) < 4, 'Send must land on its real row');
  await capture('send-landed');
  await load();
  await submit('First consecutive message');
  await until(() => document.querySelector('.mosaic-send-overlay'));
  await evaluate(() => {
    window.__firstFlight = document.querySelector('.mosaic-send-overlay');
    for (const animation of window.__firstFlight.getAnimations({subtree:true})) animation.pause();
  });
  await submit('Second consecutive message');
  await until(() => document.querySelectorAll('.mosaic-send-overlay').length === 2);
  assert.equal(await evaluate(() => window.__firstFlight.isConnected), true, 'A consecutive send must preserve the first flight');
  await evaluate(() => { for(const node of document.querySelectorAll('.mosaic-send-overlay')) for(const animation of node.getAnimations({subtree:true})) animation.play(); });
  await until(() => !document.querySelector('.mosaic-send-overlay, [data-personal-send-source]'));
  report.cases.push({name:'consecutive-sends',passed:true});
  // Rich messages keep their own overflow controls inside the reading column.
  await evaluate(() => {
    const flow = window.__messageFlowFixture;
    flow.append('assistant', 'A short reply.');
    flow.append('assistant', '> A quoted paragraph that remains inside the reading column.\n\n- First item\n- A longer second item with detail.\n\n```javascript\nconst longValue = "' + 'abcdef'.repeat(40) + '";\n```\n\nhttps://example.test/' + 'long-path-segment/'.repeat(30));
  });
  await until(() => document.querySelector('.mosaic-main pre'));
  const rich = await evaluate(() => {
    const scroll = document.querySelector('.mosaic-main .plugin-conversation-scroll');
    return {client:scroll.clientWidth, content:scroll.scrollWidth, pre:!!scroll.querySelector('pre'), list:!!scroll.querySelector('li'), quote:!!scroll.querySelector('blockquote')};
  });
  assert(rich.content <= rich.client + 1 && rich.pre && rich.list && rich.quote, JSON.stringify(rich));
  report.cases.push({name:'rich-text',rich});
  await capture('rich-text');
  // The live stream grows while following; a reader who scrolls up stays put.
  await evaluate(() => {
    for(let index=0; index<12; index++) window.__messageFlowFixture.append('assistant', 'Earlier message ' + index + '. ' + 'Reading history. '.repeat(12));
  });
  await until(() => document.querySelector('.mosaic-main').textContent.includes('Earlier message 11'));
  await evaluate(() => {const scroll=document.querySelector('.mosaic-main .plugin-conversation-scroll');scroll.scrollTop=scroll.scrollHeight;scroll.dispatchEvent(new Event('scroll'));window.__messageFlowFixture.start();window.__messageFlowFixture.delta('STREAM CHECK ');});
  await until(() => document.querySelector('.mosaic-main').textContent.includes('STREAM CHECK'));
  await evaluate(() => window.__messageFlowFixture.delta('A growing reply. '.repeat(80)));
  await until(() => {const scroll=document.querySelector('.mosaic-main .plugin-conversation-scroll');return scroll.scrollHeight-scroll.scrollTop-scroll.clientHeight<20;});
  await evaluate(() => {
    const scroll=document.querySelector('.mosaic-main .plugin-conversation-scroll');
    scroll.dispatchEvent(new WheelEvent('wheel',{deltaY:-200,bubbles:true}));
    scroll.scrollTop=100;scroll.dispatchEvent(new Event('scroll'));
  });
  await until(() => document.querySelector('[data-wuu-component="jump-to-latest"]'));
  const before = await evaluate(() => document.querySelector('.mosaic-main .plugin-conversation-scroll').scrollTop);
  await evaluate(() => window.__messageFlowFixture.delta('More streaming content. '.repeat(80)));
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const after = await evaluate(() => document.querySelector('.mosaic-main .plugin-conversation-scroll').scrollTop);
  assert(Math.abs(after-before)<2, 'Streaming must preserve an explicitly paused reader');
  await capture('paused-reader');
  await evaluate(() => document.querySelector('[data-wuu-component="jump-to-latest"]').click());
  await until(() => {const scroll=document.querySelector('.mosaic-main .plugin-conversation-scroll');return scroll.scrollHeight-scroll.scrollTop-scroll.clientHeight<20;});
  await evaluate(() => window.__messageFlowFixture.finish());
  await until(() => !document.querySelector('.mosaic-main [data-streaming="true"]'));
  report.cases.push({name:'stream-follow-and-pause',before,after});
  // Reduced motion and history restoration must never hide a durable message.
  await evaluate(() => {document.documentElement.dataset.appearanceMotion='reduce';window.__receiptScenario.mode='processing';});
  await send('Reduced motion message');
  assert.equal(await evaluate(() => !!document.querySelector('.mosaic-send-overlay, [data-personal-send-source]')),false);
  await capture('reduced-motion');
  await load();
  assert.equal(await evaluate(() => !!document.querySelector('.mosaic-send-overlay, [data-personal-send-source]')),false);
  await evaluate(() => [...document.querySelectorAll('.sidebar-ribbon [data-ribbon-id]')].find(node => !node.hasAttribute('data-wuu-plugin')).click());
  await evaluate(() => document.querySelector('[data-wuu-plugin="personal"]').click());
  await until(() => document.querySelector('.mosaic-main textarea'));
  assert.equal(await evaluate(() => !!document.querySelector('.mosaic-send-overlay, [data-personal-send-source]')),false);
  report.cases.push({name:'reduced-motion-reload-tab-return',passed:true});
  report.status = 'passed';
}).catch(error => { report.status = 'failed'; report.error = error.stack; console.error(error); }).finally(async () => {
  recordingActive = false;
  await recordingTask;
  fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({status:report.status, cases:report.cases.map(item=>item.name), error:report.error}));
  app.exit(report.status === 'passed' ? 0 : 1);
});
