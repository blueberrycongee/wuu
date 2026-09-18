import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ThreadSummary } from "./AppState";
import { ManagedAgentSessionView } from "./ManagedAgentSessionView";
import { translateCurrent as t } from "./i18n";

let host: HTMLDivElement;
let root: Root;
const select = vi.fn();
const threads: ThreadSummary[] = Array.from({ length: 65 }, (_, i) => ({
  id: `session-${i}`, title: `Session ${i}`, cwd: i === 0 ? "/unique-project" : "/project", status: "idle",
  model: "test", model_provider: "test", preview: "", turns: [], turn_count: 0,
  created_at: "2026-09-01T00:00:00Z", updated_at: new Date(Date.UTC(2026, 8, 1, 0, i)).toISOString(),
}));
beforeEach(() => { select.mockClear(); host = document.createElement("div"); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });
const render = (items = threads) => act(() => root.render(<ManagedAgentSessionView threads={items} lastViewedTurnByThreadID={{}} onSelect={select} />));
const results = () => [...host.querySelectorAll<HTMLButtonElement>('.managed-session-result')];
const next = () => host.querySelector<HTMLButtonElement>(`[aria-label="${t("channels.managedSessions.next")}"]`)!;
function search(query: string) {
  const input = host.querySelector('input')!;
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, query);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

it("pages the full history, searches beyond the current page, and opens the exact session", () => {
  render();
  expect(results()).toHaveLength(30);
  act(() => next().click());
  expect(results()).toHaveLength(30);
  act(() => next().click());
  expect(results()).toHaveLength(5);
  expect(next().disabled).toBe(true);
  search('UNIQUE-PROJECT');
  expect(results()).toHaveLength(1);
  act(() => results()[0].click());
  expect(select).toHaveBeenCalledExactlyOnceWith('session-0');
  search('no-such-session');
  expect(results()).toHaveLength(0);
  search('');
  expect(results()).toHaveLength(30);
});

it("recovers a valid page when sessions disappear and remains usable with an empty history", () => {
  render();
  act(() => next().click());
  act(() => next().click());
  render(threads.slice(0, 2));
  expect(results()).toHaveLength(2);
  render([]);
  expect(results()).toHaveLength(0);
  expect(host.querySelector('input')).not.toBeNull();
});

it("keeps paths out of the reading label while retaining path search and exact session selection", () => {
  const items = threads.slice(0, 2).map(thread => ({ ...thread, title: 'Shared title' }));
  render(items);
  for (const row of results()) {
    expect(row.textContent).toBe('Shared title');
    expect(row.title).toContain('/');
  }
  search('unique-project');
  expect(results()).toHaveLength(1);
  expect(results()[0].title).toContain('/unique-project');
  act(() => results()[0].click());
  expect(select).toHaveBeenCalledExactlyOnceWith('session-0');
});
