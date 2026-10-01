// Real Electron layout with a gated image server: no timing race or model request
// is needed to reproduce an image completing after a conversation is restored.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const http = require("node:http");
const { performance } = require("node:perf_hooks");
const { app, BrowserWindow, nativeImage } = require("electron");

const desktop = path.resolve(__dirname, "..");
const output = process.env.WUU_IMAGE_LAYOUT_OUTPUT || path.join(desktop, "out/image-layout-e2e");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
app.on("window-all-closed", () => {});
process.env.WUU_STREAM_E2E_CWD = output;
const viewportSelector = ".conversation-pane > .scroll-region";
const activeSelector = '.cached-conversation-pane[data-active="true"]';
const marker = "IMAGE-LAYOUT-READING-MARKER";
const results = [];
const diagnostic = process.env.WUU_IMAGE_LAYOUT_DIAGNOSTIC === "1";
const selectedCase = diagnostic ? process.env.WUU_IMAGE_LAYOUT_CASE : undefined;
const scheduling = [];
let phase = "startup";
let sequence = 0;
const persistScheduling = () => {
  if (diagnostic) fs.writeFileSync(path.join(output, "scheduling.json"), JSON.stringify({
    selectedCase: selectedCase || "all", hidden: process.env.WUU_E2E_HIDDEN === "true",
    versions: process.versions, sourceCommit: process.env.WUU_FRAME_DIAGNOSTIC_COMMIT || null, scheduling,
  }, null, 2));
};
let win;
let pending = [];
let requestCount;
let requestsReady;
let released = false;
let fail = false;
let images;
const server = http.createServer((request, response) => {
  const reply = () => {
    response.writeHead(fail ? 404 : 200, { "Content-Type": "image/png", "Cache-Control": "public, max-age=3600" });
    response.end(fail ? "" : images[Number(new URL(request.url, "http://fixture").searchParams.get("image")) % images.length]);
  };
  if (released) reply();
  else {
    pending.push(reply);
    if (pending.length === requestCount) requestsReady();
  }
});
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = async (fn, ...args) => {
  const code = `(${fn})(${args.map(arg => JSON.stringify(arg)).join(",")})`;
  if (!diagnostic) return win.webContents.executeJavaScript(code, true);
  const id = ++sequence;
  const requestedEpochMs = performance.timeOrigin + performance.now();
  const result = await win.webContents.executeJavaScript(`(async () => {
    const enteredEpochMs = performance.timeOrigin + performance.now();
    const value = await (${code});
    return { value, timeOrigin: performance.timeOrigin, enteredEpochMs, finishedEpochMs: performance.timeOrigin + performance.now(), visibility: document.visibilityState };
  })()`, true);
  const receivedEpochMs = performance.timeOrigin + performance.now();
  scheduling.push({ id, phase, operation: fn.name || fn.toString().slice(0, 90), requestedEpochMs, receivedEpochMs,
    rendererTimeOrigin: result.timeOrigin, enteredEpochMs: result.enteredEpochMs, finishedEpochMs: result.finishedEpochMs,
    visibility: result.visibility, nativeVisible: win.isVisible(),
    ...(Array.isArray(result.value) && result.value.every(value => typeof value === "number") ? { frameTimes: result.value } : {}),
  });
  persistScheduling();
  return result.value;
};
async function until(fn, ...args) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (await evaluate(fn, ...args)) return;
    await sleep(20);
  }
  throw new Error(`Timed out: ${fn}`);
}
const frames = (count = 4) => evaluate((count, trace) => new Promise(resolve => {
  const times = [];
  const tick = timestamp => {
    if (trace) times.push(timestamp);
    if (--count) requestAnimationFrame(tick); else resolve(trace ? times : undefined);
  };
  requestAnimationFrame(tick);
}), count, diagnostic);
async function select(id) {
  phase = `select:${id}:click`;
  await evaluate(id => {
    const row = [...document.querySelectorAll(".thread-row")].find(row => row.textContent.includes(id));
    if (!row) throw new Error(`Missing conversation ${id}`);
    row.querySelector(".thread-row-main").click();
  }, id);
  phase = `select:${id}:active-turn`;
  await until((selector, id) => document.querySelector(`${selector} [data-turn-id]`)?.dataset.turnId === `${id}-0`, activeSelector, id);
  phase = `select:${id}:frames`;
  await frames();
  phase = "geometry";
}
function geometry() {
  const viewport = document.querySelector(".conversation-pane > .scroll-region");
  const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
  const paragraph = [...pane.querySelectorAll(".rich-paragraph")].find(node => node.textContent.startsWith("IMAGE-LAYOUT-READING-MARKER"));
  return {
    time: performance.now(), top: viewport.scrollTop, height: viewport.scrollHeight, client: viewport.clientHeight,
    markerY: paragraph?.getBoundingClientRect().top,
    images: [...pane.querySelectorAll("img.rich-image, .turn-artifact-inline-image img")].map(image => {
      const rect = image.getBoundingClientRect();
      return { complete: image.complete, width: rect.width, height: rect.height, naturalWidth: image.naturalWidth, naturalHeight: image.naturalHeight };
    }),
  };
}
function threads(url, kind, count) {
  const now = new Date().toISOString();
  return ["pictures", "plain"].map(id => ({
    id, preview: id, cwd: output, model_provider: "e2e", model: "mock-stream", status: "idle", created_at: now, updated_at: now,
    turns: Array.from({ length: 12 }, (_, index) => {
      const pictures = id === "pictures" && index === 11;
      const images = Array.from({ length: count }, (_, image) => `${url}&image=${image}`);
      return {
        id: `${id}-${index}`, status: "completed", items_view: "full", started_at: now, completed_at: now,
        items: [
          { id: `${id}-user-${index}`, type: "user_message", status: "completed", text: `Question ${index + 1}` },
          ...(pictures && kind === "artifact" ? images.map((uri, image) => ({
            id: `artifact-${image}`, type: "tool_call", name: "present_artifact", status: "completed",
            result_detail: { content: [{ type: "image", name: "fixture.png", mime_type: "image/png", uri, artifact: { placement: "inline", ref: `image-${image}` } }] },
          })) : []),
          { id: `${id}-answer-${index}`, type: "agent_message", status: "completed", text:
            `Answer ${index + 1}.\n\n` +
            (pictures && kind === "markdown-table"
              ? "| | Description |\n| --- | --- |\n" + images.map(uri => `| ![Fixture](${uri}) | This is a description of the image included in a comparison table. |`).join("\n") + "\n\n"
              : pictures && kind === "markdown" ? images.map(uri => `![Fixture](${uri})\n\n`).join("") : "") +
            (pictures ? `${marker}: keep this paragraph in the same place.\n\n` : "") +
            Array.from({ length: 4 }, (_, paragraph) => `Paragraph ${paragraph + 1}. Stable content for reading while an image finishes loading in the same conversation.`).join("\n\n") },
        ],
      };
    }),
  }));
}
async function capture(name) {
  const requestedEpochMs = performance.timeOrigin + performance.now();
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  if (diagnostic) { scheduling.push({ phase, operation: `capture:${name}`, requestedEpochMs,
    receivedEpochMs: performance.timeOrigin + performance.now() }); persistScheduling(); }
}

app.whenReady().then(async () => {
  images = [[400, 350], [300, 900], [1400, 160]].map(([width, height]) => {
    const pixels = Buffer.alloc(width * height * 4);
    for (let i = 0; i < pixels.length; i += 4) {
      const x = (i / 4) % width;
      pixels[i] = 170; pixels[i + 1] = Math.round(80 + x / width * 120); pixels[i + 2] = 60; pixels[i + 3] = 255;
    }
    return nativeImage.createFromBitmap(pixels, { width, height }).toPNG();
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  for (const kind of ["markdown-table", "markdown", "artifact"]) {
    for (const [mode, count, broken, width] of [
      ["history", 1, false, 1180], ["history", 3, false, 1180], ["bottom", 1, false, 1180], ["history", 1, true, 1180],
      ["history", 3, false, 420], ["bottom", 1, false, 420], ["history", 1, true, 420],
    ]) {
      const name = `${kind}-${mode}-${count}${broken ? "-failed" : ""}-${width}`;
      if (selectedCase && name !== selectedCase) continue;
      phase = name;
      pending = []; released = false; fail = broken; requestCount = count;
      const allRequests = new Promise(resolve => { requestsReady = resolve; });
      const url = `http://127.0.0.1:${server.address().port}/fixture.png?case=${name}`;
      const fixture = path.join(output, "threads.json");
      fs.writeFileSync(fixture, JSON.stringify(threads(url, kind, count)));
      process.env.WUU_IMAGE_LAYOUT_FIXTURE = fixture;
      win = new BrowserWindow({ width, height: 820, ...(diagnostic && process.env.WUU_FRAME_DIAGNOSTIC_FRAMELESS === "1" ? { frame: false } : {}), show: process.env.WUU_E2E_HIDDEN !== "true", webPreferences: {
        preload: path.join(__dirname, "image-layout-e2e-preload.cjs"), contextIsolation: true, sandbox: false, backgroundThrottling: false,
      } });
      await win.loadFile(diagnostic && process.env.WUU_IMAGE_LAYOUT_RENDERER || path.join(desktop, "out/renderer/index.html"));
      await until(() => document.querySelectorAll(".thread-row").length === 2);
      await select("pictures");
      if (width === 420) {
        await evaluate(() => {
          document.documentElement.dataset.theme = "dark";
          document.documentElement.style.setProperty("--font-ui", "20px");
        });
        await frames(8);
      }
      if (mode === "history") {
        await evaluate(selector => {
          const viewport = document.querySelector(selector);
          viewport.dispatchEvent(new WheelEvent("wheel", { deltaY: -150, bubbles: true }));
          const paragraph = [...document.querySelector('.cached-conversation-pane[data-active="true"]').querySelectorAll(".rich-paragraph")]
            .find(node => node.textContent.startsWith("IMAGE-LAYOUT-READING-MARKER"));
          viewport.scrollTop = Math.min(viewport.scrollHeight - viewport.clientHeight - 150,
            viewport.scrollTop + paragraph.getBoundingClientRect().top - viewport.getBoundingClientRect().top - viewport.clientHeight * 0.4);
        }, viewportSelector);
        await frames();
      }
      await select("plain");
      await select("pictures");
      const before = await evaluate(geometry);
      assert.ok(before.markerY !== undefined, "The reading marker must be rendered");
      assert.ok(before.images.length === count, "All pending image previews must be present");
      assert.ok(before.images.every(image => !image.complete), "Image responses must still be gated");
      assert.ok(before.images.every(image => image.width > 0 && image.height > 0), "Pending images must have visible preview space");
      await capture(`${name}-before`);
      if (kind === "markdown-table") {
        assert.ok(before.images.every(image => image.width >= 100),
          `Table previews must remain readable before loading, got widths: ${before.images.map(image => image.width).join(", ")}`);
      }
      await win.webContents.executeJavaScript(`window.imageLayoutGeometry = ${geometry}; void 0;`);
      await evaluate(() => {
        window.imageLayoutFrames = []; window.imageLayoutSampling = true;
        const tick = () => {
          window.imageLayoutFrames.push(window.imageLayoutGeometry());
          if (window.imageLayoutSampling) requestAnimationFrame(tick);
        };
        requestAnimationFrame(tick);
      });
      const timeout = setTimeout(() => { console.error("Image requests did not reach the gate"); app.exit(1); }, 10000);
      await allRequests;
      clearTimeout(timeout);
      released = true; pending.splice(0).forEach(reply => reply());
      await until((selector, broken, count) => {
        const pane = document.querySelector(selector);
        const images = [...pane.querySelectorAll("img.rich-image, .turn-artifact-inline-image img")];
        return broken ? images.length === 0 : images.length === count && images.every(image => image.complete && image.naturalWidth > 0);
      }, activeSelector, broken, count);
      await frames(8);
      const after = await evaluate(geometry);
      if (kind === "markdown-table" && !broken) {
        assert.ok(after.images.every(image => image.width >= 100), "Loaded table previews must remain readable");
      }
      if (mode === "bottom") assert.ok(Math.abs(after.height - after.client - after.top) <= 1, "Image completion must retain bottom following");
      const samples = await evaluate(() => { window.imageLayoutSampling = false; return window.imageLayoutFrames; });
      await capture(`${name}-after`);
      const warm = [];
      for (let repeat = 0; repeat < 3; repeat++) {
        await select("plain"); await select("pictures");
        warm.push(await evaluate(geometry));
      }
      const shifted = Math.max(...samples.map(sample => Math.abs(sample.markerY - before.markerY)));
      results.push({ name, before, after, samples, warm, shifted });
      fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(results, null, 2));
      console.log(`${name}: largest reading-marker movement ${shifted}px`);
      if (count === 3 && width === 1180) {
        for (const theme of ["light", "dark"]) for (const font of [14, 20]) for (const width of [1180, 420]) {
          win.setContentSize(width, 820);
          await evaluate((theme, font) => {
            document.documentElement.dataset.theme = theme;
            document.documentElement.style.setProperty("--font-ui", `${font}px`);
          }, theme, font);
          await frames(8);
          await evaluate((selector, width) => {
            const images = document.querySelector(selector).querySelectorAll("img.rich-image, .turn-artifact-inline-image img");
            images[width > 500 ? 1 : 2].scrollIntoView({ block: "center" });
          }, activeSelector, width);
          await frames(8);
          const contained = await evaluate(selector => {
            const pane = document.querySelector(selector);
            const bounds = pane.getBoundingClientRect();
            return [...pane.querySelectorAll("img.rich-image, .turn-artifact-inline-image img")].every(image => {
              const rect = image.getBoundingClientRect();
              const table = image.closest(".rich-table-wrap");
              if (table) {
                // Wide tables scroll within the message; both edges of a preview
                // must remain reachable without overflowing the conversation.
                const tableBounds = table.getBoundingClientRect();
                const initialScroll = table.scrollLeft;
                table.scrollLeft = 0;
                const left = image.getBoundingClientRect().left;
                table.scrollLeft = table.scrollWidth;
                const right = image.getBoundingClientRect().right;
                table.scrollLeft = initialScroll;
                return rect.width >= 100 && rect.height > 0 && tableBounds.left >= bounds.left && tableBounds.right <= bounds.right
                  && left >= tableBounds.left - 1 && right <= tableBounds.right + 1;
              }
              return rect.width > 0 && rect.height > 0 && rect.left >= bounds.left && rect.right <= bounds.right;
            });
          }, activeSelector);
          assert.ok(contained, "Portrait and panorama previews must fit the conversation at every width/font size");
          await capture(`${kind}-${theme}-${font}-${width}`);
        }
      }
      win.destroy();
    }
  }
  assert.ok(results.length === (selectedCase ? 1 : 21), "Every selected image scenario must finish");
  assert.ok(results.every(result => result.shifted <= 1 && result.warm.every(frame => Math.abs(frame.markerY - result.after.markerY) <= 1)),
    `Image completion must not move reading content: ${results.filter(result => result.shifted > 1).map(result => `${result.name}=${result.shifted}px`).join(", ")}`);
  server.close(); app.exit(0);
}).catch(async error => {
  console.error(error);
  if (win && !win.isDestroyed()) await capture("failure");
  fs.writeFileSync(path.join(output, "failure.txt"), String(error.stack));
  server.close(); app.exit(1);
});
