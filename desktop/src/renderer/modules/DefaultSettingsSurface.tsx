import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import type { ApplicationGenerationAPI, ApplicationServiceV1, UISlotRenderProps } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationSettingsLayoutV1 } from "../../../../packages/plugin-sdk/src/application-settings";
import type { EngineListResult, InitializeResult } from "../../shared/protocol";
import { SettingsShellRenderer, type SettingsShellRendererProps } from "../ConversationShellRenderers";
import { useArchiveDeletion } from "../useArchiveDeletion";
import { useSettingsRuntimeState } from "../SettingsRuntimeState";
import type { SettingsPage } from "../SettingsView";
import { showErrorToast } from "../Toast";
import { useI18n } from "../i18n";
import { UILayerPortal } from "../ui/layers/UILayerHost";
import { TopNotice } from "../TopNotice";
import { CircleAlert, RefreshCw } from "../WuuIcons";

export function DefaultSettingsSurface({ application, archive, onNoticeVisibilityChange, ...layout }: ApplicationSettingsLayoutV1 & {
  application: ApplicationServiceV1;
  onNoticeVisibilityChange: (visible: boolean) => void;
  archive: Pick<SettingsShellRendererProps, "archivedThreads" | "onUnarchiveThread" | "archiveDeletion" | "onDeleteAllArchivedThreads" | "onRetryArchiveDeletion">;
}): JSX.Element | null {
  const settings = useSyncExternalStore(application.subscribe, () => application.getSnapshot().settings);
  const runtime = useSettingsRuntimeState({ settingsOpen: settings.open, application });
  // Existing controls edit local drafts; copy the public immutable view at this boundary.
  const initialized = useMemo(() => structuredClone(settings.configuration) as InitializeResult | undefined, [settings.configuration]);
  const engines = useMemo(() => structuredClone(settings.engineInventory) as EngineListResult | undefined, [settings.engineInventory]);
  const { t } = useI18n();
  const [catalogNotice, setCatalogNotice] = useState<{ message: string; isError: boolean }>();
  const dismissCatalogNotice = useCallback(() => setCatalogNotice(undefined), []);
  useEffect(() => {
    onNoticeVisibilityChange(Boolean(catalogNotice));
    return () => onNoticeVisibilityChange(false);
  }, [catalogNotice, onNoticeVisibilityChange]);
  const active = useRef(true);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);
  useEffect(() => {
    if (!settings.open) return;
    const lifetime = new AbortController();
    void application.invoke("settings.refresh-engines", {}, { signal: lifetime.signal }).catch(() => { /* The service publishes discovery errors beside the engine controls. */ });
    return () => lifetime.abort();
  }, [application, settings.open, initialized?.workspace_root]);
  async function refreshModelCatalog(): Promise<void> {
    setCatalogNotice(undefined);
    try {
      const result = await application.invoke("settings.refresh-model-catalog", {});
      if (active.current) setCatalogNotice({ message: t("settings.modelCatalogUpdated", { count: result.modelCount }), isError: false });
    } catch {
      if (active.current) setCatalogNotice({ message: t("settings.modelCatalogUpdateFailed"), isError: true });
    }
  }
  return <>
    {catalogNotice && <UILayerPortal layer="notice"><TopNotice message={catalogNotice.message}
      icon={catalogNotice.isError ? CircleAlert : RefreshCw} isError={catalogNotice.isError}
      onDismiss={dismissCatalogNotice} dismissAriaLabel={t("common.closeNotice")} /></UILayerPortal>}
    {settings.open && <div className={layout.embedded ? "settings-workbench-layer" : "settings-standalone-layer"}>
      <SettingsShellRenderer {...layout} application={application} initialized={initialized} running={settings.runtimeSwitchPending}
        runningProviderNames={settings.runningProviderNames} engineInventory={engines} engineInventoryError={settings.engineInventoryError}
        page={(settings.activePageId ?? "providers") as SettingsPage}
        onPageChange={pageId => { void application.invoke("settings.open", { pageId }).catch(showErrorToast); }}
        onBack={() => { void application.invoke("settings.close", {}).catch(showErrorToast); }}
        onSave={(provider, model, effort, connection, variant) => application.invoke("settings.save-provider", { provider, model, effort, connection, variant })}
        onRemoveProvider={(provider, options) => application.invoke("settings.remove-provider", { provider, ...options })}
        onRefreshModelCatalog={refreshModelCatalog}
        onProvidersChanged={providers => { void application.invoke("settings.reconcile-connections", { providers }).catch(showErrorToast); }}
        onProviderModelsChanged={(provider, accountId, models) => { void application.invoke("settings.reconcile-account-models", { provider, accountId, models }).catch(showErrorToast); }}
        onRefreshEngineInventory={async () => structuredClone(await application.invoke("settings.refresh-engines", {})) as EngineListResult | undefined}
        onUpdateEngineInventory={async input => structuredClone(await application.invoke("settings.update-engine", input)) as EngineListResult}
        onAdvancedSave={input => application.invoke("settings.update-advanced", input)}
        onGeneralSave={input => application.invoke("settings.update-general", input)}
        {...archive}
        usage={runtime.settingsUsage} usageLoading={runtime.settingsUsageLoading} usageError={runtime.settingsUsageError}
        codexPets={runtime.codexPets} codexPetsLoading={runtime.codexPetsLoading} codexPetsError={runtime.codexPetsError}
        onCodexPetsRefresh={runtime.refreshCodexPets} onCodexPetsUpdate={runtime.updateCodexPets}
      />
    </div>}
  </>;
}

/** Settings owns its domain reads and mutations; the slot supplies only presentation geometry. */
export function activate(api: ApplicationGenerationAPI): void {
  function SettingsContribution({ application, context }: UISlotRenderProps): JSX.Element {
    const layout = context.layout as ApplicationSettingsLayoutV1;
    const settings = useSyncExternalStore(application.subscribe, () => application.getSnapshot().settings);
    const archiveDeletion = useArchiveDeletion({
      list: () => application.invoke("settings.list-archived", {}),
      remove: threadId => application.invoke("settings.delete-archived", { threadId }),
    });
    const onNoticeVisibilityChange = useCallback((visible: boolean) => layout.onNoticeVisibilityChange?.(visible), [layout.onNoticeVisibilityChange]);
    return <DefaultSettingsSurface {...layout} application={application}
      onNoticeVisibilityChange={onNoticeVisibilityChange}
      archive={{ archivedThreads: settings.archivedThreads,
        onUnarchiveThread: thread => { void application.invoke("settings.unarchive", { threadId: thread.id }).catch(showErrorToast); },
        archiveDeletion, onDeleteAllArchivedThreads: () => void archiveDeletion.removeAll(),
        onRetryArchiveDeletion: () => void archiveDeletion.retry(),
      }} />;
  }
  api.registerUIContribution({ id: "ui.settings", target: "wuu.default-shell/application.settings", render: SettingsContribution });
}
