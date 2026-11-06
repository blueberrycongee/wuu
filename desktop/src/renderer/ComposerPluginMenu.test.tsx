import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ComposerPluginChoices, type ComposerPluginMenu } from "./ComposerPluginMenu";
import { ComposerServicesProvider, type ComposerServices } from "./ComposerServicesContext";
import { I18nProvider, translateCurrent } from "./i18n";
import { clearToasts, ToastViewport } from "./Toast";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let container: HTMLDivElement;
let root: Root | null = null;

function composerServices(conversationPluginsSupported: boolean): ComposerServices {
  return {
    slots: {} as ComposerServices["slots"],
    ownerTabId: "tab",
    presentation: {} as ComposerServices["presentation"],
    view: {} as ComposerServices["view"],
    catalog: { getSnapshot: () => ({ inventory: [] }) } as unknown as ComposerServices["catalog"],
    settingsControls: { listMCPServers: async () => ({ servers: [] }) } as unknown as ComposerServices["settingsControls"],
    conversationPluginsSupported,
  };
}

function pluginMenu(): ComposerPluginMenu {
  return {
    id: "menu", available: true, open: true, query: "", fromMention: false,
    setOpen: () => {}, close: () => {}, observe: () => {}, choose: () => {},
    handleKeyDown: () => false, inputElement: () => null,
  };
}

async function renderChoices(supported: boolean): Promise<void> {
  await act(async () => {
    root = createRoot(container);
    root.render(
      <I18nProvider>
        <ComposerServicesProvider value={composerServices(supported)}>
          <WuuUIRoot>
            <ToastViewport />
            <ComposerPluginChoices menu={pluginMenu()} />
          </WuuUIRoot>
        </ComposerServicesProvider>
      </I18nProvider>,
    );
  });
}

describe("ComposerPluginChoices engine support notice", () => {
  beforeEach(() => {
    clearToasts();
    container = document.createElement("div");
    document.body.appendChild(container);
  });

  afterEach(() => {
    act(() => root?.unmount());
    container.remove();
    clearToasts();
    root = null;
  });

  it("surfaces the unsupported engine notice as a toast instead of in the menu", async () => {
    await renderChoices(false);
    const notice = document.body.querySelector('[data-wuu-component="notice"]');
    expect(notice?.textContent).toContain(translateCurrent("composer.pluginsEngineUnsupported"));
    const notices = Array.from(container.querySelectorAll(".composer-plugin-notice"));
    expect(notices.some(item => item.textContent?.includes(translateCurrent("composer.pluginsEngineUnsupported")))).toBe(false);
  });

  it("does not toast when the engine supports conversation plugins", async () => {
    await renderChoices(true);
    expect(document.body.querySelector('[data-wuu-component="notice"]')).toBeNull();
  });
});
