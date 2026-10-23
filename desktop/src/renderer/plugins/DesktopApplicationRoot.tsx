import { useEffect, useSyncExternalStore } from "react";
import { getDesktopApplication, type ShellInitializeResult } from "./ApplicationController";
import { desktopPluginHost, useDesktopPluginRuntime } from "./DesktopPluginRuntime";
import { ApplicationShellOutlet } from "./ApplicationShell";

export function DesktopApplicationRoot(): JSX.Element {
  const desktopApplication = getDesktopApplication();
  const state = useSyncExternalStore(desktopApplication.subscribeState, desktopApplication.getState, desktopApplication.getState);
  useEffect(() => desktopApplication.start(), []);
  useDesktopPluginRuntime(state.initialized?.extension_inventory, state.initialized?.features?.safe_mode);
  const selection = (state.initialized as ShellInitializeResult | undefined)?.application_shell;
  const binding = selection ? { pluginId: selection.plugin_id, generation: selection.generation, id: selection.provider_id } : null;
  if (!state.initialized) return <div className="application-recovery" role={state.status === "connecting" ? "status" : "alert"}>{state.status === "connecting" ? "Connecting…" : state.status}</div>;
  return <ApplicationShellOutlet host={desktopPluginHost} binding={binding} />;
}
