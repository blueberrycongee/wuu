// Regression: Modal closes while the archive action is disabled, so its normal
// focus restoration runs before the initiating button becomes focusable again.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { ConfirmDialogHost } from "./ConfirmDialog";
import { useArchiveDeletion } from "./useArchiveDeletion";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let root: Root | undefined;
let container: HTMLDivElement | undefined;

function button(selector: string): HTMLButtonElement {
  const element = document.querySelector<HTMLButtonElement>(selector);
  if (!element) throw new Error(`Missing ${selector}`);
  return element;
}

async function mount() {
  const remove = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: {
      listArchivedThreads: vi.fn().mockResolvedValue({
        threads: [{ id: "archived", archived: true }],
      }),
    },
  });
  function Harness() {
    const deletion = useArchiveDeletion(remove, () => {});
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
    expect(document.activeElement).toBe(button('[data-confirm-action="confirm"]'));

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
