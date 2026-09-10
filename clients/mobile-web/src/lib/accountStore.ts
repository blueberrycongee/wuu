import {
  AccountRequestError,
  accountRequest,
  loginAccount,
  accountOrigin,
  Identity,
  b64decode,
  b64encode,
  type AccountSession,
  startGitHubLogin, pollGitHubLogin, completeGitHubLogin, type GitHubPending,
} from "@wuu/remote-core";
import type {
  AccountDriver,
  AccountView,
} from "../../../../desktop/src/renderer/AccountPanel";
import { webCredStore } from "./credStore";
import { secretStorage, clearNativeShareCache, isNative } from "./native";

const key = "wuu.account.v1";
const identitiesKey = "wuu.account-identities.v1";
const pendingKey = 'wuu.github.pending';
const directoryKey = 'wuu.account.directory';
async function loadPending(): Promise<GitHubPending | null> {
  const raw = await secretStorage.get(pendingKey);
  if (!raw) return null;
  try {
    const pending = JSON.parse(raw) as GitHubPending;
    if (Number.isFinite(pending.expires) && Date.now() < pending.expires && typeof pending.verifier === 'string' && pending.verifier.length === 43 && typeof pending.request_id === 'string' && typeof pending.server === 'string') return pending;
  } catch { /* Discard an interrupted or malformed pending login. */ }
  await secretStorage.remove(pendingKey);
  return null;
}
let persistence: Promise<unknown> = Promise.resolve();
function persist<T>(change: () => Promise<T>): Promise<T> {
  const next = persistence.then(change, change);
  persistence = next.catch(() => {});
  return next;
}
async function clearAccount(expected: AccountSession): Promise<void> {
  await accountIdentity(expected.server, expected.username, expected);
  return persist(async () => {
    const current = await loadAccount();
    if (current?.token !== expected.token || current.server !== expected.server)
      return;
    await secretStorage.remove(key);
    await secretStorage.remove(pendingKey);
    await webCredStore.clear();
    await clearNativeShareCache();
    // Shared renderer preferences may contain workspace paths and provider choices.
    for (const name of Object.keys(localStorage))
      if (name.startsWith("wuu.") && name !== identitiesKey && name !== "wuu.web.language" && name !== "wuu.account.server" && name !== "wuu.web.paired") localStorage.removeItem(name);
    window.dispatchEvent(new Event('wuu:account-change'));
  });
}

export async function loadAccount(): Promise<AccountSession | null> {
  const raw = await secretStorage.get(key);
  return raw ? (JSON.parse(raw) as AccountSession) : null;
}

// Scope identities to an account and service: one public key cannot belong to
// multiple accounts. Keep these separately from revocable login tokens.
async function accountIdentity(server: string, username: string, current: AccountSession | null): Promise<Identity> {
  return persist(async () => {
    const scope = JSON.stringify([accountOrigin(server), username.trim().toLowerCase()]);
    const identities = JSON.parse(await secretStorage.get(identitiesKey) || '{}') as Record<string, string>;
    const migrated = current && JSON.stringify([accountOrigin(current.server), current.username.trim().toLowerCase()]) === scope ? current.device_seed : undefined;
    const seed = identities[scope] || migrated;
    const identity = seed ? Identity.fromSeed(b64decode(seed)) : Identity.generate();
    identities[scope] = b64encode(identity.seed());
    await secretStorage.set(identitiesKey, JSON.stringify(identities));
    return identity;
  });
}
export const accountDriver: AccountDriver = async (action, input = {}) => {
  const current = await loadAccount();
  if (action === 'config') return accountRequest(input.server, '', 'GET', '/config');
  if (action === 'github-start') {
    const pending = await startGitHubLogin(input.server, isNative);
    pending.name = input.name?.trim() || 'Wuu 手机';
    await persist(() => secretStorage.set(pendingKey, JSON.stringify(pending)));
    return { oauth_url: pending.oauth_url };
  }
  if (action === 'github-cancel') {
    const pending = await loadPending();
    await persist(() => secretStorage.remove(pendingKey));
    if (pending) await accountRequest(pending.server, '', 'POST', '/github/cancel', { request_id: pending.request_id, verifier: pending.verifier }).catch(() => {});
    return {};
  }
  if (action === 'github-poll') {
    const pending = await loadPending();
    if (!pending) throw new Error('GitHub 登录已过期，请重新开始');
    const status = await pollGitHubLogin(pending);
    if (status.status !== 'authorized' || !status.username) return { oauth_url: pending.oauth_url };
    const identity = await accountIdentity(pending.server, status.username, current);
    const session = await completeGitHubLogin(pending, status.username, pending.name || 'Wuu 手机', identity);
    // Cancellation may have happened while the network request was in flight.
    const saved = await persist(async () => {
      if ((await loadPending())?.request_id !== pending.request_id) return false;
      await secretStorage.set(key, JSON.stringify(session));
      await secretStorage.remove(pendingKey);
      return true;
    });
    if (!saved) return {};
    window.dispatchEvent(new Event('wuu:account-change'));
    return { username: session.username, server: session.server, auth_method: 'github' };
  }
  if (action === "login" || action === "register") {
    const result = await loginAccount(
      input.server,
      input.username,
      input.password,
      input.name?.trim() || "Wuu 手机",
      action === "register",
      await accountIdentity(input.server, input.username, current),
    );
    await persist(() => secretStorage.set(key, JSON.stringify(result.session)));
    window.dispatchEvent(new Event('wuu:account-change'));
    return { recovery: result.recovery };
  }
  if (action === "recover")
    return accountRequest(input.server, "", "POST", "/recover", {
      username: input.username,
      secret: input.secret,
      password: input.password,
    });
  if (action === "status") {
    const pending = await loadPending();
    if (pending) return { oauth_url: pending.oauth_url };
    if (!current) return {};
    try {
      const result = await accountRequest<AccountView>(
        current.server,
        current.token,
        "GET",
        "/devices",
      );
      const view = { ...result, server: current.server, pub: current.pub };
      localStorage.setItem(directoryKey, JSON.stringify(view));
      return view;
    } catch (error) {
      if (!(error instanceof AccountRequestError) || error.status !== 401)
        {
          let cached: AccountView = {};
          try { cached = JSON.parse(localStorage.getItem(directoryKey) || '{}'); } catch { /* no cached directory */ }
          if (cached.server !== current.server || cached.username !== current.username || cached.pub !== current.pub) cached = {};
          return { ...cached, username: current.username, server: current.server, pub: current.pub, unavailable: true };
        }
      await clearAccount(current);
      return {};
    }
  }
  if (!current) throw new Error("请先登录");
  if (action === "logout") {
    // Local logout must work offline; remote revocation is best effort.
    let localLogoutOnly = false;
    await accountRequest(current.server, current.token, "POST", "/logout").catch((error) => {
      localLogoutOnly = !(error instanceof AccountRequestError && error.status === 401);
    });
    await clearAccount(current);
    return { localLogoutOnly };
  }
  if (action === "password") {
    const result = await accountRequest<AccountView>(
      current.server,
      current.token,
      "POST",
      "/password",
      {
        username: current.username,
        secret: input.secret,
        password: input.password,
      },
    );
    await clearAccount(current);
    return result;
  }
  await accountRequest(
    current.server,
    current.token,
    "DELETE",
    "/devices/" + encodeURIComponent(input.pub),
  );
  return {};
};
