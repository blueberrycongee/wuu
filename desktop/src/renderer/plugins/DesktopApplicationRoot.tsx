import { useEffect, useSyncExternalStore, useState, useRef } from "react";
import { desktopPluginHost, useDesktopPluginRuntime } from "./DesktopPluginRuntime";
import { desktopBootstrap } from "./DesktopBootstrap";
import { ApplicationShellOutlet } from "./ApplicationShell";
import { dismissToast, showErrorToast } from "../Toast";
import { useI18n } from "../i18n";
import { RuntimeLoading, ViewSwitchLoading } from "../LoadingViews";
import { ConversationPresentationBoundary } from "../ConversationPresentationTransition";

export function DesktopApplicationRoot(): JSX.Element {
  const reportFailure = useSyncExternalStore(listener => desktopPluginHost.subscribe(listener), desktopPluginHost.getActivationReportFailure);
  const { t } = useI18n();
  const notice = useRef<number | undefined>(undefined);
  const hasShownApplication = useRef(false);
  const state = useSyncExternalStore(desktopBootstrap.subscribe, desktopBootstrap.getSnapshot, desktopBootstrap.getSnapshot);
  useEffect(() => desktopBootstrap.start(), []);
  const selection = state.initialized?.application_surfaces?.["ui.application"];
  const selectionKey = selection && `${selection.plugin_id}\0${selection.generation}\0${selection.provider_id}`;
  const [selectionReady, setSelectionReady] = useState<{ key: string | undefined; error?: string }>();
  useEffect(() => {
    let active = true;
    void desktopPluginHost.selectApplicationBinding(selection ? { pluginId: selection.plugin_id, generation: selection.generation, id: selection.provider_id } : undefined)
      .then(() => { if (active) setSelectionReady({ key: selectionKey }); })
      .catch(error => { if (active) setSelectionReady({ key: selectionKey, error: String(error) }); });
    return () => { active = false; };
  }, [selectionKey]);
  // Publish the desired graph binding before modules prepare; never infer a provider from inventory order.
  const inventory = selectionReady?.key === selectionKey ? state.initialized?.extension_inventory : undefined;
  const pluginsPending = useDesktopPluginRuntime(inventory, state.initialized?.features?.safe_mode, state.connectionGeneration);
  useSyncExternalStore(desktopPluginHost.applicationProviders.subscribe, desktopPluginHost.applicationProviders.getRevision);
  const businessFailure = desktopPluginHost.applicationProviders.failure;
  const failure = state.error ?? selectionReady?.error ?? businessFailure ?? reportFailure;
  const failureMessage = state.error ? t("plugins.connectionUnavailable")
    : reportFailure && !businessFailure && !selectionReady?.error ? t("plugins.activationReportUnavailable") : t("plugins.applicationUnavailable");
  useEffect(() => {
    // Preparation and its subsequent IPC report describe one incident, not two user actions.
    // Keep graph identities and stack details in the existing plugin diagnostics.
    if (failure && !pluginsPending) {
      const id = showErrorToast(failureMessage, "", "application-activation");
      if (id !== undefined) notice.current = id;
    }
    else if (!failure && notice.current !== undefined) { dismissToast(notice.current); notice.current = undefined; }
  }, [failure, failureMessage, pluginsPending]);
  const active = Boolean(desktopPluginHost.applicationProviders.current);
  useEffect(() => { if (active) hasShownApplication.current = true; }, [active]);
  const shell = state.initialized?.application_shell;
  const binding = shell ? { pluginId: shell.plugin_id, generation: shell.generation, id: shell.provider_id } : null;
  const pending = pluginsPending || !state.initialized && !state.error || selectionReady?.key !== selectionKey;
  useEffect(() => {
    if (pending || !state.initialized) return;
    desktopBootstrap.settleRestoration(state.connectionGeneration,
      failure ?? (!active ? "Selected application provider is unavailable" : undefined));
  }, [pending, state.initialized, state.connectionGeneration, active, failure]);
  const wrap = (content: React.ReactNode) => <ConversationPresentationBoundary binding={desktopPluginHost.applicationProviders.getBindingRevision()} retiring={!active && pending} failed={Boolean(failure && !pending)} getRetirement={desktopPluginHost.applicationProviders.getRetirement}>{content}</ConversationPresentationBoundary>;
  if (!state.initialized || !active) {
    // A stopped writer must not remain interactive. Reuse the existing text-free
    // transition surface rather than flashing launch/error copy on normal reloads.
    if (pending && hasShownApplication.current) return wrap(<ViewSwitchLoading />);
    return wrap(<RuntimeLoading fullscreen status={pending ? "connecting" : failureMessage} onRetry={window.wuu?.openRecovery ? undefined : () => window.location.reload()} onRecovery={pending || !window.wuu?.openRecovery ? undefined : () => {
      void window.wuu.openRecovery!().catch(() => showErrorToast(t("loading.recoveryUnavailable"), "", "application-recovery"));
    }} />);
  }
  const surfaceBindings = Object.fromEntries(Object.entries(state.initialized.application_surfaces ?? {})
    .map(([service, selected]) => [service, { pluginId: selected.plugin_id, generation: selected.generation, id: selected.provider_id }]));
  return wrap(<ApplicationShellOutlet key={desktopPluginHost.applicationProviders.getBindingRevision()} host={desktopPluginHost} binding={binding} surfaceBindings={surfaceBindings} pending={pluginsPending} />);
}
