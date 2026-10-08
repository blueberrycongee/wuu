// Real Electron layout with a gated image server: no timing race or model request
// is needed to reproduce an image completing after a conversation is restored.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const http = require("node:http");
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
let win;
let pending = [];
let requestCount;
let requestsReady;
let released = false;
let fail = false;
let images;
// Two solid-color frames, looping every 400ms; no external fixture dependency.
const animatedGIF = Buffer.from("R0lGODlhMAAwAIEAAP+lAAAAAAAAAAAAACH/C05FVFNDQVBFMi4wAwEAAAAh+QQAFAAAACwAAAAAMAAwAAAITwABCBxIsKDBgwgTKlzIsKHDhxAjSpxIsaLFixgzatzIsaPHjyBDihxJsqTJkyhTqlzJsqXLlzBjypxJs6bNmzhz6tzJs6fPn0CDCh2qMSAAIfkEARQAAQAsAAAAADAAMACBAAD/AAAAAAAAAAAACE8AAQgcSLCgwYMIEypcyLChw4cQI0qcSLGixYsYM2rcyLGjx48gQ4ocSbKkyZMoU6pcybKly5cwY8qcSbOmzZs4c+rcybOnz59AgwodqjEgADs=", "base64");
const server = http.createServer((request, response) => {
  const reply = () => {
    const params = new URL(request.url, "http://fixture").searchParams;
    const gif = params.get("gif") === "true";
    response.writeHead(fail ? 404 : 200, { "Content-Type": gif ? "image/gif" : "image/png", "Cache-Control": "public, max-age=3600" });
    response.end(fail ? "" : gif ? animatedGIF : images[Number(params.get("image")) % images.length]);
  };
  if (released) reply();
  else {
    pending.push(reply);
    if (pending.length === requestCount) requestsReady();
  }
});
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn})(${args.map(arg => JSON.stringify(arg)).join(",")})`, true);
async function until(fn, ...args) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (await evaluate(fn, ...args)) return;
    await sleep(20);
  }
  throw new Error(`Timed out: ${fn}`);
}
const frames = (count = 4) => evaluate(count => new Promise(resolve => {
  const tick = () => --count ? requestAnimationFrame(tick) : resolve();
  requestAnimationFrame(tick);
}), count);
function layoutMotionSettled() {
  return !document.querySelector(".window-resizing, .layout-motion-active, .sidebar-animating, .environment-panel.closing")
    && !document.getAnimations().some(animation => animation instanceof CSSTransition && animation.playState === "running");
}
async function select(id) {
  await evaluate(id => {
    const row = [...document.querySelectorAll(".thread-row")].find(row => row.textContent.includes(id));
    if (!row) throw new Error(`Missing conversation ${id}`);
    row.querySelector(".thread-row-main").click();
  }, id);
  await until((selector, id) => document.querySelector(`${selector} [data-turn-id]`)?.dataset.turnId === `${id}-0`, activeSelector, id);
  await frames();
}
function geometry() {
  const viewport = document.querySelector(".conversation-pane > .scroll-region");
  const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
  const paragraph = [...pane.querySelectorAll(".rich-paragraph")].find(node => node.textContent.startsWith("IMAGE-LAYOUT-READING-MARKER"));
  return {
    time: performance.now(), top: viewport.scrollTop, height: viewport.scrollHeight, client: viewport.clientHeight,
    pageLeft: document.scrollingElement.scrollLeft, viewportLeft: viewport.scrollLeft,
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
      const images = Array.from({ length: count }, (_, image) => `${url}&image=${image}&gif=${kind === "artifact" && image === 0}`);
      return {
        id: `${id}-${index}`, status: "completed", items_view: "full", started_at: now, completed_at: now,
        items: [
          { id: `${id}-user-${index}`, type: "user_message", status: "completed", text: `Question ${index + 1}` },
          ...(pictures && kind === "artifact" ? images.map((uri, image) => ({
            id: `artifact-${image}`, type: "tool_call", name: "present_artifact", status: "completed",
            result_detail: { content: [{ type: "image", name: image === 0 ? "fixture.gif" : "fixture.png", mime_type: image === 0 ? "image/gif" : "image/png", uri, artifact: { placement: "inline", ref: `image-${image}` } }] },
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
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
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
      ["history", 1, false, 1180], ["history", kind === "artifact" ? 4 : 3, false, 1180], ["bottom", 1, false, 1180], ["history", 1, true, 1180],
      ["history", kind === "artifact" ? 4 : 3, false, 420], ["bottom", 1, false, 420], ["history", 1, true, 420],
    ]) {
      const name = `${kind}-${mode}-${count}${broken ? "-failed" : ""}-${width}`;
      pending = []; released = false; fail = broken; requestCount = count;
      const allRequests = new Promise(resolve => { requestsReady = resolve; });
      const url = `http://127.0.0.1:${server.address().port}/fixture.png?case=${name}`;
      const fixture = path.join(output, "threads.json");
      fs.writeFileSync(fixture, JSON.stringify(threads(url, kind, count)));
      process.env.WUU_IMAGE_LAYOUT_FIXTURE = fixture;
      win = new BrowserWindow({ width, height: 820, show: process.env.WUU_E2E_HIDDEN !== "true", webPreferences: {
        preload: path.join(__dirname, "image-layout-e2e-preload.cjs"), contextIsolation: true, sandbox: false, backgroundThrottling: false,
      } });
      await win.loadFile(path.join(desktop, "out/renderer/index.html"));
      await until(() => document.querySelectorAll(".thread-row").length === 2);
      await select("pictures");
      if (width === 420) {
        await evaluate(() => {
          document.documentElement.dataset.theme = "dark";
          document.documentElement.style.setProperty("--font-ui", "20px");
        });
        await frames(8);
      }
      // The initial environment panel transition can still reflow paragraphs.
      // Establish the reading position only after that layout has settled.
      await until(layoutMotionSettled);
      await frames();
      if (kind === "artifact" && count > 1) {
        // Offscreen carousel images load lazily. Visit them before establishing
        // the reading baseline, while every response is still held by the gate.
        for (let index = 0; index < count; index++) {
          await evaluate((selector, index) => {
            document.querySelectorAll(`${selector} .turn-artifact-inline-image img`)[index]
              .scrollIntoView({ block: "center", inline: "nearest" });
          }, activeSelector, index);
          await frames();
        }
        await evaluate(selector => {
          document.querySelector(`${selector} .turn-artifact-gallery-items`).scrollLeft = 0;
        }, activeSelector);
        await frames();
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
      if (kind === "artifact" && count > 1) {
        const gallery = await evaluate(selector => {
          const pane = document.querySelector(selector);
          const images = [...pane.querySelectorAll(".turn-artifact-inline-image")];
          return {
            groups: pane.querySelectorAll(".turn-artifact-inline-list").length,
            boxes: images.map(image => {
              const { x, y, width, height } = image.getBoundingClientRect();
              return { x, y, width, height };
            }),
          };
        }, activeSelector);
        assert.equal(gallery.groups, 1, "Consecutive publications must share one gallery");
        const [first, second] = gallery.boxes;
        assert.ok(Math.abs(first.y - second.y) <= 1 && second.x >= first.x + first.width,
          "The default gallery must remain one row at every window width");
        assert.ok(gallery.boxes.every(box => Math.abs(box.y - first.y) <= 1),
          "Additional images must scroll horizontally rather than wrap");
      }
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
      if (count > 1 && width === 1180) {
        for (const theme of ["light", "dark"]) for (const font of [14, 20]) for (const width of [1180, 420]) {
          // Native resize delivery and responsive panel motion can outlast a
          // fixed frame count. Measure only after resize handlers and transitions finish.
          await evaluate(width => {
            window.imageLayoutResizeReady = window.innerWidth === width && window.innerHeight === 820;
            if (!window.imageLayoutResizeReady) {
              const resized = () => {
                if (window.innerWidth !== width || window.innerHeight !== 820) return;
                window.removeEventListener("resize", resized);
                requestAnimationFrame(() => requestAnimationFrame(() => {
                  window.imageLayoutResizeReady = true;
                }));
              };
              window.addEventListener("resize", resized);
            }
          }, width);
          win.setContentSize(width, 820);
          await until(() => window.imageLayoutResizeReady);
          await until(layoutMotionSettled);
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
              const table = image.closest(".rich-table-wrap, .turn-artifact-gallery-items");
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
                // Table previews keep a readable intrinsic width; carousel
                // thumbnails scale with the available conversation column.
                const readable = table.classList.contains("rich-table-wrap") ? rect.width >= 100 : rect.width > 0;
                return readable && rect.height > 0 && tableBounds.left >= bounds.left && tableBounds.right <= bounds.right
                  && left >= tableBounds.left - 1 && right <= tableBounds.right + 1;
              }
              return rect.width > 0 && rect.height > 0 && rect.left >= bounds.left && rect.right <= bounds.right;
            });
          }, activeSelector);
          assert.ok(contained, `${kind}-${theme}-${font}-${width}: portrait and panorama previews must fit the conversation`);
          await capture(`${kind}-${theme}-${font}-${width}`);
          if (kind === "artifact") {
            win.webContents.sendInputEvent({ type: "mouseMove", x: 1, y: 1 });
            const gallery = await evaluate(selector => {
              document.activeElement?.blur();
              const pane = document.querySelector(selector);
              const list = pane.querySelector(".turn-artifact-gallery-items");
              list.scrollIntoView({ block: "center" });
              const bounds = list.getBoundingClientRect();
              const root = list.parentElement.getBoundingClientRect();
              const button = pane.querySelector("[data-gallery-toggle]").getBoundingClientRect();
              return { height: bounds.height, rootHeight: root.height,
                x: Math.round(bounds.left + 12), y: Math.round(bounds.top + bounds.height / 2),
                buttonX: Math.round(button.left + button.width / 2), buttonY: Math.round(button.top + button.height / 2),
                overlays: button.top >= bounds.top && button.bottom <= bounds.bottom && button.right <= bounds.right };
            }, activeSelector);
            assert.ok(Math.abs(gallery.height - gallery.rootHeight) <= 1, "View controls must not reserve a toolbar row");
            assert.ok(gallery.overlays, "The view toggle must overlay the image area");
            await until(selector => getComputedStyle(document.querySelector(`${selector} .turn-artifact-gallery-toolbar`)).opacity === "0", activeSelector);
            win.webContents.sendInputEvent({ type: "mouseMove", x: gallery.x, y: gallery.y });
            await until(selector => getComputedStyle(document.querySelector(`${selector} .turn-artifact-gallery-toolbar`)).opacity === "1", activeSelector);
            await capture(`gallery-hover-${theme}-${font}-${width}`);
            win.webContents.sendInputEvent({ type: "mouseMove", x: gallery.buttonX, y: gallery.buttonY });
            await until(selector => document.querySelector(`${selector} [data-gallery-toggle]`).matches(":hover"), activeSelector);
            await until(selector => !document.querySelector(`${selector} [data-gallery-toggle]`).getAnimations().some(animation => animation.playState === "running"), activeSelector);
            await capture(`gallery-control-hover-${theme}-${font}-${width}`);
            await evaluate(selector => {
              const pane = document.querySelector(selector);
              window.galleryImages = [...pane.querySelectorAll(".turn-artifact-inline-image img")];
              pane.querySelector("[data-gallery-toggle]").click();
            }, activeSelector);
            await frames();
            const grid = await evaluate(selector => {
              const pane = document.querySelector(selector);
              const list = pane.querySelector(".turn-artifact-gallery-items");
              return {
                sameImages: window.galleryImages.every((image, index) => image === list.querySelectorAll("img")[index]),
                overflow: list.scrollWidth > list.clientWidth,
                boxes: [...list.querySelectorAll(".turn-artifact-inline-image")].map(image => {
                  const { x, y, width, height } = image.getBoundingClientRect();
                  return { x, y, width, height };
                }),
              };
            }, activeSelector);
            assert.ok(grid.sameImages, "Changing view must not remount images or restart GIFs");
            assert.ok(!grid.overflow, "The tiled view must not scroll horizontally");
            assert.ok(grid.boxes.slice(0, 3).every(box => Math.abs(box.y - grid.boxes[0].y) <= 1),
              "Tiled view must show three images per row");
            assert.ok(grid.boxes[3].y >= grid.boxes[0].y + grid.boxes[0].height,
              "The fourth image must appear on the next row");
            await evaluate(selector => document.querySelector(`${selector} .turn-artifact-gallery-items`).scrollIntoView({ block: "center" }), activeSelector);
            await frames();
            await capture(`gallery-grid-${theme}-${font}-${width}`);
            await evaluate(selector => document.querySelector(`${selector} [data-gallery-toggle]`).click(), activeSelector);
            await frames();
          }
        }
      }
      if (kind === "artifact" && count > 1 && width === 1180) {
        // The theme/font matrix ends at 420px. Restore this scenario's width
        // before pointer interactions, where thumbnail toolbars have a different footprint.
        win.setContentSize(width, 820);
        await until(width => window.innerWidth === width && window.innerHeight === 820, width);
        await frames();
        await until(layoutMotionSettled);
        await evaluate(selector => {
          const list = document.querySelector(`${selector} .turn-artifact-gallery-items`);
          list.scrollLeft = 0;
          list.scrollIntoView({ block: "center" });
        }, activeSelector);
        await until(selector => {
          const style = getComputedStyle(document.querySelector(`${selector} .turn-artifact-gallery-items`));
          return parseFloat(style.getPropertyValue("--scroll-fade-start")) === 0
            && parseFloat(style.getPropertyValue("--scroll-fade-end")) > 0;
        }, activeSelector);
        await capture("carousel-start-fade");
        const wheel = await evaluate(selector => {
          const list = document.querySelector(`${selector} .turn-artifact-gallery-items`);
          const zoom = new WheelEvent("wheel", { deltaY: 100, ctrlKey: true, bubbles: true, cancelable: true });
          list.dispatchEvent(zoom);
          const move = new WheelEvent("wheel", { deltaY: 100, bubbles: true, cancelable: true });
          list.dispatchEvent(move);
          const moved = list.scrollLeft;
          list.scrollLeft = list.scrollWidth;
          const edge = new WheelEvent("wheel", { deltaY: 100, bubbles: true, cancelable: true });
          list.dispatchEvent(edge);
          return { moved, moveHandled: move.defaultPrevented, zoomHandled: zoom.defaultPrevented, edgeHandled: edge.defaultPrevented };
        }, activeSelector);
        assert.ok(wheel.moved > 0 && wheel.moveHandled, "A mouse wheel must reveal more images");
        assert.ok(!wheel.zoomHandled && !wheel.edgeHandled, "Zoom and page scrolling at the edge must remain available");
        await until(selector => {
          const style = getComputedStyle(document.querySelector(`${selector} .turn-artifact-gallery-items`));
          return parseFloat(style.getPropertyValue("--scroll-fade-start")) > 0
            && parseFloat(style.getPropertyValue("--scroll-fade-end")) === 0;
        }, activeSelector);
        await capture("carousel-end-fade");
        await evaluate(selector => {
          const list = document.querySelector(`${selector} .turn-artifact-gallery-items`);
          list.scrollLeft = 0;
          document.querySelector(`${selector} [data-gallery-toggle]`).focus();
        }, activeSelector);
        win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Tab" });
        win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Tab" });
        await until(selector => document.activeElement === document.querySelector(`${selector} .turn-artifact-gallery-items`), activeSelector);
        win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Right" });
        win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Right" });
        await until(selector => document.querySelector(`${selector} .turn-artifact-gallery-items`).scrollLeft > 0, activeSelector);
        await capture("carousel-keyboard-focus");
        await evaluate(selector => {
          const button = document.querySelector(`${selector} .turn-artifact-inline-image button`);
          button.scrollIntoView({ block: "center" });
          button.focus();
        }, activeSelector);
        await frames();
        await capture("gallery-entry");
        const imageTarget = await evaluate(() => {
          const button = document.activeElement;
          const rect = button.getBoundingClientRect();
          const x = Math.round(rect.left + rect.width / 2), y = Math.round(rect.top + rect.height / 2);
          if (!button.contains(document.elementFromPoint(x, y))) throw new Error("Image preview click target is obscured");
          return { x, y };
        });
        // Use a real pointer click so hover and focus follow the image target.
        win.webContents.sendInputEvent({ type: "mouseMove", ...imageTarget });
        win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...imageTarget });
        win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...imageTarget });
        await until(() => document.querySelector(".image-preview-overlay img")?.naturalWidth === 48);
        await until(() => !document.querySelector(".image-preview-overlay").getAnimations({ subtree: true }).some(animation => animation.playState === "running"));
        await capture("gallery-gif-preview");
        const crop = await evaluate(() => {
          const rect = document.querySelector(".image-preview-overlay img").getBoundingClientRect();
          return { x: Math.round(rect.x + rect.width / 2), y: Math.round(rect.y + rect.height / 2), width: 1, height: 1 };
        });
        const colors = new Set();
        const deadline = Date.now() + 5000;
        while (colors.size < 2 && Date.now() < deadline) {
          await frames();
          const frame = await win.webContents.capturePage(crop);
          const [blue, green, red] = frame.toBitmap();
          // Check actual GIF colors, not a difference caused by the overlay fade.
          const color = blue > 250 && red < 5 && green < 5 ? "blue"
            : red > 250 && blue < 5 && Math.abs(green - 165) < 5 ? "orange" : undefined;
          if (color) {
            colors.add(color);
            fs.writeFileSync(path.join(output, `gif-frame-${color}.png`), frame.toPNG());
          }
        }
        assert.equal(colors.size, 2, "GIF preview must display both animated frames");
        await evaluate(() => document.querySelector(".image-preview-navigation button:last-child").click());
        await until(() => document.querySelector(".image-preview-overlay img")?.naturalWidth === 300);
        await until(() => !document.querySelector(".image-preview-overlay").getAnimations({ subtree: true }).some(animation => animation.playState === "running"));
        await capture("gallery-next-image");
        win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Escape" });
        await until(() => !document.querySelector(".image-preview-overlay"));
      }
      win.destroy();
    }
  }
  assert.ok(results.every(result => result.shifted <= 1 && result.warm.every(frame => Math.abs(frame.markerY - result.after.markerY) <= 1)),
    `Image completion must not move reading content: ${results.filter(result => result.shifted > 1).map(result => `${result.name}=${result.shifted}px`).join(", ")}`);
  server.close(); app.exit(0);
}).catch(async error => {
  console.error(error);
  if (win && !win.isDestroyed()) await capture("failure");
  fs.writeFileSync(path.join(output, "failure.txt"), String(error.stack));
  server.close(); app.exit(1);
});
