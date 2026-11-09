import { test } from "node:test";
import assert from "node:assert/strict";
import { modelCapacity, modelCapacityServiceDescriptor } from "./model-capacity.js";
import type { RuntimeHost } from "./index.js";

test("capacity settlement is explicit cleanup and never retries an uncertain acquire", async () => {
  const calls: Array<{method: string; params: any; options?: {cleanup?: boolean}}> = [];
  const host = { call: async (method: string, params: any, options?: {cleanup?: boolean}) => {
    calls.push({ method, params, options });
    if (params.method === "acquire") return { lease: "owned" };
    return {};
  } } as unknown as RuntimeHost;
  const client = modelCapacity(host);
  const lease = await client.acquire({ scope: "opaque-account", profile: "interactive" });
  await client.settle({ ...lease, outcome: "abandoned" });
  assert.equal(calls.length, 2);
  assert.equal(calls[1].params.cleanup, true);
  assert.equal(calls[1].options?.cleanup, true);
  assert.equal(calls[1].params.params.lease, "owned");
  let attempts = 0;
  const failed = modelCapacity({ call: async () => { attempts++; throw new Error("uncertain"); } } as unknown as RuntimeHost);
  await assert.rejects(failed.acquire({ scope: "opaque-account" }), /uncertain/);
  assert.equal(attempts, 1);
  assert.equal(modelCapacityServiceDescriptor().methods.find(method => method.name === "settle")?.cleanup, true);
});
