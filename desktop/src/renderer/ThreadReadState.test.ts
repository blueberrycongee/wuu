import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  initialState,
  isThreadUnread,
  markThreadSummariesViewed,
  markThreadTurnsViewed,
} from "./AppState";
import type { Thread } from "../shared/protocol";
import { readThreadReadState, writeThreadReadState } from "./ThreadReadState";

function thread(id: string, turnID: string): Thread {
  return {
    id,
    preview: "",
    model_provider: "fake",
    model: "fake-model",
    cwd: "/tmp",
    status: "idle",
    created_at: "2026-09-08T00:00:00Z",
    updated_at: "2026-09-08T00:00:00Z",
    latest_completed_turn_id: turnID,
    turns: [],
  };
}

beforeEach(() => window.localStorage.clear());
afterEach(() => vi.restoreAllMocks());

describe("persisted thread read state", () => {
  it("keeps viewed replies read after reload while new replies remain unread", () => {
    const viewed = thread("viewed", "turn-1");
    const unread = thread("unread", "turn-2");
    const state = markThreadTurnsViewed({
      ...initialState,
      threads: [viewed, unread],
    }, viewed.id);
    writeThreadReadState(state.lastViewedTurnByThreadID);

    const restored = { ...initialState, lastViewedTurnByThreadID: readThreadReadState() };
    expect(isThreadUnread(viewed, restored.lastViewedTurnByThreadID[viewed.id])).toBe(false);
    expect(isThreadUnread(unread, restored.lastViewedTurnByThreadID[unread.id])).toBe(true);
    expect(isThreadUnread(thread(viewed.id, "turn-3"), restored.lastViewedTurnByThreadID[viewed.id])).toBe(true);
  });

  it("preserves mark-all-read across reload without marking running turns read", () => {
    const threads = [thread("first", "turn-1"), thread("second", "turn-2")];
    const running: Thread = { ...thread("running", "turn-3"), status: "in_progress" };
    const state = markThreadSummariesViewed(initialState, [...threads, running].map(
      (item) => ({ ...item, turn_count: 1 }),
    ));
    writeThreadReadState(state.lastViewedTurnByThreadID);

    const restored = readThreadReadState();
    for (const item of threads) expect(isThreadUnread(item, restored[item.id])).toBe(false);
    expect(isThreadUnread(thread(running.id, "turn-3"), restored[running.id])).toBe(true);
  });

  it.each(["{", "null", "[]", '"invalid"'])("recovers from invalid stored data: %s", (raw) => {
    vi.spyOn(Storage.prototype, "getItem").mockReturnValue(raw);
    expect(readThreadReadState()).toEqual({});
  });

  it("discards invalid entries without losing valid read markers", () => {
    vi.spyOn(Storage.prototype, "getItem").mockReturnValue(JSON.stringify({
      valid: "turn-1", invalid: 42, empty: "", nested: {},
    }));
    expect(readThreadReadState()).toEqual({ valid: "turn-1" });
  });

  it("does not break the session when storage is unavailable", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("denied"); });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("quota"); });
    vi.spyOn(console, "warn").mockImplementation(() => {});
    expect(readThreadReadState()).toEqual({});
    expect(() => writeThreadReadState({ viewed: "turn-1" })).not.toThrow();
  });
});
