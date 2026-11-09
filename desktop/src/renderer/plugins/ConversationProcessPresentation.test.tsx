import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ThreadItem } from "../../shared/protocol";
import { ConversationControlsTestProvider } from "../ConversationControlsTestProvider";
import { ConversationTurnIdentityProvider } from "../ConversationControlsContext";
import { ConversationProcessPresentation } from "./ConversationProcessPresentation";
import { toConversationProcessSnapshot } from "./ApplicationSnapshots";
let root: Root;
let container: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); container?.remove(); });
const reasoning: ThreadItem = { id: "reason-1", source_id: "private-source", type: "reasoning", status: "in_progress", text: "Checking files" };
const tool: ThreadItem = { id: "tool-1", type: "tool_call", status: "completed", name: "read_file", arguments: '{"path":"secret.ts"}', result: "private result", display: { capability: "workspace.read", kind: "read" } };
describe("ConversationProcessPresentation", () => {
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

  it("keeps the fallback DOM while addressing an expanded process through the same public slot", () => {
    container = document.createElement("div"); root = createRoot(container);
    const Slot = vi.fn(({ fallback }: { fallback?: import("react").ReactNode }) => <>{fallback}</>);
    const render = (items: ThreadItem[]) => act(() => root.render(<ConversationControlsTestProvider composition={{ Slot }}><ConversationTurnIdentityProvider threadId="thread" turnId="turn"><ConversationProcessPresentation processItems={items} streaming={false} active={false} fallback={<div data-native-root />} /></ConversationTurnIdentityProvider></ConversationControlsTestProvider>));
    render([tool]); const before = container.querySelector("[data-native-root]");
    render([tool, reasoning]);
    expect(container.querySelector("[data-native-root]")).toBe(before);
    expect(Slot.mock.calls.at(-1)?.[0]).toMatchObject({ name: "wuu.default-conversation/conversation.process", context: { threadId: "thread", turnId: "turn", itemIds: [tool.id, reasoning.id], snapshot: { kind: "mixed", active: false, streaming: false } } });
  });
});
