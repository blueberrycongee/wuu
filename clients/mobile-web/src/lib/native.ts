import { Capacitor, registerPlugin } from "@capacitor/core";
import { App } from "@capacitor/app";
import { Network } from "@capacitor/network";
import { Browser } from "@capacitor/browser";
import { Filesystem, Directory } from "@capacitor/filesystem";
import { Share } from "@capacitor/share";

interface SecureStoragePlugin {
  get(options: { key: string }): Promise<{ value?: string }>;
  set(options: { key: string; value: string }): Promise<void>;
  remove(options: { key: string }): Promise<void>;
}
const secure = registerPlugin<SecureStoragePlugin>("WuuSecureStorage");
export const isNative = Capacitor.isNativePlatform();
export const secretStorage = {
  async get(key: string): Promise<string | null> {
    return isNative
      ? ((await secure.get({ key }))?.value ?? null)
      : localStorage.getItem(key);
  },
  async set(key: string, value: string): Promise<void> {
    if (isNative) await secure.set({ key, value });
    else localStorage.setItem(key, value);
  },
  async remove(key: string): Promise<void> {
    if (isNative) await secure.remove({ key });
    else localStorage.removeItem(key);
  },
};
export async function startNativeLifecycle(): Promise<void> {
  if (!isNative) return;
  await pruneSharedFiles();
  document.documentElement.dataset.native = Capacitor.getPlatform();
  await App.addListener('appUrlOpen', ({ url }) => {
    if (url !== 'wuu://account/github') return;
    void Browser.close().catch(() => {});
    window.dispatchEvent(new Event('online'));
  });
  const wake = () => window.dispatchEvent(new Event("online"));
  await App.addListener("appStateChange", ({ isActive }) => {
    if (isActive) wake();
    else window.dispatchEvent(new Event('wuu:background'));
  });
  await Network.addListener("networkStatusChange", ({ connected }) => {
    if (connected) wake();
  });
  await App.addListener("backButton", () => {
    const event = new CustomEvent("wuu:native-back", { cancelable: true });
    if (window.dispatchEvent(event)) void App.minimizeApp();
  });
  document.addEventListener("click", (event) => {
    if (event.defaultPrevented) return;
    const link = (event.target as Element | null)?.closest(
      "a[href]",
    ) as HTMLAnchorElement | null;
    if (!link || !/^https?:/.test(link.href)) return;
    event.preventDefault();
    void Browser.open({ url: link.href });
  });
}
export async function shareNativeFile(
  name: string,
  data: string,
): Promise<void> {
  const safe =
    name.replace(/[^\p{L}\p{N}._-]/gu, "_").slice(-150) || "attachment";
  await pruneSharedFiles();
  const path = "shared/" + Date.now() + '-' + Math.random().toString(36).slice(2) + '/' + safe;
  const result = await Filesystem.writeFile({
    path,
    data,
    directory: Directory.Cache,
    recursive: true,
  });
  // Android may return from its chooser before the destination reads the URI.
  // Keep the original filename and retain its cache file for that handoff.
  await Share.share({ files: [result.uri], title: name });
}

async function pruneSharedFiles(): Promise<void> {
  const entries = await Filesystem.readdir({ path: 'shared', directory: Directory.Cache }).catch(() => ({ files: [] }));
  for (const entry of entries.files) {
    const created = Number(entry.name.split('-')[0]);
    if (!Number.isFinite(created) || created <= 0 || Date.now() - created < 24 * 60 * 60 * 1000) continue;
    const options = { path: 'shared/' + entry.name, directory: Directory.Cache };
    if (entry.type === 'directory') await Filesystem.rmdir({ ...options, recursive: true }).catch(() => {});
    else await Filesystem.deleteFile(options).catch(() => {});
  }
}

export async function clearNativeShareCache(): Promise<void> {
  if (isNative) await Filesystem.rmdir({ path: 'shared', directory: Directory.Cache, recursive: true }).catch(() => {});
}

export async function openNativeURL(url: string): Promise<void> {
  await Browser.open({ url });
}
export async function saveNativeArtifact(
  name: string,
  source: string,
): Promise<void> {
  if (!/^(data:|blob:)/.test(source)) throw new Error("请先加载文件预览");
  const response = await fetch(source);
  const blob = await response.blob();
  if (blob.size > 64 * 1024 * 1024)
    throw new Error("文件超过手机分享的 64 MB 限制");
  const data = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",")[1]);
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(blob);
  });
  await shareNativeFile(name, data);
}

// Reserve a web tab during the user gesture, before the asynchronous OAuth start.
export function reserveAuthorization(): { open: (url: string) => Promise<void>; close: () => void } {
  const popup = isNative ? null : window.open('about:blank', '_blank');
  if (popup) popup.opener = null;
  return {
    async open(url) {
      if (isNative) await Browser.open({ url });
      else if (popup && !popup.closed) popup.location.href = url;
      else throw new Error('请允许弹出窗口，然后点击继续前往 GitHub');
    },
    close() { popup?.close(); },
  };
}
