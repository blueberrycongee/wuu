import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AppearanceTypography } from "./AppearanceTypography";
import { readAppearance } from "./AppearancePreferences";
import { I18nProvider } from "./i18n";
import type { WuuDesktopApi } from "../shared/protocol";

let container: HTMLDivElement;
let root: Root;
beforeEach(() => {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  window.wuu = { initialLanguagePreference: "en-US", initialSystemLocale: "en-US" } as WuuDesktopApi;
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ measureText: () => ({ width: 64 }) } as unknown as CanvasRenderingContext2D);
});
afterEach(() => {
  act(() => root.unmount());
  container.remove();
  localStorage.clear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
async function mount() {
  await act(async () => root.render(<I18nProvider><AppearanceTypography section="text" /></I18nProvider>));
}
async function click(element: Element) {
  await act(async () => element.dispatchEvent(new MouseEvent("click", { bubbles: true })));
}

it("loads installed families on demand, deduplicates faces and persists a selected font", async () => {
  const query = vi.fn().mockResolvedValue([{ family: "Example Sans" }, { family: "Example Sans" }, { family: "Example Mono" }]);
  vi.stubGlobal("queryLocalFonts", query);
  await mount();
  expect(query).not.toHaveBeenCalled();
  await click(container.querySelector(".select-menu-trigger")!);
  const options = [...document.querySelectorAll(".select-menu-item")];
  const sans = options.filter((item) => item.textContent?.includes("Example Sans"));
  expect(sans).toHaveLength(1);
  await click(sans[0]);
  expect(readAppearance().uiFont).toBe("Example Sans");
});

it("preserves the selected font after access fails and retries when reopened", async () => {
  localStorage.setItem("wuu.appearance.v1", JSON.stringify({ uiFont: "Existing Font" }));
  const query = vi.fn().mockRejectedValueOnce(new Error("denied")).mockResolvedValue([{ family: "New Font" }]);
  vi.stubGlobal("queryLocalFonts", query);
  await mount();
  const trigger = container.querySelector(".select-menu-trigger")!;
  await click(trigger);
  expect(readAppearance().uiFont).toBe("Existing Font");
  await click(trigger);
  await click(trigger);
  expect([...document.querySelectorAll(".select-menu-item")].some((item) => item.textContent?.includes("New Font"))).toBe(true);
});
