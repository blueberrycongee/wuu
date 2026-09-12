// Server: npx vite --config dev/channel-agent-settings/vite.config.ts
// Check: npx electron scripts/channel-composer-overlay-e2e.cjs
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/channel-composer-overlay");
app.setPath("userData", path.join(output, "browser-state"));
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function waitFor(win, expression) {
  for (let i = 0; i < 80; i++) {
    if (await win.webContents.executeJavaScript(expression)) return;
    await pause(100);
  }
  throw new Error(`Timed out: ${expression}`);
}
app.whenReady().then(async () => {
  fs.mkdirSync(output, { recursive: true });
  const win = new BrowserWindow({ show: false, width: 1400, height: 900, webPreferences: { backgroundThrottling: false } });
  const js = code => win.webContents.executeJavaScript(code);
  const report = [];
  for (const theme of ["light", "dark"]) {
    for (const width of [1400, 720, 390]) {
      win.setSize(width, 900);
      await win.loadURL(`http://127.0.0.1:5201/dev/channel-agent-settings/index.html?theme=${theme}`);
      await waitFor(win, `!!document.querySelector('.channel-composer textarea')`);
      await js(`document.querySelector('.app-shell').style.setProperty('--sidebar-open-width', '240px')`);
      if (width < 800) await js(`document.querySelector('.app-shell').style.gridTemplateColumns='minmax(0,1fr)'; document.querySelector('.app-shell').firstElementChild.style.display='none'`);
      await js(`window.wuu.listChannelMessages = async ({room_id}) => ({messages: Array.from({length: 30}, (_, i) => ({
        id: 'message-' + i, room_id, seq: i + 1, author_type: i % 2 ? 'agent' : 'human', author_id: i % 2 ? 'agent-0' : 'user', kind: 'text',
        body: i % 2 ? '可以，头像和名称作为入口，在聊天旁边调整角色说明与模型。\\n\\n运行中的任务仍可以随时查看进展。' : '输入框上方保留完整的聊天内容。滚动时，消息应该沿输入框的圆角边缘被遮挡。',
        created_at: '2026-09-12T14:00:00Z'
      }))}); window.selectRoom('group')`);
      await waitFor(win, `document.querySelectorAll('.channel-message').length === 30`);
      await waitFor(win, `(() => { const s = document.querySelector('.channel-message-stream'); return s.scrollHeight - s.scrollTop - s.clientHeight < 2; })()`);
      for (const expanded of [false, true]) {
        if (expanded) {
          await js(`(() => { const input = document.querySelector('.channel-composer textarea'); Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, '第一行：确认布局\\n第二行：输入框随内容增高\\n第三行：消息仍然可以完整滚动'); input.dispatchEvent(new Event('input', {bubbles:true})); })()`);
        }
        await js(`(() => { const s = document.querySelector('.channel-message-stream'); s.dispatchEvent(new WheelEvent('wheel', {deltaY:-500})); s.scrollTop -= 500; s.dispatchEvent(new Event('scroll')); })()`);
        await waitFor(win, `!!document.querySelector('.jump-to-latest-pill')`);
        await pause(250);
        const geometry = await js(`(() => {
          const el = s => document.querySelector(s);
          const frame = el('.channel-composer .composer-frame'), stream = el('.channel-message-stream');
          const f = frame.getBoundingClientRect(), s = stream.getBoundingClientRect(), p = el('.jump-to-latest-pill').getBoundingClientRect();
          const hit = (x, y) => document.elementFromPoint(x, y);
          return {gap: f.top - p.bottom, centerOffset: (f.left + f.right - p.left - p.right) / 2,
            streamBottom: s.bottom, frameBottom: f.bottom, frameHeight: f.height,
            clearAbove: stream.contains(hit(f.left + 5, f.top - 2)),
            clearCorner: stream.contains(hit(f.left + 2, f.top + 2)),
            cornerHit: hit(f.left + 2, f.top + 2)?.className,
            coveredInside: frame.contains(hit(f.left + f.width / 2, f.top + 4)),
            overflow: document.documentElement.scrollWidth > innerWidth};
        })()`);
        fs.writeFileSync(path.join(output, `${theme}-${width}-${expanded ? 'multiline' : 'compact'}.png`), (await win.webContents.capturePage()).toPNG());
        assert(geometry.gap >= 7 && geometry.gap <= 9, `Pill must track the input's top edge: ${JSON.stringify(geometry)}`);
        assert(Math.abs(geometry.centerOffset) < 1, "Pill must stay centered over the input");
        assert(geometry.streamBottom >= geometry.frameBottom, "Messages must scroll behind the input");
        assert(geometry.clearAbove && geometry.clearCorner && geometry.coveredInside, `Only the rounded input should cover messages at its top edge: ${JSON.stringify(geometry)}`);
        assert(!geometry.overflow, "Layout must fit the viewport");
        if (expanded) assert(geometry.frameHeight > 80, "Multiline input must grow");
        await js(`document.querySelector('.jump-to-latest-pill').click()`);
        await waitFor(win, `(() => { const s = document.querySelector('.channel-message-stream'); return s.scrollHeight - s.scrollTop - s.clientHeight < 2; })()`);
        const latestVisible = await js(`document.querySelector('.channel-message-stream').lastElementChild.getBoundingClientRect().bottom <= document.querySelector('.channel-conversation-footer').getBoundingClientRect().top`);
        assert(latestVisible, "Latest message must remain fully above the footer");
        report.push({ theme, width, expanded, ...geometry });
      }
    }
  }
  fs.writeFileSync(path.join(output, "geometry.json"), JSON.stringify(report, null, 2));
  console.log("PASS: 12 theme/size/input layouts, rounded occlusion, pill anchoring, and latest-message visibility");
  win.destroy();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
