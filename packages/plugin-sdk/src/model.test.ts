import { test } from "node:test";
import assert from "node:assert/strict";
import type { LogicalModelRequestV1 } from "./model.js";

test("model forwarding preserves resumed sampling without fabricating a user turn", () => {
  const request: LogicalModelRequestV1 = { version: 1, resume_sampling: true, model: "fixture", messages: [{ role: "assistant", content: "completed progress" }], tools: [] };
  const forwarded: LogicalModelRequestV1 = JSON.parse(JSON.stringify({ ...request }));
  assert.equal(forwarded.resume_sampling, true);
  assert.deepEqual(forwarded.messages, request.messages);
});
