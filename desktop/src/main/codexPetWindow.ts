import { BrowserWindow, Menu, screen, type Rectangle } from "electron";
import {
  CODEX_PET_HINTS_MAX,
  CODEX_PET_SIZE_DEFAULT,
  CODEX_PET_SIZE_OPTIONS,
  type AppLocale,
  type CodexPet,
  type CodexPetHint,
  type CodexPetHintStatus,
  type CodexPetSize,
  type CodexPetSubmitResult,
  type CodexPetSubmitTarget,
  type CodexPetsSnapshot,
  type CodexPetWorkspace,
} from "../shared/protocol";
import type { CodexPetMood, CodexPetReaction } from "./codexPetActivity";
import { CODEX_PET_CELL_HEIGHT, CODEX_PET_CELL_WIDTH, CODEX_PET_STATES } from "./codexPets";
import { appShellWebPreferences } from "./appShellGuards";
import { getMainLocale, mainTranslate } from "./i18n";

export type CodexPetView = {
  spritesheetURL: string;
  mood: CodexPetMood;
  label: string;
  // The conversations shown as cards; see CodexPetWindowManager.visibleHints.
  hints: CodexPetHint[];
  layout: CodexPetBubbleLayout;
  // Whether the dock shows the composer (the panel is open).
  expanded: boolean;
  // Name of the workspace a new conversation from the open panel starts
  // in; empty while collapsed or when the main window has named none.
  workspaceName: string;
  // Render-time sprite geometry — pre-computed at view-build time so the
  // HTML template and the JS bridge don't have to know about CodexPetSize.
  size: CodexPetSize;
  spriteWidth: number;
  spriteHeight: number;
  scale: number;
  // Horizontal sprite offset from the window center; see spriteShift on
  // CodexPetLayoutDecision.
  spriteShift: number;
};

export type CodexPetBubbleLayout = "above" | "right" | "below" | "left" | "hidden";

export type CodexPetLayoutDecision = {
  layout: CodexPetBubbleLayout;
  bounds: Rectangle;
  // Set when a centered window slid sideways to fit its work area: the
  // sprite sits this many px right of the window center so it keeps its place.
  spriteShift?: number;
};

// The pet lives in its own frameless always-on-top window so it survives the
// main window being hidden or minimized. The window holds a pet column (the
// sprite with a dock under it) and, when there is something to show, a stack
// of conversation cards in the chosen layout direction. The anchor all
// geometry keeps fixed is the sprite's bottom-center on screen.
const CODEX_PET_SCREEN_INSET = 24;

// Cards: one per surfaced conversation, up to CODEX_PET_HINTS_MAX. Each is a
// fixed two-line card (status dot and title, then the latest stable
// commentary, each ellipsized) so the stack height is a pure function of the
// card count. The pet's job is a glance, not a paragraph.
const CODEX_PET_CARD_WIDTH = 280;
const CODEX_PET_CARD_HEIGHT = 52;
const CODEX_PET_CARD_GAP = 6;
// Transparent room around the window content for shadows and the dismiss
// button that overhangs a card's corner.
const CODEX_PET_WINDOW_PADDING = 8;
const CODEX_PET_BUBBLE_GAP = 8;

// The dock under the sprite: a small handle at rest, a toolbar while the
// pointer is over the pet, and the one-line composer while the panel is
// open. Its slot has the same height in every state, so opening the composer
// never moves the sprite or pushes the window past the bottom of the screen.
const CODEX_PET_DOCK_GAP = 4;
const CODEX_PET_DOCK_HEIGHT = 36;
const CODEX_PET_TOOLBAR_WIDTH = 64;
const CODEX_PET_COMPOSER_WIDTH = CODEX_PET_CARD_WIDTH;
// Window space below the anchor: the dock slot and the padding under it.
const CODEX_PET_BELOW_ANCHOR =
  CODEX_PET_DOCK_GAP + CODEX_PET_DOCK_HEIGHT + CODEX_PET_WINDOW_PADDING;
// Bounds the text one submit carries; the composer enforces it as maxlength.
export const CODEX_PET_SUBMIT_MAX = 4000;

// Card stack footprint. Layout math (window bounds, direction picking) sizes
// the window around this, so it must stay in lockstep with the inline CSS;
// the trailing 1px is slack for subpixel rounding.
export function codexPetCardsSize(count: number): { width: number; height: number } {
  const cards = Math.max(1, Math.min(CODEX_PET_HINTS_MAX, count));
  return {
    width: CODEX_PET_CARD_WIDTH,
    height: CODEX_PET_CARD_HEIGHT * cards + CODEX_PET_CARD_GAP * (cards - 1) + 1,
  };
}
// Card swap animation: when the cards change, the stack fades out, swaps
// content, and fades back in. Asymmetric timings so the fade-out feels
// decisive and the fade-in is gentle — total cycle ~320ms.
const CODEX_PET_BUBBLE_SWAP_OUT_MS = 140;
const CODEX_PET_BUBBLE_SWAP_IN_MS = 180;

// The sprite is rendered from the 192×208 layout box via CSS transform:
// scale(<size>). At the default (100%) size the transform is 0.5, so the
// base rendered footprint is 96×104; the per-size helper below recomputes
// the footprint against the user's chosen multiplier.
const CODEX_PET_SPRITE_RENDER_WIDTH_BASE = CODEX_PET_CELL_WIDTH / 2; // 96
const CODEX_PET_SPRITE_RENDER_HEIGHT_BASE = CODEX_PET_CELL_HEIGHT / 2; // 104
const CODEX_PET_SPRITE_BASE_SCALE = 0.5;

// With no cards the window is the pet column plus constant chrome (hit-test
// room and room for the sprite's drop shadow) that doesn't grow with the pet.
const CODEX_PET_WINDOW_HORIZONTAL_PADDING = 24; // 12px each side
const CODEX_PET_WINDOW_TOP_PADDING = 16;

export type CodexPetRenderedSprite = {
  width: number;
  height: number;
  scale: number;
};

// Maps a size level to its rendered footprint + the CSS transform that
// produces it. Driven by CODEX_PET_SIZE_OPTIONS so the menu, the
// persisted settings, and the geometry stay in sync.
export function codexPetRenderedSpriteForSize(
  size: CodexPetSize,
): CodexPetRenderedSprite {
  const option =
    CODEX_PET_SIZE_OPTIONS.find((entry) => entry.id === size) ??
    CODEX_PET_SIZE_OPTIONS.find((entry) => entry.id === CODEX_PET_SIZE_DEFAULT)!;
  return codexPetRenderedSpriteForScale(CODEX_PET_SPRITE_BASE_SCALE * option.multiplier);
}

// Build rendered sprite geometry from a raw scale value. Used by the
// continuous resize path: when the user drags a resize handle the scale
// moves smoothly between the preset extremes and we need geometry for any
// value, not just the four preset ids.
export function codexPetRenderedSpriteForScale(
  scale: number,
): CodexPetRenderedSprite {
  const multiplier = scale / CODEX_PET_SPRITE_BASE_SCALE;
  return {
    width: Math.round(CODEX_PET_SPRITE_RENDER_WIDTH_BASE * multiplier),
    height: Math.round(CODEX_PET_SPRITE_RENDER_HEIGHT_BASE * multiplier),
    scale,
  };
}

function renderedSprite(size: CodexPetSize, customScale: number | undefined): CodexPetRenderedSprite {
  return typeof customScale === "number" && Number.isFinite(customScale) && customScale > 0
    ? codexPetRenderedSpriteForScale(customScale)
    : codexPetRenderedSpriteForSize(size);
}

// The sprite over its dock. The dock is as wide as the toolbar while the
// panel is closed and as wide as the composer while it is open.
function codexPetColumn(
  sprite: CodexPetRenderedSprite,
  expanded: boolean,
): { width: number; height: number } {
  return {
    width: Math.max(sprite.width, expanded ? CODEX_PET_COMPOSER_WIDTH : CODEX_PET_TOOLBAR_WIDTH),
    height: sprite.height + CODEX_PET_DOCK_GAP + CODEX_PET_DOCK_HEIGHT,
  };
}

// Sheets that extend the 9-row atlas with two rows of 16 look directions
// (clockwise from straight up). The page detects them from the image height.
const CODEX_PET_LOOK_FIRST_ROW = 9;
const CODEX_PET_LOOK_DIRECTIONS = 16;
const CODEX_PET_LOOK_SHEET_HEIGHT = (CODEX_PET_LOOK_FIRST_ROW + 2) * CODEX_PET_CELL_HEIGHT;
// The pet follows the cursor while it is idle, the cursor is within this
// distance of the sprite, and the cursor moved recently.
const CODEX_PET_LOOK_POLL_MS = 120;
const CODEX_PET_LOOK_RADIUS = 480;
const CODEX_PET_LOOK_IDLE_MS = 2000;

// One-shot animations the host can request, with how many passes each plays.
const CODEX_PET_REACTION_LOOPS: Record<CodexPetReaction | "waving", number> = {
  review: 3,
  failed: 3,
  waving: 2,
};

export function selectedCodexPet(snapshot: CodexPetsSnapshot | undefined): CodexPet | undefined {
  if (!snapshot?.enabled) {
    return undefined;
  }
  return snapshot.pets.find((pet) => pet.id === snapshot.selected_id) ?? snapshot.pets[0];
}

export function codexPetView(
  pet: CodexPet,
  {
    mood,
    hints,
    layout,
    expanded = false,
    workspaceName = "",
    size = CODEX_PET_SIZE_DEFAULT,
    customScale,
    spriteShift = 0,
  }: {
    mood: CodexPetMood;
    hints: CodexPetHint[];
    layout: CodexPetBubbleLayout;
    expanded?: boolean;
    workspaceName?: string;
    size?: CodexPetSize;
    customScale?: number;
    spriteShift?: number;
  },
): CodexPetView {
  // Continuous drag passes a raw `customScale` and we derive geometry
  // from it directly. Without it, fall back to the preset-derived
  // geometry.
  const rendered = renderedSprite(size, customScale);
  return {
    spritesheetURL: pet.spritesheet_url,
    mood,
    label: pet.display_name,
    hints,
    layout,
    expanded,
    workspaceName,
    size,
    spriteWidth: rendered.width,
    spriteHeight: rendered.height,
    scale: rendered.scale,
    spriteShift,
  };
}

export type CodexPetAction =
  | { action: "menu" }
  | { action: "jump"; thread_id: string }
  | { action: "resize"; id: CodexPetSize }
  | { action: "scale"; value: number; commit: boolean }
  | { action: "panel"; open: boolean }
  | { action: "submit"; text: string; thread_id?: string }
  | { action: "dismiss"; thread_id: string }
  | { action: "show" }
  | { action: "look"; capable: boolean }
  | undefined;

// Continuous resize pushes the live scale as a number. Clamp the parser
// range so a malformed URL can't drag the pet off-screen (or under
// 1px) even if the inline script's client-side clamp ever drifts.
export const CODEX_PET_SCALE_MIN = 0.25;
export const CODEX_PET_SCALE_MAX = 1.5;

export function codexPetActionFromURL(rawURL: string): CodexPetAction {
  try {
    const url = new URL(rawURL);
    if (url.protocol !== "wuu-pet:" || url.hostname !== "action") return undefined;
    const actionName = url.pathname.replace(/^\/+/, "");
    if (actionName === "menu") return { action: "menu" };
    if (actionName === "show") return { action: "show" };
    if (actionName === "jump" || actionName === "dismiss") {
      const threadId = url.searchParams.get("thread_id");
      if (!threadId) return undefined;
      return { action: actionName, thread_id: threadId };
    }
    if (actionName === "panel") {
      return { action: "panel", open: url.searchParams.get("open") === "1" };
    }
    if (actionName === "submit") {
      // Validation of the text itself happens in the manager, which must
      // answer every submit so the page never stays "sending".
      const threadId = url.searchParams.get("thread_id");
      return {
        action: "submit",
        text: url.searchParams.get("text") ?? "",
        ...(threadId ? { thread_id: threadId } : {}),
      };
    }
    if (actionName === "look") {
      return { action: "look", capable: url.searchParams.get("capable") === "1" };
    }
    if (actionName === "resize") {
      // Only forward ids the preset list knows about. A stale page that names
      // an id the type system doesn't model falls through to undefined so the
      // navigate handler treats it as a no-op instead of crashing setSize.
      const id = url.searchParams.get("id");
      if (!id) return undefined;
      const known = CODEX_PET_SIZE_OPTIONS.some((option) => option.id === id);
      if (!known) return undefined;
      return { action: "resize", id: id as CodexPetSize };
    }
    if (actionName === "scale") {
      // Live drag emits a float; drop anything non-finite and clamp the
      // accepted range. Without the clamp a hostile or buggy page could
      // push values like 1e9 or 0 and resize the pet into uselessness.
      // `commit=1` marks the pointerup emission: same geometry update,
      // but the manager also notifies the host so the value persists.
      const valueStr = url.searchParams.get("value");
      if (!valueStr) return undefined;
      const value = parseFloat(valueStr);
      if (!Number.isFinite(value)) return undefined;
      const clamped = Math.max(
        CODEX_PET_SCALE_MIN,
        Math.min(CODEX_PET_SCALE_MAX, value),
      );
      return {
        action: "scale",
        value: clamped,
        commit: url.searchParams.get("commit") === "1",
      };
    }
    return undefined;
  } catch {
    return undefined;
  }
}

// Compute window bounds for a layout, keeping the sprite's bottom-center
// (the `anchor`) at the same screen point. `hidden` is the pet column alone;
// the other layouts put the card stack (`bubble`) beside the column. The
// returned bounds are not yet clamped to the workArea; the caller decides
// whether the layout fits before applying. `size` defaults to the 100%
// preset; `expanded` widens the dock to the composer.
export function codexPetBoundsForLayout({
  layout,
  anchor,
  bubble = { width: 0, height: 0 },
  expanded = false,
  size = CODEX_PET_SIZE_DEFAULT,
  customScale,
}: {
  layout: CodexPetBubbleLayout;
  anchor: { x: number; y: number };
  bubble?: { width: number; height: number };
  expanded?: boolean;
  size?: CodexPetSize;
  customScale?: number;
}): Rectangle {
  const sprite = renderedSprite(size, customScale);
  const column = codexPetColumn(sprite, expanded);
  const pad = CODEX_PET_WINDOW_PADDING;
  const gap = CODEX_PET_BUBBLE_GAP;
  switch (layout) {
    case "hidden": {
      const width = column.width + CODEX_PET_WINDOW_HORIZONTAL_PADDING;
      const height = CODEX_PET_WINDOW_TOP_PADDING + sprite.height + CODEX_PET_BELOW_ANCHOR;
      return {
        x: Math.round(anchor.x - width / 2),
        y: Math.round(anchor.y - height + CODEX_PET_BELOW_ANCHOR),
        width,
        height,
      };
    }
    case "above": {
      const width = Math.max(column.width, bubble.width) + 2 * pad;
      const height = pad + bubble.height + gap + column.height + pad;
      return {
        x: Math.round(anchor.x - width / 2),
        y: Math.round(anchor.y - height + CODEX_PET_BELOW_ANCHOR),
        width,
        height,
      };
    }
    case "below": {
      const width = Math.max(column.width, bubble.width) + 2 * pad;
      const height = pad + column.height + gap + bubble.height + pad;
      return {
        x: Math.round(anchor.x - width / 2),
        y: Math.round(anchor.y - sprite.height - pad),
        width,
        height,
      };
    }
    case "right":
    case "left": {
      const width = pad + column.width + gap + bubble.width + pad;
      const height = Math.max(column.height, bubble.height) + 2 * pad;
      const columnCenter = pad + column.width / 2;
      return {
        x: Math.round(anchor.x - (layout === "right" ? columnCenter : width - columnCenter)),
        y: Math.round(anchor.y - height + CODEX_PET_BELOW_ANCHOR),
        width,
        height,
      };
    }
  }
}

// Choose the first layout whose bounds fully fit inside the given workArea:
// cards above the pet ("pet 头上") first, then right, then left/below as
// fallbacks. Without cards (`bubble` undefined) only the pet column is
// placed. Near a side edge a centered window may not fit, which would hide
// even an open composer, so a centered layout slides along the edge as long
// as the sprite stays inside it. When nothing fits, the result is the
// unclamped pet column: the user may have dragged the pet partly off screen.
export function selectCodexPetBubbleLayout({
  workArea,
  anchor,
  bubble,
  expanded = false,
  size = CODEX_PET_SIZE_DEFAULT,
  customScale,
}: {
  workArea: Rectangle;
  anchor: { x: number; y: number };
  bubble?: { width: number; height: number };
  expanded?: boolean;
  size?: CodexPetSize;
  customScale?: number;
}): CodexPetLayoutDecision {
  const sprite = renderedSprite(size, customScale);
  const boundsFor = (layout: CodexPetBubbleLayout) =>
    codexPetBoundsForLayout({ layout, anchor, bubble, expanded, size, customScale });
  const fits = (bounds: Rectangle) =>
    bounds.x >= workArea.x &&
    bounds.y >= workArea.y &&
    bounds.x + bounds.width <= workArea.x + workArea.width &&
    bounds.y + bounds.height <= workArea.y + workArea.height;
  if (bubble) {
    for (const layout of ["above", "right", "left", "below"] as const) {
      const bounds = boundsFor(layout);
      if (fits(bounds)) return { layout, bounds };
    }
  }
  const centered: CodexPetBubbleLayout[] = bubble ? ["above", "below", "hidden"] : ["hidden"];
  for (const layout of centered) {
    const bounds = boundsFor(layout);
    const x = Math.min(
      Math.max(bounds.x, workArea.x),
      workArea.x + workArea.width - bounds.width,
    );
    const spriteShift = bounds.x - x;
    const slid = { ...bounds, x };
    if (fits(slid) && Math.abs(spriteShift) <= (bounds.width - sprite.width) / 2) {
      return spriteShift ? { layout, bounds: slid, spriteShift } : { layout, bounds };
    }
  }
  return { layout: "hidden", bounds: boundsFor("hidden") };
}

// Escapes JSON for embedding in an inline <script>: `<` becomes a plain JS
// string escape, so pushed text can never terminate the block (e.g. a
// preview containing "</script>") while the parsed value stays unchanged.
function scriptJSON(value: unknown): string {
  return JSON.stringify(value).replace(/</g, "\\u003c");
}

// Line icons on a 16px grid, stroked in the current text color.
const CODEX_PET_ICON_PATHS = {
  compose:
    '<path d="M8.5 3h-4A1.5 1.5 0 0 0 3 4.5v7A1.5 1.5 0 0 0 4.5 13h7a1.5 1.5 0 0 0 1.5-1.5v-4"/><path d="M11.8 2.6a1.3 1.3 0 0 1 1.8 1.8L8.4 9.6 6 10l.4-2.4z"/>',
  open:
    '<path d="M9 3h4v4"/><path d="M13 3 7.5 8.5"/><path d="M11 9.5v2A1.5 1.5 0 0 1 9.5 13h-5A1.5 1.5 0 0 1 3 11.5v-5A1.5 1.5 0 0 1 4.5 5h2"/>',
  reply: '<path d="M6.5 4 3 7.5 6.5 11"/><path d="M3 7.5h6.5A3.5 3.5 0 0 1 13 11v1"/>',
  plus: '<path d="M8 3.5v9M3.5 8h9"/>',
  send: '<path d="M8 13V3.5"/><path d="M4 7.5 8 3.5l4 4"/>',
  close: '<path d="M5 5l6 6M11 5l-6 6"/>',
} as const;

function codexPetIcon(name: keyof typeof CODEX_PET_ICON_PATHS, size: number): string {
  return `<svg viewBox="0 0 16 16" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${CODEX_PET_ICON_PATHS[name]}</svg>`;
}

export function codexPetWindowHTML(
  view: CodexPetView,
  locale: AppLocale = getMainLocale(),
): string {
  const resizeLabel = mainTranslate("resize", {}, locale);
  // `{title}` and `{workspace}` placeholders survive translation and are
  // filled in the page.
  const text = {
    conversation: mainTranslate("conversation", {}, locale),
    openConversation: mainTranslate("openConversation", {}, locale),
    newConversation: mainTranslate("petNewConversation", {}, locale),
    noWorkspace: mainTranslate("petNoWorkspace", {}, locale),
    replyTo: mainTranslate("petReplyTo", {}, locale),
    answer: mainTranslate("petAnswer", {}, locale),
    cancelReply: mainTranslate("petCancelReply", {}, locale),
    placeholder: mainTranslate("petInputPlaceholder", {}, locale),
    needsAnswer: mainTranslate("petNeedsAnswer", {}, locale),
    send: mainTranslate("petSend", {}, locale),
    sending: mainTranslate("petSending", {}, locale),
    sendFailed: mainTranslate("petSendFailed", {}, locale),
    dismiss: mainTranslate("petDismiss", {}, locale),
  };
  const newChatLabel = escapeHTML(mainTranslate("petNewChat", {}, locale));
  const showAppLabel = escapeHTML(mainTranslate("petShowApp", {}, locale));
  const icons = {
    reply: codexPetIcon("reply", 14),
    plus: codexPetIcon("plus", 12),
    close: codexPetIcon("close", 10),
  };
  const states = Object.fromEntries(
    CODEX_PET_STATES.map((state) => [state.id, { row: state.row, durations: state.durations }]),
  );
  const styleVars = [
    `--pet-sheet:url('${view.spritesheetURL}')`,
    `--pet-frame-width:${view.spriteWidth}px`,
    `--pet-frame-height:${view.spriteHeight}px`,
    `--pet-scale:${view.scale}`,
    `--sprite-shift:${view.spriteShift}px`,
  ].join(";");
  const resizeHandles = [
    ["corner-nw", -1, -1],
    ["corner-ne", 1, -1],
    ["corner-sw", -1, 1],
    ["corner-se", 1, 1],
    ["edge-top", 0, -1],
    ["edge-bottom", 0, 1],
    ["edge-left", -1, 0],
    ["edge-right", 1, 0],
  ]
    .map(
      ([name, x, y]) =>
        `<div class="resize-handle ${name}" data-dir-x="${x}" data-dir-y="${y}" aria-label="${escapeHTML(resizeLabel)}"></div>`,
    )
    .join("\n");
  const pad = CODEX_PET_WINDOW_PADDING;
  return `<!doctype html>
<html lang="${locale}"><head><meta charset="utf-8" />
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src wuu-file:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; navigate-to wuu-pet:" />
<meta name="viewport" content="width=device-width,initial-scale=1" />
<style>
*{box-sizing:border-box}
html,body{width:100%;height:100%;margin:0;background:transparent;overflow:hidden;font:13px/18px -apple-system,BlinkMacSystemFont,system-ui,sans-serif}
/* Graphite ink on neutral glass, matching the app's monochrome theme: ink is
   the only accent, and status hues stay desaturated so the pet reads as part
   of the desktop rather than a notification. */
.stage{--surface:rgba(255,255,255,.9);--fg:#111315;--muted:#6f7478;--edge:rgba(17,19,21,.09);--shadow:0 0 0 .5px var(--edge),0 1px 2px rgba(0,0,0,.05),0 10px 28px -8px rgba(0,0,0,.18);--hover-bg:rgba(31,35,40,.06);--accent:#1f2328;--accent-fg:#fff;--target-bg:rgba(31,35,40,.08);--dot-idle:#b0b6bb;--dot-running:#5b7fb8;--dot-done:#4f8f6c;--dot-failed:#c0533f;--dot-review:#b9842c;--dock-width:${CODEX_PET_TOOLBAR_WIDTH}px}
@media (prefers-color-scheme:dark){.stage{--surface:rgba(36,36,41,.92);--fg:#e5e7e9;--muted:#a1a8ae;--edge:rgba(255,255,255,.1);--shadow:inset 0 0 0 .5px var(--edge),0 0 0 .5px rgba(0,0,0,.5),0 10px 28px -8px rgba(0,0,0,.55);--hover-bg:rgba(220,228,235,.08);--accent:#e5e7e9;--accent-fg:#111315;--target-bg:rgba(220,228,235,.12);--dot-idle:#70777e;--dot-running:#8fade0;--dot-done:#72b892;--dot-failed:#e3826f;--dot-review:#d8ad5e}}
.stage.is-expanded{--dock-width:${CODEX_PET_COMPOSER_WIDTH}px}
.stage{position:relative;width:100%;height:100%;padding:${pad}px;display:flex;flex-direction:column;align-items:center;justify-content:flex-end;cursor:grab;-webkit-user-select:none;user-select:none}
.stage.is-dragging{cursor:grabbing}
.stage[data-layout=hidden]{padding:0 0 ${pad}px}
.stage[data-layout=below]{flex-direction:column-reverse}
.stage[data-layout=right]{flex-direction:row-reverse;justify-content:flex-end;align-items:flex-end}
.stage[data-layout=left]{flex-direction:row;justify-content:flex-start;align-items:flex-end}
button{font:inherit;color:inherit}
button:focus-visible,.card:focus-visible{outline:2px solid var(--accent);outline-offset:1px}
.bubble{width:${CODEX_PET_CARD_WIDTH}px;flex-shrink:0;margin-bottom:${CODEX_PET_BUBBLE_GAP}px;text-align:left}
.stage[data-layout=below] .bubble{margin:${CODEX_PET_BUBBLE_GAP}px 0 0}
.stage[data-layout=right] .bubble{margin:0 0 0 ${CODEX_PET_BUBBLE_GAP}px}
.stage[data-layout=left] .bubble{margin:0 ${CODEX_PET_BUBBLE_GAP}px 0 0}
.bubble[hidden],.stage[data-layout=hidden] .bubble{display:none}
.cards{display:flex;flex-direction:column;gap:${CODEX_PET_CARD_GAP}px}
.card{position:relative;display:flex;align-items:center;gap:8px;height:${CODEX_PET_CARD_HEIGHT}px;padding:8px 10px 8px 12px;border-radius:14px;background:var(--surface);color:var(--fg);box-shadow:var(--shadow);cursor:pointer;opacity:1;transition:opacity ${CODEX_PET_BUBBLE_SWAP_IN_MS}ms ease,box-shadow 120ms ease}
.cards.is-swapping .card{opacity:0;transition:opacity ${CODEX_PET_BUBBLE_SWAP_OUT_MS}ms ease}
.stage.is-expanded .card.is-target{box-shadow:0 0 0 1.5px var(--accent),var(--shadow)}
.card-body{flex:1;min-width:0}
.card-head{display:flex;align-items:center;gap:7px;height:18px}
.dot{width:7px;height:7px;border-radius:50%;flex-shrink:0;background:var(--dot-idle)}
.card[data-status=running] .dot{background:var(--dot-running);animation:pulse 1.6s ease-in-out infinite}
.card[data-status=done] .dot{background:var(--dot-done)}
.card[data-status=failed] .dot{background:var(--dot-failed)}
.card[data-status=needs_review] .dot{background:var(--dot-review)}
@keyframes pulse{50%{opacity:.3}}
.card-title{min-width:0;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.card-preview{height:18px;padding-left:14px;color:var(--muted);font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.card-action{display:none;flex-shrink:0;align-items:center;justify-content:center;min-width:28px;height:28px;padding:0 6px;border:0;border-radius:14px;background:var(--hover-bg);font-size:12px;cursor:pointer}
.card-action:hover{background:var(--target-bg)}
.card-action[data-action=answer]{display:flex;padding:0 11px;background:var(--accent);color:var(--accent-fg);font-weight:500}
.card-action[data-action=answer]:hover{background:var(--accent);opacity:.86}
.card:hover .card-action,.card:focus-within .card-action{display:flex}
.card-dismiss{position:absolute;top:-6px;left:-6px;display:none;align-items:center;justify-content:center;width:18px;height:18px;padding:0;border:0;border-radius:50%;background:var(--surface);color:var(--muted);box-shadow:var(--shadow);cursor:pointer}
.card-dismiss:hover{color:var(--fg)}
.stage:not(.is-expanded) .card:hover .card-dismiss{display:flex}
.pet{position:relative;display:flex;flex-direction:column;align-items:center;flex-shrink:0;width:max(var(--pet-frame-width),var(--dock-width))}
.sprite-frame{position:relative;left:var(--sprite-shift);width:var(--pet-frame-width);height:var(--pet-frame-height);display:flex;align-items:flex-end;justify-content:center;flex-shrink:0}
.sprite{width:${CODEX_PET_CELL_WIDTH}px;height:${CODEX_PET_CELL_HEIGHT}px;flex-shrink:0;transform:scale(var(--pet-scale));transform-origin:bottom center;background-image:var(--pet-sheet);background-repeat:no-repeat;background-position:0 0;image-rendering:pixelated;filter:drop-shadow(0 10px 16px rgba(0,0,0,.18));pointer-events:none}
.badge{position:absolute;top:0;right:0;min-width:18px;height:18px;padding:0 5px;border-radius:9px;background:var(--accent);color:var(--accent-fg);box-shadow:var(--shadow);font-size:11px;font-weight:600;line-height:18px;text-align:center;pointer-events:none}
.badge.is-urgent{background:var(--dot-review);color:#fff}
.badge[hidden]{display:none}
/* The dock follows the sprite when the window slid along a screen edge, but
   never past the window's padding. */
.dock{position:relative;left:clamp(calc((var(--dock-width) - 100vw) / 2 + ${pad}px),var(--sprite-shift),calc((100vw - var(--dock-width)) / 2 - ${pad}px));display:flex;align-items:center;justify-content:center;flex-shrink:0;width:var(--dock-width);height:${CODEX_PET_DOCK_HEIGHT}px;margin-top:${CODEX_PET_DOCK_GAP}px}
.dock-handle{width:28px;height:6px;border-radius:3px;background:var(--surface);box-shadow:var(--shadow);transition:opacity 120ms ease}
.toolbar{position:absolute;display:flex;gap:2px;padding:3px;border-radius:16px;background:var(--surface);color:var(--fg);box-shadow:var(--shadow);opacity:0;visibility:hidden;transform:scale(.85);transition:opacity 120ms ease,transform 120ms ease,visibility 0s linear 120ms}
.pet:hover .toolbar,.toolbar:focus-within{opacity:1;visibility:visible;transform:none;transition:opacity 120ms ease,transform 120ms ease}
.pet:hover .dock-handle,.toolbar:focus-within+.dock-handle{opacity:0}
.stage.is-dragging .toolbar{opacity:0;visibility:hidden}
.stage.is-dragging .dock-handle{opacity:1}
.tool{display:flex;align-items:center;justify-content:center;width:26px;height:26px;padding:0;border:0;border-radius:13px;background:transparent;cursor:pointer}
.tool:hover{background:var(--hover-bg)}
.composer{position:absolute;inset:0;display:none;align-items:center;gap:6px;padding:0 4px 0 4px;border-radius:18px;background:var(--surface);color:var(--fg);box-shadow:var(--shadow);cursor:text}
.stage.is-expanded .composer{display:flex;animation:dock-open 140ms ease-out}
.stage.is-expanded .dock-handle,.stage.is-expanded .toolbar{display:none}
@keyframes dock-open{from{opacity:0;transform:scale(.92)}}
.composer-target{display:flex;align-items:center;gap:4px;flex-shrink:1;min-width:0;max-width:112px;height:28px;padding:0 9px 0 7px;border:0;border-radius:14px;background:var(--hover-bg);color:var(--muted);font-size:12px;white-space:nowrap;cursor:default}
.target-icon{display:flex;align-items:center;justify-content:center;flex-shrink:0}
.composer-target svg{display:block;flex-shrink:0}
.composer-target.is-reply{background:var(--target-bg);color:var(--fg);cursor:pointer}
.composer-target[hidden]{display:none}
.target-label{min-width:0;overflow:hidden;text-overflow:ellipsis}
.composer input{flex:1;min-width:0;height:100%;margin:0;padding:0 2px;border:0;background:transparent;color:inherit;font:inherit;outline:none;cursor:text;-webkit-user-select:text;user-select:text}
.composer input::placeholder{color:var(--muted)}
.composer input:disabled{cursor:default}
.composer-send{display:flex;align-items:center;justify-content:center;flex-shrink:0;width:28px;height:28px;padding:0;border:0;border-radius:50%;background:var(--accent);color:var(--accent-fg);cursor:pointer}
.composer-send:disabled{opacity:.35;cursor:default}
.composer-send.is-sending{opacity:1}
.composer-send.is-sending svg{display:none}
.composer-send.is-sending::after{content:"";width:12px;height:12px;border:2px solid currentColor;border-right-color:transparent;border-radius:50%;animation:spin .8s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
.composer-status{position:absolute;left:50%;bottom:calc(100% + 6px);max-width:100%;padding:3px 10px;border-radius:11px;background:var(--surface);box-shadow:var(--shadow);font-size:12px;line-height:16px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;transform:translateX(-50%);pointer-events:none}
.composer-status.is-error{color:var(--dot-failed)}
.composer-status.is-blocked{color:var(--dot-review)}
.composer-status[hidden]{display:none}
.resize-handle{position:absolute;z-index:2}
.stage.is-expanded .resize-handle{display:none}
.resize-handle.edge-top{top:-6px;left:10px;right:10px;height:12px;cursor:ns-resize}
.resize-handle.edge-bottom{bottom:-4px;left:10px;right:10px;height:8px;cursor:ns-resize}
.resize-handle.edge-left{left:-6px;top:10px;bottom:10px;width:12px;cursor:ew-resize}
.resize-handle.edge-right{right:-6px;top:10px;bottom:10px;width:12px;cursor:ew-resize}
.resize-handle.corner-nw{top:-6px;left:-6px;width:16px;height:16px;cursor:nwse-resize}
.resize-handle.corner-ne{top:-6px;right:-6px;width:16px;height:16px;cursor:nesw-resize}
.resize-handle.corner-sw{bottom:-4px;left:-6px;width:16px;height:14px;cursor:nesw-resize}
.resize-handle.corner-se{bottom:-4px;right:-6px;width:16px;height:14px;cursor:nwse-resize}
@media (prefers-reduced-motion:reduce){.card,.cards.is-swapping .card,.dock-handle,.toolbar,.pet:hover .toolbar{transition:none}.card[data-status=running] .dot,.stage.is-expanded .composer,.composer-send.is-sending::after{animation:none}}
</style></head><body><div class="stage" data-layout="${escapeHTML(view.layout)}" style="${escapeHTML(styleVars)}">
<div class="bubble" hidden><div class="cards"></div></div>
<div class="pet">
<div class="sprite-frame"><div class="sprite" role="img" aria-label="${escapeHTML(view.label)}"></div><div class="badge" hidden></div>
${resizeHandles}
</div>
<div class="dock">
<div class="toolbar"><button type="button" class="tool" data-action="new" title="${newChatLabel}" aria-label="${newChatLabel}">${codexPetIcon("compose", 16)}</button><button type="button" class="tool" data-action="show" title="${showAppLabel}" aria-label="${showAppLabel}">${codexPetIcon("open", 16)}</button></div>
<div class="dock-handle"></div>
<div class="composer"><div class="composer-status" role="status" hidden></div><button type="button" class="composer-target"><span class="target-icon"></span><span class="target-label"></span></button><input type="text" maxlength="${CODEX_PET_SUBMIT_MAX}" spellcheck="false" autocomplete="off" /><button type="button" class="composer-send">${codexPetIcon("send", 16)}</button></div>
</div>
</div>
</div>
<script>
(() => {
  const stage = document.querySelector('.stage');
  const bubble = document.querySelector('.bubble');
  const cardsEl = bubble.querySelector('.cards');
  const composer = document.querySelector('.composer');
  const statusEl = composer.querySelector('.composer-status');
  const targetEl = composer.querySelector('.composer-target');
  const targetIcon = targetEl.querySelector('.target-icon');
  const targetLabel = targetEl.querySelector('.target-label');
  const input = composer.querySelector('input');
  const sendButton = composer.querySelector('.composer-send');
  const spriteFrame = document.querySelector('.sprite-frame');
  const sprite = document.querySelector('.sprite');
  const badge = document.querySelector('.badge');
  const STATES = ${scriptJSON(states)};
  const TEXT = ${scriptJSON(text)};
  const ICONS = ${scriptJSON(icons)};
  input.placeholder = TEXT.placeholder;
  const MOOD_STATES = { idle: 'idle', running: 'running', waiting: 'waiting' };
  const CELL_WIDTH = ${CODEX_PET_CELL_WIDTH};
  const CELL_HEIGHT = ${CODEX_PET_CELL_HEIGHT};
  const LOOK_FIRST_ROW = ${CODEX_PET_LOOK_FIRST_ROW};
  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
  const fill = (template, value) => template.replace(/[{][a-z]+[}]/i, () => value);
  const send = (path) => { location.href = 'wuu-pet://action/' + path; };
  let view = ${scriptJSON(view)};

  // Animation. One sequence is on screen at a time, chosen by priority:
  // dragging > a one-shot (reaction or interaction) > looking at the
  // cursor (idle only) > the mood loop. Frames advance on per-frame
  // durations from the atlas, so held poses read as poses.
  let mood = view.mood;
  let oneShot = null;
  let dragState = null;
  let look = null;
  let shown = '';
  let frame = 0;
  let frameTimer = 0;
  const sequence = () =>
    dragState ||
    (oneShot && oneShot.id) ||
    (look !== null && mood === 'idle' ? 'look' : MOOD_STATES[mood] || 'idle');
  const paint = (row, column) => {
    sprite.style.backgroundPosition = (-column * CELL_WIDTH) + 'px ' + (-row * CELL_HEIGHT) + 'px';
  };
  const animate = () => {
    clearTimeout(frameTimer);
    const key = sequence();
    if (key !== shown) {
      shown = key;
      frame = 0;
    }
    if (key === 'look') {
      paint(LOOK_FIRST_ROW + Math.floor(look / 8), look % 8);
      return;
    }
    const state = STATES[key];
    if (reducedMotion.matches) {
      // Hold the first frame; a one-shot still expires on its own schedule.
      paint(state.row, 0);
      if (oneShot) {
        const total = state.durations.reduce((sum, ms) => sum + ms, 0) * oneShot.loops;
        frameTimer = setTimeout(() => { oneShot = null; animate(); }, total);
      }
      return;
    }
    paint(state.row, frame);
    frameTimer = setTimeout(() => {
      frame += 1;
      if (frame >= state.durations.length) {
        frame = 0;
        if (oneShot && oneShot.id === key && --oneShot.loops <= 0) oneShot = null;
      }
      animate();
    }, state.durations[frame]);
  };
  // Reactions the user caused or the host reports interrupt whatever
  // one-shot is playing; ambient ones (hover, fidget) never do.
  const play = (id, loops, interrupt) => {
    if (!STATES[id] || (oneShot && !interrupt)) return;
    oneShot = { id, loops };
    shown = '';
    animate();
  };
  window.wuuPetReact = (id, loops) => play(id, loops, true);
  window.wuuPetLook = (index) => {
    look = typeof index === 'number' ? index : null;
    if (sequence() !== shown || shown === 'look') animate();
  };
  reducedMotion.addEventListener('change', animate);
  // Report whether this sheet has the look-direction rows, so the host only
  // tracks the cursor for pets that can use it.
  const probeSheet = (url) => {
    const probe = new Image();
    probe.onload = () => {
      if (view.spritesheetURL === url) send('look?capable=' + (probe.naturalHeight >= ${CODEX_PET_LOOK_SHEET_HEIGHT} ? '1' : '0'));
    };
    probe.src = url;
  };
  const scheduleFidget = () => {
    setTimeout(() => {
      if (mood === 'idle' && look === null && !expanded && !dragState) {
        play(Math.random() < 0.5 ? 'waving' : 'jumping', 1, false);
      }
      scheduleFidget();
    }, 60000 + Math.random() * 90000);
  };

  // Cards. Text is set via textContent, never innerHTML, so pushed strings
  // can't inject markup; only the constant icons are parsed. Content changes
  // fade the stack out, rebuild, and fade back in; cancelSwap clears a
  // pending swap so a fast second push doesn't strand cards mid-fade.
  let currentHints = [];
  let pendingSwap = null;
  const SWAP_OUT_MS = ${CODEX_PET_BUBBLE_SWAP_OUT_MS};
  const hintsSignature = (hints) =>
    JSON.stringify(hints.map((h) => [h.thread_id, h.title, h.preview, h.status]));
  const iconButton = (className, icon, label) => {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = className;
    button.innerHTML = icon;
    button.title = label;
    button.setAttribute('aria-label', label);
    return button;
  };
  const buildCards = (hints) => {
    cardsEl.textContent = '';
    for (const hint of hints) {
      const title = hint.title || TEXT.conversation;
      const card = document.createElement('div');
      card.className = 'card';
      card.dataset.threadId = hint.thread_id;
      card.dataset.status = hint.status;
      card.setAttribute('role', 'button');
      card.title = fill(TEXT.openConversation, title);
      const body = document.createElement('div');
      body.className = 'card-body';
      const head = document.createElement('div');
      head.className = 'card-head';
      const dot = document.createElement('span');
      dot.className = 'dot';
      const titleEl = document.createElement('span');
      titleEl.className = 'card-title';
      titleEl.textContent = title;
      head.append(dot, titleEl);
      const preview = document.createElement('div');
      preview.className = 'card-preview';
      preview.textContent = hint.preview;
      body.append(head, preview);
      // A conversation waiting on the user is answered in Wuu, not from
      // here, so its card offers the way there instead of a reply.
      const answer = hint.status === 'needs_review';
      let action;
      if (answer) {
        action = document.createElement('button');
        action.type = 'button';
        action.className = 'card-action';
        action.textContent = TEXT.answer;
        action.title = fill(TEXT.openConversation, title);
      } else {
        action = iconButton('card-action', ICONS.reply, fill(TEXT.replyTo, title));
      }
      action.dataset.action = answer ? 'answer' : 'reply';
      const dismiss = iconButton('card-dismiss', ICONS.close, TEXT.dismiss);
      card.append(body, action, dismiss);
      cardsEl.append(card);
    }
    syncComposer();
  };
  const cancelSwap = () => {
    if (pendingSwap === null) return;
    clearTimeout(pendingSwap);
    pendingSwap = null;
    cardsEl.classList.remove('is-swapping');
  };
  const applyHints = (hints, animated) => {
    cancelSwap();
    const usable = (hints || []).filter(
      (h) => h && typeof h.thread_id === 'string' && (h.preview || '').trim(),
    );
    if (hintsSignature(usable) === hintsSignature(currentHints)) return;
    const swap = () => {
      pendingSwap = null;
      cardsEl.classList.remove('is-swapping');
      currentHints = usable;
      buildCards(usable);
      bubble.hidden = usable.length === 0;
      syncBadge();
    };
    if (!animated || currentHints.length === 0 || reducedMotion.matches) {
      swap();
      return;
    }
    cardsEl.classList.add('is-swapping');
    pendingSwap = setTimeout(swap, SWAP_OUT_MS);
  };
  // With the cards off screen (no room for any layout), a badge on the
  // sprite counts the conversations that want attention.
  const syncBadge = () => {
    const attention = currentHints.filter((h) => h.attention);
    badge.hidden = view.layout !== 'hidden' || attention.length === 0;
    badge.textContent = String(attention.length);
    badge.classList.toggle(
      'is-urgent',
      attention.some((h) => h.status === 'needs_review' || h.status === 'failed'),
    );
  };

  // Composer. A submit starts a new conversation in the workspace the host
  // captured when the panel opened (view.workspaceName), or replies to the
  // card whose reply action the user pressed. Both are fixed before the
  // user types. A reply target keeps its title after its card leaves the
  // stack so a send is never silently redirected.
  let expanded = false;
  let reply = null;
  let sending = false;
  let failed = false;
  const syncComposer = () => {
    const replyHint = reply && currentHints.find((h) => h.thread_id === reply.id);
    const blocked = Boolean(replyHint && replyHint.status === 'needs_review');
    const unplaced = !reply && !view.workspaceName;
    for (const card of cardsEl.children) {
      card.classList.toggle('is-target', Boolean(reply) && card.dataset.threadId === reply.id);
      card.tabIndex = expanded ? 0 : -1;
      for (const button of card.querySelectorAll('button')) button.tabIndex = expanded ? 0 : -1;
    }
    // A blocked reply keeps any draft in the disabled input, so the reason
    // floats above the composer rather than in the placeholder.
    const status = failed ? TEXT.sendFailed : sending ? '' : blocked ? TEXT.needsAnswer : unplaced ? TEXT.noWorkspace : '';
    statusEl.textContent = status;
    statusEl.hidden = !status;
    statusEl.classList.toggle('is-error', failed);
    statusEl.classList.toggle('is-blocked', !failed && blocked);
    targetEl.hidden = unplaced;
    targetEl.classList.toggle('is-reply', Boolean(reply));
    targetIcon.innerHTML = reply ? ICONS.reply : ICONS.plus;
    targetLabel.textContent = reply ? reply.title : view.workspaceName;
    const targetText = reply ? fill(TEXT.replyTo, reply.title) : fill(TEXT.newConversation, view.workspaceName);
    targetEl.title = reply ? targetText + ' · ' + TEXT.cancelReply : targetText;
    targetEl.setAttribute('aria-label', targetEl.title);
    input.disabled = blocked || unplaced;
    input.readOnly = sending;
    sendButton.classList.toggle('is-sending', sending);
    sendButton.disabled = sending || input.disabled || !input.value.trim();
    sendButton.title = sending ? TEXT.sending : TEXT.send;
    sendButton.setAttribute('aria-label', sendButton.title);
  };
  const setExpanded = (next) => {
    if (next === expanded) return;
    expanded = next;
    stage.classList.toggle('is-expanded', expanded);
    if (expanded) {
      requestAnimationFrame(() => { if (expanded && !input.disabled) input.focus(); });
    } else {
      // Cards leave the tab order when collapsed; drop focus too so a clicked
      // card does not keep its focus ring in the glance stack.
      if (stage.contains(document.activeElement)) document.activeElement.blur();
      if (!sending) {
        reply = null;
        failed = false;
      }
    }
    syncComposer();
  };
  const setReply = (next) => {
    if (sending) return;
    reply = next;
    failed = false;
    syncComposer();
    if (expanded && !input.disabled) input.focus();
  };
  const openPanel = () => { if (!expanded) send('panel?open=1'); };
  const jump = (card) => send('jump?thread_id=' + encodeURIComponent(card.dataset.threadId));
  const submit = () => {
    const text = input.value.trim();
    if (!text || sending || input.disabled) return;
    sending = true;
    failed = false;
    syncComposer();
    send('submit?text=' + encodeURIComponent(text) + (reply ? '&thread_id=' + encodeURIComponent(reply.id) : ''));
  };
  window.wuuPetSubmitResult = (result) => {
    if (!sending) return;
    sending = false;
    if (result && result.ok) {
      input.value = '';
      play('jumping', 1, true);
      if (expanded) send('panel?open=0');
    } else {
      failed = true;
      play('failed', 1, true);
    }
    syncComposer();
  };
  input.addEventListener('keydown', (event) => {
    // Enter while an IME composition is open confirms the composition.
    if (event.key !== 'Enter' || event.shiftKey || event.isComposing || event.keyCode === 229) return;
    event.preventDefault();
    submit();
  });
  input.addEventListener('input', () => {
    failed = false;
    syncComposer();
  });
  window.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && expanded) {
      event.preventDefault();
      send('panel?open=0');
      return;
    }
    const card = event.target instanceof Element && event.target.classList.contains('card') ? event.target : null;
    if (card && (event.key === 'Enter' || event.key === ' ')) {
      event.preventDefault();
      jump(card);
    }
  });
  window.addEventListener('blur', () => {
    if (expanded && !sending) send('panel?open=0');
  });

  window.wuuPetView = (next) => {
    const sheetChanged = next.spritesheetURL !== view.spritesheetURL;
    view = next;
    stage.style.setProperty('--pet-sheet', 'url("' + next.spritesheetURL + '")');
    stage.style.setProperty('--pet-scale', String(next.scale));
    stage.style.setProperty('--pet-frame-width', next.spriteWidth + 'px');
    stage.style.setProperty('--pet-frame-height', next.spriteHeight + 'px');
    stage.style.setProperty('--sprite-shift', next.spriteShift + 'px');
    liveScale = next.scale;
    stage.dataset.layout = next.layout;
    sprite.setAttribute('aria-label', next.label);
    if (next.mood !== mood) {
      mood = next.mood;
      animate();
    }
    applyHints(next.hints, true);
    setExpanded(next.expanded);
    syncComposer();
    syncBadge();
    if (sheetChanged) probeSheet(next.spritesheetURL);
  };
  window.addEventListener('contextmenu', (event) => {
    event.preventDefault();
    location.href = 'wuu-pet://action/menu';
  });
  // Standard window-style continuous resize around the sprite: four corner
  // squares and four edge strips (all invisible — the directional resize
  // cursors are the affordance). Everything maps to the single uniform
  // sprite scale: each pointermove converts the drag delta along the
  // handle's outward direction (data-dir-x / data-dir-y) into a scale
  // delta. The sprite is a ${CODEX_PET_CELL_WIDTH}×${CODEX_PET_CELL_HEIGHT} cell under transform: scale, so
  // 1px of drag = 1px of rendered sprite width/height on that axis; corners
  // average both axes so a 45° drag tracks ~1:1 too. Dragging away from the
  // sprite grows, toward it shrinks. The main process live-updates window
  // bounds and CSS scale around the sprite's bottom-center anchor; emission
  // is rAF-throttled so a fast drag doesn't queue more navigations than
  // frames, and pointerup re-sends the final value with commit=1 so the
  // host persists it. stopPropagation on pointerdown keeps the stage's
  // move-the-window drag from engaging, and a 2px dead-zone keeps a plain
  // click from emitting anything.
  const SCALE_MIN = ${CODEX_PET_SCALE_MIN};
  const SCALE_MAX = ${CODEX_PET_SCALE_MAX};
  let liveScale = ${JSON.stringify(view.scale)};
  const emitScale = (value, commit) => {
    location.href = 'wuu-pet://action/scale?value=' + encodeURIComponent(value)
      + (commit ? '&commit=1' : '');
  };
  let resizePointerID = null;
  let resizeStartX = 0;
  let resizeStartY = 0;
  let resizeStartScale = 0;
  let resizeMoved = false;
  let pendingScale = null;
  let resizeRAF = 0;
  const RESIZE_DEAD_ZONE = 2;
  const finishResize = (event) => {
    if (event.pointerId !== resizePointerID) return;
    resizePointerID = null;
    if (resizeRAF) {
      cancelAnimationFrame(resizeRAF);
      resizeRAF = 0;
    }
    if (resizeMoved && pendingScale !== null) {
      liveScale = pendingScale;
      emitScale(pendingScale, true);
    }
    pendingScale = null;
    resizeMoved = false;
  };
  for (const handle of stage.querySelectorAll('.resize-handle')) {
    const dirX = Number(handle.dataset.dirX) || 0;
    const dirY = Number(handle.dataset.dirY) || 0;
    const axes = (dirX !== 0 ? 1 : 0) + (dirY !== 0 ? 1 : 0);
    handle.addEventListener('pointerdown', (event) => {
      if (event.button !== 0) return;
      event.stopPropagation();
      resizePointerID = event.pointerId;
      resizeStartX = event.screenX;
      resizeStartY = event.screenY;
      resizeStartScale = liveScale;
      resizeMoved = false;
      pendingScale = null;
      handle.setPointerCapture(event.pointerId);
      event.preventDefault();
    });
    handle.addEventListener('pointermove', (event) => {
      if (event.pointerId !== resizePointerID) return;
      const deltaX = event.screenX - resizeStartX;
      const deltaY = event.screenY - resizeStartY;
      if (
        !resizeMoved &&
        Math.abs(deltaX) < RESIZE_DEAD_ZONE &&
        Math.abs(deltaY) < RESIZE_DEAD_ZONE
      ) {
        return;
      }
      resizeMoved = true;
      const deltaScale =
        ((dirX * deltaX) / CELL_WIDTH + (dirY * deltaY) / CELL_HEIGHT) / axes;
      pendingScale = Math.min(
        SCALE_MAX,
        Math.max(SCALE_MIN, resizeStartScale + deltaScale),
      );
      if (!resizeRAF) {
        resizeRAF = requestAnimationFrame(() => {
          resizeRAF = 0;
          if (pendingScale !== null) emitScale(pendingScale, false);
        });
      }
    });
    handle.addEventListener('pointerup', finishResize);
    handle.addEventListener('pointercancel', finishResize);
  }
  // Pointer handling. A press that moves less than DRAG_THRESHOLD is a
  // click: on the pet it toggles the composer; on a card it opens that
  // conversation; card and dock buttons do what they say. A longer move
  // drags the window, and the pet runs in the drag direction until the
  // pointer rests.
  const DRAG_THRESHOLD = 4;
  const DRAG_REST_MS = 300;
  let pointerID = null;
  let downX = 0;
  let downY = 0;
  let offsetX = 0;
  let offsetY = 0;
  let lastX = 0;
  let dragging = false;
  let suppressClick = false;
  let restTimer = 0;
  stage.addEventListener('pointerdown', (event) => {
    suppressClick = false;
    if (event.button !== 0) return;
    // In the composer and the open panel's cards the pointer selects text
    // and presses controls.
    if (event.target instanceof Element && event.target.closest(expanded ? '.composer,.bubble' : '.composer')) return;
    pointerID = event.pointerId;
    downX = lastX = event.screenX;
    downY = event.screenY;
    offsetX = event.screenX - window.screenX;
    offsetY = event.screenY - window.screenY;
    dragging = false;
    event.preventDefault();
  });
  stage.addEventListener('pointermove', (event) => {
    if (event.pointerId !== pointerID) return;
    if (!dragging) {
      if (Math.abs(event.screenX - downX) < DRAG_THRESHOLD && Math.abs(event.screenY - downY) < DRAG_THRESHOLD) return;
      dragging = true;
      stage.setPointerCapture(pointerID);
      stage.classList.add('is-dragging');
    }
    const deltaX = event.screenX - lastX;
    if (Math.abs(deltaX) >= 2) {
      lastX = event.screenX;
      const next = deltaX > 0 ? 'running-right' : 'running-left';
      if (next !== dragState) {
        dragState = next;
        animate();
      }
      clearTimeout(restTimer);
      restTimer = setTimeout(() => {
        dragState = null;
        animate();
      }, DRAG_REST_MS);
    }
    window.moveTo(Math.round(event.screenX - offsetX), Math.round(event.screenY - offsetY));
  });
  const finishDrag = (event) => {
    if (event.pointerId !== pointerID) return;
    pointerID = null;
    if (!dragging) return;
    dragging = false;
    suppressClick = true;
    stage.classList.remove('is-dragging');
    clearTimeout(restTimer);
    if (dragState) {
      dragState = null;
      animate();
    }
  };
  stage.addEventListener('pointerup', finishDrag);
  stage.addEventListener('pointercancel', finishDrag);
  stage.addEventListener('click', (event) => {
    if (suppressClick) {
      suppressClick = false;
      return;
    }
    const element = event.target instanceof Element ? event.target : null;
    if (!element || element.closest('.resize-handle')) return;
    const card = element.closest('.card');
    if (card) {
      const id = card.dataset.threadId;
      if (element.closest('.card-dismiss')) {
        send('dismiss?thread_id=' + encodeURIComponent(id));
        return;
      }
      const action = element.closest('.card-action');
      if (action && action.dataset.action === 'reply') {
        const hint = currentHints.find((h) => h.thread_id === id);
        setReply({ id, title: (hint && hint.title) || TEXT.conversation });
        openPanel();
      } else {
        jump(card);
      }
      return;
    }
    const tool = element.closest('.tool');
    if (tool) {
      if (tool.dataset.action === 'show') {
        send('show');
      } else {
        setReply(null);
        openPanel();
      }
      return;
    }
    if (element.closest('.composer-target')) {
      if (reply) setReply(null);
      return;
    }
    if (element.closest('.composer-send')) {
      submit();
      return;
    }
    if (element.closest('.composer,.bubble')) return;
    if (element.closest('.dock')) {
      openPanel();
      return;
    }
    if (!expanded) play('waving', 1, true);
    send('panel?open=' + (expanded ? '0' : '1'));
  });
  let lastHover = 0;
  spriteFrame.addEventListener('pointerenter', () => {
    if (pointerID !== null || Date.now() - lastHover < 8000) return;
    lastHover = Date.now();
    play('jumping', 1, false);
  });

  applyHints(view.hints, false);
  syncBadge();
  syncComposer();
  play('waving', 1, true);
  probeSheet(view.spritesheetURL);
  scheduleFidget();
})();
</script></body></html>`;
}

export type CodexPetWindowOptions = {
  onClose: () => void;
  // Opens a bubble row's conversation in the main window.
  onJump: (threadID: string) => void;
  // Sends text to Wuu as a reply or as a new conversation.
  onSubmit: (text: string, target: CodexPetSubmitTarget) => Promise<CodexPetSubmitResult>;
  onShowApp: () => void;
  onSizeChange?: (size: CodexPetSize) => void;
  onScaleChange?: (scale: number) => void;
  isPackaged?: boolean;
};

export class CodexPetWindowManager {
  private win: BrowserWindow | undefined;
  private loaded = false;
  private pendingView: CodexPetView | undefined;
  private lastViewSignature = "";
  private mood: CodexPetMood = "idle";
  private expanded = false;
  private snapshot: CodexPetsSnapshot | undefined;
  private hints: CodexPetHint[] = [];
  // Conversations the user hid from the glance, with the status they had.
  // A hidden card comes back when its conversation changes status.
  private dismissed = new Map<string, CodexPetHintStatus>();
  // The workspace open in the main window, and the one captured when the
  // panel opened. A new conversation goes to the captured one, so switching
  // workspaces while typing does not move the text.
  private workspace: CodexPetWorkspace | undefined;
  private panelWorkspace: CodexPetWorkspace | undefined;
  private currentLayout: CodexPetBubbleLayout = "hidden";
  private spriteShift = 0;
  // User-facing sprite size; defaults to the 100% preset. Mutated only
  // through setSize(), which re-applies the view (so the window resizes
  // around the sprite's existing anchor) and notifies onSizeChange so the
  // host shell can persist the choice.
  private size: CodexPetSize = CODEX_PET_SIZE_DEFAULT;
  // Live sprite scale from the continuous resize handles. Overrides the
  // preset-derived scale while defined so the pet can be sized to any
  // value between the smallest and largest presets. Cleared the next time
  // setSize() picks a preset.
  private currentScale: number | undefined;
  // The scale (preset-derived or continuous) that produced the window's
  // current bounds. anchorFromBounds() must reverse-engineer the sprite
  // anchor from the bounds as they are on screen, so it needs the scale
  // that was in effect when those bounds were applied — not this.size /
  // this.currentScale, which setSize()/setScale() mutate *before*
  // applyView() runs. Without this, every left/right-bubble resize frame
  // extracts a slightly wrong anchor and the pet drifts sideways over a
  // continuous drag.
  private appliedScale: number | undefined;
  // Likewise the dock width that produced the current bounds.
  private appliedExpanded = false;
  // Cursor following, for sheets with look-direction rows.
  private lookCapable = false;
  private lookTimer: ReturnType<typeof setInterval> | undefined;
  private lookIndex: number | null = null;
  private lastCursor = { x: Number.NaN, y: Number.NaN };
  private cursorMovedAt = 0;

  constructor(private readonly options: CodexPetWindowOptions) {}

  refreshLocale(): void {
    const pet = selectedCodexPet(this.snapshot);
    if (!pet || !this.win || this.win.isDestroyed()) return;
    this.destroy();
    this.createWindow(pet);
  }

  setSize(size: CodexPetSize, commit = true): void {
    if (size === this.size) return;
    this.size = size;
    // Switching to a preset clears any in-flight custom scale: the user
    // explicitly picked a discrete preset.
    this.currentScale = undefined;
    this.refresh();
    if (commit) this.options.onSizeChange?.(size);
  }

  setScale(value: number, commit = false): void {
    // Live-update the sprite scale without going through the preset
    // list. During a drag (commit=false) the host shell is intentionally
    // not notified; the pointerup emission arrives with commit=true. Clamp
    // here too: startup rehydration calls setScale directly with whatever
    // desktop-settings.json holds, bypassing the URL parser's clamp.
    const clamped = Math.max(
      CODEX_PET_SCALE_MIN,
      Math.min(CODEX_PET_SCALE_MAX, value),
    );
    this.currentScale = clamped;
    this.refresh();
    if (commit) this.options.onScaleChange?.(clamped);
  }

  sync(snapshot: CodexPetsSnapshot | undefined): void {
    this.snapshot = snapshot;
    const pet = selectedCodexPet(snapshot);
    if (!pet) {
      this.destroy();
      return;
    }
    if (!this.win || this.win.isDestroyed()) {
      this.createWindow(pet);
      return;
    }
    this.applyView(pet);
  }

  setMood(mood: CodexPetMood): void {
    if (mood === this.mood) return;
    this.mood = mood;
    this.refresh();
    this.updateLook();
  }

  // Plays a one-shot animation. Reactions are momentary, so one that
  // arrives before the page loads is dropped rather than replayed late.
  react(state: CodexPetReaction | "waving"): void {
    this.runScript(
      `window.wuuPetReact?.(${JSON.stringify(state)}, ${CODEX_PET_REACTION_LOOPS[state]})`,
    );
  }

  setHints(hints: CodexPetHint[] | null | undefined): void {
    // Sanitize at the trust boundary: the renderer should already omit
    // conversations without commentary, but a malformed push must not
    // produce phantom cards or an oversized window. Cap at
    // CODEX_PET_HINTS_MAX.
    this.hints = (Array.isArray(hints) ? hints : [])
      .filter(
        (hint): hint is CodexPetHint =>
          Boolean(hint) &&
          typeof hint.thread_id === "string" &&
          hint.thread_id.length > 0 &&
          typeof hint.preview === "string" &&
          hint.preview.trim().length > 0,
      )
      .slice(0, CODEX_PET_HINTS_MAX);
    for (const [threadID, status] of this.dismissed) {
      if (!this.hints.some((hint) => hint.thread_id === threadID && hint.status === status)) {
        this.dismissed.delete(threadID);
      }
    }
    this.refresh();
  }

  setWorkspace(workspace: CodexPetWorkspace | null | undefined): void {
    this.workspace =
      workspace && typeof workspace.name === "string" && typeof workspace.context?.cwd === "string"
        ? workspace
        : undefined;
    // A panel opened before any workspace was known adopts the first one;
    // there was no earlier target for it to replace.
    if (this.expanded && !this.panelWorkspace && this.workspace) {
      this.panelWorkspace = this.workspace;
      this.refresh();
    }
  }

  destroy(): void {
    const win = this.win;
    this.forgetWindow();
    if (win && !win.isDestroyed()) win.close();
  }

  private forgetWindow(): void {
    this.win = undefined;
    this.loaded = false;
    this.pendingView = undefined;
    this.lastViewSignature = "";
    this.currentLayout = "hidden";
    this.spriteShift = 0;
    this.appliedScale = undefined;
    this.appliedExpanded = false;
    this.expanded = false;
    this.panelWorkspace = undefined;
    this.lookCapable = false;
    this.updateLook();
  }

  private refresh(): void {
    const pet = selectedCodexPet(this.snapshot);
    if (pet && this.win && !this.win.isDestroyed()) this.applyView(pet);
  }

  private setExpanded(expanded: boolean): void {
    if (expanded === this.expanded) return;
    this.expanded = expanded;
    this.panelWorkspace = expanded ? this.workspace : undefined;
    this.refresh();
    // The composer needs keyboard focus; the pet window is otherwise
    // shown inactive and never takes focus from the user's work.
    if (expanded && this.win && !this.win.isDestroyed()) this.win.focus();
    this.updateLook();
  }

  private dismiss(threadID: string): void {
    const hint = this.hints.find((candidate) => candidate.thread_id === threadID);
    if (!hint) return;
    this.dismissed.set(threadID, hint.status);
    this.refresh();
  }

  // The glance shows conversations with something to report that the user
  // has not hidden. The open panel lists every recent conversation, so any
  // of them can be a reply target.
  private visibleHints(): CodexPetHint[] {
    if (this.expanded) return this.hints;
    return this.hints.filter(
      (hint) => hint.status !== "idle" && this.dismissed.get(hint.thread_id) !== hint.status,
    );
  }

  private submit(text: string, threadID: string | undefined): void {
    const trimmed = text.trim();
    const target: CodexPetSubmitTarget | undefined = threadID
      ? { thread_id: threadID }
      : this.panelWorkspace && { workspace: this.panelWorkspace.context };
    const result =
      target && trimmed && trimmed.length <= CODEX_PET_SUBMIT_MAX
        ? this.options.onSubmit(trimmed, target)
        : Promise.resolve({ ok: false });
    void result
      .catch(() => ({ ok: false }))
      .then(({ ok }) => this.runScript(`window.wuuPetSubmitResult?.(${JSON.stringify({ ok: ok === true })})`));
  }

  private createWindow(pet: CodexPet): void {
    const workArea = screen.getPrimaryDisplay().workArea;
    // Startup rehydration may have pushed a persisted continuous scale
    // via setScale() before the window exists; honor it here the same
    // way applyView() does, so the pet doesn't flash at the preset size
    // and then jump on the first view push.
    const rendered = renderedSprite(this.size, this.currentScale);
    const petOnly = codexPetBoundsForLayout({
      layout: "hidden",
      anchor: { x: 0, y: 0 },
      size: this.size,
      customScale: this.currentScale,
    });
    const initialAnchor = {
      x: workArea.x + workArea.width - petOnly.width / 2 - CODEX_PET_SCREEN_INSET,
      y: workArea.y + workArea.height - CODEX_PET_BELOW_ANCHOR - CODEX_PET_SCREEN_INSET,
    };
    const { layout, bounds, spriteShift = 0 } = this.layoutAround(initialAnchor, workArea);
    this.currentLayout = layout;
    this.spriteShift = spriteShift;
    const view = this.view(pet, layout);
    this.appliedScale = rendered.scale;
    this.appliedExpanded = this.expanded;
    this.lastViewSignature = JSON.stringify(view);
    const win = new BrowserWindow({
      ...bounds,
      frame: false,
      transparent: true,
      backgroundColor: "#00000000",
      hasShadow: false,
      alwaysOnTop: true,
      skipTaskbar: true,
      resizable: false,
      minimizable: false,
      maximizable: false,
      fullscreenable: false,
      acceptFirstMouse: true,
      show: false,
      type: "panel",
      webPreferences: {
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
        ...appShellWebPreferences(this.options.isPackaged ?? false),
      },
    });
    this.win = win;
    this.loaded = false;
    win.setAlwaysOnTop(true, "floating");
    win.setHasShadow(false);
    win.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: true });
    win.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    win.webContents.on("will-navigate", (navigationEvent, rawURL) => {
      const parsed = codexPetActionFromURL(rawURL);
      if (!parsed) return;
      navigationEvent.preventDefault();
      switch (parsed.action) {
        case "menu":
          this.popupMenu(win);
          return;
        case "jump":
          if (this.hints.some((hint) => hint.thread_id === parsed.thread_id)) {
            this.options.onJump(parsed.thread_id);
          }
          return;
        case "dismiss":
          this.dismiss(parsed.thread_id);
          return;
        case "show":
          this.options.onShowApp();
          return;
        case "panel":
          this.setExpanded(parsed.open);
          return;
        case "submit":
          this.submit(parsed.text, parsed.thread_id);
          return;
        case "look":
          this.lookCapable = parsed.capable;
          this.updateLook();
          return;
        case "resize":
          this.setSize(parsed.id);
          return;
        case "scale":
          this.setScale(parsed.value, parsed.commit);
          return;
      }
    });
    win.webContents.on("did-finish-load", () => {
      if (win.isDestroyed() || this.win !== win) return;
      this.loaded = true;
      const pending = this.pendingView;
      this.pendingView = undefined;
      if (pending) this.pushView(win, pending);
      // Belt-and-suspenders: ready-to-show is unreliable for data: URLs
      // in current Electron, so surface the window from the load event
      // when it is still hidden. showInactive() is a no-op once the
      // window is already up, so calling it after ready-to-show
      // already ran is safe.
      if (!win.isVisible()) {
        win.showInactive();
      }
    });
    win.once("ready-to-show", () => {
      if (win.isDestroyed() || this.win !== win) return;
      win.showInactive();
    });
    win.on("closed", () => {
      if (this.win === win) this.forgetWindow();
    });
    void win.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(codexPetWindowHTML(view))}`);
  }

  private popupMenu(win: BrowserWindow): void {
    const pet = selectedCodexPet(this.snapshot);
    const template: Electron.MenuItemConstructorOptions[] = [
      { label: pet ? pet.display_name : mainTranslate("pet"), enabled: false },
    ];
    for (const hint of this.hints) {
      template.push({
        label: mainTranslate("openConversation", { title: hint.title || mainTranslate("conversation") }),
        click: () => this.options.onJump(hint.thread_id),
      });
    }
    template.push(
      { type: "separator" },
      { label: mainTranslate("petTalk"), click: () => this.setExpanded(true) },
      { label: mainTranslate("petShowApp"), click: () => this.options.onShowApp() },
      { type: "separator" },
      { label: mainTranslate("closePet"), click: () => this.options.onClose() },
    );
    Menu.buildFromTemplate(template).popup({ window: win });
  }

  private view(pet: CodexPet, layout: CodexPetBubbleLayout): CodexPetView {
    return codexPetView(pet, {
      mood: this.mood,
      hints: this.visibleHints(),
      layout,
      expanded: this.expanded,
      workspaceName: this.panelWorkspace?.name ?? "",
      size: this.size,
      customScale: this.currentScale,
      spriteShift: this.spriteShift,
    });
  }

  // Window bounds that keep the sprite's bottom-center at `anchor`, with
  // the cards (when there are any) in the first direction that fits.
  private layoutAround(anchor: { x: number; y: number }, workArea: Rectangle): CodexPetLayoutDecision {
    const count = this.visibleHints().length;
    return selectCodexPetBubbleLayout({
      workArea,
      anchor,
      bubble: count ? codexPetCardsSize(count) : undefined,
      expanded: this.expanded,
      size: this.size,
      customScale: this.currentScale,
    });
  }

  private applyView(pet: CodexPet): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    const currentBounds = win.getBounds();
    const anchor = this.anchorFromBounds(currentBounds, this.currentLayout);
    const workArea = screen.getDisplayMatching(currentBounds).workArea;
    const { layout, bounds, spriteShift = 0 } = this.layoutAround(anchor, workArea);
    this.currentLayout = layout;
    this.spriteShift = spriteShift;
    if (
      currentBounds.x !== bounds.x ||
      currentBounds.y !== bounds.y ||
      currentBounds.width !== bounds.width ||
      currentBounds.height !== bounds.height
    ) {
      win.setBounds(bounds);
    }
    const view = this.view(pet, layout);
    this.appliedScale = view.scale;
    this.appliedExpanded = this.expanded;
    const signature = JSON.stringify(view);
    if (signature === this.lastViewSignature && this.loaded) return;
    this.lastViewSignature = signature;
    if (!this.loaded) {
      this.pendingView = view;
      return;
    }
    this.pushView(win, view);
  }

  private anchorFromBounds(
    bounds: Rectangle,
    layout: CodexPetBubbleLayout,
  ): { x: number; y: number } {
    // anchor = sprite's bottom-center on screen. Reads sprite footprint from
    // the scale that produced these bounds (see appliedScale) so a resize
    // that happens between snapshots still extracts the right anchor.
    const rendered = renderedSprite(this.size, this.appliedScale);
    const column = codexPetColumn(rendered, this.appliedExpanded);
    const pad = CODEX_PET_WINDOW_PADDING;
    const y = layout === "below"
      ? bounds.y + pad + rendered.height
      : bounds.y + bounds.height - CODEX_PET_BELOW_ANCHOR;
    let x: number;
    switch (layout) {
      case "above":
      case "below":
      case "hidden":
        x = bounds.x + bounds.width / 2 + this.spriteShift;
        break;
      case "right":
        x = bounds.x + pad + column.width / 2;
        break;
      case "left":
        x = bounds.x + bounds.width - pad - column.width / 2;
        break;
    }
    return { x, y };
  }

  // Polls the cursor only while the pet can actually look at it: a sheet
  // with look rows, an idle mood, and a collapsed panel.
  private updateLook(): void {
    const active =
      this.lookCapable &&
      this.mood === "idle" &&
      !this.expanded &&
      this.win !== undefined &&
      !this.win.isDestroyed();
    if (active && !this.lookTimer) {
      this.lookTimer = setInterval(() => this.lookAtCursor(), CODEX_PET_LOOK_POLL_MS);
    } else if (!active && this.lookTimer) {
      clearInterval(this.lookTimer);
      this.lookTimer = undefined;
      this.pushLook(null);
    }
  }

  private lookAtCursor(): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    const cursor = screen.getCursorScreenPoint();
    const now = Date.now();
    if (cursor.x !== this.lastCursor.x || cursor.y !== this.lastCursor.y) {
      this.lastCursor = cursor;
      this.cursorMovedAt = now;
    }
    const anchor = this.anchorFromBounds(win.getBounds(), this.currentLayout);
    const sprite = renderedSprite(this.size, this.appliedScale);
    const dx = cursor.x - anchor.x;
    const dy = cursor.y - (anchor.y - sprite.height / 2);
    const distance = Math.hypot(dx, dy);
    if (
      now - this.cursorMovedAt > CODEX_PET_LOOK_IDLE_MS ||
      distance > CODEX_PET_LOOK_RADIUS ||
      distance < sprite.width / 2
    ) {
      this.pushLook(null);
      return;
    }
    // Clockwise angle from straight up, bucketed into the sheet's directions.
    const angle = ((Math.atan2(dx, -dy) * 180) / Math.PI + 360) % 360;
    const step = 360 / CODEX_PET_LOOK_DIRECTIONS;
    this.pushLook(Math.round(angle / step) % CODEX_PET_LOOK_DIRECTIONS);
  }

  private pushLook(index: number | null): void {
    if (index === this.lookIndex) return;
    this.lookIndex = index;
    this.runScript(`window.wuuPetLook?.(${JSON.stringify(index)})`);
  }

  private runScript(script: string): void {
    const win = this.win;
    if (!win || win.isDestroyed() || !this.loaded) return;
    void win.webContents.executeJavaScript(script, true).catch(() => undefined);
  }

  private pushView(win: BrowserWindow, view: CodexPetView): void {
    void win.webContents
      .executeJavaScript(`window.wuuPetView?.(${JSON.stringify(view)})`, true)
      .catch(() => undefined);
  }
}

function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  })[character] ?? character);
}
