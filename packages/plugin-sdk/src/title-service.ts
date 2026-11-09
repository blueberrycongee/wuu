import type { RuntimeHost, ServiceDescriptor } from './index.js';
import type { ModelMessageV1 } from './model.js';

export const TITLE_SERVICE = 'session.title';
export const TITLE_SERVICE_DESCRIPTOR: ServiceDescriptor = {
  name: TITLE_SERVICE, version: '1.0.0', methods: ['plan', 'resolve'].map(name => ({
    name, input_schema: `${TITLE_SERVICE}.${name}.input.v1`, output_schema: `${TITLE_SERVICE}.${name}.output.v1`,
  })),
};

/** Authenticated message authorship is projected by the host before this call. */
export interface TitlePlanRequest {
  readonly model: string;
  readonly human_messages: readonly string[];
  readonly title?: string;
  readonly summary?: string;
  readonly read_only?: boolean;
  readonly ephemeral?: boolean;
  readonly parent_id?: string;
  readonly force?: boolean;
  readonly persist: boolean;
}
export type TitlePlan =
  | { readonly action: 'skip'; readonly reason?: string; readonly source?: string }
  | { readonly action: 'complete'; readonly title: string; readonly source?: string }
  | { readonly action: 'infer'; readonly messages: readonly ModelMessageV1[]; readonly temperature?: number; readonly source?: string };

/** The provider owns eligibility, complete inference input and title cleanup.
 * The host retains model accounting, transport and atomic generated-title writes.
 * Omitting the optional service disables automatic and explicit title generation.
 */
export function titleService(host: RuntimeHost) {
  return {
    async plan(params: TitlePlanRequest): Promise<TitlePlan> {
      const result = await host.call('host.service.call', { service: TITLE_SERVICE, method: 'plan', params }) as TitlePlan;
      if (!result || !['skip', 'complete', 'infer'].includes(result.action)
        || (result.action === 'complete' && (typeof result.title !== 'string' || !result.title.trim()))
        || (result.action === 'infer' && (!Array.isArray(result.messages) || result.messages.length === 0))) {
        throw new TypeError('Invalid session.title plan');
      }
      return result;
    },
    async resolve(text: string): Promise<string> {
      const result = await host.call('host.service.call', { service: TITLE_SERVICE, method: 'resolve', params: { text } }) as { title: string };
      if (!result || typeof result.title !== 'string') throw new TypeError('Invalid session.title result');
      return result.title;
    },
  };
}
