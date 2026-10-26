// Independent wire fixture: do not import any SDK or fill in omitted versions.
import { createInterface } from "node:readline";
for await (const line of createInterface({ input: process.stdin })) {
  const request = JSON.parse(line);
  let result = null;
  if (request.method === "initialize") {
    result = { lifecycle_version: Number(process.env.FIXTURE_LIFECYCLE ?? 2) };
    if (process.env.FIXTURE_PROTOCOL !== "missing") result.protocol_version = Number(process.env.FIXTURE_PROTOCOL);
    if (process.env.FIXTURE_SETTINGS_MAJOR) {
      result.provided_services = [{ name: "host.settings.get", version: `${process.env.FIXTURE_SETTINGS_MAJOR}.0.0`, methods: [{ name: "call", input_schema: "host.settings.get.input.v2", output_schema: "host.settings.get.output.v2", preparation: true }] }];
    }
  }
  if (request.method === "service.invoke") throw new Error("incompatible selected provider must never be invoked");
  process.stdout.write(JSON.stringify({ id: request.id, result }) + "\n");
  if (request.method === "shutdown") break;
}
