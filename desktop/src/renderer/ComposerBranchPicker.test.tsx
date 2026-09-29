import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GitStatusResult } from "../shared/protocol";
import { ComposerBranchPicker } from "./ComposerBranchPicker";
import { translateCurrent as t } from "./i18n";
import { clearToasts, ToastViewport } from "./Toast";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let container: HTMLDivElement;
let root: Root;
beforeEach(() => {
  clearToasts();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(() => {
  act(() => root.unmount());
  container.remove();
  clearToasts();
  vi.useRealTimers();
});
// The core lists branches most recently committed first.
const gitStatus: GitStatusResult = {
  is_repo: true,
  branch: "feature/two",
  branches: ["feature/one", "feature/two", "main", "fix/older"],
  default_branch: "main",
  dirty_count: 3,
};
function render(props: Partial<Parameters<typeof ComposerBranchPicker>[0]> = {}) {
  function Harness() {
    const [open, setOpen] = useState(true);
    return <ComposerBranchPicker gitStatus={gitStatus}
      disabled={false} open={open} onToggle={() => setOpen(!open)} onSelect={vi.fn()} onCreate={vi.fn()} {...props} />;
  }
  act(() => root.render(<WuuUIRoot><Harness /><ToastViewport /></WuuUIRoot>));
}
function search(value: string) {
  const element = document.querySelector<HTMLInputElement>('[role="menu"] input')!;
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
function pressEnter() {
  act(() => document.querySelector('[role="menu"] input')!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
}
function branchNames() {
  return [...document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')].map((button) => button.title);
}
function branchButton(name: string) {
  return [...document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')].find((button) => button.title === name)!;
}
function createButton() {
  return document.querySelector<HTMLButtonElement>('[role="menu"] button[role="menuitem"]');
}

describe("composer branch picker", () => {
  it("leads with the checked-out and default branches, then keeps the core's recency order", () => {
    render();
    expect(branchNames()).toEqual(["feature/two", "main", "feature/one", "fix/older"]);
    expect(branchButton("feature/two").getAttribute("aria-checked")).toBe("true");
    expect(branchButton("feature/two").querySelector(`[aria-label="${t("composer.branchDirtyFiles", { count: 3 })}"]`)).not.toBeNull();
    expect(branchButton("main").querySelector(`[aria-label="${t("composer.branchDirtyFiles", { count: 3 })}"]`)).toBeNull();
  });

  it("searches branches and prevents duplicate checkout while pending", async () => {
    let finish!: () => void;
    const onSelect = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
    render({ onSelect });
    expect(branchButton("feature/two").disabled).toBe(true);
    search("FEATURE/ONE");
    expect(branchNames()).toEqual(["feature/one"]);
    act(() => { branchButton("feature/one").click(); branchButton("feature/one").click(); });
    expect(onSelect).toHaveBeenCalledExactlyOnceWith("feature/one");
    expect(branchButton("feature/one").disabled).toBe(true);
    await act(async () => finish());
    expect(branchButton("feature/one").disabled).toBe(false);
  });

  it("offers the typed name as a new branch only when no branch has that name", () => {
    const onCreate = vi.fn();
    const onSelect = vi.fn();
    render({ onCreate, onSelect });
    expect(createButton()).toBeNull();
    search("main");
    expect(createButton()).toBeNull();
    pressEnter();
    expect(onSelect).toHaveBeenCalledExactlyOnceWith("main");
    search("feat");
    expect(createButton()).not.toBeNull();
    // Enter never turns a partial match into a new branch.
    pressEnter();
    expect(onCreate).not.toHaveBeenCalled();
  });

  it("creates from the search text, keeps it after a failure, and permits retry", async () => {
    vi.useFakeTimers();
    const onCreate = vi.fn().mockRejectedValueOnce(new Error("branch already exists")).mockResolvedValueOnce(undefined);
    render({ onCreate });
    search(" feature/new ");
    expect(branchNames()).toEqual([]);
    await act(async () => pressEnter());
    expect(onCreate).toHaveBeenCalledWith("feature/new");
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("branch already exists");
    expect(document.querySelector('[role="alert"]')?.closest('[data-wuu-layer="notice"]')).not.toBeNull();
    expect(document.querySelector<HTMLInputElement>('[role="menu"] input')?.value).toBe(" feature/new ");
    act(() => document.querySelector<HTMLButtonElement>(`button[aria-label="${t("common.closeNotice")}"]`)!.click());
    act(() => vi.advanceTimersByTime(200));
    await act(async () => createButton()!.click());
    expect(onCreate).toHaveBeenCalledTimes(2);
    expect(document.querySelector('[role="alert"]')).toBeNull();
  });

  it("only moves a worktree's start point and never offers a new branch", () => {
    const onSelect = vi.fn();
    render({ onSelect, onCreate: undefined, worktreeStart: { branch: "main" } });
    expect(branchButton("main").getAttribute("aria-checked")).toBe("true");
    expect(branchButton("feature/two").disabled).toBe(false);
    search("feature/new");
    expect(createButton()).toBeNull();
    search("");
    act(() => branchButton("feature/two").click());
    expect(onSelect).toHaveBeenCalledExactlyOnceWith("feature/two");
  });

  it("shows checkout conflicts through the shared notice and permits retry", async () => {
    const onSelect = vi.fn().mockRejectedValueOnce(new Error(t("git.checkoutBlockedByRunningThread"))).mockResolvedValueOnce(undefined);
    render({ onSelect });
    await act(async () => branchButton("feature/one").click());
    expect(document.querySelector('[role="alert"]')?.textContent).toContain(t("git.checkoutBlockedByRunningThread"));
    expect(document.querySelector('.environment-side-error')).toBeNull();
    expect(branchButton("feature/one").disabled).toBe(false);
    await act(async () => branchButton("feature/one").click());
    expect(onSelect).toHaveBeenCalledTimes(2);
  });

  it("blocks opening and mutations when explicitly disabled", () => {
    const onSelect = vi.fn();
    render({ disabled: true, onSelect });
    const trigger = container.querySelector<HTMLButtonElement>("button")!;
    expect(trigger.disabled).toBe(true);
    act(() => trigger.click());
    expect(document.querySelector('[role="menu"]')).toBeNull();
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("closes with Escape and returns focus to the branch trigger", () => {
    render();
    act(() => document.querySelector("input")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(document.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(container.querySelector("button"));
  });
});
