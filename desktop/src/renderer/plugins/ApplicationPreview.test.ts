import { describe, expect, it, vi } from "vitest";
import { ApplicationPreview } from "./ApplicationPreview";

// Public contracts: normalized resource identity, MRU focus restoration,
// immutable caller data, and history-owned artifact provenance.
describe("application preview ownership", () => {
  it("deduplicates normalized files and restores the most recently used surviving tab", async () => {
    const preview = new ApplicationPreview({ getThreads: () => [], changed: vi.fn() });
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    await preview.invoke("preview.open-file", { context, path: "/workspace/a.md" });
    const first = preview.getSnapshot().activeTabId!;
    await preview.invoke("preview.open-file", { context, path: "a.md" });
    expect(preview.getSnapshot().tabs).toHaveLength(1);
    await preview.invoke("preview.open-file", { context, path: "b.md" });
    await preview.invoke("preview.open-tool", { toolId: "terminal" });
    await preview.invoke("preview.focus-tab", { tabId: first });
    await preview.invoke("preview.close-tab", { tabId: first });
    expect(preview.getSnapshot().activeTabId).toBe("terminal");
  });

  it("keeps the active picker when prewarming an inactive tool", async () => {
    const preview = new ApplicationPreview({ getThreads: () => [], changed: vi.fn() });
    const { tabId } = await preview.invoke("preview.new-tab", {});
    await preview.invoke("preview.open-tool", { toolId: "terminal", activate: false });
    expect(preview.getSnapshot().activeTabId).toBe(tabId);
    expect(preview.getSnapshot().tabs.map(tab => tab.id)).toEqual([tabId, "terminal"]);
  });

  it("reopens a cited PDF from its verified immutable URI when history is absent", async () => {
    const digest = "b".repeat(64);
    const uri = `cloud:opaque-pdf?version=${digest}`;
    const read = vi.fn().mockResolvedValue(new Response("%", { status: 206, headers: { "Content-Type": "application/pdf" } }));
    vi.stubGlobal("fetch", read);
    try {
      const acquireSource = vi.fn(async ({threadId}: {threadId?:string}) => {if(threadId!=="thread") throw new Error("Artifact is unavailable"); return {url:"wuu-artifact://lease/test",leaseId:"test"};});
      const releaseSource = vi.fn();
      const preview = new ApplicationPreview({ getThreads: () => [], changed: vi.fn(), artifacts: () => ({acquireSource, releaseSource}) as unknown as import("../../../../packages/plugin-sdk/src/ui-artifacts").ApplicationArtifactsV1 });
      await preview.invoke("preview.open-artifact", { threadId: "thread", artifactId: uri, sha256: digest, page: 3 });
      expect(preview.getSnapshot().tabs[0]).toMatchObject({ kind: "artifact", page: 3, artifact: { uri, sha256: digest } });
      await expect(preview.invoke("preview.open-artifact", { threadId: "other", artifactId: uri, sha256: digest })).rejects.toThrow("Artifact is unavailable");
      read.mockResolvedValueOnce(new Response("Not found", { status: 404 }));
      await expect(preview.invoke("preview.open-artifact", { threadId: "thread", artifactId: uri, sha256: digest })).rejects.toThrow("Artifact is unavailable");
    } finally { vi.unstubAllGlobals(); }
  });

  it("copies nested diff data and rejects artifacts absent from owned history", async () => {
    const preview = new ApplicationPreview({ getThreads: () => [], changed: vi.fn() });
    const selection = { path: "a.md", additions: 1, deletions: 0, newFile: false,
      diff: { hunks: [{ oldStart: 1, newStart: 1, lines: [{ op: "insert" as const, content: "original" }] }] } };
    await preview.invoke("preview.open-diff", { threadId: "thread", path: "a.md", selection });
    selection.diff.hunks[0].lines[0].content = "mutated";
    const tab = preview.getSnapshot().tabs[0];
    expect(tab.kind === "diff" && tab.selection.diff?.hunks[0].lines[0].content).toBe("original");
    expect(tab.kind === "diff" && Object.isFrozen(tab.selection.diff?.hunks[0].lines)).toBe(true);
    await expect(preview.invoke("preview.open-artifact", { threadId: "thread", artifactId: "missing" })).rejects.toThrow("Artifact is unavailable");
  });
});
