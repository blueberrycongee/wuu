import type { RuntimeHost, ServiceDescriptor } from './index.js';

export const PROMPT_SERVICE = 'agent.prompt';
export const PROMPT_SERVICE_DESCRIPTOR: ServiceDescriptor = {
  name: PROMPT_SERVICE, version: '1.0.0', methods: [{
    name: 'assemble', input_schema: 'agent.prompt.assemble.input.v1', output_schema: 'agent.prompt.assemble.output.v1',
  }],
};

/** These existing nested records retain Go wire casing. They are facts, not authority. */
export interface PromptInstruction {
  readonly Path: string;
  readonly Content: string;
  readonly Source: string;
  readonly Name: string;
}
export interface PromptSkill {
  readonly Name: string;
  readonly Description?: string;
  readonly WhenToUse?: string;
  readonly Content?: string;
  readonly Source?: string;
  readonly Path?: string;
  readonly Dir?: string;
  readonly ArgumentHint?: string;
  readonly TriggerCondition?: string;
  readonly RequiredContext?: readonly string[];
  readonly Examples?: readonly string[];
  readonly VerificationChecklist?: readonly string[];
  readonly ProgressiveDisclosure?: string;
  readonly Model?: string;
  readonly Context?: string;
  readonly Agent?: string;
  readonly AllowedTools?: readonly string[];
  readonly UserInvocable?: boolean;
  readonly DisableModelInvoke?: boolean;
  readonly Paths?: readonly string[];
  readonly Effort?: string;
  readonly Version?: string;
  readonly Shell?: string;
}
export interface PromptToolSurface {
  readonly ProfileName?: string;
  readonly Provider?: string;
  readonly Model?: string;
  readonly Tools?: Readonly<Record<string, string>>;
  readonly DeferredTools?: Readonly<Record<string, string>>;
  readonly NestedTools?: Readonly<Record<string, string>>;
  readonly ContributedTools?: Readonly<Record<string, Record<string, never>>>;
  readonly HiddenTools?: Readonly<Record<string, string>>;
  readonly Capabilities?: readonly string[];
  readonly DeferredCapabilities?: readonly string[];
  readonly HiddenCapabilities?: readonly string[];
  readonly SystemFragment?: string;
  readonly DeferredToolCatalog?: string;
}
export interface PromptContribution {
  readonly key: string;
  readonly content: string;
  readonly static: boolean;
}
export interface PromptWorkerContext {
  readonly type?: string;
  readonly role?: string;
  readonly prompt?: string;
  readonly context_scope?: string;
  readonly output_schema?: string;
  readonly success_criteria?: readonly string[];
  readonly isolation: 'inplace' | 'worktree';
  readonly agent_id?: string;
  readonly agent_path?: string;
  readonly agent_profile?: string;
}
export interface PromptAssemblyRequest {
  readonly worker?: PromptWorkerContext;
  readonly role: 'main' | 'worker';
  readonly cwd: string;
  /** Frozen session date supplied by the consumer. */
  readonly date: string;
  readonly provider?: string;
  readonly model?: string;
  readonly user_prompt?: string;
  readonly surface: PromptToolSurface;
  readonly instructions?: readonly PromptInstruction[];
  readonly skills?: readonly PromptSkill[];
  readonly memory_teaching?: string;
  readonly memory_index?: string;
  readonly contributions?: readonly PromptContribution[];
}
export interface PromptSectionInfo {
  readonly key: string;
  readonly static: boolean;
  readonly bytes: number;
  readonly hash: string;
}
export interface PromptAssemblyResult {
  readonly content: string;
  readonly sections: readonly PromptSectionInfo[];
}

/** Use the callback's bound host. The result is complete; callers add no native base. */
export function promptService(host: RuntimeHost) {
  return {
    assemble: async (params: PromptAssemblyRequest): Promise<PromptAssemblyResult> => {
      const result = await host.call('host.service.call', { service: PROMPT_SERVICE, method: 'assemble', params }) as PromptAssemblyResult;
      if (!result || typeof result.content !== 'string' || !Array.isArray(result.sections)) {
        throw new TypeError('Invalid agent.prompt result: content and sections required');
      }
      return result;
    },
  };
}
