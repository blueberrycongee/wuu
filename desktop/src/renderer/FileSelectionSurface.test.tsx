import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FileSelectionSurface, type FileSelectionControls } from "./FileSelectionSurface";
import { fileSelectionSource } from "./FileSelectionMapping";
import type { FileSelectionPart } from "./FileSelectionContext";

const state = vi.hoisted(() => ({ actions: null as null | {
  ownerKey: string;
  comments: FileSelectionPart[]; addQuote: ReturnType<typeof vi.fn>; addComment: ReturnType<typeof vi.fn>;
  removeComment: ReturnType<typeof vi.fn>; edit: ReturnType<typeof vi.fn>; openFile: ReturnType<typeof vi.fn>;
} }));
vi.mock("./FileSelectionContext", () => ({ useFileSelectionActions: () => state.actions }));
vi.mock("./i18n", () => ({ useI18n: () => ({ locale: "en" }) }));

let root: Root;
let container: HTMLDivElement;
let controls: FileSelectionControls;
const text = "first line\nselected text\nlast line";
const source = fileSelectionSource("/repo", "note.md", text, { start: 11, end: 24, blockFallback: false })!;
beforeEach(() => {
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  state.actions = { ownerKey: "thread-one", comments: [], addQuote: vi.fn(), addComment: vi.fn(), removeComment: vi.fn(), edit: vi.fn().mockResolvedValue(true), openFile: vi.fn() };
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 700, 600));
  Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => new DOMRect(30, 40, 160, 24) });
});
afterEach(() => { act(() => root.unmount()); container.remove(); vi.restoreAllMocks(); delete (Range.prototype as Partial<Range>).getBoundingClientRect; window.getSelection()?.removeAllRanges(); });
function render(path = "note.md", value = text, active = true) {
  act(() => root.render(<FileSelectionSurface workspace="/repo" path={path} text={value} active={active}>{next => {
    controls = next;
    return <pre data-file-source-start="0" data-file-source-end={value.length} data-file-source-text="true">{value}</pre>;
  }}</FileSelectionSurface>));
}
function select() {
  const range = document.createRange(); range.setStart(container.querySelector("pre")!.firstChild!, 11); range.setEnd(container.querySelector("pre")!.firstChild!, 24);
  act(() => { window.getSelection()!.removeAllRanges(); window.getSelection()!.addRange(range); document.dispatchEvent(new Event("selectionchange")); });
}
function button(name: string) { return Array.from(document.querySelectorAll("button")).find(button => button.textContent === name || button.getAttribute("aria-label") === name)!; }
function click(name: string) { act(() => button(name).click()); }
function type(value: string) {
  const input = document.querySelector("textarea")!;
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("file selection surface", () => {
  it("aligns the measured toolbar and comment to the source start without covering it", () => {
    vi.mocked(HTMLElement.prototype.getBoundingClientRect).mockImplementation(function (this: HTMLElement) {
      if (this.classList.contains("file-selection-action-menu")) return new DOMRect(0, 0, this.classList.contains("selection-action-menu-commenting") ? 360 : 200, this.classList.contains("selection-action-menu-commenting") ? 80 : 40);
      return new DOMRect(0, 0, 700, 600);
    });
    Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => new DOMRect(300, 240, 100, 24) });
    render(); select();
    const toolbar = document.querySelector<HTMLElement>(".file-selection-action-menu")!;
    expect(Number.parseFloat(toolbar.style.left)).toBe(300);
    expect(Number.parseFloat(toolbar.style.top)).toBe(192);
    click("Comment");
    const comment = document.querySelector<HTMLElement>(".file-selection-action-menu")!;
    expect(Number.parseFloat(comment.style.left)).toBe(300);
    expect(Number.parseFloat(comment.style.top)).toBe(152);
    expect(document.activeElement).toBe(comment.querySelector("textarea"));
  });

  it("does not expose tools or callbacks without context, or for an inactive file", () => {
    state.actions = null; render(); select();
    expect(document.querySelector('[role="toolbar"]')).toBeNull();
    expect(controls.onSelectionChange).toBeUndefined();
  });

  it("quotes the captured source after browser selection loss on an action click", () => {
    render(); select();
    act(() => window.getSelection()!.removeAllRanges());
    click("Add to conversation");
    expect(state.actions!.addQuote).toHaveBeenCalledWith(source);
    expect(document.querySelector('[role="toolbar"]')).toBeNull();
  });

  it("keeps the selection while typing a comment and submits that captured source", () => {
    render(); select(); click("Comment");
    expect(document.activeElement).toBe(document.querySelector(".selection-action-comment-input"));
    type("Explain this line");
    act(() => { window.getSelection()!.removeAllRanges(); document.dispatchEvent(new Event("selectionchange")); });
    expect(controls.persistentSelection).toEqual(source);
    act(() => document.querySelector("textarea")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })));
    expect(state.actions!.addComment).toHaveBeenCalledWith(source, "Explain this line", undefined);
  });

  it("awaits edit acceptance, prevents duplicate submissions, and retains rejected instructions", async () => {
    let resolve!: (accepted: boolean) => void;
    state.actions!.edit.mockImplementation(() => new Promise<boolean>(done => { resolve = done; }));
    render(); select(); click("Edit"); type("Make this clearer");
    const form = document.querySelector("form")!;
    act(() => { form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); });
    expect(state.actions!.edit).toHaveBeenCalledTimes(1);
    expect(state.actions!.edit).toHaveBeenCalledWith(source, "Make this clearer");
    expect(document.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(true);
    await act(async () => resolve(false));
    expect(document.querySelector("textarea")!.value).toBe("Make this clearer");
    expect(document.querySelector('[role="alert"]')).not.toBeNull();
    state.actions!.edit.mockResolvedValueOnce(true);
    await act(async () => button("Send edit request").click());
    expect(document.querySelector("textarea")).toBeNull();
  });

  it("dismisses the file edit composer on an outside pointer press", () => {
    render(); select(); click("Edit"); type("A draft");
    const input = document.querySelector<HTMLTextAreaElement>(".file-selection-edit-composer textarea")!;
    expect(document.activeElement).toBe(input);
    act(() => input.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(document.querySelector(".file-selection-edit-composer")).not.toBeNull();
    act(() => document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(document.querySelector(".file-selection-edit-composer")).toBeNull();
    expect(state.actions!.edit).not.toHaveBeenCalled();
  });

  it("dismisses an unfinished file comment on an outside pointer press", () => {
    render(); select(); click("Comment"); type("Unsent comment");
    const input = document.querySelector<HTMLTextAreaElement>(".selection-action-comment-input")!;
    act(() => input.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(document.querySelector(".selection-action-comment-input")).not.toBeNull();
    act(() => document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(document.querySelector(".selection-action-comment-input")).toBeNull();
    expect(state.actions!.addComment).not.toHaveBeenCalled();
  });

  it("marks changed revisions stale and blocks edit submission without overwriting the draft", () => {
    render(); select(); click("Edit"); type("Change it"); render("note.md", `${text}!`);
    expect(document.querySelector("textarea")!.value).toBe("Change it");
    expect(button("Send edit request").disabled).toBe(true);
    expect(document.querySelector('[role="status"]')).not.toBeNull();
    expect(controls.persistentSelection).toBeUndefined();
  });

  it("clears transient state on Escape, file switches, and deactivation", () => {
    render(); select(); click("Comment");
    act(() => { document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })); document.dispatchEvent(new KeyboardEvent("keyup", { key: "Escape" })); });
    expect(document.querySelector("textarea")).toBeNull();
    expect(document.querySelector('[role="toolbar"]')).not.toBeNull();
    expect(document.activeElement).toBe(button("Comment"));
    act(() => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    expect(document.querySelector('[role="toolbar"]')).toBeNull();
    select(); click("Comment"); render("other.md");
    expect(document.querySelector("textarea")).toBeNull();
    render("note.md"); select(); render("note.md", text, false);
    expect(controls.onSelectionChange).toBeUndefined();
    expect(document.querySelector('[role="toolbar"]')).toBeNull();
  });

  it("filters saved comments by workspace and file and preserves their ids when editing", () => {
    const part: FileSelectionPart = { type: "file_selection", id: "one", intent: "comment", text: "", source, comment: "Existing" };
    state.actions!.comments = [part, { ...part, id: "two", source: { ...source, path: "else.md" }, comment: "Other" }];
    render();
    expect(container.querySelectorAll(".file-selection-comment")).toHaveLength(1);
    click("Edit comment"); type("Updated"); click("Comment");
    expect(state.actions!.addComment).toHaveBeenCalledWith(source, "Updated", "one");
    click("Remove"); expect(state.actions!.removeComment).toHaveBeenCalledWith("one");
  });

  it("accepts model offsets for virtualized code without depending on DOM text", () => {
    render();
    act(() => controls.onSelectionChange!({ start: 11, end: 24, blockFallback: false, getRect: () => new DOMRect(20, 20, 1, 18) }));
    click("Add to conversation");
    expect(state.actions!.addQuote).toHaveBeenCalledWith(source);
  });

  it("cancels an unfinished form when the conversation owner changes with the same file open", () => {
    render(); select(); click("Comment"); type("Belongs to the first thread");
    const original = state.actions!;
    state.actions = { ...original, ownerKey: "thread-two", addComment: vi.fn() };
    render();
    expect(document.querySelector("textarea")).toBeNull();
    expect(controls.persistentSelection).toBeUndefined();
    expect(state.actions.addComment).not.toHaveBeenCalled();
    select(); click("Comment"); type("Second thread"); click("Comment");
    expect(state.actions.addComment).toHaveBeenCalledWith(source, "Second thread", undefined);
    expect(original.addComment).not.toHaveBeenCalled();
  });

  it("does not close a new owner's form when an old owner's in-flight edit completes", async () => {
    let resolve!: (accepted: boolean) => void;
    state.actions!.edit.mockImplementation(() => new Promise<boolean>(done => { resolve = done; }));
    render(); select(); click("Edit"); type("Old request"); click("Send edit request");
    state.actions = { ...state.actions!, ownerKey: "thread-two" };
    render(); select(); click("Comment"); type("New comment");
    await act(async () => resolve(true));
    expect(document.querySelector("textarea")!.value).toBe("New comment");
  });
});
