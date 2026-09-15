import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { RuntimeContext, Thread } from "../shared/protocol";
import { createThreadSessionTab, initialState, threadSessionTabID } from "./AppState";
import { SessionTabStrip } from "./SessionTabs";

const context: RuntimeContext = { kind: "no_project", cwd: "/tmp/project" };
const shellStyles = readFileSync(
  resolve(__dirname, "styles/conversation-shell.css"),
  "utf8",
);

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
let style: HTMLStyleElement;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  // Class-only assertions miss cascade conflicts between running and pending-switch.
  style = document.createElement("style");
  style.textContent = shellStyles;
  document.head.appendChild(style);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  style.remove();
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

describe("session tab status styles", () => {
  it.each(["turn", "child agent", "workspace aggregate"])(
    "preserves the %s spinner before, during and after tab selection",
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
      const animation = getComputedStyle(indicator).animation;
      expect(animation).toContain("wuu-spin");

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
        expect(container.querySelector(".session-tab-main")?.getAttribute("aria-busy")).toBe(
          String(pending),
        );
        const computed = getComputedStyle(indicator);
        expect(computed.width).toBe("10px");
        expect(computed.height).toBe("10px");
        expect(computed.backgroundColor).toBe("rgba(0, 0, 0, 0)");
        expect(computed.opacity).toBe("1");
        expect(computed.animation).toBe(animation);
      }
    },
  );

  it("still shows a pending dot for an idle tab and clears it after selection", () => {
    const thread = makeThread("idle");
    renderTabs(thread, false, true);
    const indicator = container.querySelector(".session-tab-status")!;
    expect(getComputedStyle(indicator).width).toBe("7px");
    expect(getComputedStyle(indicator).opacity).toBe("1");
    expect(getComputedStyle(indicator).animation).not.toContain("wuu-spin");

    renderTabs(thread, true, false);
    expect(getComputedStyle(indicator).width).toBe("0px");
    expect(getComputedStyle(indicator).opacity).toBe("0");
  });

  it("still shows an unread dot for a completed tab", () => {
    const thread = makeThread("unread");
    thread.turns = [{ id: "turn-1", status: "completed", items: [], items_view: "full" }];
    renderTabs(thread, false, false);
    expect(container.querySelector(".session-tab.has-unread")).not.toBeNull();
    const indicator = container.querySelector(".session-tab-status")!;
    expect(getComputedStyle(indicator).width).toBe("7px");
    expect(getComputedStyle(indicator).opacity).toBe("1");
    expect(getComputedStyle(indicator).animation).not.toContain("wuu-spin");
  });
});
