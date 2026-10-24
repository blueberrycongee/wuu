import { describe, expect, it, vi } from "vitest";
import { ApplicationPreview } from "./ApplicationPreview";

// Public contracts: normalized resource identity, MRU focus restoration,
// immutable caller data, and history-owned artifact provenance.
describe("application preview ownership", () => {
  it("deduplicates normalized files and restores the most recently used surviving tab", () => {
    const preview = new ApplicationPreview({ getThreads: () => [], changed: vi.fn() });
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    preview.invoke("preview.open-file", { context, path: "/workspace/a.md" });
    const first = preview.getSnapshot().activeTabId!;
    preview.invoke("preview.open-file", { context, path: "a.md" });
    expect(preview.getSnapshot().tabs).toHaveLength(1);
    preview.invoke("preview.open-file", { context, path: "b.md" });
    preview.invoke("preview.open-tool", { toolId: "terminal" });
    preview.invoke("preview.focus-tab", { tabId: first });
    preview.invoke("preview.close-tab", { tabId: first });
    expect(preview.getSnapshot().activeTabId).toBe("terminal");
  });

  it("keeps the active picker when prewarming an inactive tool", () => {
    const preview = new ApplicationPreview({ getThreads: () => [], changed: vi.fn() });
    const { tabId } = preview.invoke("preview.new-tab", {});
    preview.invoke("preview.open-tool", { toolId: "terminal", activate: false });
    expect(preview.getSnapshot().activeTabId).toBe(tabId);
    expect(preview.getSnapshot().tabs.map(tab => tab.id)).toEqual([tabId, "terminal"]);
  });

  it("copies nested diff data and rejects artifacts absent from owned history", () => {
    const preview = new ApplicationPreview({ getThreads: () => [], changed: vi.fn() });
    const selection = { path: "a.md", additions: 1, deletions: 0, newFile: false,
      diff: { hunks: [{ oldStart: 1, newStart: 1, lines: [{ op: "insert" as const, content: "original" }] }] } };
    preview.invoke("preview.open-diff", { threadId: "thread", path: "a.md", selection });
    selection.diff.hunks[0].lines[0].content = "mutated";
    const tab = preview.getSnapshot().tabs[0];
    expect(tab.kind === "diff" && tab.selection.diff?.hunks[0].lines[0].content).toBe("original");
    expect(tab.kind === "diff" && Object.isFrozen(tab.selection.diff?.hunks[0].lines)).toBe(true);
    expect(() => preview.invoke("preview.open-artifact", { threadId: "thread", artifactId: "missing" })).toThrow("Artifact is unavailable");
  });
});
