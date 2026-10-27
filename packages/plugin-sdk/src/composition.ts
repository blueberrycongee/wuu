import type { ProductComposition, RuntimeHost, ServiceBinding, ServiceRequirement, ServiceRegistrySnapshot } from "./index.js";

export const COMPOSITION_SERVICE = "host.composition";

export function requireCompositionService(): ServiceRequirement {
  return { name: COMPOSITION_SERVICE, major_version: 1, required: true };
}

export interface CompositionCreate {
  /** Capture the parent's actual modules and bindings before applying selection overrides. */
  readonly inherit?: boolean;
  /** Required unless inherit is true. Inheritance cannot be combined with a profile.
   * Version 4's minimal profile selects only a session host: select an engine
   * explicitly for product turns; host-managed run also needs its loop services. */
  readonly selection?: ProductComposition;
  /** Omit to inherit the parent's model; empty fields in an explicit choice use connection defaults. */
  readonly model?: CompositionModel;
  /** Local directories, zip packages, or one-file TypeScript plugins. Installing grants trust in this scope. */
  readonly paths?: readonly string[];
  /** Parent imports durable sessions, documents and artifacts; isolated is the default. */
  readonly state?: "isolated" | "parent";
  /** Defaults to a new empty workspace. Explicit roots share their files. */
  readonly root?: string;
  readonly driver_profile?: string;
}

export interface CompositionModel {
  readonly provider?: string;
  readonly model?: string;
  readonly variant?: string;
  readonly effort?: string;
}

export interface CompositionView {
  readonly id: string;
  readonly parent_generation: string;
  readonly model: CompositionModel;
  readonly root: string;
  readonly state: "active";
  readonly running: number;
  readonly agents: readonly string[];
  readonly state_source: "isolated" | "parent";
  readonly selection: {
    readonly profile?: string;
    readonly modules: readonly string[] | null;
    readonly module_source: string;
    readonly module_sources?: Readonly<Record<string, string>>;
    readonly layers?: readonly string[];
    readonly services?: readonly ServiceBinding[];
  };
  readonly modules: readonly { id: string; state: string; error?: string }[];
  readonly services: ServiceRegistrySnapshot;
  readonly plugins: readonly { id: string; fingerprint: string }[];
}

export interface CompositionBinding {
  readonly agent?: string;
  readonly session?: string;
  readonly id: string;
  readonly previous: string;
}

export interface CompositionSessionView {
  readonly session: string;
  readonly id: string;
  readonly state: "default" | "active" | "unavailable";
  readonly owner?: string;
  readonly thread?: string;
  /** Live when active; otherwise the immutable snapshot saved at binding. */
  readonly composition?: CompositionView;
}

export interface CompositionRunResult {
  readonly id: string;
  readonly execution_id: string;
  readonly content: string;
  /** Inference failure; the captured composition remains inspectable. */
  readonly error?: string;
}

export type CompositionRun = ({ readonly id: string; readonly agent?: never } |
  { readonly agent: string; readonly id?: never }) & { readonly prompt: string };

/** Scopes and logical agent names belong to this plugin and conversation in this generation.
 * Use the invocation's bound host so cancellation reaches admitted runs.
 * Each run starts a fresh execution. Bind changes subsequent admission only;
 * restore the previous ID with another compare-and-exchange to undo selection.
 */
export function compositionService(host: RuntimeHost) {
  const call = async <T>(method: string, params: unknown): Promise<T> =>
    await host.call("host.service.call", { service: COMPOSITION_SERVICE, method, params }) as T;
  return {
    create: (params: CompositionCreate) => call<CompositionView>("create", params),
    inspect: (id: string) => call<CompositionView>("inspect", { id }),
    bind: (agent: string, id: string, expected: string) =>
      call<CompositionBinding>("bind", { agent, id, expected }),
    /** Read durable selection and availability, including after disposal or restart. */
    inspectSession: (session: string) => call<CompositionSessionView>("inspectSession", { session }),
    /** Persist a parent-state scope for future turns. Missing resources fail closed after restart. */
    bindSession: (session: string, id: string, expected: string) =>
      call<CompositionBinding>("bind", { session, id, expected }),
    run: (params: CompositionRun) => call<CompositionRunResult>("run", params),
    /** Stops admission and unbinds logical agents. Product selections remain inspectable;
     * explicitly bindSession(session, "", oldID) to return to defaults. Admitted runs drain. */
    dispose: (id: string) => call<{ id: string; draining: boolean }>("dispose", { id }),
  };
}
