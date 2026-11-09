import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RichContent } from "./RichContent";
import { StreamingMarkdown } from "./StreamingMarkdown";
import { streamTextKey, streamTextStore } from "./StreamText";
import { PreviewContentProvider } from "./PreviewContentContext";
import { ApplicationPreviewContent } from "./plugins/ApplicationPreviewContent";

const markdownRender = vi.hoisted(() => ({ count: 0 }));

vi.mock("react-markdown", async () => {
  const React = await import("react");
  return {
    default: ({ children }: { children?: import("react").ReactNode }) => {
      markdownRender.count += 1;
      return React.createElement("div", { "data-mock-markdown": "true" }, children);
    }
  };
});

const longMarkdown = Array.from(
  { length: 80 },
  (_, index) => `## Section ${index + 1}

This paragraph includes **bold text**, \`inline code\`, and enough prose to
represent a long completed conversation item.

- First item
- Second item

`
).join("");

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  vi.useFakeTimers();
  markdownRender.count = 0;
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  streamTextStore.clearItem("turn", "memo");
  vi.clearAllTimers();
  vi.useRealTimers();
});

function render(element: JSX.Element): void {
  act(() => {
    if (!root) {
      root = createRoot(container);
    }
    root.render(element);
  });
}

describe("Markdown memoization", () => {
  it("keeps cached message markdown parsed across navigation with the same preview service", () => {
    const service = new ApplicationPreviewContent({
      platform: "linux", listWorkspaceDirectory: vi.fn(), readWorkspaceFile: vi.fn(),
      revealWorkspaceItem: vi.fn(), showWorkspaceItemMenu: vi.fn(), exportWorkspaceFile: vi.fn(),
    });
    function Harness({ active }: { active: number }): JSX.Element {
      return <PreviewContentProvider service={service}>
        {[0, 1].map(id => <div key={id} aria-hidden={id !== active}>
          <RichContent text={longMarkdown} cwd="/tmp/project" />
        </div>)}
      </PreviewContentProvider>;
    }
    render(<Harness active={0} />);
    const parsed = markdownRender.count;
    expect(parsed).toBe(2);
    render(<Harness active={1} />);
    render(<Harness active={0} />);
    expect(markdownRender.count).toBe(parsed);
  });

  it("does not re-render user-message markdown when only parent draft state changes", () => {
    function Harness({ draft }: { draft: string }): JSX.Element {
      return (
        <>
          <textarea readOnly value={draft} />
          <RichContent text={longMarkdown} cwd="/tmp/project" />
        </>
      );
    }

    render(<Harness draft="" />);
    expect(markdownRender.count).toBe(1);

    render(<Harness draft="typing in the composer" />);
    expect(markdownRender.count).toBe(1);
  });

  it("does not re-render settled assistant markdown when only parent draft state changes", async () => {
    const key = streamTextKey("turn", "memo", "text");

    function Harness({ draft }: { draft: string }): JSX.Element {
      return (
        <>
          <textarea readOnly value={draft} />
          <StreamingMarkdown
            streamKey={key}
            initialText={longMarkdown}
            isLive={false}
            phase="final_answer"
          />
        </>
      );
    }

    render(<Harness draft="" />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(240);
    });

    const countAfterCursorSettles = markdownRender.count;
    expect(countAfterCursorSettles).toBeGreaterThan(0);

    render(<Harness draft="typing in the composer" />);
    expect(markdownRender.count).toBe(countAfterCursorSettles);
  });
});
