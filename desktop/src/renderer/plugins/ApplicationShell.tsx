import { LegacyUIContributions } from "./LegacyUIContributions";
import { RuntimeLoading } from "../LoadingViews";
import { useI18n } from "../i18n";
import { createPluginTranslator } from "./pluginI18n";
import * as React from "react";
import type { UICompositionV1, UISlotProps } from "../../../../packages/plugin-sdk/src/ui";
import type { PluginHost, RegisteredApplicationShell, RegisteredUIContribution } from "./PluginHost";

export interface ApplicationShellBinding {
  readonly pluginId: string;
  readonly generation: string;
  readonly id: string;
}

const activationKeys = new WeakMap<object, number>();
let nextActivationKey = 1;
function activationKey(activation: object): number {
  let key = activationKeys.get(activation);
  if (key === undefined) { key = nextActivationKey++; activationKeys.set(activation, key); }
  return key;
}

const SurfaceBindings = React.createContext<Readonly<Record<string, ApplicationShellBinding>>>({});
export const UICompositionPendingContext = React.createContext(false);

const SlotAncestors = React.createContext<readonly string[]>([]);

export function createUIComposition(host: PluginHost): UICompositionV1 {
  const createViewHost = host.createLegacyViewHost.bind(host);
  const renderLegacyWorkbench = host.renderLegacyWorkbench.bind(host);
  function Slot({ name, context = {}, fallback, layout: Layout }: UISlotProps): React.ReactNode {
    const registered = React.useSyncExternalStore(listener => host.subscribe(listener), host.getUICompositionSnapshot, host.getUICompositionSnapshot);
    const ancestors = React.useContext(SlotAncestors);
    if (ancestors.includes(name)) throw new Error(`UI slot cycle: ${[...ancestors, name].join(" → ")}`);
    // A contribution cannot recreate a slot after its defining provider unloads.
    const slot = host.getUISlot(name);
    const bindings = React.useContext(SurfaceBindings);
    const pending = React.useContext(UICompositionPendingContext);
    if (!slot) return null;
    let contributions = host.getUIContributions(name);
    if (slot.service) {
      const binding = bindings[slot.service];
      // The graph selects a surface independently of the layout that renders it.
      contributions = binding ? registered.filter(item => item.pluginId === binding.pluginId
        && item.generation === binding.generation && item.id === binding.id) : [];
      if (contributions.length !== 1 || contributions[0].mode === "wrap") return binding && pending ? <RuntimeLoading status="opening" />
        : <div role="alert">Selected UI surface is unavailable: {slot.service}</div>;
    }
    const contentContributions = contributions.filter(contribution => contribution.mode !== "wrap");
    const items = contentContributions.map(contribution => ({
      key: String(activationKey(contribution)), pluginId: contribution.pluginId, id: contribution.id, title: contribution.title ?? contribution.id,
      node: <ShellBoundary key={activationKey(contribution)}
        identity={`${contribution.pluginId}/${contribution.id}`} host={host} pluginId={contribution.pluginId} generation={contribution.generation}>
        <Contribution host={host} contribution={contribution} slots={composition} context={context} />
      </ShellBoundary>,
    }));
    const content = items.length === 0 ? fallback : Layout && !slot.service ? <Layout items={items} context={context} /> : items.map(item => item.node);
    let composed = slot.service || slot.id === "workbench.layers" && contentContributions.length > 0 ? content
      : <LegacyUIContributions key={activationKey(slot)} host={host} semanticSlot={slot.id}
          application={slot.application} context={context} fallback={content} createViewHost={createViewHost} renderLegacyWorkbench={renderLegacyWorkbench} />;
    for (const contribution of contributions.filter(contribution => contribution.mode === "wrap")) {
      composed = <ShellBoundary key={activationKey(contribution)} recover fallback={composed}
        identity={`${contribution.pluginId}/${contribution.id}`} host={host} pluginId={contribution.pluginId} generation={contribution.generation}>
        <Contribution host={host} contribution={contribution} slots={composition} context={context}>{composed}</Contribution>
      </ShellBoundary>;
    }
    return <SlotAncestors.Provider value={[...ancestors, name]}>{composed}</SlotAncestors.Provider>;
  }
  const composition: UICompositionV1 = Object.freeze({ Slot: React.memo(Slot) });
  return composition;
}

function Contribution({ host, contribution, slots, context, children }: {
  host: PluginHost; contribution: RegisteredUIContribution; slots: UICompositionV1; context: Readonly<Record<string, unknown>>; children?: React.ReactNode;
}): React.ReactNode {
  React.useEffect(() => host.retainGeneration(contribution.pluginId, contribution.generation), [host, contribution]);
  const { locale } = useI18n();
  const translate = React.useMemo(() => createPluginTranslator(host, locale), [host, locale]);
  const View = contribution.render;
  return <View application={contribution.application} slots={slots} context={context} locale={locale} translate={translate}>{children}</View>;
}

class ShellBoundary extends React.Component<{ children: React.ReactNode; identity: string; host: PluginHost; pluginId: string; generation: string; recover?: boolean; fallback?: React.ReactNode }, { error?: Error }> {
  private observer?: ReturnType<PluginHost["observeGenerationRender"]>;
  componentDidMount(): void {
    this.observer = this.props.host.observeGenerationRender(this.props.pluginId, this.props.generation, this.state.error);
  }
  componentDidCatch(error: Error): void { this.observer?.failed(error); }
  componentWillUnmount(): void { this.observer?.dispose(); }
  state: { error?: Error } = {};
  static getDerivedStateFromError(error: Error): { error: Error } { return { error }; }
  render(): React.ReactNode {
    return this.state.error
      ? this.props.recover ? this.props.fallback : <div role="alert">{this.props.identity}: {this.state.error.message}</div>
      : this.props.children;
  }
}

function SelectedShell({ host, shell }: { host: PluginHost; shell: RegisteredApplicationShell }): React.ReactNode {
  const slots = React.useMemo(() => createUIComposition(host), [host]);
  React.useEffect(() => host.retainGeneration(shell.pluginId, shell.generation), [host, shell]);
  const View = shell.render;
  return <View application={shell.application} slots={slots} />;
}

/** The selected graph binding is authoritative; absence never chooses a local default. */
export function ApplicationShellOutlet({ host, binding, surfaceBindings = {}, pending = false }: { host: PluginHost; binding: ApplicationShellBinding | null; surfaceBindings?: Readonly<Record<string, ApplicationShellBinding>>; pending?: boolean }): React.ReactNode {
  const shells = React.useSyncExternalStore(listener => host.subscribe(listener), host.getApplicationShells, host.getApplicationShells);
  const selected = binding && shells.find(shell => shell.pluginId === binding.pluginId
    && shell.generation === binding.generation && shell.id === binding.id);
  if (!selected) return <RuntimeLoading fullscreen onRetry={() => window.location.reload()}
    status={binding && pending ? "connecting" : binding ? `Selected application shell is unavailable: ${binding.pluginId}/${binding.id}@${binding.generation}` : "No application shell is selected."} />;
  // Activation identity changes even when a reload reuses the same code fingerprint.
  const activation = host.getGenerationActivation(selected.pluginId, selected.generation);
  return <ShellBoundary key={activationKey(activation!)} identity={`${selected.pluginId}/${selected.id}`} host={host} pluginId={selected.pluginId} generation={selected.generation}>
    <UICompositionPendingContext.Provider value={pending}>
      <SurfaceBindings.Provider value={surfaceBindings}><SelectedShell host={host} shell={selected} /></SurfaceBindings.Provider>
    </UICompositionPendingContext.Provider>
  </ShellBoundary>;
}
