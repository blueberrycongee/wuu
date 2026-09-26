/**
 * Project-bucket freshness regression test.
 *
 * Root cause under test: the sidebar's project buckets were summarized
 * straight from the sidebar thread cache, which never sees the active
 * renderer's turn lifecycle updates. The pinned and scratch buckets are
 * summarized from live state, so a click-time optimistic turn spun those
 * rows immediately while project rows — and the bell's running section,
 * which consumes the same buckets — stayed idle until the main process
 * reported the run back through runningThreadIDs one IPC round-trip later.
 */
import { describe, expect, it } from "vitest";
import type { Thread } from "../shared/protocol";
import { isThreadExecuting, summarizeWorkspaceThreadsForSidebar } from "./AppState";

function thread(id: string, overrides: Partial<Thread> = {}): Thread {
  return {
    id,
    title: id,
    preview: id,
    cwd: "/repo",
    status: "idle",
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
    ...overrides,
  } as unknown as Thread;
}

function running(id: string): Thread {
  return thread(id, {
    status: "in_progress",
    turns: [{ id: `${id}-turn`, status: "in_progress" }],
  } as Partial<Thread>);
}

describe("summarizeWorkspaceThreadsForSidebar", () => {
  it("overlays a live optimistic turn onto the cached project bucket", () => {
    const summaries = summarizeWorkspaceThreadsForSidebar(
      { "project-1": [thread("a"), thread("b")] },
      [running("b")],
    );

    expect(summaries["project-1"].map(({ id }) => id).sort()).toEqual(["a", "b"]);
    const overlaid = summaries["project-1"].find(({ id }) => id === "b");
    expect(isThreadExecuting(overlaid!)).toBe(true);
    const untouched = summaries["project-1"].find(({ id }) => id === "a");
    expect(isThreadExecuting(untouched!)).toBe(false);
  });

  it("keeps bucket membership owned by the cache", () => {
    const summaries = summarizeWorkspaceThreadsForSidebar(
      { "project-1": [thread("a")] },
      [running("not-in-any-bucket")],
    );

    expect(summaries["project-1"].map(({ id }) => id)).toEqual(["a"]);
  });

  it("still applies the cross-workdir running override", () => {
    const summaries = summarizeWorkspaceThreadsForSidebar(
      { "project-1": [thread("a")] },
      [],
      new Set(["a"]),
    );

    expect(isThreadExecuting(summaries["project-1"][0])).toBe(true);
  });
});
