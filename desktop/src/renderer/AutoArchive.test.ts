import { describe, expect, it } from "vitest";
import type { Thread } from "../shared/protocol";
import { autoArchiveCandidates } from "./AutoArchive";

const now = Date.parse("2026-11-08T00:00:00Z");
const daysAgo = (days: number) => new Date(now - days * 86_400_000).toISOString();

function thread(id: string, overrides: Partial<Thread> = {}): Thread {
  return { id, preview: "", model_provider: "", model: "", cwd: "/w", status: "idle",
    created_at: daysAgo(60), updated_at: daysAgo(31), turns: [], ...overrides } as Thread;
}

describe("autoArchiveCandidates", () => {
  it("archives nothing while the setting is off", () => {
    expect(autoArchiveCandidates([thread("old")], 0, now, new Set())).toEqual([]);
  });

  it("selects only closed top-level conversations idle longer than the window", () => {
    const threads = [
      thread("idle"),
      thread("recent", { updated_at: daysAgo(29) }),
      thread("pinned", { pinned: true }),
      thread("running", { status: "in_progress" }),
      thread("archived", { archived: true }),
      thread("ephemeral", { ephemeral: true }),
      thread("child", { parent_id: "idle" }),
      thread("unknown-activity", { updated_at: "not a date" }),
      thread("open"),
    ];
    expect(autoArchiveCandidates(threads, 30, now, new Set(["open"])).map(item => item.id)).toEqual(["idle"]);
  });
});
