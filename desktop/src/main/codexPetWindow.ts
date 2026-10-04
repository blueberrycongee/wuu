import { BrowserWindow, Menu, screen, type Rectangle } from "electron";
import {
  CODEX_PET_HINTS_MAX,
  CODEX_PET_SIZE_DEFAULT,
  CODEX_PET_SIZE_OPTIONS,
  type AppLocale,
  type CodexPet,
  type CodexPetHint,
  type CodexPetSize,
  type CodexPetSubmitResult,
  type CodexPetsSnapshot,
} from "../shared/protocol";
import type { CodexPetMood, CodexPetReaction } from "./codexPetActivity";
import { CODEX_PET_CELL_HEIGHT, CODEX_PET_CELL_WIDTH, CODEX_PET_STATES } from "./codexPets";
import { appShellWebPreferences } from "./appShellGuards";
import { getMainLocale, mainTranslate } from "./i18n";

export type CodexPetView = {
  spritesheetURL: string;
  mood: CodexPetMood;
  label: string;
  hints: CodexPetHint[];
  layout: CodexPetBubbleLayout;
  // Whether the bubble is expanded into the quick panel.
  expanded: boolean;
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
  // Set when an above/below window slid sideways to fit its work area: the
  // sprite sits this many px right of the window center so it keeps its place.
  spriteShift?: number;
};

// The pet lives in its own frameless always-on-top window so it survives the
// main window being hidden or minimized. The base window is sized for the
// sprite cell plus its drop shadow; when a hint bubble is shown, the window
// grows in the chosen layout direction. Per-size math is computed by
// `codexPetRenderedSpriteForSize(this.size)`.
const CODEX_PET_SCREEN_INSET = 24;

// The bubble is a stack of up to CODEX_PET_HINTS_MAX single-line rows,
// one per surfaced session (status dot, title, and latest stable
// commentary, each ellipsized). The pet's job is a glance, not a
// paragraph: rows are clamped to one line of 13px text on a fixed 19px
// line so the card height is a pure function of the row count. The
// renderer only sends rows that have commentary, and an empty list hides
// the collapsed bubble entirely.
//
// Clicking the pet expands the bubble into a quick panel: the same rows
// (now selectable as the send target) above a divider, a one-line target
// label, and a three-line composer.
const CODEX_PET_BUBBLE_WIDTH = 280;
const CODEX_PET_BUBBLE_INSET = 12;
const CODEX_PET_BUBBLE_ROW_LINE = 19;
const CODEX_PET_BUBBLE_ROW_GAP = 6;
const CODEX_PET_BUBBLE_PADDING = 8;
const CODEX_PET_BUBBLE_GAP = 8;
// 9px above + 1px rule + 10px below, between the rows and the composer.
const CODEX_PET_PANEL_DIVIDER = 20;
const CODEX_PET_PANEL_TARGET_LINE = 18;
const CODEX_PET_PANEL_TARGET_GAP = 6;
// Three 18px lines + 6px vertical padding + 1px border on each side.
const CODEX_PET_PANEL_INPUT_HEIGHT = 68;
// Bounds the text one submit carries; the composer enforces it as maxlength.
export const CODEX_PET_SUBMIT_MAX = 4000;

// Bubble card footprint. Layout math (window bounds, direction picking)
// sizes the window around this, so it must stay in lockstep with the
// inline CSS; the trailing 1px is slack for subpixel rounding.
export function codexPetBubbleSizeForCount(
  count: number,
  expanded = false,
): { width: number; height: number } {
  const rows = Math.max(expanded ? 0 : 1, Math.min(CODEX_PET_HINTS_MAX, count));
  const rowsHeight = rows
    ? CODEX_PET_BUBBLE_ROW_LINE * rows + CODEX_PET_BUBBLE_ROW_GAP * (rows - 1)
    : 0;
  const composerHeight = expanded
    ? (rows ? CODEX_PET_PANEL_DIVIDER : 0) +
      CODEX_PET_PANEL_TARGET_LINE +
      CODEX_PET_PANEL_TARGET_GAP +
      CODEX_PET_PANEL_INPUT_HEIGHT
    : 0;
  return {
    width: CODEX_PET_BUBBLE_WIDTH,
    height: 2 * CODEX_PET_BUBBLE_INSET + rowsHeight + composerHeight + 1,
  };
}
// Bubble preview swap animation: when `hint.preview` changes (e.g. first
// commentary finishes and a second one lands, or the bubble disappears), the
// .preview element fades out, swaps text, fades back in. Asymmetric timings so
// the fade-out feels decisive and the fade-in is gentle — total cycle ~320ms.
const CODEX_PET_BUBBLE_SWAP_OUT_MS = 140;
const CODEX_PET_BUBBLE_SWAP_IN_MS = 180;

// The sprite is rendered from the 192×208 layout box via CSS transform:
// scale(<size>). At the default (100%) size the transform is 0.5, so the
// base rendered footprint is 96×104; the per-size helper below recomputes
// the footprint against the user's chosen multiplier.
const CODEX_PET_SPRITE_RENDER_WIDTH_BASE = CODEX_PET_CELL_WIDTH / 2; // 96
const CODEX_PET_SPRITE_RENDER_HEIGHT_BASE = CODEX_PET_CELL_HEIGHT / 2; // 104
const CODEX_PET_SPRITE_BASE_SCALE = 0.5;

// Window chrome padding around the sprite is held constant across sizes so
// the chrome (drop shadow margin, hit-test room, breathing space) doesn't
// grow with the pet itself.
const CODEX_PET_WINDOW_HORIZONTAL_PADDING = 24; // 12px each side
const CODEX_PET_WINDOW_TOP_PADDING = 16;

// The anchor is the sprite's bottom-center on screen. The sprite sits 8px
// above the window bottom in every layout except `below`, where it sits on
// top with the bubble underneath. The page CSS mirrors this placement; if
// the two drift apart, the sprite jumps whenever the bubble changes.
const CODEX_PET_SPRITE_BOTTOM_OFFSET = 8;

export type CodexPetRenderedSprite = {
  width: number;
  height: number;
  scale: number;
  windowWidth: number;
  windowHeight: number;
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
  const multiplier = option.multiplier;
  const width = Math.round(CODEX_PET_SPRITE_RENDER_WIDTH_BASE * multiplier);
  const height = Math.round(CODEX_PET_SPRITE_RENDER_HEIGHT_BASE * multiplier);
  return {
    width,
    height,
    scale: CODEX_PET_SPRITE_BASE_SCALE * multiplier,
    windowWidth: width + CODEX_PET_WINDOW_HORIZONTAL_PADDING,
    windowHeight:
      height + CODEX_PET_WINDOW_TOP_PADDING + CODEX_PET_SPRITE_BOTTOM_OFFSET,
  };
}

// Build rendered sprite geometry from a raw scale value. Used by the
// continuous resize path: when the user drags the strip the scale moves
// smoothly between the preset extremes and we need geometry for any
// value, not just the four preset ids. Same math as
// codexPetRenderedSpriteForSize but skips the preset lookup so it can
// interpolate freely without going through CODEX_PET_SIZE_OPTIONS.
export function codexPetRenderedSpriteForScale(
  scale: number,
): CodexPetRenderedSprite {
  const multiplier = scale / CODEX_PET_SPRITE_BASE_SCALE;
  const width = Math.round(CODEX_PET_SPRITE_RENDER_WIDTH_BASE * multiplier);
  const height = Math.round(CODEX_PET_SPRITE_RENDER_HEIGHT_BASE * multiplier);
  return {
    width,
    height,
    scale,
    windowWidth: width + CODEX_PET_WINDOW_HORIZONTAL_PADDING,
    windowHeight:
      height + CODEX_PET_WINDOW_TOP_PADDING + CODEX_PET_SPRITE_BOTTOM_OFFSET,
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
    size = CODEX_PET_SIZE_DEFAULT,
    customScale,
    spriteShift = 0,
  }: {
    mood: CodexPetMood;
    hints: CodexPetHint[];
    layout: CodexPetBubbleLayout;
    expanded?: boolean;
    size?: CodexPetSize;
    customScale?: number;
    spriteShift?: number;
  },
): CodexPetView {
  // Continuous drag passes a raw `customScale` and we derive geometry
  // from it directly. Without it, fall back to the preset-derived
  // geometry.
  const useCustomScale =
    typeof customScale === "number" &&
    Number.isFinite(customScale) &&
    customScale > 0;
  const rendered = useCustomScale
    ? codexPetRenderedSpriteForScale(customScale)
    : codexPetRenderedSpriteForSize(size);
  return {
    spritesheetURL: pet.spritesheet_url,
    mood,
    label: pet.display_name,
    hints,
    layout,
    expanded,
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
    if (actionName === "jump") {
      const threadId = url.searchParams.get("thread_id");
      if (!threadId) return undefined;
      return { action: "jump", thread_id: threadId };
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

// Compute window bounds for a given bubble layout, keeping the sprite's
// bottom-center (the `anchor`) at the same screen point. The returned
// bounds are not yet clamped to the workArea; the caller decides whether
// the layout fits before applying. `size` defaults to the 100% preset so
// callers that don't pass one see the pre-size-feature geometry.
export function codexPetBoundsForLayout({
  layout,
  anchor,
  bubble,
  size = CODEX_PET_SIZE_DEFAULT,
  customScale,
}: {
  layout: Exclude<CodexPetBubbleLayout, "hidden">;
  anchor: { x: number; y: number };
  bubble: { width: number; height: number };
  size?: CodexPetSize;
  customScale?: number;
}): Rectangle {
  // During continuous resize we already know the live scale; use it
  // directly so the window bounds shrink/grow with the sprite. Without
  // it, fall back to the preset-derived geometry so callers that don't
  // know about continuous resize see the original behavior.
  const useCustomScale =
    typeof customScale === "number" &&
    Number.isFinite(customScale) &&
    customScale > 0;
  const rendered = useCustomScale
    ? codexPetRenderedSpriteForScale(customScale)
    : codexPetRenderedSpriteForSize(size);
  const spriteW = rendered.width;
  const spriteH = rendered.height;
  switch (layout) {
    case "above": {
      const width = Math.max(spriteW, bubble.width);
      const height =
        CODEX_PET_BUBBLE_PADDING +
        bubble.height +
        CODEX_PET_BUBBLE_GAP +
        spriteH +
        CODEX_PET_BUBBLE_PADDING;
      return {
        x: Math.round(anchor.x - width / 2),
        y: Math.round(anchor.y - height + CODEX_PET_SPRITE_BOTTOM_OFFSET),
        width,
        height,
      };
    }
    case "below": {
      const width = Math.max(spriteW, bubble.width);
      const height =
        CODEX_PET_BUBBLE_PADDING +
        spriteH +
        CODEX_PET_BUBBLE_GAP +
        bubble.height +
        CODEX_PET_BUBBLE_PADDING;
      return {
        x: Math.round(anchor.x - width / 2),
        y: Math.round(anchor.y - spriteH - CODEX_PET_BUBBLE_PADDING),
        width,
        height,
      };
    }
    case "right": {
      const width =
        CODEX_PET_BUBBLE_PADDING +
        spriteW +
        CODEX_PET_BUBBLE_GAP +
        bubble.width +
        CODEX_PET_BUBBLE_PADDING;
      const height =
        Math.max(spriteH, bubble.height) + 2 * CODEX_PET_BUBBLE_PADDING;
      return {
        x: Math.round(anchor.x - CODEX_PET_BUBBLE_PADDING - spriteW / 2),
        y: Math.round(anchor.y - height + CODEX_PET_SPRITE_BOTTOM_OFFSET),
        width,
        height,
      };
    }
    case "left": {
      const width =
        CODEX_PET_BUBBLE_PADDING +
        bubble.width +
        CODEX_PET_BUBBLE_GAP +
        spriteW +
        CODEX_PET_BUBBLE_PADDING;
      const height =
        Math.max(spriteH, bubble.height) + 2 * CODEX_PET_BUBBLE_PADDING;
      return {
        x: Math.round(
          anchor.x - (width - CODEX_PET_BUBBLE_PADDING - spriteW / 2),
        ),
        y: Math.round(anchor.y - height + CODEX_PET_SPRITE_BOTTOM_OFFSET),
        width,
        height,
      };
    }
  }
}

// Choose the first layout direction whose bounds fully fit inside the
// given workArea. If none fit, returns "hidden" with base window bounds
// (no bubble shown). Priority matches the user's brief: above first
// ("pet 头上"), then right, then left/below as last-resort fallbacks.
// `size` defaults to the 100% preset so callers that don't pass one see
// the pre-size-feature geometry.
export function selectCodexPetBubbleLayout({
  workArea,
  anchor,
  bubble,
  size = CODEX_PET_SIZE_DEFAULT,
  customScale,
}: {
  workArea: Rectangle;
  anchor: { x: number; y: number };
  bubble: { width: number; height: number };
  size?: CodexPetSize;
  customScale?: number;
}): CodexPetLayoutDecision {
  const rendered =
    typeof customScale === "number" &&
    Number.isFinite(customScale) &&
    customScale > 0
      ? codexPetRenderedSpriteForScale(customScale)
      : codexPetRenderedSpriteForSize(size);
  const fits = (bounds: Rectangle) =>
    bounds.x >= workArea.x &&
    bounds.y >= workArea.y &&
    bounds.x + bounds.width <= workArea.x + workArea.width &&
    bounds.y + bounds.height <= workArea.y + workArea.height;
  const order: Array<Exclude<CodexPetBubbleLayout, "hidden">> = [
    "above",
    "right",
    "left",
    "below",
  ];
  for (const layout of order) {
    const bounds = codexPetBoundsForLayout({
      layout,
      anchor,
      bubble,
      size,
      customScale,
    });
    if (fits(bounds)) return { layout, bounds };
  }
  // Near a side edge nothing centered fits, which would hide even an open
  // quick panel. Slide an above/below window along the edge instead, as long
  // as the sprite stays inside it.
  for (const layout of ["above", "below"] as const) {
    const bounds = codexPetBoundsForLayout({
      layout,
      anchor,
      bubble,
      size,
      customScale,
    });
    const x = Math.min(
      Math.max(bounds.x, workArea.x),
      workArea.x + workArea.width - bounds.width,
    );
    const spriteShift = bounds.x - x;
    const slid = { ...bounds, x };
    if (fits(slid) && Math.abs(spriteShift) <= (bounds.width - rendered.width) / 2) {
      return { layout, bounds: slid, spriteShift };
    }
  }
  return {
    layout: "hidden",
    bounds: {
      x: Math.round(anchor.x - rendered.windowWidth / 2),
      y: Math.round(
        anchor.y - rendered.windowHeight + CODEX_PET_SPRITE_BOTTOM_OFFSET,
      ),
      width: rendered.windowWidth,
      height: rendered.windowHeight,
    },
  };
}

// Escapes JSON for embedding in an inline <script>: `<` becomes a plain JS
// string escape, so pushed text can never terminate the block (e.g. a
// preview containing "</script>") while the parsed value stays unchanged.
function scriptJSON(value: unknown): string {
  return JSON.stringify(value).replace(/</g, "\\u003c");
}

export function codexPetWindowHTML(
  view: CodexPetView,
  locale: AppLocale = getMainLocale(),
): string {
  const resizeLabel = mainTranslate("resize", {}, locale);
  // `{title}` placeholders survive translation and are filled in the page.
  const text = {
    conversation: mainTranslate("conversation", {}, locale),
    openConversation: mainTranslate("openConversation", {}, locale),
    sendTo: mainTranslate("petSendTo", {}, locale),
    current: mainTranslate("petCurrentConversation", {}, locale),
    placeholder: mainTranslate("petInputPlaceholder", {}, locale),
    needsAnswer: mainTranslate("petNeedsAnswer", {}, locale),
    sending: mainTranslate("petSending", {}, locale),
    sendFailed: mainTranslate("petSendFailed", {}, locale),
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
  return `<!doctype html>
<html lang="${locale}"><head><meta charset="utf-8" />
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src wuu-file:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; navigate-to wuu-pet:" />
<meta name="viewport" content="width=device-width,initial-scale=1" />
<style>
*{box-sizing:border-box}
html,body{width:100%;height:100%;margin:0;background:transparent;overflow:hidden;font:13px/${CODEX_PET_BUBBLE_ROW_LINE}px -apple-system,BlinkMacSystemFont,system-ui,sans-serif}
.stage{--bubble-bg:rgba(255,255,255,.96);--bubble-fg:#1a1a1a;--bubble-muted:#555;--bubble-rule:rgba(0,0,0,.1);--field-bg:rgba(0,0,0,.035);--accent:#2f6fec;--target-bg:rgba(47,111,236,.1);--dot-idle:#a1a1aa;--dot-running:#3b82f6;--dot-done:#22a06b;--dot-failed:#e5484d;--dot-review:#e8833a}
@media (prefers-color-scheme:dark){.stage{--bubble-bg:rgba(38,38,42,.96);--bubble-fg:#f2f2f2;--bubble-muted:#b4b4b4;--bubble-rule:rgba(255,255,255,.14);--field-bg:rgba(255,255,255,.06);--accent:#7aa5ff;--target-bg:rgba(122,165,255,.16)}}
.stage{position:relative;width:100%;height:100%;padding-bottom:${CODEX_PET_SPRITE_BOTTOM_OFFSET}px;display:flex;align-items:center;justify-content:center;flex-direction:column;cursor:grab;-webkit-user-select:none;user-select:none}
.stage.is-dragging{cursor:grabbing}
.stage[data-layout=above],.stage[data-layout=hidden]{flex-direction:column;justify-content:flex-end}
.stage[data-layout=below]{flex-direction:column-reverse;justify-content:flex-end;padding:${CODEX_PET_BUBBLE_PADDING}px 0 0}
.stage[data-layout=right]{flex-direction:row-reverse;justify-content:flex-start;align-items:flex-end}
.stage[data-layout=left]{flex-direction:row;justify-content:flex-start;align-items:flex-end}
.stage[data-layout=right] .bubble,.stage[data-layout=left] .bubble{margin-bottom:0}
.sprite-frame{position:relative;left:var(--sprite-shift);width:var(--pet-frame-width);height:var(--pet-frame-height);display:flex;align-items:flex-end;justify-content:center;flex-shrink:0}
.sprite{width:${CODEX_PET_CELL_WIDTH}px;height:${CODEX_PET_CELL_HEIGHT}px;flex-shrink:0;transform:scale(var(--pet-scale));transform-origin:bottom center;background-image:var(--pet-sheet);background-repeat:no-repeat;background-position:0 0;image-rendering:pixelated;filter:drop-shadow(0 10px 16px rgba(0,0,0,.18));pointer-events:none}
.badge{position:absolute;top:0;right:0;min-width:18px;height:18px;padding:0 5px;border-radius:9px;background:var(--dot-done);color:#fff;font-size:11px;font-weight:600;line-height:18px;text-align:center;pointer-events:none}
.badge.is-urgent{background:var(--dot-review)}
.badge[hidden]{display:none}
.bubble{display:flex;flex-direction:column;width:${CODEX_PET_BUBBLE_WIDTH}px;padding:${CODEX_PET_BUBBLE_INSET}px;margin:${CODEX_PET_BUBBLE_PADDING}px;border-radius:12px;background:var(--bubble-bg);color:var(--bubble-fg);box-shadow:0 6px 24px rgba(0,0,0,.18);flex-shrink:0;text-align:left}
.bubble[hidden],.stage[data-layout=hidden] .bubble{display:none}
.rows{display:flex;flex-direction:column;gap:${CODEX_PET_BUBBLE_ROW_GAP}px}
.rows:empty{display:none}
.hint-row{display:flex;align-items:center;gap:6px;height:${CODEX_PET_BUBBLE_ROW_LINE}px;margin:0 -4px;padding:0 4px;border-radius:5px;cursor:pointer;opacity:1;transition:opacity ${CODEX_PET_BUBBLE_SWAP_IN_MS}ms ease}
.rows.is-swapping .hint-row{opacity:0;transition:opacity ${CODEX_PET_BUBBLE_SWAP_OUT_MS}ms ease}
.hint-row:focus-visible,.row-open:focus-visible{outline:2px solid var(--accent);outline-offset:1px}
.stage.is-expanded .hint-row.is-target{background:var(--target-bg)}
.dot{width:6px;height:6px;border-radius:50%;flex-shrink:0;background:var(--dot-idle)}
.hint-row[data-status=running] .dot{background:var(--dot-running)}
.hint-row[data-status=done] .dot{background:var(--dot-done)}
.hint-row[data-status=failed] .dot{background:var(--dot-failed)}
.hint-row[data-status=needs_review] .dot{background:var(--dot-review)}
.row-title{flex-shrink:0;max-width:96px;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.row-preview{flex:1;min-width:0;color:var(--bubble-muted);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.row-open{display:none;flex-shrink:0;width:${CODEX_PET_BUBBLE_ROW_LINE}px;height:${CODEX_PET_BUBBLE_ROW_LINE}px;padding:0;border:0;border-radius:5px;background:transparent;color:var(--bubble-muted);font:inherit;cursor:pointer}
.row-open:hover{color:var(--bubble-fg);background:var(--target-bg)}
.stage.is-expanded .row-open{display:block}
.composer{display:none;flex-direction:column;gap:${CODEX_PET_PANEL_TARGET_GAP}px}
.stage.is-expanded .composer{display:flex}
.rows:not(:empty)+.composer{margin-top:9px;padding-top:10px;border-top:1px solid var(--bubble-rule)}
.composer-target{height:${CODEX_PET_PANEL_TARGET_LINE}px;font-size:12px;line-height:${CODEX_PET_PANEL_TARGET_LINE}px;color:var(--bubble-muted);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.composer-target.is-error{color:var(--dot-failed)}
.composer-target.is-blocked{color:var(--dot-review)}
.composer textarea{display:block;width:100%;height:${CODEX_PET_PANEL_INPUT_HEIGHT}px;margin:0;padding:6px 8px;border:1px solid var(--bubble-rule);border-radius:8px;background:var(--field-bg);color:inherit;font:inherit;line-height:18px;resize:none;outline:none;cursor:text;-webkit-user-select:text;user-select:text}
.composer textarea:focus{border-color:var(--accent)}
.composer textarea::placeholder{color:var(--bubble-muted)}
.composer textarea:disabled{cursor:default;opacity:.7}
.resize-handle{position:absolute;z-index:2}
.stage.is-expanded .resize-handle{display:none}
.resize-handle.edge-top{top:0;left:16px;right:16px;height:16px;cursor:ns-resize}
.resize-handle.edge-bottom{bottom:0;left:16px;right:16px;height:16px;cursor:ns-resize}
.resize-handle.edge-left{left:0;top:16px;bottom:16px;width:16px;cursor:ew-resize}
.resize-handle.edge-right{right:0;top:16px;bottom:16px;width:16px;cursor:ew-resize}
.resize-handle.corner-nw{top:0;left:0;width:16px;height:16px;cursor:nwse-resize}
.resize-handle.corner-ne{top:0;right:0;width:16px;height:16px;cursor:nesw-resize}
.resize-handle.corner-sw{bottom:0;left:0;width:16px;height:16px;cursor:nesw-resize}
.resize-handle.corner-se{bottom:0;right:0;width:16px;height:16px;cursor:nwse-resize}
@media (prefers-reduced-motion:reduce){.hint-row,.rows.is-swapping .hint-row{transition:none}}
</style></head><body><div class="stage" data-layout="${escapeHTML(view.layout)}" style="${escapeHTML(styleVars)}">
<div class="bubble" hidden><div class="rows"></div><div class="composer"><div class="composer-target"></div><textarea rows="3" maxlength="${CODEX_PET_SUBMIT_MAX}" spellcheck="false"></textarea></div></div>
<div class="sprite-frame"><div class="sprite" role="img" aria-label="${escapeHTML(view.label)}"></div><div class="badge" hidden></div></div>
${resizeHandles}
</div>
<script>
(() => {
  const stage = document.querySelector('.stage');
  const bubble = document.querySelector('.bubble');
  const rowsEl = bubble.querySelector('.rows');
  const targetEl = bubble.querySelector('.composer-target');
  const input = bubble.querySelector('textarea');
  const spriteFrame = document.querySelector('.sprite-frame');
  const sprite = document.querySelector('.sprite');
  const badge = document.querySelector('.badge');
  const STATES = ${scriptJSON(states)};
  const TEXT = ${scriptJSON(text)};
  input.placeholder = TEXT.placeholder;
  const MOOD_STATES = { idle: 'idle', running: 'running', waiting: 'waiting' };
  const CELL_WIDTH = ${CODEX_PET_CELL_WIDTH};
  const CELL_HEIGHT = ${CODEX_PET_CELL_HEIGHT};
  const LOOK_FIRST_ROW = ${CODEX_PET_LOOK_FIRST_ROW};
  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
  const fill = (template, title) => template.replace('{title}', () => title);
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

  // Bubble rows. Rows are rebuilt via DOM APIs (textContent, never
  // innerHTML) so pushed strings can't inject markup. Content changes fade
  // the rows out, rebuild, and fade back in; cancelSwap clears a pending
  // swap so a fast second push doesn't strand rows mid-fade.
  let currentHints = [];
  let pendingSwap = null;
  const SWAP_OUT_MS = ${CODEX_PET_BUBBLE_SWAP_OUT_MS};
  const hintsSignature = (hints) =>
    JSON.stringify(hints.map((h) => [h.thread_id, h.title, h.preview, h.status]));
  const buildRows = (hints) => {
    rowsEl.textContent = '';
    for (const hint of hints) {
      const title = hint.title || TEXT.conversation;
      const row = document.createElement('div');
      row.className = 'hint-row';
      row.dataset.threadId = hint.thread_id;
      row.dataset.status = hint.status;
      row.setAttribute('role', 'button');
      const dot = document.createElement('span');
      dot.className = 'dot';
      const titleEl = document.createElement('span');
      titleEl.className = 'row-title';
      titleEl.textContent = title;
      const preview = document.createElement('span');
      preview.className = 'row-preview';
      preview.textContent = hint.preview;
      const open = document.createElement('button');
      open.type = 'button';
      open.className = 'row-open';
      open.textContent = '↗';
      open.title = fill(TEXT.openConversation, title);
      open.setAttribute('aria-label', open.title);
      row.append(dot, titleEl, preview, open);
      rowsEl.append(row);
    }
    syncComposer();
  };
  const cancelSwap = () => {
    if (pendingSwap === null) return;
    clearTimeout(pendingSwap);
    pendingSwap = null;
    rowsEl.classList.remove('is-swapping');
  };
  const syncBubble = () => {
    bubble.hidden = currentHints.length === 0 && !expanded;
  };
  const applyHints = (hints, animated) => {
    cancelSwap();
    const usable = (hints || []).filter(
      (h) => h && typeof h.thread_id === 'string' && (h.preview || '').trim(),
    );
    if (hintsSignature(usable) === hintsSignature(currentHints)) return;
    const swap = () => {
      pendingSwap = null;
      rowsEl.classList.remove('is-swapping');
      currentHints = usable;
      buildRows(usable);
      syncBubble();
      syncBadge();
    };
    if (!animated || currentHints.length === 0 || reducedMotion.matches) {
      swap();
      return;
    }
    rowsEl.classList.add('is-swapping');
    pendingSwap = setTimeout(swap, SWAP_OUT_MS);
  };
  // With the bubble off screen (no room for any layout), a badge on the
  // sprite counts the rows that want attention.
  const syncBadge = () => {
    const attention = currentHints.filter((h) => h.attention);
    badge.hidden = view.layout !== 'hidden' || attention.length === 0;
    badge.textContent = String(attention.length);
    badge.classList.toggle(
      'is-urgent',
      attention.some((h) => h.status === 'needs_review' || h.status === 'failed'),
    );
  };

  // Quick panel. The target is the conversation a submit goes to: a row
  // the user picked, or the conversation open in the main window. A picked
  // target keeps its title after its row leaves the bubble so a send is
  // never silently redirected.
  let expanded = false;
  let target = null;
  let sending = false;
  let failed = false;
  const syncComposer = () => {
    const targetHint = target && currentHints.find((h) => h.thread_id === target.id);
    const blocked = Boolean(targetHint && targetHint.status === 'needs_review');
    for (const row of rowsEl.children) {
      row.classList.toggle('is-target', Boolean(target) && row.dataset.threadId === target.id);
      row.tabIndex = expanded ? 0 : -1;
      row.querySelector('.row-open').tabIndex = expanded ? 0 : -1;
    }
    targetEl.classList.toggle('is-error', failed);
    targetEl.classList.toggle('is-blocked', blocked && !failed && !sending);
    // A blocked target keeps any draft in the disabled input, so the reason
    // goes on the target line rather than in the placeholder.
    targetEl.textContent = failed
      ? TEXT.sendFailed
      : sending
        ? TEXT.sending
        : blocked
          ? TEXT.needsAnswer
          : target
            ? fill(TEXT.sendTo, target.title)
            : TEXT.current;
    targetEl.title = targetEl.textContent;
    input.disabled = blocked;
    input.readOnly = sending;
  };
  const setExpanded = (next) => {
    if (next === expanded) return;
    expanded = next;
    stage.classList.toggle('is-expanded', expanded);
    if (expanded) {
      requestAnimationFrame(() => { if (expanded && !input.disabled) input.focus(); });
    } else {
      // Rows leave the tab order when collapsed; drop focus too so a clicked
      // row does not keep its focus ring in the glance bubble.
      if (bubble.contains(document.activeElement)) document.activeElement.blur();
      if (!sending) {
        target = null;
        failed = false;
      }
    }
    syncComposer();
    syncBubble();
  };
  const toggleTarget = (row) => {
    const id = row.dataset.threadId;
    const hint = currentHints.find((h) => h.thread_id === id);
    target = target && target.id === id ? null : { id, title: (hint && hint.title) || TEXT.conversation };
    failed = false;
    syncComposer();
    if (!input.disabled) input.focus();
  };
  const submit = () => {
    const text = input.value.trim();
    if (!text || sending || input.disabled) return;
    sending = true;
    failed = false;
    syncComposer();
    send('submit?text=' + encodeURIComponent(text) + (target ? '&thread_id=' + encodeURIComponent(target.id) : ''));
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
    if (!failed) return;
    failed = false;
    syncComposer();
  });
  window.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && expanded) {
      event.preventDefault();
      send('panel?open=0');
      return;
    }
    const row = event.target instanceof Element && event.target.classList.contains('hint-row') ? event.target : null;
    if (row && (event.key === 'Enter' || event.key === ' ')) {
      event.preventDefault();
      toggleTarget(row);
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
    syncBadge();
    if (sheetChanged) probeSheet(next.spritesheetURL);
  };
  window.addEventListener('contextmenu', (event) => {
    event.preventDefault();
    location.href = 'wuu-pet://action/menu';
  });
  // Standard window-style continuous resize: four 16px corner squares
  // and four edge strips between them (all invisible — the directional
  // resize cursors are the affordance; earlier revisions painted a pair
  // of dark grip bars that read as a stray black line over the pet).
  // Everything still maps to the single uniform sprite scale: each
  // pointermove converts the drag delta along the handle's outward
  // direction (data-dir-x / data-dir-y) into a scale delta. The sprite
  // is a ${CODEX_PET_CELL_WIDTH}×${CODEX_PET_CELL_HEIGHT} cell under transform: scale, so 1px of drag =
  // 1px of rendered sprite width/height on that axis; corners average
  // both axes so a 45° drag tracks ~1:1 too. Dragging away from the
  // sprite grows (window-edge semantics), toward it shrinks. The main
  // process live-updates window bounds and CSS scale around the
  // sprite's bottom-center anchor; emission is rAF-throttled so a fast
  // drag doesn't queue more navigations than frames, and pointerup
  // re-sends the final value with commit=1 so the host persists it.
  // stopPropagation on pointerdown keeps the stage's move-the-window
  // drag from engaging, and a 2px dead-zone keeps a plain click from
  // emitting anything.
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
  // click: on the pet it toggles the quick panel; on a row it opens that
  // conversation (collapsed) or picks it as the send target (expanded).
  // A longer move drags the window, and the pet runs in the drag
  // direction until the pointer rests.
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
    // Inside the open panel the pointer selects text and presses controls.
    if (expanded && event.target instanceof Element && event.target.closest('.bubble')) return;
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
    const row = element.closest('.hint-row');
    if (row) {
      if (expanded && !element.closest('.row-open')) toggleTarget(row);
      else send('jump?thread_id=' + encodeURIComponent(row.dataset.threadId));
      return;
    }
    if (element.closest('.bubble')) return;
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
  // Sends text to Wuu; without a thread id it targets the conversation open
  // in the main window.
  onSubmit: (text: string, threadID?: string) => Promise<CodexPetSubmitResult>;
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
    // rows without commentary, but a malformed push must not produce
    // phantom rows or an oversized bubble. Cap at CODEX_PET_HINTS_MAX.
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
    this.refresh();
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
    this.expanded = false;
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
    this.refresh();
    // The composer needs keyboard focus; the pet window is otherwise
    // shown inactive and never takes focus from the user's work.
    if (expanded && this.win && !this.win.isDestroyed()) this.win.focus();
    this.updateLook();
  }

  private submit(text: string, threadID: string | undefined): void {
    const trimmed = text.trim();
    const result =
      trimmed && trimmed.length <= CODEX_PET_SUBMIT_MAX
        ? this.options.onSubmit(trimmed, threadID)
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
    const rendered = this.renderedSprite();
    const initialAnchor = {
      x:
        workArea.x +
        workArea.width -
        rendered.windowWidth / 2 -
        CODEX_PET_SCREEN_INSET,
      y:
        workArea.y +
        workArea.height -
        CODEX_PET_SPRITE_BOTTOM_OFFSET -
        CODEX_PET_SCREEN_INSET,
    };
    const { layout, bounds, spriteShift = 0 } = this.layoutAround(initialAnchor, workArea);
    this.currentLayout = layout;
    this.spriteShift = spriteShift;
    const view = this.view(pet, layout);
    this.appliedScale = rendered.scale;
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

  private renderedSprite(): CodexPetRenderedSprite {
    return typeof this.currentScale === "number"
      ? codexPetRenderedSpriteForScale(this.currentScale)
      : codexPetRenderedSpriteForSize(this.size);
  }

  private view(pet: CodexPet, layout: CodexPetBubbleLayout): CodexPetView {
    return codexPetView(pet, {
      mood: this.mood,
      hints: this.hints,
      layout,
      expanded: this.expanded,
      size: this.size,
      customScale: this.currentScale,
      spriteShift: this.spriteShift,
    });
  }

  // Window bounds that keep the sprite's bottom-center at `anchor`, with
  // the bubble (when there is one) in the first direction that fits.
  private layoutAround(anchor: { x: number; y: number }, workArea: Rectangle): CodexPetLayoutDecision {
    if (this.hints.length || this.expanded) {
      return selectCodexPetBubbleLayout({
        workArea,
        anchor,
        bubble: codexPetBubbleSizeForCount(this.hints.length, this.expanded),
        size: this.size,
        customScale: this.currentScale,
      });
    }
    const rendered = this.renderedSprite();
    return {
      layout: "hidden",
      bounds: {
        x: Math.round(anchor.x - rendered.windowWidth / 2),
        y: Math.round(anchor.y - rendered.windowHeight + CODEX_PET_SPRITE_BOTTOM_OFFSET),
        width: rendered.windowWidth,
        height: rendered.windowHeight,
      },
    };
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
    const rendered =
      typeof this.appliedScale === "number"
        ? codexPetRenderedSpriteForScale(this.appliedScale)
        : codexPetRenderedSpriteForSize(this.size);
    const spriteW = rendered.width;
    const y = layout === "below"
      ? bounds.y + CODEX_PET_BUBBLE_PADDING + rendered.height
      : bounds.y + bounds.height - CODEX_PET_SPRITE_BOTTOM_OFFSET;
    let x: number;
    switch (layout) {
      case "above":
      case "below":
      case "hidden":
        x = bounds.x + bounds.width / 2 + this.spriteShift;
        break;
      case "right":
        x = bounds.x + CODEX_PET_BUBBLE_PADDING + spriteW / 2;
        break;
      case "left":
        x = bounds.x + bounds.width - CODEX_PET_BUBBLE_PADDING - spriteW / 2;
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
    const sprite = typeof this.appliedScale === "number"
      ? codexPetRenderedSpriteForScale(this.appliedScale)
      : codexPetRenderedSpriteForSize(this.size);
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
