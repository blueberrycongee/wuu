import type { ChromeOverlayRequest, ChromeOverlayRendererApi } from "../shared/chromeOverlay";
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ApplicationDesktopControlsV1, ApplicationDesktopChromeOverlayApiV1, ApplicationDesktopChromeOverlayEventV1 } from "../../../packages/plugin-sdk/src/ui-desktop";
import { DesktopControlsProvider } from "./DesktopControlsContext";
import { useChromeOverlay } from "./ChromeOverlay";

const mountedRoots = vi.hoisted(() => [] as { unmount(): void }[]);
vi.mock("react-dom/client", async importOriginal => {
  const original = await importOriginal<typeof import("react-dom/client")>();
  return { ...original, createRoot: (...args: Parameters<typeof original.createRoot>) => {
    const root = original.createRoot(...args); mountedRoots.push(root); return root;
  } };
});
afterEach(() => { vi.restoreAllMocks(); mountedRoots.length = 0; });

describe("native overlay failure lifetime", () => {
  it("does not automatically reopen a persistent panel after its native renderer fails", async () => {
    let receive!: (event: ApplicationDesktopChromeOverlayEventV1) => void;
    const show = vi.fn<ApplicationDesktopChromeOverlayApiV1["show"]>(async () => {});
    const api: ApplicationDesktopChromeOverlayApiV1 = {
      show, hide: vi.fn(async () => {}),
      onEvent: listener => { receive = listener; return () => {}; },
    };
    const desktop: ApplicationDesktopControlsV1 = {
      hostKind: "desktop", popOut: { kind: null, threadID: null, context: null },
      capabilities: { account: false, pets: false, nativeControls: false, chromeOverlay: true, notifications: false },
      onVisibilityChange: () => () => {}, chromeOverlay: api,
    };
    vi.spyOn(window, "getComputedStyle").mockReturnValue({
      fontFamily: "sans-serif", *[Symbol.iterator]() {}, getPropertyValue: () => "",
    } as unknown as CSSStyleDeclaration);
    function PersistentPanel({ enabled, title }: { enabled: boolean; title: string }) {
      const [expanded, setExpanded] = useState(true);
      useChromeOverlay(enabled, { x: 10, y: 10, width: 200, height: 80 }, {
        kind: "capsule", expanded, menuOpen: false, draft: "Keep draft", busy: false,
        sourceTitle: title, label: "Ask", placeholder: "Question", sendLabel: "Send", openLabel: "Open", collapseLabel: "Close",
      }, () => {}, () => setExpanded(false), expanded);
      return null;
    }
    const element = document.createElement("div"); document.body.appendChild(element);
    const root = createRoot(element);
    const render = async (enabled: boolean, title = "Original page") => {
      await act(async () => root.render(<DesktopControlsProvider controls={desktop}><PersistentPanel enabled={enabled} title={title} /></DesktopControlsProvider>));
    };
    try {
      await render(true);
      const initial = show.mock.calls[0][0];
      await act(async () => receive({ token: initial.token, generation: initial.generation, command: { type: "dismiss", reason: "failed" } }));
      await render(true, "Later page metadata");
      expect(show).toHaveBeenCalledTimes(1);
      await render(false);
      await render(true);
      expect(show).toHaveBeenCalledTimes(2);
      expect(show.mock.calls[1][0].token).not.toBe(initial.token);
      expect(show.mock.calls[1][0].model).toMatchObject({ draft: "Keep draft" });
    } finally {
      await act(async () => root.unmount()); element.remove();
    }
  });
});

describe("native overlay first-paint geometry", () => {
  it("waits for the recovered viewport before acknowledging and focusing, and cancels a dismissed wait", async () => {
    const frames = new Map<number, FrameRequestCallback>(); let nextFrame = 0;
    vi.spyOn(window, "requestAnimationFrame").mockImplementation(callback => { frames.set(++nextFrame, callback); return nextFrame; });
    vi.spyOn(window, "cancelAnimationFrame").mockImplementation(id => { frames.delete(id); });
    vi.stubGlobal("innerWidth", 1); vi.stubGlobal("innerHeight", 1);
    const request: ChromeOverlayRequest = {
      version: 1, token: "recovered", generation: 1, focus: true,
      bounds: { x: 0, y: 0, width: 320, height: 91 },
      appearance: { theme: "dark", locale: "en-US", fontFamily: "sans-serif", variables: {} },
      model: { kind: "account", panel: { label: "Account", rows: [{ action: "providers", label: "Settings" }] } },
    };
    let receive!: (value: ChromeOverlayRequest | null) => void;
    const api: ChromeOverlayRendererApi = {
      ready: async () => request, onState: listener => { receive = listener; return () => {}; },
      painted: vi.fn(), command: vi.fn(),
    };
    Object.assign(window, { wuuChromeOverlay: api });
    const element = document.createElement("div"); element.id = "root"; document.body.appendChild(element);
    const frame = async () => { const pending = [...frames.values()]; frames.clear(); await act(async () => pending.forEach(callback => callback(0))); };
    try {
      await act(async () => { await import("./chromeOverlayMain"); });
      await frame();
      expect(api.painted).not.toHaveBeenCalled();
      expect(document.activeElement).not.toBe(element.querySelector("button"));
      vi.stubGlobal("innerWidth", 320); vi.stubGlobal("innerHeight", 91);
      await frame();
      expect(api.painted).toHaveBeenCalledWith("recovered", 1);
      expect(document.activeElement).toBe(element.querySelector("button"));
      vi.stubGlobal("innerWidth", 1);
      await act(async () => receive({ ...request, generation: 2 }));
      await frame();
      await act(async () => receive(null));
      vi.stubGlobal("innerWidth", 320);
      await frame();
      expect(api.painted).toHaveBeenCalledTimes(1);
    } finally {
      await act(async () => mountedRoots.at(-1)?.unmount()); element.remove(); vi.unstubAllGlobals();
      delete (window as unknown as { wuuChromeOverlay?: ChromeOverlayRendererApi }).wuuChromeOverlay;
    }
  });
});
