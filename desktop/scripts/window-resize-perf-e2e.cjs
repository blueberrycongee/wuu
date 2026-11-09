const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const rendererHtml = path.join(desktopRoot, "out", "renderer", "index.html");
const preload = path.join(__dirname, "resize-e2e-preload.cjs");
const userData = path.join(desktopRoot, "out", "e2e", "window-resize-user-data");
const disableGpu = process.env.WUU_E2E_DISABLE_GPU === "true";
const visible = process.env.WUU_E2E_VISIBLE === "true";

process.env.WUU_RESIZE_E2E_CWD = repoRoot;
fs.rmSync(userData, { recursive: true, force: true });
fs.mkdirSync(userData, { recursive: true });
app.setPath("userData", userData);
if (disableGpu) {
  app.commandLine.appendSwitch("disable-gpu");
  app.commandLine.appendSwitch("disable-software-rasterizer");
}

app.whenReady().then(run).catch(fail);

async function run() {
  assert.ok(fs.existsSync(rendererHtml), "Renderer build is missing. Run npm run build first.");
  assert.ok(fs.existsSync(preload), "Resize E2E preload is missing.");

  const win = new BrowserWindow({
    width: 1380,
    height: 860,
    show: visible,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      preload,
      sandbox: false
    }
  });

  win.webContents.on("render-process-gone", (_event, details) => {
    fail(new Error(`Renderer process exited: ${details.reason}`));
  });
  win.webContents.on("console-message", (_event, level, message, line, sourceId) => {
    if (level >= 2) {
      console.error(`renderer console: ${message} (${sourceId}:${line})`);
    }
  });

  await loadFile(win, rendererHtml);
  await waitFor(win, () => Boolean(document.querySelector(".conversation-pane")), 5000);
  await waitFor(
    win,
    () => {
      const node = document.querySelector(".scroll-region");
      return document.querySelectorAll(".turn").length > 0 &&
        node instanceof HTMLElement &&
        node.scrollHeight > node.clientHeight;
    },
    5000
  );
  await delay(250);
  await evaluate(win, () => {
    const node = document.querySelector(".scroll-region");
    if (node instanceof HTMLElement) {
      const bottom = Math.max(0, node.scrollHeight - node.clientHeight);
      node.scrollTop = Math.max(0, bottom - 100);
      node.dispatchEvent(new Event("scroll", { bubbles: true }));
      node.scrollTop = node.scrollHeight;
      node.dispatchEvent(new Event("scroll", { bubbles: true }));
    }
  });
  await waitFor(
    win,
    () => document.querySelector(".scroll-region")?.style.overflowAnchor === "none",
    1000
  );
  await delay(120);
  win.setSize(1020, 790);
  await delay(220);
  await evaluate(win, () => {
    window.__wuuWindowResizePerfProbe = {
      turnScansDuringResize: 0,
      scrollTopWritesDuringResize: 0,
      scrollMetricReadsDuringResize: 0,
      boundingRectReadsDuringResize: 0
    };
    if (!window.__wuuWindowResizePerfProbeInstalled) {
      window.__wuuWindowResizePerfProbeInstalled = true;
      const originalQuerySelectorAll = Element.prototype.querySelectorAll;
      Element.prototype.querySelectorAll = function querySelectorAllProbe(selector) {
        if (
          selector === ".turn[data-turn-id]" &&
          document.documentElement.classList.contains("window-resizing")
        ) {
          window.__wuuWindowResizePerfProbe.turnScansDuringResize += 1;
        }
        return originalQuerySelectorAll.call(this, selector);
      };
      const originalGetBoundingClientRect = Element.prototype.getBoundingClientRect;
      Element.prototype.getBoundingClientRect = function getBoundingClientRectProbe() {
        if (document.documentElement.classList.contains("window-resizing")) {
          window.__wuuWindowResizePerfProbe.boundingRectReadsDuringResize += 1;
        }
        return originalGetBoundingClientRect.call(this);
      };

      let scrollTopProto = HTMLElement.prototype;
      let scrollTopDescriptor;
      while (scrollTopProto && !scrollTopDescriptor) {
        const descriptor = Object.getOwnPropertyDescriptor(scrollTopProto, "scrollTop");
        if (descriptor?.get && descriptor?.set && descriptor.configurable) {
          scrollTopDescriptor = descriptor;
          break;
        }
        scrollTopProto = Object.getPrototypeOf(scrollTopProto);
      }
      if (scrollTopDescriptor && scrollTopProto) {
        Object.defineProperty(scrollTopProto, "scrollTop", {
          configurable: true,
          enumerable: scrollTopDescriptor.enumerable,
          get() {
            return scrollTopDescriptor.get.call(this);
          },
          set(value) {
            if (
              document.documentElement.classList.contains("window-resizing") &&
              this instanceof HTMLElement &&
              this.matches(".scroll-region, .reasoning-scroll, .process-output-scroll")
            ) {
              window.__wuuWindowResizePerfProbe.scrollTopWritesDuringResize += 1;
            }
            return scrollTopDescriptor.set.call(this, value);
          }
        });
      }
      for (const property of ["scrollHeight", "clientHeight"]) {
        let metricProto = Element.prototype;
        let metricDescriptor;
        while (metricProto && !metricDescriptor) {
          const descriptor = Object.getOwnPropertyDescriptor(metricProto, property);
          if (descriptor?.get && descriptor.configurable) {
            metricDescriptor = descriptor;
            break;
          }
          metricProto = Object.getPrototypeOf(metricProto);
        }
        if (metricDescriptor && metricProto) {
          Object.defineProperty(metricProto, property, {
            configurable: true,
            enumerable: metricDescriptor.enumerable,
            get() {
              if (
                document.documentElement.classList.contains("window-resizing") &&
                this instanceof HTMLElement &&
                this.matches(".scroll-region")
              ) {
                window.__wuuWindowResizePerfProbe.scrollMetricReadsDuringResize += 1;
              }
              return metricDescriptor.get.call(this);
            }
          });
        }
      }
    }
    window.__wuuWindowResizePerfSamples = [];
    window.__wuuWindowResizePerfSampling = true;
    let previousTs;
    const sample = (ts) => {
      const samples = window.__wuuWindowResizePerfSamples;
      const scroll = document.querySelector(".scroll-region");
      const shell = document.querySelector(".app-shell");
      samples.push({
        dt: previousTs === undefined ? 0 : ts - previousTs,
        resizing: document.documentElement.classList.contains("window-resizing"),
        autoFollow: scroll instanceof HTMLElement && scroll.style.overflowAnchor === "none",
        shellTransitionProperty: shell instanceof HTMLElement ? getComputedStyle(shell).transitionProperty : "",
        sidebarCollapsed: shell?.classList.contains("sidebar-collapsed") ?? false,
        scrollTop: scroll?.scrollTop ?? 0,
        width: window.innerWidth
      });
      previousTs = ts;
      if (window.__wuuWindowResizePerfSampling) {
        window.requestAnimationFrame(sample);
      }
    };
    window.requestAnimationFrame(sample);
  });

  for (let index = 0; index < 96; index += 1) {
    const shrinking = index < 48;
    const offset = shrinking ? (48 - index) * 5 : (index - 48) * 5;
    win.setSize(780 + offset, 760 + Math.floor(offset / 8));
    await delay(8);
  }
  await delay(260);

  const result = await evaluate(win, () => {
    window.__wuuWindowResizePerfSampling = false;
    return new Promise((resolve) => {
      window.requestAnimationFrame(() => {
        resolve({
          samples: window.__wuuWindowResizePerfSamples ?? [],
          probe: window.__wuuWindowResizePerfProbe
        });
      });
    });
  });
  const samples = result.samples;
  const probe = result.probe ?? {};

  const maxFrameMs = Math.max(...samples.map((sample) => sample.dt));
  const sorted = samples.map((sample) => sample.dt).sort((left, right) => left - right);
  const p95FrameMs = sorted[Math.floor(sorted.length * 0.95)] ?? 0;
  const resizingSamples = samples.filter((sample) => sample.resizing);
  const autoFollowResizeSamples = resizingSamples.filter((sample) => sample.autoFollow);
  const narrowResizeSamples = resizingSamples.filter((sample) => sample.width < 880);
  const widths = samples.map((sample) => sample.width);
  const summary = {
    samples: samples.length,
    resizingSamples: resizingSamples.length,
    autoFollowResizeSamples: autoFollowResizeSamples.length,
    narrowResizeSamples: narrowResizeSamples.length,
    maxFrameMs: Math.round(maxFrameMs),
    p95FrameMs: Math.round(p95FrameMs),
    minWidth: Math.min(...widths),
    maxWidth: Math.max(...widths),
    turnScansDuringResize: probe.turnScansDuringResize ?? 0,
    scrollTopWritesDuringResize: probe.scrollTopWritesDuringResize ?? 0,
    scrollMetricReadsDuringResize: probe.scrollMetricReadsDuringResize ?? 0,
    boundingRectReadsDuringResize: probe.boundingRectReadsDuringResize ?? 0,
    disableGpu,
    visible
  };
  console.log(JSON.stringify(summary, null, 2));
  assert.ok(samples.length > 20, "Window resize perf probe should collect frame samples.");
  assert.ok(resizingSamples.length > 0, "window-resizing marker should appear during BrowserWindow resize.");
  assert.equal(
    summary.autoFollowResizeSamples,
    summary.resizingSamples,
    `Resize performance probe must stay in the auto-follow scenario it is intended to verify. Summary=${JSON.stringify(summary)}`
  );
  assert.ok(
    narrowResizeSamples.length > 0 && narrowResizeSamples.every((sample) => sample.sidebarCollapsed),
    `Narrow resize samples should use the collapsed sidebar layout. Summary=${JSON.stringify(summary)}`
  );
  assert.ok(
    narrowResizeSamples.every((sample) => sample.shellTransitionProperty === "none"),
    `Sidebar auto-collapse must not lag behind live window resize through a grid transition. Summary=${JSON.stringify(summary)}`
  );
  assert.ok(summary.maxWidth - summary.minWidth >= 160, `Window width should change during probe. Summary=${JSON.stringify(summary)}`);
  assert.ok(summary.p95FrameMs <= 50, `Window resize p95 frame time should stay below 50ms. Summary=${JSON.stringify(summary)}`);
  assert.ok(summary.maxFrameMs <= 120, `Window resize max frame time should stay below 120ms. Summary=${JSON.stringify(summary)}`);
  assert.equal(summary.turnScansDuringResize, 0, `Turn rail should not scan turn DOM during live window resize. Summary=${JSON.stringify(summary)}`);
  assert.ok(summary.scrollTopWritesDuringResize > 0, `Auto-follow should continuously track reflow during live window resize. Summary=${JSON.stringify(summary)}`);
  assert.ok(
    summary.scrollTopWritesDuringResize <= summary.resizingSamples + 2,
    `Auto-follow should coalesce live resize updates to at most one scrollTop write per frame. Summary=${JSON.stringify(summary)}`
  );
  assert.equal(summary.scrollMetricReadsDuringResize, 0, `Auto-follow should not read scroll metrics during live window resize. Summary=${JSON.stringify(summary)}`);
  assert.equal(summary.boundingRectReadsDuringResize, 0, `Resize handlers should not force element rect reads during live window resize. Summary=${JSON.stringify(summary)}`);

  win.close();
  app.quit();
}

function loadFile(win, file) {
  return new Promise((resolve, reject) => {
    const finish = () => resolve();
    const failLoad = (_event, code, description) => reject(new Error(`Failed to load ${file}: ${code} ${description}`));
    win.webContents.once("did-finish-load", finish);
    win.webContents.once("did-fail-load", failLoad);
    win.loadFile(file).catch(reject);
  });
}

async function evaluate(win, fn) {
  return win.webContents.executeJavaScript(`(${fn.toString()})()`, true);
}

async function waitFor(win, fn, timeoutMs) {
  const started = Date.now();
  let lastValue;
  while (Date.now() - started < timeoutMs) {
    lastValue = await evaluate(win, fn);
    if (lastValue) {
      return lastValue;
    }
    await delay(20);
  }
  throw new Error(`Timed out waiting for condition. Last value: ${JSON.stringify(lastValue)}`);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function fail(error) {
  console.error(error);
  app.quit();
  process.exitCode = 1;
}
