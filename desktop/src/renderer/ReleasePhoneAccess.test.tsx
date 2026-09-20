import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { WuuDesktopApi } from "../shared/protocol";

let root: Root | undefined;
let container: HTMLDivElement | undefined;
const originalHost = window.wuu;

afterEach(() => {
  act(() => root?.unmount());
  root = undefined;
  container?.remove();
  window.wuu = originalHost;
  vi.unstubAllEnvs();
  vi.resetModules();
});

describe.each([undefined, "true"])("production phone access (build override: %s)", (override) => {
  it("keeps settings usable without exposing or contacting phone/account services", async () => {
    vi.stubEnv("DEV", false);
    vi.stubEnv("VITE_ENABLE_ACCOUNT", override);
    vi.stubEnv("VITE_ENABLE_REMOTE_CONTROL", override);
    vi.resetModules();
    const { SidebarAccountMenu } = await import("./SidebarAccountMenu");
    const { SettingsView } = await import("./SettingsView");
    const { translateCurrent: t } = await import("./i18n");
    const { PhoneNavigationContext } = await import("./PhoneNavigationContext");
    const remoteAccount = vi.fn().mockResolvedValue({ username: "test-user" });
    const getRemoteControlSnapshot = vi.fn().mockResolvedValue({ status: null, host_running: false, pair_uri: null });
    const onRemoteControlEvent = vi.fn(() => () => {});
    window.wuu = {
      remoteAccount,
      getRemoteControlSnapshot,
      onRemoteControlEvent,
      getBuildInfo: vi.fn().mockResolvedValue({}),
      listMCPServers: vi.fn().mockResolvedValue({ servers: [] }),
    } as unknown as WuuDesktopApi;
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    const onOpenSettings = vi.fn();
    const onOpenAccount = vi.fn();
    const openDevices = vi.fn();
    await act(async () => root!.render(
      <PhoneNavigationContext.Provider value={{ openDevices }}>
        <SidebarAccountMenu disabled={false} onOpenSettings={onOpenSettings} onOpenAccount={onOpenAccount} />
      </PhoneNavigationContext.Provider>,
    ));
    await act(async () => container!.querySelector<HTMLButtonElement>(".sidebar-account-trigger")!.click());
    const menuItems = Array.from(container.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'));
    expect(menuItems.map(item => item.textContent)).toEqual([t("settings.usage"), t("sidebar.settings")]);
    expect(remoteAccount).not.toHaveBeenCalled();
    await act(async () => menuItems.find(item => item.textContent === t("sidebar.settings"))!.click());
    expect(onOpenSettings).toHaveBeenCalledWith("providers");
    expect(onOpenAccount).not.toHaveBeenCalled();
    expect(openDevices).not.toHaveBeenCalled();

    const pets = { home: "", enabled: false, selected_id: "", pets: [], errors: [] };
    const engines = { engines: [] };
    await act(async () => root!.render(
      <SettingsView
        initialPage="remote"
        running={false}
        codexPetsLoading={false}
        codexPetsError=""
        sidebarWidth={280}
        sidebarMinWidth={240}
        sidebarMaxWidth={480}
        resizingSidebar={false}
        sidebarCollapsed={false}
        sidebarAnimating={false}
        sidebarMotionMs={0}
        onToggleSidebar={vi.fn()}
        onBack={vi.fn()}
        onSave={vi.fn()}
        onRemoveProvider={vi.fn()}
        onRefreshModelCatalog={vi.fn()}
        onRefreshEngineInventory={vi.fn().mockResolvedValue(engines)}
        onUpdateEngineInventory={vi.fn().mockResolvedValue(engines)}
        onAdvancedSave={vi.fn()}
        onGeneralSave={vi.fn()}
        onCodexPetsRefresh={vi.fn().mockResolvedValue(pets)}
        onCodexPetsUpdate={vi.fn().mockResolvedValue(pets)}
        onSidebarResizeStart={vi.fn()}
        onSidebarSeparatorKey={vi.fn()}
        onUnarchiveThread={vi.fn()}
      />,
    ));
    const navigation = container.querySelector('[data-wuu-component="settings-navigation"]')!;
    expect(navigation.textContent).not.toContain(t("settings.remote"));
    expect(container.querySelector('[data-testid="settings-remote-page"]')).toBeNull();
    expect(container.querySelector(".settings-nav-item.active")?.textContent).toBe(t("settings.providers"));
    expect(getRemoteControlSnapshot).not.toHaveBeenCalled();
    expect(onRemoteControlEvent).not.toHaveBeenCalled();
  });
});
