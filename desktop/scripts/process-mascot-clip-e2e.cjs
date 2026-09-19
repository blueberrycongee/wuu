// Rendered check for the process mascot's paint room.
//
// The live process row places its mascot's painted silhouette on the row's left
// edge, which is also where the fold body's clip box ends. This check renders a
// live turn, measures the painted ink of that mascot with the shipped styles,
// and fails when the clip boxes shave the silhouette:
//   * the fold wrappers must reserve `--process-mascot-room` left of the row,
// * paint must stay inside the tightmost clip edge for the largest morph inset,
// * removing the clipping ancestors must not change the measured silhouette,
//   which is what proves nothing was cut rather than merely looking aligned.
// Screenshots are written to out/e2e/process-mascot-clip for inspection.
// The harness renders the app's default light theme; the invariant it checks is
// geometric, so it does not vary by theme.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const rendererHtml = path.join(desktopRoot, "out", "renderer", "index.html");
const preload = path.join(__dirname, "streaming-e2e-preload.cjs");
const evidenceDir = path.join(desktopRoot, "out", "e2e", "process-mascot-clip");

process.env.WUU_STREAM_E2E_CWD = repoRoot;
app.commandLine.appendSwitch("disable-gpu");
app.commandLine.appendSwitch("disable-software-rasterizer");

// Extra left shifts applied to the mascot on top of the stylesheet's own
// per-morph inset. 12px is the largest inset (`bang`), so the reserved room has
// to absorb it on top of the art's own paint offset.
const STRESS_SHIFTS = [0, 8, 12];
const INK_PAD = 8;
const INK_TOLERANCE = 0.75;

app.whenReady().then(run).catch(fail);

async function run() {
  assert.ok(fs.existsSync(rendererHtml), "Renderer build is missing. Run npm run build first.");
  assert.ok(fs.existsSync(preload), "Streaming E2E preload is missing.");
  fs.mkdirSync(evidenceDir, { recursive: true });

  const win = new BrowserWindow({
    width: 1100,
    height: 820,
    show: false,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      preload,
      sandbox: false,
      backgroundThrottling: false,
    },
  });
  win.webContents.on("render-process-gone", (_event, details) => {
    fail(new Error(`Renderer process exited: ${details.reason}`));
  });
  win.webContents.on("console-message", (event) => {
    if ((event.level ?? 0) >= 2) {
      console.error(`renderer console: ${event.message ?? String(event)}`);
    }
  });

  await loadFile(win, rendererHtml);
  await waitFor(win, () => Boolean(document.querySelector(".conversation-pane")), 5000);
  await waitFor(win, () => Boolean(document.querySelector(".composer textarea")), 5000);
  const threadID = await startActiveThread(win);

  // One live turn: an answer paragraph, then a running command tool. The tool
  // row is the latest gray entry, so its ProcessSurface renders the live mascot.
  const turnID = "turn-process-mascot-clip-e2e";
  emitNotification(win, "turn/started", {
    thread_id: threadID,
    turn: {
      id: turnID,
      items: [{ id: "user-process-mascot-clip-e2e", type: "user_message", status: "completed", text: "Process mascot clip e2e seed." }],
      items_view: "full",
      status: "in_progress",
      started_at: new Date().toISOString(),
    },
  });
  emitNotification(win, "item/started", {
    thread_id: threadID,
    turn_id: turnID,
    item: { id: "answer-process-mascot-clip-e2e", type: "agent_message", phase: "final_answer", status: "in_progress", text: "" },
  });
  emitNotification(win, "item/agentMessage/delta", {
    thread_id: threadID,
    turn_id: turnID,
    item_id: "answer-process-mascot-clip-e2e",
    delta: "Mascot paint room check keeps the row text and the ball column aligned.",
  });
  await delay(400);
  emitNotification(win, "item/started", {
    thread_id: threadID,
    turn_id: turnID,
    item: {
      id: "tool-process-mascot-clip-e2e",
      type: "tool_call",
      status: "in_progress",
      name: "bash",
      capability: "command.bash",
      arguments: JSON.stringify({ command: "wc -l desktop/src/renderer/AppSidebar.tsx" }),
    },
  });
  await waitFor(win, () => Boolean(document.querySelector("svg.process-surface-blobatar")), 5000);
  await delay(500);

  const viewport = await evaluate(win, () => ({ width: window.innerWidth, height: window.innerHeight }));
  const geometry = await evaluate(win, mascotGeometry);
  console.log(
    `mascot box ${geometry.mascotBox.width}x${geometry.mascotBox.height} at ${geometry.mascotBox.left}; ` +
      `clip edge ${geometry.clipEdge} (room ${geometry.room}); row ${geometry.rowLeft}; text ${geometry.textLeft}`,
  );

  // The row's text column keeps its own position: the reserved room may not
  // push the row, the text, or the mascot slot sideways.
  assert.ok(geometry.room > 0, "No --process-mascot-room token is defined.");
  assert.ok(
    Math.abs(geometry.rowLeft - (geometry.clipEdge + geometry.room)) <= 1,
    `Fold clip box should extend ${geometry.room}px left of the row. Row=${geometry.rowLeft} clip=${geometry.clipEdge}`,
  );
  const expectedTextLeft = geometry.rowLeft + geometry.mascotBox.width + geometry.mascotGap;
  assert.ok(
    Math.abs(geometry.textLeft - expectedTextLeft) <= 1,
    `Summary text moved: text=${geometry.textLeft} expected=${expectedTextLeft}`,
  );
  assert.ok(geometry.clipEdge <= geometry.mascotBox.left, "Mascot slot should start at the clip edge or later.");

  for (const shift of STRESS_SHIFTS) {
    // Painted geometry is measured without clipping, so the assertion holds
    // regardless of where the morph animation happens to be.
    const painted = await withMascotShift(win, shift, () => evaluate(win, paintGeometry));
    const state = shift === 0 ? "live" : `shift-${shift}`;
    console.log(
      `${state}: paint x ${painted.paint.left.toFixed(1)}..${painted.paint.right.toFixed(1)} ` +
        `y ${painted.paint.top.toFixed(1)}..${painted.paint.bottom.toFixed(1)} | tightest clip ${geometry.clipEdge} | elements ${painted.elements}`,
    );
    assert.ok(painted.elements > 0, `${state}: no painted mascot artwork found.`);
    for (const clipper of painted.clippers) {
      assert.ok(
        painted.paint.left >= clipper.edge - INK_TOLERANCE,
        `${state}: painted mascot crosses the ${clipper.name} clip edge (${painted.paint.left} < ${clipper.edge}).`,
      );
    }

    // Screenshots keep a pixel-level record of the same state, clipped and with
    // the clipping ancestors opened up; the two are expected to agree.
    const clipped = await captureInk(win, viewport, geometry, shift);
    fs.writeFileSync(path.join(evidenceDir, `clipped-shift-${shift}.png`), clipped.image.toPNG());
    const unclipped = await captureUnclippedInk(win, viewport, geometry, shift);
    fs.writeFileSync(path.join(evidenceDir, `unclipped-shift-${shift}.png`), unclipped.image.toPNG());
    console.log(
      `${state}: clipped ink ${formatBox(clipped.ink)} | unclipped ink ${formatBox(unclipped.ink)}`,
    );
    assert.ok(clipped.ink, `${state}: mascot painted nothing.`);
    assert.ok(unclipped.ink, `${state}: mascot painted nothing without clipping.`);
    assert.ok(
      clipped.ink.left >= geometry.clipEdge - INK_TOLERANCE,
      `${state}: paint starts left of the clip edge (${clipped.ink.left} < ${geometry.clipEdge}).`,
    );
    for (const side of ["left", "top", "right", "bottom"]) {
      assert.ok(
        Math.abs(clipped.ink[side] - unclipped.ink[side]) <= INK_TOLERANCE,
        `${state}: clip boxes shave the mascot on the ${side} (${clipped.ink[side]} vs ${unclipped.ink[side]}).`,
      );
    }
  }

  // Alignment intent: the painted silhouette, not the SVG box, meets the text
  // column's left edge instead of floating inside its slot. The reserved room
  // moves the clip edge left of that column, so the column is the row itself.
  const live = await captureInk(win, viewport, geometry, 0);
  assert.ok(
    live.ink.left >= geometry.rowLeft - 1 && live.ink.left <= geometry.rowLeft + 8,
    `Live mascot drifted off the text column: paint ${live.ink.left} column ${geometry.rowLeft}.`,
  );

  console.log("process mascot clip e2e passed");
  win.destroy();
  app.exit(0);
}

function mascotGeometry() {
  const mascot = document.querySelector("svg.process-surface-blobatar");
  if (!(mascot instanceof SVGElement)) return null;
  const box = mascot.getBoundingClientRect();
  const row = mascot.closest(".process-surface-row");
  const rowBox = row.getBoundingClientRect();
  const text = mascot.closest(".process-surface-summary-line")?.querySelector(".process-surface-summary-text");
  const gap = parseFloat(getComputedStyle(mascot).marginRight) || 0;
  const room = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--process-mascot-room")) || 0;
  const clippers = [];
  for (let node = mascot.parentElement; node; node = node.parentElement) {
    const style = getComputedStyle(node);
    if (style.overflowX === "visible" && style.overflowY === "visible") continue;
    const rect = node.getBoundingClientRect();
    clippers.push({
      name: node.className,
      edge: rect.left + (parseFloat(style.borderLeftWidth) || 0),
      overflowX: style.overflowX,
      overflowY: style.overflowY,
      tight: rect.left - box.left < 40,
    });
  }
  return {
    mascotBox: { left: box.left, top: box.top, width: box.width, height: box.height },
    rowLeft: rowBox.left,
    textLeft: text ? text.getBoundingClientRect().left : Number.NaN,
    mascotGap: gap,
    room,
    clipEdge: clippers.length ? clippers[0].edge : Number.NaN,
    clipperCount: clippers.length,
    tightClipperCount: clippers.filter((c) => c.tight).length,
  };
}

// Screen-space bounds of every painted mascot shape plus the tight clip edges.
// getBoundingClientRect() reports the painted geometry and ignores ancestor
// clipping, so comparing the two states whether anything is being cut without
// depending on where the morph animation currently is.
function paintGeometry() {
  const mascot = document.querySelector("svg.process-surface-blobatar");
  if (!(mascot instanceof SVGElement)) return null;
  let paint = null;
  let elements = 0;
  for (const shape of mascot.querySelectorAll("path, rect, circle, ellipse, polygon, polyline, line")) {
    const style = getComputedStyle(shape);
    if (style.display === "none" || style.visibility === "hidden") continue;
    const filled = style.fill !== "none" && Number(style.fillOpacity) > 0;
    const strokeWidth = parseFloat(style.strokeWidth) || 0;
    const stroked = style.stroke !== "none" && strokeWidth > 0;
    if (!filled && !stroked) continue;
    const rect = shape.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) continue;
    const pad = stroked ? strokeWidth / 2 : 0;
    const bounds = {
      left: rect.left - pad,
      top: rect.top - pad,
      right: rect.right + pad,
      bottom: rect.bottom + pad,
    };
    elements += 1;
    paint = paint
      ? {
          left: Math.min(paint.left, bounds.left),
          top: Math.min(paint.top, bounds.top),
          right: Math.max(paint.right, bounds.right),
          bottom: Math.max(paint.bottom, bounds.bottom),
        }
      : bounds;
  }
  const clippers = [];
  for (let node = mascot.parentElement; node; node = node.parentElement) {
    const style = getComputedStyle(node);
    if (style.overflowX === "visible" && style.overflowY === "visible") continue;
    const rect = node.getBoundingClientRect();
    if (rect.left - mascot.getBoundingClientRect().left >= 40) continue;
    clippers.push({
      name: String(node.className).split(" ")[0] || node.tagName,
      edge: rect.left + (parseFloat(style.borderLeftWidth) || 0),
    });
  }
  return { paint, elements, clippers };
}

async function withMascotShift(win, shift, body) {
  await evaluate(win, (shiftPx) => {
    const mascot = document.querySelector("svg.process-surface-blobatar");
    if (!(mascot instanceof SVGElement)) return false;
    mascot.style.translate = shiftPx === 0 ? "" : `-${shiftPx}px 0`;
    return true;
  }, shift);
  await delay(140);
  try {
    return await body();
  } finally {
    await evaluate(win, () => {
      const mascot = document.querySelector("svg.process-surface-blobatar");
      if (mascot instanceof SVGElement) mascot.style.translate = "";
    });
  }
}

async function captureInk(win, viewport, geometry, shift) {
  return withMascotShift(win, shift, async () => {
    const image = await win.webContents.capturePage();
    return { image, ink: measureInk(image, viewport, geometry) };
  });
}

async function captureUnclippedInk(win, viewport, geometry, shift) {
  const restored = await evaluate(win, () => {
    const mascot = document.querySelector("svg.process-surface-blobatar");
    const cleared = [];
    for (let node = mascot?.parentElement; node; node = node.parentElement) {
      const style = getComputedStyle(node);
      if (style.overflowX === "visible" && style.overflowY === "visible") continue;
      const rect = node.getBoundingClientRect();
      if (rect.left - mascot.getBoundingClientRect().left >= 40) continue;
      cleared.push(node.className);
      node.dataset.clipProbeOverflow = node.style.overflow;
      node.style.overflow = "visible";
    }
    return cleared;
  });
  try {
    assert.ok(restored.length > 0, "No clipping ancestor surrounds the mascot; the check would be meaningless.");
    return await withMascotShift(win, shift, async () => {
      const image = await win.webContents.capturePage();
      return { image, ink: measureInk(image, viewport, geometry) };
    });
  } finally {
    await evaluate(win, () => {
      for (const node of document.querySelectorAll("[data-clip-probe-overflow]")) {
        node.style.overflow = node.dataset.clipProbeOverflow ?? "";
        delete node.dataset.clipProbeOverflow;
      }
    });
  }
}

// Painted ink inside a window around the mascot box: any device pixel that
// departs from the conversation surface counts. The window is identical for the
// clipped and unclipped captures, so comparing the two bounding boxes isolates
// clipping from the artwork itself.
function measureInk(image, viewport, geometry) {
  const size = image.getSize();
  const scale = size.width / viewport.width;
  const bitmap = image.toBitmap();
  const left = geometry.mascotBox.left - INK_PAD;
  const top = geometry.mascotBox.top - INK_PAD;
  const right = geometry.mascotBox.left + geometry.mascotBox.width + INK_PAD;
  const bottom = geometry.mascotBox.top + geometry.mascotBox.height + INK_PAD;
  const x0 = Math.max(0, Math.floor(left * scale));
  const y0 = Math.max(0, Math.floor(top * scale));
  const x1 = Math.min(size.width, Math.ceil(right * scale));
  const y1 = Math.min(size.height, Math.ceil(bottom * scale));
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (let y = y0; y < y1; y += 1) {
    for (let x = x0; x < x1; x += 1) {
      const offset = (y * size.width + x) * 4;
      const b = bitmap[offset];
      const g = bitmap[offset + 1];
      const r = bitmap[offset + 2];
      const max = Math.max(b, g, r);
      const min = Math.min(b, g, r);
      if (max - min <= 25 && max >= 225) continue;
      if (x < minX) minX = x;
      if (y < minY) minY = y;
      if (x > maxX) maxX = x;
      if (y > maxY) maxY = y;
    }
  }
  if (minX === Infinity) return null;
  return {
    left: minX / scale,
    top: minY / scale,
    right: (maxX + 1) / scale,
    bottom: (maxY + 1) / scale,
    width: (maxX + 1 - minX) / scale,
    height: (maxY + 1 - minY) / scale,
  };
}

function formatBox(ink) {
  if (!ink) return "nothing";
  return `x ${(ink.left).toFixed(1)}..${ink.right.toFixed(1)} (w ${ink.width.toFixed(1)}) y ${ink.top.toFixed(1)}..${ink.bottom.toFixed(1)} (h ${ink.height.toFixed(1)})`;
}

async function startActiveThread(win) {
  const started = await waitFor(
    win,
    () => {
      const textarea = document.querySelector(".composer textarea");
      if (!(textarea instanceof HTMLTextAreaElement)) return false;
      textarea.focus();
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
      valueSetter?.call(textarea, "Process mascot clip e2e seed.");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      const enter = new KeyboardEvent("keydown", { key: "Enter", code: "Enter", bubbles: true, cancelable: true });
      textarea.dispatchEvent(enter);
      return enter.defaultPrevented;
    },
    3000,
  );
  assert.equal(started, true, "Composer should create the e2e thread.");
  await waitFor(
    win,
    () => (document.querySelector(".conversation-width")?.textContent ?? "").includes("Process mascot clip e2e seed."),
    3000,
  );
  return "thread-immediate-title-e2e";
}

function emitNotification(win, method, params) {
  win.webContents.send("test:server-event", {
    workdir: process.env.WUU_STREAM_E2E_CWD || process.cwd(),
    kind: "notification",
    message: { method, params },
  });
}

function loadFile(win, file) {
  return new Promise((resolve, reject) => {
    win.webContents.once("did-fail-load", (_event, _code, description) => reject(new Error(description)));
    win.webContents.once("did-finish-load", () => resolve());
    win.loadFile(file);
  });
}

async function waitFor(win, predicate, timeoutMs) {
  const started = Date.now();
  let lastValue;
  while (Date.now() - started < timeoutMs) {
    lastValue = await evaluate(win, predicate);
    if (lastValue) return lastValue;
    await delay(40);
  }
  throw new Error(`Timed out waiting for condition. Last value: ${JSON.stringify(lastValue)}`);
}

async function evaluate(win, fn, ...args) {
  return win.webContents.executeJavaScript(`(${fn.toString()})(${args.map((value) => JSON.stringify(value)).join(",")})`, true);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function fail(error) {
  console.error(error);
  app.exit(1);
}
