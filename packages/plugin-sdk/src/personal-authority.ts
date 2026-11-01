/** Business facts of the selected personal.agents provider. They are not
 * authorization: the host verifies session/source identity and retains the
 * selected generation and durable user-state inspection across effects.
 * Implementations keep these facts in their own format in host user storage;
 * the inspection freezes that opaque storage, not an SDK-defined key layout. */
export interface PersonalExecutionFacts {
  session_id: string;
  agent_id: string;
  main_session_id: string;
  active: boolean;
  task?: {
    id: string;
    revision: number;
    run_id: string;
    active: boolean;
    goal: string;
    source_session_id: string;
    /** Zero when no exact, original human source is eligible for intent review. */
    source_message_seq: number;
    routine_id?: string;
    routine_revision?: number;
  };
}
export const PERSONAL_AUTHORITY_METHODS = [
  { name: "observe-execution", input_schema: "personal.execution-facts.input.v1", output_schema: "personal.execution-facts.output.v1" },
  { name: "legacy-memory", input_schema: "personal.legacy-memory.input.v1", output_schema: "personal.legacy-memory.output.v1" },
] as const;
