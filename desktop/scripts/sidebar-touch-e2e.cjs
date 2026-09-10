const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const root = path.resolve(__dirname, "..");
app.setPath("userData", fs.mkdtempSync(path.join(root, "out", "sidebar-touch-e2e-")));
process.env.WUU_RESIZE_E2E_CWD = path.resolve(root, "..");

app.whenReady().then(async () => {
  const timeout = setTimeout(() => { console.error("Touch E2E timed out"); app.exit(1); }, 30000);
  const win = new BrowserWindow({
    width: 390, height: 820, show: true,
    webPreferences: {
      preload: path.join(__dirname, "resize-e2e-preload.cjs"),
      contextIsolation: true, sandbox: false, backgroundThrottling: false,
    },
  });
  const evaluate = (fn) => win.webContents.executeJavaScript(`(${fn})()`);
  const waitFor = async (fn) => {
    for (let i = 0; i < 100; i++) {
      if (await evaluate(fn)) return;
      await new Promise((resolve) => setTimeout(resolve, 30));
    }
    const state = await evaluate(() => {
      const shell = document.querySelector(".app-shell");
      const sidebar = document.querySelector(".sidebar");
      return { classes: shell?.className, style: shell?.getAttribute("style"), touch: shell?.dataset.sidebarTouch,
        rect: sidebar?.getBoundingClientRect().toJSON() };
    });
    throw new Error(`Timed out: ${fn}; ${JSON.stringify(state)}`);
  };
  await win.loadURL("about:blank");
  win.webContents.debugger.attach("1.3");
  const send = (method, params) => win.webContents.debugger.sendCommand(method, params);
  await send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 5 });
  await win.loadFile(path.join(root, "out/renderer/index.html"));
  win.focus();
  await waitFor(() => !!document.querySelector(".turn"));
  // Use the real renderer with fixture IPC, switching to the web host policy.
  await evaluate(() => { document.documentElement.dataset.hostKind = "web"; });
  win.setContentSize(800, 820);
  await waitFor(() => !document.querySelector(".app-shell").classList.contains("compact-navigation"));
  win.setContentSize(390, 820);
  await waitFor(() => document.querySelector(".app-shell").classList.contains("compact-navigation"));
  assert.equal(await evaluate(() => matchMedia("(pointer: coarse)").matches), true);
  await waitFor(() => document.querySelector(".app-shell")?.dataset.wuuSidebarMode === "collapsed");
  const ready = async () => {
    await waitFor(() => !document.querySelector(".app-shell").classList.contains("sidebar-drawer-closing"));
    // Let compositor hit testing catch up after the drawer closes and its
    // non-passive touch listener is reattached.
    await evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  };
  const point = (type, x, y = 300) => send("Input.dispatchTouchEvent", {
    type, touchPoints: type === "touchEnd" || type === "touchCancel" ? [] : [{ x, y }],
  });
  const geometry = () => evaluate(async () => {
    await new Promise(requestAnimationFrame);
    const rect = document.querySelector(".sidebar").getBoundingClientRect();
    const backdrop = document.querySelector(".compact-session-switcher-backdrop");
    const style = backdrop && getComputedStyle(backdrop);
    return { left: rect.left, right: rect.right, width: rect.width,
      shade: !style || style.display === "none" ? 0 : Number(style.opacity) };
  });
  const swipe = async (points) => {
    await ready();
    for (let i = 0; i < points.length; i++) {
      await send("Input.dispatchTouchEvent", {
        type: i === 0 ? "touchStart" : "touchMove",
        touchPoints: [{ x: points[i][0], y: points[i][1] }],
      });
    }
    await send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
  };
  await ready();
  await point("touchStart", 16);
  await point("touchMove", 76);
  const first = await geometry();
  assert.ok(first.right > 20 && first.right < 100, `partial reveal before release: ${JSON.stringify(first)}`);
  await point("touchMove", 136);
  const second = await geometry();
  assert.ok(Math.abs(second.right - first.right - 60) < 3, "drawer tracks finger pixel for pixel");
  assert.ok(second.shade > first.shade && second.shade < 1, "backdrop follows reveal progress");
  await point("touchMove", 230);
  await point("touchEnd");
  await waitFor(() => document.querySelector(".app-shell")?.dataset.wuuSidebarMode === "drawer");
  await waitFor(() => Math.abs(document.querySelector(".sidebar").getBoundingClientRect().left) < 1);

  const headerTargets = await evaluate(() => {
    const drawer = document.querySelector(".sidebar").getBoundingClientRect();
    return [
      document.querySelector(".compact-session-switcher-close"),
      ...document.querySelectorAll(".sidebar-brand button"),
    ].map((button) => {
      const rect = button.getBoundingClientRect();
      return {
        label: button.getAttribute("aria-label") || button.textContent,
        inside: rect.left >= drawer.left && rect.right <= drawer.right,
        reachable: button.contains(document.elementFromPoint(
          rect.left + rect.width / 2, rect.top + rect.height / 2,
        )),
      };
    });
  });
  for (const target of headerTargets) {
    assert.ok(target.inside, `header target stays inside drawer: ${target.label}`);
    assert.ok(target.reachable, `header target is not covered: ${target.label}`);
  }

  // Reverse a closing drag before release: the drawer must return with the finger.
  await ready();
  assert.equal(await evaluate(() => !!document.elementFromPoint(220, 300)?.closest(".sidebar")), true);
  await point("touchStart", 220);
  await point("touchMove", 130);
  const closing = await geometry();
  assert.ok(closing.left < -50 && closing.left > -120, "left swipe tracks closing before release");
  await point("touchMove", 200);
  const reversed = await geometry();
  assert.ok(Math.abs(reversed.left - closing.left - 70) < 3, "reversing tracks without waiting for release");
  await point("touchCancel");
  await waitFor(() => Math.abs(document.querySelector(".sidebar").getBoundingClientRect().left) < 1);
  assert.equal(await evaluate(() => document.querySelector(".app-shell").dataset.wuuSidebarMode), "drawer");

  await point("touchStart", 220);
  await point("touchMove", 140);
  await point("touchEnd");
  await point("touchStart", 320);
  assert.equal(await evaluate(() => document.querySelector(".app-shell").dataset.sidebarTouch), "dragging");
  const caughtClosing = await geometry();
  assert.ok(caughtClosing.right > 0 && caughtClosing.left < -40, "catch the drawer while it closes");
  await point("touchMove", 360);
  const reopened = await geometry();
  assert.ok(Math.abs(reopened.right - caughtClosing.right - 40) < 3, "closing animation can reverse from the backdrop");
  await point("touchEnd");
  await waitFor(() => Math.abs(document.querySelector(".sidebar").getBoundingClientRect().left) < 1);

  await swipe([[220, 300], [140, 300], [15, 300]]);
  await waitFor(() => document.querySelector(".app-shell")?.dataset.wuuSidebarMode === "collapsed");
  await ready();
  await point("touchStart", 160);
  await point("touchMove", 250);
  await point("touchCancel");
  await waitFor(() => document.querySelector(".sidebar").getBoundingClientRect().right <= 1);
  assert.equal(await evaluate(() => document.querySelector(".app-shell").dataset.wuuSidebarMode), "collapsed");

  // Catch an opening animation from the backdrop and pull it closed again.
  await ready();
  await point("touchStart", 160);
  await point("touchMove", 250);
  await point("touchEnd");
  await point("touchStart", 360);
  assert.equal(await evaluate(() => document.querySelector(".app-shell").dataset.sidebarTouch), "dragging");
  const caught = await geometry();
  assert.ok(caught.right > 0 && caught.left < 0, "catch the drawer before it finishes opening");
  await point("touchMove", 320);
  const pulledBack = await geometry();
  assert.ok(Math.abs(pulledBack.right - caught.right + 40) < 3, "caught drawer follows the new finger without jumping");
  await point("touchMove", 80);
  await point("touchEnd");
  await waitFor(() => document.querySelector(".sidebar").getBoundingClientRect().right <= 1);
  assert.equal(await evaluate(() => document.querySelector(".app-shell").dataset.wuuSidebarMode), "collapsed");

  // Rotation/window resizing must drop an in-flight touch override.
  await ready();
  await point("touchStart", 100);
  await point("touchMove", 180);
  win.setContentSize(800, 820);
  await waitFor(() => !document.querySelector(".app-shell").classList.contains("compact-navigation"));
  await point("touchCancel");
  win.setContentSize(390, 820);
  await waitFor(() => document.querySelector(".app-shell").classList.contains("compact-navigation"));
  await waitFor(() => document.querySelector(".sidebar").getBoundingClientRect().right <= 1);

  // A natural diagonal thumb swipe should open without a flat horizontal start.
  await swipe([[160, 300], [184, 320], [240, 355], [365, 390]]);
  await waitFor(() => document.querySelector(".app-shell")?.dataset.wuuSidebarMode === "drawer");
  await waitFor(() => Math.abs(document.querySelector(".sidebar").getBoundingClientRect().left) < 1);
  await evaluate(() => document.querySelector(".compact-session-switcher-backdrop").click());
  await waitFor(() => document.querySelector(".app-shell")?.dataset.wuuSidebarMode === "collapsed");
  await swipe([[160, 300], [169, 288], [178, 285], [250, 260]]);
  await waitFor(() => document.querySelector(".app-shell")?.dataset.wuuSidebarMode === "drawer");
  await waitFor(() => Math.abs(document.querySelector(".sidebar").getBoundingClientRect().left) < 1);
  await evaluate(() => document.querySelector(".compact-session-switcher-backdrop").click());
  await waitFor(() => document.querySelector(".app-shell")?.dataset.wuuSidebarMode === "collapsed");

  for (const [x, dx, dy] of [[250, 34, 42], [280, 34, -42], [350, 26, 32]]) {
    // Sparse events model a light diagonal thumb swipe from the right half.
    await swipe([[x, 300], [x + dx, 300 + dy]]);
    await waitFor(() => document.querySelector(".app-shell")?.dataset.wuuSidebarMode === "drawer");
    await waitFor(() => Math.abs(document.querySelector(".sidebar").getBoundingClientRect().left) < 1);
    await evaluate(() => document.querySelector(".compact-session-switcher-backdrop").click());
    await waitFor(() => document.querySelector(".app-shell")?.dataset.wuuSidebarMode === "collapsed");
  }
  await swipe([[160, 300], [162, 320], [164, 360]]);
  assert.equal(await evaluate(() => document.querySelector(".app-shell").dataset.wuuSidebarMode), "collapsed");
  console.log("PASS: real Chromium touch tracks and interrupts the drawer, opens with light right-side diagonal swipes, cancels, and preserves vertical scrolling");
  clearTimeout(timeout);
  win.destroy();
  app.quit();
}).catch((error) => {
  console.error(error);
  app.exit(1);
});
