// Regression: Modal closes while the archive action is disabled, so its normal
// focus restoration runs before the initiating button becomes focusable again.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { ConfirmDialogHost } from "./ConfirmDialog";
import { useArchiveDeletion } from "./useArchiveDeletion";
import { ApplicationSettings } from "./plugins/ApplicationSettings";
import { initialState, type AppState } from "./AppState";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let root: Root | undefined;
let container: HTMLDivElement | undefined;

function button(selector: string): HTMLButtonElement {
  const element = document.querySelector<HTMLButtonElement>(selector);
  if (!element) throw new Error(`Missing ${selector}`);
  return element;
}

async function mount(listing = Promise.resolve({ threads: [{ id: "archived", archived: true }] }), listSource?: (signal?: AbortSignal) => Promise<readonly { id: string; archived?: boolean }[]>) {
  const remove = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: {
      listArchivedThreads: vi.fn().mockReturnValue(listing),
    },
  });
  const source = { remove, list: listSource ?? (async () => (await window.wuu.listArchivedThreads()).threads) };
  function Harness() {
    const deletion = useArchiveDeletion(source);
    return (
      <>
        <button
          id="delete-archive"
          disabled={deletion.pending}
          onClick={() => void deletion.removeAll()}
        >
          Delete archive
        </button>
        <button id="other-action">Other action</button>
        <ConfirmDialogHost />
      </>
    );
  }
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  await act(async () => root!.render(<WuuUIRoot><Harness /></WuuUIRoot>));
  return remove;
}

afterEach(async () => {
  const cancel = document.querySelector<HTMLButtonElement>('[data-confirm-action="cancel"]');
  if (cancel) await act(async () => cancel.click());
  await act(async () => root?.unmount());
  container?.remove();
  delete (window as { wuu?: unknown }).wuu;
  vi.restoreAllMocks();
});

it.each(["cancel", "escape", "backdrop"])(
  "returns focus after %s dismisses the archive confirmation",
  async (dismissal) => {
    const remove = await mount();
    const opener = button("#delete-archive");
    opener.focus();
    await act(async () => opener.click());
    expect(opener.disabled).toBe(true);
    expect(document.activeElement).toBe(button('[data-confirm-action="cancel"]'));

    await act(async () => {
      if (dismissal === "cancel") button('[data-confirm-action="cancel"]').click();
      else if (dismissal === "escape") window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
      else document.querySelector<HTMLElement>(".modal-backdrop")!.click();
    });

    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(remove).not.toHaveBeenCalled();
    expect(opener.disabled).toBe(false);
    expect(document.activeElement).toBe(opener);
  },
);

it("preserves a newer focus destination when the confirmation is dismissed", async () => {
  await mount();
  const opener = button("#delete-archive");
  opener.focus();
  await act(async () => opener.click());
  const destination = button("#other-action");
  destination.focus();
  await act(async () => button('[data-confirm-action="cancel"]').click());
  expect(opener.disabled).toBe(false);
  expect(document.activeElement).toBe(destination);
});

// A slow authoritative read must not hide the dialog or permit deletion.
it("opens immediately, gates confirmation on a fresh snapshot, and ignores repeated clicks", async () => {
  let resolve!: (value: { threads: { id: string; archived: boolean }[] }) => void;
  const listing = new Promise<{ threads: { id: string; archived: boolean }[] }>(done => { resolve = done; });
  const remove = await mount(listing);
  const opener = button("#delete-archive");
  opener.focus();
  await act(async () => { opener.click(); opener.click(); });
  expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1);
  expect(button('[data-confirm-action="confirm"]').disabled).toBe(true);
  await act(async () => button('[data-confirm-action="confirm"]').click());
  expect(remove).not.toHaveBeenCalled();
  expect(window.wuu.listArchivedThreads).toHaveBeenCalledTimes(1);
  await act(async () => resolve({ threads: [{ id: "fresh-one", archived: true }, { id: "fresh-two", archived: true }] }));
  expect(button('[data-confirm-action="confirm"]').disabled).toBe(false);
  expect(remove).not.toHaveBeenCalled();
  await act(async () => button('[data-confirm-action="confirm"]').click());
  expect(remove.mock.calls).toEqual([["fresh-one"], ["fresh-two"]]);
});

it.each(["cancel", "escape", "backdrop"])("cancels slow preparation with %s without reopening or deleting", async dismissal => {
  let resolve!: (value: { threads: { id: string; archived: boolean }[] }) => void;
  const listing = new Promise<{ threads: { id: string; archived: boolean }[] }>(done => { resolve = done; });
  const remove = await mount(listing);
  const opener = button("#delete-archive");
  opener.focus();
  await act(async () => opener.click());
  expect(document.querySelector('[role="dialog"]')).not.toBeNull();
  await act(async () => {
    if (dismissal === "cancel") button('[data-confirm-action="cancel"]').click();
    else if (dismissal === "escape") window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    else document.querySelector<HTMLElement>(".modal-backdrop")!.click();
  });
  expect(opener.disabled).toBe(false);
  expect(document.activeElement).toBe(opener);
  await act(async () => resolve({ threads: [{ id: "late", archived: true }] }));
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(remove).not.toHaveBeenCalled();
});

it("does not let canceled preparation disturb a newer confirmation", async () => {
  let finishOld!: (value: { threads: { id: string; archived: boolean }[] }) => void;
  const listing = new Promise<{ threads: { id: string; archived: boolean }[] }>(done => { finishOld = done; });
  const remove = await mount(listing);
  await act(async () => button("#delete-archive").click());
  await act(async () => button('[data-confirm-action="cancel"]').click());
  vi.mocked(window.wuu.listArchivedThreads).mockResolvedValue({ threads: [{ id: "successor", archived: true }] } as never);
  await act(async () => button("#delete-archive").click());
  const nextPrompt = button('[data-confirm-action="confirm"]');
  await act(async () => finishOld({ threads: [] }));
  expect(button('[data-confirm-action="confirm"]')).toBe(nextPrompt);
  expect(button("#delete-archive").disabled).toBe(true);
  await act(async () => nextPrompt.click());
  expect(remove.mock.calls).toEqual([["successor"]]);
});

it("dismisses preparation when its settings surface unmounts", async () => {
  let finish!: (value: { threads: { id: string; archived: boolean }[] }) => void;
  const listing = new Promise<{ threads: { id: string; archived: boolean }[] }>(done => { finish = done; });
  const remove = await mount(listing);
  await act(async () => button("#delete-archive").click());
  await act(async () => { root!.unmount(); root = undefined; });
  await act(async () => finish({ threads: [{ id: "retired", archived: true }] }));
  // A new host must not inherit the retired request from the global queue.
  root = createRoot(container!);
  await act(async () => root!.render(<WuuUIRoot><ConfirmDialogHost /></WuuUIRoot>));
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(remove).not.toHaveBeenCalled();
});

it("aborts a canceled authoritative read before it can republish stale archived rows", async () => {
  let state: AppState = { ...initialState };
  const settings = new ApplicationSettings({ getState: () => state,
    updateState: update => { state = typeof update === "function" ? update(state) : update; },
    changed: vi.fn(), isNavigationPending: () => false, deleteArchivedThread: vi.fn(), unarchiveThread: vi.fn() });
  let finishOld!: (value: { threads: { id: string; archived: boolean }[] }) => void;
  const listing = new Promise<{ threads: { id: string; archived: boolean }[] }>(done => { finishOld = done; });
  const remove = await mount(listing, signal => settings.invoke("settings.list-archived", {}, signal));
  await act(async () => button("#delete-archive").click());
  await act(async () => button('[data-confirm-action="cancel"]').click());
  vi.mocked(window.wuu.listArchivedThreads).mockResolvedValue({ threads: [{ id: "new", archived: true }] } as never);
  await act(async () => button("#delete-archive").click());
  expect(state.threads.map(thread => thread.id)).toEqual(["new"]);
  await act(async () => finishOld({ threads: [{ id: "stale", archived: true }] }));
  expect(state.threads.map(thread => thread.id)).toEqual(["new"]);
  await act(async () => button('[data-confirm-action="confirm"]').click());
  expect(remove.mock.calls).toEqual([["new"]]);
});
