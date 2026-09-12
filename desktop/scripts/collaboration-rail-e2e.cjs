const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/collaboration-rail");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-state"));
app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1100, height: 800 });
  for (const theme of ["light", "dark"]) for (const [width, height] of [[1100, 800], [900, 480]]) {
    win.setContentSize(width, height);
    await win.loadURL(`http://127.0.0.1:5199/dev/channel-replies/index.html?rail&theme=${theme}`);
    await new Promise(resolve => setTimeout(resolve, 800));
    const geometry = await win.webContents.executeJavaScript(`(() => {
      const sidebar = document.querySelector('.collaboration-sidebar');
      const nav = sidebar.querySelector('nav');
      const first = nav.querySelector('button');
      const before = sidebar.getBoundingClientRect().width;
      first.focus(); first.click();
      return { width: before, afterFocus: sidebar.getBoundingClientRect().width,
        footerBottom: sidebar.querySelector('.collaboration-sidebar-footer').getBoundingClientRect().bottom,
        overflow: nav.scrollWidth - nav.clientWidth, scrollable: nav.scrollHeight > nav.clientHeight,
        label: first.getAttribute('aria-label'), copyDisplay: getComputedStyle(first.querySelector('.collaboration-contact-copy')).display };
    })()`);
    assert.equal(geometry.width, 88);
    assert.equal(geometry.afterFocus, 88);
    assert.equal(geometry.overflow, 0);
    assert.equal(geometry.copyDisplay, "none");
    assert(geometry.label.includes("Research"));
    assert(geometry.scrollable);
    assert(geometry.footerBottom <= height);
    await win.webContents.executeJavaScript(`document.querySelector('.sidebar-account-trigger').click()`);
    await new Promise(resolve => setTimeout(resolve, 100));
    const menu = await win.webContents.executeJavaScript(`(() => {const r=document.querySelector('.sidebar-account-menu').getBoundingClientRect(); return {left:r.left,right:r.right,top:r.top,width:r.width}})()`);
    assert(menu.width >= 200 && menu.right <= width && menu.left >= 0 && menu.top >= 0);
    fs.writeFileSync(path.join(output, `${theme}-${width}.png`), (await win.webContents.capturePage()).toPNG());
    await win.webContents.executeJavaScript(`document.querySelector('.sidebar-account-trigger').click(); document.querySelector('[title="展开左侧栏"]').click()`);
    await new Promise(resolve => setTimeout(resolve, 350));
    const expanded = await win.webContents.executeJavaScript(`({width:document.querySelector('.collaboration-sidebar').getBoundingClientRect().width, search:!!document.querySelector('.collaboration-sidebar input[type=search]')})`);
    assert.equal(expanded.width, 296);
    assert(expanded.search);
  }
  console.log("PASS: avatar rail, scrollable shortcuts, fixed footer, accessible account menu and expansion in both themes and desktop sizes.");
  win.destroy(); app.quit();
}).catch(error => { console.error(error); app.exit(1); });
