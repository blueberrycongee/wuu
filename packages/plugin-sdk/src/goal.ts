import type { ServiceDescriptor } from "./index.js";

/** User controls for a selected persistent-goal provider. Model completion and
 * blocking remain tools; presentation cannot claim either through this service.
 */
export const GOAL_CONTROL_SERVICE_DESCRIPTOR = {
  name: "goal.control", version: "1.0.0",
  methods: [{ name: "request", input_schema: "goal.control.request.v1", output_schema: "goal.control.response.v1" }],
} satisfies ServiceDescriptor;

export interface GoalSnapshotV1 {
  readonly id: string;
  readonly thread_id: string;
  readonly objective: string;
  readonly status: "active" | "paused" | "blocked" | "complete";
  readonly tokens_used: number;
  readonly time_used_seconds: number;
  readonly error: string;
  readonly updated_at: string;
}

export type GoalControlRequestV1 =
  | { readonly method: "get_goal" | "pause" | "resume" | "clear"; readonly input: { readonly thread_id: string } }
  | { readonly method: "create_goal"; readonly input: { readonly thread_id: string; readonly objective: string } };

export interface GoalControlResultV1 {
  /** Cleared and absent goals both return null. Usage includes settled turns only. */
  readonly goal: GoalSnapshotV1 | null;
}
