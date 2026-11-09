import { useCallback, useMemo, type ReactNode } from "react";

import type { InitializeResult, RuntimeAdvancedSettingsUpdate, RuntimeGeneralSettingsUpdate } from "../../shared/protocol";
import { SETTINGS_ACTIONS, type SettingsPageSummaryV1, type SettingsSnapshotV1 } from "../../shared/workbench";
import { useApplicationWorkbench } from "./ApplicationWorkbenchContext";
import type { ApplicationWorkbenchV1 } from "../../../../packages/plugin-sdk/src/ui-contributions";
import { PluginPresentation } from "./PluginPresentation";

export const SETTINGS_PRESENTATION_ACTIONS = Object.freeze([
  SETTINGS_ACTIONS.openPage,
  SETTINGS_ACTIONS.updateValue,
  SETTINGS_ACTIONS.refresh,
]);

export const SETTINGS_UPDATE_KEYS = Object.freeze({
  maxSteps: "runtime.maxSteps",
  temperature: "runtime.temperature",
  autoCompact: "runtime.autoCompact",
  gitAttributionEnabled: "general.gitAttributionEnabled",
} as const);

interface SettingsPresentationProps {
  controller?: ApplicationWorkbenchV1;
  initialized?: InitializeResult;
  activePageId: string;
  availablePages: readonly SettingsPageSummaryV1[];
  runningProviderNames?: readonly string[];
  busy: boolean;
  hasError: boolean;
  fallback: ReactNode;
  onOpenPage(pageId: string): void;
  onAdvancedSave(settings: RuntimeAdvancedSettingsUpdate): Promise<void>;
  onGeneralSave(settings: RuntimeGeneralSettingsUpdate): Promise<void>;
  onRefresh(): Promise<void>;
}

export function SettingsPresentation(props: SettingsPresentationProps): ReactNode {
  const controller = useApplicationWorkbench(props.controller);
  const snapshot = useMemo(() => createSettingsSnapshot(props), [
    props.activePageId,
    props.availablePages,
    props.busy,
    props.hasError,
    props.initialized,
    props.runningProviderNames,
  ]);
  const dispatchAction = useCallback(async (action: string, input?: unknown): Promise<unknown> => {
    switch (action) {
      case SETTINGS_ACTIONS.openPage: {
        const pageId = readOpenPageInput(input);
        if (!props.availablePages.some((page) => page.id === pageId && page.disabled !== true)) {
          throw new Error(`Settings page is unavailable: ${pageId}`);
        }
        props.onOpenPage(pageId);
        return undefined;
      }
      case SETTINGS_ACTIONS.updateValue:
        return updateSettingsValue(input, props.onAdvancedSave, props.onGeneralSave);
      case SETTINGS_ACTIONS.refresh:
        readRefreshInput(input);
        await props.onRefresh();
        return undefined;
      default:
        throw new Error(`Unsupported settings action: ${action}`);
    }
  }, [props.availablePages, props.onAdvancedSave, props.onGeneralSave, props.onOpenPage, props.onRefresh]);

  return (
    <PluginPresentation
      host={controller.registry}
      controller={controller}
      target="settings"
      snapshot={snapshot}
      fallback={props.fallback}
      actions={SETTINGS_PRESENTATION_ACTIONS}
      dispatchAction={dispatchAction}
    />
  );
}

export function createSettingsSnapshot({ initialized, activePageId, availablePages, runningProviderNames, busy, hasError }: Pick<SettingsPresentationProps, "initialized" | "activePageId" | "availablePages" | "runningProviderNames" | "busy" | "hasError">): SettingsSnapshotV1 {
  const runningProviders = new Set((runningProviderNames ?? []).map((name) => name.trim()).filter(Boolean));
  const plugins = (initialized?.extension_inventory ?? [])
    .filter((extension) => extension.kind === "plugin")
    .map((extension) => Object.freeze({
      id: extension.id,
      name: extension.name,
      enabled: extension.enabled !== false,
      status: extension.runtime_state ?? extension.state,
    }));
  const runtimes = initialized ? [Object.freeze({
    id: "core",
    label: initialized.runtime_host?.kind === "cloud" ? "Wuu Cloud Runtime" : "Wuu Local Runtime",
    version: initialized.core?.version,
    status: initialized.status,
  })] : [];
  const providers = (initialized?.providers ?? []).map((provider) => Object.freeze({
    id: provider.name,
    label: provider.name,
    configured: provider.api_key_configured === true,
    status: runningProviders.has(provider.name) ? "running" : "available",
  }));
  return Object.freeze({
    contractVersion: 1,
    activePageId,
    availablePages: Object.freeze(availablePages.map((page) => Object.freeze({
      id: page.id,
      label: page.label,
      ...(page.description === undefined ? {} : { description: page.description }),
      ...(page.disabled === undefined ? {} : { disabled: page.disabled }),
    }))),
    plugins: Object.freeze(plugins),
    runtimes: Object.freeze(runtimes),
    providers: Object.freeze(providers),
    busy,
    ...(hasError ? { error: "A settings operation failed." } : {}),
  });
}

function readOpenPageInput(input: unknown): string {
  const record = requireRecord(input, "settings.open-page input");
  if (typeof record.pageId !== "string" || record.pageId.trim() !== record.pageId || !record.pageId) {
    throw new Error("settings.open-page requires a non-empty pageId");
  }
  return record.pageId;
}

function readRefreshInput(input: unknown): void {
  if (input === undefined) return;
  const record = requireRecord(input, "settings.refresh input");
  if (record.scope !== "model-catalog" || Object.keys(record).length !== 1) {
    throw new Error("settings.refresh only supports the model-catalog scope");
  }
}

async function updateSettingsValue(input: unknown, onAdvancedSave: (settings: RuntimeAdvancedSettingsUpdate) => Promise<void>, onGeneralSave: (settings: RuntimeGeneralSettingsUpdate) => Promise<void>): Promise<void> {
  const record = requireRecord(input, "settings.update-value input");
  if (typeof record.key !== "string" || !("value" in record)) throw new Error("settings.update-value requires key and value");
  switch (record.key) {
    case SETTINGS_UPDATE_KEYS.maxSteps:
      await onAdvancedSave({ max_steps: requireInteger(record.value, record.key, 0, 1000) }); return;
    case SETTINGS_UPDATE_KEYS.temperature:
      await onAdvancedSave({ temperature: requireNumber(record.value, record.key, 0, 2) }); return;
    case SETTINGS_UPDATE_KEYS.autoCompact:
      await onAdvancedSave({ disable_auto_compact: !requireBoolean(record.value, record.key) }); return;
    case SETTINGS_UPDATE_KEYS.gitAttributionEnabled:
      await onGeneralSave({ git_attribution_enabled: requireBoolean(record.value, record.key) }); return;
    default:
      throw new Error(`Unsupported settings value: ${record.key}`);
  }
}

function requireRecord(input: unknown, label: string): Record<string, unknown> {
  if (typeof input !== "object" || input === null || Array.isArray(input)) throw new Error(`${label} must be an object`);
  return input as Record<string, unknown>;
}

function requireBoolean(value: unknown, key: string): boolean {
  if (typeof value !== "boolean") throw new Error(`${key} must be a boolean`);
  return value;
}

function requireNumber(value: unknown, key: string, minimum: number, maximum: number): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value < minimum || value > maximum) throw new Error(`${key} must be between ${minimum} and ${maximum}`);
  return value;
}

function requireInteger(value: unknown, key: string, minimum: number, maximum: number): number {
  const number = requireNumber(value, key, minimum, maximum);
  if (!Number.isInteger(number)) throw new Error(`${key} must be an integer`);
  return number;
}
