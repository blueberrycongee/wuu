// Server: npx vite --config dev/channel-replies/vite.config.ts
// Check: npx electron scripts/collaboration-polish-e2e.cjs
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/collaboration-polish");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-state"));
async function waitFor(win, expression) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (await win.webContents.executeJavaScript(expression)) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error(`Timed out: ${expression}`);
}
app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1400, height: 800, webPreferences: { backgroundThrottling: false } });
  const js = expression => win.webContents.executeJavaScript(expression);
  const report = [];
  for (const theme of ["light", "dark"]) for (const width of [1400, 900, 600, 360]) {
    win.setContentSize(width, 800);
    await win.loadURL(`http://127.0.0.1:5199/dev/channel-replies/index.html?theme=${theme}`);
    await waitFor(win, `document.querySelectorAll('.channel-activity-inspect').length === 2`);
    await js(`(() => {
      window.sessions = Array.from({length: 25}, (_, index) => ({
        session_ref: 'session-' + index, room_id: 'room', named_agent_id: 'a0',
        title: index === 0 ? '' : '检查长名称与并发任务 / ' + 'unbroken-title-'.repeat(12),
        objective: '检查来源并保留完整上下文。'.repeat(24) + '/workspace/' + 'longpath'.repeat(40),
        state: ['running', 'waiting', 'failed', 'interrupted', 'completed'][index % 5],
        updated_at: '2026-09-12T14:00:00Z', created_at: '2026-09-12T14:00:00Z',
      }));
      window.wuu.listChannelSessions = async () => {
        if (window.failList) throw new Error('加载失败 / ' + 'long-error-'.repeat(40));
        return {sessions: window.sessions};
      };
      window.wuu.resumeChannelSession = ({sessionRef}) => new Promise(resolve => {
        window.resumed = sessionRef; window.finishResume = () => {
          window.sessions = window.sessions.map(s => s.session_ref === sessionRef ? {...s, state:'running'} : s);
          resolve({});
        };
      });
      document.querySelector('.channel-activity-inspect').click();
    })()`);
    await waitFor(win, `document.querySelectorAll('.channel-activity-session-row').length === 20`);
    await waitFor(win, `!document.querySelector('.session-inspector-extension').getAnimations().some(a => a.playState === 'running')`);
    const geometry = await js(`(() => {
      const list = document.querySelector('.channel-activity-session-list');
      const rows = [...list.querySelectorAll('.channel-activity-session-row')];
      const first = rows[0].getBoundingClientRect();
      const bounds = list.getBoundingClientRect();
      return {overflow:list.scrollWidth-list.clientWidth, scrollable:list.scrollHeight>list.clientHeight,
        centered:Math.abs((first.left-bounds.left)-(bounds.right-first.right)),
        rows:rows.map(row => {const entry=row.querySelector('button').getBoundingClientRect(); const retry=row.querySelector('.channel-activity-session-resume')?.getBoundingClientRect();
          return {height:row.getBoundingClientRect().height, overlap:!!retry && entry.right>retry.left+1, retryHeight:retry?.height};})};
    })()`);
    assert(geometry.overflow <= 1, `No horizontal overflow at ${theme}/${width}`);
    assert(geometry.scrollable, "Long history must scroll within the panel");
    assert(geometry.centered < 2, "Bounded session list must have symmetric gutters");
    assert(geometry.rows.every(row => row.height < 160 && !row.overlap && (!row.retryHeight || row.retryHeight >= 32)), "Long prompts must not overwhelm rows or overlap retry controls");
    await js(`document.querySelector('.channel-activity-session-resume').click()`);
    assert(await js(`!!window.resumed && [...document.querySelectorAll('.channel-activity-session-resume')].every(b => b.disabled)`));
    await js(`window.finishResume()`);
    await waitFor(win, `!document.querySelector('.channel-activity-session-resume:disabled')`);
    await js(`document.querySelector('.channel-activity-session-more').click()`);
    await waitFor(win, `document.querySelectorAll('.channel-activity-session-row').length === 25`);
    await js(`document.querySelector('.channel-activity-session-entry').focus()`);
    fs.writeFileSync(path.join(output, `${theme}-${width}-sessions.png`), (await win.webContents.capturePage()).toPNG());
    await js(`document.querySelectorAll('.channel-activity-session-entry')[1].click()`);
    await waitFor(win, `!!document.querySelector('.session-inspector-history .turn')`);
    assert(await js(`(() => {const h=document.querySelector('.session-inspector-header'); return h.scrollWidth<=h.clientWidth+1 && !!h.querySelector('button[aria-label]')})()`), "Long trace title must leave navigation accessible");
    fs.writeFileSync(path.join(output, `${theme}-${width}-trace.png`), (await win.webContents.capturePage()).toPNG());
    await js(`document.querySelector('.channel-sessions-close').click()`);
    await waitFor(win, `!document.querySelector('.session-inspector-extension')`);
    await js(`window.failList=true; document.querySelector('.channel-activity-inspect').click()`);
    await waitFor(win, `!!document.querySelector('.session-inspector-extension [role=alert]')`);
    assert(await js(`(() => {const e=document.querySelector('.session-inspector-extension [role=alert]'); return e.scrollWidth<=e.clientWidth+1;})()`), "Long error must wrap");
    await js(`window.failList=false; document.querySelector('.session-inspector-extension [role=alert] button').click()`);
    await waitFor(win, `!!document.querySelector('.channel-activity-session-entry')`);
    report.push({theme,width,...geometry});
  }
  fs.writeFileSync(path.join(output, "geometry.json"), JSON.stringify(report, null, 2));
  console.log("PASS: 8 theme/width layouts, long session titles/prompts, concurrent states, retry, pagination, trace navigation and error recovery.");
  win.destroy(); app.quit();
}).catch(error => { console.error(error); app.exit(1); });
