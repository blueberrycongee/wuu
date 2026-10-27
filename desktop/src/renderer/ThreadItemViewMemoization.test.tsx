import { ConversationControlsTestProvider } from "./ConversationControlsTestProvider";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ThreadItem } from "../shared/protocol";

const toolActivityProbe = vi.hoisted(() => ({ renders: 0 }));

vi.mock("./ToolActivity", () => ({
  ToolActivityRow: (): JSX.Element => {
    toolActivityProbe.renders += 1;
    return <div data-testid="tool-activity" />;
  },
}));

import { ThreadItemView } from "./ThreadItemView";

let container: HTMLDivElement | undefined;
let root: Root | undefined;

afterEach(() => {
  if (root) {
    act(() => root?.unmount());
  }
  root = undefined;
  container?.remove();
  container = undefined;
  toolActivityProbe.renders = 0;
});

describe("ThreadItemView memoization", () => {
  it("does not re-render a settled item when its parent turn updates", () => {
    const item: ThreadItem = {
      id: "tool-1",
      type: "tool_call",
      name: "read_file",
      status: "completed",
    };
    const onStreamFrame = vi.fn();
    const render = (nextItem: ThreadItem): void => {
      root?.render(
        <ConversationControlsTestProvider>{<ThreadItemView
          turnID="turn-1"
          turnStatus="in_progress"
          item={nextItem}
          streaming={false}
          onStreamFrame={onStreamFrame}
        />}</ConversationControlsTestProvider>,
      );
    };

    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);

    act(() => render(item));
    expect(toolActivityProbe.renders).toBe(1);

    act(() => render(item));
    expect(toolActivityProbe.renders).toBe(1);

    act(() => render({ ...item, status: "in_progress" }));
    expect(toolActivityProbe.renders).toBe(2);
  });
});
