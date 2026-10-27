import type { ReactNode } from "react";

import {
  HEADER_ACTIONS,
  type HeaderActionId,
  type HeaderSnapshotV1,
  type HeaderTabDescriptorV1,
} from "../../shared/workbench";
import { useApplicationWorkbench } from "./ApplicationWorkbenchContext";
import type { UIContributionRegistryV1 as PluginHost } from "../../../../packages/plugin-sdk/src/ui-contributions";
import { PluginPresentation } from "./PluginPresentation";
import type { ApplicationWorkbenchV1 as WorkbenchController } from "../../../../packages/plugin-sdk/src/ui-contributions";

export interface HeaderPresentationProps {
  enabled?: boolean;
  snapshot: HeaderSnapshotV1;
  fallback: ReactNode;
  onSelectTab?: (tabId: string) => void;
  onCloseTab?: (tabId: string) => void;
  onNavigateBack?: () => void;
  onNavigateForward?: () => void;
  host?: PluginHost;
  controller?: WorkbenchController;
}

export function immutableHeaderSnapshot(
  snapshot: Omit<HeaderSnapshotV1, "contractVersion">,
): HeaderSnapshotV1 {
  const tabs = snapshot.tabs?.map((tab) => Object.freeze({ ...tab }));
  return Object.freeze({
    contractVersion: 1,
    ...snapshot,
    ...(tabs === undefined ? {} : { tabs: Object.freeze(tabs) }),
  });
}

export function immutableHeaderTab(
  tab: HeaderTabDescriptorV1,
): HeaderTabDescriptorV1 {
  return Object.freeze({ ...tab });
}

export function HeaderPresentation({
  enabled = true,
  snapshot,
  fallback,
  onSelectTab,
  onCloseTab,
  onNavigateBack,
  onNavigateForward,
  host: suppliedHost,
  controller: suppliedController,
}: HeaderPresentationProps): JSX.Element {
  const controller = useApplicationWorkbench(suppliedController);
  const host = suppliedHost ?? controller.registry;
  if (!enabled) return <>{fallback}</>;
  const actions: HeaderActionId[] = [];
  if (onSelectTab) actions.push(HEADER_ACTIONS.selectTab);
  if (onCloseTab) actions.push(HEADER_ACTIONS.closeTab);
  if (onNavigateBack) actions.push(HEADER_ACTIONS.navigateBack);
  if (onNavigateForward) actions.push(HEADER_ACTIONS.navigateForward);

  const dispatchAction = (action: string, input?: unknown): void => {
    switch (action) {
      case HEADER_ACTIONS.selectTab:
        onSelectTab?.(validatedTabId(input, snapshot.tabs));
        return;
      case HEADER_ACTIONS.closeTab:
        onCloseTab?.(validatedTabId(input, snapshot.tabs));
        return;
      case HEADER_ACTIONS.navigateBack:
        onNavigateBack?.();
        return;
      case HEADER_ACTIONS.navigateForward:
        onNavigateForward?.();
        return;
      default:
        throw new Error(`Unsupported header action: ${action}`);
    }
  };

  return (
    <PluginPresentation
      host={host}
      controller={controller}
      target={`header.${snapshot.scope}`}
      snapshot={snapshot}
      fallback={fallback}
      actions={actions}
      dispatchAction={dispatchAction}
    />
  );
}

function validatedTabId(
  input: unknown,
  tabs: readonly HeaderTabDescriptorV1[] | undefined,
): string {
  if (typeof input !== "object" || input === null || !("tabId" in input)) {
    throw new Error("Header tab action requires a tabId");
  }
  const tabId = (input as { readonly tabId?: unknown }).tabId;
  if (typeof tabId !== "string" || !tabs?.some((tab) => tab.id === tabId)) {
    throw new Error("Header tab action requires an existing tabId");
  }
  return tabId;
}
