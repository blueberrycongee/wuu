/**
 * Destructive actions run only after `confirmAction` resolves true. These
 * cases are the ways the dialog could fail its callers: resolving the wrong
 * way on dismissal, crossing two pending requests, and stranding focus.
 */
import { afterEach, describe, expect, it } from "vitest";
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ConfirmDialogHost, confirmAction } from "./ConfirmDialog";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function mountHost(): void {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(createElement(WuuUIRoot, null, createElement(ConfirmDialogHost)));
  });
}

function request(title = "Delete “Draft”?"): Parameters<typeof confirmAction>[0] {
  return { title, message: "This can't be undone.", confirmLabel: "Delete", tone: "danger" };
}

function dialog(): HTMLElement | null {
  return document.querySelector<HTMLElement>('[role="dialog"]');
}

function action(name: "confirm" | "cancel"): HTMLButtonElement {
  const button = dialog()?.querySelector<HTMLButtonElement>(`[data-confirm-action="${name}"]`);
  if (!button) throw new Error(`missing ${name} action`);
  return button;
}

// Wrapped so awaiting the render does not also await the answer.
async function open(title?: string): Promise<{ answer: Promise<boolean> }> {
  let answer!: Promise<boolean>;
  await act(async () => {
    answer = confirmAction(request(title));
  });
  return { answer };
}

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container?.remove();
  container = null;
  document.body.innerHTML = "";
});

describe("confirmAction", () => {
  it("resolves false when cancelled, escaped, or dismissed from the backdrop", async () => {
    mountHost();
    const dismissals: Array<() => void> = [
      () => action("cancel").click(),
      () => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })),
      () => document.querySelector<HTMLElement>(".modal-backdrop")!.click(),
    ];
    for (const dismiss of dismissals) {
      const { answer } = await open();
      expect(dialog()?.textContent).toContain("Delete “Draft”?");
      await act(async () => dismiss());
      await expect(answer).resolves.toBe(false);
      expect(dialog()).toBeNull();
    }
  });

  it("answers each request with its own dialog, in order", async () => {
    mountHost();
    const { answer: first } = await open("First?");
    const { answer: second } = await open("Second?");
    expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1);
    expect(dialog()?.textContent).toContain("First?");

    await act(async () => action("confirm").click());
    await expect(first).resolves.toBe(true);
    expect(dialog()?.textContent).toContain("Second?");

    await act(async () => action("cancel").click());
    await expect(second).resolves.toBe(false);
    expect(dialog()).toBeNull();
  });

  it("starts on the confirm action and returns focus to the control that asked", async () => {
    mountHost();
    const opener = document.createElement("button");
    document.body.appendChild(opener);
    opener.focus();

    const { answer } = await open();
    expect(document.activeElement).toBe(action("confirm"));

    await act(async () => action("cancel").click());
    await answer;
    expect(document.activeElement).toBe(opener);
  });

  it("cancels only the aborted request without disturbing queued confirmations", async () => {
    mountHost();
    const owner = new AbortController();
    let first!: Promise<boolean>;
    await act(async () => { first = confirmAction(request("Retired context?"), owner.signal); });
    const { answer: next } = await open("Current context?");
    await act(async () => owner.abort());
    await expect(first).resolves.toBe(false);
    expect(dialog()?.textContent).toContain("Current context?");
    await act(async () => action("confirm").click());
    await expect(next).resolves.toBe(true);
    expect(dialog()).toBeNull();
  });

  it("removes an aborted queued request and never opens an already-aborted request", async () => {
    mountHost();
    const { answer: first } = await open("Visible?");
    const owner = new AbortController();
    let queued!: Promise<boolean>;
    await act(async () => { queued = confirmAction(request("Queued?"), owner.signal); });
    await act(async () => owner.abort());
    await expect(queued).resolves.toBe(false);
    await expect(confirmAction(request("Already retired?"), owner.signal)).resolves.toBe(false);
    expect(dialog()?.textContent).toContain("Visible?");
    await act(async () => action("cancel").click());
    await expect(first).resolves.toBe(false);
    expect(dialog()).toBeNull();
  });

});

// Preparation may settle after cancellation, after a successor, or with an error.
it("keeps a prepared request disabled until ready without reviving a canceled request", async () => {
  mountHost();
  let finish!: (value: ReturnType<typeof request>) => void;
  const preparation = new Promise<ReturnType<typeof request>>(resolve => { finish = resolve; });
  let answer!: Promise<boolean>;
  await act(async () => { answer = confirmAction({ ...request("Loading"), prepare: () => preparation }); });
  expect(action("confirm").disabled).toBe(true);
  expect(document.activeElement).toBe(action("cancel"));
  await act(async () => action("cancel").click());
  await expect(answer).resolves.toBe(false);
  const { answer: successor } = await open("Successor");
  await act(async () => finish(request("Stale")));
  expect(dialog()?.textContent).toContain("Successor");
  await act(async () => action("cancel").click());
  await expect(successor).resolves.toBe(false);
});

it.each(["empty", "failure", "aborted"])("settles %s preparation without allowing confirmation", async outcome => {
  mountHost();
  let finish!: (value: ReturnType<typeof request> | null) => void;
  let fail!: (reason: Error) => void;
  const preparation = new Promise<ReturnType<typeof request> | null>((resolve, reject) => { finish = resolve; fail = reject; });
  const owner = new AbortController();
  let answer!: Promise<boolean>;
  await act(async () => { answer = confirmAction({ ...request(), prepare: () => preparation }, owner.signal); });
  const observed = answer.catch(error => error.message);
  await act(async () => {
    if (outcome === "aborted") { owner.abort(); fail(new Error("late failure")); }
    else if (outcome === "failure") fail(new Error("read failed"));
    else finish(null);
  });
  await expect(observed).resolves.toBe(outcome === "failure" ? "read failed" : false);
  expect(dialog()).toBeNull();
});
