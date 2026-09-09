// Browser credential persistence: localStorage. The device seed is a secret
// (it is the phone's long-term identity key), so note the tradeoff: the
// desktop host's threat model assumes device storage; a browser origin has
// weaker isolation than Keychain/Keystore. For a LAN companion page this is
// acceptable, but never persist credentials for a shared/公共 origin.

import { secretStorage } from "./native";
import type { Credentials } from "@wuu/remote-core";

export interface WebCredentialStore {
  load(): Promise<Credentials | null>;
  save(creds: Credentials): Promise<void>;
  clear(): Promise<void>;
  loadLastViewed(): Promise<Record<string, string> | null>;
  saveLastViewed(lastViewed: Record<string, string>): Promise<void>;
}

const CREDS_KEY = "wuu.web.credentials";
const LAST_VIEWED_KEY = "wuu.web.lastViewed";

export const webCredStore: WebCredentialStore = {
  async load(): Promise<Credentials | null> {
    const raw = await secretStorage.get(CREDS_KEY);
    if (!raw) return null;
    try {
      return JSON.parse(raw) as Credentials;
    } catch {
      await secretStorage.remove(CREDS_KEY);
      return null;
    }
  },
  async save(creds: Credentials): Promise<void> {
    await secretStorage.set(CREDS_KEY, JSON.stringify(creds));
  },
  async clear(): Promise<void> {
    await secretStorage.remove(CREDS_KEY);
    localStorage.removeItem(LAST_VIEWED_KEY);
  },
  async loadLastViewed(): Promise<Record<string, string> | null> {
    const raw = localStorage.getItem(LAST_VIEWED_KEY);
    if (!raw) return null;
    try {
      return JSON.parse(raw) as Record<string, string>;
    } catch {
      return null;
    }
  },
  async saveLastViewed(lastViewed: Record<string, string>): Promise<void> {
    localStorage.setItem(LAST_VIEWED_KEY, JSON.stringify(lastViewed));
  },
};
