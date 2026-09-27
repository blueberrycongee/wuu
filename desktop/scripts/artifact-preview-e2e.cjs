// Exercises the production renderer and managed-file protocol with synthetic
// deliveries. The app-server bridge is supplied by the shared streaming fixture.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { createHash } = require("node:crypto");
const { app, BrowserWindow } = require("electron");
const { buildSync } = require("esbuild");

const desktopRoot = path.resolve(__dirname, "..");
const output = path.join(desktopRoot, "out", "artifact-preview-e2e");
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
  const win = new BrowserWindow({ width: 1440, height: 900, show: false, webPreferences: {
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
  await evaluate(win, () => document.querySelector(".artifact-preview-actions button:last-child").click());
  await waitFor(win, () => !document.querySelector(".artifact-preview-panel"));
  emit(win, "turn/completed", { thread_id: threadID, turn: completed });
  await settle(win);
  assert.equal(await evaluate(win, () => Boolean(document.querySelector(".artifact-preview-panel"))), false);
  await evaluate(win, () => document.querySelector('[data-wuu-component="turn-artifacts"] button').click());
  await waitFor(win, () => Boolean(document.querySelector(".artifact-preview-panel iframe")));
  await evaluate(win, () => document.querySelector(".artifact-preview-actions button:last-child").click());
  await waitFor(win, () => !document.querySelector(".artifact-preview-panel"));
  await complete(win, 1);
  await waitFor(win, () => document.querySelector(".artifact-preview-text")?.textContent.startsWith("Delivered text snapshot"));
  await evaluate(win, () => document.querySelector(".artifact-preview-actions button:last-child").click());
  await waitFor(win, () => !document.querySelector(".artifact-preview-panel"));
  for (const index of [2, 3]) {
    await complete(win, index);
    await waitForValue(async () => await evaluate(win, () => document.querySelectorAll('.turn-artifact-inline-image img').length) === index - 1, "delivered image appears");
    await evaluate(win, () => {
      const images = document.querySelectorAll('.turn-artifact-inline-image img');
      images[images.length - 1].scrollIntoView({ block: "center" });
    });
    await waitFor(win, () => [...document.querySelectorAll('.turn-artifact-inline-image img')].every(image => image.complete && image.naturalWidth > 0));
    for (const width of [1440, 760]) {
      win.setContentSize(width, 900);
      await settle(win);
      const geometry = await evaluate(win, () => [...document.querySelectorAll('.turn-artifact-inline-image img')].map(image => {
        const rect = image.getBoundingClientRect();
        const button = image.closest('button').getBoundingClientRect();
        const frame = image.closest('figure').getBoundingClientRect();
        return {
          source: image.src, width: rect.width, height: rect.height,
          ratio: image.naturalWidth / image.naturalHeight,
          buttonWidth: button.width, buttonHeight: button.height,
          frameWidth: frame.width, frameHeight: frame.height,
        };
      }));
      for (const image of geometry) {
        assert.ok(image.width > 0 && image.height > 0, "Delivered images must remain visible");
        assert.ok(Math.abs(image.width - image.height * image.ratio) < 1, "The image box must follow its original ratio without letterboxing");
        assert.ok(Math.abs(image.buttonWidth - image.width) < 1 && Math.abs(image.buttonHeight - image.height) < 1, "The preview target must hug the image rather than an empty frame");
        assert.ok(Math.abs(image.frameWidth - image.width) < 1 && Math.abs(image.frameHeight - image.height) < 1, "The message layout must not reserve white margins around the image");
      }
      const source = geometry[geometry.length - 1].source;
      await evaluate(win, () => {
        const images = document.querySelectorAll('.turn-artifact-inline-image img');
        images[images.length - 1].closest('button').click();
      });
      await waitFor(win, () => Boolean(document.querySelector('.image-preview-image.loaded')));
      assert.equal(await evaluate(win, () => document.querySelector('.image-preview-image').src), source, "Enlarging must still open the complete original image");
      if (index === 2) assert.equal(await evaluate(win, () => document.querySelector('.image-preview-navigation')), null);
      await evaluate(win, () => document.querySelector('.image-preview-toolbar .wuu-icon-x').closest('button').click());
      await waitFor(win, () => !document.querySelector('.image-preview-overlay'));
    }
  }
  await evaluate(win, () => {
    const opener = document.querySelectorAll('.turn-artifact-inline-image button')[1];
    opener.focus();
    opener.click();
  });
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
          return rect.left >= 0 && rect.right <= innerWidth && rect.bottom <= document.querySelector('.image-preview-stage').getBoundingClientRect().top;
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
  assert.equal(await evaluate(win, () => document.activeElement === document.querySelectorAll('.turn-artifact-inline-image button')[1]), true);

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
    document.querySelector('.artifact-preview-actions button:last-child').click();
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
  console.log('Video preview e2e passed: managed/local playback, no autoplay, seeking, range bytes, card reopen, failure fallback, light/dark, 14/20px, wide/narrow layout.');
  console.log("Artifact preview e2e passed: completion, managed HTML/text/images, sandbox, dismissal, manual reopen, light/dark, 14/20px, wide/narrow geometry, portrait/panorama fit, gallery navigation, boundaries and focus restoration.");
  win.destroy();
  app.exit(0);
}).catch((error) => { console.error(error); app.exit(1); });
