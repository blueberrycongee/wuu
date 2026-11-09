// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from "vitest";
import { AccountRequestError, Identity, b64encode, encodeKey } from "@wuu/remote-core";
import { accountDriver, loadAccount, revalidateAccount } from "../src/lib/accountStore";
import { webCredStore } from "../src/lib/credStore";
vi.mock("../src/lib/native", () => ({
  isNative: false,
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
it('keeps the login when only the selected computer is unavailable', async () => {
  saved();
  const current = (await loadAccount())!;
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ devices: [] }))));
  await revalidateAccount(current);
  expect(await loadAccount()).toEqual(current);
});
it('does not clear a replacement login after a late authorization failure', async () => {
  saved();
  const expired = (await loadAccount())!;
  let release!: () => void;
  let requested!: () => void;
  const started = new Promise<void>(resolve => { requested = resolve; });
  vi.stubGlobal('fetch', vi.fn(async () => {
    requested();
    await new Promise<void>(resolve => { release = resolve; });
    return new Response(JSON.stringify({ error: 'revoked' }), { status: 401 });
  }));
  const check = revalidateAccount(expired);
  await started;
  const replacement = { ...expired, token: 'new-login' };
  localStorage.setItem('wuu.account.v1', JSON.stringify(replacement));
  release();
  await check;
  expect(await loadAccount()).toEqual(replacement);
  expect(await webCredStore.load()).not.toBeNull();
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
  await expect(accountDriver("status")).resolves.toMatchObject({ username: "test", unavailable: true });
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

it('finishes a persisted GitHub login with a signed local identity and reuses it after logout', async () => {
 const pending = { server: 'https://account.example', request_id: 'request', verifier: 'v'.repeat(43), oauth_url: 'https://account.example/v1/account/github/authorize?state=request', expires: Date.now() + 600000 };
 const pubs: string[] = [];
 vi.stubGlobal('fetch', vi.fn(async (url: string, options: RequestInit) => {
  if (url.endsWith('/github/poll')) return new Response(JSON.stringify({ status: 'authorized', username: 'gh-identity' }));
  if (url.endsWith('/github/complete')) {
   const body = JSON.parse(String(options.body)); expect(body.proof).toBeTruthy(); expect(body.verifier).toBe(pending.verifier); pubs.push(body.pub);
   return new Response(JSON.stringify({ token: 'wuu-session', username: 'gh-identity', pub: body.pub }));
  }
  return new Response('{}');
 }));
 for (let i = 0; i < 2; i++) {
  localStorage.setItem('wuu.github.pending', JSON.stringify(pending));
  expect(await accountDriver('status')).toEqual({ oauth_url: pending.oauth_url });
  expect(await accountDriver('github-poll')).toMatchObject({ username: 'gh-identity', auth_method: 'github' });
  expect(localStorage.getItem('wuu.github.pending')).toBeNull();
  expect((await loadAccount())?.token).toBe('wuu-session');
  await accountDriver('logout');
 }
 expect(pubs[0]).toBe(pubs[1]);
});

it('does not persist an OAuth completion that arrives after cancellation', async () => {
 const pending = { server: 'https://account.example', request_id: 'request', verifier: 'v'.repeat(43), oauth_url: 'https://account.example/v1/account/github/authorize?state=request', expires: Date.now() + 600000 };
 localStorage.setItem('wuu.github.pending', JSON.stringify(pending));
 let release!: () => void;
 let completing!: () => void;
 const started = new Promise<void>(yes => { completing = yes; });
 vi.stubGlobal('fetch', vi.fn(async (url: string, options: RequestInit) => {
  if (url.endsWith('/github/poll')) return new Response(JSON.stringify({ status: 'authorized', username: 'gh-identity' }));
  if (url.endsWith('/github/complete')) {
   const body = JSON.parse(String(options.body)); completing();
   await new Promise<void>(yes => { release = yes; });
   return new Response(JSON.stringify({ token: 'late-session', username: 'gh-identity', pub: body.pub }));
  }
  return new Response('{}');
 }));
 const poll = accountDriver('github-poll'); await started;
 await accountDriver('github-cancel'); release(); await poll;
 expect(await loadAccount()).toBeNull();
});
