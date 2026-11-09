// Opt-in end-to-end smoke: proves the browser-usable data layer
// (@wuu/remote-core + this package's controller/store) talks to a real Go
// host through a real relay.
//
//   export WUU_SMOKE_URI="wuu://pair/..."   # printed by `wuu remote host --pair`
//   npx vitest run test/smoke.e2e.test.ts
//
// Without the env var the suite skips. The host/relay processes are started
// outside vitest (see README "端到端冒烟").
//
// Uses the raw RemoteClient (not the WuuMobile controller) so a failure can
// be attributed to pairing/attach/RPC before any UI logic is involved.

import { describe, expect, it } from "vitest";

import {
  CLIENT_PROFILE_MOBILE_CHAT,
  RemoteClient,
  pair,
} from "@wuu/remote-core";

// Vitest runs in Node; keep this file self-contained without @types/node.
declare const process: { env: Record<string, string | undefined> };

const URI = process.env.WUU_SMOKE_URI?.trim();

describe.skipIf(!URI)("relay → host → app-server end to end", () => {
  it(
    "pairs, attaches, initializes, and lists threads",
    async () => {
      const creds = await pair(URI!, "web-smoke");
      expect(creds.host_pub).toBeTruthy();
      expect(creds.relay_url).toMatch(/^wss?:\/\//);

      let resolveAttach!: () => void;
      const attached = new Promise<void>((resolve) => {
        resolveAttach = resolve;
      });

      const client = new RemoteClient(creds, {
        clientProfile: CLIENT_PROFILE_MOBILE_CHAT,
        onNotification: () => {},
        onAttach: () => resolveAttach(),
        onDetach: () => {},
      });

      client.start();
      await attached;

      const init = await client.call<{ ok?: boolean }>("initialize");
      expect(init).toBeTruthy();

      const list = await client.call<{ threads?: unknown[] }>("thread/list");
      expect(Array.isArray(list.threads)).toBe(true);

      await client.stop();
    },
    60_000,
  );
});
