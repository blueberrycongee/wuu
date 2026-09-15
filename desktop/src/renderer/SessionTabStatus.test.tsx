import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { RuntimeContext, Thread } from "../shared/protocol";
import { createThreadSessionTab, initialState, threadSessionTabID } from "./AppState";
import { SessionTabStrip } from "./SessionTabs";

const context: RuntimeContext = { kind: "no_project", cwd: "/tmp/project" };

function makeThread(id: string): Thread {
  return {
    id,
    preview: id,
    model_provider: "fake",
    model: "fake-model",
    cwd: context.cwd,
    status: "idle",
    created_at: "2026-06-18T00:00:00Z",
    updated_at: "2026-06-18T00:00:00Z",
    turns: [],
  };
}

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function renderTabs(
  thread: Thread,
  active: boolean,
  pending: boolean,
  aggregateRunning = false,
): void {
  const other = makeThread("other");
  act(() => {
    root.render(
      <SessionTabStrip
        state={{
          ...initialState,
          activeContext: context,
          thread: active ? thread : other,
          threads: [thread, other],
          sessionTabs: [
            createThreadSessionTab(thread, context),
            createThreadSessionTab(other, context),
          ],
          activeSessionTabID: threadSessionTabID(active ? thread.id : other.id),
        }}
        runningThreadIDs={new Set(aggregateRunning ? [thread.id] : [])}
        pendingSwitchThreadID={pending ? thread.id : undefined}
        pendingComposerMessagesByThread={{}}
        canStartNewThread
        onSelect={() => {}}
        onClose={() => {}}
        onCloseTabs={() => {}}
        onPopOut={() => {}}
        onNewThread={() => {}}
        onReorder={() => {}}
      />,
    );
  });
}

// Exercise state transitions and stable indicator ownership, not CSS rendering.
describe("session tab status during selection", () => {
  it.each(["turn", "child agent", "workspace aggregate"])(
    "preserves the %s running state and indicator before, during and after selection",
    (source) => {
      const thread = makeThread("running");
      if (source === "turn") {
        thread.turns = [{ id: "turn-1", status: "in_progress", items: [], items_view: "full" }];
      } else if (source === "child agent") {
        thread.child_agents = [{ id: "agent-1", status: "running" }] as Thread["child_agents"];
      }
      const aggregateRunning = source === "workspace aggregate";
      renderTabs(thread, false, false, aggregateRunning);
      const indicator = container.querySelector(".session-tab-status")!;
      expect(indicator).not.toBeNull();

      // Pending selection can overlap either the old or the new active tab.
      const phases = [
        [false, false],
        [false, true],
        [true, true],
        [true, false],
        [false, false],
      ];
      for (const [active, pending] of phases) {
        renderTabs(thread, active, pending, aggregateRunning);
        expect(container.querySelector(".session-tab-status")).toBe(indicator);
        const tab = indicator.closest(".session-tab")!;
        expect(tab.classList.contains("running")).toBe(true);
        expect(tab.classList.contains("pending-switch")).toBe(pending);
        expect(tab.classList.contains("has-unread")).toBe(false);
        const button = tab.querySelector('[role="tab"]')!;
        expect(button.getAttribute("aria-busy")).toBe(String(pending));
        expect(button.getAttribute("aria-selected")).toBe(String(active));
      }
    },
  );

  it("clears an idle tab's busy state after selection without marking it running", () => {
    const thread = makeThread("idle");
    renderTabs(thread, false, true);
    const tab = container.querySelector(".session-tab")!;
    const button = tab.querySelector('[role="tab"]')!;
    expect(tab.classList.contains("running")).toBe(false);
    expect(tab.classList.contains("pending-switch")).toBe(true);
    expect(button.getAttribute("aria-busy")).toBe("true");
    expect(button.getAttribute("aria-selected")).toBe("false");

    renderTabs(thread, true, false);
    expect(container.querySelector(".session-tab")).toBe(tab);
    expect(tab.classList.contains("running")).toBe(false);
    expect(tab.classList.contains("pending-switch")).toBe(false);
    expect(button.getAttribute("aria-busy")).toBe("false");
    expect(button.getAttribute("aria-selected")).toBe("true");
  });
});
