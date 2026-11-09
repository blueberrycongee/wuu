import { afterEach, describe, expect, it } from "vitest";

import { RendererServerEventBatcher } from "../main/rendererServerEventBatcher";
import type { ServerEvent, Thread } from "../shared/protocol";
import {
  handleStreamingNotification,
  initialState,
  requireThread,
} from "./AppState";
import { streamTextKey, streamTextStore } from "./StreamText";

const turnID = "turn-snapshot-ordering";
const itemID = "agent-snapshot-ordering";
const answer = "Committed once.";

function runningThread(): Thread {
  return {
    id: "thread-snapshot-ordering",
    title: "Snapshot ordering",
    preview: "",
    status: "in_progress",
    model_provider: "test",
    model: "test-model",
    cwd: "/repo",
    created_at: "2026-08-28T00:00:00Z",
    updated_at: "2026-08-28T00:00:00Z",
    turns: [
      {
        id: turnID,
        items_view: "full",
        status: "in_progress",
        items: [
          {
            id: itemID,
            type: "agent_message",
            status: "in_progress",
            text: answer,
          },
        ],
      },
    ],
  };
}

describe("stream snapshot ordering", () => {
  afterEach(() => {
    streamTextStore.clearItem(turnID, itemID);
  });

  it("flushes a delta before returning a snapshot that already contains it", async () => {
    const thread = runningThread();
    const state = {
      ...initialState,
      activeContext: { kind: "no_project" as const, cwd: "/repo" },
      thread,
      threads: [thread],
    };
    const delta: ServerEvent = {
      kind: "notification",
      workdir: "/repo",
      message: {
        method: "item/agentMessage/delta",
        params: {
          thread_id: thread.id,
          turn_id: turnID,
          item_id: itemID,
          delta: answer,
        },
      },
    };
    const batcher = new RendererServerEventBatcher((event) => {
      handleStreamingNotification(event, state);
    });

    batcher.enqueue(delta);
    const resumed = await batcher.resolveSnapshot(Promise.resolve({ thread }));
    requireThread(resumed, "missing resumed thread");

    expect(streamTextStore.get(streamTextKey(turnID, itemID, "text"))).toBe(answer);
  });
});
