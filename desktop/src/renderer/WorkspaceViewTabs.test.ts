import { describe, expect, it } from "vitest";
import type { RuntimeContext } from "../shared/protocol";
import type { ToolDiffPreviewFileDiff } from "./ToolDiffPreview";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import {
  closeViewTab,
  closeViewTabsWhere,
  focusViewTab,
  initialWorkspaceViewTabsState,
  openViewTab,
  reorderViewTabs,
  workspaceDiffViewTab,
  workspaceArtifactViewTab,
  workspaceDiffViewTabID,
  workspaceFileViewTab,
  workspaceFileViewTabID,
  workspaceFileBasename,
  workspaceToolViewTab,
  type WorkspaceViewTabsState,
} from "./WorkspaceViewTabs";

const projectContext: RuntimeContext = {
  kind: "project",
  project_id: "project-1",
  cwd: "/repo/project",
};

it("keeps delivered snapshots separate from editable files, deduplicates repeats, and retains new versions", () => {
  const artifact = {
    id: "delivery", itemId: "tool", index: 0, type: "file" as const,
    name: "report.html", mimeType: "text/html", placement: "turn_end" as const,
    sha256: "first", uri: "wuu-artifact://workspace/thread/first/report.html",
  };
  const input = { threadID: "thread", cwd: projectContext.cwd, artifact };
  const editable = workspaceFileViewTab({ context: projectContext, path: "report.html" });
  const first = workspaceArtifactViewTab(input);
  let state = openViewTab(openViewTab(initialWorkspaceViewTabsState, editable), first);
  state = openViewTab(state, workspaceArtifactViewTab({ ...input, artifact: { ...artifact, id: "repeat" } }));
  expect(state.tabs).toHaveLength(2);
  expect(state.activeTabID).toBe(first.id);
  const revision = workspaceArtifactViewTab({ ...input, artifact: { ...artifact, sha256: "second" } });
  state = openViewTab(state, revision);
  expect(state.tabs).toHaveLength(3);
  expect(closeViewTab(state, revision.id).activeTabID).toBe(first.id);
  state = openViewTab(state, workspaceArtifactViewTab({ ...input, threadID: "other-thread" }));
  expect(state.tabs).toHaveLength(4);
});

function makeDiff(path: string): ToolDiffPreviewFileDiff {
  return {
    path,
    hunks: [
      {
        oldStart: 1,
        newStart: 1,
        lines: [
          { op: "delete", content: "old value" },
          { op: "insert", content: "new value" },
        ],
      },
    ],
  };
}

function makeSelection(path: string, overrides: Partial<TurnFileDiffSelection> = {}): TurnFileDiffSelection {
  return {
    path,
    diff: makeDiff(path),
    additions: 1,
    deletions: 1,
    newFile: false,
    ...overrides,
  };
}

describe("workspaceDiffViewTab", () => {
  it("derives a deterministic id from threadID + path and a basename title", () => {
    const tab = workspaceDiffViewTab({
      threadID: "thread-1",
      path: "src/renderer/App.tsx",
      selection: makeSelection("src/renderer/App.tsx"),
    });
    expect(tab.id).toBe(workspaceDiffViewTabID("thread-1", "src/renderer/App.tsx"));
    expect(tab.id).toBe("diff:thread-1:src%2Frenderer%2FApp.tsx");
    expect(tab.title).toBe("App.tsx");
    expect(tab.kind).toBe("diff");
  });

  it("keeps outputs for the same file from separate turns in separate tabs", () => {
    const first = workspaceDiffViewTab({
      threadID: "thread-1",
      path: "notes/brief.md",
      selection: makeSelection("notes/brief.md", { artifactID: "item-1" }),
    });
    const second = workspaceDiffViewTab({
      threadID: "thread-1",
      path: "notes/brief.md",
      selection: makeSelection("notes/brief.md", { artifactID: "item-2" }),
    });

    expect(first.id).not.toBe(second.id);
  });
});

describe("workspaceFileViewTab", () => {
  it("derives a deterministic resource id from the runtime root and path", () => {
    const tab = workspaceFileViewTab({
      context: projectContext,
      path: "src/renderer/App.tsx",
    });

    expect(tab).toEqual({
      kind: "file",
      id: workspaceFileViewTabID(projectContext, "src/renderer/App.tsx"),
      context: projectContext,
      path: "src/renderer/App.tsx",
      title: "App.tsx",
    });
  });

  it("keeps the same relative path in separate worktrees as independent resources", () => {
    const worktreeContext: RuntimeContext = {
      ...projectContext,
      cwd: "/repo/.wuu/worktrees/feature/project",
    };

    expect(workspaceFileViewTabID(projectContext, "src/App.tsx")).not.toBe(
      workspaceFileViewTabID(worktreeContext, "src/App.tsx"),
    );
  });

  it("normalizes aliases of the same workspace file to one resource", () => {
    const relative = workspaceFileViewTab({
      context: projectContext,
      path: "src/App.tsx",
    });
    const dotted = workspaceFileViewTab({
      context: projectContext,
      path: "./src/App.tsx",
    });
    const absolute = workspaceFileViewTab({
      context: projectContext,
      path: "/repo/project/src/App.tsx",
    });

    expect(dotted).toEqual(relative);
    expect(absolute).toEqual(relative);
  });

  it("keeps file identity separate from its requested editor selection", () => {
    const first = workspaceFileViewTab({
      context: projectContext,
      path: "src/App.tsx:12:4-18:9",
    });
    const second = workspaceFileViewTab({
      context: projectContext,
      path: "src/App.tsx#L30",
    });

    expect(first.path).toBe("src/App.tsx");
    expect(first.selection).toEqual({
      startLineNumber: 12,
      startColumn: 4,
      endLineNumber: 18,
      endColumn: 9,
    });
    expect(second.selection).toEqual({ startLineNumber: 30, startColumn: 1 });
    expect(first.id).toBe(second.id);
  });

  it("keeps a cross-file Markdown anchor separate from the file path", () => {
    const tab = workspaceFileViewTab({
      context: projectContext,
      path: "README.md#install-notes",
    });

    expect(tab.path).toBe("README.md");
    expect(tab.anchor).toBe("install-notes");
  });
});

describe("workspaceFileBasename", () => {
  it("returns the trailing path segment", () => {
    expect(workspaceFileBasename("src/renderer/App.tsx")).toBe("App.tsx");
    expect(workspaceFileBasename("App.tsx")).toBe("App.tsx");
    expect(workspaceFileBasename("src/renderer/")).toBe("renderer");
  });
});

describe("openViewTab", () => {
  it("dedupes and focuses a file tab by filename resource", () => {
    const fileTab = workspaceFileViewTab({
      context: projectContext,
      path: "README.md",
    });
    let state = openViewTab(initialWorkspaceViewTabsState, fileTab);
    state = openViewTab(state, workspaceToolViewTab("terminal"));
    state = openViewTab(
      state,
      workspaceFileViewTab({ context: projectContext, path: "README.md" }),
    );

    expect(state.tabs.map((tab) => tab.id)).toEqual([fileTab.id, "terminal"]);
    expect(state.activeTabID).toBe(fileTab.id);
    expect(state.activeFileTabID).toBe(fileTab.id);
  });

  it("updates the selection when the same file is reopened", () => {
    let state = openViewTab(
      initialWorkspaceViewTabsState,
      workspaceFileViewTab({ context: projectContext, path: "src/App.tsx#L12" }),
    );
    state = openViewTab(
      state,
      workspaceFileViewTab({ context: projectContext, path: "src/App.tsx#L40" }),
    );

    expect(state.tabs).toHaveLength(1);
    const tab = state.tabs[0];
    expect(tab?.kind === "file" ? tab.selection?.startLineNumber : undefined).toBe(40);
  });

  it("keeps several file tabs open and focuses the selected file", () => {
    const first = workspaceFileViewTab({ context: projectContext, path: "src/a.ts" });
    const second = workspaceFileViewTab({ context: projectContext, path: "src/b.ts" });
    let state = openViewTab(initialWorkspaceViewTabsState, first);
    state = openViewTab(state, second);

    expect(state.tabs.map((tab) => tab.id)).toEqual([first.id, second.id]);
    expect(state.activeTabID).toBe(second.id);
    expect(state.activeFileTabID).toBe(second.id);
  });

  it("appends and focuses a new diff tab", () => {
    const state = openViewTab(
      initialWorkspaceViewTabsState,
      workspaceDiffViewTab({ threadID: "t1", path: "a.txt", selection: makeSelection("a.txt") }),
    );
    expect(state.tabs).toHaveLength(1);
    expect(state.activeTabID).toBe(workspaceDiffViewTabID("t1", "a.txt"));
  });

  it("re-opening the same thread+path does not create a second tab, but updates the selection and focuses it", () => {
    let state = openViewTab(
      initialWorkspaceViewTabsState,
      workspaceDiffViewTab({ threadID: "t1", path: "a.txt", selection: makeSelection("a.txt", { additions: 1 }) }),
    );
    // Focus something else in between, so re-opening has to re-focus.
    state = openViewTab(state, workspaceToolViewTab("files"));
    expect(state.tabs).toHaveLength(2);

    state = openViewTab(
      state,
      workspaceDiffViewTab({ threadID: "t1", path: "a.txt", selection: makeSelection("a.txt", { additions: 5 }) }),
    );

    expect(state.tabs).toHaveLength(2);
    const diffTab = state.tabs.find((tab) => tab.kind === "diff");
    expect(diffTab?.kind).toBe("diff");
    expect(diffTab && diffTab.kind === "diff" ? diffTab.selection.additions : undefined).toBe(5);
    expect(state.activeTabID).toBe(workspaceDiffViewTabID("t1", "a.txt"));
  });

  it("a different path under the same thread produces a second, independent diff tab", () => {
    let state = openViewTab(
      initialWorkspaceViewTabsState,
      workspaceDiffViewTab({ threadID: "t1", path: "a.txt", selection: makeSelection("a.txt") }),
    );
    state = openViewTab(
      state,
      workspaceDiffViewTab({ threadID: "t1", path: "b.txt", selection: makeSelection("b.txt") }),
    );

    expect(state.tabs.map((tab) => tab.id)).toEqual([
      workspaceDiffViewTabID("t1", "a.txt"),
      workspaceDiffViewTabID("t1", "b.txt"),
    ]);
    expect(state.activeTabID).toBe(workspaceDiffViewTabID("t1", "b.txt"));
  });

  it("dedupes tool tabs by kind, matching the existing single-instance tool behavior", () => {
    let state = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    state = openViewTab(state, workspaceToolViewTab("terminal"));
    state = openViewTab(state, workspaceToolViewTab("files"));

    expect(state.tabs.map((tab) => tab.id)).toEqual(["files", "terminal"]);
    expect(state.activeTabID).toBe("files");
  });
});

describe("closeViewTab", () => {
  it("restores the Files browser after closing the final file tab", () => {
    const file = workspaceFileViewTab({ context: projectContext, path: "src/App.tsx" });
    let state = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    state = openViewTab(state, file);

    expect(state.tabs.map((tab) => tab.id)).toEqual([file.id]);
    state = closeViewTab(state, file.id);

    expect(state.tabs.map((tab) => tab.id)).toEqual(["files"]);
    expect(state.activeTabID).toBe("files");
    expect(state.activeFileTabID).toBeUndefined();
  });

  it("closing the active tab restores the previously active tab", () => {
    let state = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    state = openViewTab(
      state,
      workspaceDiffViewTab({ threadID: "t1", path: "a.txt", selection: makeSelection("a.txt") }),
    );
    expect(state.activeTabID).toBe(workspaceDiffViewTabID("t1", "a.txt"));

    state = closeViewTab(state, state.activeTabID as string);

    expect(state.tabs.map((tab) => tab.id)).toEqual(["files"]);
    expect(state.activeTabID).toBe("files");
  });

  it("falls back to the tool picker (undefined) when there is no prior tab to restore", () => {
    let state = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    state = closeViewTab(state, "files");

    expect(state.tabs).toHaveLength(0);
    expect(state.activeTabID).toBeUndefined();
  });

  it("can open a background tab without stealing the active tool", () => {
    let state = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    state = openViewTab(state, workspaceToolViewTab("browser"), { activate: false });
    expect(state.tabs.map((tab) => tab.id)).toEqual(["files", "browser"]);
    expect(state.activeTabID).toBe("files");
  });

  it("closing an inactive tab leaves the active tab untouched", () => {
    let state = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    state = openViewTab(state, workspaceToolViewTab("terminal"));
    expect(state.activeTabID).toBe("terminal");

    state = closeViewTab(state, "files");

    expect(state.tabs.map((tab) => tab.id)).toEqual(["terminal"]);
    expect(state.activeTabID).toBe("terminal");
  });

  it("skips history entries that no longer exist when picking a fallback", () => {
    let state = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    state = openViewTab(state, workspaceToolViewTab("terminal"));
    state = openViewTab(state, workspaceToolViewTab("browser"));
    // History is now [files, terminal], active = browser.
    state = closeViewTab(state, "terminal"); // Remove terminal from the tab list (not active).
    expect(state.tabs.map((tab) => tab.id)).toEqual(["files", "browser"]);

    state = closeViewTab(state, "browser"); // Active tab closed; terminal is stale in history, should be skipped.
    expect(state.activeTabID).toBe("files");
  });
});

describe("closeViewTabsWhere", () => {
  it("removes every matching tab and re-focuses per the normal close fallback", () => {
    let state = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    state = openViewTab(
      state,
      workspaceDiffViewTab({ threadID: "t1", path: "a.txt", selection: makeSelection("a.txt") }),
    );
    state = openViewTab(
      state,
      workspaceDiffViewTab({ threadID: "t2", path: "b.txt", selection: makeSelection("b.txt") }),
    );
    expect(state.activeTabID).toBe(workspaceDiffViewTabID("t2", "b.txt"));

    // Simulate switching the active thread away from t1 and t2: drop every
    // diff tab whose threadID no longer matches the active thread.
    state = closeViewTabsWhere(state, (tab) => tab.kind === "diff");

    expect(state.tabs.map((tab) => tab.id)).toEqual(["files"]);
    expect(state.activeTabID).toBe("files");
  });
});

describe("focusViewTab", () => {
  it("is a no-op when the tab is already active", () => {
    const state = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    const next = focusViewTab(state, "files");
    expect(next).toBe(state);
  });

  it("records the previously active tab in history", () => {
    let state = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    state = focusViewTab(state, undefined);
    expect(state.activeTabID).toBeUndefined();
    expect(state.activationHistory).toEqual(["files"]);
  });
});

describe("reorderViewTabs", () => {
  it("moves a tab to sit next to another, mixing tool and diff tabs", () => {
    let state = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    state = openViewTab(state, workspaceToolViewTab("terminal"));
    state = openViewTab(
      state,
      workspaceDiffViewTab({ threadID: "t1", path: "a.txt", selection: makeSelection("a.txt") }),
    );
    expect(state.tabs.map((tab) => tab.id)).toEqual(["files", "terminal", workspaceDiffViewTabID("t1", "a.txt")]);

    state = reorderViewTabs(state, "terminal", "files");

    expect(state.tabs.map((tab) => tab.id)).toEqual(["terminal", "files", workspaceDiffViewTabID("t1", "a.txt")]);
  });

  it("is a no-op for unknown ids", () => {
    const state: WorkspaceViewTabsState = openViewTab(initialWorkspaceViewTabsState, workspaceToolViewTab("files"));
    const next = reorderViewTabs(state, "files", "does-not-exist");
    expect(next).toBe(state);
  });
});
