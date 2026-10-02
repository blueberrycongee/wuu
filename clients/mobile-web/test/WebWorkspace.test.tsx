// @vitest-environment jsdom
import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { confirmAction } from "../../../desktop/src/renderer/ConfirmDialog";
import WebWorkspace from "../src/WebWorkspace";

vi.mock("../../../desktop/src/renderer/App", () => ({ App: () => null }));

afterEach(() => vi.unstubAllGlobals());

it("answers shared destructive-action confirmations in the connected Web workspace", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("wuu", { initialThemePreference: "light" });
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  try {
    await act(async () => root.render(createElement(WebWorkspace)));
    for (const answer of [false, true]) {
      let pending!: Promise<boolean>;
      await act(async () => {
        pending = confirmAction({ title: "Remove draft?", message: "Discard the unsaved draft.", confirmLabel: "Remove", tone: "danger" });
      });
      const dialog = document.querySelector('[role="dialog"]');
      expect(dialog).not.toBeNull();
      const action = dialog!.querySelector<HTMLButtonElement>(`[data-confirm-action="${answer ? "confirm" : "cancel"}"]`);
      await act(async () => action!.click());
      await expect(pending).resolves.toBe(answer);
      expect(document.querySelector('[role="dialog"]')).toBeNull();
    }
  } finally {
    await act(async () => root.unmount());
    container.remove();
  }
});
