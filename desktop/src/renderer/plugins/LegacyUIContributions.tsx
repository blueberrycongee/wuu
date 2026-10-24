import * as React from "react";
import type { ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ConversationItemSnapshotV1, HeaderSnapshotV1, PresentationHost, PresenterProps, ViewHostAPI } from "../../shared/workbench";
import { HEADER_ACTIONS, FILE_PREVIEW_ACTIONS, CONVERSATION_ITEM_ACTIONS } from "../../shared/workbench";
import { PLUGIN_SLOT_IDS, type PluginHost, type PluginSlotId, type RegisteredPresenter } from "./PluginHost";
import { PluginSlotContribution } from "./PluginSlot";
import { PluginSurface } from "./PluginSurface";
import { PluginConversationCards } from "./PluginConversationCards";
import { PluginCommandActions } from "./PluginCommandActions";
import { createPluginTranslator } from "./pluginI18n";
import { useI18n } from "../i18n";
import { dispatchFilePreviewAction } from "./FilePreviewPresentation";
import { toConversationItemSnapshot, toConversationProcessSnapshot } from "./ApplicationSnapshots";
import { toToolActivitySnapshot } from "./ToolActivityPresenter";

export interface LegacyViewIdentity {
  readonly pluginId: string;
  readonly generation: string;
  readonly viewTypeId?: string;
  readonly tabId?: string;
  readonly context?: Readonly<Record<string, unknown>>;
}
export interface LegacyWorkbenchRenderInput {
  readonly application: ApplicationServiceV1;
  readonly context: Readonly<Record<string, unknown>>;
}
export interface LegacyUIContributionsProps {
  readonly host: PluginHost;
  /** The declared semantic slot ID, independent of the declaring plugin's ID. */
  readonly semanticSlot: string;
  readonly application: ApplicationServiceV1;
  readonly context: Readonly<Record<string, unknown>>;
  readonly fallback?: React.ReactNode;
  readonly createViewHost: (identity: LegacyViewIdentity) => ViewHostAPI;
  readonly renderLegacyWorkbench?: (input: LegacyWorkbenchRenderInput) => React.ReactNode;
}

type Identity = Pick<LegacyViewIdentity, "pluginId" | "generation">;
const activationKeys = new WeakMap<object, number>();
let nextKey = 1;
function activationKey(host: PluginHost, identity: Identity): number {
  const activation = host.getGenerationActivation(identity.pluginId, identity.generation);
  if (!activation) return 0;
  let key = activationKeys.get(activation);
  if (!key) { key = nextKey++; activationKeys.set(activation, key); }
  return key;
}
function useRegistry<T>(host: PluginHost, read: () => T): T {
  return React.useSyncExternalStore(listener => host.subscribe(listener), read, read);
}
function Lifetime({ host, identity, children }: { host: PluginHost; identity: Identity; children: React.ReactNode }): React.ReactNode {
  const key = activationKey(host, identity);
  React.useEffect(() => host.retainGeneration(identity.pluginId, identity.generation), [host, identity.pluginId, identity.generation, key]);
  return children;
}
function retain(host: PluginHost, identities: readonly Identity[], child: React.ReactNode): React.ReactNode {
  return identities.reduceRight((content, identity) => <Lifetime key={activationKey(host, identity)} host={host} identity={identity}>{content}</Lifetime>, child);
}

/** Called only for a declared non-service slot by the host's composition boundary. */
export function LegacyUIContributions(props: LegacyUIContributionsProps): React.ReactNode {
  const { semanticSlot } = props;
  if (semanticSlot === "workbench.layers") {
    if (!props.renderLegacyWorkbench) throw new Error("Legacy workbench host is unavailable");
    return props.renderLegacyWorkbench({ application: props.application, context: props.context });
  }
  if ((PLUGIN_SLOT_IDS as readonly string[]).includes(semanticSlot)) return <LegacySlots {...props} id={semanticSlot as PluginSlotId} />;
  if (semanticSlot === "workspace.tools") return <WorkspaceTools {...props} />;
  if (semanticSlot === "workspace.view") return <WorkspaceView {...props} />;
  if (semanticSlot === "conversation.cards") return <Cards {...props} />;
  if (semanticSlot === "conversation.message.actions") return <MessageActions {...props} />;
  if (["conversation.message", "conversation.timeline", "conversation.process", "conversation.tool-activity", "header.workspace", "header.conversation", "content.preview"].includes(semanticSlot)) {
    return <Presenters {...props} />;
  }
  return props.fallback;
}

function LegacySlots(props: LegacyUIContributionsProps & { id: PluginSlotId }): React.ReactNode {
  const records = useRegistry(props.host, () => props.host.getSlotSnapshot(props.id));
  return <>{props.fallback}{records.map(record => <Lifetime key={`${activationKey(props.host, record)}:${record.id}`} host={props.host} identity={record}>
    <PluginSlotContribution host={props.host} id={props.id} contribution={record} context={props.context} />
  </Lifetime>)}</>;
}
function Cards(props: LegacyUIContributionsProps): React.ReactNode {
  const cards = React.useSyncExternalStore(listener => props.host.subscribeConversationCards(listener), () => props.host.getConversationCards(), () => props.host.getConversationCards());
  const threadId = props.context.threadId;
  if (typeof threadId !== "string") return props.fallback;
  return <>{props.fallback}{retain(props.host, cards.filter(card => card.threadId === threadId), <PluginConversationCards host={props.host} threadId={threadId} />)}</>;
}

type Mutable<T> = T extends readonly (infer V)[] ? Mutable<V>[] : T extends object ? { -readonly [K in keyof T]: Mutable<T[K]> } : T;
function mutableCopy<T>(value: T): Mutable<T> { return structuredClone(value) as Mutable<T>; }
function sourceItem(snapshot: ApplicationSnapshotV1, context: Readonly<Record<string, unknown>>) {
  const threads = snapshot.conversationWorkspace.threads;
  const thread = threads.find(value => value.id === context.threadId);
  const turns = thread?.turns ?? (snapshot.sideConversation.entry?.summary?.side_thread_id === context.threadId ? snapshot.sideConversation.turns : []);
  const turn = turns.find(value => value.id === context.turnId);
  const item = turn?.items.find(value => value.id === context.itemId);
  return { thread, turn, item };
}
function itemSnapshot(snapshot: ApplicationSnapshotV1, context: Readonly<Record<string, unknown>>): ConversationItemSnapshotV1 | undefined {
  const { item } = sourceItem(snapshot, context);
  if (!item) return undefined;
  const kind = item.type === "user_message" ? "user-message" : item.type === "agent_message" ? "assistant-message"
    : item.type === "reasoning" ? "reasoning" : item.type === "error" || item.type === "context_compaction" ? "notice" : undefined;
  return kind ? toConversationItemSnapshot(mutableCopy(item), kind) : undefined;
}
function MessageActions(props: LegacyUIContributionsProps): React.ReactNode {
  const snapshot = React.useSyncExternalStore(props.application.subscribe, props.application.getSnapshot, props.application.getSnapshot);
  const commands = useRegistry(props.host, () => props.host.getCommands());
  const item = itemSnapshot(snapshot, props.context);
  if (!item || typeof props.context.turnId !== "string") return props.fallback;
  const context = Object.freeze({ contractVersion: 1 as const, target: "conversation.message.actions" as const,
    threadId: typeof props.context.threadId === "string" ? props.context.threadId : undefined, turnId: props.context.turnId, item });
  const applicable = props.host.resolveCommandActions(context).actions;
  if (!applicable.length) return props.fallback;
  return retain(props.host, commands, <PluginCommandActions host={props.host} context={context} />);
}

function WorkspaceTools(props: LegacyUIContributionsProps): React.ReactNode {
  const entries = useRegistry(props.host, () => props.host.getWorkspaceTools());
  return <>{props.fallback}{entries.map(entry => <WorkspaceTool key={`${activationKey(props.host, entry)}:${entry.id}`} {...props} entry={entry} />)}</>;
}
function WorkspaceTool({ entry, ...props }: LegacyUIContributionsProps & { entry: ReturnType<PluginHost["getWorkspaceTools"]>[number] }): React.ReactNode {
  const [error, setError] = React.useState<string>();
  const activation = React.useMemo(() => props.host.getGenerationActivation(entry.pluginId, entry.generation), [props.host, entry]);
  return <Lifetime host={props.host} identity={entry}><button type="button" className="workspace-tool-menu-item" onClick={() => {
    if (props.host.getGenerationActivation(entry.pluginId, entry.generation) !== activation) return;
    void props.application.invoke("preview.open-plugin", { entryId: entry.id, pluginId: entry.pluginId, generation: entry.generation,
      viewTypeId: entry.view, title: entry.title, icon: entry.icon }).catch(error => setError(String(error)));
  }}>{entry.title}</button>{error && <span role="alert">{error}</span>}</Lifetime>;
}

class LegacyBoundary extends React.Component<{ host: PluginHost; identity: Identity; fallback?: React.ReactNode; onFailure?: (error: unknown) => void; children: React.ReactNode }, { failed: boolean }> {
  state = { failed: false };
  private observer?: ReturnType<PluginHost["observeGenerationRender"]>;
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidMount() { this.observer = this.props.host.observeGenerationRender(this.props.identity.pluginId, this.props.identity.generation); }
  componentDidCatch(error: Error) { this.observer?.failed(error); this.props.onFailure?.(error); }
  componentWillUnmount() { this.observer?.dispose(); }
  render() { return this.state.failed ? this.props.fallback : this.props.children; }
}
function WorkspaceView(props: LegacyUIContributionsProps): React.ReactNode {
  const definitions = useRegistry(props.host, () => props.host.getViewTypes());
  const { pluginId, generation, viewTypeId, tabId } = props.context;
  const definition = definitions.find(view => view.pluginId === pluginId && view.generation === generation && view.id === viewTypeId);
  if (!definition || typeof tabId !== "string") return props.fallback ?? <div role="status">Plugin view is unavailable</div>;
  return <ViewContent key={`${activationKey(props.host, definition)}:${tabId}`} {...props} definition={definition} tabId={tabId} />;
}
function ViewContent({ definition, tabId, ...props }: LegacyUIContributionsProps & { definition: ReturnType<PluginHost["getViewTypes"]>[number]; tabId: string }): React.ReactNode {
  const { locale } = useI18n();
  const commands = useRegistry(props.host, () => props.host.getCommands());
  const identity = React.useMemo(() => ({ pluginId: definition.pluginId, generation: definition.generation, viewTypeId: definition.id, tabId, context: props.context }), [definition, tabId, props.context]);
  const api = React.useMemo(() => capturedHost(props, identity), [props.host, props.application, props.createViewHost, identity]);
  const translate = React.useMemo(() => createPluginTranslator(props.host, locale), [props.host, locale, definition]);
  const commandContext = React.useMemo(() => Object.freeze({ contractVersion: 1 as const, target: "view.title" as const,
    viewId: tabId, viewTypeId: definition.id, viewPluginId: definition.pluginId, region: "auxiliary" as const }), [tabId, definition]);
  const View = definition.render;
  return <Lifetime host={props.host} identity={definition}><LegacyBoundary host={props.host} identity={definition} fallback={props.fallback}>
    <div className="plugin-view-content" data-wuu-plugin={definition.pluginId} data-wuu-view={definition.id}>
      {retain(props.host, commands, <PluginCommandActions host={props.host} context={commandContext} />)}
      <View host={api} context={props.context} locale={locale} translate={translate} />
    </div>
  </LegacyBoundary></Lifetime>;
}

function capturedHost(props: LegacyUIContributionsProps, identity: LegacyViewIdentity): ViewHostAPI {
  const activation = props.host.getGenerationActivation(identity.pluginId, identity.generation);
  const assertActive = () => {
    if (!activation || props.host.getGenerationActivation(identity.pluginId, identity.generation) !== activation) throw new Error("Plugin host context is no longer active");
  };
  const base = props.createViewHost(identity);
  return Object.freeze({
    getStorage: async (...args: Parameters<ViewHostAPI["getStorage"]>) => { assertActive(); const result = await base.getStorage(...args); assertActive(); return result; },
    setStorage: async (...args: Parameters<ViewHostAPI["setStorage"]>) => { assertActive(); const result = await base.setStorage(...args); assertActive(); return result; },
    getSetting: async (...args: Parameters<ViewHostAPI["getSetting"]>) => { assertActive(); const result = await base.getSetting(...args); assertActive(); return result; },
    executeCommand: async (...args: Parameters<ViewHostAPI["executeCommand"]>) => { assertActive(); const result = await base.executeCommand(...args); assertActive(); return result; },
    openView: async (viewTypeId: string, options?: Parameters<ViewHostAPI["openView"]>[1]) => {
      assertActive();
      if (options?.region && options.region !== "auxiliary") return base.openView(viewTypeId, options);
      const entry = props.host.getWorkspaceTools().find(entry => entry.pluginId === identity.pluginId && entry.generation === identity.generation && entry.view === viewTypeId);
      if (!entry) throw new Error("Workspace view is not declared by this generation");
      await props.application.invoke("preview.open-plugin", { entryId: entry.id, pluginId: entry.pluginId, generation: entry.generation, viewTypeId, title: entry.title, icon: entry.icon });
    },
    closeView: async () => { assertActive(); if (identity.tabId) await props.application.invoke("preview.close-tab", { tabId: identity.tabId }); else await base.closeView(); },
  });
}

function Presenters(props: LegacyUIContributionsProps): React.ReactNode {
  const snapshot = React.useSyncExternalStore(props.application.subscribe, props.application.getSnapshot, props.application.getSnapshot);
  const { item, turn } = sourceItem(snapshot, props.context);
  const target = props.semanticSlot === "conversation.message" ? "conversation.item" : props.semanticSlot;
  let value: unknown = props.context.snapshot;
  let key: string | undefined;
  if (target === "conversation.item") { value = itemSnapshot(snapshot, props.context); key = (value as ConversationItemSnapshotV1 | undefined)?.kind; }
  if (target === "conversation.process" && turn) value = toConversationProcessSnapshot(mutableCopy(turn.items.filter(item => Array.isArray(props.context.itemIds) && props.context.itemIds.includes(item.id))), turn.status === "in_progress", true, snapshot.locale);
  if (target === "conversation.tool-activity" && item) { value = toToolActivitySnapshot(mutableCopy(item), snapshot.locale); key = item.display?.capability ?? item.name; }
  if (target === "content.preview") key = (value as { contentType?: string } | undefined)?.contentType;
  const presenters = useRegistry(props.host, () => props.host.getPresenters(target, key));
  const surfaceId = props.semanticSlot === "conversation.message" || props.semanticSlot === "conversation.timeline" ? props.semanticSlot : undefined;
  const surfaces = useRegistry(props.host, () => props.host.getSurfaceSnapshot(surfaceId ?? "conversation.message"));
  const chain = [...presenters.filter(record => record.mode === "wrap").reverse(), ...presenters.filter(record => record.mode === "replace").reverse()];
  const draw = (index: number, value: unknown): React.ReactNode => {
    const record = chain[index];
    if (!record) return props.fallback;
    return <PresenterContent key={`${activationKey(props.host, record)}:${record.id}`} {...props} record={record} target={target} value={value} presentationKey={key}
      downstream={draw(index + 1, value)} next={input => draw(index + 1, input === undefined ? value : input.snapshot)} />;
  };
  const content = value === undefined ? props.fallback : draw(0, value);
  return surfaceId ? retain(props.host, surfaces, <PluginSurface host={props.host} id={surfaceId} context={props.context} fallback={content} />) : content;
}
function PresenterContent({ record, target, value, presentationKey, downstream, next, ...props }: LegacyUIContributionsProps & {
  record: RegisteredPresenter; target: string; value: unknown; presentationKey?: string; downstream: React.ReactNode; next: PresenterProps["next"];
}): React.ReactNode {
  const actions = presenterActions(props, target, value);
  const api = React.useMemo(() => capturedHost(props, record), [props.host, props.application, props.createViewHost, record]);
  const activation = React.useMemo(() => props.host.getGenerationActivation(record.pluginId, record.generation), [props.host, record]);
  const host: PresentationHost = Object.freeze({ ...api, actions: Object.freeze(Object.keys(actions)), invoke: async (action: string, input?: unknown) => {
    if (props.host.getGenerationActivation(record.pluginId, record.generation) !== activation) throw new Error("Plugin host context is no longer active");
    const handler = actions[action]; if (!handler) throw new Error(`Unsupported presentation action: ${action}`);
    const result = await handler(input);
    if (props.host.getGenerationActivation(record.pluginId, record.generation) !== activation) throw new Error("Plugin host context is no longer active");
    return result;
  } });
  return <Lifetime host={props.host} identity={record}><LegacyBoundary host={props.host} identity={record} fallback={downstream} onFailure={error => props.host.recordPresenterFailure(record, error)}>
    <RenderPresenter record={record} props={{ contractVersion: 1, target, key: presentationKey, snapshot: value, host, fallback: downstream, original: props.fallback, next }} />
  </LegacyBoundary></Lifetime>;
}
function RenderPresenter({ record, props }: { record: RegisteredPresenter; props: PresenterProps }): React.ReactNode { return record.render(props); }
function presenterActions(props: LegacyUIContributionsProps, target: string, value: unknown): Record<string, (input?: unknown) => unknown> {
  if (target === "header.workspace") {
    const header = value as HeaderSnapshotV1;
    const selectedTab = (input: unknown) => {
      const id = (input as { tabId?: unknown } | undefined)?.tabId;
      const tab = header.tabs?.find(tab => tab.id === id);
      if (typeof id !== "string" || !tab) throw new Error("Header tab action requires an existing tabId");
      return tab;
    };
    return {
      [HEADER_ACTIONS.selectTab]: input => {
        const tab = selectedTab(input);
        return tab.kind === "conversation" ? props.application.invoke("preview.show-conversation", {})
          : props.application.invoke("preview.focus-tab", { tabId: tab.id });
      },
      [HEADER_ACTIONS.closeTab]: input => {
        const tab = selectedTab(input);
        if (tab.kind !== "conversation") return props.application.invoke("preview.close-tab", { tabId: tab.id });
      },
    };
  }
  if (target === "content.preview") {
    const resource = props.context.resource as { root: string; path: string } | undefined;
    if (!resource) return {};
    const callbacks = { open: () => props.application.previewContent.openFile(resource),
      ...(props.application.previewContent.capabilities.revealItem ? { reveal: () => props.application.previewContent.revealItem(resource) } : {}) };
    const actions = [FILE_PREVIEW_ACTIONS.open, ...(callbacks.reveal ? [FILE_PREVIEW_ACTIONS.reveal] : [])];
    return Object.fromEntries(actions.map(action => [action, (input: unknown) => dispatchFilePreviewAction(action, input, resource.path, callbacks)]));
  }
  if (target === "conversation.item" && (value as ConversationItemSnapshotV1)?.kind === "user-message"
    && typeof props.context.threadId === "string" && typeof props.context.turnId === "string" && typeof props.context.itemId === "string") {
    const source = { threadId: props.context.threadId, turnId: props.context.turnId, itemId: props.context.itemId };
    return { [CONVERSATION_ITEM_ACTIONS.edit]: input => {
      if (input !== undefined) throw new Error("conversation.item.edit does not accept input");
      return props.application.invoke("history.begin-edit", source);
    } };
  }
  return {};
}
