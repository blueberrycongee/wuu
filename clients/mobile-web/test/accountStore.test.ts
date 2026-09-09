// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from "vitest";
import { AccountRequestError } from "@wuu/remote-core";
import { accountDriver, loadAccount } from "../src/lib/accountStore";
import { webCredStore } from "../src/lib/credStore";
vi.mock("../src/lib/native", () => ({
  clearNativeShareCache: vi.fn(async () => {}),
  secretStorage: {
    get: async (key: string) => localStorage.getItem(key),
    set: async (key: string, value: string) => localStorage.setItem(key, value),
    remove: async (key: string) => localStorage.removeItem(key),
  },
}));
beforeEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});
function saved() {
  localStorage.setItem(
    "wuu.account.v1",
    JSON.stringify({
      server: "https://account.example",
      token: "expired",
      pub: "phone",
      username: "test",
    }),
  );
  localStorage.setItem(
    "wuu.web.credentials",
    JSON.stringify({ host_pub: "host" }),
  );
  localStorage.setItem("wuu.desktop.lastDraftRuntime", "private provider");
}
it("forgets revoked credentials and account-specific renderer state", async () => {
  saved();
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ error: "unauthorized" }), {
          status: 401,
        }),
    ),
  );
  expect(await accountDriver("status")).toEqual({});
  expect(await loadAccount()).toBeNull();
  expect(await webCredStore.load()).toBeNull();
  expect(localStorage.getItem("wuu.desktop.lastDraftRuntime")).toBeNull();
});
it("preserves the account through network and server failures", async () => {
  saved();
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ error: "unavailable" }), { status: 503 }),
    ),
  );
  await expect(accountDriver("status")).rejects.toBeInstanceOf(
    AccountRequestError,
  );
  expect(await loadAccount()).not.toBeNull();
  await expect(accountDriver("logout")).rejects.toBeInstanceOf(
    AccountRequestError,
  );
  expect(await loadAccount()).not.toBeNull();
});
it("can sign out after another device revoked this device", async () => {
  saved();
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ error: "unauthorized" }), {
          status: 401,
        }),
    ),
  );
  await accountDriver("logout");
  expect(await loadAccount()).toBeNull();
});
