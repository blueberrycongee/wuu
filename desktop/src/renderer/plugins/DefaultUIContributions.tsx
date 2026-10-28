import { TruncatedText } from "../TruncatedText";
import * as React from "react";
import type { ApplicationServiceV1, ApplicationPresentationAPI, UISlotRenderProps } from "../../../../packages/plugin-sdk/src/ui";
import type { ConversationItemSnapshotV1, HeaderSnapshotV1, PresentationHost, PresenterProps, ViewHostAPI } from "../../shared/workbench";
import { HEADER_ACTIONS, FILE_PREVIEW_ACTIONS } from "../../shared/workbench";
import type { UIContributionRegistryV1 as PluginHost, RegisteredPresenterV1 as RegisteredPresenter } from "../../../../packages/plugin-sdk/src/ui-contributions";
import { PluginConversationCards } from "./PluginConversationCards";
import { PluginCommandActions } from "./PluginCommandActions";
import { MessageActionsPresenceContext } from "../MessageActions";
import { createPluginTranslator } from "./pluginI18n";
import { useI18n } from "../i18n";
import { dispatchFilePreviewAction } from "./FilePreviewPresentation";
import { showActionErrorToast } from "../Toast";
import { PluginIcon } from "../PublicIcon";

export interface ContributionViewIdentity {
  readonly pluginId: string;
  readonly generation: string;
  readonly viewTypeId?: string;
  readonly tabId?: string;
  readonly context?: Readonly<Record<string, unknown>>;
}
export interface WorkbenchRenderInput {
  readonly application: ApplicationServiceV1;
  readonly context: Readonly<Record<string, unknown>>;
}
export interface ContributionViewProps {
  readonly host: PluginHost;
  /** The declared semantic slot ID, independent of the declaring plugin's ID. */
  readonly semanticSlot: string;
  readonly application: ApplicationServiceV1;
  readonly context: Readonly<Record<string, unknown>>;
  readonly fallback?: React.ReactNode;
  readonly createViewHost: (identity: ContributionViewIdentity) => ViewHostAPI;
}

type Identity = Pick<ContributionViewIdentity, "pluginId" | "generation">;
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

/** Product behavior is explicitly registered by the owning surface. The composition
 * kernel knows no slot names and cannot silently restore disabled implementations. */
function registerView(api: ApplicationPresentationAPI, slot: string, View: React.ComponentType<ContributionViewProps>): void {
  api.registerUIContribution({ id: `default.${slot}`, target: `${api.manifestId}/${slot}`, mode: "wrap", order: -100,
    render: ({ application, context, children }: UISlotRenderProps) => <View host={api.contributions} semanticSlot={slot}
      application={application} context={context} fallback={children}
      createViewHost={identity => application.workbench.createViewHostAPI({
        id: identity.tabId ?? `renderer:${identity.pluginId}`, pluginId: identity.pluginId,
        generation: identity.generation, viewTypeId: identity.viewTypeId ?? "renderer", region: "primary",
        persistence: "session", context: identity.context ?? {},
      })} />,
  });
}
export function registerConversationContributions(api: ApplicationPresentationAPI): void {
  registerView(api, "conversation.cards", Cards);
  registerView(api, "conversation.message.actions", MessageActions);
}
export function registerPreviewContributions(api: ApplicationPresentationAPI): void {
  registerView(api, "workspace.tools", WorkspaceTools);
  registerView(api, "workspace.view", WorkspaceView);
  registerView(api, "content.preview", Presenters);
  registerView(api, "header.workspace", Presenters);
}
export function registerShellContributions(api: ApplicationPresentationAPI): void {
  registerView(api, "header.workspace", Presenters);
  registerView(api, "header.conversation", Presenters);
  api.registerUIContribution({ id: "default.workbench", target: `${api.manifestId}/workbench.layers`,
    render: ({ application, context }) => application.workbench.renderLayers({ settingsOnly: context.settingsOnly === true }),
  });
}

function Cards(props: ContributionViewProps): React.ReactNode {
  const cards = React.useSyncExternalStore(listener => props.host.subscribeConversationCards(listener), () => props.host.getConversationCards(), () => props.host.getConversationCards());
  const threadId = props.context.threadId;
  if (typeof threadId !== "string") return props.fallback;
  return <>{props.fallback}{retain(props.host, cards.filter(card => card.threadId === threadId), <PluginConversationCards host={props.host} threadId={threadId} />)}</>;
}

function MessageActions(props: ContributionViewProps): React.ReactNode {
  const commands = useRegistry(props.host, () => props.host.getCommands());
  const item = props.context.snapshot as ConversationItemSnapshotV1 | undefined;
  const context = item && typeof props.context.turnId === "string" ? Object.freeze({ contractVersion: 1 as const, target: "conversation.message.actions" as const,
    threadId: typeof props.context.threadId === "string" ? props.context.threadId : undefined, turnId: props.context.turnId, item }) : undefined;
  const present = !!context && props.host.resolveCommandActions(context).actions.length > 0;
  return <MessageActionsPresenceContext.Provider value={present}>
    {props.fallback}
    {present && context && retain(props.host, commands, <PluginCommandActions host={props.host} context={context} />)}
  </MessageActionsPresenceContext.Provider>;
}

function WorkspaceTools(props: ContributionViewProps): React.ReactNode {
  const entries = useRegistry(props.host, () => props.host.getWorkspaceTools());
  return <>{props.fallback}{entries.map(entry => <WorkspaceTool key={`${activationKey(props.host, entry)}:${entry.id}`} {...props} entry={entry} />)}</>;
}
function WorkspaceTool({ entry, ...props }: ContributionViewProps & { entry: ReturnType<PluginHost["getWorkspaceTools"]>[number] }): React.ReactNode {
  const { t } = useI18n();
  const activation = React.useMemo(() => props.host.getGenerationActivation(entry.pluginId, entry.generation), [props.host, entry]);
  return <Lifetime host={props.host} identity={entry}><button type="button" className="workspace-tool-menu-item" onClick={() => {
    if (props.host.getGenerationActivation(entry.pluginId, entry.generation) !== activation) return;
    void props.application.invoke("preview.open-plugin", { entryId: entry.id, pluginId: entry.pluginId, generation: entry.generation,
      viewTypeId: entry.view, title: entry.title, icon: entry.icon }).catch(error => showActionErrorToast(error, t("plugins.surfaceUnavailable")));
  }}>
    <span className="workspace-tool-menu-icon" aria-hidden="true">
      <PluginIcon loadIcon={props.application.catalog.loadIcon} icon={entry.icon} pluginId={entry.pluginId} fingerprint={entry.generation} className="icon" />
    </span>
    <span className="workspace-tool-menu-copy"><TruncatedText as="strong" text={entry.title} />{entry.description && <TruncatedText text={entry.description} />}</span>
  </button></Lifetime>;
}

class ContributionBoundary extends React.Component<{ host: PluginHost; identity: Identity; fallback?: React.ReactNode; onFailure?: (error: unknown) => void; children: React.ReactNode }, { failed: boolean }> {
  state = { failed: false };
  private observer?: ReturnType<PluginHost["observeGenerationRender"]>;
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidMount() { this.observer = this.props.host.observeGenerationRender(this.props.identity.pluginId, this.props.identity.generation); }
  componentDidCatch(error: Error) { this.observer?.failed(error); this.props.onFailure?.(error); }
  componentWillUnmount() { this.observer?.dispose(); }
  render() { return this.state.failed ? this.props.fallback : this.props.children; }
}
function WorkspaceView(props: ContributionViewProps): React.ReactNode {
  const definitions = useRegistry(props.host, () => props.host.getViewTypes());
  const { pluginId, generation, viewTypeId, tabId } = props.context;
  const definition = definitions.find(view => view.pluginId === pluginId && view.generation === generation && view.id === viewTypeId);
  if (!definition || typeof tabId !== "string") return props.fallback ?? <div role="status">Plugin view is unavailable</div>;
  return <ViewContent key={`${activationKey(props.host, definition)}:${tabId}`} {...props} definition={definition} tabId={tabId} />;
}
function ViewContent({ definition, tabId, ...props }: ContributionViewProps & { definition: ReturnType<PluginHost["getViewTypes"]>[number]; tabId: string }): React.ReactNode {
  const { locale } = useI18n();
  const commands = useRegistry(props.host, () => props.host.getCommands());
  const identity = React.useMemo(() => ({ pluginId: definition.pluginId, generation: definition.generation, viewTypeId: definition.id, tabId, context: props.context }), [definition, tabId, props.context]);
  const api = React.useMemo(() => capturedHost(props, identity), [props.host, props.application, props.createViewHost, identity]);
  const translate = React.useMemo(() => createPluginTranslator(props.host, locale), [props.host, locale, definition]);
  const commandContext = React.useMemo(() => Object.freeze({ contractVersion: 1 as const, target: "view.title" as const,
    viewId: tabId, viewTypeId: definition.id, viewPluginId: definition.pluginId, region: "auxiliary" as const }), [tabId, definition]);
  const View = definition.render;
  return <Lifetime host={props.host} identity={definition}><ContributionBoundary host={props.host} identity={definition} fallback={props.fallback}>
    <div className="plugin-view-content" data-wuu-plugin={definition.pluginId} data-wuu-view={definition.id}>
      {retain(props.host, commands, <PluginCommandActions host={props.host} context={commandContext} />)}
      <View host={api} context={props.context} locale={locale} translate={translate} />
    </div>
  </ContributionBoundary></Lifetime>;
}

function capturedHost(props: ContributionViewProps, identity: ContributionViewIdentity): ViewHostAPI {
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
      const definition = props.host.getViewTypes().find(view => view.pluginId === identity.pluginId && view.generation === identity.generation && view.id === viewTypeId);
      const region = options?.region ?? definition?.defaultRegion ?? "primary";
      const entry = identity.tabId && region === "auxiliary"
        ? props.host.getWorkspaceTools().find(entry => entry.pluginId === identity.pluginId && entry.generation === identity.generation && entry.view === viewTypeId)
        : undefined;
      // Preview tabs cannot represent instance context, persistence or reveal policy.
      if (!entry || options?.context !== undefined || options?.persistence !== undefined
        || options?.reveal !== undefined || definition?.persistence !== undefined) {
        await base.openView(viewTypeId, options);
      } else {
        await props.application.invoke("preview.open-plugin", { entryId: entry.id, pluginId: entry.pluginId, generation: entry.generation, viewTypeId, title: entry.title, icon: entry.icon });
      }
      assertActive();
    },
    closeView: async () => { assertActive(); if (identity.tabId) await props.application.invoke("preview.close-tab", { tabId: identity.tabId }); else await base.closeView(); },
  });
}

function Presenters(props: ContributionViewProps): React.ReactNode {
  const target = props.semanticSlot;
  const value: unknown = props.context.snapshot;
  let key: string | undefined;
  if (target === "content.preview") key = (value as { contentType?: string } | undefined)?.contentType;
  const presenters = useRegistry(props.host, () => props.host.getPresenters(target, key));
  const chain = [...presenters.filter(record => record.mode === "wrap").reverse(), ...presenters.filter(record => record.mode === "replace").reverse()];
  const draw = (index: number, value: unknown): React.ReactNode => {
    const record = chain[index];
    if (!record) return props.fallback;
    return <PresenterContent key={`${activationKey(props.host, record)}:${record.id}`} {...props} record={record} target={target} value={value} presentationKey={key}
      downstream={draw(index + 1, value)} next={input => draw(index + 1, input === undefined ? value : input.snapshot)} />;
  };
  return value === undefined ? props.fallback : draw(0, value);
}
function PresenterContent({ record, target, value, presentationKey, downstream, next, ...props }: ContributionViewProps & {
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
  return <Lifetime host={props.host} identity={record}><ContributionBoundary host={props.host} identity={record} fallback={downstream} onFailure={error => props.host.recordPresenterFailure(record, error)}>
    <RenderPresenter record={record} props={{ contractVersion: 1, target, key: presentationKey, snapshot: value, host, fallback: downstream, original: props.fallback, next }} />
  </ContributionBoundary></Lifetime>;
}
function RenderPresenter({ record, props }: { record: RegisteredPresenter; props: PresenterProps }): React.ReactNode { return record.render(props); }
function presenterActions(props: ContributionViewProps, target: string, value: unknown): Record<string, (input?: unknown) => unknown> {
  if (target === "header.workspace") {
    const header = value as HeaderSnapshotV1;
    const viewId = props.context.viewId;
    const selectedTab = (input: unknown) => {
      const id = (input as { tabId?: unknown } | undefined)?.tabId;
      const tab = header.tabs?.find(tab => tab.id === id);
      if (typeof id !== "string" || !tab) throw new Error("Header tab action requires an existing tabId");
      return tab;
    };
    return {
      ...(header.canNavigateBack && typeof viewId === "string" ? {
        [HEADER_ACTIONS.navigateBack]: () => {
          if (props.application.getSnapshot().navigationWorkspace.activePluginView?.id !== viewId) {
            throw new Error("Header view is no longer active");
          }
          return props.application.invoke("navigation.show-native", {});
        },
      } : {}),
      [HEADER_ACTIONS.selectTab]: input => {
        const tab = selectedTab(input);
        if (tab.kind === "conversation" && tab.closable !== undefined
          && props.application.getSnapshot().tabs.activeTabId !== tab.id) throw new Error("Conversation entry is no longer active");
        return tab.kind === "conversation" ? props.application.invoke("preview.show-conversation", {})
          : props.application.invoke("preview.focus-tab", { tabId: tab.id });
      },
      [HEADER_ACTIONS.closeTab]: input => {
        const tab = selectedTab(input);
        if (tab.kind === "conversation" && tab.closable === false) return;
        return tab.kind === "conversation" ? props.application.invoke("preview.close-conversation", { tabId: tab.id })
          : props.application.invoke("preview.close-tab", { tabId: tab.id });
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
  return {};
}
