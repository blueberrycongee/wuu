import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { ConversationReaction } from "./ConversationReaction";

// Public interaction failures: accidental text submission, inaccessible touch/keyboard
// controls, replacement instead of removal, and Escape closing the parent dialog.
let cleanup = () => {};
afterEach(() => cleanup());
async function mount(emoji?: string, editable = true) {
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container), change = vi.fn(), parentEscape = vi.fn();
  await act(async () => root.render(<div onKeyDown={parentEscape}><ConversationReaction reaction={{ emoji, label: "Reaction", onChange: editable ? change : undefined }} /></div>));
  cleanup = () => { act(() => root.unmount()); container.remove(); };
  const click = async (selector: string) => { const node = document.querySelector<HTMLButtonElement>(selector)!; expect(node).not.toBeNull(); await act(async () => node.click()); };
  return { container, change, parentEscape, click };
}
it("opens with a tap, replaces or removes exactly one emoji without a text submission", async () => {
  const ui = await mount("❤️"); await ui.click('[data-action="react-message"]');
  await ui.click('[data-emoji="❤️"]'); expect(ui.change).toHaveBeenLastCalledWith(null);
  await ui.click('[data-action="react-message"]'); await ui.click('[data-emoji="✅"]');
  expect(ui.change).toHaveBeenLastCalledWith("✅"); expect(document.querySelector('[data-reaction-picker]')).toBeNull();
});
it("handles keyboard focus, Escape and outside dismissal without escaping into a reply dialog", async () => {
  const ui = await mount(); await ui.click('[data-action="react-message"]');
  const picker = document.querySelector('[data-reaction-picker]')!;
  expect(picker.contains(document.activeElement)).toBe(true);
  await act(async () => document.activeElement!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(document.querySelector('[data-reaction-picker]')).toBeNull(); expect(ui.parentEscape).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(ui.container.querySelector('[data-action="react-message"]'));
  await ui.click('[data-action="react-message"]');
  await act(async () => document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
  expect(document.querySelector('[data-reaction-picker]')).toBeNull();
});
it("renders a twin reaction as a named non-editable badge", async () => {
  const ui = await mount("✅", false);
  expect(ui.container.querySelector('[data-reaction-badge]')?.getAttribute("aria-label")).toContain("✅");
  expect(ui.container.querySelector("button")).toBeNull();
});
