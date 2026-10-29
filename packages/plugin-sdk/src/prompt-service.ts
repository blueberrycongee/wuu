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
export const PROMPT_SECTION_LAYOUT_V1 = 'wuu.prompt.sections.v1';
export interface PromptAssemblyResult {
  /** Optional opt-in layout for section-aware composition. */
  readonly layout?: string;
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

/** Replace exactly one section of an explicitly declared, verified v1 layout.
 * V1 joins UTF-8 section contents with two LF bytes; bytes counts UTF-8 and
 * hash is the first 16 bytes of SHA-256 encoded as lowercase hexadecimal.
 * Duplicate keys, invalid metadata or absent keys fail without fallback. The
 * original result is not mutated; all other sections retain their metadata.
 */
export async function replacePromptSection(result: PromptAssemblyResult, key: string, replacement: string): Promise<PromptAssemblyResult> {
  if (!result || result.layout !== PROMPT_SECTION_LAYOUT_V1 || typeof result.content !== 'string' || !Array.isArray(result.sections)) {
    throw new Error('Prompt does not declare a supported section layout');
  }
  if (!key || typeof replacement !== 'string' || replacement.length === 0) throw new Error('A section key and non-empty replacement are required');
  const encoder = new TextEncoder();
  const decoder = new TextDecoder('utf-8', {fatal: true, ignoreBOM: true});
  const source = encoder.encode(result.content);
  if (decoder.decode(source) !== result.content || decoder.decode(encoder.encode(replacement)) !== replacement) throw new Error('Prompt text must be valid UTF-8');
  const hash = async (value: Uint8Array): Promise<string> => {
    const digest = await crypto.subtle.digest('SHA-256', Uint8Array.from(value).buffer);
    return Array.from(new Uint8Array(digest).slice(0, 16), byte => byte.toString(16).padStart(2, '0')).join('');
  };
  let offset = 0;
  const seen = new Set<string>();
  const parts: string[] = [];
  for (const [index, section] of result.sections.entries()) {
    if (!section || typeof section.key !== 'string' || !section.key || seen.has(section.key) || typeof section.static !== 'boolean' || !Number.isSafeInteger(section.bytes) || section.bytes < 0 || !/^[0-9a-f]{32}$/.test(section.hash)) throw new Error('Invalid or duplicate prompt section metadata');
    seen.add(section.key);
    if (index > 0) {
      if (source[offset] !== 10 || source[offset + 1] !== 10) throw new Error('Invalid prompt section separator');
      offset += 2;
    }
    if (section.bytes > source.length - offset) throw new Error('Invalid prompt section length');
    const bytes = source.slice(offset, offset + section.bytes);
    offset += section.bytes;
    const content = decoder.decode(bytes);
    if (await hash(bytes) !== section.hash) throw new Error('Prompt section hash mismatch');
    parts.push(content);
  }
  if (offset !== source.length) throw new Error('Prompt section layout has trailing content');
  const index = result.sections.findIndex(section => section.key === key);
  if (index < 0) throw new Error(`Prompt section ${key} not found`);
  const bytes = encoder.encode(replacement);
  parts[index] = replacement;
  const sections = result.sections.map((section, i) => i === index ? {...section, bytes: bytes.length, hash: ''} : section);
  sections[index] = {...sections[index], hash: await hash(bytes)};
  return {...result, content: parts.join('\n\n'), sections};
}
