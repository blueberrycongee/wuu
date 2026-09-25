import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ArchiveTip } from "./ArchiveTip";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  vi.useFakeTimers();
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  vi.useRealTimers();
});

function renderTip(
  props: Partial<React.ComponentProps<typeof ArchiveTip>> = {},
): {
  onViewArchive: Mock;
  onDismiss: Mock;
} {
  const onViewArchive = (props.onViewArchive ?? vi.fn()) as Mock;
  const onDismiss = (props.onDismiss ?? vi.fn()) as Mock;
  act(() => {
    root = createRoot(container);
    root!.render(
      <ArchiveTip
        threadTitle={props.threadTitle ?? "会话标题"}
        errorMessage={props.errorMessage}
        onViewArchive={onViewArchive}
        onDismiss={onDismiss}
      />,
    );
  });
  return { onViewArchive, onDismiss };
}

describe("ArchiveTip", () => {
  it("renders the archived session title and the jump-to-archive action", () => {
    renderTip({ threadTitle: "迁移脚本调试" });

    const tip = container.querySelector(".archive-tip");
    expect(tip).not.toBeNull();
    expect(tip?.getAttribute("role")).toBe("status");
    expect(tip?.querySelector("strong")?.textContent).toBe("迁移脚本调试");
  });

  it("falls back to a generic label when the title is empty or whitespace", () => {
    renderTip({ threadTitle: "   " });

    const tip = container.querySelector(".archive-tip");
    // 强标签不应被渲染，提示只显示通用文案，避免出现空白标题
    expect(tip?.querySelector("strong")).toBeNull();
    expect(tip?.textContent).toContain("会话已归档");
  });

  it("shows an error instead of archive success when the mutation fails", () => {
    renderTip({ errorMessage: "会话仍在运行，结束后再归档" });

    const tip = container.querySelector(".archive-tip");
    expect(tip?.classList.contains("is-error")).toBe(true);
    expect(tip?.getAttribute("role")).toBe("alert");
    expect(tip?.textContent).toContain("会话仍在运行，结束后再归档");
    expect(tip?.textContent).not.toContain("已归档");
    expect(tip?.querySelector(".archive-tip-action")).toBeNull();
  });

  it("invokes onViewArchive when the jump-to-archive link is clicked", () => {
    const onViewArchive = vi.fn();
    renderTip({ onViewArchive });

    const action = container.querySelector<HTMLButtonElement>(
      ".archive-tip-action",
    );
    expect(action).not.toBeNull();
    act(() => {
      action?.click();
    });

    expect(onViewArchive).toHaveBeenCalledTimes(1);
  });

  it("invokes onDismiss after the dismiss button click settles", () => {
    const onDismiss = vi.fn();
    renderTip({ onDismiss });

    const dismiss = container.querySelector<HTMLButtonElement>(
      ".archive-tip-dismiss",
    );
    expect(dismiss).not.toBeNull();
    act(() => {
      dismiss?.click();
    });

    // dismiss 按钮点击后会先标记 leaving，200ms 后再触发回调
    expect(container.querySelector(".archive-tip")?.classList.contains("leaving")).toBe(true);
    expect(onDismiss).not.toHaveBeenCalled();

    act(() => {
      vi.advanceTimersByTime(250);
    });
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("auto-dismisses after the timeout elapses", () => {
    const onDismiss = vi.fn();
    renderTip({ onDismiss });

    expect(onDismiss).not.toHaveBeenCalled();
    // ARCHIVE_TIP_AUTO_DISMISS_MS 默认 6000；中途标记 leaving，再交还控件
    act(() => {
      vi.advanceTimersByTime(5600);
    });
    expect(container.querySelector(".archive-tip")?.classList.contains("leaving")).toBe(true);
    expect(onDismiss).not.toHaveBeenCalled();

    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("clears timers when the component unmounts mid-flight", () => {
    const onDismiss = vi.fn();
    renderTip({ onDismiss });

    // 卸载后再推进定时器，回调不应再触发（避免 setState-on-unmounted 警告）
    act(() => {
      root?.unmount();
    });
    root = null;
    act(() => {
      vi.advanceTimersByTime(10_000);
    });
    expect(onDismiss).not.toHaveBeenCalled();
  });
});
