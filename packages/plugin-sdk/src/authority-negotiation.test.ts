import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";

// New SDK provider / old host offer is the dangerous direction: the old host
// understands v3 and export v2, but would journal locally after ignoring the
// additional authority method. Exercise a real isolated SDK process, not an
// invented required service that the old host might also ignore.
const sdk = new URL("./index.js", import.meta.url).href;
const program = `
import { documentStoreDescriptor, runJSONLRuntime } from ${JSON.stringify(sdk)};
const definition = JSON.parse(process.env.DEFINITION);
await runJSONLRuntime({
  initialize() {
    return { protocol_version: 3,
      provided_services: [definition.raw ? {
        name: "state.documents", version: "1.0.0", methods: [{ name: "authority", input_schema: "state.documents.authority.input.v1", output_schema: "state.documents.authority.output.v1", preparation: true }],
      } : documentStoreDescriptor({ authority: definition.authority, paged_export: true })],
      ...(definition.required === undefined ? {} : { required_host_features: definition.required }),
    };
  },
  activate() { process.stderr.write("ACTIVATED\\n"); },
}, { input: process.stdin, output: process.stdout });
`;
const feature = "state.documents.authority.v1";
async function initialize(definition: object, supported?: unknown) {
  const child = spawn(process.execPath, ["--input-type=module", "-e", program], {
    env: { ...process.env, DEFINITION: JSON.stringify(definition) }, stdio: ["pipe", "pipe", "pipe"],
  });
  const exited = once(child, "exit");
  const timer = setTimeout(() => child.kill(), 15000);
  let stderr = "";
  child.stderr.on("data", data => { stderr += data; });
  child.stdout.setEncoding("utf8");
  const responses = child.stdout[Symbol.asyncIterator]();
  let buffered = "";
  async function request(method: string, params?: unknown) {
    child.stdin.write(JSON.stringify({ id: method, method, params }) + "\n");
    while (!buffered.includes("\n")) {
      const chunk = await responses.next();
      assert.equal(chunk.done, false, stderr);
      buffered += chunk.value;
    }
    const end = buffered.indexOf("\n");
    const value = JSON.parse(buffered.slice(0, end));
    buffered = buffered.slice(end + 1);
    assert.equal(value.id, method);
    return value;
  }
  try {
    // These are the initialize fields actually emitted by host 00c2811.
    const response = await request("initialize", {
      protocol_version: 1, capability_protocol_version: 3, lifecycle_version: 2,
      plugin_id: "external.authority", plugin_root: ".", project_root: ".", wuu_home: ".",
      supported_host_services: ["host.service.call"],
      ...(supported === undefined ? {} : { supported_host_features: supported }),
    });
    assert.equal(stderr.includes("ACTIVATED"), false, "negotiation activated the provider");
    if (!response.error) assert.equal((await request("activate")).error, undefined);
    await request("shutdown");
    child.stdin.end();
    assert.equal((await exited)[0], 0, stderr);
    return response;
  } finally { clearTimeout(timer); child.kill(); await exited; }
}

for (const supported of [undefined, [], ["state.documents.authority.v2"], ["unrelated.future.v1"]]) {
  for (const definition of [{ authority: true }, { raw: true }]) {
    const response = await initialize(definition, supported);
    assert.equal(response.error?.code, "host_feature_unavailable");
    assert.match(response.error.message, /state\.documents\.authority\.v1/);
    assert.equal(response.result, undefined, "old host could admit a provider-owned authority");
  }
}
const admitted = await initialize({ authority: true }, ["unrelated.future.v1", feature]);
assert.equal(admitted.error, undefined);
assert.deepEqual(admitted.result.required_host_features, [feature]);
assert.equal((await initialize({ authority: false })).error, undefined, "legacy providers must remain compatible");
assert.equal((await initialize({ authority: false, required: ["unknown.feature.v1"] }, [feature])).error?.code, "host_feature_unavailable");
assert.equal((await initialize({ authority: true }, null)).error?.code, "invalid_host_features");
assert.equal((await initialize({ authority: false, required: [42] }, [feature])).error?.code, "invalid_host_features");
console.log("SDK process host-feature negotiation: old/missing/unsupported offers rejected before authority admission; exact offer and legacy providers accepted");
