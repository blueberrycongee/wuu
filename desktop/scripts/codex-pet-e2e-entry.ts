// Runs the production pet window against synthetic spritesheets: moods,
// reactions, bubble rows, the quick panel (new conversation, reply, failure,
// blocked reply, keyboard), cursor following, sizes, themes, and edge layouts. Writes
// screenshots, a contact sheet, and results.json for review.
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import { app, BrowserWindow, nativeTheme, screen } from "electron";
import type { CodexPet, CodexPetHint, CodexPetSubmitResult, CodexPetSubmitTarget, CodexPetsSnapshot } from "../src/shared/protocol";
import { CODEX_PET_STATES } from "../src/main/codexPets";
import { CodexPetWindowManager, codexPetRenderedSpriteForSize } from "../src/main/codexPetWindow";
import { setMainLocale } from "../src/main/i18n";
import { registerRenderableFileProtocol, registerRenderableFileScheme } from "../src/main/renderableFileProtocol";
import { renderableFileURL } from "../src/main/renderableFileURLs";

const artifacts = process.env.WUU_CODEX_PET_E2E_ARTIFACTS ?? join(__dirname, "..", "out", "e2e", "codex-pet");
const scratch = mkdtempSync(join(tmpdir(), "wuu-codex-pet-e2e-"));
app.setPath("userData", join(scratch, "profile"));
registerRenderableFileScheme();
// Helper windows come and go; the run decides when the app exits.
app.on("window-all-closed", () => undefined);

const CELL_W = 192;
const CELL_H = 208;
type Check = { name: string; pass: boolean; detail?: unknown };
const results = {
  startedAt: new Date().toISOString(),
  electron: process.versions.electron,
  platform: process.platform,
  checks: [] as Check[],
  screenshots: [] as Array<{ file: string; theme: "light" | "dark"; note: string }>,
  unverified: [] as string[],
};
const delay = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

function check(name: string, pass: boolean, detail?: unknown): void {
  results.checks.push({ name, pass, ...(detail === undefined ? {} : { detail }) });
  console.log(`${pass ? "PASS" : "FAIL"} ${name}${pass || detail === undefined ? "" : ` ${JSON.stringify(detail)}`}`);
}

async function until<T>(read: () => Promise<T> | T, label: string, timeoutMs = 4000): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  let last: T | undefined;
  while (Date.now() < deadline) {
    last = await read();
    if (last) return last;
    await delay(20);
  }
  throw new Error(`Timed out waiting for ${label} (last=${JSON.stringify(last)})`);
}

// Draws an 8-column sheet with one hue per row and a frame number per cell,
// so a screenshot or a background-position read identifies row and frame.
// The two look rows draw eyes and an arrow toward their direction.
async function writeSheet(file: string, rows: number): Promise<void> {
  const painter = new BrowserWindow({ show: false, width: 200, height: 200 });
  await painter.loadURL("about:blank");
  const names = CODEX_PET_STATES.map((state) => state.id);
  const dataURL: string = await painter.webContents.executeJavaScript(`(() => {
    const W = ${CELL_W}, H = ${CELL_H}, rows = ${rows}, names = ${JSON.stringify(names)};
    const canvas = document.createElement('canvas');
    canvas.width = W * 8; canvas.height = H * rows;
    const g = canvas.getContext('2d');
    for (let row = 0; row < rows; row++) for (let col = 0; col < 8; col++) {
      const x = col * W, y = row * H, look = row >= 9;
      const bob = look ? 0 : Math.sin(col / 8 * Math.PI * 2) * 10;
      g.fillStyle = 'hsl(' + (row * 37 % 360) + ' 70% 58%)';
      g.beginPath(); g.ellipse(x + W / 2, y + 112 + bob, 62, 70, 0, 0, Math.PI * 2); g.fill();
      g.strokeStyle = 'rgba(0,0,0,.35)'; g.lineWidth = 4; g.stroke();
      let ex = 0, ey = 0;
      if (look) {
        const a = ((row - 9) * 8 + col) * 22.5 * Math.PI / 180;
        ex = Math.sin(a) * 12; ey = -Math.cos(a) * 12;
        g.strokeStyle = '#111'; g.lineWidth = 6; g.beginPath();
        g.moveTo(x + W / 2, y + 112); g.lineTo(x + W / 2 + ex * 5, y + 112 + ey * 5); g.stroke();
      }
      g.fillStyle = '#fff';
      for (const side of [-1, 1]) { g.beginPath(); g.arc(x + W / 2 + side * 24, y + 92 + bob, 15, 0, Math.PI * 2); g.fill(); }
      g.fillStyle = '#111';
      for (const side of [-1, 1]) { g.beginPath(); g.arc(x + W / 2 + side * 24 + ex * .6, y + 92 + bob + ey * .6, 7, 0, Math.PI * 2); g.fill(); }
      g.font = '600 26px system-ui'; g.textAlign = 'center'; g.fillStyle = '#111';
      g.fillText((look ? 'look ' + ((row - 9) * 8 + col) : (names[row] || row)) , x + W / 2, y + 160 + bob);
      g.font = '22px system-ui'; g.fillText(String(col), x + W / 2, y + 186 + bob);
    }
    return canvas.toDataURL('image/png');
  })()`);
  painter.destroy();
  writeFileSync(file, Buffer.from(dataURL.split(",")[1], "base64"));
}

function pet(id: string, name: string, sheet: string): CodexPet {
  return {
    id,
    display_name: name,
    description: "",
    manifest_path: join(scratch, id, "pet.json"),
    spritesheet_path: sheet,
    spritesheet_url: renderableFileURL(sheet),
  };
}

function snapshotFor(selected: CodexPet, pets: CodexPet[]): CodexPetsSnapshot {
  return { home: scratch, enabled: true, selected_id: selected.id, errors: [], pets };
}

const now = Date.now();
const hints: CodexPetHint[] = [
  {
    thread_id: "thread-review",
    title: "发布前检查",
    status: "needs_review",
    preview: "需要你确认：是否删除旧的迁移脚本？",
    attention: true,
    updated_at: now,
  },
  {
    thread_id: "thread-failed",
    title: "</script><img src=x onerror=alert(1)>",
    status: "failed",
    preview: "Rate limited by provider; retry in 30s",
    attention: true,
    updated_at: now - 1000,
  },
  {
    thread_id: "thread-running",
    title: "重构认证流程",
    status: "running",
    preview: "正在运行 npm test -- auth",
    attention: false,
    updated_at: now - 2000,
  },
];

async function main(): Promise<void> {
  await app.whenReady();
  mkdirSync(artifacts, { recursive: true });
  // Drop numbered screenshots from earlier runs so the contact sheet only shows this one.
  for (const file of readdirSync(artifacts)) {
    if (/^\d{2}-.*\.png$/.test(file)) rmSync(join(artifacts, file));
  }
  registerRenderableFileProtocol(scratch);
  setMainLocale("zh-CN");
  nativeTheme.themeSource = "light";

  const v2Sheet = join(scratch, "v2.png");
  const v1Sheet = join(scratch, "v1.png");
  await writeSheet(v2Sheet, 11);
  await writeSheet(v1Sheet, 9);
  const lookPet = pet("looker", "Looker", v2Sheet);
  const classicPet = pet("classic", "Classic", v1Sheet);

  // The real cursor is the user's; the pet reads a scripted one instead.
  const cursor = { x: -10_000, y: -10_000 };
  let cursorOverridden = true;
  try {
    (screen as unknown as { getCursorScreenPoint: () => Electron.Point }).getCursorScreenPoint = () => ({ ...cursor });
    cursorOverridden = screen.getCursorScreenPoint().x === cursor.x;
  } catch {
    cursorOverridden = false;
  }

  const submits: Array<{ text: string; target: CodexPetSubmitTarget; settle: (result: CodexPetSubmitResult) => void }> = [];
  const jumps: string[] = [];
  const manager = new CodexPetWindowManager({
    onClose: () => undefined,
    onJump: (threadID) => jumps.push(threadID),
    onSubmit: (text, target) => new Promise((settle) => submits.push({ text, target, settle })),
    onShowApp: () => undefined,
  });

  const before = new Set(BrowserWindow.getAllWindows());
  manager.sync(snapshotFor(lookPet, [lookPet, classicPet]));
  const petWindow = () => {
    const win = BrowserWindow.getAllWindows().find((candidate) => !before.has(candidate) && !candidate.isDestroyed());
    assert.ok(win, "pet window exists");
    return win;
  };
  const scripts: string[] = [];
  const instrument = (win: BrowserWindow) => {
    const original = win.webContents.executeJavaScript.bind(win.webContents);
    win.webContents.executeJavaScript = (code: string, gesture?: boolean) => {
      scripts.push(code);
      return original(code, gesture);
    };
  };
  let win = petWindow();
  instrument(win);
  const page = <T>(code: string): Promise<T> => win.webContents.executeJavaScript(code, true) as Promise<T>;
  const ready = async () => {
    await until(() => !win.webContents.isLoading() && win.isVisible(), "pet window shown");
    await until(() => page<boolean>("typeof window.wuuPetView === 'function'"), "pet page ready");
  };
  await ready();

  const cell = () =>
    page<{ row: number; col: number }>(`(() => {
      const [x, y] = getComputedStyle(document.querySelector('.sprite')).backgroundPosition.split(' ').map(parseFloat);
      return { row: Math.round(-y / ${CELL_H}), col: Math.round(-x / ${CELL_W}) };
    })()`);
  const waitRow = async (row: number, label: string, timeoutMs = 4000) => {
    try {
      await until(async () => (await cell()).row === row, label, timeoutMs);
      check(label, true);
    } catch (error) {
      check(label, false, { expectedRow: row, actual: await cell(), error: String(error) });
    }
  };
  const panel = () =>
    page<{
      expanded: boolean;
      bubbleHidden: boolean;
      rows: Array<{ id: string; status: string; title: string; target: boolean; action: string }>;
      target: string;
      canClear: boolean;
      targetError: boolean;
      targetBlocked: boolean;
      bubbleFocused: boolean;
      value: string;
      disabled: boolean;
      readOnly: boolean;
      focused: boolean;
      injected: number;
      layout: string;
    }>(`(() => {
      const input = document.querySelector('textarea');
      return {
        expanded: document.querySelector('.stage').classList.contains('is-expanded'),
        bubbleHidden: document.querySelector('.bubble').hidden,
        rows: [...document.querySelectorAll('.hint-row')].map((row) => ({
          id: row.dataset.threadId,
          status: row.dataset.status,
          title: row.querySelector('.row-title').textContent,
          target: row.classList.contains('is-target'),
          action: row.querySelector('.row-action').dataset.action,
        })),
        target: document.querySelector('.target-label').textContent,
        canClear: !document.querySelector('.target-clear').hidden,
        targetError: document.querySelector('.composer-target').classList.contains('is-error'),
        targetBlocked: document.querySelector('.composer-target').classList.contains('is-blocked'),
        bubbleFocused: document.querySelector('.bubble').contains(document.activeElement),
        value: input.value,
        disabled: input.disabled,
        readOnly: input.readOnly,
        focused: document.activeElement === input,
        injected: document.querySelectorAll('img').length,
        layout: document.querySelector('.stage').dataset.layout,
      };
    })()`);
  const shot = async (name: string, note: string) => {
    await delay(120);
    const image = await win.webContents.capturePage();
    const file = `${String(results.screenshots.length + 1).padStart(2, "0")}-${name}.png`;
    writeFileSync(join(artifacts, file), image.toPNG());
    results.screenshots.push({ file, theme: nativeTheme.shouldUseDarkColors ? "dark" : "light", note });
  };
  // The element is fully inside the page and the window inside its work area.
  const fits = async (selector: string) => {
    await delay(150);
    const inside = await page<boolean>(`(() => {
      const box = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect();
      return box.width > 0 && box.left >= 0 && box.top >= 0 && box.right <= innerWidth && box.bottom <= innerHeight;
    })()`);
    const bounds = win.getBounds();
    const area = screen.getDisplayMatching(bounds).workArea;
    return inside && bounds.x >= area.x && bounds.y >= area.y &&
      bounds.x + bounds.width <= area.x + area.width && bounds.y + bounds.height <= area.y + area.height;
  };
  const click = async (selector: string) => {
    const point = await page<{ x: number; y: number }>(`(() => {
      const box = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect();
      return { x: Math.round(box.left + box.width / 2), y: Math.round(box.top + box.height / 2) };
    })()`);
    win.webContents.sendInputEvent({ type: "mouseMove", x: point.x, y: point.y });
    win.webContents.sendInputEvent({ type: "mouseDown", x: point.x, y: point.y, button: "left", clickCount: 1 });
    win.webContents.sendInputEvent({ type: "mouseUp", x: point.x, y: point.y, button: "left", clickCount: 1 });
  };
  const key = (keyCode: string) => {
    win.webContents.sendInputEvent({ type: "keyDown", keyCode });
    win.webContents.sendInputEvent({ type: "char", keyCode });
    win.webContents.sendInputEvent({ type: "keyUp", keyCode });
  };
  // The sprite's bottom-center on screen, measured from the rendered page.
  const spriteFoot = async () => {
    const foot = await page<{ x: number; y: number }>(`(() => {
      const box = document.querySelector('.sprite-frame').getBoundingClientRect();
      return { x: box.left + box.width / 2, y: box.bottom };
    })()`);
    const bounds = win.getBounds();
    return { x: bounds.x + foot.x, y: bounds.y + foot.y };
  };
  // The sprite must not move when the bubble appears, grows, or changes side.
  const holdsStill = async (label: string, before: { x: number; y: number }) => {
    const near = (a: { x: number; y: number }) => Math.abs(a.x - before.x) <= 1 && Math.abs(a.y - before.y) <= 1;
    await until(async () => near(await spriteFoot()), label).then(
      () => check(label, true),
      async () => check(label, false, { before, after: await spriteFoot(), layout: (await panel()).layout }),
    );
  };
  const spriteCenter = () => {
    const bounds = win.getBounds();
    const sprite = codexPetRenderedSpriteForSize("default");
    // Collapsed with no bubble the sprite is centered, 8px above the bottom.
    return { x: bounds.x + bounds.width / 2, y: bounds.y + bounds.height - 8 - sprite.height / 2 };
  };

  // Greeting, then idle.
  await waitRow(3, "greets with the waving row after loading", 1500);
  await waitRow(0, "settles into the idle row after the greeting");
  await shot("idle", "Idle, no bubble");
  {
    const bounds = win.getBounds();
    const foot = await spriteFoot();
    check(
      "renders the sprite where the window geometry expects it",
      Math.abs(foot.x - (bounds.x + bounds.width / 2)) <= 1 && Math.abs(foot.y - (bounds.y + bounds.height - 8)) <= 1,
      { foot, bounds },
    );
  }

  // Cursor following on a sheet with look rows.
  if (cursorOverridden) {
    const center = spriteCenter();
    cursor.x = center.x + 200;
    cursor.y = center.y;
    await waitRow(9, "looks at a cursor to its right using the look rows");
    const looking = await cell();
    check("faces right (direction 4 of 16)", looking.row === 9 && looking.col === 4, looking);
    await shot("look-right", "Looking at a cursor to the right");
    cursor.x = center.x - 140;
    cursor.y = center.y + 140;
    await until(async () => {
      const current = await cell();
      return current.row === 10 && current.col === 2;
    }, "look down-left").then(
      () => check("follows the cursor down-left (direction 10)", true),
      async () => check("follows the cursor down-left (direction 10)", false, await cell()),
    );
    await waitRow(0, "stops looking once the cursor rests for two seconds", 4000);
  } else {
    results.unverified.push("Cursor following: screen.getCursorScreenPoint could not be scripted in this Electron build.");
  }

  // Moods and reactions.
  manager.setMood("running");
  await waitRow(7, "running mood plays the running row");
  if (cursorOverridden) {
    const pushes = scripts.filter((code) => code.includes("wuuPetLook")).length;
    cursor.x += 50;
    await delay(400);
    check(
      "does not track the cursor while working",
      scripts.filter((code) => code.includes("wuuPetLook")).length === pushes && (await cell()).row === 7,
    );
  }
  manager.setMood("waiting");
  await waitRow(6, "waiting mood plays the waiting row");
  manager.react("review");
  await waitRow(8, "a finished turn plays the review row");
  await waitRow(6, "returns to the mood after the review reaction", 6000);
  manager.react("failed");
  await waitRow(5, "a failed turn plays the failed row");
  manager.setMood("idle");
  await waitRow(0, "returns to idle after the failed reaction", 6000);

  // Bubble rows.
  let foot = await spriteFoot();
  manager.setHints(hints);
  await until(async () => (await panel()).rows.length === 3, "three bubble rows");
  await holdsStill("keeps the sprite in place when the bubble opens to its left", foot);
  let state = await panel();
  check("shows the three rows beside the pet in the bottom-right corner", !state.bubbleHidden && state.layout === "left", state);
  check("fits the collapsed bubble inside the window and the window on screen", await fits(".bubble"), win.getBounds());
  check(
    "renders hostile titles as text",
    state.injected === 0 && state.rows[1].title === hints[1].title,
    state.rows[1],
  );
  await shot("rows-light", "Collapsed bubble: needs review, failed, running");
  nativeTheme.themeSource = "dark";
  await delay(200);
  await shot("rows-dark", "Collapsed bubble in dark mode");
  nativeTheme.themeSource = "light";

  const area = screen.getDisplayMatching(win.getBounds()).workArea;
  win.setBounds({ ...win.getBounds(), x: area.x + Math.round(area.width / 2), y: area.y + Math.round(area.height / 2) });
  await delay(100);
  foot = await spriteFoot();
  manager.setHints([...hints]);
  await holdsStill("keeps the sprite in place when the bubble moves above it", foot);
  await until(async () => (await panel()).layout === "above", "bubble above the pet mid-screen").then(
    async () => check("puts the bubble above the pet when there is room", await fits(".bubble")),
    async () => check("puts the bubble above the pet when there is room", false, await panel()),
  );
  await shot("rows-above", "Bubble above the pet");

  // A collapsed row opens its conversation.
  await click('.hint-row[data-thread-id="thread-running"] .row-title');
  await until(() => jumps.length === 1, "row jump");
  check("clicking a collapsed row opens its conversation", jumps[0] === "thread-running", jumps);

  // Quick panel: the pet starts a new conversation in the workspace that
  // was active when the panel opened, even if Wuu switches while typing.
  const appWorkspace = { context: { kind: "project", project_id: "project-app", cwd: "/work/app" }, name: "wuu" } as const;
  const docsWorkspace = { context: { kind: "project", project_id: "project-docs", cwd: "/work/docs" }, name: "docs" } as const;
  manager.setWorkspace(appWorkspace);
  foot = await spriteFoot();
  await click(".sprite");
  await until(async () => (await panel()).expanded, "panel opens");
  await holdsStill("keeps the sprite in place when the panel opens above it", foot);
  await until(async () => (await panel()).focused, "composer focused");
  check("clicking the pet opens the panel with a focused composer that fits", await fits(".bubble"), win.getBounds());
  state = await panel();
  check(
    "names the new conversation and its workspace, with no row picked",
    state.target === "新对话 · wuu" && !state.canClear && state.rows.every((row) => !row.target),
    state,
  );
  check(
    "offers Answer on the row that waits for the user and reply on the others",
    state.rows.map((row) => row.action).join() === "answer,reply,reply",
    state.rows,
  );
  await shot("panel-new", `Panel open: ${state.target}`);
  win.webContents.insertText("帮我总结一下今天的改动");
  manager.setWorkspace(docsWorkspace);
  await delay(50);
  state = await panel();
  check("keeps the workspace captured at open when Wuu switches workspace", state.target === "新对话 · wuu", state);
  await shot("panel-typed", "New conversation with a draft (focused composer)");
  key("Enter");
  await until(() => submits.length === 1, "first submit");
  check(
    "Enter starts the conversation in the captured workspace",
    submits[0].text === "帮我总结一下今天的改动" &&
      JSON.stringify(submits[0].target) === JSON.stringify({ workspace: appWorkspace.context }),
    submits[0],
  );
  state = await panel();
  check("locks the draft while sending", state.readOnly && state.value.length > 0, state);
  await shot("panel-sending", "Sending");
  submits[0].settle({ ok: true });
  await until(async () => !(await panel()).expanded, "panel closes after a send");
  state = await panel();
  check("a successful send clears the draft and closes the panel", state.value === "", state);
  await waitRow(4, "celebrates a successful send with the jumping row", 1500);

  // Reply to a row; the host rejects it.
  await click(".sprite");
  await until(async () => (await panel()).expanded, "panel reopens");
  state = await panel();
  check("a reopened panel captures the workspace active now", state.target === "新对话 · docs", state);
  await click('.hint-row[data-thread-id="thread-failed"] .row-action');
  await until(async () => (await panel()).rows[1].target, "reply target picked");
  state = await panel();
  check(
    "the reply action names the conversation and focuses the composer",
    state.target === `回复 · ${hints[1].title}` && state.canClear && state.focused && jumps.length === 1,
    { state, jumps },
  );
  await shot("panel-reply", "Replying to a conversation");
  win.webContents.insertText("重试一次");
  await delay(50);
  key("Enter");
  await until(() => submits.length === 2, "second submit");
  check("sends the reply to that conversation", JSON.stringify(submits[1].target) === JSON.stringify({ thread_id: "thread-failed" }), submits[1]);
  submits[1].settle({ ok: false });
  await until(async () => (await panel()).targetError, "send failure shown");
  state = await panel();
  check("a failed send keeps the draft and the panel", state.expanded && state.value === "重试一次", state);
  await waitRow(5, "a failed send plays the failed row", 1500);
  await shot("panel-failed", "Send failed; draft kept");

  // × returns to a new conversation; the row stays clickable to open it.
  await click(".target-clear");
  await until(async () => (await panel()).canClear === false, "reply cleared");
  state = await panel();
  check("× switches back to a new conversation", state.target === "新对话 · docs" && state.rows.every((row) => !row.target), state);
  await click('.hint-row[data-thread-id="thread-running"] .row-title');
  await until(() => jumps.length === 2, "expanded row jump");
  check("clicking a row in the open panel still opens its conversation", jumps[1] === "thread-running", jumps);
  await click('.hint-row[data-thread-id="thread-review"] .row-action');
  await until(() => jumps.length === 3, "answer jump");
  check("Answer opens the conversation that waits for the user", jumps[2] === "thread-review", jumps);

  // A reply target that starts waiting on a question cannot take a message.
  await click('.hint-row[data-thread-id="thread-running"] .row-action');
  await until(async () => (await panel()).rows[2].target, "running row picked");
  manager.setHints(hints.map((hint) => (hint.thread_id === "thread-running" ? { ...hint, status: "needs_review" } : hint)));
  await until(async () => (await panel()).targetBlocked, "reply blocked");
  state = await panel();
  check("blocks a reply to a conversation that now needs an answer and says why", state.disabled && state.targetBlocked, state);
  await shot("panel-blocked", "Reply target needs an answer first");
  manager.setHints([...hints]);
  win.focus();
  await delay(50);
  key("Escape");
  await until(async () => !(await panel()).expanded, "Escape closes the panel");
  check("Escape closes the panel and leaves no focus ring in the bubble", !(await panel()).bubbleFocused, await panel());

  // Sizes and edge layouts.
  manager.setSize("large");
  await delay(250);
  await shot("rows-large", "Large size with rows");
  manager.setSize("default");
  const workArea = screen.getDisplayMatching(win.getBounds()).workArea;
  // The sprite's own footprint at the top-left corner, as after a drag there.
  const spriteOnly = codexPetRenderedSpriteForSize("default");
  win.setBounds({ x: workArea.x + 4, y: workArea.y, width: spriteOnly.windowWidth, height: spriteOnly.windowHeight });
  // Where the manager reads the sprite from these sprite-only bounds.
  foot = { x: workArea.x + 4 + spriteOnly.windowWidth / 2, y: workArea.y + spriteOnly.windowHeight - 8 };
  manager.setHints([...hints]);
  await delay(250);
  await holdsStill("keeps the sprite in place when the bubble opens beside it at the top edge", foot);
  state = await panel();
  const edgeBounds = win.getBounds();
  check(
    "moves the bubble off the top edge and stays on screen",
    state.layout !== "above" &&
      edgeBounds.y >= workArea.y &&
      edgeBounds.x >= workArea.x &&
      edgeBounds.y + edgeBounds.height <= workArea.y + workArea.height,
    { layout: state.layout, bounds: edgeBounds, workArea },
  );
  await shot("rows-edge", `Near the top-left edge: ${state.layout}`);
  await click(".sprite");
  await until(async () => (await panel()).expanded, "edge panel opens");
  state = await panel();
  check("opens the panel below the pet when there is no room above", state.layout === "below" && (await fits(".bubble")), {
    layout: state.layout,
    bounds: win.getBounds(),
  });
  await holdsStill("keeps the sprite in place when the panel opens below it", foot);
  nativeTheme.themeSource = "dark";
  await delay(200);
  await shot("panel-below-dark", `Panel at the top edge (${state.layout}), dark mode`);
  nativeTheme.themeSource = "light";
  win.focus();
  key("Escape");
  await until(async () => !(await panel()).expanded, "edge panel closes");
  await holdsStill("returns the sprite to the same place when the panel closes", foot);

  // English locale and a sheet without look rows.
  setMainLocale("en-US");
  manager.setHints([]);
  manager.sync(snapshotFor(classicPet, [lookPet, classicPet]));
  manager.refreshLocale();
  win = petWindow();
  instrument(win);
  await ready();
  manager.setHints(hints.slice(2));
  await click(".sprite");
  await until(async () => (await panel()).expanded, "English panel opens");
  await shot("panel-en", "English locale, panel open");
  key("Escape");
  if (cursorOverridden) {
    await waitRow(0, "classic sheet idles");
    const pushes = scripts.filter((code) => code.includes("wuuPetLook") && !code.includes("(null)")).length;
    const center = spriteCenter();
    cursor.x = center.x + 120;
    cursor.y = center.y;
    await delay(600);
    check(
      "never tracks the cursor with a sheet that lacks look rows",
      scripts.filter((code) => code.includes("wuuPetLook") && !code.includes("(null)")).length === pushes &&
        (await cell()).row === 0,
    );
  }

  results.unverified.push(
    "Native context menu contents (Menu.popup is not capturable).",
    "prefers-reduced-motion rendering.",
    "IME composition while pressing Enter.",
    "Hidden layout attention badge (needs a work area too small for every bubble layout).",
    "Pointer drag animation and continuous resize handles.",
  );
  manager.destroy();
  await writeContactSheet();
}

// Puts every screenshot on a backdrop matching its theme, so transparent
// pet captures can be reviewed in one image.
async function writeContactSheet(): Promise<void> {
  const tiles = results.screenshots
    .map(
      (entry) =>
        `<figure class="${entry.theme}"><img src="${entry.file}"><figcaption>${entry.file}<br>${entry.note
          .replace(/&/g, "&amp;")
          .replace(/</g, "&lt;")}</figcaption></figure>`,
    )
    .join("");
  const html = `<!doctype html><meta charset="utf-8"><style>
    body{margin:0;padding:16px;display:flex;flex-wrap:wrap;gap:16px;align-items:flex-start;background:#ddd;font:12px system-ui}
    figure{margin:0;padding:12px;border-radius:8px;display:flex;flex-direction:column;align-items:center;gap:8px}
    figure.light{background:linear-gradient(135deg,#f4f7fb,#c9d6e8);color:#222}
    figure.dark{background:linear-gradient(135deg,#1d2230,#3a3f52);color:#eee}
    figcaption{text-align:center;max-width:300px}
  </style>${tiles}`;
  const page = join(artifacts, "contact-sheet.html");
  writeFileSync(page, html);
  const sheet = new BrowserWindow({ show: false, width: 1500, height: 1200, webPreferences: { offscreen: true } });
  await sheet.loadFile(page);
  const height: number = await sheet.webContents.executeJavaScript("document.body.scrollHeight");
  sheet.setContentSize(1500, Math.min(height, 4000));
  await delay(300);
  writeFileSync(join(artifacts, "contact-sheet.png"), (await sheet.webContents.capturePage()).toPNG());
  sheet.destroy();
}

main()
  .catch((error) => {
    check("e2e run completed", false, String(error?.stack ?? error));
  })
  .finally(() => {
    const failed = results.checks.filter((entry) => !entry.pass).length;
    writeFileSync(join(artifacts, "results.json"), JSON.stringify({ ...results, failed }, null, 2));
    console.log(`${results.checks.length - failed}/${results.checks.length} checks passed; artifacts: ${relative(process.cwd(), artifacts)}`);
    rmSync(scratch, { recursive: true, force: true });
    app.exit(failed ? 1 : 0);
  });
