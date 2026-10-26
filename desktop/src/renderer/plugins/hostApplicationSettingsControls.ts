import { loadPluginIconURL } from "../PublicIcon";
import { readBackground, updateBackground, observeBackground } from "../background/preferences";
import { processBackground } from "../background/image";
import { subscribeShellSkins, getShellSkins, selectShellSkin, loadSkinImage } from "../background/ShellSkin";
import { connectionDisplayPreferences, connectionDisplaySnapshot, subscribeConnectionDisplay } from "../ConnectionDisplayPreferences";
import { readDraftEngineMemory, writeDraftEngineMemory, clearDraftEngineMemory } from "../DraftEngineMemory";
import { appearanceDefaults, readAppearance, saveAppearance, observeAppearance } from "../AppearancePreferences";
import { listLocalFonts, isMonospaceFont } from "../LocalFonts";
import { applyMessageFlowFontSize } from "../MessageFlowFontSizeSection";
import { applyThemePreference, currentAppliedTheme, observeAppliedTheme, availableExtensionThemes, selectedExtensionThemeKey, selectExtensionTheme } from "../Theme";
import { MESSAGE_FLOW_FONT_SIZE_RANGE } from "../../shared/protocol";
import type { InitializeResult } from "../../shared/protocol";
import { createElement } from "react";
import type { ApplicationSettingsControlsV1, SettingsControlsDiagnosticV1 } from "../../../../packages/plugin-sdk/src/ui-settings-controls";
import type { UIContributionRegistryV1 as PluginHost, ApplicationWorkbenchV1 as WorkbenchController } from "../../../../packages/plugin-sdk/src/ui-contributions";
import { SettingsPresentation } from "./SettingsPresentation";
import { hostSupports } from "../HostCapabilities";
import { ENABLE_REMOTE_CONTROL } from "../FeatureFlags";

/** Host mechanisms only; constructing controls starts no reads or subscriptions. */
export function createApplicationSettingsControls(options: {
  host?: PluginHost;
  controller?: WorkbenchController;
} = {}): ApplicationSettingsControlsV1 {
  let skinSource: ReturnType<typeof getShellSkins> | undefined;
  let skinView: ReturnType<ApplicationSettingsControlsV1["getShellSkins"]>;
  const host = () => { if (!options.host) throw new Error("Application contribution registry is unavailable"); return options.host; };
  const controller = () => { if (!options.controller) throw new Error("Application workbench provider is unavailable"); return options.controller; };
  const diagnostics = new WeakMap<readonly unknown[], readonly SettingsControlsDiagnosticV1[]>();
  return {
    capabilities: Object.freeze({
      systemPermissions: window.wuu?.platform === "darwin" && typeof window.wuu?.getSystemPermissions === "function",
      permissionGuide: typeof window.wuu?.onSystemPermissionGuideChanged === "function",
      permissionGuideUpdate: typeof window.wuu?.updateSystemPermissionGuide === "function",
      permissionGuideClose: typeof window.wuu?.closeSystemPermissionGuide === "function",
      themeChanges: typeof window.wuu?.onThemePreferenceChange === "function",
      appContext: window.wuu?.platform === "darwin" && typeof window.wuu?.getAppContextState === "function",
      appContextChanges: typeof window.wuu?.onAppContextChanged === "function",
      appContextRecording: typeof window.wuu?.setAppContextShortcutRecording === "function",

      chatGPTLogin: hostSupports("startChatGPTLogin"),
      engineLogin: window.wuu?.hostKind !== "web",
      platform: window.wuu?.platform,
      browser: Boolean(window.wuu?.listBrowserProfiles && window.wuu.importBrowserCookies && window.wuu.listBrowserExtensions && window.wuu.addBrowserExtension && window.wuu.setBrowserExtensionEnabled && window.wuu.removeBrowserExtension),
      browserReload: typeof window.wuu?.reloadBrowserExtension === "function",
      browserOptions: typeof window.wuu?.openBrowserExtensionOptions === "function",
      remote: ENABLE_REMOTE_CONTROL && hostSupports("getRemoteControlSnapshot"),
      build: hostSupports("getBuildInfo"),
      catalog: hostSupports("listCatalogProviders") && typeof window.wuu?.listCatalogProviders === "function",
      engines: hostSupports("listEngines"),
      pets: hostSupports("listCodexPets"),
      diagnostics: typeof window.wuu?.getPluginDiagnostics === "function"
    }),
    currentAppliedTheme, observeAppliedTheme, availableExtensionThemes, selectedExtensionThemeKey, selectExtensionTheme,
    observeContentThemeSelection: listener => {
      window.addEventListener("wuu-theme-selection", listener);
      window.addEventListener("storage", listener);
      return () => { window.removeEventListener("wuu-theme-selection", listener); window.removeEventListener("storage", listener); };
    },
    connectionDisplay: Object.freeze({ ...connectionDisplayPreferences, subscribe: subscribeConnectionDisplay, getSnapshot: connectionDisplaySnapshot }),
    readDraftEngineMemory, writeDraftEngineMemory, clearDraftEngineMemory,
    loadPluginIconURL,
    readBackground, updateBackground, observeBackground, processBackground,
    subscribeShellSkins, selectShellSkin, loadSkinImage,
    getShellSkins: () => {
      const source = getShellSkins();
      if (source !== skinSource) {
        skinSource = source;
        skinView = Object.freeze({ selected: source.selected, skins: Object.freeze(source.skins.map(skin => Object.freeze({ ...skin, light: Object.freeze({ ...skin.light }), dark: Object.freeze({ ...skin.dark }) }))) });
      }
      return skinView;
    },
    initialMessageFlowFontSize: window.wuu?.initialMessageFlowFontSize ?? MESSAGE_FLOW_FONT_SIZE_RANGE.default,
    initialThemePreference: window.wuu?.initialThemePreference ?? "system",
    appearanceDefaults: Object.freeze({ ...appearanceDefaults }),
    readAppearance, saveAppearance, observeAppearance, applyMessageFlowFontSize, applyThemePreference,
    listLocalFonts: async () => { const names = await listLocalFonts(); return { names, monospace: names.filter(isMonospaceFont) }; },
    subscribe: listener => host().subscribe(listener),
    getPages: () => host().getSettingsPages(),
    getConflicts: () => host().getConflicts(),
    getDiagnostics: (id, generation) => {
      const source = host().getGenerationDiagnostics(id, generation);
      let view = diagnostics.get(source);
      if (!view) {
        view = Object.freeze(source.map(({
          kind,
          message
        }) => Object.freeze({
          kind,
          message
        })));
        diagnostics.set(source, view);
      }
      return view;
    },
    chooseConflict: async (key, id) => {
      const preferences = await window.wuu.setPluginConflictPreference(key, id);
      host().setConflictPreferences(preferences);
    },
    subscribeRemote: listener => window.wuu.onRemoteControlEvent(listener),
    renderPage: input => controller().renderView({
      pluginId: input.pluginId,
      viewTypeId: input.view,
      context: Object.freeze({
        surface: "settings"
      }),
      settings: input.settings,
      onFailure: input.onFailure
    }),
    // The legacy presentation adapter receives its own writable copy of the public view.
    renderPresentation: input => createElement(SettingsPresentation, {
      ...input, controller: controller(),
      initialized: input.initialized ? structuredClone(input.initialized) as InitializeResult : undefined
    }),
    listChatGPTAccounts: async (...args) => {
      if (typeof window.wuu?.listChatGPTAccounts !== "function") throw new Error("Settings operation unavailable: listChatGPTAccounts");
      return window.wuu.listChatGPTAccounts(...args);
    },
    signOutChatGPTAccount: async (...args) => {
      if (typeof window.wuu?.signOutChatGPTAccount !== "function") throw new Error("Settings operation unavailable: signOutChatGPTAccount");
      return window.wuu.signOutChatGPTAccount(...args);
    },
    startChatGPTLogin: async (...args) => {
      if (typeof window.wuu?.startChatGPTLogin !== "function") throw new Error("Settings operation unavailable: startChatGPTLogin");
      return window.wuu.startChatGPTLogin(...args);
    },
    pollChatGPTLogin: async (...args) => {
      if (typeof window.wuu?.pollChatGPTLogin !== "function") throw new Error("Settings operation unavailable: pollChatGPTLogin");
      return window.wuu.pollChatGPTLogin(...args);
    },
    cancelChatGPTLogin: async (...args) => {
      if (typeof window.wuu?.cancelChatGPTLogin !== "function") throw new Error("Settings operation unavailable: cancelChatGPTLogin");
      return window.wuu.cancelChatGPTLogin(...args);
    },
    listEngineAuthMethods: async (...args) => {
      if (typeof window.wuu?.listEngineAuthMethods !== "function") throw new Error("Settings operation unavailable: listEngineAuthMethods");
      return window.wuu.listEngineAuthMethods(...args);
    },
    authenticateEngine: async (...args) => {
      if (typeof window.wuu?.authenticateEngine !== "function") throw new Error("Settings operation unavailable: authenticateEngine");
      return window.wuu.authenticateEngine(...args);
    },
    cancelEngineAuth: async (...args) => {
      if (typeof window.wuu?.cancelEngineAuth !== "function") throw new Error("Settings operation unavailable: cancelEngineAuth");
      return window.wuu.cancelEngineAuth(...args);
    },
    getSystemPermissions: async (...args) => {
      if (typeof window.wuu?.getSystemPermissions !== "function") throw new Error("Settings operation unavailable: getSystemPermissions");
      return window.wuu.getSystemPermissions(...args);
    },
    requestSystemPermission: async (...args) => {
      if (typeof window.wuu?.requestSystemPermission !== "function") throw new Error("Settings operation unavailable: requestSystemPermission");
      return window.wuu.requestSystemPermission(...args);
    },
    updateSystemPermissionGuide: (...args) => {
      if (typeof window.wuu?.updateSystemPermissionGuide !== "function") throw new Error("Settings operation unavailable: updateSystemPermissionGuide");
      return window.wuu.updateSystemPermissionGuide(...args);
    },
    onSystemPermissionGuideChanged: (...args) => {
      if (typeof window.wuu?.onSystemPermissionGuideChanged !== "function") throw new Error("Settings operation unavailable: onSystemPermissionGuideChanged");
      return window.wuu.onSystemPermissionGuideChanged(...args);
    },
    closeSystemPermissionGuide: async (...args) => {
      if (typeof window.wuu?.closeSystemPermissionGuide !== "function") throw new Error("Settings operation unavailable: closeSystemPermissionGuide");
      return window.wuu.closeSystemPermissionGuide(...args);
    },
    getMessageFlowFontSize: async (...args) => {
      if (typeof window.wuu?.getMessageFlowFontSize !== "function") throw new Error("Settings operation unavailable: getMessageFlowFontSize");
      return window.wuu.getMessageFlowFontSize(...args);
    },
    setMessageFlowFontSize: async (...args) => {
      if (typeof window.wuu?.setMessageFlowFontSize !== "function") throw new Error("Settings operation unavailable: setMessageFlowFontSize");
      return window.wuu.setMessageFlowFontSize(...args);
    },
    getThemePreference: async (...args) => {
      if (typeof window.wuu?.getThemePreference !== "function") throw new Error("Settings operation unavailable: getThemePreference");
      return window.wuu.getThemePreference(...args);
    },
    setThemePreference: async (...args) => {
      if (typeof window.wuu?.setThemePreference !== "function") throw new Error("Settings operation unavailable: setThemePreference");
      return window.wuu.setThemePreference(...args);
    },
    onThemePreferenceChange: (...args) => {
      if (typeof window.wuu?.onThemePreferenceChange !== "function") throw new Error("Settings operation unavailable: onThemePreferenceChange");
      return window.wuu.onThemePreferenceChange(...args);
    },
    getAppContextState: async (...args) => {
      if (typeof window.wuu?.getAppContextState !== "function") throw new Error("Settings operation unavailable: getAppContextState");
      return window.wuu.getAppContextState(...args);
    },
    updateAppContextSettings: async (...args) => {
      if (typeof window.wuu?.updateAppContextSettings !== "function") throw new Error("Settings operation unavailable: updateAppContextSettings");
      return window.wuu.updateAppContextSettings(...args);
    },
    setAppContextShortcutRecording: async (...args) => {
      if (typeof window.wuu?.setAppContextShortcutRecording !== "function") throw new Error("Settings operation unavailable: setAppContextShortcutRecording");
      return window.wuu.setAppContextShortcutRecording(...args);
    },
    onAppContextChanged: (...args) => {
      if (typeof window.wuu?.onAppContextChanged !== "function") throw new Error("Settings operation unavailable: onAppContextChanged");
      return window.wuu.onAppContextChanged(...args);
    },
    discardAppContextSnapshot: async (...args) => {
      if (typeof window.wuu?.discardAppContextSnapshot !== "function") throw new Error("Settings operation unavailable: discardAppContextSnapshot");
      return window.wuu.discardAppContextSnapshot(...args);
    },
    getBuildInfo: async (...args) => {
      if (typeof window.wuu?.getBuildInfo !== "function") throw new Error("Settings operation unavailable: getBuildInfo");
      return window.wuu.getBuildInfo(...args);
    },
    useCodexCredentials: async (...args) => {
      if (typeof window.wuu?.useCodexCredentials !== "function") throw new Error("Settings operation unavailable: useCodexCredentials");
      return window.wuu.useCodexCredentials(...args);
    },
    loadCodexModels: async (...args) => {
      if (typeof window.wuu?.loadCodexModels !== "function") throw new Error("Settings operation unavailable: loadCodexModels");
      return window.wuu.loadCodexModels(...args);
    },
    refreshModelCatalog: async (...args) => {
      if (typeof window.wuu?.refreshModelCatalog !== "function") throw new Error("Settings operation unavailable: refreshModelCatalog");
      return window.wuu.refreshModelCatalog(...args);
    },
    listCatalogProviders: async (...args) => {
      if (typeof window.wuu?.listCatalogProviders !== "function") throw new Error("Settings operation unavailable: listCatalogProviders");
      return window.wuu.listCatalogProviders(...args);
    },
    listEngines: async (...args) => {
      if (typeof window.wuu?.listEngines !== "function") throw new Error("Settings operation unavailable: listEngines");
      return window.wuu.listEngines(...args);
    },
    getPluginSetting: async (...args) => {
      if (typeof window.wuu?.getPluginSetting !== "function") throw new Error("Settings operation unavailable: getPluginSetting");
      return window.wuu.getPluginSetting(...args);
    },
    setPluginSetting: async (...args) => {
      if (typeof window.wuu?.setPluginSetting !== "function") throw new Error("Settings operation unavailable: setPluginSetting");
      return window.wuu.setPluginSetting(...args);
    },
    getPluginDiagnostics: async (...args) => {
      if (typeof window.wuu?.getPluginDiagnostics !== "function") throw new Error("Settings operation unavailable: getPluginDiagnostics");
      return window.wuu.getPluginDiagnostics(...args);
    },
    listMCPServers: async (...args) => {
      if (typeof window.wuu?.listMCPServers !== "function") throw new Error("Settings operation unavailable: listMCPServers");
      return window.wuu.listMCPServers(...args);
    },
    connectMCPServer: async (...args) => {
      if (typeof window.wuu?.connectMCPServer !== "function") throw new Error("Settings operation unavailable: connectMCPServer");
      return window.wuu.connectMCPServer(...args);
    },
    disconnectMCPServer: async (...args) => {
      if (typeof window.wuu?.disconnectMCPServer !== "function") throw new Error("Settings operation unavailable: disconnectMCPServer");
      return window.wuu.disconnectMCPServer(...args);
    },
    refreshMCPServer: async (...args) => {
      if (typeof window.wuu?.refreshMCPServer !== "function") throw new Error("Settings operation unavailable: refreshMCPServer");
      return window.wuu.refreshMCPServer(...args);
    },
    startMCPAuth: async (...args) => {
      if (typeof window.wuu?.startMCPAuth !== "function") throw new Error("Settings operation unavailable: startMCPAuth");
      return window.wuu.startMCPAuth(...args);
    },
    finishMCPAuth: async (...args) => {
      if (typeof window.wuu?.finishMCPAuth !== "function") throw new Error("Settings operation unavailable: finishMCPAuth");
      return window.wuu.finishMCPAuth(...args);
    },
    removeMCPAuth: async (...args) => {
      if (typeof window.wuu?.removeMCPAuth !== "function") throw new Error("Settings operation unavailable: removeMCPAuth");
      return window.wuu.removeMCPAuth(...args);
    },
    listChatGPTModels: async (...args) => {
      if (typeof window.wuu?.listChatGPTModels !== "function") throw new Error("Settings operation unavailable: listChatGPTModels");
      return window.wuu.listChatGPTModels(...args);
    },
    startXAILogin: async (...args) => {
      if (typeof window.wuu?.startXAILogin !== "function") throw new Error("Settings operation unavailable: startXAILogin");
      return window.wuu.startXAILogin(...args);
    },
    pollXAILogin: async (...args) => {
      if (typeof window.wuu?.pollXAILogin !== "function") throw new Error("Settings operation unavailable: pollXAILogin");
      return window.wuu.pollXAILogin(...args);
    },
    cancelXAILogin: async (...args) => {
      if (typeof window.wuu?.cancelXAILogin !== "function") throw new Error("Settings operation unavailable: cancelXAILogin");
      return window.wuu.cancelXAILogin(...args);
    },
    listBrowserProfiles: async (...args) => {
      if (typeof window.wuu?.listBrowserProfiles !== "function") throw new Error("Settings operation unavailable: listBrowserProfiles");
      return window.wuu.listBrowserProfiles(...args);
    },
    importBrowserCookies: async (...args) => {
      if (typeof window.wuu?.importBrowserCookies !== "function") throw new Error("Settings operation unavailable: importBrowserCookies");
      return window.wuu.importBrowserCookies(...args);
    },
    listBrowserExtensions: async (...args) => {
      if (typeof window.wuu?.listBrowserExtensions !== "function") throw new Error("Settings operation unavailable: listBrowserExtensions");
      return window.wuu.listBrowserExtensions(...args);
    },
    addBrowserExtension: async (...args) => {
      if (typeof window.wuu?.addBrowserExtension !== "function") throw new Error("Settings operation unavailable: addBrowserExtension");
      return window.wuu.addBrowserExtension(...args);
    },
    setBrowserExtensionEnabled: async (...args) => {
      if (typeof window.wuu?.setBrowserExtensionEnabled !== "function") throw new Error("Settings operation unavailable: setBrowserExtensionEnabled");
      return window.wuu.setBrowserExtensionEnabled(...args);
    },
    reloadBrowserExtension: async (...args) => {
      if (typeof window.wuu?.reloadBrowserExtension !== "function") throw new Error("Settings operation unavailable: reloadBrowserExtension");
      return window.wuu.reloadBrowserExtension(...args);
    },
    openBrowserExtensionOptions: async (...args) => {
      if (typeof window.wuu?.openBrowserExtensionOptions !== "function") throw new Error("Settings operation unavailable: openBrowserExtensionOptions");
      return window.wuu.openBrowserExtensionOptions(...args);
    },
    removeBrowserExtension: async (...args) => {
      if (typeof window.wuu?.removeBrowserExtension !== "function") throw new Error("Settings operation unavailable: removeBrowserExtension");
      return window.wuu.removeBrowserExtension(...args);
    },
    getRemoteControlSnapshot: async (...args) => {
      if (typeof window.wuu?.getRemoteControlSnapshot !== "function") throw new Error("Settings operation unavailable: getRemoteControlSnapshot");
      return window.wuu.getRemoteControlSnapshot(...args);
    },
    setRemoteHostEnabled: async (...args) => {
      if (typeof window.wuu?.setRemoteHostEnabled !== "function") throw new Error("Settings operation unavailable: setRemoteHostEnabled");
      return window.wuu.setRemoteHostEnabled(...args);
    },
    startRemotePairing: async (...args) => {
      if (typeof window.wuu?.startRemotePairing !== "function") throw new Error("Settings operation unavailable: startRemotePairing");
      return window.wuu.startRemotePairing(...args);
    },
    removeRemoteDevice: async (...args) => {
      if (typeof window.wuu?.removeRemoteDevice !== "function") throw new Error("Settings operation unavailable: removeRemoteDevice");
      return window.wuu.removeRemoteDevice(...args);
    },
    openExternal: async (...args) => {
      if (typeof window.wuu?.openExternal !== "function") throw new Error("Settings operation unavailable: openExternal");
      return window.wuu.openExternal(...args);
    }
  };
}
