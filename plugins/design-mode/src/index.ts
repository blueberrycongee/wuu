import { readFile, realpath } from "node:fs/promises";
import type { RuntimePlugin } from "@wuu/plugin-sdk";
import { createDesignServer } from "./server.mjs";

type DesignServer = ReturnType<typeof createDesignServer>;

function requireString(value: unknown, field: string): string {
  if (typeof value !== "string" || !value.trim()) throw new Error(`design request requires ${field}`);
  return value;
}

export function createDesignModePlugin(): RuntimePlugin {
  let bridgeSource = "";
  let server: DesignServer | undefined;
  let port = 0;
  const knownRoots = new Set<string>();

  async function ensureServer(): Promise<DesignServer> {
    if (!server) {
      server = createDesignServer({ bridgeSource });
      port = await server.listen(0);
    }
    return server;
  }

  /** Pages and saves only operate on roots the desktop has explicitly opened. */
  async function knownRoot(root: unknown): Promise<string> {
    const real = await realpath(requireString(root, "root"));
    if (!knownRoots.has(real)) throw new Error("open the design root before reading or saving pages");
    return real;
  }

  async function clientRequest(method: string, input: unknown): Promise<unknown> {
    const params = (typeof input === "object" && input !== null ? input : {}) as Record<string, unknown>;
    switch (method.trim()) {
      case "design.open": {
        const instance = await ensureServer();
        const session = await instance.open(requireString(params.root, "root"));
        knownRoots.add(await realpath(requireString(params.root, "root")));
        return { base: `http://127.0.0.1:${port}${session.base}` };
      }
      case "design.pages": {
        const instance = await ensureServer();
        return { pages: await instance.listPages(await knownRoot(params.root)) };
      }
      case "design.save": {
        const instance = await ensureServer();
        return await instance.save(await knownRoot(params.root), params as { path: string; html: string; base_sha256?: string });
      }
      default:
        throw new Error(`unknown design client method ${JSON.stringify(method)}`);
    }
  }

  return {
    async initialize() {
      bridgeSource = await readFile(new URL("./bridge.js", import.meta.url), "utf8");
      return {
        protocol_version: 3 as const,
        capabilities: [{ id: "plugin.client.request", kind: "decision" as const, version: 1 }],
      };
    },
    async invokeCapability(call) {
      if (call.capability !== "plugin.client.request") throw new Error(`unknown design capability ${JSON.stringify(call.capability)}`);
      const request = call.input as { method: string; input?: unknown };
      return { output: { result: await clientRequest(request.method, request.input) } };
    },
    async shutdown() {
      await server?.close();
      server = undefined;
      knownRoots.clear();
    },
  };
}

export default createDesignModePlugin();
