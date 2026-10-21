import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WebWorkspacePanel } from "./WebWorkspace";
import type { WebWorkspaceApi, WebWorkspaceState } from "../shared/webWorkspace";

vi.mock("./BrowserLibrary", () => ({ BrowserLibrary: () => null }));
vi.mock("./WebWorkspaceDownloadsMenu", () => ({ WebWorkspaceDownloadsMenu: () => null }));
vi.mock("./i18n", () => ({ useI18n: () => ({ t: (key: string) => key }) }));

const state: WebWorkspaceState = {
  id: "site", activeTabId: "tab", url: "https://example.com", title: "Example",
  loading: false, canGoBack: false, canGoForward: false,
};
let container: HTMLDivElement;
let root: Root;
let focusFind: (id: string) => void;
let api: WebWorkspaceApi;
let previousAPI: typeof window.wuu;
let previousAnimations: typeof document.getAnimations;

beforeEach(async () => {
  const result = async () => state;
  previousAPI = window.wuu;
  previousAnimations = document.getAnimations;
  document.getAnimations = vi.fn(() => []);
  api = {
    open: vi.fn(result), command: vi.fn(result), find: vi.fn(result), stopFind: vi.fn(result),
    reportBounds: vi.fn(async () => {}), setShortcutTarget: vi.fn(async () => {}),
    onState: () => () => {}, onFocusAddress: () => () => {},
    onFocusFind: (handler: Parameters<WebWorkspaceApi["onFocusFind"]>[0]) => { focusFind = handler; return () => {}; },
  } as unknown as WebWorkspaceApi;
  window.wuu = Object.assign({}, window.wuu, { webWorkspace: api });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ x: 0, y: 0, left: 0, top: 0, right: 800, bottom: 600, width: 800, height: 600, toJSON: () => ({}) });
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  await act(async () => root.render(<WebWorkspacePanel site={{ id: state.id, url: state.url }} />));
  await act(async () => focusFind(state.id));
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  window.wuu = previousAPI;
  document.getAnimations = previousAnimations;
  vi.restoreAllMocks();
});

function findInput(): HTMLInputElement {
  return container.querySelector(".web-workspace-findbar input")!;
}

async function enterQuery(): Promise<void> {
  await act(async () => {
    const input = findInput();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "候选");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  vi.mocked(api.find).mockClear();
}

describe("website find input composition", () => {
  it.each(["Escape", "Enter"])("leaves composing %s to the input method", async key => {
    await enterQuery();
    const input = findInput();
    await act(async () => input.dispatchEvent(new KeyboardEvent("keydown", { key, isComposing: true, bubbles: true, cancelable: true })));
    expect(findInput()).toBe(input);
    expect(api.find).not.toHaveBeenCalled();
    expect(api.stopFind).not.toHaveBeenCalled();
    expect(api.command).not.toHaveBeenCalled();
  });

  it("keeps composition ownership when the key event omits isComposing", async () => {
    await enterQuery();
    const input = findInput();
    await act(async () => input.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true })));
    await act(async () => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })));
    expect(api.find).not.toHaveBeenCalled();
    await act(async () => input.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true })));
    await act(async () => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })));
    expect(api.find).toHaveBeenCalledWith("site", "候选", { forward: true, findNext: true });
  });

  it("still closes on Escape outside composition", async () => {
    await act(async () => findInput().dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));
    expect(findInput()).toBeNull();
    expect(api.stopFind).toHaveBeenCalledWith("site");
    expect(api.command).toHaveBeenCalledWith("site", "focus");
  });
});
