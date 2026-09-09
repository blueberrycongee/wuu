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
  document.documentElement.dataset.native = Capacitor.getPlatform();
  const wake = () => window.dispatchEvent(new Event("online"));
  await App.addListener("appStateChange", ({ isActive }) => {
    if (isActive) wake();
  });
  await Network.addListener("networkStatusChange", ({ connected }) => {
    if (connected) wake();
  });
  await App.addListener("backButton", () => {
    const event = new CustomEvent("wuu:native-back", { cancelable: true });
    if (window.dispatchEvent(event)) void App.minimizeApp();
  });
  document.addEventListener("click", (event) => {
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
  const path = "shared/" + Date.now() + "-" + safe;
  const result = await Filesystem.writeFile({
    path,
    data,
    directory: Directory.Cache,
    recursive: true,
  });
  try {
    await Share.share({ files: [result.uri], title: name });
  } finally {
    await Filesystem.deleteFile({ path, directory: Directory.Cache }).catch(
      () => {},
    );
  }
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
