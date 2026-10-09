// Exercises the production renderer and managed-file protocol with synthetic
// deliveries. The app-server bridge is supplied by the shared streaming fixture.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { createHash } = require("node:crypto");
const { app, BrowserWindow } = require("electron");
const { buildSync } = require("esbuild");

const desktopRoot = path.resolve(__dirname, "..");
const output = path.resolve(process.env.WUU_ARTIFACT_OUTPUT || path.join(desktopRoot, "out", "artifact-preview-e2e"));
const inspectionOnly = process.env.WUU_ARTIFACT_E2E_ONLY === "inspection";
fs.mkdirSync(output, { recursive: true });
const profile = fs.mkdtempSync(path.join(output, "profile-"));
app.setPath("userData", profile);
process.env.WUU_STREAM_E2E_CWD = output;
const protocolModule = path.join(output, "protocol.cjs");
buildSync({
  entryPoints: [path.join(desktopRoot, "src/main/renderableFileProtocol.ts")],
  outfile: protocolModule, bundle: true, platform: "node", format: "cjs", external: ["electron"],
});
const { registerRenderableFileScheme, registerRenderableFileProtocol } = require(protocolModule);
registerRenderableFileScheme();

const threadID = "thread-immediate-title-e2e";
const html = "<!doctype html><style>body{font:18px system-ui;padding:24px}p{line-height:1.6}</style><h1>Delivered report</h1>"
  + "<p>A saved snapshot beside the conversation.</p>".repeat(24)
  + "<script>document.body.dataset.scriptRan='yes'</script>";
const text = "Delivered text snapshot\n".repeat(100);
const fixtures = [
  { name: "A long delivered report name for checking tabs and preview actions.html", mime: "text/html", bytes: html },
  { name: "notes.txt", mime: "text/plain", bytes: text },
  ...["media-portrait.svg", "media-panorama.svg"].map(name => ({
    name, mime: "image/svg+xml", bytes: fs.readFileSync(path.join(desktopRoot, "dev/message-flow-reading", name), "utf8"),
  })),
];

function delivery(index) {
  const fixture = fixtures[index];
  const id = String(index + 1).padStart(32, "0");
  const sha256 = createHash("sha256").update(fixture.bytes).digest("hex");
  const directory = path.join(profile, "workspaces", "fixture", "sessions", threadID, "artifacts", id);
  fs.mkdirSync(directory, { recursive: true });
  fs.writeFileSync(path.join(directory, fixture.name), fixture.bytes);
  fs.writeFileSync(path.join(directory, ".artifact.json"), JSON.stringify({
    version: 1, id, thread_id: threadID, plugin_id: "fixture", name: fixture.name,
    sha256, size: Buffer.byteLength(fixture.bytes),
  }));
  return {
    id: `deliver-${index}`, type: "tool_call", name: "present_artifact", status: "completed",
    result_detail: { content: [{
      type: fixture.mime.startsWith("image/") ? "image" : "file", name: fixture.name, mime_type: fixture.mime,
      uri: `wuu-artifact://fixture/${threadID}/${id}/${encodeURIComponent(fixture.name)}?sha256=${sha256}`,
      artifact: { placement: fixture.mime.startsWith("image/") ? "inline" : "turn_end", ref: id, sha256, size_bytes: Buffer.byteLength(fixture.bytes) },
    }] },
  };
}

function emit(win, method, params) {
  win.webContents.send("test:server-event", { workdir: output, kind: "notification", message: { method, params } });
}
function evaluate(win, fn) { return win.webContents.executeJavaScript(`(${fn.toString()})()`, true); }
async function waitForValue(read, label) {
  const deadline = Date.now() + 8_000;
  while (Date.now() < deadline) {
    const result = await read();
    if (result) return result;
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
  throw new Error(`Timed out: ${label}`);
}
function waitFor(win, fn) { return waitForValue(() => evaluate(win, fn), fn.toString()); }
async function settle(win) {
  await evaluate(win, () => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}
async function complete(win, index) {
  const turn = { id: `delivery-turn-${index}`, status: "in_progress", items_view: "full", items: [
    { id: `user-${index}`, type: "user_message", status: "completed", text: `Deliver report ${index}` },
  ] };
  emit(win, "turn/started", { thread_id: threadID, turn });
  await settle(win);
  turn.items.push(delivery(index));
  turn.items.push({ id: `answer-${index}`, type: "agent_message", phase: "final_answer", terminal: true, status: "completed", text: "The requested file is ready." });
  for (const item of turn.items.slice(1)) emit(win, "item/completed", { thread_id: threadID, turn_id: turn.id, item });
  await settle(win);
  turn.status = "completed";
  emit(win, "turn/completed", { thread_id: threadID, turn });
  return turn;
}

app.whenReady().then(async () => {
  registerRenderableFileProtocol(profile);
  const win = new BrowserWindow({ width: 1440, height: 900, show: false,
    titleBarStyle: 'hiddenInset', webPreferences: {
    preload: path.join(__dirname, "streaming-e2e-preload.cjs"),
    contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
  } });
  win.webContents.on("console-message", (event) => {
    if (event.level === "error" && !event.message.startsWith("Blocked script execution")) console.error(event.message);
  });
  await win.loadFile(path.join(desktopRoot, "out/renderer/index.html"));
  // Hidden windows need focused-page emulation to paint keyboard focus rings.
  win.webContents.debugger.attach('1.3');
  await win.webContents.debugger.sendCommand('Emulation.setFocusEmulationEnabled', { enabled: true });
  await waitFor(win, () => Boolean(document.querySelector(".composer textarea")));
  await evaluate(win, () => {
    const input = document.querySelector(".composer textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "Preview delivered files");
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
  });
  await waitFor(win, () => document.querySelector(".conversation-width")?.textContent.includes("Preview delivered files"));
  if (!inspectionOnly) {
  const completed = await complete(win, 0);
  await waitFor(win, () => Boolean(document.querySelector(".artifact-preview-panel iframe")));
  const frame = await waitForValue(() => win.webContents.mainFrame.frames.find((frame) => frame.url.startsWith("wuu-artifact:")), "managed HTML frame");
  assert.ok(frame, "Managed HTML must load through the real protocol");
  assert.equal(await evaluate(win, async () => (await fetch(document.querySelector(".artifact-preview-panel iframe").src)).text()), html);
  await assert.rejects(frame.executeJavaScript("true"), "Delivered HTML scripts must remain blocked");

  for (const theme of ["light", "dark"]) {
    for (const size of [14, 20]) {
      await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--conversation-message-font-size','${size}px');window.dispatchEvent(new Event('wuu-content-size-change'))`);
      for (const width of [1440, 760]) {
        win.setContentSize(width, 900);
        await waitFor(win, () => {
          const toolbar = document.querySelector(".artifact-preview-toolbar")?.getBoundingClientRect();
          const body = document.querySelector(".artifact-preview-body")?.getBoundingClientRect();
          return toolbar && body && body.width > 100 && body.height > 100 && toolbar.bottom <= body.top + 1;
        });
        const geometry = await evaluate(win, () => {
          const panel = document.querySelector(".artifact-preview-panel").getBoundingClientRect();
          const actions = document.querySelector(".artifact-preview-actions").getBoundingClientRect();
          return { visible: actions.left >= panel.left && actions.right <= panel.right, panelWidth: panel.width };
        });
        assert.equal(geometry.visible, true, "Preview actions must remain within the panel");
        const image = await win.webContents.capturePage();
        fs.writeFileSync(path.join(output, `${theme}-${size}-${width}.png`), image.toPNG());
      }
    }
  }
  win.setContentSize(1440, 900);
  await evaluate(win, () => [...document.querySelectorAll(".artifact-preview-actions button")].at(-1).click());
  await waitFor(win, () => !document.querySelector(".artifact-preview-panel"));
  emit(win, "turn/completed", { thread_id: threadID, turn: completed });
  await settle(win);
  assert.equal(await evaluate(win, () => Boolean(document.querySelector(".artifact-preview-panel"))), false);
  await evaluate(win, () => document.querySelector('[data-wuu-component="turn-artifacts"] button').click());
  await waitFor(win, () => Boolean(document.querySelector(".artifact-preview-panel iframe")));
  await evaluate(win, () => [...document.querySelectorAll(".artifact-preview-actions button")].at(-1).click());
  await waitFor(win, () => !document.querySelector(".artifact-preview-panel"));
  await complete(win, 1);
  await waitFor(win, () => document.querySelector(".artifact-preview-text")?.textContent.startsWith("Delivered text snapshot"));
  await evaluate(win, () => [...document.querySelectorAll(".artifact-preview-actions button")].at(-1).click());
  await waitFor(win, () => !document.querySelector(".artifact-preview-panel"));

  // The docked preview is the panel's content, not a card inside it: it must
  // reach the panel edges and paint the same surface as the tab strip.
  const surfaceIndex = fixtures.length;
  fixtures.push({ name: "results.json", mime: "application/json", bytes: JSON.stringify({ passed: 61 }) });
  await complete(win, surfaceIndex);
  await waitFor(win, () => [...document.querySelectorAll('[data-wuu-component="turn-artifacts"] button')].some(button => button.textContent.includes("results.json")));
  await evaluate(win, () => [...document.querySelectorAll('[data-wuu-component="turn-artifacts"] button')].find(button => button.textContent.includes("results.json")).click());
  await waitFor(win, () => Boolean(document.querySelector(".artifact-preview-panel .artifact-preview-body > *")));
  for (const theme of ["light", "dark"]) {
    await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)}`);
    for (const width of [1440, 760]) {
      win.setContentSize(width, 900);
      await settle(win);
      // Panel, tab and theme changes animate geometry and colour; sample the
      // resting state.
      await evaluate(win, () => document.getAnimations()
        .filter((animation) => animation.effect?.getComputedTiming().endTime !== Infinity)
        .forEach((animation) => animation.finish()));
      await settle(win);
      const layout = await evaluate(win, () => {
        const box = (selector) => document.querySelector(selector).getBoundingClientRect();
        const panel = box(".workspace-right-panel");
        const host = box(".workspace-panel-body");
        const preview = box(".artifact-preview-panel");
        const toolbar = box(".artifact-preview-toolbar");
        const body = box(".artifact-preview-body");
        const tabbar = box(".workspace-panel-tabbar");
        return {
          flush: ["left", "right", "top", "bottom"].every((side) => Math.abs(preview[side] - host[side]) < 1),
          points: {
            tabbar: { x: panel.left + 4, y: tabbar.top + tabbar.height / 2 },
            toolbar: { x: toolbar.left + toolbar.width * 0.6, y: toolbar.top + 3 },
            body: { x: body.left + 4, y: body.top + 4 },
          },
          viewportWidth: innerWidth,
        };
      });
      assert.equal(layout.flush, true, "The docked preview must fill the panel without an inset");
      const image = await win.webContents.capturePage();
      const scale = image.getSize().width / layout.viewportWidth;
      const bitmap = image.toBitmap();
      const pixel = ({ x, y }) => {
        const offset = (Math.round(y * scale) * image.getSize().width + Math.round(x * scale)) * 4;
        return [...bitmap.subarray(offset, offset + 3)].join(",");
      };
      const colors = Object.fromEntries(Object.entries(layout.points).map(([name, point]) => [name, pixel(point)]));
      assert.equal(colors.toolbar, colors.tabbar, `Preview header must match the panel surface (${theme} ${width})`);
      assert.equal(colors.body, colors.tabbar, `Preview body must match the panel surface (${theme} ${width})`);
      fs.writeFileSync(path.join(output, `surface-${theme}-${width}.png`), image.toPNG());
    }
  }
  win.setContentSize(1440, 900);
  await win.webContents.executeJavaScript(`document.documentElement.dataset.theme="light"`);
  await evaluate(win, () => [...document.querySelectorAll(".artifact-preview-actions button")].at(-1).click());
  await waitFor(win, () => !document.querySelector(".artifact-preview-panel"));
  for (const index of [2, 3]) {
    await complete(win, index);
    await waitForValue(async () => await evaluate(win, () => document.querySelectorAll('.turn-artifact-inline-image img').length) === index - 1, "delivered image appears");
    await evaluate(win, () => {
      const images = document.querySelectorAll('.turn-artifact-inline-image img');
      images[images.length - 1].scrollIntoView({ block: "center" });
    });
    await waitFor(win, () => [...document.querySelectorAll('.turn-artifact-inline-image img')].every(image => image.complete && image.naturalWidth > 0));
    for (const width of [1440, 760, 430]) {
      win.setContentSize(width, 900);
      await settle(win);
      await waitFor(win, () => !document.documentElement.classList.contains('window-resizing') && !document.documentElement.classList.contains('layout-motion-active'));
      await settle(win);
      const geometry = await evaluate(win, () => [...document.querySelectorAll('.turn-artifact-inline-image img')].map(image => {
        const rect = image.getBoundingClientRect();
        const button = image.closest('button').getBoundingClientRect();
        const frame = image.closest('figure').getBoundingClientRect();
        return {
          source: image.src, width: rect.width, height: rect.height,
          buttonWidth: button.width, buttonHeight: button.height,
          frameWidth: frame.width, frameHeight: frame.height,
        };
      }));
      for (const image of geometry) {
        assert.ok(image.width > 0 && image.height > 0, "Delivered images must remain visible");
        assert.ok(Math.abs(image.buttonWidth - image.width) < 1 && Math.abs(image.buttonHeight - image.height) < 1, "The preview target must cover the entire reserved image frame");
        assert.ok(Math.abs(image.frameWidth - image.width) < 1 && Math.abs(image.frameHeight - image.height) < 1, "The image must stay inside its reserved message frame");
      }
      const source = geometry[geometry.length - 1].source;
      await evaluate(win, () => {
        const images = document.querySelectorAll('.turn-artifact-inline-image img');
        images[images.length - 1].closest('button').click();
      });
      await waitFor(win, () => Boolean(document.querySelector('.image-preview-image.loaded')));
      assert.equal(await evaluate(win, () => document.querySelector('.image-preview-image').src), source, 'Primary image clicks must still open the full viewer');
      assert.equal(await evaluate(win, () => Boolean(document.querySelector('.artifact-preview-panel'))), false);
      await evaluate(win, () => document.querySelector('.image-preview-toolbar .wuu-icon-x').closest('button').click());
      await waitFor(win, () => !document.querySelector('.image-preview-overlay'));
      await evaluate(win, () => [...document.querySelectorAll('[data-artifact-panel]')].at(-1).scrollIntoView({ block: 'center', behavior: 'instant' }));
      await settle(win);
      const panelPoint = await evaluate(win, () => {
        const rect = [...document.querySelectorAll('[data-artifact-panel]')].at(-1).getBoundingClientRect();
        return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) };
      });
      win.webContents.sendInputEvent({ type: 'mouseMove', ...panelPoint });
      await settle(win);
      const hit = await evaluate(win, () => {
        const button = [...document.querySelectorAll('[data-artifact-panel]')].at(-1);
        const rect = button.getBoundingClientRect();
        const target = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
        return { matches: button.contains(target), target: target?.outerHTML.slice(0, 300), x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2), opacity: getComputedStyle(button.closest('.turn-artifact-image-toolbar')).opacity };
      });
      assert.ok(hit.matches, `The side-panel control must receive pointer input: ${JSON.stringify(hit)}`);
      win.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, x: hit.x, y: hit.y });
      win.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, x: hit.x, y: hit.y });
      await waitFor(win, () => Boolean(document.querySelector('.artifact-preview-panel .artifact-preview-image')));
      assert.equal(await evaluate(win, () => Boolean(document.querySelector('.image-preview-overlay'))), false, 'The side-panel action must not also open the viewer');
      await waitFor(win, () => !document.querySelector('.artifact-preview-flight'));
      assert.equal(await evaluate(win, () => document.querySelector('.artifact-preview-image').src), source);
      assert.equal(await evaluate(win, () => {
        const image = document.querySelector('.artifact-preview-image').getBoundingClientRect();
        const body = document.querySelector('.artifact-preview-body').getBoundingClientRect();
        return image.left >= body.left - 1 && image.right <= body.right + 1 && image.top >= body.top - 1 && image.bottom <= body.bottom + 1;
      }), true, 'Portrait and panoramic images must fit completely inside the preview');
      for (const theme of ['light', 'dark']) {
        for (const size of [14, 20]) {
          await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--font-ui','${size}px')`);
          await settle(win);
          await evaluate(win, () => Promise.all(document.getAnimations()
            .filter(animation => Number.isFinite(animation.effect.getComputedTiming().endTime))
            .map(animation => animation.finished.catch(() => {}))));
          assert.equal(await evaluate(win, () => {
            const actions = document.querySelector('.artifact-preview-actions').getBoundingClientRect();
            const panel = document.querySelector('.artifact-preview-panel').getBoundingClientRect();
            return actions.left >= panel.left && actions.right <= panel.right;
          }), true, 'Image actions must remain inside the panel at larger text sizes');
          fs.writeFileSync(path.join(output, `image-panel-${index}-${theme}-${size}-${width}.png`), (await win.webContents.capturePage()).toPNG());
        }
      }
      await evaluate(win, () => document.querySelector('.artifact-preview-enlarge').click());
      await waitFor(win, () => Boolean(document.querySelector('.image-preview-image.loaded')));
      assert.equal(await evaluate(win, () => document.querySelector('.image-preview-image').src), source, "Enlarging must still open the complete original image");
      if (index === 2) assert.equal(await evaluate(win, () => document.querySelector('.image-preview-navigation')), null);
      await evaluate(win, () => document.querySelector('.image-preview-toolbar .wuu-icon-x').closest('button').click());
      await waitFor(win, () => !document.querySelector('.image-preview-overlay'));
      await evaluate(win, () => [...document.querySelectorAll('.artifact-preview-actions button')].at(-1).click());
      await waitFor(win, () => !document.querySelector('.artifact-preview-panel') && !document.querySelector('.artifact-preview-flight'));
    }
  }
  await evaluate(win, () => {
    const opener = document.querySelectorAll('[data-artifact-panel]')[1];
    opener.focus();
    opener.click();
  });
  await waitFor(win, () => Boolean(document.querySelector('.artifact-preview-enlarge')));
  await waitFor(win, () => !document.querySelector('.artifact-preview-flight'));
  await evaluate(win, () => { document.querySelector('.artifact-preview-enlarge').focus(); document.querySelector('.artifact-preview-enlarge').click(); });
  await waitFor(win, () => Boolean(document.querySelector('.image-preview-image.loaded')));
  assert.equal(await evaluate(win, () => document.querySelector('.image-preview-position').textContent), '2 / 2');
  await evaluate(win, () => {
    window.previewDialog = document.querySelector('.image-preview-overlay');
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
  });
  await waitFor(win, () => document.querySelector('.image-preview-position')?.textContent === '1 / 2' && Boolean(document.querySelector('.image-preview-image.loaded')));
  assert.equal(await evaluate(win, () => document.querySelector('.image-preview-overlay') === window.previewDialog), true);
  assert.equal(await evaluate(win, () => document.querySelector('.image-preview-image').src === document.querySelector('.turn-artifact-inline-image img').src), true);
  assert.equal(await evaluate(win, () => document.querySelector('.image-preview-navigation button').disabled), true);
  for (const theme of ['light', 'dark']) {
    for (const size of [14, 20]) {
      await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--font-ui','${size}px')`);
      for (const width of [1440, 760, 360]) {
        win.setContentSize(width, 900);
        await settle(win);
        const contained = await evaluate(win, () => [...document.querySelectorAll('.image-preview-toolbar button, .image-preview-position')].every(node => {
          const rect = node.getBoundingClientRect();
          const stage = document.querySelector('.image-preview-stage').getBoundingClientRect();
          const outsideStage = rect.bottom <= stage.top || rect.top >= stage.bottom;
          const outsideWindowControls = rect.left >= 80 || rect.top >= 40;
          return rect.left >= 0 && rect.right <= innerWidth && rect.bottom <= innerHeight && outsideStage && outsideWindowControls;
        }));
        assert.equal(contained, true, 'Preview navigation and actions must fit without overlapping the image stage');
        win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Tab' });
        win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Tab' });
        await settle(win);
        assert.equal(await evaluate(win, () => document.querySelector('.image-preview-overlay').contains(document.activeElement)), true);
        assert.equal(await evaluate(win, () => getComputedStyle(document.activeElement).outlineStyle !== 'none'), true, 'Keyboard focus must be visible');
        fs.writeFileSync(path.join(output, `gallery-${theme}-${size}-${width}.png`), (await win.webContents.capturePage()).toPNG());
      }
    }
  }
  await evaluate(win, () => document.querySelector('.image-preview-navigation button:last-child').click());
  await waitFor(win, () => document.querySelector('.image-preview-position')?.textContent === '2 / 2' && Boolean(document.querySelector('.image-preview-image.loaded')));
  assert.equal(await evaluate(win, () => document.querySelector('.image-preview-navigation button:last-child').disabled), true);
  await evaluate(win, () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })));
  await waitFor(win, () => !document.querySelector('.image-preview-overlay'));
  assert.equal(await evaluate(win, () => document.activeElement === document.querySelector('.artifact-preview-enlarge')), true);

  // Recovery contracts: closing during entry, resizing, and changing motion
  // preferences must leave both the card and the panel usable.
  win.setContentSize(1440, 900);
  await settle(win);
  for (let remaining = 8; remaining > 0 && await evaluate(win, () => Boolean(document.querySelector('.artifact-preview-panel'))); remaining -= 1) {
    await evaluate(win, () => [...document.querySelectorAll('.artifact-preview-actions button')].at(-1).click());
    await settle(win);
  }
  await waitFor(win, () => !document.querySelector('.artifact-preview-flight'));
  await evaluate(win, () => {
    document.documentElement.dataset.appearanceMotion = 'full';
    const opener = document.querySelectorAll('[data-artifact-panel]')[1];
    opener.scrollIntoView({ block: 'center' });
  });
  await settle(win);
  await evaluate(win, () => document.querySelectorAll('[data-artifact-panel]')[1].click());
  await waitFor(win, () => Boolean(document.querySelector('.artifact-preview-flight')));
  fs.writeFileSync(path.join(output, 'image-flight.png'), (await win.webContents.capturePage()).toPNG());
  await evaluate(win, () => [...document.querySelectorAll('.artifact-preview-actions button')].at(-1).click());
  await waitFor(win, () => !document.querySelector('.artifact-preview-panel') && !document.querySelector('.artifact-preview-flight'));
  assert.equal(await evaluate(win, () => getComputedStyle(document.querySelectorAll('.turn-artifact-inline-image img')[1]).visibility), 'visible');
  await evaluate(win, () => document.querySelectorAll('[data-artifact-panel]')[1].click());
  await waitFor(win, () => Boolean(document.querySelector('.artifact-preview-flight')));
  win.setContentSize(1000, 800);
  await waitFor(win, () => !document.querySelector('.artifact-preview-flight'));
  assert.equal(await evaluate(win, () => getComputedStyle(document.querySelector('.artifact-preview-image')).visibility), 'visible');
  await evaluate(win, () => [...document.querySelectorAll('.artifact-preview-actions button')].at(-1).click());
  await waitFor(win, () => !document.querySelector('.artifact-preview-panel') && !document.querySelector('.artifact-preview-flight'));
  await evaluate(win, () => {
    document.querySelectorAll('[data-artifact-panel]')[1].scrollIntoView({ block: 'center' });
    document.documentElement.dataset.appearanceMotion = 'reduce';
  });
  await settle(win);
  await evaluate(win, () => document.querySelectorAll('[data-artifact-panel]')[1].click());
  await waitFor(win, () => Boolean(document.querySelector('.artifact-preview-image')));
  assert.equal(await evaluate(win, () => Boolean(document.querySelector('.artifact-preview-flight'))), false);
  await evaluate(win, () => [...document.querySelectorAll('.artifact-preview-actions button')].at(-1).click());
  await waitFor(win, () => !document.querySelector('.artifact-preview-panel'));
  await evaluate(win, () => delete document.documentElement.dataset.appearanceMotion);
  await evaluate(win, () => document.querySelectorAll('[data-artifact-panel]')[1].click());
  await waitFor(win, () => Boolean(document.querySelector('.artifact-preview-flight')));
  await evaluate(win, () => document.documentElement.dataset.appearanceMotion = 'reduce');
  await waitFor(win, () => !document.querySelector('.artifact-preview-flight'));
  assert.equal(await evaluate(win, () => getComputedStyle(document.querySelector('.artifact-preview-image')).visibility), 'visible');
  await evaluate(win, () => [...document.querySelectorAll('.artifact-preview-actions button')].at(-1).click());
  await waitFor(win, () => !document.querySelector('.artifact-preview-panel'));
  await evaluate(win, () => delete document.documentElement.dataset.appearanceMotion);
  await evaluate(win, () => document.querySelectorAll('[data-artifact-panel]')[1].click());
  await waitFor(win, () => Boolean(document.querySelector('.artifact-preview-flight')));
  await evaluate(win, () => document.querySelector('.artifact-preview-enlarge').click());
  await waitFor(win, () => Boolean(document.querySelector('.image-preview-image.loaded')));
  assert.equal(await evaluate(win, () => Boolean(document.querySelector('.artifact-preview-flight'))), false, 'Enlarging during entry must settle the flight');
  await evaluate(win, () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })));
  await waitFor(win, () => !document.querySelector('.image-preview-overlay'));
  await evaluate(win, () => document.querySelector('button[aria-label="Close right sidebar"]').click());
  await waitFor(win, () => document.querySelector('.workspace-right-panel')?.getAttribute('aria-hidden') === 'true' && !document.querySelector('.artifact-preview-flight'));
  assert.equal(await evaluate(win, () => getComputedStyle(document.querySelectorAll('.turn-artifact-inline-image img')[1]).visibility), 'visible');
  await evaluate(win, () => document.querySelectorAll('[data-artifact-panel]')[1].click());
  await waitFor(win, () => Boolean(document.querySelector('.artifact-preview-panel')));
  await waitFor(win, () => !document.querySelector('.artifact-preview-flight'));
  await evaluate(win, () => document.querySelector('.workspace-tool-tab-close').click());
  await waitFor(win, () => !document.querySelector('.artifact-preview-panel') && !document.querySelector('.artifact-preview-flight'));
  fs.writeFileSync(path.join(output, 'motion-evidence.json'), JSON.stringify({ rapidClose: true, resize: true, reducedMotion: true, reducedMotionDuringEntry: true, enlargeDuringEntry: true, panelClose: true, tabClose: true }, null, 2));

  // Real pointer input protects hit testing, including the toolbar's blank margin.
  for (const target of ['stage', 'toolbar']) {
    await evaluate(win, () => document.querySelectorAll('[data-artifact-panel]')[1].click());
    await waitFor(win, () => Boolean(document.querySelector('.artifact-preview-enlarge')));
    await evaluate(win, () => document.querySelector('.artifact-preview-enlarge').click());
    await waitFor(win, () => Boolean(document.querySelector('.image-preview-image.loaded')));
    const imagePoint = await evaluate(win, () => {
      const rect = document.querySelector('.image-preview-image').getBoundingClientRect();
      return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) };
    });
    win.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...imagePoint });
    win.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...imagePoint });
    await settle(win);
    assert.equal(await evaluate(win, () => Boolean(document.querySelector('.image-preview-overlay'))), true, 'Clicking the image must not dismiss it');
    const point = await win.webContents.executeJavaScript(`(() => {
      const rect = document.querySelector('.image-preview-${target}').getBoundingClientRect();
      return { x: Math.round(rect.left + 2), y: Math.round(rect.top + rect.height / 2) };
    })()`);
    win.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...point });
    win.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...point });
    await waitFor(win, () => !document.querySelector('.image-preview-overlay'));
    await evaluate(win, () => [...document.querySelectorAll('.artifact-preview-actions button')].at(-1).click());
    await waitFor(win, () => !document.querySelector('.artifact-preview-panel') && !document.querySelector('.artifact-preview-flight'));
  }

  // The per-image panel action must coexist with the carousel/grid control.
  const controlsTurn = { id: 'image-controls-turn', status: 'completed', items_view: 'full', items: [
    { id: 'controls-user', type: 'user_message', status: 'completed', text: 'Compare these two images.' },
    { ...delivery(2), id: 'controls-images', result_detail: { content: [delivery(2).result_detail.content[0], delivery(3).result_detail.content[0]] } },
    { id: 'controls-answer', type: 'agent_message', phase: 'final_answer', terminal: true, status: 'completed', text: 'Two views of the landscape.' },
  ] };
  emit(win, 'turn/started', { thread_id: threadID, turn: { ...controlsTurn, status: 'in_progress' } });
  emit(win, 'turn/completed', { thread_id: threadID, turn: controlsTurn });
  await waitFor(win, () => document.querySelectorAll('[data-turn-id="image-controls-turn"] [data-artifact-panel]').length === 2);
  for (const theme of ['light', 'dark']) {
    for (const size of [14, 20]) {
      for (const width of [1440, 430]) {
        win.setContentSize(width, 900);
        await settle(win);
        await waitFor(win, () => !document.documentElement.classList.contains('window-resizing') && !document.documentElement.classList.contains('layout-motion-active'));
        await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--font-ui','${size}px')`);
        for (const tiled of [false, true]) {
          await evaluate(win, () => {
            const group = document.querySelector('[data-turn-id="image-controls-turn"]');
            group.querySelector('[data-artifact-panel]').scrollIntoView({ block: 'center', behavior: 'instant' });
            group.querySelector('.turn-artifact-image-preview > button').focus();
          });
          win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Tab' });
          win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Tab' });
          await settle(win);
          await evaluate(win, () => Promise.all(document.getAnimations()
            .filter(animation => Number.isFinite(animation.effect.getComputedTiming().endTime))
            .map(animation => animation.finished.catch(() => {}))));
          const controls = await evaluate(win, () => {
            const group = document.querySelector('[data-turn-id="image-controls-turn"]');
            const button = group.querySelector('[data-artifact-panel]');
            const rect = button.getBoundingClientRect();
            const toggle = group.querySelector('[data-gallery-toggle]').getBoundingClientRect();
            const picture = button.closest('.turn-artifact-image-preview').getBoundingClientRect();
            return { visible: Number(getComputedStyle(button.closest('.turn-artifact-image-toolbar')).opacity) === 1 && rect.width >= 24 && rect.height >= 24,
              contained: rect.left >= picture.left && rect.right <= picture.right && rect.top >= picture.top && rect.bottom <= picture.bottom,
              separate: rect.bottom <= toggle.top || rect.top >= toggle.bottom || rect.right <= toggle.left || rect.left >= toggle.right,
              focus: document.activeElement === button };
          });
          assert.deepEqual(controls, { visible: true, contained: true, separate: true, focus: true });
          fs.writeFileSync(path.join(output, `image-actions-${theme}-${size}-${width}-${tiled ? 'grid' : 'carousel'}.png`), (await win.webContents.capturePage()).toPNG());
          await evaluate(win, () => document.querySelector('[data-turn-id="image-controls-turn"] [data-gallery-toggle]').click());
        }
      }
    }
  }

  // Record synthetic frames in Chromium so this test needs no binary fixture,
  // external encoder, network media, or access to the user's files.
  const videoBytes = Buffer.from(await evaluate(win, async () => {
    const canvas = document.createElement('canvas');
    canvas.width = 640; canvas.height = 360;
    const context = canvas.getContext('2d');
    const stream = canvas.captureStream(12);
    const recorder = new MediaRecorder(stream, { mimeType: 'video/webm;codecs=vp8' });
    const chunks = [];
    recorder.ondataavailable = event => chunks.push(event.data);
    const stopped = new Promise(resolve => { recorder.onstop = resolve; });
    recorder.start();
    const start = performance.now();
    await new Promise(resolve => {
      function draw(now) {
        context.fillStyle = '#263e50'; context.fillRect(0, 0, 640, 360);
        context.fillStyle = '#edf1e9'; context.font = '32px sans-serif';
        context.fillText('Video preview', 40, 100);
        context.fillStyle = '#97cbb5';
        context.fillRect(40, 180, 40 + (now - start) / 6, 60);
        if (now - start < 2400) requestAnimationFrame(draw);
        else resolve();
      }
      requestAnimationFrame(draw);
    });
    recorder.stop(); await stopped;
    stream.getTracks().forEach(track => track.stop());
    return Array.from(new Uint8Array(await new Blob(chunks).arrayBuffer()));
  }));
  const videoIndex = fixtures.length;
  fixtures.push({ name: 'A long delivered video name for checking playback in the sidebar.webm', mime: 'video/webm', bytes: videoBytes });
  win.setContentSize(1440, 900);
  await complete(win, videoIndex);
  await waitFor(win, () => [...document.querySelectorAll('[data-wuu-component="turn-artifacts"] button')].some(button => button.textContent.includes('.webm')));
  await evaluate(win, () => [...document.querySelectorAll('[data-wuu-component="turn-artifacts"] button')].find(button => button.textContent.includes('.webm')).click());
  await waitFor(win, () => document.querySelector('.artifact-preview-panel video')?.readyState >= 2);
  assert.equal(await evaluate(win, () => document.querySelector('.artifact-preview-panel video').paused), true, 'Opening a video must not autoplay');
  await evaluate(win, async () => { await document.querySelector('.artifact-preview-panel video').play(); });
  await waitFor(win, () => document.querySelector('.artifact-preview-panel video')?.currentTime > 0.2);
  await evaluate(win, () => document.querySelector('.artifact-preview-panel video').pause());
  await evaluate(win, async () => {
    const video = document.querySelector('.artifact-preview-panel video');
    const seeked = new Promise(resolve => video.addEventListener('seeked', resolve, { once: true }));
    video.currentTime = 1;
    await seeked;
  });
  assert.ok(await evaluate(win, () => Math.abs(document.querySelector('.artifact-preview-panel video').currentTime - 1) < 0.1), 'Video seeking must reach the requested position');
  const videoURL = await evaluate(win, () => document.querySelector('.artifact-preview-panel video').src);
  const range = await evaluate(win, async () => {
    const response = await fetch(document.querySelector('.artifact-preview-panel video').src, { headers: { Range: 'bytes=2-5' } });
    return { status: response.status, type: response.headers.get('content-type'), bytes: Array.from(new Uint8Array(await response.arrayBuffer())) };
  });
  assert.deepEqual(range, { status: 206, type: 'video/webm', bytes: [...videoBytes.subarray(2, 6)] });

  for (const theme of ['light', 'dark']) {
    for (const size of [14, 20]) {
      await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--font-ui','${size}px')`);
      for (const width of [1440, 760]) {
        win.setContentSize(width, 900);
        await settle(win);
        assert.equal(await evaluate(win, () => {
          const video = document.querySelector('.artifact-preview-panel video').getBoundingClientRect();
          const body = document.querySelector('.artifact-preview-body').getBoundingClientRect();
          const toolbar = document.querySelector('.artifact-preview-toolbar').getBoundingClientRect();
          return video.width > 100 && video.height > 100 && video.left >= body.left
            && video.right <= body.right + 1 && video.bottom <= body.bottom + 1 && video.top >= toolbar.bottom;
        }), true, 'Player must fit the panel below its toolbar');
        fs.writeFileSync(path.join(output, `video-${theme}-${size}-${width}.png`), (await win.webContents.capturePage()).toPNG());
      }
    }
  }
  await evaluate(win, async () => {
    window.closedVideo = document.querySelector('.artifact-preview-panel video');
    await window.closedVideo.play();
    [...document.querySelectorAll(".artifact-preview-actions button")].at(-1).click();
  });
  await waitFor(win, () => !document.querySelector('.artifact-preview-panel'));
  await waitFor(win, () => window.closedVideo.paused);
  await evaluate(win, () => [...document.querySelectorAll('[data-wuu-component="turn-artifacts"] button')].find(button => button.textContent.includes('.webm')).click());
  await waitFor(win, () => document.querySelector('.artifact-preview-panel video')?.readyState >= 2);
  assert.equal(await evaluate(win, () => document.querySelector('.artifact-preview-panel video').src), videoURL, 'Reopening must use the same delivered snapshot');

  // Exercise the local-file scheme and its CSP as well as managed deliveries.
  const localVideo = path.join(output, 'local-preview.webm');
  fs.writeFileSync(localVideo, videoBytes);
  await win.webContents.executeJavaScript(`document.querySelector('.artifact-preview-panel video').src=${JSON.stringify(`wuu-file://local/${Buffer.from(localVideo).toString('base64url')}`)}`);
  await waitFor(win, () => document.querySelector('.artifact-preview-panel video')?.readyState >= 2);
  await evaluate(win, async () => { await document.querySelector('.artifact-preview-panel video').play(); });
  await waitFor(win, () => document.querySelector('.artifact-preview-panel video')?.currentTime > 0.2);
  await evaluate(win, () => document.querySelector('.artifact-preview-panel video').pause());

  const invalidIndex = fixtures.length;
  fixtures.push({ name: 'unplayable.webm', mime: 'video/webm', bytes: 'not a video' });
  await complete(win, invalidIndex);
  await waitFor(win, () => [...document.querySelectorAll('[data-wuu-component="turn-artifacts"] button')].some(button => button.textContent.includes('unplayable.webm')));
  await evaluate(win, () => [...document.querySelectorAll('[data-wuu-component="turn-artifacts"] button')].find(button => button.textContent.includes('unplayable.webm')).click());
  await waitFor(win, () => Boolean(document.querySelector('.video-preview [role="alert"]')));
  assert.ok(await evaluate(win, () => document.querySelector('.artifact-preview-actions button')?.getAttribute('aria-label')), 'Download remains available for an unplayable file');
  fs.writeFileSync(path.join(output, 'video-error.png'), (await win.webContents.capturePage()).toPNG());

  // Closing the active preview reveals the next tab until all previews close.
  for (let remaining = 10; remaining > 0 && await evaluate(win, () => Boolean(document.querySelector('.artifact-preview-panel'))); remaining--) {
    await evaluate(win, () => [...document.querySelectorAll('.artifact-preview-actions button')].at(-1).click());
    await settle(win);
  }
  await waitFor(win, () => !document.querySelector('.artifact-preview-panel'));
  await evaluate(win, () => document.querySelector('.environment-panel-close-row button')?.click());
  await waitFor(win, () => !document.querySelector('.environment-panel'));
  }
  const inspectionTurn = { id: 'image-inspection-turn', status: 'completed', items_view: 'full', items: [
    { id: 'inspection-user', type: 'user_message', status: 'completed', text: 'Inspect images and deliver the result.' },
    ...['read_file', 'run_code', 'process'].map((name, index) => ({
      id: `inspection-${index}`, type: 'tool_call', name, status: 'completed',
      result_detail: { content: [{
        type: 'image', mime_type: 'image/svg+xml', uri: delivery(2).result_detail.content[0].uri,
        name: index === 0 ? 'A very long inspected screenshot filename for checking narrow windows and large fonts.svg' : `${name}-inspection.svg`,
      }] },
    })),
    { ...delivery(3), id: 'inspection-delivery' },
    { id: 'inspection-answer', type: 'agent_message', status: 'completed', text: 'The requested image is ready.' },
  ] };
  emit(win, 'turn/started', { thread_id: threadID, turn: { ...inspectionTurn, status: 'in_progress', items: [] } });
  for (const item of inspectionTurn.items) emit(win, 'item/completed', { thread_id: threadID, turn_id: inspectionTurn.id, item });
  emit(win, 'turn/completed', { thread_id: threadID, turn: inspectionTurn });
  await waitFor(win, () => document.querySelectorAll('[data-turn-id="image-inspection-turn"] .turn-process-entry .process-surface-fold').length === 1);
  const inspectionEvidence = [];
  for (const theme of ['light', 'dark']) {
    for (const size of [14, 20]) {
      await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--conversation-message-font-size','${size}px');document.documentElement.style.setProperty('--appearance-scale','${size / 14}');window.dispatchEvent(new Event('wuu-content-size-change'))`);
      for (const width of [1440, 760]) {
        win.setContentSize(width, 900);
        await settle(win);
        assert.equal(await evaluate(win, () => document.querySelectorAll('[data-turn-id="image-inspection-turn"] .turn-artifact-inline-list img').length), 1, 'Only the explicitly delivered image appears before inspection is expanded');
        await evaluate(win, () => document.querySelector('.environment-panel-close-row button')?.click());
        await waitFor(win, () => !document.querySelector('.environment-panel'));
        await evaluate(win, () => document.querySelector('[data-turn-id="image-inspection-turn"] .turn-process-entry .process-surface-fold > summary').scrollIntoView({ block: 'center' }));
        await settle(win);
        const geometry = await evaluate(win, () => [...document.querySelectorAll('[data-turn-id="image-inspection-turn"] .turn-process-entry .process-surface-fold > summary')].map(summary => {
          const rect = summary.getBoundingClientRect();
          return { left: rect.left, right: rect.right, width: innerWidth, height: rect.height };
        }));
        assert.ok(geometry.every(row => row.left >= 0 && row.right <= row.width && row.height > 0), 'Inspection rows must fit the reading area');
        fs.writeFileSync(path.join(output, `inspection-collapsed-${theme}-${size}-${width}.png`), (await win.webContents.capturePage()).toPNG());
        await evaluate(win, () => {
          const summary = document.querySelector('[data-turn-id="image-inspection-turn"] .turn-process-entry .process-surface-fold > summary');
          summary.focus();
        });
        await win.webContents.debugger.sendCommand('Input.dispatchKeyEvent', { type: 'keyDown', key: ' ', code: 'Space', windowsVirtualKeyCode: 32 });
        await win.webContents.debugger.sendCommand('Input.dispatchKeyEvent', { type: 'keyUp', key: ' ', code: 'Space', windowsVirtualKeyCode: 32 });
        await waitFor(win, () => {
          const fold = document.querySelector('[data-turn-id="image-inspection-turn"] .turn-process-entry .process-surface-fold');
          const image = fold.querySelector('img');
          return fold.open && image?.complete && image.naturalWidth > 0;
        });
        assert.equal(await evaluate(win, () => document.querySelectorAll('[data-turn-id="image-inspection-turn"] .turn-artifact-inline-list img').length), 4);
        await evaluate(win, () => Promise.all(document.querySelector('[data-turn-id="image-inspection-turn"]').getAnimations({ subtree: true })
          .filter(animation => Number.isFinite(animation.effect.getComputedTiming().endTime))
          .map(animation => animation.finished.catch(() => {}))));
        await settle(win);
        fs.writeFileSync(path.join(output, `inspection-expanded-${theme}-${size}-${width}.png`), (await win.webContents.capturePage()).toPNG());
        await evaluate(win, () => {
          const opener = document.querySelector('[data-turn-id="image-inspection-turn"] .turn-process-entry .process-surface-body .turn-artifact-inline-list button');
          opener.focus();
          opener.click();
        });
        await waitFor(win, () => Boolean(document.querySelector('.image-preview-image.loaded')));
        await evaluate(win, () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })));
        await waitFor(win, () => !document.querySelector('.image-preview-overlay'));
        assert.equal(await evaluate(win, () => document.activeElement === document.querySelector('[data-turn-id="image-inspection-turn"] .turn-process-entry .process-surface-body .turn-artifact-inline-list button')), true);
        await evaluate(win, () => document.querySelector('[data-turn-id="image-inspection-turn"] .turn-process-entry .process-surface-fold > summary').click());
        await waitFor(win, () => !document.querySelector('[data-turn-id="image-inspection-turn"] .turn-process-entry .process-surface-fold').open);
        inspectionEvidence.push({ theme, size, width, geometry, collapsedImages: 1, expandedImages: 4 });
      }
    }
  }
  fs.writeFileSync(path.join(output, 'inspection-evidence.json'), JSON.stringify(inspectionEvidence, null, 2));
  console.log('Image inspection e2e passed: reads, PTC and background results collapsed, explicit delivery visible, expand/collapse, enlarge, focus restoration, light/dark, 14/20px, wide/narrow.');
  if (!inspectionOnly) {
  console.log('Video preview e2e passed: managed/local playback, no autoplay, seeking, range bytes, card reopen, failure fallback, light/dark, 14/20px, wide/narrow layout.');
  console.log("Artifact preview e2e passed: primary image viewer, separate panel action, completion, managed HTML/text/images, sandbox, dismissal, manual reopen, light/dark, 14/20px, wide/narrow geometry, portrait/panorama fit, gallery navigation, boundaries and focus restoration.");
  }
  win.destroy();
  app.exit(0);
}).catch((error) => { console.error(error); app.exit(1); });
