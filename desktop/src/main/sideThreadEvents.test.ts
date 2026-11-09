import { describe, expect, it } from "vitest";
import type { ServerEvent, SideThreadEvent } from "../shared/protocol";
import { sideThreadEventFromServerEvent } from "./sideThreadEvents";

const delta: SideThreadEvent = {
  type: "delta",
  side_thread_id: "side-1",
  main_thread_id: "main-1",
  revision: 1,
  message_id: "message-1",
  text_delta: "hello"
};

describe("sideThreadEventFromServerEvent", () => {
  it("extracts sideThread/event notification params", () => {
    const event: ServerEvent = {
      workdir: "/repo",
      kind: "notification",
      message: { method: "sideThread/event", params: delta }
    };
    expect(sideThreadEventFromServerEvent(event)).toEqual({
      workdir: "/repo",
      event: delta
    });
  });

  it("ignores unrelated server notifications", () => {
    const event: ServerEvent = {
      workdir: "/repo",
      kind: "notification",
      message: { method: "turn/started", params: delta }
    };
    expect(sideThreadEventFromServerEvent(event)).toBeUndefined();
  });

  it("ignores malformed side-thread notifications", () => {
    const event: ServerEvent = {
      workdir: "/repo",
      kind: "notification",
      message: {
        method: "sideThread/event",
        params: { type: "delta", side_thread_id: "side-1" }
      }
    };
    expect(sideThreadEventFromServerEvent(event)).toBeUndefined();
  });

  it("rejects legacy status events without an authoritative summary", () => {
    const event: ServerEvent = {
      workdir: "/repo",
      kind: "notification",
      message: {
        method: "sideThread/event",
        params: {
          type: "status",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          status: "running"
        }
      }
    };
    expect(sideThreadEventFromServerEvent(event)).toBeUndefined();
  });

  it("rejects delta events without a revision", () => {
    const event: ServerEvent = {
      workdir: "/repo",
      kind: "notification",
      message: {
        method: "sideThread/event",
        params: {
          type: "delta",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          message_id: "message-1",
          text_delta: "stale"
        }
      }
    };
    expect(sideThreadEventFromServerEvent(event)).toBeUndefined();
  });

  it("accepts canonical side-thread item snapshots and rejects malformed items", () => {
    const valid: ServerEvent = {
      workdir: "/repo",
      kind: "notification",
      message: {
        method: "sideThread/event",
        params: {
          type: "items",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 1,
          message_id: "message-1",
          items: [{ id: "tool-1", type: "tool_call", status: "completed", name: "read_file" }]
        }
      }
    };
    expect(sideThreadEventFromServerEvent(valid)?.event.type).toBe("items");

    const malformed: ServerEvent = {
      ...valid,
      message: {
        ...valid.message,
        params: {
          ...(valid.message.params as Record<string, unknown>),
          items: [{ id: "tool-1", type: "not-a-thread-item" }]
        }
      }
    };
    expect(sideThreadEventFromServerEvent(malformed)).toBeUndefined();
  });
});
