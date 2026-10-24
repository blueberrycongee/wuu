import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ThreadItem } from "../../shared/protocol";
import { ConversationControlsTestProvider } from "../ConversationControlsTestProvider";
import { ConversationTurnIdentityProvider } from "../ConversationControlsContext";
import { ToolActivityPresenter, toToolActivitySnapshot } from "./ToolActivityPresenter";

let root: Root | undefined;
let container: HTMLDivElement | undefined;

afterEach(() => {
  act(() => root?.unmount());
  container?.remove();
  root = undefined;
  container = undefined;
});

describe("ToolActivityPresenter", () => {
  it("adapts only public fields, preserves raw arguments, and deeply freezes structured data", () => {
    const item = toolItem({
      seq: 9,
      source_id: "private-source",
      arguments: '{"unfinished":',
      result: "done",
      result_detail: {
        content: [{ type: "text", text: "ok", resource: { nested: [1] } }],
        structured_content: { answer: [42] },
        meta: { trace: "public-result-metadata" },
        activity: { id: "job", kind: "process", thread_id: "thread" },
      },
    });
    const snapshot = toToolActivitySnapshot(item);

    expect(snapshot).toEqual({
      contractVersion: 1,
      id: "call",
      toolName: "echo",
      capability: "tool.echo",
      kind: "test",
      status: "running",
      argumentsText: '{"unfinished":',
      resultText: "done",
      structuredResult: {
        content: [{ type: "text", text: "ok", resource: { nested: [1] } }],
        structuredContent: { answer: [42] },
        metadata: { trace: "public-result-metadata" },
        activity: { id: "job", kind: "process", threadId: "thread" },
      },
    });
    expect("seq" in snapshot).toBe(false);
    expect("source_id" in snapshot).toBe(false);
    expect(Object.isFrozen(snapshot)).toBe(true);
    expect(Object.isFrozen(snapshot.structuredResult?.structuredContent)).toBe(true);
    expect(Object.isFrozen((snapshot.structuredResult?.content?.[0].resource as { nested: number[] }).nested)).toBe(true);
  });

  it("resolves tool presentation through public identity-only composition", () => {
    container = document.createElement("div"); root = createRoot(container);
    const Slot = vi.fn(({ context }: { context?: Readonly<Record<string, unknown>> }) => <section data-public>{String(context?.itemId)}</section>);
    act(() => root!.render(<ConversationControlsTestProvider composition={{ Slot }}><ConversationTurnIdentityProvider threadId="thread" turnId="turn"><ToolActivityPresenter item={toolItem()} fallback={<span>default</span>} /></ConversationTurnIdentityProvider></ConversationControlsTestProvider>));
    expect(container.querySelector("[data-public]")?.textContent).toBe("call");
    expect(Slot.mock.calls[0][0]).toMatchObject({ name: "wuu.default-conversation/conversation.tool-activity", context: { threadId: "thread", turnId: "turn", itemId: "call" } });
  });
});

function toolItem(overrides: Partial<ThreadItem> = {}): ThreadItem {
  return {
    id: "call",
    type: "tool_call",
    status: "in_progress",
    name: "echo",
    arguments: '{"value":1}',
    display: { capability: "tool.echo", kind: "test" },
    ...overrides,
  };
}
