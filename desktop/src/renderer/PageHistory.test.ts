import { describe, expect, it } from "vitest";
import { recordPage, historyTarget } from "./PageHistory";

describe("page history", () => {
  it("updates a layout-only destination in place without truncating the forward branch", () => {
    expect(recordPage({ entries: ["a", "workspace", "c"], index: 1 }, "conversation", true))
      .toEqual({ entries: ["a", "conversation", "c"], index: 1 });
  });
  it("keeps repeated visits stable and truncates the forward branch after new navigation", () => {
    let history = { entries: ["a"], index: 0 };
    history = recordPage(history, "b");
    history = recordPage(history, "c");
    expect(recordPage(history, "c")).toBe(history);
    history = { ...history, index: 1 };
    expect(recordPage(history, "d")).toEqual({ entries: ["a", "b", "d"], index: 2 });
  });
  it("skips unavailable entries in either direction without wrapping or duplicating visits", () => {
    const history = { entries: ["a", "deleted", "b", "closed", "c"], index: 2 };
    const available = (id: string) => ["a", "b", "c"].includes(id);
    expect(historyTarget(history, -1, available)).toBe(0);
    expect(historyTarget(history, 1, available)).toBe(4);
    expect(historyTarget({ ...history, index: 0 }, -1, available)).toBeUndefined();
    expect(historyTarget({ ...history, index: 4 }, 1, available)).toBeUndefined();
  });
  it("does not turn adjacent identical surviving destinations into extra stops", () => {
    expect(historyTarget({ entries: ["a", "deleted", "a"], index: 2 }, -1, id => id === "a")).toBeUndefined();
  });
});
