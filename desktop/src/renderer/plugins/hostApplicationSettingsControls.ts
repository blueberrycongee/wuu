import type { InitializeResult } from "../../shared/protocol";
import { createElement } from "react";
import type { ApplicationSettingsControlsV1, SettingsControlsDiagnosticV1 } from "../../../../packages/plugin-sdk/src/ui-settings-controls";
import { desktopPluginHost, desktopWorkbenchController } from "./DesktopPluginRuntime";
import type { PluginHost } from "./PluginHost";
import { PluginViewContent, type WorkbenchController } from "./Workbench";
import { SettingsPresentation } from "./SettingsPresentation";
import { hostSupports } from "../HostCapabilities";
import { ENABLE_REMOTE_CONTROL } from "../FeatureFlags";

/** Host mechanisms only; constructing controls starts no reads or subscriptions. */
export function createApplicationSettingsControls(options: {
  host?: PluginHost;
  controller?: WorkbenchController;
} = {}): ApplicationSettingsControlsV1 {
  const host = () => options.host ?? desktopPluginHost;
  const diagnostics = new WeakMap<readonly unknown[], readonly SettingsControlsDiagnosticV1[]>();
  return {
    capabilities: Object.freeze({
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
    renderPage: input => createElement(PluginViewContent, {
      controller: options.controller ?? desktopWorkbenchController,
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
      ...input,
      initialized: input.initialized ? structuredClone(input.initialized) as InitializeResult : undefined
    }),
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
