import type { RuntimeHost, ServiceDescriptor, CompactionMessage } from './index.js';

export const COMPACTION_SERVICE = 'agent.compaction';
export const COMPACTION_SERVICE_DESCRIPTOR: ServiceDescriptor = {
  name: COMPACTION_SERVICE, version: '1.0.0',
  methods: ['describe', 'compact', 'window', 'handoff', 'note-plan', 'compact-note', 'recover'].map(name => ({
    name, input_schema: `agent.compaction.${name}.input.v1`, output_schema: `agent.compaction.${name}.output.v1`,
  })),
};
/** Transcript records use the existing compaction wire, not ModelMessageV1.
 * Preserve all fields when retaining a message, including native replay evidence.
 */
export interface PolicyMessage extends CompactionMessage {
  readonly Role: string;
  readonly Content: string;
  readonly Name?: string;
  readonly Hidden?: boolean;
  readonly Origin?: string;
  readonly Cause?: string;
  readonly seq?: number;
}
export interface PolicyMediaInput {
  readonly Video?: boolean; readonly VideoKnown?: boolean;
  readonly Image?: boolean; readonly ImageKnown?: boolean;
  readonly File?: boolean; readonly FileKnown?: boolean;
}
export interface CompactionPolicyRequest {
  readonly context_tokens?: number;
  readonly input_tokens?: number;
  readonly output_reserve_tokens?: number;
  readonly compact_threshold_tokens?: number;
}
export interface CompactionPolicy {
  readonly key: string;
  readonly notes_enabled: boolean;
  readonly context_windows: boolean;
  readonly target_tokens: number;
  readonly guidance: readonly PolicyMessage[];
}
export interface CompactionBudget {
  readonly MediaInput?: PolicyMediaInput;
  readonly ContextTokens?: number;
  readonly InputTokens?: number;
  readonly OutputReserveTokens?: number;
  readonly KeepRecentTokens?: number;
  readonly TargetTokens?: number;
  readonly SummaryInputTokens?: number;
}
export interface CompactionToolDefinition {
  readonly Name: string; readonly Description: string;
  readonly InputSchema: Readonly<Record<string, unknown>>;
  readonly Freeform?: boolean; readonly DirectOnly?: boolean;
  readonly DeferLoading?: boolean; readonly CacheStable?: boolean;
}
export interface CompactionNativeOptions {
  readonly Provider?: string;
  readonly Tools?: readonly CompactionToolDefinition[];
  readonly Temperature?: number;
  readonly ProviderOptions?: Readonly<Record<string, unknown>>;
  readonly MediaInput?: PolicyMediaInput;
  readonly NativeDeferredToolDiscovery?: boolean;
}
export interface PolicyCompactRequest {
  readonly policy_key?: string;
  readonly model: string;
  readonly messages: readonly PolicyMessage[];
  readonly budget?: CompactionBudget;
  readonly options?: CompactionNativeOptions;
  readonly hint?: {
    readonly reason?: string;
    readonly last_successful_tokens?: number;
    readonly failed_request_tokens?: number;
    readonly target_total_tokens?: number;
  };
}
export interface PolicyWindowRequest {
  readonly policy_key?: string;
  readonly messages: readonly PolicyMessage[];
  readonly history_head: number;
  readonly fixed_tokens: number;
  readonly target_tokens: number;
  readonly media_input?: PolicyMediaInput;
}
export interface PolicyHandoffRequest {
  readonly policy_key?: string;
  readonly model?: string;
  readonly previous_covered_messages?: number;
  readonly messages: readonly PolicyMessage[];
  readonly previous_note?: string;
  readonly intent?: string;
  readonly source_session_id?: string;
  readonly source_through_seq?: number;
}
export interface PolicyNoteRequest {
  readonly policy_key: string;
  readonly model?: string;
  readonly messages?: readonly PolicyMessage[];
  readonly delta?: readonly PolicyMessage[];
  readonly previous_note?: string;
  readonly previous_covered_messages?: number;
  readonly note?: string;
  readonly covered_messages?: number;
}
export interface PolicyNotePlan { readonly prompt: string; readonly interval_tokens: number; readonly max_bytes: number }
export interface PolicyNoteReplacement extends PolicyReplacement { readonly covered_messages: number }
export interface PolicyRecoveryRequest extends PolicyCompactRequest {
  readonly failure: { readonly code: string; readonly message: string };
}
export interface PolicyReplacement { readonly messages: readonly PolicyMessage[] }
export interface PolicyHandoffPlan { readonly prompt: string; readonly max_bytes: number }

/** Exactly one selected service call; neither errors nor absence select a fallback.
 * Replacement messages are proposals. The caller still validates and commits history.
 */
export function compactionService(host: RuntimeHost) {
  async function call<T>(method: string, params: object): Promise<T> {
    return await host.call('host.service.call', { service: COMPACTION_SERVICE, method, params }) as T;
  }
  async function replacement(method: string, params: object): Promise<PolicyReplacement> {
    const result = await call<PolicyReplacement>(method, params);
    if (!result || !Array.isArray(result.messages)) throw new TypeError('Invalid compaction result: messages required');
    return result;
  }
  return {
    describe: async (params: CompactionPolicyRequest): Promise<CompactionPolicy> => {
      const result = await call<CompactionPolicy>('describe', params);
      if (!result || typeof result.key !== 'string' || !result.key || typeof result.notes_enabled !== 'boolean' || typeof result.context_windows !== 'boolean' || !Number.isInteger(result.target_tokens)
        || (result.context_windows && result.target_tokens <= 0) || !Array.isArray(result.guidance)) {
        throw new TypeError('Invalid compaction policy result');
      }
      return result;
    },
    notePlan: async (params: PolicyNoteRequest): Promise<PolicyNotePlan> => {
      const result = await call<PolicyNotePlan>('note-plan', params);
      if (!result || typeof result.prompt !== 'string' || !result.prompt.trim()) throw new TypeError('Invalid note plan');
      return result;
    },
    compactNote: async (params: PolicyNoteRequest): Promise<PolicyNoteReplacement> => {
      const result = await call<PolicyNoteReplacement>('compact-note', params);
      if (!result || !Array.isArray(result.messages) || !Number.isInteger(result.covered_messages)
        || result.covered_messages <= 0 || result.covered_messages > result.messages.length) throw new TypeError('Invalid note replacement coverage');
      return result;
    },
    recover: (params: PolicyRecoveryRequest) => replacement('recover', params),
    compact: (params: PolicyCompactRequest) => replacement('compact', params),
    window: (params: PolicyWindowRequest) => replacement('window', params),
    handoff: async (params: PolicyHandoffRequest): Promise<PolicyHandoffPlan> => {
      const result = await call<PolicyHandoffPlan>('handoff', params);
      if (!result || typeof result.prompt !== 'string' || !result.prompt || !Number.isInteger(result.max_bytes) || result.max_bytes < 0) {
        throw new TypeError('Invalid handoff plan');
      }
      return result;
    },
  };
}
