import { act, useEffect, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ThreadItem } from "../../shared/protocol";
import { CONVERSATION_ITEM_ACTIONS, type ConversationItemSnapshotV1 } from "../../shared/workbench";
import { ConversationItemPresentation } from "./ConversationItemPresentation";
import { PluginHost } from "./PluginHost";
import { WorkbenchController } from "./Workbench";

let container: HTMLDivElement;
let root: Root;

afterEach(() => {
  act(() => root?.unmount());
  container?.remove();
  vi.restoreAllMocks();
});

async function setup(
  register: Parameters<PluginHost["activateGeneration"]>[0]["register"],
): Promise<{ host: PluginHost; controller: WorkbenchController }> {
  const host = new PluginHost({ react: await import("react") });
  await host.activateGeneration({ pluginId: "item-plugin", generation: "gen-1", register });
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  return { host, controller: new WorkbenchController(host) };
}

function renderItem(
  runtime: { host: PluginHost; controller: WorkbenchController },
  item: ThreadItem,
  onEdit?: () => void,
): void {
  act(() => root.render(
    <ConversationItemPresentation
      {...runtime}
      item={item}
      fallback={<article data-native-root>native</article>}
      onEdit={onEdit}
    />,
  ));
}

describe("ConversationItemPresentation", () => {
  it("replaces the complete native root with the exact public kind", async () => {
    const runtime = await setup((api) => {
      api.registerPresenter({
        id: "assistant",
        target: "conversation.item",
        key: "assistant-message",
        render: ({ snapshot }) => <section data-plugin-root>{(snapshot as ConversationItemSnapshotV1).text}</section>,
      });
    });

    renderItem(runtime, { id: "reason-1", type: "reasoning", status: "completed", text: "thought" });
    expect(container.querySelector("[data-native-root]")).not.toBeNull();

    renderItem(runtime, { id: "answer-1", type: "agent_message", status: "completed", text: "answer" });

    expect(container.querySelector("[data-plugin-root]")?.textContent).toBe("answer");
    expect(container.querySelector("[data-native-root]")).toBeNull();
  });

  it("publishes a deeply immutable sanitized snapshot without private records or attachment bytes", async () => {
    let received: ConversationItemSnapshotV1 | undefined;
    const runtime = await setup((api) => {
      api.registerPresenter({
        id: "user",
        target: "conversation.item",
        key: "user-message",
        render: ({ snapshot }) => {
          received = snapshot as ConversationItemSnapshotV1;
          return <section data-plugin-root />;
        },
      });
    });
    renderItem(runtime, {
      id: "user-1",
      seq: 42,
      source_id: "private-source",
      agent_id: "private-agent",
      type: "user_message",
      status: "completed",
      text: "hello",
      images: [{ media_type: "image/png", data: "raw-image-base64" }],
      files: [{ media_type: "application/pdf", data: "raw-file-base64", filename: "brief.pdf" }],
    });

    expect(received).toEqual({
      contractVersion: 1,
      id: "user-1",
      kind: "user-message",
      status: "completed",
      phase: undefined,
      text: "hello",
      contentType: "markdown",
      content: [{ type: "markdown", text: "hello" }],
      attachments: [
        { id: "user-1:image:0", name: "image-1", mimeType: "image/png" },
        { id: "user-1:file:0", name: "brief.pdf", mimeType: "application/pdf" },
      ],
    });
    expect(JSON.stringify(received)).not.toMatch(/private-source|private-agent|raw-.*base64|seq|source_id/);
    expect(Object.isFrozen(received)).toBe(true);
    expect(Object.isFrozen(received?.content)).toBe(true);
    expect(Object.isFrozen(received?.content?.[0])).toBe(true);
    expect(Object.isFrozen(received?.attachments)).toBe(true);
    expect(Object.isFrozen(received?.attachments?.[0])).toBe(true);
  });

  it("updates streaming data without remounting the presenter", async () => {
    let mounts = 0;
    function StatefulPresenter({ snapshot }: { snapshot: ConversationItemSnapshotV1 }): JSX.Element {
      const [instance] = useState(() => ++mounts);
      useEffect(() => undefined, []);
      return <section data-instance={instance}>{snapshot.text}:{snapshot.status}</section>;
    }
    const runtime = await setup((api) => {
      api.registerPresenter({
        id: "assistant",
        target: "conversation.item",
        key: "assistant-message",
        render: ({ snapshot }) => <StatefulPresenter snapshot={snapshot as ConversationItemSnapshotV1} />,
      });
    });

    renderItem(runtime, { id: "answer-1", type: "agent_message", status: "in_progress", text: "hel" });
    renderItem(runtime, { id: "answer-1", type: "agent_message", status: "in_progress", text: "hello" });

    expect(container.textContent).toBe("hello:streaming");
    expect(container.querySelector("section")?.dataset.instance).toBe("1");
    expect(mounts).toBe(1);
  });

  it("advertises and dispatches only the available validated edit action", async () => {
    const onEdit = vi.fn();
    let actions: readonly string[] = [];
    let invoke: ((action: string, input?: unknown) => Promise<unknown>) | undefined;
    const runtime = await setup((api) => {
      api.registerPresenter({
        id: "user",
        target: "conversation.item",
        key: "user-message",
        render: ({ host }) => {
          actions = host.actions;
          invoke = host.invoke;
          return <section data-plugin-root />;
        },
      });
    });
    renderItem(runtime, { id: "user-1", type: "user_message", status: "completed", text: "hello" }, onEdit);

    expect(actions).toEqual([CONVERSATION_ITEM_ACTIONS.edit]);
    await act(async () => { await invoke?.(CONVERSATION_ITEM_ACTIONS.edit); });
    expect(onEdit).toHaveBeenCalledOnce();
    await expect(invoke?.(CONVERSATION_ITEM_ACTIONS.edit, { text: "changed" })).rejects.toThrow(
      "does not accept input",
    );
    expect(onEdit).toHaveBeenCalledOnce();
  });

  it("falls back locally after unload and presenter render failure", async () => {
    const runtime = await setup((api) => {
      api.registerPresenter({
        id: "assistant",
        target: "conversation.item",
        key: "assistant-message",
        render: () => <section data-plugin-root />,
      });
    });
    const item: ThreadItem = { id: "answer-1", type: "agent_message", status: "completed", text: "answer" };
    renderItem(runtime, item);
    expect(container.querySelector("[data-plugin-root]")).not.toBeNull();

    act(() => runtime.host.unload("item-plugin"));
    expect(container.querySelector("[data-native-root]")).not.toBeNull();

    vi.spyOn(console, "error").mockImplementation(() => undefined);
    await act(async () => {
      await runtime.host.activateGeneration({
        pluginId: "broken-plugin",
        generation: "gen-1",
        register: (api) => {
          api.registerPresenter({
            id: "broken",
            target: "conversation.item",
            key: "assistant-message",
            render: () => { throw new Error("broken presenter"); },
          });
        },
      });
    });
    renderItem(runtime, item);
    expect(container.querySelector("[data-native-root]")).not.toBeNull();
  });
});
