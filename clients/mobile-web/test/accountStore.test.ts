// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from "vitest";
import { AccountRequestError, Identity, b64encode, encodeKey } from "@wuu/remote-core";
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
  const identity = Identity.generate();
  localStorage.setItem(
    "wuu.account.v1",
    JSON.stringify({
      server: "https://account.example",
      token: "expired",
      pub: encodeKey(identity.public_()),
      device_seed: b64encode(identity.seed()),
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
it("preserves the account on failed refresh but allows local logout", async () => {
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
  await expect(accountDriver("logout")).resolves.toEqual({ localLogoutOnly: true });
  expect(await loadAccount()).toBeNull();
  expect(await webCredStore.load()).toBeNull();
});
it("reuses the device after offline logout and isolates accounts and services", async () => {
  const enrollments: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (url: string, options: RequestInit) => {
    if (url.endsWith('/logout')) throw new TypeError('offline');
    const body = JSON.parse(String(options.body));
    enrollments.push(body.pub);
    return new Response(JSON.stringify({ token: 'token', username: body.username, pub: body.pub }));
  }));
  const input = { server: 'https://account.example', username: 'test', password: 'password' };
  await accountDriver('login', input);
  await accountDriver('logout');
  expect(await loadAccount()).toBeNull();
  await accountDriver('login', { ...input, username: ' TEST ' });
  expect(enrollments[1]).toBe(enrollments[0]);
  await accountDriver('login', { ...input, username: 'other' });
  await accountDriver('login', { ...input, server: 'https://other.example' });
  expect(new Set(enrollments).size).toBe(3);
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
it("migrates an existing session identity before clearing an expired login", async () => {
  saved();
  const previous = await loadAccount();
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'unauthorized' }), { status: 401 })));
  await accountDriver('status');
  expect(await loadAccount()).toBeNull();
  vi.stubGlobal('fetch', vi.fn(async (_url: string, options: RequestInit) => {
    const body = JSON.parse(String(options.body));
    expect(body.pub).toBe(previous?.pub);
    return new Response(JSON.stringify({ token: 'new', pub: body.pub, username: body.username }));
  }));
  await accountDriver('login', { server: 'https://account.example', username: 'test', password: 'password' });
  expect((await loadAccount())?.device_seed).toBe(previous?.device_seed);
});
