import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DesktopControlsProvider } from "./DesktopControlsContext";
import type { ApplicationDesktopControlsV1 } from "../../../packages/plugin-sdk/src/ui-desktop";
import { SidebarAccountMenu } from "./SidebarAccountMenu";
import type { AccountDriver } from "./AccountPanel";
import { translateCurrent } from "./i18n";

let root: Root;
let container: HTMLDivElement;
const original = window.wuu;
afterEach(() => { act(() => root?.unmount()); container?.remove(); window.wuu = original; });
async function mount(driver?: AccountDriver, onOpenAccount = vi.fn()) {
  window.wuu = {} as typeof window.wuu;
  const desktop: ApplicationDesktopControlsV1 = { hostKind: "desktop", popOut: { kind: null, threadID: null, context: null },
    capabilities: { account: Boolean(driver), pets: false, nativeControls: false, chromeOverlay: false, notifications: false },
    onVisibilityChange: () => () => {}, account: driver };
  container = document.createElement("div"); document.body.append(container);
  root = createRoot(container);
  const navigate = vi.fn();
  await act(async () => root.render(<DesktopControlsProvider controls={desktop}><SidebarAccountMenu disabled={false} onOpenSettings={navigate} onOpenAccount={onOpenAccount} /></DesktopControlsProvider>));
  return navigate;
}
const trigger = () => container.querySelector("button")!;
async function open() {
  await act(async () => trigger().click());
  await act(async () => new Promise<void>(resolve => requestAnimationFrame(() => resolve())));
}
function item(key: Parameters<typeof translateCurrent>[0]) {
  const result = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')).find(button => button.textContent === translateCurrent(key));
  if (!result) throw new Error(`Missing menu item ${key}`);
  return result;
}

describe("SidebarAccountMenu", () => {
  it("opens settings and usage without requiring account support, and restores keyboard focus", async () => {
    const navigate = await mount(); await open();
    expect(document.activeElement).toBe(item("settings.usage"));
    act(() => document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
    expect(document.activeElement).toBe(item("sidebar.settings"));
    act(() => document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(document.querySelector('[role="menu"]')).toBeNull(); expect(document.activeElement).toBe(trigger());
    for (const shiftKey of [false, true]) {
      await open();
      const tab = new KeyboardEvent("keydown", { key: "Tab", shiftKey, bubbles: true, cancelable: true });
      act(() => document.activeElement?.dispatchEvent(tab));
      expect(document.querySelector('[role="menu"]')).toBeNull();
      expect(document.activeElement).toBe(trigger());
      expect(tab.defaultPrevented).toBe(false);
    }
    await open(); act(() => item("sidebar.settings").click()); expect(navigate).toHaveBeenLastCalledWith("providers");
    await open(); act(() => item("settings.usage").click()); expect(navigate).toHaveBeenLastCalledWith("usage");
  });
  it("refreshes identity after account changes and links to account management", async () => {
    let username: string | undefined;
    const driver = vi.fn(async () => ({ username, server: "https://account.example" }));
    const onOpenAccount = vi.fn();
    const navigate = await mount(driver, onOpenAccount); await open();
    await act(async () => item("account.linkDevices").click()); expect(onOpenAccount).toHaveBeenCalledTimes(1); expect(navigate).not.toHaveBeenCalled();
    username = "andywu";
    await act(async () => window.dispatchEvent(new Event("wuu:account-changed")));
    expect(trigger().textContent).toContain("andywu"); await open();
    await act(async () => item("account.manage").click()); expect(onOpenAccount).toHaveBeenCalledTimes(2); expect(navigate).not.toHaveBeenCalled();
  });
  it("keeps logout failures visible and reports a local-only logout", async () => {
    let signedIn = true; let fail = true;
    const driver: AccountDriver = async action => {
      if (action === "logout") { if (fail) throw new Error("Service unavailable"); signedIn = false; return { localLogoutOnly: true }; }
      return signedIn ? { username: "andywu" } : {};
    };
    await mount(driver); await open();
    await act(async () => item("account.logout").click());
    expect(document.querySelector('[role="alert"]')?.textContent).toBe("Service unavailable");
    fail = false; await act(async () => item("account.logout").click());
    expect(document.querySelector('[role="status"]')?.textContent).toBe(translateCurrent("account.localLogout"));
    expect(trigger().textContent).not.toContain("andywu");
  });
});
