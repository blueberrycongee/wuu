import { useContext, useMemo, useSyncExternalStore, type ReactNode } from "react";
import { ArtifactThreadContext } from "../ArtifactPreviewContext";

import type { ThreadItem } from "../../shared/protocol";
import { useI18n } from "../i18n";
import { toolDisplayLabel } from "../ToolActivityHelpers";
import type { ToolActivitySnapshot, ToolActivityStructuredResult } from "../../shared/workbench";
import { desktopPluginHost, desktopWorkbenchController } from "./DesktopPluginRuntime";
import type { PluginHost } from "./PluginHost";
import { PluginPresentation } from "./PluginPresentation";
import type { WorkbenchController } from "./Workbench";

export interface ToolActivityPresenterProps {
  item?: ThreadItem;
  fallback: ReactNode;
  host?: PluginHost;
  controller?: WorkbenchController;
}

export function ToolActivityPresenter({
  item,
  fallback,
  host = desktopPluginHost,
  controller = desktopWorkbenchController,
}: ToolActivityPresenterProps): JSX.Element {
  const { locale } = useI18n();
  const threadId = useContext(ArtifactThreadContext);
  const dispatchKey = item?.display?.capability ?? item?.name;
  return (
    <PluginPresentation
      enabled={item !== undefined && dispatchKey !== undefined}
      host={host}
      controller={controller}
      target="conversation.tool-activity"
      presentationKey={dispatchKey}
      snapshot={item === undefined ? EMPTY_TOOL_ACTIVITY_SNAPSHOT : toToolActivitySnapshot(item, locale, threadId)}
      fallback={fallback}
    />
  );
}

/** Registry-aware identities shared by turn layout and process rendering. */
export function useToolActivityPresentationIDs(items: readonly ThreadItem[]): ReadonlySet<string> {
  const ids = useSyncExternalStore(
    listener => desktopPluginHost.subscribe(listener),
    () => items.filter(item => item.type === "tool_call"
      && desktopPluginHost.getPresenters("conversation.tool-activity", item.display?.capability ?? item.name).length > 0)
      .map(item => item.id).join("\0"),
  );
  return useMemo(() => new Set(ids ? ids.split("\0") : []), [ids]);
}

const EMPTY_TOOL_ACTIVITY_SNAPSHOT = Object.freeze({});

/** Convert the private thread record into the complete public presenter DTO. */
export function toToolActivitySnapshot(item: ThreadItem, locale?: string, threadId?: string): ToolActivitySnapshot {
  const displayLabel = toolDisplayLabel(item.display, locale);
  const detail = item.result_detail;
  const structuredResult: ToolActivityStructuredResult | undefined = detail === undefined
    ? undefined
    : Object.freeze({
      content: detail.content === undefined
        ? undefined
        : Object.freeze(detail.content.map((part) => Object.freeze({
          type: part.type,
          ...(part.text === undefined ? {} : { text: part.text }),
          ...(part.data === undefined ? {} : { data: part.data }),
          ...(part.mime_type === undefined ? {} : { mimeType: part.mime_type }),
          ...(part.uri === undefined ? {} : { uri: part.uri }),
          ...(part.name === undefined ? {} : { name: part.name }),
          ...(part.resource === undefined ? {} : { resource: immutableValue(part.resource) }),
          ...(part.artifact === undefined ? {} : {
            artifact: Object.freeze({
              ...(part.artifact.placement === undefined ? {} : { placement: part.artifact.placement }),
              ...(part.artifact.ref === undefined ? {} : { ref: part.artifact.ref }),
              ...(part.artifact.sha256 === undefined ? {} : { sha256: part.artifact.sha256 }),
              ...(part.artifact.size_bytes === undefined ? {} : { sizeBytes: part.artifact.size_bytes }),
            }),
          }),
        }))),
      ...(detail.structured_content === undefined ? {} : { structuredContent: immutableValue(detail.structured_content) }),
      ...(detail.meta === undefined ? {} : { metadata: immutableValue(detail.meta) }),
      ...(detail.is_error === undefined ? {} : { isError: detail.is_error }),
      ...(detail.activity === undefined ? {} : {
        activity: Object.freeze({
          id: detail.activity.id,
          kind: detail.activity.kind,
          ...(detail.activity.state === undefined ? {} : { state: detail.activity.state }),
          ...(detail.activity.thread_id === undefined ? {} : { threadId: detail.activity.thread_id }),
          ...(detail.activity.preview_uri === undefined ? {} : { previewUri: detail.activity.preview_uri }),
        }),
      }),
    });
  return Object.freeze({
    contractVersion: 1,
    ...(threadId === undefined ? {} : { threadId }),
    ...(locale === undefined ? {} : { locale }),
    id: item.id,
    ...(item.source_id === undefined ? {} : { callId: item.source_id }),
    toolName: item.name ?? "",
    ...(displayLabel === undefined ? {} : { displayLabel }),
    ...(item.display?.capability === undefined ? {} : { capability: item.display.capability }),
    ...(item.display?.kind === undefined ? {} : { kind: item.display.kind }),
    status: item.status === "in_progress"
      ? "running"
      : item.status === "failed" ? "failed" : "completed",
    ...(item.arguments === undefined ? {} : { argumentsText: item.arguments }),
    ...(item.result === undefined ? {} : { resultText: item.result }),
    ...(structuredResult === undefined ? {} : { structuredResult }),
    ...(item.error === undefined ? {} : { error: item.error }),
  });
}

function immutableValue(value: unknown): unknown {
  if (Array.isArray(value)) return Object.freeze(value.map(immutableValue));
  if (typeof value === "object" && value !== null) {
    return Object.freeze(Object.fromEntries(
      Object.entries(value).map(([key, entry]) => [key, immutableValue(entry)]),
    ));
  }
  return value;
}
