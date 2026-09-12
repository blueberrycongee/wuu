const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const outputDir = path.resolve(__dirname, "../../artifacts/channel-reply-order");
fs.mkdirSync(outputDir, { recursive: true });
app.setPath("userData", path.join(outputDir, "browser-state"));
app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1100, height: 800, webPreferences: { backgroundThrottling: false } });
  const results = [];
  for (const theme of ["light", "dark"]) for (const width of [1100, 390]) {
    win.setContentSize(width, 800);
    await win.loadURL(`http://127.0.0.1:5199/dev/channel-replies/index.html?theme=${theme}`);
    let composerTop;
    let firstReplyTop;
    for (let stage = 0; stage <= 4; stage++) {
      await win.webContents.executeJavaScript(`window.stage = ${stage}`);
      await new Promise(resolve => setTimeout(resolve, 2300));
      const geometry = await win.webContents.executeJavaScript(`(() => {
        const stream = document.querySelector('.channel-message-stream');
        return {
          authors: [...document.querySelectorAll('.channel-message-stream .channel-author-mention')].map(n => n.textContent.replace(/^@/, '')),
          live: document.querySelectorAll('.channel-response').length,
          waiting: document.querySelectorAll('.channel-response-status').length,
          waitingAvatars: document.querySelectorAll('.channel-response-status [data-agent-avatar-id]').length,
          activityNames: [...document.querySelectorAll('.channel-activity-region strong')].map(n => n.textContent),
          activityInTranscript: document.querySelectorAll('.channel-message-stream .channel-response-status').length,
          composerTop: document.querySelector('.channel-composer').getBoundingClientRect().top,
          overflow: stream ? stream.scrollWidth - stream.clientWidth : -1,
          rows: [...document.querySelectorAll('.channel-message-stream .channel-message.agent')].map(n => ({top: n.getBoundingClientRect().top, left: n.getBoundingClientRect().left, width: n.getBoundingClientRect().width})),
        };
      })()`);
      assert.equal(geometry.overflow, 0, JSON.stringify(geometry));
      assert.deepEqual(geometry.authors, stage < 3 ? [] : stage === 3 ? ["Andy"] : ["Andy", "Andy2"]);
      assert.deepEqual(geometry.activityNames, stage < 3 ? ["Andy", "Andy2"] : stage === 3 ? ["Andy2"] : []);
      assert.equal(geometry.activityInTranscript, 0);
      assert.equal(geometry.live, 0);
      if (stage === 0) composerTop = geometry.composerTop;
      assert.equal(geometry.composerTop, composerTop, "Activity updates must not move the composer");
      if (stage === 3) firstReplyTop = geometry.rows[0].top;
      if (stage === 4) assert.equal(geometry.rows[0].top, firstReplyTop, "A later publication must not move an earlier message");
      results.push({theme, width, stage, ...geometry});
      fs.writeFileSync(path.join(outputDir, `${theme}-${width}-${stage}.png`), (await win.webContents.capturePage()).toPNG());
    }
  }
  fs.writeFileSync(path.join(outputDir, "results.json"), JSON.stringify(results, null, 2));
  console.log("PASS: light/dark, desktop/narrow; completed-only messages, ordered publication, separate activity and stable composer; no horizontal overflow.");
  win.destroy();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
