import { MESSAGE_FLOW_FONT_SIZE_RANGE } from "../shared/protocol";

export interface AppearancePreferences {
  codeSize: number;
  uiFont: string;
  codeFont: string;
  motion: "system" | "reduce";
}

export const appearanceDefaults: AppearancePreferences = {
  codeSize: 11,
  uiFont: "", codeFont: "", motion: "system",
};
const storageKey = "wuu.appearance.v1";
const changeEvent = "wuu-appearance-change";

export function normalizeAppearance(value: unknown): AppearancePreferences {
  const input = value && typeof value === "object" ? value as Record<string, unknown> : {};
  const size = (key: "codeSize", min: number, max: number) => {
    const value = input[key];
    return typeof value === "number" && Number.isFinite(value)
      ? Math.min(max, Math.max(min, Math.round(value))) : appearanceDefaults[key];
  };
  const font = (key: string) => typeof input[key] === "string" ? input[key].trim().slice(0, 120) : "";
  return {
    codeSize: size("codeSize", 9, 24), uiFont: font("uiFont"),
    codeFont: font("codeFont"),
    motion: input.motion === "reduce" ? "reduce" : "system",
  };
}

export function readAppearance(): AppearancePreferences {
  try { return normalizeAppearance(JSON.parse(localStorage.getItem(storageKey) ?? "null")); }
  catch { return { ...appearanceDefaults }; }
}

export function fontFamily(name: string, fallback: string): string {
  return name ? `${JSON.stringify(name)}, ${fallback}` : fallback;
}

export function codeEditorTypography(): { fontFamily: string; fontSize: number; lineHeight: number } {
  const preferences = readAppearance();
  const fontSize = preferences.codeSize;
  return {
    fontFamily: fontFamily(preferences.codeFont, 'ui-monospace, "SFMono-Regular", Consolas, monospace'),
    fontSize,
    lineHeight: Math.round(fontSize * 1.65),
  };
}

export function applyAppearance(value: AppearancePreferences): void {
  const preferences = normalizeAppearance(value);
  const root = document.documentElement;
  // Keep the existing persisted reading size as the unified UI preference.
  // The retired local uiSize must not override it after an upgrade.
  root.style.removeProperty("--appearance-ui-size");
  const uiSize = Number.parseFloat(root.style.getPropertyValue("--conversation-message-font-size")) || MESSAGE_FLOW_FONT_SIZE_RANGE.default;
  root.style.setProperty("--appearance-scale", String(uiSize / 14));
  root.style.setProperty("--appearance-code-size", `${preferences.codeSize}px`);
  root.style.setProperty("--appearance-ui-font", fontFamily(preferences.uiFont, "system-ui, sans-serif"));
  root.style.setProperty("--appearance-content-font", "var(--appearance-ui-font)");
  root.style.setProperty("--appearance-code-font", fontFamily(preferences.codeFont, 'ui-monospace, "SFMono-Regular", Consolas, monospace'));
  root.dataset.appearanceMotion = preferences.motion;
}

export function saveAppearance(value: AppearancePreferences): void {
  const preferences = normalizeAppearance(value);
  // Write first: callers show a failure rather than claiming an unsaved value is saved.
  localStorage.setItem(storageKey, JSON.stringify(preferences));
  applyAppearance(preferences);
  window.dispatchEvent(new Event(changeEvent));
}

export function observeAppearance(callback: () => void): () => void {
  const storage = (event: StorageEvent) => {
    if (event.key === storageKey || event.key === null) callback();
  };
  window.addEventListener(changeEvent, callback);
  window.addEventListener("wuu-content-size-change", callback);
  window.addEventListener("storage", storage);
  return () => {
    window.removeEventListener(changeEvent, callback);
    window.removeEventListener("wuu-content-size-change", callback);
    window.removeEventListener("storage", storage);
  };
}

export function startAppearanceSync(): () => void {
  const update = () => applyAppearance(readAppearance());
  update();
  return observeAppearance(update);
}
