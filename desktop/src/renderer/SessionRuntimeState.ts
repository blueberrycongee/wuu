import type { InitializeResult, Thread, Turn } from "../shared/protocol";
import { engineRequiresHostModel } from "./RuntimeHelpers";

// InitializeResult describes workspace defaults. Existing conversations carry
// their persisted selection on Thread, while an in-progress Turn records what
// is actually executing. Centralize that priority so every model-facing UI
// tells the same story: running turn -> next turn in this session -> default
// for a session that has not been created yet.
// Presentation snapshots call these projections on every state update. The
// projected objects must stay identity-stable while the runtime fields they
// read are unchanged, or downstream caches keyed on the projection (frozen
// configuration copies, composer effect deps) churn on every stream event.
const sessionViewCache = new WeakMap<InitializeResult, Map<string, InitializeResult>>();
const conversationViewCache = new WeakMap<InitializeResult, { provider: string | undefined; model: string | undefined; value: InitializeResult }>();

export function runtimeViewForSession(
  initialized: InitializeResult | undefined,
  thread: Thread | undefined,
): InitializeResult | undefined {
  if (!initialized || !thread) {
    return initialized;
  }
  const provider = thread.model_provider || initialized.provider;
  const model = thread.model || initialized.model;
  const variant = thread.model_variant ?? initialized.variant;
  const effort = thread.model_effort ?? initialized.effort;
  const speed = thread.speed ?? "";
  const mode = thread.permission_mode || initialized.permissions?.mode;
  const approveForMe = thread.approve_for_me ?? initialized.permissions?.approve_for_me;
  const key = JSON.stringify([provider, model, variant, effort, speed, mode, approveForMe]);
  let entries = sessionViewCache.get(initialized);
  const cached = entries?.get(key);
  if (cached) {
    return cached;
  }
  const value = {
    ...initialized,
    provider,
    model,
    variant,
    effort,
    speed,
    permissions: {
      ...initialized.permissions,
      mode,
      approve_for_me: approveForMe,
    },
  };
  if (!entries) {
    entries = new Map();
    sessionViewCache.set(initialized, entries);
  }
  entries.set(key, value);
  return value;
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
  const provider = turn.model_provider || session.provider;
  const model = turn.model || session.model;
  const cached = conversationViewCache.get(session);
  if (cached && cached.provider === provider && cached.model === model) {
    return cached.value;
  }
  const value = {
    ...session,
    provider,
    model,
  };
  conversationViewCache.set(session, { provider, model, value });
  return value;
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
