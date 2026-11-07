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
const gear = () => container.querySelector<HTMLButtonElement>(".sidebar-settings-trigger")!;
const menuTrigger = () => container.querySelector<HTMLButtonElement>(".sidebar-account-menu-trigger")!;
async function openMenu() {
  await act(async () => menuTrigger().click());
  await act(async () => new Promise<void>(resolve => requestAnimationFrame(() => resolve())));
}
function item(key: Parameters<typeof translateCurrent>[0]) {
  const result = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')).find(button => button.textContent === translateCurrent(key));
  if (!result) throw new Error(`Missing menu item ${key}`);
  return result;
}

describe("SidebarAccountMenu", () => {
  it("opens Settings directly from the gear when no account menu applies", async () => {
    const navigate = await mount();
    expect(container.querySelector(".sidebar-account-menu-trigger")).toBeNull();
    await act(async () => gear().click());
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(navigate.mock.calls[0]).toHaveLength(0);
    expect(document.querySelector(".sidebar-account-menu")).toBeNull();
  });
  it("keeps account actions in the menu and restores keyboard focus", async () => {
    const driver = vi.fn(async () => ({}));
    const navigate = await mount(driver);
    await act(async () => gear().click());
    expect(navigate).toHaveBeenCalledTimes(1);
    await openMenu();
    expect(document.activeElement).toBe(item("settings.usage"));
    act(() => document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
    expect(document.activeElement).toBe(item("account.linkDevices"));
    act(() => document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(document.querySelector('[role="menu"]')).toBeNull(); expect(document.activeElement).toBe(menuTrigger());
    for (const shiftKey of [false, true]) {
      await openMenu();
      const tab = new KeyboardEvent("keydown", { key: "Tab", shiftKey, bubbles: true, cancelable: true });
      act(() => document.activeElement?.dispatchEvent(tab));
      expect(document.querySelector('[role="menu"]')).toBeNull();
      expect(document.activeElement).toBe(menuTrigger());
      expect(tab.defaultPrevented).toBe(false);
    }
    await openMenu(); act(() => item("settings.usage").click()); expect(navigate).toHaveBeenLastCalledWith("usage");
  });
  it("refreshes identity after account changes and links to account management", async () => {
    let username: string | undefined;
    const driver = vi.fn(async () => ({ username, server: "https://account.example" }));
    const onOpenAccount = vi.fn();
    const navigate = await mount(driver, onOpenAccount); await openMenu();
    await act(async () => item("account.linkDevices").click()); expect(onOpenAccount).toHaveBeenCalledTimes(1); expect(navigate).not.toHaveBeenCalled();
    username = "andywu";
    await act(async () => window.dispatchEvent(new Event("wuu:account-changed")));
    expect(menuTrigger().textContent).toContain("andywu"); await openMenu();
    await act(async () => item("account.manage").click()); expect(onOpenAccount).toHaveBeenCalledTimes(2);
  });
  it("keeps logout failures visible and reports a local-only logout", async () => {
    let signedIn = true; let fail = true;
    const driver: AccountDriver = async action => {
      if (action === "logout") { if (fail) throw new Error("Service unavailable"); signedIn = false; return { localLogoutOnly: true }; }
      return signedIn ? { username: "andywu" } : {};
    };
    await mount(driver); await openMenu();
    await act(async () => item("account.logout").click());
    expect(document.querySelector('[role="alert"]')?.textContent).toBe("Service unavailable");
    fail = false; await act(async () => item("account.logout").click());
    expect(document.querySelector('[role="status"]')?.textContent).toBe(translateCurrent("account.localLogout"));
    expect(menuTrigger().textContent).not.toContain("andywu");
  });
});
