import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConversationStatusCluster } from "./ConversationStatusCluster";
import { I18nProvider } from "./i18n";
import { PluginHost } from "./plugins/PluginHost";

describe("ConversationStatusCluster", () => {
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

  it("closes the overflow menu when the user clicks outside", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "status-test",
      generation: "one",
      register(api) {
        api.registerComposerStatusSource({
          id: "agents",
          getSnapshot: () => [
            { id: "one", label: "agent_one", state: "running" },
            { id: "two", label: "agent_two", state: "running" },
            { id: "three", label: "agent_three", state: "running" },
            { id: "four", label: "agent_four", state: "running" },
          ],
          subscribe: () => () => undefined,
        });
      },
    });

    act(() => root.render(
      <ConversationStatusCluster
        host={host}
        visible
        threadId="parent-session"
        todoUpdate={undefined}
        onOpenSession={vi.fn()}
      />,
    ));

    const overflow = container.querySelector<HTMLDetailsElement>(".conversation-status-overflow");
    const trigger = overflow?.querySelector<HTMLElement>("summary");
    expect(overflow).not.toBeNull();
    expect(trigger).not.toBeNull();

    act(() => trigger?.click());
    expect(overflow?.open).toBe(true);

    act(() => {
      document.body.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true }));
    });
    expect(overflow?.open).toBe(false);
  });

  it("shows only the TODO items in the hover card", async () => {
    const host = new PluginHost({ react: React });

    act(() => root.render(
      <I18nProvider>
        <ConversationStatusCluster
          host={host}
          visible
          threadId="parent-session"
          todoUpdate={{
            explanation: "Why the list changed.",
            todos: [
              { content: "Inspect the current task", status: "completed" },
              { content: "Keep the current task in view", status: "in_progress" },
              { content: "Write the follow-up", status: "pending" },
            ],
          }}
          onOpenSession={vi.fn()}
        />
      </I18nProvider>,
    ));

    const trigger = container.querySelector(".conversation-status-todo-trigger");
    const card = container.querySelector(".conversation-status-todo-card");
    expect(trigger?.textContent).toBe("TODO1/3");
    expect(card?.querySelector("strong")).toBeNull();
    expect(card?.textContent).not.toContain("Why the list changed.");
    expect(card?.textContent).not.toContain("1/3");
    expect([...card?.querySelectorAll("li") ?? []].map((item) => item.textContent)).toEqual([
      "✓Inspect the current task",
      "2Keep the current task in view",
      "3Write the follow-up",
    ]);
  });
});
