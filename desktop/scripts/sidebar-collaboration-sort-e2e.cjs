// Run with: cd desktop && npx electron scripts/sidebar-collaboration-sort-e2e.cjs
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktop = path.resolve(__dirname, "..");
const output = path.resolve(desktop, "../artifacts/sidebar-collaboration-sort");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
const orderKey = "wuu.desktop.sidebarFunctionalGroupOrder";

app.whenReady().then(async () => {
  let stage = "starting preview server";
  const timeout = setTimeout(() => { console.error(`Sidebar sorting timed out: ${stage}`); app.exit(1); }, 90000);
  let server;
  let win;
  try {
    const { createServer } = await import("vite");
    server = await createServer({
      root: desktop,
      configFile: path.join(desktop, "dev/sidebar-accessories/vite.config.ts"),
      server: { host: "127.0.0.1", port: 0, strictPort: false, watch: { usePolling: true } },
      logLevel: "error",
    });
    await server.listen();
    console.log("Preview server ready");
    const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
    win = new BrowserWindow({ show: false, width: 1100, height: 960,
      webPreferences: { backgroundThrottling: false } });
    const js = (expression) => win.webContents.executeJavaScript(expression);
    const waitFor = (expression) => js(`new Promise((resolve, reject) => {
      const deadline = Date.now() + 10000;
      function check() {
        if (${expression}) return requestAnimationFrame(() => requestAnimationFrame(resolve));
        if (Date.now() > deadline) return reject(new Error(${JSON.stringify(`Timed out: ${expression}`)}));
        requestAnimationFrame(check);
      } check();
    })`);
    const settle = () => js(`(() => {
      for (const animation of document.getAnimations()) {
        if (Number.isFinite(animation.effect.getComputedTiming().endTime)) animation.finish();
      }
      return new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    })()`);
    const group = (id) => `[data-functional-group-id="${id}"], [data-section-id="${id}"]`;
    const order = () => js(`[...document.querySelectorAll('.sidebar-main > .sidebar-functional-group')]
      .map(el => el.dataset.functionalGroupId || el.dataset.sectionId)`);
    const pointer = async (type, point) => {
      win.webContents.sendInputEvent({ type, ...point, ...(type !== "mouseMove" ? { button: "left", clickCount: 1 } : {}) });
      await js("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
    };
    const clickCollaborationHeading = async () => {
      const point = await js(`(() => {
        const r = document.querySelector('[data-section-id="collaboration"] button[aria-expanded]').getBoundingClientRect();
        return { x: Math.round(r.x + 30), y: Math.round(r.y + r.height / 2) };
      })()`);
      await pointer("mouseMove", point);
      await pointer("mouseDown", point);
      await pointer("mouseUp", point);
    };
    const drag = async (from, to, cancel = false) => {
      stage = `drag ${from} to ${to}, cancel=${cancel}`;
      console.log(stage);
      await settle();
      const points = await js(`(() => {
        const source = document.querySelector(${JSON.stringify(group(from))});
        const target = document.querySelector(${JSON.stringify(group(to))});
        const handle = source.querySelector('.sidebar-functional-heading-toggle').getBoundingClientRect();
        const b = target.querySelector('.sidebar-functional-heading-toggle').getBoundingClientRect();
        const start = { x: Math.round(handle.x + 30), y: Math.round(handle.y + handle.height / 2) };
        return { start, end: { x: start.x, y: Math.round(b.y + b.height / 2) } };
      })()`);
      await pointer("mouseMove", points.start);
      await pointer("mouseDown", points.start);
      await pointer("mouseMove", { x: points.start.x, y: points.start.y + 8 });
      await waitFor(`document.querySelector('.sidebar-functional-group-drag-overlay')`);
      const label = await js(`document.querySelector(${JSON.stringify(group(from))}).querySelector('.sidebar-functional-heading-label').textContent`);
      assert.equal(await js("document.querySelector('.sidebar-functional-group-drag-overlay').textContent"), label);
      await pointer("mouseMove", points.end);
      if (cancel) {
        win.webContents.sendInputEvent({ type: "keyDown", keyCode: "ESC" });
        win.webContents.sendInputEvent({ type: "keyUp", keyCode: "ESC" });
      }
      await pointer("mouseUp", points.end);
      await waitFor(`!document.querySelector('.sidebar-functional-group[data-dragging="true"]')`);
      await settle();
    };
    const report = [];
    for (const theme of ["light", "dark"]) for (const size of [14, 20]) {
      stage = `load ${theme}/${size}`;
      console.log(stage);
      win.setContentSize(size === 20 ? 640 : 1100, 960);
      const url = `${origin}/dev/sidebar-accessories/?agents&theme=${theme}&size=${size}&width=${size === 20 ? 240 : 296}`;
      await win.loadURL(url);
      await waitFor(`document.querySelector('[data-section-id="collaboration"]')`);
      await js(`localStorage.setItem(${JSON.stringify(orderKey)}, JSON.stringify(['workspace', 'pinned', 'folders']))`);
      await win.loadURL(url);
      await waitFor(`document.querySelector('[data-section-id="collaboration"]')`);
      assert.deepEqual(await order(), ["collaboration", "workspace", "pinned", "folders"]);
      await drag("collaboration", "folders");
      const moved = ["workspace", "pinned", "folders", "collaboration"];
      assert.deepEqual(await order(), moved);
      assert.deepEqual(await js(`JSON.parse(localStorage.getItem(${JSON.stringify(orderKey)}))`), moved);
      assert.equal(await js(`document.querySelector('[data-section-id="collaboration"] button[aria-expanded]').getAttribute('aria-expanded')`), "true");
      await win.loadURL(url);
      await waitFor(`document.querySelector('[data-section-id="collaboration"]')`);
      assert.deepEqual(await order(), moved);
      await drag("collaboration", "workspace", true);
      assert.deepEqual(await order(), moved);
      // A plain click folds; the folded group is still a sortable target/source.
      await clickCollaborationHeading();
      assert.equal(await js(`document.querySelector('[data-section-id="collaboration"] button[aria-expanded]').getAttribute('aria-expanded')`), "false");
      await drag("collaboration", "workspace");
      assert.deepEqual(await order(), ["collaboration", "workspace", "pinned", "folders"]);
      assert.equal(await js(`document.querySelector('[data-section-id="collaboration"] button[aria-expanded]').getAttribute('aria-expanded')`), "false");
      await drag("folders", "collaboration");
      assert.deepEqual(await order(), ["folders", "collaboration", "workspace", "pinned"]);
      await clickCollaborationHeading();
      await settle();
      const geometry = await js(`(() => {
        const groups = [...document.querySelectorAll('.sidebar-main > .sidebar-functional-group')];
        const rects = groups.map(el => el.getBoundingClientRect());
        const sidebar = document.querySelector('.sidebar-main');
        return { overlap: rects.some((r, i) => i && r.top < rects[i - 1].bottom - 1),
          overflow: sidebar.scrollWidth - sidebar.clientWidth,
          headings: groups.map(el => el.querySelector('.sidebar-functional-heading-label').getBoundingClientRect().left) };
      })()`);
      assert(!geometry.overlap, "Groups must not overlap after a drop");
      assert(geometry.overflow <= 1, "Sidebar must not overflow horizontally");
      assert(Math.max(...geometry.headings) - Math.min(...geometry.headings) < 1, "Peer headings must align");
      fs.writeFileSync(path.join(output, `${theme}-${size}.png`), (await win.webContents.capturePage()).toPNG());
      report.push({ theme, size, ...geometry });
    }
    await win.loadURL(`${origin}/dev/sidebar-accessories/?agents&empty&theme=dark&size=20&width=240`);
    await waitFor(`document.querySelector('[data-section-id="collaboration"]')`);
    await drag("collaboration", "pinned");
    assert.deepEqual(await order(), ["folders", "workspace", "pinned", "collaboration"]);
    fs.writeFileSync(path.join(output, "geometry.json"), JSON.stringify(report, null, 2));
    console.log("PASS: Collaboration group drag in both directions, cancellation, folded/empty groups, legacy order migration and reload persistence; four theme/font/window layouts.");
  } finally {
    clearTimeout(timeout);
    win?.destroy();
    await server?.close();
  }
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
