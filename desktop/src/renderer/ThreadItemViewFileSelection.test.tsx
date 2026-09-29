import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { MessageContentPart, ThreadItem, WuuDesktopApi } from "../shared/protocol";
import { ThreadItemView } from "./ThreadItemView";
import { buildFileSelectionPart } from "./FileSelectionContext";
import { translateCurrent } from "./i18n";
import { fileSelectionRevision } from "./FileSelectionMapping";

let root: Root | undefined;
let container: HTMLDivElement;
const selection = buildFileSelectionPart({
  workspace: "/repo", path: "src/example.ts", start_line: 12, start_column: 3,
  end_line: 14, end_column: 9, quote: "const original = true;\nreturn original;", revision: "sha256:old",
}, "comment", "Explain this branch");
const paste: MessageContentPart = { type: "pasted_text", text: "Pasted context\n", title: "Context" };
const parts: MessageContentPart[] = [selection, paste, { type: "text", text: "Please review." }];
const item: ThreadItem = {
  id: "file-message", type: "user_message", status: "completed",
  text: parts.map((part) => part.text).join(""), content_parts: parts,
};

afterEach(() => {
  act(() => root?.unmount());
  root = undefined;
  document.body.innerHTML = "";
  delete (window as unknown as { wuu?: unknown }).wuu;
});

function render(editing = false, message = item) {
  const onSubmit = vi.fn();
  const onOpenFile = vi.fn();
  if (!root) {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  }
  act(() => root!.render(<ThreadItemView item={message} turnID="turn-1" turnStatus="completed"
    streaming={false} onStreamFrame={() => {}} editing={editing} onSubmitEditMessage={onSubmit}
    onOpenFile={onOpenFile} />));
  return { onSubmit, onOpenFile };
}

function click(selector: string): void {
  const button = document.querySelector<HTMLElement>(selector);
  expect(button).not.toBeNull();
  act(() => button!.click());
}

function input(selector: string, value: string): void {
  const field = document.querySelector<HTMLTextAreaElement>(selector)!;
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("file selections in message history", () => {
  it("opens on keyboard focus and transfers focus into the panel, then returns on Escape", () => {
    render();
    const trigger = document.querySelector<HTMLButtonElement>(".file-selection-tag")!;
    act(() => trigger.focus());
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    act(() => trigger.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true })));
    expect(document.querySelector('[role="dialog"]')?.contains(document.activeElement)).toBe(true);
    act(() => document.activeElement!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("keeps canonical context out of the bubble and verifies the revision only when opening its location", async () => {
    const currentText = "current file contents";
    const part = { ...selection, source: { ...selection.source, revision: fileSelectionRevision(currentText) } };
    const readWorkspaceFile = vi.fn().mockResolvedValue({ text: currentText });
    window.wuu = { readWorkspaceFile } as unknown as WuuDesktopApi;
    const { onOpenFile } = render(false, { ...item, content_parts: [part, ...parts.slice(1)] });
    expect(container.querySelector(".user-message")?.textContent).toBe("Please review.");
    click(".file-selection-tag");
    expect(document.querySelector(".file-selection-comment")?.textContent).toBe(selection.comment);
    expect(document.querySelector("details")?.open).toBe(false);
    click(".file-selection-original summary");
    expect(document.querySelector("details")?.open).toBe(true);
    expect(document.querySelector(".file-selection-original pre")?.textContent).toBe(selection.source.quote);
    expect(readWorkspaceFile).not.toHaveBeenCalled();
    await act(async () => document.querySelector<HTMLButtonElement>(".file-selection-location")!.click());
    expect(readWorkspaceFile).toHaveBeenCalledWith(selection.source.path, selection.source.workspace);
    expect(onOpenFile).toHaveBeenCalledWith("/repo/src/example.ts#L12,3-L14,9");
    expect(document.querySelectorAll(".file-selection-card .file-selection-action")).toHaveLength(0);
  });

  it.each(["ambiguous", "missing", "unreadable", "truncated"])("opens without a range and preserves the excerpt when the location is %s", async (scenario) => {
    const readWorkspaceFile = scenario === "unreadable" ? vi.fn().mockRejectedValue(new Error("offline"))
      : vi.fn().mockResolvedValue({ text: scenario === "missing" ? "new contents" : `${selection.source.quote}\n${selection.source.quote}`, truncated: scenario === "truncated" });
    window.wuu = { readWorkspaceFile } as unknown as WuuDesktopApi;
    const { onOpenFile } = render();
    click(".file-selection-tag");
    await act(async () => document.querySelector<HTMLButtonElement>(".file-selection-location")!.click());
    expect(onOpenFile).toHaveBeenCalledWith("/repo/src/example.ts");
    expect(document.querySelector('[role="status"]')).not.toBeNull();
    expect(document.querySelector(".file-selection-original pre")?.textContent).toBe(selection.source.quote);
  });

  it("preserves source, ID, canonical text and mixed attachment order when changing the visible prompt", () => {
    const { onSubmit } = render(true);
    expect(document.querySelector<HTMLTextAreaElement>(".user-message-edit-input")?.value).toBe("Please review.");
    input(".user-message-edit-input", "Focus on recovery.");
    click(".composer-send-button");
    expect(onSubmit).toHaveBeenCalledWith("turn-1", item,
      `${selection.text}${paste.text}Focus on recovery.`, [], [],
      [selection, paste, { type: "text", text: "Focus on recovery." }]);
  });

  it("reveals a pasted block without revealing or dropping the file selection", () => {
    const { onSubmit } = render(true);
    click(".composer-collapsed-prompt-card .composer-document-card-main");
    expect(document.querySelector<HTMLTextAreaElement>(".user-message-edit-input")?.value).toBe(`Please review.${paste.text}`);
    click(".composer-send-button");
    expect(onSubmit.mock.calls[0][5]).toEqual([selection, { type: "text", text: `Please review.${paste.text}` }]);
  });

  it("updates a historical comment and its canonical block together", () => {
    const { onSubmit } = render(true);
    click(".file-selection-tag");
    click(".file-selection-card .file-selection-action");
    input(".file-selection-comment-editor textarea", "Handle the empty case.");
    const save = [...document.querySelectorAll<HTMLButtonElement>(".file-selection-comment-actions button")]
      .find((button) => button.textContent === translateCurrent("common.save"))!;
    act(() => save.click());
    click(".composer-send-button");
    const submitted = onSubmit.mock.calls[0][5] as MessageContentPart[];
    expect(submitted[0]).toEqual(buildFileSelectionPart(selection.source, selection.intent, "Handle the empty case.", selection.id));
    expect(onSubmit.mock.calls[0][2]).toBe(submitted.map((part) => part.text).join(""));
  });

  it("drops an unfinished comment edit when clicking outside the selection panel", () => {
    render(true);
    click(".file-selection-tag");
    click(".file-selection-card .file-selection-action");
    input(".file-selection-comment-editor textarea", "Unsent change");
    act(() => document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(document.querySelector(".file-selection-comment-editor")).toBeNull();
    click(".file-selection-tag");
    expect(document.querySelector(".file-selection-comment-editor")).toBeNull();
    expect(document.querySelector(".file-selection-comment")?.textContent).toBe(selection.comment);
  });

  it("removes only the selected file block and allows a file-only historical message", () => {
    const { onSubmit } = render(true, { ...item, text: selection.text, content_parts: [selection] });
    const send = document.querySelector<HTMLButtonElement>(".composer-send-button")!;
    expect(send.disabled).toBe(false);
    click(".composer-send-button");
    expect(onSubmit.mock.calls[0][5]).toEqual([selection]);
    click(".file-selection-tag");
    click(".file-selection-card .file-selection-action:last-child");
    expect(send.disabled).toBe(true);
    expect(document.querySelector(".file-selection-tag")).toBeNull();
  });

  it("reseeds the editor when switching historical messages", () => {
    render(true);
    input(".user-message-edit-input", "Unsaved draft");
    const next = { ...item, id: "next", text: "Next prompt", content_parts: [{ type: "text" as const, text: "Next prompt" }] };
    const { onSubmit } = render(true, next);
    click(".composer-send-button");
    expect(onSubmit.mock.calls[0][5]).toEqual(next.content_parts);
    expect(document.querySelector(".file-selection-tag")).toBeNull();
  });
});
