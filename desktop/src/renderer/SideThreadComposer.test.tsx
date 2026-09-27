import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SideThreadComposer } from "./SideThreadComposer";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => root?.unmount());
  root = null;
  container.remove();
});

function renderComposer({
  draft = "",
  running = false,
  disabledReason,
  onChangeDraft = () => {},
  onSend = () => {},
  onInterrupt = () => {},
  onReset = () => {},
}: {
  draft?: string;
  running?: boolean;
  disabledReason?: string;
  onChangeDraft?: (draft: string) => void;
  onSend?: (prompt: string) => void;
  onInterrupt?: () => void;
  onReset?: () => void;
} = {}): void {
  act(() => {
    root = createRoot(container);
    root.render(
      <SideThreadComposer
        draft={draft}
        running={running}
        disabledReason={disabledReason}
        queryHistorySessionID="side-1"
        queryHistory={[]}
        onChangeDraft={onChangeDraft}
        onSend={onSend}
        onInterrupt={onInterrupt}
        onReset={onReset}
      />,
    );
  });
}

describe("SideThreadComposer", () => {
  it("sends the trimmed draft through the side transport", () => {
    const onSend = vi.fn();
    renderComposer({ draft: " status? ", onSend });

    act(() => {
      (container.querySelector('.composer-action-button[aria-label="发送"]') as HTMLButtonElement).click();
    });

    expect(onSend).toHaveBeenCalledWith("status?");
  });

  it("offers only the side /reset slash command and dispatches it", () => {
    const onReset = vi.fn();
    const onSend = vi.fn();
    renderComposer({ draft: "/reset", onReset, onSend });

    const items = Array.from(
      document.body.querySelectorAll<HTMLButtonElement>(
        '[data-floating-menu-owner="composer-slash"] .slash-command-item',
      ),
    );
    expect(items.map((item) => item.textContent)).toHaveLength(1);

    act(() => items[0]?.click());
    expect(onReset).toHaveBeenCalledTimes(1);
    expect(onSend).not.toHaveBeenCalled();
  });

  it("keeps the side composer focusable during a running turn", () => {
    const onInterrupt = vi.fn();
    renderComposer({ draft: "keep this", running: true, onInterrupt });

    const textarea = container.querySelector("textarea") as HTMLTextAreaElement;
    expect(textarea.disabled).toBe(false);
    expect(textarea.value).toBe("keep this");
    const stop = container.querySelector(
      '.composer-action-button[aria-label="暂停"]',
    ) as HTMLButtonElement;
    expect(stop).toBeTruthy();
    act(() => stop.click());
    expect(onInterrupt).toHaveBeenCalledTimes(1);
  });
});
