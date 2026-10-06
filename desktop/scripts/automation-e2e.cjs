const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktop = path.resolve(__dirname, "..");
fs.mkdirSync(path.join(desktop, "out"), { recursive: true });
const output = fs.mkdtempSync(path.join(desktop, "out/automation-e2e-"));
app.setPath("userData", path.join(output, "profile"));
app.whenReady().then(async () => {
  const timeout = setTimeout(() => app.exit(1), 120000);
  const results = [];
  let win;
  try {
    const { build } = await import("vite");
    await build({ configFile: false, root: path.join(desktop, "dev/automation"), base: "./", logLevel: "warn",
      build: { outDir: path.join(output, "renderer") } });
    win = new BrowserWindow({ width: 1280, height: 860, show: false, webPreferences: { backgroundThrottling: false } });
    const run = (fn, ...args) => win.webContents.executeJavaScript(`(${fn.toString()})(...${JSON.stringify(args)})`);
    const wait = async (selector) => run(async (selector) => {
      const start = performance.now();
      while (!document.querySelector(selector)) {
        if (performance.now() - start > 8000) throw new Error("Missing " + selector);
        await new Promise(requestAnimationFrame);
      }
      await document.fonts.ready;
      await new Promise(requestAnimationFrame);
      document.getAnimations().filter(a => a.effect.getComputedTiming().iterations !== Infinity).forEach(a => a.finish());
      await new Promise(requestAnimationFrame);
    }, selector);
    const click = async (selector) => {
      await wait(selector);
      await run(selector => document.querySelector(selector).click(), selector);
    };
    const edit = async (selector, value) => run((selector, value) => {
      const node = document.querySelector(selector);
      const proto = node.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto, "value").set.call(node, value);
      node.dispatchEvent(new Event("input", { bubbles: true }));
    }, selector, value);
    const capture = async (name) => {
      const metrics = await run(() => {
        const selectors = [".plugin-automation-main", ".plugin-automation-detail-body", ".plugin-automation-page-head", ".plugin-automation-list", ".plugin-automation-item-main", ".plugin-automation-task-title", ".plugin-automation-form", ".plugin-automation-facts"];
        return { viewport: { width: innerWidth, height: innerHeight, dpr: devicePixelRatio }, regions: selectors.flatMap(selector => [...document.querySelectorAll(selector)].filter(el => el.getClientRects().length).map(el => {
          const rect = el.getBoundingClientRect();
          return { selector, x: rect.x, y: rect.y, width: rect.width, height: rect.height, clientWidth: el.clientWidth, scrollWidth: el.scrollWidth,
            overflow: [...el.querySelectorAll("*")].filter(child => child.getClientRects().length && child.getBoundingClientRect().right > rect.right + 1).map(child => ({ tag: child.tagName, class: child.className, right: child.getBoundingClientRect().right })) };
        })) };
      });
      const image = await win.webContents.capturePage();
      fs.writeFileSync(path.join(output, name + ".png"), image.toPNG());
      results.push({ name, ...metrics, image: image.getSize() });
      // Shared switches extend their invisible hit target into the pane padding.
      // Check the actual scroll containers, not that intentional inner footprint.
      for (const region of metrics.regions.filter(region => [".plugin-automation-main", ".plugin-automation-detail-body"].includes(region.selector))) {
        assert.ok(region.scrollWidth <= region.clientWidth + 1, `${name}: horizontal overflow in ${region.selector}`);
      }
    };
    const load = async (query, width = 1280) => {
      win.setContentSize(width, 820);
      await win.loadFile(path.join(output, "renderer/index.html"), { query: { region: "primary", ...query } });
    };
    for (const theme of ["light", "dark"]) {
      for (const [width, font] of [[1280, "14"], [1000, "18"], [390, "18"]]) {
        const name = `${theme}-${width}-${font}`;
        await load({ theme, font, long: "" }, width);
        await wait(".plugin-automation-list button");
        await capture(name + "-list");
        await run(() => [...document.querySelectorAll(".plugin-automation-item-title")].find(el => el.textContent === "每日简报").closest("button").click());
        await wait(".plugin-automation-overview");
        assert.equal(await run(() => !!document.querySelector("form")), false);
        assert.ok(await run(() => document.querySelector("aside").textContent.includes("Provider rate limit reached")));
        await capture(name + "-details");
        await click('button[aria-label="编辑自动化"]');
        await wait("form textarea");
        await capture(name + "-editor");
        await click('button[aria-label="运行于"]');
        await wait('[role="listbox"]');
        await capture(name + "-menu");
      }
    }
    await load({ theme: "dark", font: "18", long: "", states: "" }, 1000);
    await wait('.plugin-automation-badge[data-run="running"]');
    await run(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true }));
      document.querySelector('.plugin-automation-badge[data-run="running"]').closest("button").focus();
    });
    win.webContents.focus();
    const focus = await run(() => ({ active: document.activeElement.className, focused: document.hasFocus() }));
    assert.equal(focus.active, "plugin-automation-item-main", JSON.stringify(focus));
    await capture("running-interrupted-keyboard-focus");
    await click('.plugin-automation-badge[data-run="running"]');
    await wait(".plugin-automation-overview");
    await capture("long-title-running-details");
    win.setContentSize(390, 500);
    await wait(".plugin-automation-overview");
    await run(() => { const pane = document.querySelector(".plugin-automation-detail-body"); pane.scrollTop = pane.scrollHeight; });
    await capture("narrow-details-scrolled");
    // Exercise the real plugin/host flow against an isolated in-memory runtime.
    await load({});
    await click(".plugin-automation-list button");
    await click('button[aria-label="编辑自动化"]');
    await edit("textarea", "Discard this draft");
    await run(() => [...document.querySelectorAll("form button")].find(b => b.textContent === "取消").click());
    await wait(".plugin-automation-overview");
    assert.ok(await run(() => !document.querySelector("aside").textContent.includes("Discard this draft")));
    await click('button[aria-label="编辑自动化"]');
    await edit('input[aria-label="名称"]', "Verified automation");
    await click('button[type="submit"]');
    await wait(".plugin-automation-overview");
    assert.equal(await run(() => document.querySelector(".plugin-automation-task-title").textContent), "Verified automation");
    await click('aside input[type="checkbox"]');
    await wait('.plugin-automation-item[data-paused="true"][data-selected="true"]');
    await capture("saved-and-paused");
    await click('button[aria-label="关闭"]');
    await wait('.plugin-automation-body[data-panel="false"]');
    await click(".plugin-automation-group:last-child .plugin-automation-item-main");
    await wait(".plugin-automation-overview");
    assert.equal(await run(() => !!document.querySelector('aside button[aria-label="编辑自动化"]')), false);
    await capture("completed-read-only");
    await load({ empty: "", theme: "dark", font: "18" }, 390);
    await wait(".plugin-automation-suggestions");
    await capture("empty-narrow-dark");
    await click(".plugin-automation-suggestions button");
    await click('button[type="submit"]');
    await wait(".plugin-automation-overview");
    assert.equal(await run(() => document.querySelectorAll(".plugin-automation-list button").length), 1);
    await capture("created-from-suggestion");
    results.push({ behavior: "overview, edit cancellation, save, pause, completed snapshot, template creation", passed: true });
    console.log(`PASS: ${results.length - 1} rendered states and interaction flow. Evidence: ${output}`);
  } catch (error) {
    console.error(error);
    results.push({ error: String(error) });
    process.exitCode = 1;
  } finally {
    fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(results, null, 2));
    win?.destroy();
    clearTimeout(timeout);
    app.exit(process.exitCode || 0);
  }
});
