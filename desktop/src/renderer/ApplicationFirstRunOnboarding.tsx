import { useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ApplicationOnboardingWorkflowV1 } from "../../../packages/plugin-sdk/src/ui-onboarding";
import type { CatalogProviderSummary, EngineListResult, ExtensionInventoryRecord, ProviderSummary } from "../shared/protocol";
import { FirstRunOnboarding, type FirstRunOnboardingState } from "./FirstRunOnboarding";
import { SettingsControlsProvider } from "./SettingsControlsContext";
import { showErrorToast } from "./Toast";

function flowState(workflow: ApplicationOnboardingWorkflowV1 | undefined): FirstRunOnboardingState | undefined {
  return workflow ? structuredClone({ ...workflow, selectedPluginIDs: new Set(workflow.selectedPluginIDs), selectedPaths: new Set(workflow.selectedPaths) }) as FirstRunOnboardingState : undefined;
}

/** The default setup presentation consumes the same public services as another shell. */
export function ApplicationFirstRunOnboarding({ application }: {
  application: ApplicationServiceV1;
}): JSX.Element {
  const snapshot = useSyncExternalStore(application.subscribe, application.getSnapshot);
  const setup = useSyncExternalStore(application.onboarding.subscribe, application.onboarding.getSnapshot);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, [application]);
  const flow = useMemo(() => flowState(setup.workflow), [setup.workflow]);
  // Mutable component inputs are detached from the public immutable views.
  const configuration = useMemo(() => structuredClone(snapshot.settings.configuration), [snapshot.settings.configuration]);
  const engines = useMemo(() => structuredClone(snapshot.settings.engineInventory) as EngineListResult | undefined, [snapshot.settings.engineInventory]);
  const nextStep = application.settingsControls.capabilities.systemPermissions ? "permissions" : setup.capabilities.browserImport ? "browser" : "ready";
  return <SettingsControlsProvider controls={application.settingsControls}>
    <FirstRunOnboarding
      onboarding={application.onboarding}
      state={flow}
      onStateChange={update => {
        if (!mounted.current) return;
        const current = application.onboarding.getSnapshot();
        const next = typeof update === "function" ? update(flowState(current.workflow)) : update;
        if (next) application.onboarding.checkpoint({ revision: current.revision, workflow: {
          ...next, selectedPluginIDs: [...next.selectedPluginIDs], selectedPaths: [...next.selectedPaths],
        } });
      }}
      inventory={configuration?.extension_inventory as ExtensionInventoryRecord[] | undefined}
      providers={configuration?.providers as ProviderSummary[] | undefined}
      catalogProviders={configuration?.catalog_providers as CatalogProviderSummary[] | undefined}
      engines={engines}
      onUpdateExtensionPackage={update => application.onboarding.run({ kind: "plugins", update })}
      onSaveProvider={(provider, model, connection) => application.onboarding.run({ kind: "provider", input: { provider, model, connection }, nextStep })}
      onProvidersChanged={providers => { void application.invoke("settings.reconcile-connections", { providers }).catch(showErrorToast); }}
      onUpdateEngines={input => application.onboarding.run({ kind: "runtime", engine: input.default_engine! })}
      onImportBrowser={async profilePath => {
        await application.onboarding.run({ kind: "browser", profilePath });
        return application.onboarding.getSnapshot().workflow!.browserImportResults[profilePath];
      }}
      onComplete={paths => application.onboarding.complete({ paths: paths ?? [] })}
    />
  </SettingsControlsProvider>;
}
