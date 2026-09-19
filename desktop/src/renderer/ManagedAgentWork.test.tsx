import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ThreadSummary } from "./AppState";
import { ManagedAgentWork } from "./ManagedAgentWork";
import { translateCurrent as t } from "./i18n";

let host: HTMLDivElement;
let root: Root;
const showAll = vi.fn();
const selectSession = vi.fn();
const threads: ThreadSummary[] = Array.from({ length: 8 }, (_, i) => ({
  id: `session-${i}`, title: `Session ${i}`, cwd: "/private/long/workspace/path", status: i === 0 || i === 3 ? "in_progress" : "idle",
  model: "test", model_provider: "test", preview: "", turns: [], turn_count: 0,
  created_at: "2026-09-01T00:00:00Z", updated_at: new Date(Date.UTC(2026, 8, 1, 0, i)).toISOString(),
}));
beforeEach(() => {
  vi.useFakeTimers(); showAll.mockClear(); selectSession.mockClear();
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.useRealTimers(); });
const render = (items = threads, expanded = false, key = "agent") => act(() => root.render(<ManagedAgentWork key={key} threads={items} expanded={expanded} onShowAll={showAll} onSelect={selectSession} />));
const trigger = () => host.querySelector<HTMLButtonElement>("button")!;
const card = () => document.querySelector<HTMLElement>(".conversation-status-preview-card");
const titles = () => [...document.querySelectorAll(".managed-session-preview-title")].map(node => node.textContent);
const pointer = (node: Element, type: "pointerover" | "pointerout", relatedTarget: EventTarget | null = null) => act(() => { node.dispatchEvent(new MouseEvent(type, { bubbles: true, relatedTarget })); });

it("previews at most five sessions with running first, then recent history, without paths or native titles", () => {
  render(); pointer(trigger(), "pointerover");
  expect(titles()).toEqual(["Session 3", "Session 0", "Session 7", "Session 6", "Session 5"]);
  // Running rows carry the panel's status glyph, so the card repeats neither
  // the trigger label nor a per-row "running" string.
  expect(document.querySelectorAll(".managed-session-preview-item .managed-session-spinner")).toHaveLength(2);
  expect(card()?.textContent).toBe(`${titles().join("")}${t("channels.managedSessions.all")}`);
  expect(card()?.textContent).not.toContain("/private/");
  expect(trigger().hasAttribute("title")).toBe(false);
  expect(card()?.querySelector("[title]")).toBeNull();
  render(threads.map(thread => ({ ...thread, status: "idle" })));
  expect(titles()).toEqual(["Session 7", "Session 6", "Session 5", "Session 4", "Session 3"]);
  act(() => card()!.querySelector<HTMLButtonElement>(".managed-session-preview-all")!.click());
  expect(showAll).toHaveBeenCalledOnce();
  expect(card()).toBeNull();
});

it("navigates to the selected Harness session instead of creating a new one", () => {
  render(); pointer(trigger(), "pointerover");
  act(() => document.querySelector<HTMLButtonElement>(".managed-session-preview-item")!.click());
  expect(selectSession).toHaveBeenCalledWith("session-3");
  expect(showAll).not.toHaveBeenCalled();
  expect(card()).toBeNull();
});

it("keeps the preview open while the pointer crosses into the card and closes after leaving both", () => {
  render(); pointer(trigger(), "pointerover");
  const preview = card()!;
  pointer(trigger(), "pointerout", document.body);
  pointer(preview, "pointerover", document.body);
  act(() => vi.advanceTimersByTime(250));
  expect(card()).toBe(preview);
  pointer(preview, "pointerout", document.body);
  act(() => vi.advanceTimersByTime(250));
  expect(card()).toBeNull();
});

it("opens on focus, preserves focus inside the card, and dismisses with Escape without reopening", () => {
  render(); act(() => trigger().focus());
  act(() => card()!.querySelector<HTMLButtonElement>("button")!.focus());
  act(() => vi.advanceTimersByTime(250));
  expect(card()).not.toBeNull();
  act(() => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(card()).toBeNull();
  expect(document.activeElement).toBe(trigger());
  act(() => vi.advanceTimersByTime(250));
  expect(card()).toBeNull();
  act(() => trigger().click());
  expect(showAll).toHaveBeenCalledOnce();
});

it("offers an empty preview and retires the old card when the active agent changes", () => {
  render([]); act(() => trigger().focus());
  expect(card()?.textContent).toContain(t("channels.managedSessions.empty"));
  expect(titles()).toEqual([]);
  act(() => trigger().click());
  expect(showAll).toHaveBeenCalledOnce();
  pointer(trigger(), "pointerover");
  render(threads, false, "another-agent");
  expect(card()).toBeNull();
});

it("dismisses outside and does not toggle an already open all-sessions view from the preview", () => {
  render(threads, true); act(() => trigger().focus());
  act(() => card()!.querySelector<HTMLButtonElement>(".managed-session-preview-all")!.click());
  expect(showAll).not.toHaveBeenCalled();
  expect(card()).toBeNull();
  pointer(trigger(), "pointerover");
  act(() => document.body.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true })));
  expect(card()).toBeNull();
});
