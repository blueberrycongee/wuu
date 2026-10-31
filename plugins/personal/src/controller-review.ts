export interface ControllerReviewBinding {
  task_id: string;
  requirements_revision: number;
  worker_request_id: string;
}
export interface ControllerReviewDecision {
  decision: 'accept' | 'follow_up' | 'ask_user';
  summary: string;
  evidence: string[];
  follow_up?: string;
  question?: string;
}

/** Parse only the decision data. The caller must independently authenticate the
 * review turn and task grant before persisting an intent or applying it. */
export function controllerReviewDecision(input: Record<string, unknown>, binding: ControllerReviewBinding): ControllerReviewDecision {
  if (input.task_id !== binding.task_id || input.requirements_revision !== binding.requirements_revision || input.worker_request_id !== binding.worker_request_id) throw new Error('Controller review target or revision is no longer current');
  const bounded = (value: unknown, limit: number): value is string => typeof value === 'string' && value.trim().length > 0 && value.length <= limit;
  if (!['accept','follow_up','ask_user'].includes(String(input.decision)) || !bounded(input.summary, 8000) || !Array.isArray(input.evidence) || input.evidence.length < 1 || input.evidence.length > 20 || input.evidence.some(value => !bounded(value,4000))) throw new Error('Controller review requires a bounded decision, summary and evidence');
  if (input.decision === 'follow_up' && !bounded(input.follow_up,16000)) throw new Error('Controller follow-up requires bounded instructions within the original task');
  if (input.decision === 'ask_user' && !bounded(input.question,4000)) throw new Error('Controller question requires bounded text');
  return {decision:input.decision as ControllerReviewDecision['decision'],summary:input.summary,evidence:input.evidence as string[],...(input.decision === 'follow_up' ? {follow_up:input.follow_up as string} : {}),...(input.decision === 'ask_user' ? {question:input.question as string} : {})};
}
