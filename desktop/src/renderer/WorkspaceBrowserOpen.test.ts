import { describe, expect, it } from "vitest";

import {
  prefersSystemBrowser,
  workspaceBrowserClickModifiers,
  workspaceBrowserFocusDecision,
  workspaceBrowserOpenIntent,
  workspaceBrowserOpenTarget,
} from "./WorkspaceBrowserOpen";

describe("workspace browser open policy", () => {
  it("opens http and https pages in the workspace browser", () => {
    expect(workspaceBrowserOpenTarget("https://docs.example.com/api")).toEqual({
      url: "https://docs.example.com/api",
      reuseKey: "https://docs.example.com/api",
    });
    expect(workspaceBrowserOpenTarget("http://app.local:3000/")).toEqual({
      url: "http://app.local:3000/",
      reuseKey: "http://app.local:3000",
    });
  });

  it("reuses the same tab identity after redirects-style URL noise", () => {
    const first = workspaceBrowserOpenTarget("https://Example.com/docs/");
    const second = workspaceBrowserOpenTarget("https://example.com/docs");
    expect(first?.reuseKey).toBe(second?.reuseKey);
  });

  it("keeps mailto and unparseable values out of the workspace browser", () => {
    expect(workspaceBrowserOpenTarget("mailto:hello@example.com")).toBeUndefined();
    expect(workspaceBrowserOpenTarget("javascript:alert(1)")).toBeUndefined();
    expect(workspaceBrowserOpenTarget("not a url")).toBeUndefined();
  });

  it("uses the system browser for modifier clicks", () => {
    expect(prefersSystemBrowser({ metaKey: true })).toBe(true);
    expect(prefersSystemBrowser({ ctrlKey: true })).toBe(true);
    expect(prefersSystemBrowser({ altKey: true })).toBe(true);
    expect(prefersSystemBrowser({ button: 1 })).toBe(true);
    expect(
      workspaceBrowserOpenIntent("https://example.com", { metaKey: true }),
    ).toEqual({
      target: {
        url: "https://example.com/",
        reuseKey: "https://example.com",
      },
      external: true,
    });
    expect(workspaceBrowserClickModifiers({ button: 0 })).toBeUndefined();
    expect(workspaceBrowserClickModifiers({ metaKey: true, button: 0 })).toEqual({
      metaKey: true,
      ctrlKey: false,
      altKey: false,
      button: 0,
    });
  });

  it("does not steal focus from another workspace tool or a foreground agent browser", () => {
    expect(
      workspaceBrowserFocusDecision({
        rightPanelOpen: true,
        activeTabID: "files",
        browserForegroundOccupied: false,
      }),
    ).toEqual({ navigate: true, stealFocus: false });
    expect(
      workspaceBrowserFocusDecision({
        rightPanelOpen: true,
        activeTabID: "browser",
        browserForegroundOccupied: true,
      }),
    ).toEqual({ navigate: true, stealFocus: false });
    expect(
      workspaceBrowserFocusDecision({
        rightPanelOpen: false,
        activeTabID: undefined,
        browserForegroundOccupied: false,
      }),
    ).toEqual({ navigate: true, stealFocus: true });
  });
});
