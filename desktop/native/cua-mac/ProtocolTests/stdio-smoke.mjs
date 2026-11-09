import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const child = spawn("swift", ["run", "--quiet", "wuu-cua-mac"], {
  cwd: root,
  stdio: ["pipe", "pipe", "inherit"],
});

const requests = [
  { jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-11-25", capabilities: {}, clientInfo: { name: "smoke", version: "1" } } },
  { jsonrpc: "2.0", method: "notifications/initialized" },
  { jsonrpc: "2.0", id: 2, method: "tools/list" },
  { jsonrpc: "2.0", id: 3, method: "tools/call", params: { name: "computer", arguments: { action: "permission_status" } } },
  { jsonrpc: "2.0", id: 4, method: "tools/call", params: { name: "computer", arguments: { action: "list_apps" } } },
];
for (const request of requests) {
  child.stdin.write(`${JSON.stringify(request)}\n`);
}

let buffer = "";
const responses = [];
const timeout = setTimeout(() => finish(new Error(`timed out with ${responses.length} responses`)), 20_000);

child.stdout.setEncoding("utf8");
child.stdout.on("data", (chunk) => {
  buffer += chunk;
  for (;;) {
    const newline = buffer.indexOf("\n");
    if (newline < 0) break;
    const line = buffer.slice(0, newline).trim();
    buffer = buffer.slice(newline + 1);
    if (!line) continue;
    try {
      responses.push(JSON.parse(line));
    } catch (error) {
      finish(new Error(`non-JSON stdout: ${line}\n${error}`));
      return;
    }
    if (responses.length === 4) {
      try {
        const byID = new Map(responses.map((response) => [response.id, response]));
        if (byID.get(1)?.result?.serverInfo?.name !== "wuu-cua-mac") throw new Error("initialize response missing");
        if (byID.get(2)?.result?.tools?.[0]?.name !== "computer") throw new Error("computer tool missing");
        if (typeof byID.get(3)?.result?.structuredContent?.accessibility !== "boolean") throw new Error("permission status missing");
        if (!Array.isArray(byID.get(4)?.result?.structuredContent?.apps)) throw new Error("app list missing");
        finish();
      } catch (error) {
        finish(error);
      }
    }
  }
});
child.on("exit", (code) => {
  if (responses.length < 4) finish(new Error(`helper exited ${code} after ${responses.length} responses`));
});

let finished = false;
function finish(error) {
  if (finished) return;
  finished = true;
  clearTimeout(timeout);
  child.kill();
  if (error) {
    console.error(error.message);
    process.exitCode = 1;
  } else {
    console.log("cua-mac stdio smoke passed");
  }
}
