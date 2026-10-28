import type { ExtensionInventoryRecord } from "../shared/protocol";
import type { SettingsPage } from "./SettingsView";
import { ENABLE_REMOTE_CONTROL } from "./FeatureFlags";
import { hostSupports } from "./HostCapabilities";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import type { PluginHost } from "./plugins/PluginHost";

export function remoteControlAvailable(): boolean {
  return ENABLE_REMOTE_CONTROL && hostSupports("getRemoteControlSnapshot");
}

export function browserSettingsAvailable(): boolean {
  return Boolean(
    window.wuu?.listBrowserProfiles &&
      window.wuu.importBrowserCookies &&
      window.wuu.listBrowserExtensions &&
      window.wuu.addBrowserExtension &&
      window.wuu.setBrowserExtensionEnabled &&
      window.wuu.removeBrowserExtension,
  );
}

export function availableSettingsPage(page: SettingsPage | undefined, browserSettingsAllowed = true): SettingsPage {
  const next = page ?? "providers";
  if (next === "subscriptions" || next === "agents") {
    return "providers";
  }
  if (next === "remote" && !remoteControlAvailable()) {
    return "providers";
  }
  if (next === "browser" && (!browserSettingsAllowed || !browserSettingsAvailable())) {
    return "providers";
  }
  return next;
}

export function isConfigurablePlugin(
  extension: ExtensionInventoryRecord,
): boolean {
  const approved = extension.approval_state === "official"
    || extension.approval_state === "granted";
  return extension.kind === "plugin"
    && approved
    && extension.enabled !== false
    && (extension.contributions?.settings?.length ?? 0) > 0;
}

export function isSettingsPageAvailable(
  page: SettingsPage,
  extensionInventory: readonly ExtensionInventoryRecord[] = [],
  pluginHost: Pick<PluginHost, "getSettingsPages"> = desktopPluginHost,
  browserSettingsAllowed = true,
): boolean {
  if (availableSettingsPage(page, browserSettingsAllowed) !== page) return false;
  if (page.startsWith("plugin-settings:")) {
    return extensionInventory.some((plugin) =>
      isConfigurablePlugin(plugin) && `plugin-settings:${plugin.id}` === page,
    );
  }
  if (page.startsWith("plugin-view:")) {
    return pluginHost.getSettingsPages().some((entry) =>
      `plugin-view:${entry.pluginId}:${entry.id}` === page,
    );
  }
  return true;
}
