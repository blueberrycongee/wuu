import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ThreadItem } from "../../shared/protocol";
import type { ConversationProcessSnapshotV1 } from "../../shared/workbench";
import {
  ConversationProcessPresentation,
  toConversationProcessSnapshot,
} from "./ConversationProcessPresentation";
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
  register?: Parameters<PluginHost["activateGeneration"]>[0]["register"],
): Promise<{ host: PluginHost; controller: WorkbenchController }> {
  const host = new PluginHost({ react: await import("react") });
  if (register) {
    await host.activateGeneration({ pluginId: "process-plugin", generation: "gen-1", register });
  }
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  return { host, controller: new WorkbenchController(host) };
}

function renderProcess(
  runtime: { host: PluginHost; controller: WorkbenchController },
  processItems: readonly ThreadItem[],
  streaming = false,
): void {
  act(() => root.render(
    <ConversationProcessPresentation
      {...runtime}
      processItems={processItems}
      streaming={streaming}
      fallback={<div data-native-root>native</div>}
    />,
  ));
}

const reasoning: ThreadItem = {
  id: "reason-1",
  source_id: "private-source",
  type: "reasoning",
  status: "in_progress",
  text: "Checking files",
};

const tool: ThreadItem = {
  id: "tool-1",
  type: "tool_call",
  status: "completed",
  name: "read_file",
  arguments: "{\"path\":\"secret.ts\"}",
  result: "private result",
  display: { capability: "workspace.read", kind: "read" },
};

describe("ConversationProcessPresentation", () => {
  it("retains native notices that the v1 replacement snapshot cannot represent", async () => {
    const runtime = await setup((api) => {
      api.registerPresenter({
        id: "tools", target: "conversation.process", key: "tool-group",
        render: () => <span data-plugin-root>tools only</span>,
      });
    });
    const compaction: ThreadItem = {
      id: "compact-1",
      type: "context_compaction",
      status: "failed",
      text: "Context compaction failed; continuing without compacting history.",
    };

    renderProcess(runtime, [tool]);
    expect(container.querySelector("[data-plugin-root]")).not.toBeNull();

    renderProcess(runtime, [tool, compaction]);
    expect(container.querySelector("[data-native-root]")).not.toBeNull();
    expect(container.querySelector("[data-plugin-root]")).toBeNull();
  });

  it("matches the exact stable shape key", async () => {
    const runtime = await setup((api) => {
      api.registerPresenter({ id: "reasoning", target: "conversation.process", key: "reasoning", render: () => <span>reasoning</span> });
      api.registerPresenter({ id: "tools", target: "conversation.process", key: "tool-group", render: () => <span>tools</span> });
      api.registerPresenter({ id: "mixed", target: "conversation.process", key: "mixed", render: () => <span>mixed</span> });
    });

    renderProcess(runtime, [reasoning]);
    expect(container.textContent).toBe("reasoning");
    renderProcess(runtime, [tool]);
    expect(container.textContent).toBe("tools");
    renderProcess(runtime, [reasoning, tool]);
    expect(container.textContent).toBe("mixed");
  });

  it("publishes deeply frozen semantic data without private thread or protocol fields", () => {
    const snapshot = toConversationProcessSnapshot([reasoning, tool], true, true);

    expect(snapshot).toEqual({
      contractVersion: 1,
      kind: "mixed",
      status: "running",
      streaming: true,
      active: true,
      items: [
        { id: "reason-1", kind: "reasoning", status: "running", text: "Checking files", error: undefined },
        {
          id: "tool-1",
          kind: "tool-activity",
          status: "completed",
          toolName: "read_file",
          capability: "workspace.read",
          toolKind: "read",
          error: undefined,
        },
      ],
    });
    expect(Object.isFrozen(snapshot)).toBe(true);
    expect(Object.isFrozen(snapshot?.items)).toBe(true);
    expect(snapshot?.items.every(Object.isFrozen)).toBe(true);
    expect(JSON.stringify(snapshot)).not.toContain("private-source");
    expect(JSON.stringify(snapshot)).not.toContain("secret.ts");
    expect(JSON.stringify(snapshot)).not.toContain("private result");
  });

  it("replaces the complete fallback and composes wrappers around the replacement", async () => {
    const runtime = await setup((api) => {
      api.registerPresenter({
        id: "replacement",
        target: "conversation.process",
        key: "mixed",
        render: ({ snapshot }) => <strong data-replacement>{(snapshot as ConversationProcessSnapshotV1).items.length}</strong>,
      });
      api.registerPresenter({
        id: "wrapper",
        target: "conversation.process",
        key: "mixed",
        mode: "wrap",
        render: ({ fallback }) => <section data-wrapper>{fallback}</section>,
      });
    });

    renderProcess(runtime, [reasoning, tool], true);
    expect(container.querySelector("[data-wrapper] [data-replacement]")?.textContent).toBe("2");
    expect(container.querySelector("[data-native-root]")).toBeNull();
  });

  it("falls back for no match, render failure, and unload", async () => {
    const runtime = await setup((api) => {
      api.registerPresenter({ id: "tools", target: "conversation.process", key: "tool-group", render: () => <span data-plugin-root /> });
    });
    renderProcess(runtime, [reasoning]);
    expect(container.querySelector("[data-native-root]")).not.toBeNull();
    renderProcess(runtime, [tool]);
    expect(container.querySelector("[data-plugin-root]")).not.toBeNull();

    act(() => runtime.host.unload("process-plugin"));
    expect(container.querySelector("[data-native-root]")).not.toBeNull();

    vi.spyOn(console, "error").mockImplementation(() => undefined);
    await act(async () => runtime.host.activateGeneration({
      pluginId: "broken-process-plugin",
      generation: "gen-1",
      register(api) {
        api.registerPresenter({
          id: "broken",
          target: "conversation.process",
          key: "tool-group",
          render: () => { throw new Error("broken process presenter"); },
        });
      },
    }));
    renderProcess(runtime, [tool]);
    expect(container.querySelector("[data-native-root]")).not.toBeNull();
  });

  it("keeps the native root identity when one tool grows into a mixed process", async () => {
    const runtime = await setup();
    renderProcess(runtime, [tool], true);
    const nativeBefore = container.querySelector("[data-native-root]");

    renderProcess(runtime, [tool, reasoning], true);

    expect(container.querySelector("[data-native-root]")).toBe(nativeBefore);
  });
});
