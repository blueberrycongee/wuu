import type { InitializeResult, Thread, Turn } from "../shared/protocol";
import { engineRequiresHostModel } from "./RuntimeHelpers";

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
    speed: thread.speed ?? "",
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

// A submitted draft has no Thread yet, but thread/start already fixes the
// identity it will pin: protocol engines record their engine id as
// model_provider (they never send a provider), while host-model engines pin
// the selected provider and keep the workspace default when it is unset.
export function submittedThreadRuntime(
  selection: { engine: string; provider: string; model: string } | undefined,
  engines: readonly { id: string; requires_host_model?: boolean }[] | undefined,
  workspace: { provider?: string; model?: string },
): { provider: string; model: string } {
  if (selection && !engineRequiresHostModel(selection.engine, engines?.find(engine => engine.id === selection.engine))) {
    return { provider: selection.engine, model: selection.model };
  }
  return {
    provider: selection?.provider || workspace.provider || "",
    model: selection?.model || workspace.model || "",
  };
}
