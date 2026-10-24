import type { RuntimeHost, ServiceDescriptor, ToolResult } from './index.js';

/** Selected registry and execution pipeline, independent of tool contributions. */
export const TOOL_SERVICE = 'agent.tools';
export const TOOL_SERVICE_DESCRIPTOR: ServiceDescriptor = {
  name: TOOL_SERVICE,
  version: '1.0.0',
  methods: ['list', 'resolve', 'execute', 'freeze', 'unfreeze'].map(name => ({
    name,
    input_schema: `${TOOL_SERVICE}.${name}.input.v1`,
    output_schema: `${TOOL_SERVICE}.${name}.output.v1`,
  })),
};

export interface ToolServiceDefinition {
  name: string;
  description: string;
  input_schema: Record<string, unknown>;
  freeform?: boolean;
  direct_only?: boolean;
  defer_loading?: boolean;
  cache_stable?: boolean;
}
export interface ToolServiceCatalogEntry {
  name: string;
  summary: string;
  tags?: string[];
}
export interface ToolServiceList {
  tools: ToolServiceDefinition[];
  catalog: ToolServiceCatalogEntry[];
  /** Provider-owned discovery instructions. Empty means no catalog prompt. */
  catalog_prompt: string;
  system_fragment: string;
  /** Optional capability view; omitted means no profile-derived admission. */
  surface?: ToolServiceSurface;
}
/** Uses the established Go capability surface field names on the wire. */
export interface ToolServiceSurface {
  ProfileName: string;
  Provider: string;
  Model: string;
  Tools: Record<string, string> | null;
  DeferredTools: Record<string, string> | null;
  NestedTools: Record<string, string> | null;
  ContributedTools: Record<string, Record<string, never>> | null;
  HiddenTools: Record<string, string> | null;
  Capabilities: string[] | null;
  DeferredCapabilities: string[] | null;
  HiddenCapabilities: string[] | null;
  SystemFragment: string;
  DeferredToolCatalog: string;
}
export interface ToolServiceDisplay {
  kind?: string;
  label?: string;
  label_translations?: Record<string, string>;
  text?: string;
  capability?: string;
}
export interface ToolServiceCall {
  id?: string;
  name: string;
  /** Encoded arguments, or literal input for a freeform tool. */
  arguments?: string;
  provider_item_id?: string;
  provider_item_provider?: string;
  provider_item_model?: string;
  kind?: string;
  display?: ToolServiceDisplay;
}
export interface ToolServiceCallRequest { call: ToolServiceCall }
export interface ToolServiceMetadata {
  orchestrator: boolean;
  read_only: boolean;
  concurrency_safe: boolean;
  destructive: boolean;
  risk: string;
  reason: string;
}
export type ToolServiceResolution =
  | { found: false; supports: boolean; metadata?: never; display?: ToolServiceDisplay }
  | { found: true; supports: boolean; metadata: ToolServiceMetadata; display?: ToolServiceDisplay };
export interface ToolServiceExecution {
  result: ToolResult & { model_text?: string };
  discovered_tools?: { type: string; name: string; description?: string; input_schema?: Record<string, unknown>; defer_loading?: boolean }[];
  additional_context?: string;
}

/** Requires agent.tools@1. Bind to the current callback's RuntimeHost so calls
 * retain the selected generation and execution identity. Errors are propagated;
 * helpers never retry execution or substitute a default registry. OS resources
 * still require the host's authenticated execution and permission gateways.
 */
export function toolService(host: RuntimeHost) {
  const call = <T>(method: string, params: object): Promise<T> =>
    host.call('host.service.call', { service: TOOL_SERVICE, method, params }) as Promise<T>;
  return {
    list: () => call<ToolServiceList>('list', {}),
    freeze: () => call<Record<string, never>>('freeze', {}),
    unfreeze: () => call<Record<string, never>>('unfreeze', {}),
    resolve: (input: ToolServiceCallRequest) => call<ToolServiceResolution>('resolve', input),
    execute: (input: ToolServiceCallRequest) => call<ToolServiceExecution>('execute', input),
  };
}
