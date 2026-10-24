import { useEffect, useSyncExternalStore } from "react";
import { getDesktopApplication, type ShellInitializeResult } from "./ApplicationController";
import { desktopPluginHost, useDesktopPluginRuntime } from "./DesktopPluginRuntime";
import { ApplicationShellOutlet } from "./ApplicationShell";
import { dismissToast, showActionErrorToast } from "../Toast";
import { RuntimeLoading } from "../LoadingViews";

export function DesktopApplicationRoot(): JSX.Element {
  const reportFailure = useSyncExternalStore(listener => desktopPluginHost.subscribe(listener), desktopPluginHost.getActivationReportFailure);
  useEffect(() => {
    if (!reportFailure) return;
    const notice = showActionErrorToast(reportFailure);
    return () => { if (notice !== undefined) dismissToast(notice); };
  }, [reportFailure]);
  const desktopApplication = getDesktopApplication();
  const state = useSyncExternalStore(desktopApplication.subscribeState, desktopApplication.getState, desktopApplication.getState);
  useEffect(() => desktopApplication.start(), []);
  useDesktopPluginRuntime(state.initialized?.extension_inventory, state.initialized?.features?.safe_mode, state.runtimeConnectionGeneration ?? 0);
  const selection = (state.initialized as ShellInitializeResult | undefined)?.application_shell;
  const binding = selection ? { pluginId: selection.plugin_id, generation: selection.generation, id: selection.provider_id } : null;
  if (!state.initialized) return <RuntimeLoading fullscreen status={state.status} onRetry={() => window.location.reload()} />;
  const surfaceBindings = Object.fromEntries(Object.entries((state.initialized as ShellInitializeResult).application_surfaces ?? {})
    .map(([service, selected]) => [service, { pluginId: selected.plugin_id, generation: selected.generation, id: selected.provider_id }]));
  return <ApplicationShellOutlet host={desktopPluginHost} binding={binding} surfaceBindings={surfaceBindings} />;
}
