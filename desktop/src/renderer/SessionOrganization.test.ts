import { describe, expect, it } from "vitest";
import {
  clearOptimisticAssignment,
  clearReconciledAssignments,
  emptySessionOrganization,
  parseSessionOrganization,
} from "./SessionOrganization";
import type { ThreadSummary } from "./AppState";

describe("parseSessionOrganization", () => {
  it("keeps valid server folders", () => {
    expect(parseSessionOrganization({
      folders: [{ id: "folder-1", name: "Topic" }],
    })).toEqual({
      folders: [{ id: "folder-1", name: "Topic" }],
      folderByThreadID: {},
    });
  });

  it("falls back safely for malformed storage", () => {
    expect(parseSessionOrganization(null)).toEqual(emptySessionOrganization);
    expect(parseSessionOrganization({ folders: "bad" })).toEqual(emptySessionOrganization);
  });
});

describe("optimistic organization assignments", () => {
  it("clears only the completed mutation", () => {
    expect(clearOptimisticAssignment({ thread: "newer" }, "thread", "older")).toEqual({ thread: "newer" });
    expect(clearOptimisticAssignment({ thread: "current" }, "thread", "current")).toEqual({});
  });

  it("reconciles an assignment when the server snapshot catches up", () => {
    const threads = new Map<string, ThreadSummary>([["thread", {
      id: "thread",
      preview: "",
      model_provider: "test",
      model: "test",
      cwd: "/tmp",
      status: "idle",
      folder_id: "folder-1",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      turns: [],
      turn_count: 0,
    }]]);

    expect(clearReconciledAssignments({ thread: "folder-1" }, threads)).toEqual({});
    expect(clearReconciledAssignments({ thread: "folder-2" }, threads)).toEqual({ thread: "folder-2" });
  });
});
