import { LegacyUIContributions } from "./LegacyUIContributions";
import { RuntimeLoading } from "../LoadingViews";
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

const SlotAncestors = React.createContext<readonly string[]>([]);

function compositionFor(host: PluginHost): UICompositionV1 {
  const createViewHost = host.createLegacyViewHost.bind(host);
  const renderLegacyWorkbench = host.renderLegacyWorkbench.bind(host);
  function Slot({ name, context = {}, fallback }: UISlotProps): React.ReactNode {
    const registered = React.useSyncExternalStore(listener => host.subscribe(listener), host.getUICompositionSnapshot, host.getUICompositionSnapshot);
    const ancestors = React.useContext(SlotAncestors);
    if (ancestors.includes(name)) throw new Error(`UI slot cycle: ${[...ancestors, name].join(" → ")}`);
    // A contribution cannot recreate a slot after its defining provider unloads.
    const slot = host.getUISlot(name);
    const bindings = React.useContext(SurfaceBindings);
    if (!slot) return null;
    let contributions = host.getUIContributions(name);
    if (slot.service) {
      const binding = bindings[slot.service];
      // The graph selects a surface independently of the layout that renders it.
      contributions = binding ? registered.filter(item => item.pluginId === binding.pluginId
        && item.generation === binding.generation && item.id === binding.id) : [];
      if (contributions.length !== 1) return <div role="alert">Selected UI surface is unavailable: {slot.service}</div>;
    }
    const content = contributions.length === 0 ? fallback : contributions.map(contribution => {
        return <ShellBoundary key={activationKey(contribution)}
          identity={`${contribution.pluginId}/${contribution.id}`} host={host} pluginId={contribution.pluginId} generation={contribution.generation}>
          <Contribution host={host} contribution={contribution} slots={composition} context={context} />
        </ShellBoundary>;
      });
    return <SlotAncestors.Provider value={[...ancestors, name]}>
      {slot.service || slot.id === "workbench.layers" && contributions.length > 0 ? content : <LegacyUIContributions key={activationKey(slot)} host={host} semanticSlot={slot.id}
        application={slot.application} context={context} fallback={content} createViewHost={createViewHost} renderLegacyWorkbench={renderLegacyWorkbench} />}
    </SlotAncestors.Provider>;
  }
  const composition: UICompositionV1 = Object.freeze({ Slot });
  return composition;
}

function Contribution({ host, contribution, slots, context }: {
  host: PluginHost; contribution: RegisteredUIContribution; slots: UICompositionV1; context: Readonly<Record<string, unknown>>;
}): React.ReactNode {
  React.useEffect(() => host.retainGeneration(contribution.pluginId, contribution.generation), [host, contribution]);
  const View = contribution.render;
  return <View application={contribution.application} slots={slots} context={context} />;
}

class ShellBoundary extends React.Component<{ children: React.ReactNode; identity: string; host: PluginHost; pluginId: string; generation: string }, { error?: Error }> {
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
      ? <div role="alert">{this.props.identity}: {this.state.error.message}</div>
      : this.props.children;
  }
}

function SelectedShell({ host, shell }: { host: PluginHost; shell: RegisteredApplicationShell }): React.ReactNode {
  const slots = React.useMemo(() => compositionFor(host), [host]);
  React.useEffect(() => host.retainGeneration(shell.pluginId, shell.generation), [host, shell]);
  const View = shell.render;
  return <View application={shell.application} slots={slots} />;
}

/** The selected graph binding is authoritative; absence never chooses a local default. */
export function ApplicationShellOutlet({ host, binding, surfaceBindings = {} }: { host: PluginHost; binding: ApplicationShellBinding | null; surfaceBindings?: Readonly<Record<string, ApplicationShellBinding>> }): React.ReactNode {
  const shells = React.useSyncExternalStore(listener => host.subscribe(listener), host.getApplicationShells, host.getApplicationShells);
  const selected = binding && shells.find(shell => shell.pluginId === binding.pluginId
    && shell.generation === binding.generation && shell.id === binding.id);
  if (!selected) return <RuntimeLoading fullscreen onRetry={() => window.location.reload()}
    status={binding ? `Selected application shell is unavailable: ${binding.pluginId}/${binding.id}@${binding.generation}` : "No application shell is selected."} />;
  // Activation identity changes even when a reload reuses the same code fingerprint.
  const activation = host.getGenerationActivation(selected.pluginId, selected.generation);
  return <ShellBoundary key={activationKey(activation!)} identity={`${selected.pluginId}/${selected.id}`} host={host} pluginId={selected.pluginId} generation={selected.generation}>
    <SurfaceBindings.Provider value={surfaceBindings}><SelectedShell host={host} shell={selected} /></SurfaceBindings.Provider>
  </ShellBoundary>;
}
