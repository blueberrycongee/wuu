import type { ProductComposition, RuntimeHost, ServiceBinding, ServiceRequirement } from "./index.js";

export const COMPOSITION_SERVICE = "host.composition";

export function requireCompositionService(): ServiceRequirement {
  return { name: COMPOSITION_SERVICE, major_version: 1, required: true };
}

export interface CompositionCreate {
  readonly selection: ProductComposition;
  /** Local directories, zip packages, or one-file TypeScript plugins. Installing grants trust in this scope. */
  readonly paths?: readonly string[];
  /** Defaults to a new empty workspace. Explicit roots share their files. */
  readonly root?: string;
  readonly driver_profile?: string;
}

export interface CompositionView {
  readonly id: string;
  readonly root: string;
  readonly state: "active";
  readonly running: number;
  readonly agents: readonly string[];
  readonly selection: {
    readonly profile?: string;
    readonly modules: readonly string[] | null;
    readonly module_source: string;
    readonly module_sources?: Readonly<Record<string, string>>;
    readonly layers?: readonly string[];
    readonly services?: readonly ServiceBinding[];
  };
  readonly modules: readonly { id: string; state: string; error?: string }[];
  readonly plugins: readonly { id: string; fingerprint: string }[];
}

export interface CompositionBinding {
  readonly agent: string;
  readonly id: string;
  readonly previous: string;
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
    run: (params: CompositionRun) => call<CompositionRunResult>("run", params),
    /** Unbinds this scope and stops admission. Admitted runs finish before cleanup. */
    dispose: (id: string) => call<{ id: string; draining: boolean }>("dispose", { id }),
  };
}
