import {
  AccountRequestError,
  accountRequest,
  loginAccount,
  accountOrigin,
  Identity,
  b64decode,
  b64encode,
  type AccountSession,
} from "@wuu/remote-core";
import type {
  AccountDriver,
  AccountView,
} from "../../../../desktop/src/renderer/AccountPanel";
import { webCredStore } from "./credStore";
import { secretStorage, clearNativeShareCache } from "./native";

const key = "wuu.account.v1";
const identitiesKey = "wuu.account-identities.v1";
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
    await webCredStore.clear();
    await clearNativeShareCache();
    // Shared renderer preferences may contain workspace paths and provider choices.
    for (const name of Object.keys(localStorage))
      if (name.startsWith("wuu.") && name !== identitiesKey && name !== "wuu.web.language") localStorage.removeItem(name);
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
    if (!current) return {};
    try {
      const result = await accountRequest<AccountView>(
        current.server,
        current.token,
        "GET",
        "/devices",
      );
      return { ...result, server: current.server, pub: current.pub };
    } catch (error) {
      if (!(error instanceof AccountRequestError) || error.status !== 401)
        throw error;
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
