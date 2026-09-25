// Focused ProtocolClient behavior: envelope classification, pending
// rejection on close, and fail-closed server requests without a handler.

import { afterEach, describe, expect, it, vi } from "vitest";

import { ProtocolClient, ProtocolEnvelope } from "../src/rpc.js";

function makeClient(opts: ConstructorParameters<typeof ProtocolClient>[1] = {}) {
  const written: ProtocolEnvelope[] = [];
  const client = new ProtocolClient((env) => written.push(env), opts);
  return { client, written };
}

describe("ProtocolClient", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("resolves calls by id and ignores unknown responses", async () => {
    const { client, written } = makeClient();
    const p1 = client.call("initialize");
    const p2 = client.call("thread/list", { limit: 5 });
    expect(written.map((w) => w.method)).toEqual(["initialize", "thread/list"]);
    client.feed({ id: "unknown-99", result: { ignored: true } });
    client.feed({ id: written[1].id, result: { threads: [] } });
    client.feed({ id: written[0].id, result: { ok: true } });
    await expect(p1).resolves.toEqual({ ok: true });
    await expect(p2).resolves.toEqual({ threads: [] });
  });

  it("rejects on error responses", async () => {
    const { client, written } = makeClient();
    const p = client.call("turn/start");
    client.feed({ id: written[0].id, error: { code: "error", message: "no active thread" } });
    await expect(p).rejects.toThrow("no active thread");
  });

  it("fails closed on server requests without a handler", async () => {
    const { client, written } = makeClient();
    client.feed({ id: "srv-1", method: "tool/approval/request", params: {} });
    await Promise.resolve();
    expect(written).toHaveLength(1);
    expect(written[0].id).toBe("srv-1");
    expect(written[0].error?.code).toBe("unhandled");
  });

  it("answers server requests via the handler, including handler failures", async () => {
    const { client, written } = makeClient({
      onServerRequest: (req) => {
        if (req.method === "boom") throw new Error("handler exploded");
        return { result: { decision: "denied" } };
      },
    });
    client.feed({ id: 1, method: "tool/approval/request" });
    client.feed({ id: 2, method: "boom" });
    await new Promise((r) => setTimeout(r, 0));
    expect(written.find((w) => w.id === 1)?.result).toEqual({ decision: "denied" });
    expect(written.find((w) => w.id === 2)?.error?.code).toBe("handler_error");
  });

  it("rejects all pending calls on close and refuses new ones", async () => {
    const { client } = makeClient();
    const p = client.call("thread/resume");
    client.close("superseded");
    await expect(p).rejects.toThrow(/closed before thread\/resume/);
    await expect(client.call("initialize")).rejects.toThrow(/superseded/);
    expect(client.isClosed()).toBe(true);
  });

  it("propagates write failures as call rejections", async () => {
    const client = new ProtocolClient(() => {
      throw new Error("not attached to host");
    });
    await expect(client.call("turn/start")).rejects.toThrow(/not attached/);
  });

  it("expires unanswered calls, removes them, and ignores late responses", async () => {
    vi.useFakeTimers();
    const { client, written } = makeClient();
    const timedOut = client.call("thread/list", undefined, 20);
    const rejected = expect(timedOut).rejects.toThrow("rpc timeout: thread/list");

    await vi.advanceTimersByTimeAsync(20);
    await rejected;

    client.feed({ id: written[0].id, result: { stale: true } });
    const next = client.call("participant/list", undefined, 20);
    client.feed({ id: written[1].id, result: { participants: [] } });
    await expect(next).resolves.toEqual({ participants: [] });
    await vi.advanceTimersByTimeAsync(20);
  });
});


it("carries workspace identity independently of request parameters and notification contents", async () => {
  const seen = vi.fn();
  const { client, written } = makeClient({ onNotification: seen });
  const pending = client.call("turn/interrupt", { thread_id: "t" }, 1000, "/alpha");
  expect(written[0].workdir).toBe("/alpha");
  expect(written[0].params).toEqual({ thread_id: "t" });
  client.feed({ id: written[0].id, result: {} });
  await pending;
  client.feed({ method: "turn/completed", params: { thread_id: "t" }, workdir: "/alpha" });
  expect(seen).toHaveBeenCalledWith("turn/completed", { thread_id: "t" }, "/alpha");
});
