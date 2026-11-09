import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  CodexPetHint,
  CodexPetsSnapshot,
} from "../shared/protocol";
import {
  CODEX_PET_SUBMIT_MAX,
  CodexPetWindowManager,
  type CodexPetWindowOptions,
  codexPetActionFromURL,
  codexPetBoundsForLayout,
  codexPetRenderedSpriteForSize,
  codexPetView,
  codexPetWindowHTML,
  selectCodexPetBubbleLayout,
  selectedCodexPet,
} from "./codexPetWindow";

// Hoisted electron mocks for the CodexPetWindowManager integration test.
// vi.hoisted runs before module imports, so the mock function and the
// shared captured-listener map are available when the "electron" factory
// below captures them. We use vi.fn() instead of a class so the
// constructor call itself is a tracked spy (`new` on a vi.fn() records
// the call), and a single shared capturedListeners map keeps the
// listeners in scope across the multiple BrowserWindow instances the
// manager may create.
const codexPetElectronMocks = vi.hoisted(() => {
  const capturedListeners: Record<string, Array<(...args: unknown[]) => void>> = {};
  const loadURL = vi.fn();
  const showInactive = vi.fn();
  const close = vi.fn();
  const isDestroyed = vi.fn(() => false);
  const isVisible = vi.fn(() => false);
  const setWindowOpenHandler = vi.fn();
  const executeJavaScript = vi.fn().mockResolvedValue(undefined);
  const popup = vi.fn();
  const setAlwaysOnTop = vi.fn();
  const setHasShadow = vi.fn();
  const setVisibleOnAllWorkspaces = vi.fn();
  const focus = vi.fn();
  const cursor = { x: 0, y: 0 };
  const initialBounds = { x: 0, y: 0, width: 120, height: 168 };
  const boundsStack: Array<{ x: number; y: number; width: number; height: number }> = [initialBounds];
  const setBounds = vi.fn((next: { x: number; y: number; width: number; height: number }) => {
    boundsStack[boundsStack.length - 1] = next;
  });
  const getBounds = vi.fn(() => boundsStack[boundsStack.length - 1]);

  // `new BrowserWindow(options)` becomes a tracked spy call. The impl
  // returns a fresh mock instance each time so per-window state (e.g.
  // the listener maps above) can stay shared across instances.
  const BrowserWindow = vi.fn().mockImplementation(() => ({
    webContents: {
      on: (event: string, listener: (...args: unknown[]) => void) => {
        (capturedListeners[`wc:${event}`] ??= []).push(listener);
      },
      setWindowOpenHandler,
      executeJavaScript,
    },
    on: (event: string, listener: (...args: unknown[]) => void) => {
      (capturedListeners[`win:${event}`] ??= []).push(listener);
    },
    once: (event: string, listener: (...args: unknown[]) => void) => {
      (capturedListeners[`win:${event}:once`] ??= []).push(listener);
    },
    showInactive,
    close,
    isDestroyed,
    isVisible,
    setAlwaysOnTop,
    setHasShadow,
    setVisibleOnAllWorkspaces,
    focus,
    loadURL,
    setBounds,
    getBounds,
  }));

  return {
    capturedListeners,
    loadURL,
    showInactive,
    close,
    isDestroyed,
    isVisible,
    setWindowOpenHandler,
    executeJavaScript,
    popup,
    setBounds,
    getBounds,
    focus,
    cursor,
    BrowserWindow,
  };
});

vi.mock("electron", () => ({
  BrowserWindow: codexPetElectronMocks.BrowserWindow,
  Menu: {
    buildFromTemplate: () => ({ popup: codexPetElectronMocks.popup }),
  },
  screen: {
    getPrimaryDisplay: () => ({
      workArea: { x: 0, y: 0, width: 1920, height: 1080 },
    }),
    getDisplayMatching: () => ({
      workArea: { x: 0, y: 0, width: 1920, height: 1080 },
    }),
    getCursorScreenPoint: () => ({ ...codexPetElectronMocks.cursor }),
  },
}));

function snapshot(enabled: boolean, selectedID = "alpha"): CodexPetsSnapshot {
  return {
    home: "/tmp/pets",
    enabled,
    selected_id: selectedID,
    errors: [],
    pets: [
      {
        id: "alpha",
        display_name: "Alpha Pet",
        description: "",
        manifest_path: "/tmp/pets/alpha/pet.json",
        spritesheet_path: "/tmp/pets/alpha/spritesheet.webp",
        spritesheet_url: "wuu-file://local/alpha",
      },
    ],
  };
}

const sampleHint: CodexPetHint = {
  thread_id: "thread-42",
  title: "Refactor auth flow",
  status: "running",
  preview: "正在执行 npm test",
  attention: false,
  updated_at: 1700000000000,
};

describe("selectedCodexPet", () => {
  it("returns nothing while pets are disabled", () => {
    expect(selectedCodexPet(snapshot(false))).toBeUndefined();
    expect(selectedCodexPet(undefined)).toBeUndefined();
  });

  it("falls back to the first pet when the selection is stale", () => {
    expect(selectedCodexPet(snapshot(true, "missing"))?.id).toBe("alpha");
  });
});

describe("codexPetRenderedSpriteForSize", () => {
  it("returns the base footprint at the default size", () => {
    const rendered = codexPetRenderedSpriteForSize("default");
    expect(rendered.width).toBe(96);
    expect(rendered.height).toBe(104);
    expect(rendered.scale).toBe(0.5);
  });

  it("scales the sprite footprint for non-default sizes", () => {
    const small = codexPetRenderedSpriteForSize("small");
    expect(small.width).toBe(72);
    expect(small.height).toBe(78);
    expect(small.scale).toBeCloseTo(0.375);

    const large = codexPetRenderedSpriteForSize("large");
    expect(large.width).toBe(144);
    expect(large.height).toBe(156);
    expect(large.scale).toBeCloseTo(0.75);

    const xl = codexPetRenderedSpriteForSize("extra-large");
    expect(xl.width).toBe(192);
    expect(xl.height).toBe(208);
    expect(xl.scale).toBe(1.0);
  });
});

describe("codexPetView", () => {
  it("carries mood, hints, layout, and panel state to the page", () => {
    const view = codexPetView(snapshot(true).pets[0], {
      mood: "running",
      hints: [sampleHint],
      layout: "above",
      expanded: true,
    });
    expect(view).toMatchObject({
      spritesheetURL: "wuu-file://local/alpha",
      mood: "running",
      label: "Alpha Pet",
      hints: [sampleHint],
      layout: "above",
      expanded: true,
      size: "default",
    });
  });

  it("threads the size into the per-size sprite geometry when provided", () => {
    const view = codexPetView(snapshot(true).pets[0], {
      mood: "idle",
      hints: [],
      layout: "hidden",
      size: "large",
    });
    expect(view.spriteWidth).toBe(144);
    expect(view.spriteHeight).toBe(156);
    expect(view.scale).toBeCloseTo(0.75);
  });
});

// The page itself is exercised end to end (scripts/codex-pet-e2e.cjs); these
// checks cover the security boundary of the generated document.
describe("codexPetWindowHTML", () => {
  it("locks the page down to the spritesheet and pet actions", () => {
    const html = codexPetWindowHTML(
      codexPetView(snapshot(true).pets[0], { mood: "idle", hints: [], layout: "hidden" }),
    );
    expect(html).toContain("default-src 'none'");
    expect(html).toContain("img-src wuu-file:");
    expect(html).toContain("navigate-to wuu-pet:");
  });

  it("keeps hostile hint and pet text inert", () => {
    const pet = { ...snapshot(true).pets[0], display_name: '"><img src=x onerror=alert(1)>' };
    const hostile: CodexPetHint = {
      ...sampleHint,
      title: "</script><script>bad()</script>",
      preview: "<!-- <script>bad()</script>",
    };
    const html = codexPetWindowHTML(
      codexPetView(pet, { mood: "idle", hints: [hostile], layout: "above" }),
    );
    expect(html).not.toContain("<script>bad()");
    expect(html).not.toContain("<!--");
    expect(html).not.toContain("<img src=x");
    expect(html.match(/<script>/g)).toHaveLength(1);
  });
});

describe("codexPetActionFromURL", () => {
  it("accepts the pet menu action", () => {
    expect(codexPetActionFromURL("wuu-pet://action/menu")).toEqual({ action: "menu" });
    expect(codexPetActionFromURL("wuu-pet://action/close")).toBeUndefined();
    expect(codexPetActionFromURL("wuu-cua://action/menu")).toBeUndefined();
    expect(codexPetActionFromURL("not a url")).toBeUndefined();
  });

  it("accepts the jump action only with a thread_id", () => {
    expect(codexPetActionFromURL("wuu-pet://action/jump?thread_id=abc")).toEqual({
      action: "jump",
      thread_id: "abc",
    });
    expect(codexPetActionFromURL("wuu-pet://action/jump")).toBeUndefined();
  });

  it("parses dismiss only with a thread_id, and show", () => {
    expect(codexPetActionFromURL("wuu-pet://action/dismiss?thread_id=abc")).toEqual({
      action: "dismiss",
      thread_id: "abc",
    });
    expect(codexPetActionFromURL("wuu-pet://action/dismiss")).toBeUndefined();
    expect(codexPetActionFromURL("wuu-pet://action/show")).toEqual({ action: "show" });
  });

  it("parses panel, submit, and look actions", () => {
    expect(codexPetActionFromURL("wuu-pet://action/panel?open=1")).toEqual({ action: "panel", open: true });
    expect(codexPetActionFromURL("wuu-pet://action/panel?open=0")).toEqual({ action: "panel", open: false });
    expect(
      codexPetActionFromURL(`wuu-pet://action/submit?text=${encodeURIComponent("修一下 & 测试")}&thread_id=t-1`),
    ).toEqual({ action: "submit", text: "修一下 & 测试", thread_id: "t-1" });
    expect(codexPetActionFromURL("wuu-pet://action/submit?text=hi")).toEqual({ action: "submit", text: "hi" });
    expect(codexPetActionFromURL("wuu-pet://action/look?capable=1")).toEqual({ action: "look", capable: true });
    expect(codexPetActionFromURL("wuu-pet://action/look?capable=0")).toEqual({ action: "look", capable: false });
  });

  it("parses the continuous scale action with clamping and the commit marker", () => {
    expect(codexPetActionFromURL("wuu-pet://action/scale?value=0.62")).toEqual({
      action: "scale",
      value: 0.62,
      commit: false,
    });
    expect(
      codexPetActionFromURL("wuu-pet://action/scale?value=0.62&commit=1"),
    ).toEqual({ action: "scale", value: 0.62, commit: true });
    // Out-of-range values clamp instead of resizing the pet into
    // uselessness; non-finite / missing values are dropped entirely.
    expect(codexPetActionFromURL("wuu-pet://action/scale?value=99")).toEqual({
      action: "scale",
      value: 1.5,
      commit: false,
    });
    expect(codexPetActionFromURL("wuu-pet://action/scale?value=0")).toEqual({
      action: "scale",
      value: 0.25,
      commit: false,
    });
    expect(codexPetActionFromURL("wuu-pet://action/scale?value=abc")).toBeUndefined();
    expect(codexPetActionFromURL("wuu-pet://action/scale")).toBeUndefined();
  });
});

describe("codexPetBoundsForLayout", () => {
  const anchor = { x: 1000, y: 900 };

  // Default sprite 96×104 over a 4px gap and a 36px dock: a 96×144 column
  // whose sprite bottom sits 48px above the window bottom (dock + padding).
  it("places cards above and centers them horizontally over the sprite", () => {
    const bounds = codexPetBoundsForLayout({ layout: "above", anchor, bubble: { width: 280, height: 80 } });
    expect(bounds.width).toBe(296); // card width + 2 × 8 padding
    expect(bounds.height).toBe(248); // 8 + 80 + 8 + 144 + 8
    expect(bounds.x).toBe(852); // anchor.x - width/2
    expect(bounds.y).toBe(700); // anchor.y - (height - 48)
  });

  it("places the cards to the right of the sprite with sprite anchor preserved", () => {
    const bounds = codexPetBoundsForLayout({ layout: "right", anchor, bubble: { width: 280, height: 80 } });
    expect(bounds.width).toBe(400); // 8 + 96 + 8 + 280 + 8
    expect(bounds.height).toBe(160); // column 144 + 2 × 8
    expect(bounds.x).toBe(944); // anchor.x - 8 - 48
    expect(bounds.y).toBe(788); // anchor.y - (height - 48)
  });

  it("puts the sprite at the top of a 'below' window", () => {
    const bounds = codexPetBoundsForLayout({ layout: "below", anchor, bubble: { width: 280, height: 80 } });
    expect(bounds.height).toBe(248);
    expect(bounds.y).toBe(788); // anchor.y - 104 - 8
  });

  it("widens the pet column to the composer while the panel is open", () => {
    const bounds = codexPetBoundsForLayout({ layout: "hidden", anchor, expanded: true });
    expect(bounds.width).toBe(304); // composer 280 + 24
    expect(bounds.height).toBe(168); // 16 + 104 + 48
    expect(bounds.x).toBe(848);
    expect(bounds.y).toBe(780);
  });

  it("uses the size's sprite footprint when one is supplied", () => {
    const bounds = codexPetBoundsForLayout({
      layout: "above",
      anchor,
      bubble: { width: 280, height: 80 },
      size: "large",
    });
    // large: sprite 144×156 → column 196 → height = 8 + 80 + 8 + 196 + 8 = 300
    expect(bounds.width).toBe(296); // cards still drive width
    expect(bounds.height).toBe(300);
  });
});

describe("selectCodexPetBubbleLayout", () => {
  const bubble = { width: 280, height: 80 };

  it("picks 'above' when there is room above the sprite", () => {
    const workArea = { x: 0, y: 0, width: 1920, height: 1080 };
    const anchor = { x: 960, y: 900 };
    const decision = selectCodexPetBubbleLayout({ workArea, anchor, bubble });
    expect(decision.layout).toBe("above");
  });

  it("falls back to 'right' when the sprite sits too close to the top edge", () => {
    const workArea = { x: 0, y: 0, width: 1920, height: 1080 };
    // anchor.y is small enough that 'above' (height 248) overflows the top
    // edge, but 'right' (height 160) still fits beside the sprite.
    const anchor = { x: 960, y: 150 };
    const decision = selectCodexPetBubbleLayout({ workArea, anchor, bubble });
    expect(decision.layout).toBe("right");
  });

  it("returns 'hidden' when no layout fits inside the workArea", () => {
    const workArea = { x: 0, y: 0, width: 400, height: 60 }; // tiny workArea, nothing fits
    const anchor = { x: 200, y: 50 };
    const decision = selectCodexPetBubbleLayout({ workArea, anchor, bubble });
    expect(decision.layout).toBe("hidden");
    expect(decision.bounds.width).toBe(120);
    expect(decision.bounds.height).toBe(168);
  });

  // An open composer at a side edge would otherwise have no layout that fits
  // and fall back to unclamped bounds partly off screen.
  it("slides a centered window along a side edge and reports the sprite's offset", () => {
    const workArea = { x: 0, y: 0, width: 1920, height: 1080 };
    const decision = selectCodexPetBubbleLayout({ workArea, anchor: { x: 60, y: 900 }, expanded: true });
    expect(decision).toEqual({
      layout: "hidden",
      bounds: { x: 0, y: 780, width: 304, height: 168 },
      spriteShift: -92, // unclamped x was 60 - 152
    });
  });

  it("does not slide so far that the sprite leaves the window", () => {
    const workArea = { x: 0, y: 0, width: 1920, height: 1080 };
    const decision = selectCodexPetBubbleLayout({ workArea, anchor: { x: 30, y: 900 }, expanded: true });
    // Sliding by 122 would exceed the (304 - 96) / 2 = 104 the sprite allows.
    expect(decision.spriteShift).toBeUndefined();
    expect(decision.bounds.x).toBe(-122);
  });

  it("scales the hidden fallback bounds when a non-default size is requested", () => {
    const workArea = { x: 0, y: 0, width: 400, height: 60 };
    const anchor = { x: 200, y: 50 };
    const decision = selectCodexPetBubbleLayout({
      workArea,
      anchor,
      bubble,
      size: "large",
    });
    expect(decision.layout).toBe("hidden");
    // large: 144 + 24 wide, 16 + 156 + 48 tall
    expect(decision.bounds.width).toBe(168);
    expect(decision.bounds.height).toBe(220);
  });
});

function enabledSnapshot(overrides: Partial<CodexPetsSnapshot> = {}): CodexPetsSnapshot {
  return {
    home: "/tmp/pets",
    enabled: true,
    selected_id: "clawd",
    errors: [],
    pets: [
      {
        id: "clawd",
        display_name: "Clawd",
        description: "",
        manifest_path: "/tmp/pets/clawd/pet.json",
        spritesheet_path: "/tmp/pets/clawd/spritesheet.webp",
        spritesheet_url: "wuu-file://local/clawd",
      },
    ],
    ...overrides,
  };
}

function petManager(options: Partial<CodexPetWindowOptions> = {}): CodexPetWindowManager {
  return new CodexPetWindowManager({
    onClose: () => undefined,
    onJump: () => undefined,
    onSubmit: async () => ({ ok: true }),
    onShowApp: () => undefined,
    ...options,
  });
}

function loadedPetManager(options: Partial<CodexPetWindowOptions> = {}): CodexPetWindowManager {
  const manager = petManager(options);
  manager.sync(enabledSnapshot());
  codexPetElectronMocks.capturedListeners["wc:did-finish-load"]?.[0]?.({});
  codexPetElectronMocks.setBounds.mockClear();
  codexPetElectronMocks.executeJavaScript.mockClear();
  return manager;
}

function navigate(url: string): void {
  codexPetElectronMocks.capturedListeners["wc:will-navigate"]?.[0]?.({ preventDefault: vi.fn() }, url);
}

function scripts(): string[] {
  return codexPetElectronMocks.executeJavaScript.mock.calls.map((call) => call[0] as string);
}

describe("CodexPetWindowManager", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    codexPetElectronMocks.isDestroyed.mockReturnValue(false);
    codexPetElectronMocks.isVisible.mockReturnValue(false);
    codexPetElectronMocks.getBounds.mockImplementation(() => ({
      x: 100,
      y: 200,
      width: 120,
      height: 168,
    }));
    codexPetElectronMocks.setBounds.mockImplementation(() => undefined);
    for (const key of Object.keys(codexPetElectronMocks.capturedListeners)) {
      delete codexPetElectronMocks.capturedListeners[key];
    }
  });

  it("surfaces the pet window after did-finish-load when ready-to-show never fires", () => {
    const manager = petManager();
    manager.sync(enabledSnapshot());

    // sync creates a BrowserWindow and queues the data: URL load.
    expect(codexPetElectronMocks.BrowserWindow).toHaveBeenCalledTimes(1);
    expect(codexPetElectronMocks.loadURL).toHaveBeenCalledTimes(1);
    // Window starts hidden and is not shown until the load event surfaces it.
    expect(codexPetElectronMocks.showInactive).not.toHaveBeenCalled();

    // Simulate the regression: data: URL ready-to-show never fires, but
    // did-finish-load does. Without the fallback, the window would stay
    // at show: false forever (which is what the user saw after the
    // codex pet migration).
    const didFinishLoad =
      codexPetElectronMocks.capturedListeners["wc:did-finish-load"]?.[0];
    expect(didFinishLoad).toBeDefined();
    didFinishLoad!({});

    // The did-finish-load fallback must surface the window even though
    // ready-to-show never fired.
    expect(codexPetElectronMocks.showInactive).toHaveBeenCalledTimes(1);
  });

  it("destroys the window when the snapshot no longer has an enabled pet", () => {
    const manager = petManager();
    manager.sync(enabledSnapshot());
    expect(codexPetElectronMocks.BrowserWindow).toHaveBeenCalledTimes(1);

    // Disabling the pet (e.g. user toggled it off in Settings) tears
    // the window down so it stops eating input on the main window.
    manager.sync({ ...enabledSnapshot(), enabled: false, pets: [] });
    expect(codexPetElectronMocks.close).toHaveBeenCalledTimes(1);
  });

  it("resizes the window when a hint is set and triggers a jump via the jump callback", () => {
    const closeRequested = vi.fn();
    const jumpRequested = vi.fn();
    const manager = petManager({ onClose: closeRequested, onJump: jumpRequested });
    manager.sync(enabledSnapshot());
    const didFinishLoad =
      codexPetElectronMocks.capturedListeners["wc:did-finish-load"]?.[0];
    didFinishLoad!({});
    codexPetElectronMocks.setBounds.mockClear();
    codexPetElectronMocks.executeJavaScript.mockClear();

    manager.setHints([sampleHint]);

    // One card is 280×53 (52 plus 1px slack), so the window is
    // 8 + 53 + 8 (gap) + 144 (sprite and dock) + 8 = 221 tall and
    // 280 + 2 × 8 = 296 wide.
    expect(codexPetElectronMocks.setBounds).toHaveBeenCalledWith(
      expect.objectContaining({ width: 296, height: 221 }),
    );
    // wuuPetView is invoked with the new view carrying the hint.
    const lastCall = codexPetElectronMocks.executeJavaScript.mock.calls.at(-1)?.[0] as string;
    expect(lastCall).toContain("wuuPetView");
    expect(lastCall).toContain("thread-42");

    // Simulate a row click: the renderer navigates to wuu-pet://action/jump
    const willNavigate = codexPetElectronMocks.capturedListeners["wc:will-navigate"]?.[0];
    willNavigate!({ preventDefault: vi.fn() }, "wuu-pet://action/jump?thread_id=thread-42");
    expect(jumpRequested).toHaveBeenCalledWith("thread-42");

    // A thread the bubble does not show is never opened from the page.
    willNavigate!({ preventDefault: vi.fn() }, "wuu-pet://action/jump?thread_id=elsewhere");
    expect(jumpRequested).toHaveBeenCalledTimes(1);
  });

  it("grows the card stack by one card per extra hint, capped at three", () => {
    const manager = petManager();
    manager.sync(enabledSnapshot());
    const didFinishLoad =
      codexPetElectronMocks.capturedListeners["wc:did-finish-load"]?.[0];
    didFinishLoad!({});
    codexPetElectronMocks.setBounds.mockClear();

    const second: CodexPetHint = { ...sampleHint, thread_id: "thread-43", title: "B" };
    const third: CodexPetHint = { ...sampleHint, thread_id: "thread-44", title: "C" };
    const fourth: CodexPetHint = { ...sampleHint, thread_id: "thread-45", title: "D" };

    // Three cards: 3 × 52 + 2 × 6 + 1 = 169 → window 8+169+8+144+8 = 337.
    manager.setHints([sampleHint, second, third]);
    expect(codexPetElectronMocks.setBounds).toHaveBeenCalledWith(
      expect.objectContaining({ width: 296, height: 337 }),
    );

    // A fourth hint is dropped at the trust boundary: same 3-card bounds
    // and the pushed view carries only the first three thread ids.
    codexPetElectronMocks.setBounds.mockClear();
    codexPetElectronMocks.executeJavaScript.mockClear();
    manager.setHints([sampleHint, second, third, fourth]);
    const lastCall = codexPetElectronMocks.executeJavaScript.mock.calls.at(-1)?.[0] as string | undefined;
    if (lastCall) {
      expect(lastCall).not.toContain("thread-45");
    }
    for (const call of codexPetElectronMocks.setBounds.mock.calls) {
      expect((call[0] as { height: number }).height).toBe(337);
    }
  });

  it("drops rows without commentary at the trust boundary", () => {
    const manager = petManager();
    manager.sync(enabledSnapshot());
    const didFinishLoad =
      codexPetElectronMocks.capturedListeners["wc:did-finish-load"]?.[0];
    didFinishLoad!({});
    codexPetElectronMocks.setBounds.mockClear();

    const empty: CodexPetHint = { ...sampleHint, thread_id: "thread-46", preview: "  " };
    manager.setHints([sampleHint, empty]);
    // Only one usable hint → a single card (window 221).
    expect(codexPetElectronMocks.setBounds).toHaveBeenCalledWith(
      expect.objectContaining({ width: 296, height: 221 }),
    );
  });

  // The glance is for conversations with something to report. Idle ones
  // stay reachable as reply targets in the open panel, and a hidden card
  // stays hidden only until its conversation reports something new.
  it("keeps idle and dismissed conversations out of the glance", () => {
    const manager = loadedPetManager();
    const idle: CodexPetHint = { ...sampleHint, thread_id: "thread-idle", status: "idle" };
    const viewHints = () => {
      const view = scripts().filter((script) => script.includes("wuuPetView")).at(-1) ?? "";
      return [...view.matchAll(/"thread_id":"([^"]+)"/g)].map((match) => match[1]);
    };

    manager.setHints([sampleHint, idle]);
    expect(viewHints()).toEqual(["thread-42"]);

    navigate("wuu-pet://action/dismiss?thread_id=thread-42");
    expect(viewHints()).toEqual([]);
    expect(scripts().at(-1)).toContain('"layout":"hidden"');
    manager.setHints([sampleHint, idle]);
    expect(viewHints()).toEqual([]);

    navigate("wuu-pet://action/panel?open=1");
    expect(viewHints()).toEqual(["thread-42", "thread-idle"]);
    navigate("wuu-pet://action/panel?open=0");
    expect(viewHints()).toEqual([]);

    manager.setHints([{ ...sampleHint, status: "done" }, idle]);
    expect(viewHints()).toEqual(["thread-42"]);
  });

  it("opens the main window from the toolbar", () => {
    const onShowApp = vi.fn();
    loadedPetManager({ onShowApp });
    navigate("wuu-pet://action/show");
    expect(onShowApp).toHaveBeenCalledTimes(1);
  });

  it("resizes the pet window and notifies the host when setSize() picks a non-default size", () => {
    const onSizeChange = vi.fn();
    const manager = petManager({ onSizeChange });
    manager.sync(enabledSnapshot());
    const didFinishLoad =
      codexPetElectronMocks.capturedListeners["wc:did-finish-load"]?.[0];
    didFinishLoad!({});
    codexPetElectronMocks.setBounds.mockClear();
    codexPetElectronMocks.executeJavaScript.mockClear();

    manager.setSize("large");

    // The window grew to the "large" footprint: 168 × 220.
    expect(codexPetElectronMocks.setBounds).toHaveBeenCalledWith(
      expect.objectContaining({ width: 168, height: 220 }),
    );
    // The in-page sprite geometry also changed (spriteWidth/spriteHeight/scale
    // travel inside the wuuPetView payload; the in-page closure applies them
    // to the CSS custom properties once received). The earlier revision of
    // this test asserted on the executeJavaScript call string, which only
    // embeds the JSON payload now, so check the payload directly.
    const lastCall = codexPetElectronMocks.executeJavaScript.mock.calls.at(
      -1,
    )?.[0] as string;
    expect(lastCall).toContain("wuuPetView");
    expect(lastCall).toContain('"spriteWidth":144');
    expect(lastCall).toContain('"spriteHeight":156');
    expect(lastCall).toContain('"scale":0.75');
    expect(lastCall).toContain('"size":"large"');
    // The host was told so it can persist the choice to desktop-settings.json.
    expect(onSizeChange).toHaveBeenCalledWith("large");
  });

  it.each([
    ["default", true],
    ["large", true],
    ["large", false],
  ] as const)("restores %s geometry (enabled=%s) without committing a user change", (size, enabled) => {
    const onSizeChange = vi.fn();
    const onScaleChange = vi.fn();
    const manager = petManager({ onSizeChange, onScaleChange });

    manager.setSize(size, false);
    manager.setScale(0.63);
    expect(onSizeChange).not.toHaveBeenCalled();
    expect(onScaleChange).not.toHaveBeenCalled();
    expect(codexPetElectronMocks.BrowserWindow).not.toHaveBeenCalled();

    manager.sync(enabledSnapshot({ enabled }));
    if (!enabled) {
      expect(codexPetElectronMocks.BrowserWindow).not.toHaveBeenCalled();
      manager.sync(enabledSnapshot());
    }
    expect(codexPetElectronMocks.BrowserWindow).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ width: 145, height: 195 }),
    );

    // Restoring geometry must not disable subsequent user persistence.
    manager.setScale(0.7, true);
    expect(onScaleChange).toHaveBeenCalledExactlyOnceWith(0.7);
    manager.setSize("small");
    expect(onSizeChange).toHaveBeenCalledExactlyOnceWith("small");
  });

  it("live-resizes on setScale() without notifying the host until commit", () => {
    const onSizeChange = vi.fn();
    const onScaleChange = vi.fn();
    const manager = petManager({ onSizeChange, onScaleChange });
    manager.sync(enabledSnapshot());
    const didFinishLoad =
      codexPetElectronMocks.capturedListeners["wc:did-finish-load"]?.[0];
    didFinishLoad!({});
    codexPetElectronMocks.setBounds.mockClear();
    codexPetElectronMocks.executeJavaScript.mockClear();

    // Mid-drag frame: geometry updates live, no persistence traffic.
    manager.setScale(0.75);
    // scale 0.75 → multiplier 1.5 → sprite 144×156 → window 168×220.
    expect(codexPetElectronMocks.setBounds).toHaveBeenCalledWith(
      expect.objectContaining({ width: 168, height: 220 }),
    );
    const midDragCall = codexPetElectronMocks.executeJavaScript.mock.calls.at(
      -1,
    )?.[0] as string;
    expect(midDragCall).toContain('"scale":0.75');
    expect(onScaleChange).not.toHaveBeenCalled();
    expect(onSizeChange).not.toHaveBeenCalled();

    // Pointerup arrives with commit=true → the host persists the value.
    manager.setScale(0.8, true);
    expect(onScaleChange).toHaveBeenCalledWith(0.8);
  });

  it("clamps out-of-range scales from the startup rehydration path", () => {
    const onScaleChange = vi.fn();
    const manager = petManager({ onScaleChange });
    // setScale runs before the window exists on startup: it must record
    // the (clamped) scale so createWindow sizes the first BrowserWindow
    // from it instead of the preset.
    manager.setScale(99, true);
    expect(onScaleChange).toHaveBeenCalledWith(1.5);
    manager.sync(enabledSnapshot());
    const options = codexPetElectronMocks.BrowserWindow.mock.calls.at(-1)?.[0] as {
      width: number;
      height: number;
    };
    // scale 1.5 → multiplier 3 → sprite 288×312 → window 312×376.
    expect(options.width).toBe(312);
    expect(options.height).toBe(376);
  });

  it("routes a scale navigation from the pet page through setScale", () => {
    const onScaleChange = vi.fn();
    const manager = petManager({ onScaleChange });
    manager.sync(enabledSnapshot());
    const didFinishLoad =
      codexPetElectronMocks.capturedListeners["wc:did-finish-load"]?.[0];
    didFinishLoad!({});
    codexPetElectronMocks.setBounds.mockClear();

    const willNavigate =
      codexPetElectronMocks.capturedListeners["wc:will-navigate"]?.[0];
    willNavigate!({ preventDefault: vi.fn() }, "wuu-pet://action/scale?value=0.75");
    expect(codexPetElectronMocks.setBounds).toHaveBeenCalledWith(
      expect.objectContaining({ width: 168, height: 220 }),
    );
    expect(onScaleChange).not.toHaveBeenCalled();

    willNavigate!(
      { preventDefault: vi.fn() },
      "wuu-pet://action/scale?value=0.75&commit=1",
    );
    expect(onScaleChange).toHaveBeenCalledWith(0.75);
  });

  it("is a no-op when setSize() matches the current size", () => {
    const onSizeChange = vi.fn();
    const manager = petManager({ onSizeChange });
    manager.sync(enabledSnapshot());
    const didFinishLoad =
      codexPetElectronMocks.capturedListeners["wc:did-finish-load"]?.[0];
    didFinishLoad!({});
    codexPetElectronMocks.setBounds.mockClear();
    codexPetElectronMocks.executeJavaScript.mockClear();

    manager.setSize("default");

    expect(onSizeChange).not.toHaveBeenCalled();
    expect(codexPetElectronMocks.setBounds).not.toHaveBeenCalled();
    expect(codexPetElectronMocks.executeJavaScript).not.toHaveBeenCalled();
  });

  // Failure cases for the pet's input path: an empty, oversized, rejected,
  // or unplaced submit must reach the page as a failure (so the text stays
  // in the composer), and never reach the host as a message. A new
  // conversation must go to the workspace captured when the panel opened,
  // not one the main window switched to while the user typed.
  it("reports invalid or rejected submits as failures without sending them", async () => {
    const onSubmit = vi.fn<CodexPetWindowOptions["onSubmit"]>().mockRejectedValue(new Error("gone"));
    loadedPetManager({ onSubmit });
    navigate("wuu-pet://action/panel?open=1");
    codexPetElectronMocks.executeJavaScript.mockClear();

    navigate("wuu-pet://action/submit?text=%20%20&thread_id=thread-1");
    navigate(`wuu-pet://action/submit?text=${"x".repeat(CODEX_PET_SUBMIT_MAX + 1)}&thread_id=thread-1`);
    // No workspace was known when the panel opened.
    navigate("wuu-pet://action/submit?text=hello");
    navigate("wuu-pet://action/submit?text=hello&thread_id=thread-1");
    await vi.waitFor(() => {
      expect(scripts().filter((script) => script.includes("wuuPetSubmitResult"))).toHaveLength(4);
    });

    expect(onSubmit).toHaveBeenCalledTimes(1);
    for (const script of scripts()) {
      expect(script).toBe('window.wuuPetSubmitResult?.({"ok":false})');
    }
  });

  it("forwards a trimmed reply with its target thread and reports success", async () => {
    const onSubmit = vi.fn<CodexPetWindowOptions["onSubmit"]>().mockResolvedValue({ ok: true });
    loadedPetManager({ onSubmit });

    navigate(`wuu-pet://action/submit?text=${encodeURIComponent("  跑一下测试 \n")}&thread_id=thread-42`);
    await vi.waitFor(() => {
      expect(scripts()).toContain('window.wuuPetSubmitResult?.({"ok":true})');
    });
    expect(onSubmit).toHaveBeenCalledWith("跑一下测试", { thread_id: "thread-42" });
  });

  it("starts a new conversation in the workspace captured when the panel opened", async () => {
    const onSubmit = vi.fn<CodexPetWindowOptions["onSubmit"]>().mockResolvedValue({ ok: true });
    const manager = loadedPetManager({ onSubmit });
    const app = { kind: "project", project_id: "p-app", cwd: "/work/app" } as const;
    const docs = { kind: "project", project_id: "p-docs", cwd: "/work/docs" } as const;
    manager.setWorkspace({ context: app, name: "app" });

    navigate("wuu-pet://action/panel?open=1");
    expect(scripts().at(-1)).toContain('"workspaceName":"app"');
    manager.setWorkspace({ context: docs, name: "docs" });
    expect(scripts().at(-1)).toContain('"workspaceName":"app"');

    navigate("wuu-pet://action/submit?text=hello");
    await vi.waitFor(() => expect(onSubmit).toHaveBeenCalledWith("hello", { workspace: app }));

    navigate("wuu-pet://action/panel?open=0");
    navigate("wuu-pet://action/panel?open=1");
    expect(scripts().at(-1)).toContain('"workspaceName":"docs"');
  });

  it("opens the panel with room for the composer and gives it keyboard focus", () => {
    loadedPetManager();

    navigate("wuu-pet://action/panel?open=1");
    expect(codexPetElectronMocks.focus).toHaveBeenCalledTimes(1);
    // The dock widens to the composer in place: same height, so the sprite
    // does not move.
    expect(codexPetElectronMocks.setBounds).toHaveBeenLastCalledWith(
      expect.objectContaining({ width: 304, height: 168 }),
    );
    expect(scripts().at(-1)).toContain('"expanded":true');

    navigate("wuu-pet://action/panel?open=0");
    expect(codexPetElectronMocks.focus).toHaveBeenCalledTimes(1);
    expect(scripts().at(-1)).toContain('"expanded":false');
  });

  it("drops a reaction that arrives before the page loads", () => {
    const manager = petManager();
    manager.sync(enabledSnapshot());
    manager.react("review");
    codexPetElectronMocks.capturedListeners["wc:did-finish-load"]?.[0]?.({});
    expect(scripts().some((script) => script.includes("wuuPetReact"))).toBe(false);

    manager.react("failed");
    expect(scripts().at(-1)).toMatch(/^window\.wuuPetReact\?\.\("failed", \d+\)$/);
  });

  describe("cursor following", () => {
    // The sprite's center sits at (160, 268) for the mocked bounds.
    beforeEach(() => {
      vi.useFakeTimers();
      codexPetElectronMocks.cursor.x = 400;
      codexPetElectronMocks.cursor.y = 268;
    });
    afterEach(() => vi.useRealTimers());

    function lookScripts(): string[] {
      return scripts().filter((script) => script.includes("wuuPetLook"));
    }

    it("does not poll the cursor for sheets without look rows", () => {
      loadedPetManager();
      vi.advanceTimersByTime(1000);
      expect(lookScripts()).toEqual([]);
    });

    it("looks toward the cursor and pushes only direction changes", () => {
      loadedPetManager();
      navigate("wuu-pet://action/look?capable=1");
      vi.advanceTimersByTime(500);
      // Straight right of the sprite is a quarter turn clockwise from up.
      expect(lookScripts()).toEqual(["window.wuuPetLook?.(4)"]);

      codexPetElectronMocks.cursor.y = 268 - 240;
      codexPetElectronMocks.cursor.x = 160 + 240;
      vi.advanceTimersByTime(500);
      expect(lookScripts()).toEqual(["window.wuuPetLook?.(4)", "window.wuuPetLook?.(2)"]);
    });

    it("stops looking once the cursor rests", () => {
      loadedPetManager();
      navigate("wuu-pet://action/look?capable=1");
      vi.advanceTimersByTime(500);
      vi.advanceTimersByTime(2500);
      expect(lookScripts().at(-1)).toBe("window.wuuPetLook?.(null)");
    });

    it("hands the sprite back while the pet works or the panel is open", () => {
      const manager = loadedPetManager();
      navigate("wuu-pet://action/look?capable=1");
      vi.advanceTimersByTime(500);
      manager.setMood("running");
      expect(lookScripts().at(-1)).toBe("window.wuuPetLook?.(null)");
      vi.advanceTimersByTime(1000);
      expect(lookScripts()).toHaveLength(2);

      manager.setMood("idle");
      vi.advanceTimersByTime(500);
      expect(lookScripts().at(-1)).toBe("window.wuuPetLook?.(4)");
      navigate("wuu-pet://action/panel?open=1");
      expect(lookScripts().at(-1)).toBe("window.wuuPetLook?.(null)");
    });
  });
});
