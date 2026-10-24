import type {
  ExtensionInventoryRecord,
  ExtensionThemeDescriptor,
  ThemePreference,
} from "../shared/protocol";
import {
  canonicalThemeTokenName,
  isPublicSyntaxTokenName,
  isPublicThemeTokenName,
} from "../shared/themeContract.generated";

/**
 * Renderer-side theme controller.
 *
 * The single source of visual truth is the `data-theme` attribute on
 * <html>: every color in the stylesheets resolves through CSS custom
 * properties, and the theme blocks in base.css key off
 * `:root[data-theme="dark"]`. This module resolves a persisted
 * preference ("system" | "light" | "dark") to a concrete attribute
 * value and keeps it in sync with the OS when the preference is
 * "system".
 *
 * The preload script stamps the attribute once before first paint (so
 * boot doesn't flash light); `applyThemePreference` is the runtime
 * authority from then on.
 */

const DARK_SCHEME_QUERY = "(prefers-color-scheme: dark)";

let systemListenerCleanup: (() => void) | undefined;
const EXTENSION_THEME_KEY = "wuu.extension-theme";
const appliedExtensionTokens = new Set<string>();
let themeInventory: readonly ExtensionInventoryRecord[] | undefined;

export type AvailableExtensionTheme = ExtensionThemeDescriptor & {
  key: string;
  legacyKeys: string[];
  pluginId: string;
  pluginName: string;
};

export type AppliedTheme = "light" | "dark";

export function resolveThemePreference(
  preference: ThemePreference,
): "light" | "dark" {
  if (preference === "system") {
    return typeof window !== "undefined" &&
      window.matchMedia?.(DARK_SCHEME_QUERY).matches
      ? "dark"
      : "light";
  }
  return preference;
}

export function appliedTheme(): string | undefined {
  return document.documentElement.dataset.theme;
}

export function currentAppliedTheme(): AppliedTheme {
  return appliedTheme() === "dark" ? "dark" : "light";
}

export function observeAppliedTheme(
  onChange: (theme: AppliedTheme) => void,
): () => void {
  let current = currentAppliedTheme();
  const observer = new MutationObserver(() => {
    const next = currentAppliedTheme();
    if (next === current) {
      return;
    }
    current = next;
    onChange(next);
  });
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
  return () => observer.disconnect();
}

/**
 * Stamp the resolved theme on <html> and, for "system", follow OS
 * changes live until a different preference is applied.
 */
export function applyThemePreference(preference: ThemePreference): void {
  clearExtensionThemeTokens();
  systemListenerCleanup?.();
  systemListenerCleanup = undefined;

  document.documentElement.dataset.theme = resolveThemePreference(preference);
  applySelectedTheme();

  if (preference !== "system" || typeof window === "undefined") {
    return;
  }
  const query = window.matchMedia?.(DARK_SCHEME_QUERY);
  if (!query?.addEventListener) {
    return;
  }
  const onChange = (event: MediaQueryListEvent): void => {
    document.documentElement.dataset.theme = event.matches ? "dark" : "light";
    applySelectedTheme();
  };
  query.addEventListener("change", onChange);
  systemListenerCleanup = () => query.removeEventListener("change", onChange);
}

export function availableExtensionThemes(
  inventory: readonly ExtensionInventoryRecord[] | undefined,
): AvailableExtensionTheme[] {
  return (inventory ?? []).flatMap((plugin) => {
    if (
      plugin.kind !== "plugin" ||
      plugin.enabled === false ||
      (plugin.state !== "granted" && plugin.state !== "active")
    ) {
      return [];
    }
    const pluginId = plugin.provenance.plugin_id?.trim() || plugin.id;
    return (plugin.contributions?.themes ?? []).map((theme) => {
      const key = `${pluginId}:${theme.id}`;
      const legacyKey = `${plugin.id}:${theme.id}`;
      return {
        ...theme,
        key,
        legacyKeys: legacyKey === key ? [] : [legacyKey],
        pluginId,
        pluginName: plugin.name,
      };
    });
  });
}

export function selectedExtensionThemeKey(mode: AppliedTheme = currentAppliedTheme()): string {
  return window.localStorage?.getItem(`${EXTENSION_THEME_KEY}.${mode}`) ?? "";
}

export function applyExtensionTheme(theme: AvailableExtensionTheme): void {
  window.localStorage?.setItem(`${EXTENSION_THEME_KEY}.${theme.base}`, theme.key);
  if (theme.base !== currentAppliedTheme()) return;
  paintExtensionTheme(theme);
}

function paintExtensionTheme(theme: AvailableExtensionTheme): void {
  clearExtensionThemeTokens();
  document.documentElement.dataset.theme = theme.base;
  const explicitTokens = new Set(Object.keys(theme.tokens));
  for (const [token, value] of Object.entries(theme.tokens)) {
    if (!isPublicThemeTokenName(token)) continue;
    document.documentElement.style.setProperty(token, value);
    appliedExtensionTokens.add(token);
    const canonical = canonicalThemeTokenName(token);
    if (canonical !== token && !explicitTokens.has(canonical)) {
      document.documentElement.style.setProperty(canonical, value);
      appliedExtensionTokens.add(canonical);
    }
  }
  for (const [token, value] of Object.entries(theme.syntax ?? {})) {
    if (!isPublicSyntaxTokenName(token)) continue;
    document.documentElement.style.setProperty(token, value);
    appliedExtensionTokens.add(token);
  }
  // Public defaults remain available to shells; only declared tokens override
  // the built-in UI roles, including canonicalized legacy aliases.
  document.documentElement.dataset.themeTokens = [...appliedExtensionTokens].join(" ");
}

export function clearExtensionTheme(): void {
  clearExtensionThemeTokens();
  window.localStorage?.removeItem(EXTENSION_THEME_KEY);
  window.localStorage?.removeItem(`${EXTENSION_THEME_KEY}.light`);
  window.localStorage?.removeItem(`${EXTENSION_THEME_KEY}.dark`);
}

export function selectExtensionTheme(mode: AppliedTheme, key: string): void {
  window.localStorage.setItem(`${EXTENSION_THEME_KEY}.${mode}`, key);
  applySelectedTheme();
  window.dispatchEvent(new Event("wuu-theme-selection"));
}

function applySelectedTheme(): void {
  clearExtensionThemeTokens();
  const selected = selectedExtensionThemeKey();
  const theme = availableExtensionThemes(themeInventory).find(
    (candidate) => candidate.base === currentAppliedTheme() &&
      (candidate.key === selected || candidate.legacyKeys.includes(selected)),
  );
  if (theme) paintExtensionTheme(theme);
}

export function syncExtensionTheme(
  inventory: readonly ExtensionInventoryRecord[] | undefined,
): void {
  themeInventory = inventory;
  // Migrate only once inventory is available; disabled themes retain the selection.
  const legacy = window.localStorage?.getItem(EXTENSION_THEME_KEY);
  if (legacy && inventory) {
    const theme = availableExtensionThemes(inventory).find(
      (candidate) => candidate.key === legacy || candidate.legacyKeys.includes(legacy),
    );
    if (theme && window.localStorage.getItem(`${EXTENSION_THEME_KEY}.${theme.base}`) === null) {
      window.localStorage.setItem(`${EXTENSION_THEME_KEY}.${theme.base}`, theme.key);
    }
    if (theme) window.localStorage.removeItem(EXTENSION_THEME_KEY);
  }
  applySelectedTheme();
}

function clearExtensionThemeTokens(): void {
  for (const token of appliedExtensionTokens) {
    document.documentElement.style.removeProperty(token);
  }
  appliedExtensionTokens.clear();
  delete document.documentElement.dataset.themeTokens;
}

/**
 * Follow preference changes made in OTHER windows. The main process owns
 * the app-global preference and broadcasts every change; each window
 * re-applies it locally (the initiating window already applied it — the
 * re-apply is idempotent). Returns a disposer; the boot subscription is
 * window-lifetime, so it only matters for tests.
 */
export function startThemePreferenceSync(): () => void {
  const onStorage = (event: StorageEvent): void => {
    if (event.key === null || event.key.startsWith(EXTENSION_THEME_KEY)) {
      applySelectedTheme();
      window.dispatchEvent(new Event("wuu-theme-selection"));
    }
  };
  window.addEventListener("storage", onStorage);
  const unsubscribe = (
    window.wuu?.onThemePreferenceChange?.((theme) => {
      applyThemePreference(theme);
    }) ?? (() => {})
  );
  return () => {
    unsubscribe();
    window.removeEventListener("storage", onStorage);
  };
}
