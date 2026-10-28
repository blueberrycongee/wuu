import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ApplicationDesktopControlsV1, ApplicationDesktopChromeOverlayApiV1, ApplicationDesktopChromeOverlayEventV1 } from "../../../packages/plugin-sdk/src/ui-desktop";
import { DesktopControlsProvider } from "./DesktopControlsContext";
import { useChromeOverlay } from "./ChromeOverlay";

afterEach(() => vi.restoreAllMocks());

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
