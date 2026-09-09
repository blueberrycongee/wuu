import {
  AccountRequestError,
  accountRequest,
  loginAccount,
  type AccountSession,
} from "@wuu/remote-core";
import type {
  AccountDriver,
  AccountView,
} from "../../../../desktop/src/renderer/AccountPanel";
import { webCredStore } from "./credStore";
import { secretStorage } from "./native";

const key = "wuu.account.v1";
let persistence: Promise<unknown> = Promise.resolve();
function persist<T>(change: () => Promise<T>): Promise<T> {
  const next = persistence.then(change, change);
  persistence = next.catch(() => {});
  return next;
}
async function clearAccount(expected: AccountSession): Promise<void> {
  return persist(async () => {
    const current = await loadAccount();
    if (current?.token !== expected.token || current.server !== expected.server)
      return;
    await secretStorage.remove(key);
    await webCredStore.clear();
    // Shared renderer preferences may contain workspace paths and provider choices.
    for (const name of Object.keys(localStorage))
      if (name.startsWith("wuu.")) localStorage.removeItem(name);
  });
}

export async function loadAccount(): Promise<AccountSession | null> {
  const raw = await secretStorage.get(key);
  return raw ? (JSON.parse(raw) as AccountSession) : null;
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
    );
    await persist(() => secretStorage.set(key, JSON.stringify(result.session)));
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
    try {
      await accountRequest(current.server, current.token, "POST", "/logout");
    } catch (error) {
      if (!(error instanceof AccountRequestError) || error.status !== 401)
        throw error;
    }
    await clearAccount(current);
    return {};
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
