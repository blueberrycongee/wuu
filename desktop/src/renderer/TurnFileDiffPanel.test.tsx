import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TurnFileDiffPanel } from "./TurnFileDiffPanel";

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(element: JSX.Element): void {
  if (container) unmount();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(element);
  });
}

function unmount(): void {
  if (root) {
    act(() => {
      root!.unmount();
    });
    root = null;
  }
  container?.remove();
  container = null;
}

afterEach(() => {
  unmount();
});

describe("TurnFileDiffPanel", () => {
  it("renders the selected turn file diff in a right-side panel", () => {
    const onClose = vi.fn();

    mount(
      <TurnFileDiffPanel
        selection={{
          path: "/tmp/a.txt",
          additions: 1,
          deletions: 1,
          newFile: false,
          diff: {
            path: "/tmp/a.txt",
            hunks: [
              {
                oldStart: 4,
                newStart: 4,
                lines: [
                  { op: "delete", content: "old value" },
                  { op: "insert", content: "new value" },
                ],
              },
            ],
          },
        }}
        onClose={onClose}
      />,
    );

    const panel = container?.querySelector<HTMLElement>(".turn-file-diff-panel");
    expect(panel).toBeTruthy();
    expect(panel?.textContent).toContain("/tmp/a.txt");
    expect(panel?.textContent).toContain("old value");
    expect(panel?.textContent).toContain("new value");
    expect(panel?.textContent).toContain("+1");
    expect(panel?.textContent).toContain("-1");

    act(() => {
      panel
        ?.querySelector<HTMLButtonElement>(".turn-file-diff-close")
        ?.click();
    });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not render without a selected diff", () => {
    mount(<TurnFileDiffPanel selection={undefined} onClose={() => {}} />);

    expect(container?.querySelector(".turn-file-diff-panel")).toBeFalsy();
  });

  it("opens a new Markdown artifact in reading mode and shows workspace status", async () => {
    const previousWuu = window.wuu;
    window.wuu = {
      ...previousWuu,
      readWorkspaceFile: vi.fn().mockResolvedValue({
        root: "/repo",
        path: "docs/brief.md",
        absolute_path: "/repo/docs/brief.md",
        size_bytes: 16,
        mtime_ms: 1,
        sha256: "abc123",
        binary: false,
        truncated: false,
        text: "# Brief\n\nBody\n",
      }),
      readGitFileDiff: vi.fn().mockResolvedValue({
        is_repo: true,
        path: "docs/brief.md",
        status: "ignored",
        additions: 3,
        deletions: 0,
        patch: "",
        truncated: false,
      }),
    };

    mount(
      <TurnFileDiffPanel
        selection={{
          path: "docs/brief.md",
          cwd: "/repo",
          action: "create",
          additions: 3,
          deletions: 0,
          newFile: true,
          snapshotText: "# Brief\n\nBody\n",
          afterSha: "sha256:abc123",
        }}
        onClose={() => {}}
      />,
    );
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(container?.textContent).toContain("本轮产出");
    expect(container?.textContent).toContain("Brief");
    expect(container?.textContent).toContain("当前文件与本轮产出一致");
    expect(container?.textContent).toContain("Git：已忽略，不会提交");
    expect(window.wuu.readWorkspaceFile).toHaveBeenCalledWith(
      "docs/brief.md",
      "/repo",
    );
    expect(window.wuu.readGitFileDiff).toHaveBeenCalledWith(
      "docs/brief.md",
      "/repo",
    );
    expect(
      container?.querySelector('[role="tab"][aria-selected="true"]')?.textContent,
    ).toBe("阅读");

    window.wuu = previousWuu;
  });
});
