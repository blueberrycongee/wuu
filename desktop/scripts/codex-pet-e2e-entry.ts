// Runs the production pet window against synthetic spritesheets: moods,
// reactions, conversation cards (glance filtering, dismiss), the dock
// (hover toolbar, composer: new conversation, reply, failure, blocked
// reply, keyboard), cursor following, sizes, themes, and edge layouts.
// Writes screenshots, a contact sheet, and results.json for review.
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import { app, BrowserWindow, nativeTheme, screen } from "electron";
import type { CodexPet, CodexPetHint, CodexPetSubmitResult, CodexPetSubmitTarget, CodexPetsSnapshot } from "../src/shared/protocol";
import { CODEX_PET_STATES } from "../src/main/codexPets";
import { CodexPetWindowManager, codexPetBoundsForLayout, codexPetRenderedSpriteForSize } from "../src/main/codexPetWindow";
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
const idleHint: CodexPetHint = {
  thread_id: "thread-idle",
  title: "整理文档",
  status: "idle",
  preview: "已更新 README 的安装说明",
  attention: false,
  updated_at: now - 3000,
};
// The sprite's bottom sits above the dock slot (4 + 36) and the window padding (8).
const SPRITE_BOTTOM_INSET = 48;

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
  let shownApp = 0;
  const manager = new CodexPetWindowManager({
    onClose: () => undefined,
    onJump: (threadID) => jumps.push(threadID),
    onSubmit: (text, target) => new Promise((settle) => submits.push({ text, target, settle })),
    onShowApp: () => {
      shownApp += 1;
    },
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
    // Synthetic clicks do not activate the app like a native click. Without
    // activation, macOS can take focus back and close the composer on blur.
    app.focus({ steal: true });
    win.focus();
    await until(() => win.isFocused(), "pet window focused for synthetic input");
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
      cards: Array<{ id: string; status: string; title: string; target: boolean; action: string }>;
      target: string;
      targetHidden: boolean;
      isReply: boolean;
      status: string;
      statusError: boolean;
      statusBlocked: boolean;
      bubbleFocused: boolean;
      value: string;
      disabled: boolean;
      readOnly: boolean;
      focused: boolean;
      sendDisabled: boolean;
      toolbarShown: boolean;
      injected: number;
      layout: string;
    }>(`(() => {
      const input = document.querySelector('.composer input');
      const status = document.querySelector('.composer-status');
      const toolbar = getComputedStyle(document.querySelector('.toolbar'));
      return {
        expanded: document.querySelector('.stage').classList.contains('is-expanded'),
        bubbleHidden: document.querySelector('.bubble').hidden,
        cards: [...document.querySelectorAll('.card')].map((card) => ({
          id: card.dataset.threadId,
          status: card.dataset.status,
          title: card.querySelector('.card-title').textContent,
          target: card.classList.contains('is-target'),
          action: card.querySelector('.card-action').dataset.action,
        })),
        target: document.querySelector('.target-label').textContent,
        targetHidden: document.querySelector('.composer-target').hidden,
        isReply: document.querySelector('.composer-target').classList.contains('is-reply'),
        status: status.hidden ? '' : status.textContent,
        statusError: status.classList.contains('is-error'),
        statusBlocked: status.classList.contains('is-blocked'),
        bubbleFocused: document.querySelector('.bubble').contains(document.activeElement),
        value: input.value,
        disabled: input.disabled,
        readOnly: input.readOnly,
        focused: document.activeElement === input,
        sendDisabled: document.querySelector('.composer-send').disabled,
        toolbarShown: toolbar.visibility === 'visible' && Number(toolbar.opacity) === 1,
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
  const centerOf = (selector: string) =>
    page<{ x: number; y: number }>(`(() => {
      const box = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect();
      return { x: Math.round(box.left + box.width / 2), y: Math.round(box.top + box.height / 2) };
    })()`);
  const hover = async (selector: string) => {
    const point = await centerOf(selector);
    win.webContents.sendInputEvent({ type: "mouseMove", x: point.x, y: point.y });
    await delay(160);
  };
  // Parks the pointer in the transparent padding so hover-only controls hide.
  const leave = async () => {
    win.webContents.sendInputEvent({ type: "mouseMove", x: 1, y: 1 });
    await delay(160);
  };
  // Card actions and the toolbar only lay out while hovered, so a click on
  // them first hovers their container.
  const click = async (selector: string, hoverFirst?: string) => {
    if (hoverFirst) {
      // Synthetic moves right after a window resize can miss :hover, so
      // hover until the hover-only target is laid out.
      await until(async () => {
        await hover(hoverFirst);
        return page<boolean>(`document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect().width > 0`);
      }, `${selector} revealed by hover`);
    }
    const point = await centerOf(selector);
    win.webContents.sendInputEvent({ type: "mouseMove", x: point.x, y: point.y });
    win.webContents.sendInputEvent({ type: "mouseDown", x: point.x, y: point.y, button: "left", clickCount: 1 });
    win.webContents.sendInputEvent({ type: "mouseUp", x: point.x, y: point.y, button: "left", clickCount: 1 });
  };
  const cardSelector = (threadID: string) => `.card[data-thread-id="${threadID}"]`;
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
  // The sprite must not move when cards appear, grow, change side, or the
  // composer opens.
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
    // Collapsed with no cards the sprite is centered above the dock.
    return { x: bounds.x + bounds.width / 2, y: bounds.y + bounds.height - SPRITE_BOTTOM_INSET - sprite.height / 2 };
  };

  // Greeting, then idle.
  await waitRow(3, "greets with the waving row after loading", 1500);
  await waitRow(0, "settles into the idle row after the greeting");
  await shot("idle", "Idle: sprite and dock handle");
  {
    const bounds = win.getBounds();
    const foot = await spriteFoot();
    check(
      "renders the sprite where the window geometry expects it",
      Math.abs(foot.x - (bounds.x + bounds.width / 2)) <= 1 &&
        Math.abs(foot.y - (bounds.y + bounds.height - SPRITE_BOTTOM_INSET)) <= 1,
      { foot, bounds },
    );
  }
  await hover(".sprite-frame");
  check("hovering the pet reveals the toolbar", (await panel()).toolbarShown, await panel());
  await shot("hover-toolbar", "Hover: new conversation and open Wuu");
  await leave();
  check("the toolbar hides again when the pointer leaves", !(await panel()).toolbarShown);

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

  // Cards.
  let foot = await spriteFoot();
  manager.setHints(hints);
  await until(async () => (await panel()).cards.length === 3, "three cards");
  await holdsStill("keeps the sprite in place when the cards open to its left", foot);
  let state = await panel();
  check("shows the three cards beside the pet in the bottom-right corner", !state.bubbleHidden && state.layout === "left", state);
  check("fits the cards inside the window and the window on screen", await fits(".bubble"), win.getBounds());
  check(
    "renders hostile titles as text",
    state.injected === 0 && state.cards[1].title === hints[1].title,
    state.cards[1],
  );
  await shot("cards-light", "Cards: needs review, failed, running");
  nativeTheme.themeSource = "dark";
  await delay(200);
  await shot("cards-dark", "Cards in dark mode");
  nativeTheme.themeSource = "light";

  const area = screen.getDisplayMatching(win.getBounds()).workArea;
  win.setBounds({ ...win.getBounds(), x: area.x + Math.round(area.width / 2), y: area.y + Math.round(area.height / 2) });
  await delay(100);
  foot = await spriteFoot();
  manager.setHints([...hints]);
  await holdsStill("keeps the sprite in place when the cards move above it", foot);
  await until(async () => (await panel()).layout === "above", "cards above the pet mid-screen").then(
    async () => check("puts the cards above the pet when there is room", await fits(".bubble")),
    async () => check("puts the cards above the pet when there is room", false, await panel()),
  );
  await shot("cards-above", "Cards above the pet");
  await hover(cardSelector("thread-running"));
  await shot("card-hover", "Hovered card: reply and hide");

  // A collapsed card opens its conversation.
  await click(`${cardSelector("thread-running")} .card-title`);
  await until(() => jumps.length === 1, "card jump");
  check("clicking a collapsed card opens its conversation", jumps[0] === "thread-running", jumps);

  // The glance hides a dismissed card until its conversation changes status,
  // and never shows idle conversations.
  await click(`${cardSelector("thread-running")} .card-dismiss`, cardSelector("thread-running"));
  await until(async () => (await panel()).cards.length === 2, "dismissed card hidden").then(
    () => check("hiding a card removes it from the glance", true),
    async () => check("hiding a card removes it from the glance", false, await panel()),
  );
  await holdsStill("keeps the sprite in place when a card is hidden", foot);
  manager.setHints([...hints]);
  await delay(100);
  check("a hidden card stays hidden while its conversation is unchanged", (await panel()).cards.length === 2);
  manager.setHints(hints.map((hint) => (hint.thread_id === "thread-running" ? { ...hint, status: "done" } : hint)));
  await until(async () => (await panel()).cards.length === 3, "hidden card returns").then(
    () => check("a hidden card returns when its conversation finishes", true),
    async () => check("a hidden card returns when its conversation finishes", false, await panel()),
  );
  manager.setHints([hints[0], hints[1], idleHint]);
  await until(async () => (await panel()).cards.length === 2, "idle card hidden");
  check(
    "keeps idle conversations out of the glance",
    (await panel()).cards.every((card) => card.id !== idleHint.thread_id),
    (await panel()).cards,
  );
  await leave();

  // Composer: the pet starts a new conversation in the workspace that was
  // active when the composer opened, even if Wuu switches while typing.
  const appWorkspace = { context: { kind: "project", project_id: "project-app", cwd: "/work/app" }, name: "wuu" } as const;
  const docsWorkspace = { context: { kind: "project", project_id: "project-docs", cwd: "/work/docs" }, name: "docs" } as const;
  manager.setWorkspace(appWorkspace);
  foot = await spriteFoot();
  await click('.tool[data-action="new"]', ".sprite-frame");
  await until(async () => (await panel()).expanded, "composer opens");
  await holdsStill("keeps the sprite in place when the composer opens", foot);
  await until(async () => (await panel()).focused, "composer focused");
  check("the new-conversation tool opens a focused composer that fits", await fits(".composer"), win.getBounds());
  state = await panel();
  check(
    "targets the workspace with no card picked",
    state.target === "wuu" && !state.isReply && !state.targetHidden && state.cards.every((card) => !card.target),
    state,
  );
  check(
    "lists idle conversations as reply targets while the composer is open",
    state.cards.map((card) => card.id).join() === `${hints[0].thread_id},${hints[1].thread_id},${idleHint.thread_id}`,
    state.cards,
  );
  check(
    "offers Answer on the card that waits for the user and reply on the others",
    state.cards.map((card) => card.action).join() === "answer,reply,reply",
    state.cards,
  );
  check("disables send while the draft is empty", state.sendDisabled, state);
  await shot("composer-new", `Composer open: new conversation in ${state.target}`);
  win.webContents.insertText("帮我总结一下今天的改动");
  manager.setWorkspace(docsWorkspace);
  await delay(50);
  state = await panel();
  check(
    "keeps the workspace captured at open when Wuu switches workspace",
    state.target === "wuu" && !state.sendDisabled,
    state,
  );
  await shot("composer-typed", "New conversation with a draft");
  key("Enter");
  await until(() => submits.length === 1, "first submit");
  check(
    "Enter starts the conversation in the captured workspace",
    submits[0].text === "帮我总结一下今天的改动" &&
      JSON.stringify(submits[0].target) === JSON.stringify({ workspace: appWorkspace.context }),
    submits[0],
  );
  state = await panel();
  check("locks the draft while sending", state.readOnly && state.value.length > 0 && state.sendDisabled, state);
  await shot("composer-sending", "Sending");
  submits[0].settle({ ok: true });
  await until(async () => !(await panel()).expanded, "composer closes after a send");
  state = await panel();
  check("a successful send clears the draft and closes the composer", state.value === "", state);
  await waitRow(4, "celebrates a successful send with the jumping row", 1500);

  // Reply to a card; the host rejects it.
  await click(".sprite-frame");
  await until(async () => (await panel()).expanded, "composer reopens");
  state = await panel();
  check("clicking the pet opens the composer in the workspace active now", state.target === "docs", state);
  await click(`${cardSelector("thread-failed")} .card-action`, cardSelector("thread-failed"));
  await until(async () => (await panel()).cards[1].target, "reply target picked");
  state = await panel();
  check(
    "the reply action names the conversation and focuses the composer",
    state.target === hints[1].title && state.isReply && state.focused && jumps.length === 1,
    { state, jumps },
  );
  await shot("composer-reply", "Replying to a conversation");
  win.webContents.insertText("重试一次");
  await delay(50);
  key("Enter");
  await until(() => submits.length === 2, "second submit");
  check("sends the reply to that conversation", JSON.stringify(submits[1].target) === JSON.stringify({ thread_id: "thread-failed" }), submits[1]);
  submits[1].settle({ ok: false });
  await until(async () => (await panel()).statusError, "send failure shown");
  state = await panel();
  check(
    "a failed send keeps the draft and the composer and says so",
    state.expanded && state.value === "重试一次" && state.status === "没发出去，打开 Wuu 再试一次",
    state,
  );
  await waitRow(5, "a failed send plays the failed row", 1500);
  await shot("composer-failed", "Send failed; draft kept");

  // The reply chip returns to a new conversation; cards stay clickable.
  await click(".composer-target");
  await until(async () => !(await panel()).isReply, "reply cleared");
  state = await panel();
  check("the reply chip switches back to a new conversation", state.target === "docs" && state.cards.every((card) => !card.target), state);
  await click(`${cardSelector("thread-failed")} .card-title`);
  await until(() => jumps.length === 2, "expanded card jump");
  check("clicking a card with the composer open still opens its conversation", jumps[1] === "thread-failed", jumps);
  await click(`${cardSelector("thread-review")} .card-action`);
  await until(() => jumps.length === 3, "answer jump");
  check("Answer opens the conversation that waits for the user", jumps[2] === "thread-review", jumps);

  // A reply target that starts waiting on a question cannot take a message.
  await click(`${cardSelector(idleHint.thread_id)} .card-action`, cardSelector(idleHint.thread_id));
  await until(async () => (await panel()).cards[2].target, "idle card picked");
  manager.setHints([hints[0], hints[1], { ...idleHint, status: "needs_review" }]);
  await until(async () => (await panel()).statusBlocked, "reply blocked");
  state = await panel();
  check("blocks a reply to a conversation that now needs an answer and says why", state.disabled && state.sendDisabled, state);
  await shot("composer-blocked", "Reply target needs an answer first");
  manager.setHints([hints[0], hints[1], idleHint]);
  win.focus();
  await delay(50);
  key("Escape");
  await until(async () => !(await panel()).expanded, "Escape closes the composer");
  check("Escape closes the composer and leaves no focus ring on the cards", !(await panel()).bubbleFocused, await panel());

  // Reply straight from the glance.
  await click(`${cardSelector("thread-failed")} .card-action`, cardSelector("thread-failed"));
  await until(async () => (await panel()).expanded, "reply from the glance opens the composer");
  await until(async () => (await panel()).focused, "reply composer focused");
  state = await panel();
  check("replying from a collapsed card opens the composer aimed at it", state.isReply && state.target === hints[1].title, state);
  key("Escape");
  await until(async () => !(await panel()).expanded, "reply composer closes");

  await click('.tool[data-action="show"]', ".sprite-frame");
  await until(() => shownApp === 1, "show app").then(
    () => check("the toolbar opens Wuu", true),
    () => check("the toolbar opens Wuu", false, { shownApp }),
  );
  await leave();

  // Sizes and edge layouts.
  manager.setHints([...hints]);
  manager.setSize("large");
  await delay(250);
  await shot("cards-large", "Large size with cards");
  await click(".sprite-frame");
  await until(async () => (await panel()).expanded, "large composer opens");
  check("the composer fits at the large size", await fits(".composer"), win.getBounds());
  await shot("composer-large", "Large size, composer open");
  key("Escape");
  await until(async () => !(await panel()).expanded, "large composer closes");
  manager.setSize("default");
  const workArea = screen.getDisplayMatching(win.getBounds()).workArea;
  // The pet column alone at the top-left corner, as after a drag there.
  const petOnly = codexPetBoundsForLayout({ layout: "hidden", anchor: { x: 0, y: 0 } });
  win.setBounds({ x: workArea.x + 4, y: workArea.y, width: petOnly.width, height: petOnly.height });
  // Where the manager reads the sprite from these bounds.
  foot = { x: workArea.x + 4 + petOnly.width / 2, y: workArea.y + petOnly.height - SPRITE_BOTTOM_INSET };
  manager.setHints([...hints]);
  await delay(250);
  await holdsStill("keeps the sprite in place when the cards open beside it at the top edge", foot);
  state = await panel();
  const edgeBounds = win.getBounds();
  check(
    "moves the cards off the top edge and stays on screen",
    state.layout !== "above" &&
      edgeBounds.y >= workArea.y &&
      edgeBounds.x >= workArea.x &&
      edgeBounds.y + edgeBounds.height <= workArea.y + workArea.height,
    { layout: state.layout, bounds: edgeBounds, workArea },
  );
  await shot("cards-edge", `Near the top-left edge: ${state.layout}`);
  await click(".sprite-frame");
  await until(async () => (await panel()).expanded, "edge composer opens");
  state = await panel();
  check(
    "opens below the pet, slid along the side edge, when there is no room above",
    state.layout === "below" && (await fits(".bubble")) && (await fits(".composer")),
    { layout: state.layout, bounds: win.getBounds() },
  );
  await holdsStill("keeps the sprite in place when the composer opens at the edge", foot);
  nativeTheme.themeSource = "dark";
  await delay(200);
  await shot("composer-below-dark", `Composer at the top-left edge (${state.layout}), dark mode`);
  nativeTheme.themeSource = "light";
  win.focus();
  key("Escape");
  await until(async () => !(await panel()).expanded, "edge composer closes");
  await holdsStill("returns the sprite to the same place when the composer closes", foot);

  // English locale and a sheet without look rows.
  setMainLocale("en-US");
  manager.setHints([]);
  manager.sync(snapshotFor(classicPet, [lookPet, classicPet]));
  manager.refreshLocale();
  win = petWindow();
  instrument(win);
  await ready();
  manager.setHints([hints[0], hints[2]]);
  await until(async () => (await panel()).cards.length === 2, "English cards");
  await shot("cards-en", "English locale, cards");
  await click(".sprite-frame");
  await until(async () => (await panel()).expanded, "English composer opens");
  await shot("composer-en", "English locale, composer open");
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
    "Hidden layout attention badge (needs a work area too small for every card layout).",
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
