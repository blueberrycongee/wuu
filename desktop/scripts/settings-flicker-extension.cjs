// E2E extension (WUU_RENDERER_E2E_EXTENSION): fills in the desktop-preference
// and browser-capability APIs the resize fixture lacks, and adds production-like
// latency to every asynchronous settings read so remount-time pop-in becomes
// observable the way it is over real IPC.
module.exports = function extend(api) {
  const latency = Number(process.env.WUU_FLICKER_LATENCY_MS || 120);
  const delay = value => new Promise(resolve => setTimeout(() => resolve(value), latency));
  const prefs = {
    notify_turn_completed: true,
    notify_questions: false,
    notification_sound: true,
    auto_check_updates: true,
    open_at_login: false,
    send_shortcut: "enter",
    proxy_mode: "system",
    proxy_url: "",
    auto_archive_days: 30,
  };
  const prefListeners = new Set();
  const delayApi = (name, impl) => {
    const original = api[name];
    if (typeof original !== "function" && impl === undefined) return;
    api[name] = (...args) => delay(undefined).then(() => (impl ?? original).apply(api, args));
  };
  for (const name of [
    "getBuildInfo", "listMCPServers", "listEngines", "getSettingsUsage",
    "listCatalogProviders", "listCodexPets", "getSystemPermissions",
    "getRemoteControlSnapshot", "listBrowserProfiles", "listBrowserExtensions",
    "listArchivedThreads", "getDesktopPreferences",
  ]) delayApi(name);
  api.getDesktopPreferences = async () => delay({ ...prefs });
  api.updateDesktopPreferences = async patch => {
    Object.assign(prefs, patch);
    const next = { ...prefs };
    for (const listener of prefListeners) listener(next);
    return delay(next);
  };
  api.onDesktopPreferencesChanged = listener => { prefListeners.add(listener); return () => prefListeners.delete(listener); };
  api.getDesktopUpdateState = async () => delay({ kind: "unsupported" });
  api.getSystemPermissions = async () => delay({ available: true, screen: false, accessibility: true });
  api.requestSystemPermission = async () => delay(undefined);
  api.getThemePreference = async () => delay("system");
  api.setThemePreference = async () => delay(undefined);
  api.getMessageFlowFontSize = async () => delay(15);
  api.listCodexPets = async () => delay({ pets: [{ id: "cat", display_name: "Cat" }], selected_id: "cat", enabled: true, home: "~/.wuu/pets", errors: [] });
  api.updateCodexPetSettings = async settings => delay({ pets: [{ id: "cat", display_name: "Cat" }], selected_id: settings.selected_id ?? "cat", enabled: settings.enabled ?? true, home: "~/.wuu/pets", errors: [] });
  api.listBrowserProfiles = async () => delay([
    { appName: "Chrome", profileName: "Default", profilePath: "/profiles/chrome-default", hasCookies: true },
  ]);
  api.listBrowserExtensions = async () => delay([]);
  api.importBrowserCookies = async () => delay({ imported: 0, discovered: 1, skippedExpired: 0, skippedInvalid: 0, failed: 0 });
  api.addBrowserExtension = async () => delay([]);
  api.setBrowserExtensionEnabled = async () => delay([]);
  api.removeBrowserExtension = async () => delay([]);
  api.listMCPServers = async () => delay({ servers: [{ name: "filesystem", state: "connected", connected: true, tools: 3 }] });
  api.listArchivedThreads = async () => delay({ threads: [] });
  return api;
};
