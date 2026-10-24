export interface ApplicationProjectUsageV1 {
  readonly role: string; readonly provider: string; readonly model: string;
  readonly input_tokens: number; readonly output_tokens: number;
  readonly cache_read_tokens: number; readonly cache_creation_tokens: number;
}
export interface ApplicationProjectDispatchV1 {
  readonly session_id: string; readonly state: string; readonly turn_id?: string; readonly client_id?: string;
  readonly delivery_state?: "queued" | "steering" | "consumed";
}
export interface ApplicationProjectWorkV1 {
  readonly id: string; readonly project_id: string; readonly version: number; readonly revision: number;
  readonly title: string; readonly brief: string; readonly acceptance: string; readonly authority: string;
  readonly source_refs?: string;
  readonly phase: "planning" | "executing" | "verifying" | "reviewing" | "accepted" | "delivering" | "delivered" | "blocked" | "stopped";
  readonly lead_id: string; readonly executor_id?: string; readonly workspace: string;
  readonly summary?: string; readonly evidence?: string; readonly code_ref?: string; readonly review?: string;
  readonly delivery?: string; readonly blocker?: string; readonly submission_turn_id?: string; readonly review_turn_id?: string;
  readonly lead_dispatch?: ApplicationProjectDispatchV1; readonly executor_dispatch?: ApplicationProjectDispatchV1;
  readonly usage?: readonly ApplicationProjectUsageV1[];
}
export interface ApplicationProjectWorkListV1 {
  readonly project_id: string;
  readonly works: readonly ApplicationProjectWorkV1[];
  readonly usage: readonly ApplicationProjectUsageV1[];
}
export interface ApplicationProjectViewV1 {
  readonly projectId: string;
  readonly enabled: boolean;
  readonly loading: boolean;
  readonly error: string;
  readonly busyWorkId?: string;
  readonly snapshot?: ApplicationProjectWorkListV1;
}
export interface ApplicationProjectCallOptionsV1 { readonly signal?: AbortSignal }
/** Observes the core project ledger; UI replacement never stops project execution. */
export interface ApplicationProjectsV1 {
  getSnapshot(projectId: string): ApplicationProjectViewV1;
  subscribe(projectId: string, listener: () => void): () => void;
  refresh(projectId: string, options?: ApplicationProjectCallOptionsV1): Promise<void>;
  /** The selected revision must still be current. Effects are never automatically replayed. */
  control(input: { readonly projectId: string; readonly workId: string; readonly revision: number;
    readonly operation: "stop" | "resume" }, options?: ApplicationProjectCallOptionsV1): Promise<void>;
  releaseSession(input: { readonly projectId: string; readonly threadId: string }, options?: ApplicationProjectCallOptionsV1): Promise<void>;
}
