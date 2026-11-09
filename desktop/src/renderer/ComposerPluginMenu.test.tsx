import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ComposerPluginChoices, type ComposerPluginMenu } from "./ComposerPluginMenu";
import { ComposerPluginServicesProvider, ComposerServicesProvider, type ComposerServices } from "./ComposerServicesContext";
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

  it("uses an independent conversation's MCP inventory instead of Coding's connections", async () => {
    const choose = vi.fn();
    const coding = composerServices(true);
    coding.settingsControls.listMCPServers = vi.fn().mockResolvedValue({ servers: [{ name: "coding-only", connected: true, tool_names: ["private"] }] });
    const personal = { catalog: coding.catalog, settingsControls: { listMCPServers: vi.fn().mockResolvedValue({ servers: [{ name: "shared", connected: true, plugin_active: true, tool_names: ["lookup"], authorization_id: "original-grant" }] }) }, conversationPluginsSupported: true, includePluginTools: false };
    await act(async () => {
      root = createRoot(container);
      root.render(<I18nProvider><ComposerServicesProvider value={coding}><ComposerPluginServicesProvider value={personal}><ComposerPluginChoices menu={{ ...pluginMenu(), choose }} /></ComposerPluginServicesProvider></ComposerServicesProvider></I18nProvider>);
    });
    expect(coding.settingsControls.listMCPServers).not.toHaveBeenCalled();
    expect(container.textContent).not.toContain("coding-only");
    const choice = container.querySelector<HTMLButtonElement>('[data-plugin-key="mcp:shared"]')!;
    act(() => choice.click());
    expect(choose).toHaveBeenCalledWith("@shared", expect.objectContaining({ mcp_server: "shared", authorization_id: "original-grant" }));
    const next = { ...personal, settingsControls: { listMCPServers: () => new Promise<{ servers: [] }>(() => {}) } };
    await act(async () => root!.render(<I18nProvider><ComposerServicesProvider value={coding}><ComposerPluginServicesProvider value={next}><ComposerPluginChoices menu={pluginMenu()} /></ComposerPluginServicesProvider></ComposerServicesProvider></I18nProvider>));
    expect(container.querySelector('[data-plugin-key="mcp:shared"]')).toBeNull();

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
