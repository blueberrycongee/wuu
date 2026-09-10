import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AccountScreen } from "./AccountScreen";
import type { AccountDriver } from "./AccountPanel";
import { translateCurrent as t } from "./i18n";

let root: Root;
let container: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); container?.remove(); localStorage.clear(); });
async function mount(driver: AccountDriver) {
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  const back = vi.fn();
  await act(async () => root.render(<AccountScreen driver={driver} onBack={back} />));
  return back;
}
async function click(key: Parameters<typeof t>[0]) {
  const button = Array.from(container.querySelectorAll("button")).find(node => node.textContent?.startsWith(t(key)));
  if (!button) throw new Error(`Missing action ${key}`);
  await act(async () => button.click());
}
async function fill(selector: string, value: string) {
  const input = container.querySelector<HTMLInputElement>(selector)!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
async function credentials() {
  if (container.querySelector('input[type="url"]')) { await fill('input[type="url"]', "https://account.example"); await click("common.save"); }
  await fill('[autocomplete="username"]', "andywu");
  await fill('input[type="password"]', "long-test-password");
  await click("account.connectionSettings");
  await fill('input[type="url"]', "https://account.example");
  await click("common.save");
}

describe("AccountScreen", () => {
  it("returns to the workbench only after a successful login", async () => {
    let signedIn = false; let fail = true;
    const driver: AccountDriver = vi.fn(async action => {
      if (action === "login") { if (fail) throw new Error("Invalid credentials"); signedIn = true; }
      return signedIn ? { username: "andywu" } : {};
    });
    const back = await mount(driver); await credentials(); await click("account.login");
    expect(back).not.toHaveBeenCalled();
    expect(container.querySelector('[role="alert"]')?.textContent).toBe("Invalid credentials");
    fail = false; await click("account.login");
    expect(driver).toHaveBeenCalledWith("login", expect.objectContaining({ username: "andywu", server: "https://account.example" }));
    expect(back).toHaveBeenCalledTimes(1);
  });
  it("keeps a registered account's recovery code visible until it is saved", async () => {
    let signedIn = false;
    const back = await mount(async action => {
      if (action === "register") { signedIn = true; return { username: "andywu", recovery: "test-recovery-code" }; }
      return signedIn ? { username: "andywu" } : {};
    });
    await fill('input[type="url"]', "https://account.example"); await click("common.save");
    await click("account.moreOptions"); await click("account.register");
    await credentials(); await click("account.register");
    expect(container.querySelector("code")?.textContent).toBe("test-recovery-code");
    expect(back).not.toHaveBeenCalled(); await click("account.savedRecovery");
    expect(back).toHaveBeenCalledTimes(1);
  });
  it("lets signed-in users manage devices without redirecting, and lets them return", async () => {
    const back = await mount(async () => ({ username: "andywu", devices: [] }));
    expect(container.querySelector("h2")?.textContent).toBe(t("account.manage"));
    expect(back).not.toHaveBeenCalled(); await click("settings.backToApp"); expect(back).toHaveBeenCalledTimes(1);
  });
});
