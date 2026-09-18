import type { InitializeResult, Thread, Turn } from "../shared/protocol";

// InitializeResult describes workspace defaults. Existing conversations carry
// their persisted selection on Thread, while an in-progress Turn records what
// is actually executing. Centralize that priority so every model-facing UI
// tells the same story: running turn -> next turn in this session -> default
// for a session that has not been created yet.
export function runtimeViewForSession(
  initialized: InitializeResult | undefined,
  thread: Thread | undefined,
): InitializeResult | undefined {
  if (!initialized || !thread) {
    return initialized;
  }
  return {
    ...initialized,
    provider: thread.model_provider || initialized.provider,
    model: thread.model || initialized.model,
    variant: thread.model_variant ?? initialized.variant,
    effort: thread.model_effort ?? initialized.effort,
    permissions: {
      ...initialized.permissions,
      mode: thread.permission_mode || initialized.permissions?.mode,
      approve_for_me: thread.approve_for_me ?? initialized.permissions?.approve_for_me,
    },
  };
}

export function runtimeViewForConversation(
  initialized: InitializeResult | undefined,
  thread: Thread | undefined,
  turn: Pick<Turn, "status" | "model_provider" | "model"> | undefined,
): InitializeResult | undefined {
  const session = runtimeViewForSession(initialized, thread);
  if (!session || turn?.status !== "in_progress") {
    return session;
  }
  return {
    ...session,
    provider: turn.model_provider || session.provider,
    model: turn.model || session.model,
  };
}
