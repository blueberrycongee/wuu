import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ComposerBranchPicker } from "./ComposerBranchPicker";
import { translateCurrent as t } from "./i18n";

let container: HTMLDivElement;
let root: Root;
beforeEach(() => {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(() => {
  act(() => root.unmount());
  container.remove();
});
function render(props: Partial<Parameters<typeof ComposerBranchPicker>[0]> = {}) {
  function Harness() {
    const [open, setOpen] = useState(true);
    return <ComposerBranchPicker gitStatus={{ is_repo: true, branch: "main", branches: ["feature/one", "main", "feature/two"], dirty_count: 3 }}
      disabled={false} open={open} onToggle={() => setOpen(!open)} onSelect={vi.fn()} onCreate={vi.fn()} {...props} />;
  }
  act(() => root.render(<Harness />));
}
function input(label: string, value: string) {
  const element = document.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!;
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
function branchButton(name: string) {
  return [...document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')].find((button) => button.title === name)!;
}

describe("composer branch picker", () => {
  it("searches branches and prevents duplicate checkout while pending", async () => {
    let finish!: () => void;
    const onSelect = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
    render({ onSelect });
    expect(branchButton("main").getAttribute("aria-checked")).toBe("true");
    expect(branchButton("main").disabled).toBe(true);
    expect(branchButton("main").textContent).toContain(t("composer.branchDirtyFiles", { count: 3 }));
    input(t("environment.searchBranches"), "FEATURE/ONE");
    expect(document.querySelectorAll('[role="menuitemradio"]')).toHaveLength(1);
    act(() => { branchButton("feature/one").click(); branchButton("feature/one").click(); });
    expect(onSelect).toHaveBeenCalledExactlyOnceWith("feature/one");
    expect(branchButton("feature/one").disabled).toBe(true);
    await act(async () => finish());
    expect(branchButton("feature/one").disabled).toBe(false);
  });

  it("retains the new branch name after a failed creation and permits retry", async () => {
    const onCreate = vi.fn().mockRejectedValueOnce(new Error("branch already exists")).mockResolvedValueOnce(undefined);
    render({ onCreate });
    act(() => document.querySelector<HTMLButtonElement>('button[role="menuitem"]')!.click());
    input(t("environment.newBranchName"), " feature/new ");
    const submit = () => document.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await act(async () => { submit(); });
    expect(onCreate).toHaveBeenCalledWith("feature/new");
    expect(document.querySelector('[role="alert"]')?.textContent).toBe("branch already exists");
    expect(document.querySelector<HTMLInputElement>(`input[aria-label="${t("environment.newBranchName")}"]`)?.value).toBe(" feature/new ");
    await act(async () => { submit(); });
    expect(onCreate).toHaveBeenCalledTimes(2);
    expect(document.querySelector('[role="alert"]')).toBeNull();
  });

  it("blocks opening and mutations while the working tree is busy", () => {
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
