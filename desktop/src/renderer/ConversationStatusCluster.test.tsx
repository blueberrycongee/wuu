import { ApplicationComposerPresentation } from "./plugins/ApplicationComposerPresentation";
import { initialState } from "./AppState";
import type { Thread, TodoUpdate } from "../shared/protocol";
import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConversationStatusCluster } from "./ConversationStatusCluster";
import { readBrowserPiPHostLayout } from "./BrowserPiPHostReporter";
import { I18nProvider } from "./i18n";
import { HOVER_REVEAL_OPEN_DELAY_MS } from "./HoverReveal";
import { PluginHost } from "./plugins/PluginHost";

const services: ApplicationComposerPresentation[] = [];
function applicationFor(host = new PluginHost({ react: React }), todoUpdate?: TodoUpdate) {
  const thread: Thread = { id: "parent-session", preview: "", cwd: "/one", model_provider: "api", model: "model", status: "idle", created_at: "", updated_at: "",
    turns: [{ id: "turn", status: "in_progress", items_view: "full", items: todoUpdate ? [{ id: "todo", type: "tool_call", display: { capability: "todo" }, arguments: JSON.stringify(todoUpdate) }] : [] }] };
  const helper = new ApplicationComposerPresentation({ getState: () => ({ ...initialState, thread, threads: [thread] }), updateState: () => {}, subscribeState: () => () => {}, getPluginHost: () => host });
  services.push(helper);
  return { composerPresentation: helper.createScope(new AbortController().signal), invoke: vi.fn().mockResolvedValue(undefined) };
}

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
    services.splice(0).forEach(service => service.dispose());
    vi.useRealTimers();
  });

  it("keeps preview placement independent of the navigation and status row", () => {
    container.setAttribute("data-pip-anchor-host", "conversation");
    container.getBoundingClientRect = () => ({ left: 0, top: 0, width: 800, height: 600 }) as DOMRect;
    const before = readBrowserPiPHostLayout(document);
    act(() => root.render(
      <ConversationStatusCluster
        application={applicationFor()}
        visible
        navigation={<button>Jump</button>}
        clusterRef={(node) => {
          if (node) node.getBoundingClientRect = () => ({ left: 500, top: 550, width: 250, height: 30 }) as DOMRect;
        }}
      />,
    ));
    expect(container.querySelector("button")).not.toBeNull();
    expect(readBrowserPiPHostLayout(document)).toEqual(before);
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
        application={applicationFor(host)}
        visible
        threadId="parent-session"
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

  it("opens status sessions through the public split action and drops unloaded status", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({ pluginId: "status", generation: "one", register: api => {
      api.registerComposerStatusSource({ id: "agents", getSnapshot: () => [{ id: "child", label: "Open child", action: { kind: "open-session", sessionId: "child-session" } }], subscribe: () => () => {} });
    } });
    const application = applicationFor(host);
    act(() => root.render(<ConversationStatusCluster application={application} visible threadId="parent-session" />));
    await act(async () => container.querySelector<HTMLButtonElement>(".conversation-status-capsule")!.click());
    expect(application.invoke).toHaveBeenCalledWith("conversation.open-split", { threadId: "child-session" });
    act(() => host.unload("status"));
    expect(container.querySelector(".conversation-status-cluster")).toBeNull();
  });

  it("shows only the TODO items in the hover card", async () => {
    vi.useFakeTimers();
    const host = new PluginHost({ react: React });

    act(() => root.render(
      <I18nProvider>
        <ConversationStatusCluster
          application={applicationFor(host, {
            explanation: "Why the list changed.",
            todos: [
              { content: "Inspect the current task", status: "completed" },
              { content: "Keep the current task in view", status: "in_progress" },
              { content: "Write the follow-up", status: "pending" },
            ],
          })}
          visible
          threadId="parent-session"
        />
      </I18nProvider>,
    ));

    const trigger = container.querySelector<HTMLElement>(".conversation-status-todo-trigger");
    act(() => {
      trigger?.focus();
      vi.advanceTimersByTime(HOVER_REVEAL_OPEN_DELAY_MS);
    });
    const card = document.querySelector(".conversation-status-todo-card");
    expect(trigger?.textContent).toBe("TODO1/3");
    expect([...card?.querySelectorAll("li") ?? []].map((item) => item.textContent)).toEqual([
      "Inspect the current task",
      "Keep the current task in view",
      "Write the follow-up",
    ]);
  });
});
