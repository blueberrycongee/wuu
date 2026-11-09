// Credential persistence: Keychain/Keystore via expo-secure-store on
// device; localStorage on web (explicitly weaker — the design doc calls
// this out, web is a dev/preview surface).

import * as SecureStore from "expo-secure-store";
import { Platform } from "react-native";
import type { Credentials } from "@wuu/remote-core";

import type { CredentialStore } from "./connection";

const KEY = "wuu.remote.credentials";
const LAST_VIEWED_KEY = "wuu.remote.lastViewed";

async function read(key: string): Promise<string | null> {
  if (Platform.OS === "web") {
    return globalThis.localStorage?.getItem(key) ?? null;
  }
  return SecureStore.getItemAsync(key);
}

async function write(key: string, value: string): Promise<void> {
  if (Platform.OS === "web") {
    globalThis.localStorage?.setItem(key, value);
    return;
  }
  await SecureStore.setItemAsync(key, value);
}

async function remove(key: string): Promise<void> {
  if (Platform.OS === "web") {
    globalThis.localStorage?.removeItem(key);
    return;
  }
  await SecureStore.deleteItemAsync(key);
}

export const deviceCredentialStore: CredentialStore = {
  async load(): Promise<Credentials | null> {
    const raw = await read(KEY);
    if (!raw) return null;
    try {
      return JSON.parse(raw) as Credentials;
    } catch {
      return null;
    }
  },
  async save(creds: Credentials): Promise<void> {
    await write(KEY, JSON.stringify(creds));
  },
  async clear(): Promise<void> {
    await remove(KEY);
    await remove(LAST_VIEWED_KEY);
  },
  async loadLastViewed(): Promise<Record<string, string> | null> {
    const raw = await read(LAST_VIEWED_KEY);
    if (!raw) return null;
    try {
      return JSON.parse(raw) as Record<string, string>;
    } catch {
      return null;
    }
  },
  async saveLastViewed(lastViewed: Record<string, string>): Promise<void> {
    await write(LAST_VIEWED_KEY, JSON.stringify(lastViewed));
  },
};
