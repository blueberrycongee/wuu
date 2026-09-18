import { describe, expect, it } from "vitest";
import type { Thread } from "../shared/protocol";
import { userVisibleThreads } from "./SkillsAssistant";

describe("Skills assistant surface context", () => {
  it("keeps ephemeral assistant threads out of user-facing history", () => {
    const visible = thread("thread-visible");
    const ephemeral = { ...thread("ephemeral-1"), ephemeral: true };

    expect(userVisibleThreads([visible, ephemeral])).toEqual([visible]);
  });
});

function thread(id: string): Thread {
  return {
    id,
    preview: "",
    model_provider: "test",
    model: "test-model",
    cwd: "/tmp/wuu",
    status: "idle",
    created_at: "2026-07-26T00:00:00Z",
    updated_at: "2026-07-26T00:00:00Z",
    turns: [],
  };
}
