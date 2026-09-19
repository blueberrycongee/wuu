import { act, createRef, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { GitStatusResult, InitializeResult } from "../shared/protocol";
import { EnvironmentPanel } from "./EnvironmentPanel";
import { unhoverTooltip } from "./tooltipTestUtils";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  unhoverTooltip();
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
});

function initialized(): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "test",
    model: "test-model",
    workspace_root: "/repo",
  };
}

function gitStatus(overrides: Partial<GitStatusResult> = {}): GitStatusResult {
  return {
    is_repo: true,
    branch: "main",
    branches: ["main"],
    dirty_count: 15,
    diff: { files: 15, additions: 163, deletions: 48 },
    ...overrides,
  };
}

function todoSection(): ReactNode {
  return (
    <div className="plugin-inspector-sections" data-wuu-component="plugin-inspector-sections">
      <section className="plugin-inspector-section" data-plugin-id="todo">
        <h2>TODO</h2>
        <div className="plugin-inspector-section-content">
          <ol className="plugin-todo-list">
            <li className="plugin-todo-item" data-status="in_progress">
              <span className="plugin-todo-marker">●</span>
              <span>对齐关闭按钮与待办标题</span>
            </li>
            <li className="plugin-todo-item" data-status="pending">
              <span className="plugin-todo-marker">○</span>
              <span>检查变更行与待办列表的列对齐</span>
            </li>
          </ol>
        </div>
      </section>
    </div>
  );
}

function renderPanel({
  status = gitStatus(),
  pullRequestDisabledReason = "先创建功能分支",
  pluginSections,
}: {
  status?: GitStatusResult;
  pullRequestDisabledReason?: string;
  pluginSections?: ReactNode;
} = {}): void {
  act(() => {
    root = createRoot(container);
    root.render(
      <EnvironmentPanel
        panelRef={createRef<HTMLDivElement>()}
        motionState="open"
        initialized={initialized()}
        gitStatus={status}
        activeMenu={null}
        running={false}
        pullRequestDisabledReason={pullRequestDisabledReason}
        pluginSections={pluginSections}
        onSetActiveMenu={() => {}}
        onClose={() => {}}
        onSelectBranch={() => {}}
        onCreateBranch={() => Promise.resolve()}
        onOpenReview={() => {}}
        onOpenCommit={() => {}}
        onOpenPullRequest={() => {}}
      />,
    );
  });
}

describe("EnvironmentPanel", () => {
  it("keeps the close control beside the first content row instead of inside it", () => {
    renderPanel();

    const panel = container.querySelector(".environment-panel");
    const close = container.querySelector(".environment-panel-close-row .icon-button");
    const rows = [...container.querySelectorAll(".environment-row")];

    expect(panel?.querySelector(".environment-panel-header.floating")).toBeNull();
    expect(close).not.toBeNull();
    expect(close?.closest(".environment-row")).toBeNull();
    expect(rows.length).toBe(4);
    for (const row of rows) {
      expect(row.querySelector(":scope > .environment-row-meta")).not.toBeNull();
      expect(row.querySelector(":scope > .environment-row-trailing")).not.toBeNull();
    }
  });

  it("puts the branch chevron in the trailing slot instead of beside the label", () => {
    renderPanel();

    const branch = [...container.querySelectorAll(".environment-row")].find((row) =>
      row.textContent?.includes("main"),
    );
    expect(branch?.querySelector(".environment-row-trailing svg")).not.toBeNull();
    expect(container.textContent).not.toContain("提交当前更改");
  });

  it("renders todo inspector copy beside the close chrome rather than under it", () => {
    renderPanel({ pluginSections: todoSection() });

    const header = container.querySelector(".environment-panel-close-row");
    const todoTitle = container.querySelector(".plugin-inspector-section > h2");

    expect(header).not.toBeNull();
    expect(header?.querySelector(".icon-button")).not.toBeNull();
    expect(todoTitle?.textContent).toBe("TODO");
    expect(header?.nextElementSibling?.classList.contains("plugin-inspector-sections")).toBe(true);
    expect(container.querySelector(".plugin-todo-item")).not.toBeNull();
    expect(container.querySelector(".environment-change-row")).not.toBeNull();
  });
});
