// Manual Chromium layout probe, not a stylesheet-source or screenshot merge gate.
// From desktop/: npx vite --config dev/tool-detail-alignment/vite.config.ts
// Then: ./node_modules/.bin/electron dev/tool-detail-alignment/capture.cjs
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");

const output = path.resolve(__dirname, "../../../artifacts/tool-detail-alignment", process.env.CAPTURE_LABEL || "after");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));

function measure() {
  const body = document.querySelector(".process-surface-body");
  const measureRows = container => [...container.querySelectorAll(".activity-row, .plugin-todo-item")].map((row) => {
    const marker = row.querySelector(".tool-activity-marker, .plugin-todo-marker");
    const icon = marker.querySelector("svg") || marker;
    const text = row.querySelector(".activity-copy") || marker.nextElementSibling;
    const summary = row.querySelector(".activity-copy > span");
    const m = icon.getBoundingClientRect();
    const t = text.getBoundingClientRect();
    const lineHeight = parseFloat(getComputedStyle(text).lineHeight);
    let previousRight = summary?.getBoundingClientRect().right;
    const countsVisible = [...row.querySelectorAll(".activity-add, .activity-delete")].every(count => {
      const rect = count.getBoundingClientRect();
      const visible = rect.left > previousRight && rect.right <= row.getBoundingClientRect().right + 1
        && rect.height <= lineHeight + 1 && count.scrollWidth <= count.clientWidth + 1;
      previousRight = rect.right;
      return visible;
    });
    return {
      text: text.textContent,
      fontSize: parseFloat(getComputedStyle(text).fontSize),
      tool: summary !== null,
      truncated: summary !== null && summary.scrollWidth > summary.clientWidth + 1,
      countsVisible,
      iconLeft: m.left,
      textLeft: t.left,
      centerError: m.top + m.height / 2 - (t.top + lineHeight / 2),
      wrapped: t.height > lineHeight + 1,
      overflow: row.scrollWidth > row.clientWidth + 1
        || row.getBoundingClientRect().right > container.getBoundingClientRect().right + 1,
    };
  });
  return {
    rows: measureRows(body),
    standalone: measureRows(document.querySelector("[data-standalone-tool-row]")),
    inspector: measureRows(document.querySelector(".environment-panel")),
    scrollable: body.scrollHeight > body.clientHeight,
    horizontalOverflow: [body, document.querySelector(".conversation-pane")]
      .some(element => element.scrollWidth > element.clientWidth + 1),
  };
}

app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1100, height: 800, webPreferences: { backgroundThrottling: false } });
  win.webContents.on("console-message", event => {
    if (event.level === 3) console.error(event.message);
  });
  const timeout = setTimeout(() => { console.error("Preview timed out"); app.exit(1); }, 60000);
  const reports = [];
  for (const theme of ["light", "dark"]) {
    for (const size of [14, 20]) {
      for (const width of [1100, 390]) {
        win.setSize(width, 800);
        const name = `${theme}-${size}-${width}`;
        await win.loadURL(`http://127.0.0.1:5229/dev/tool-detail-alignment/?theme=${theme}&size=${size}`);
        console.log(`loaded ${name}`);
        await win.webContents.executeJavaScript(`new Promise(resolve => {
          function open() {
            const summary = document.querySelector('summary');
            if (!summary) return requestAnimationFrame(open);
            summary.click();
            requestAnimationFrame(() => requestAnimationFrame(resolve));
          }
          open();
        })`);
        console.log(`opened ${name}`);
        await win.webContents.executeJavaScript("Promise.all(document.querySelector('.process-surface-fold').getAnimations().map(a => a.finished.catch(() => {})))");
        const result = await win.webContents.executeJavaScript(`(${measure})()`);
        await win.webContents.executeJavaScript("document.querySelector('.process-surface-body').scrollTop = 0");
        fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
        reports.push({ name, size, ...result });
        console.log(name, JSON.stringify(result));
      }
    }
  }
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(reports, null, 2));
  const misaligned = rows => rows.some(row =>
      row.overflow || Math.abs(row.textLeft - rows[0].textLeft) > 0.5 ||
      Math.abs(row.iconLeft - rows[0].iconLeft) > 0.5 || Math.abs(row.centerError) > 0.5);
  const failures = reports.filter(({ rows, standalone, inspector, horizontalOverflow, size }) =>
    rows.length !== 12 || standalone.length !== 1 || inspector.length !== 4 || horizontalOverflow
    || misaligned(rows) || misaligned(standalone) || misaligned(inspector)
    || [...rows, ...standalone].some(row => row.tool && (row.wrapped || !row.countsVisible || row.fontSize !== size))
    || !rows.some(row => row.tool && row.truncated));
  if (failures.length) throw new Error(`Tool row layout failed in ${failures.map(r => r.name).join(", ")}`);
  clearTimeout(timeout);
  app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
