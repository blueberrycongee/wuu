import assert from "node:assert/strict";
import test from "node:test";
import { sessionDomain, sessionDomainProvider, type SessionDomainSession } from "./session-service.js";
import type { RuntimeHost, ServiceInvokeParams } from "./index.js";

// An independent provider opts into a distinct operation; the SDK transports its
// CAS errors unchanged instead of routing intent to the legacy mutation method.
test("session next-step intent uses its optional provider operation and preserves CAS conflicts", async () => {
  let stored: SessionDomainSession = { id: "thread", created_at: "2026-01-01T00:00:00Z", entries: 0,
    engine_id: "custom", provider: "one", model: "old", permission_mode: "default", model_selection_revision: 4 };
  let legacyWrites = 0;
  const provider = sessionDomainProvider({
    Find: ([id]) => [structuredClone(stored), id === stored.id],
    SetRuntimeSelection: () => { legacyWrites++; throw new Error("unexpected legacy mutation"); },
    SetNextStepModelSelection: ([id, intent]) => {
      assert.equal(id, stored.id);
      if (intent.ExpectedRevision !== stored.model_selection_revision || intent.ExpectedEngineID !== stored.engine_id) {
        throw Object.assign(new Error("selection changed"), { code: "session_model_selection_changed" });
      }
      const next = intent.Selection;
      assert.equal(next.PermissionMode, stored.permission_mode);
      assert.equal(next.ApproveForMe, stored.approve_for_me ?? false);
      stored = { ...stored, provider: next.Provider, model: next.Model, variant: next.Variant, effort: next.Effort,
        speed: next.Speed, model_options: next.ModelOptions ?? undefined,
        model_selection_revision: intent.ExpectedRevision + 1, model_selection_apply_at: "next_step" };
      return [structuredClone(stored)];
    },
  });
  assert.ok(provider.descriptor.methods.some(method => method.name === "SetNextStepModelSelection"));
  const host = { call: async (_method: string, request: ServiceInvokeParams) =>
    JSON.parse(JSON.stringify(await provider.invoke(JSON.parse(JSON.stringify({ ...request, caller: "test" }))))),
  } as unknown as RuntimeHost;
  const client = sessionDomain(host);
  const intent = { ExpectedRevision: 4, ExpectedEngineID: "custom", Selection: {
    Provider: "two", Model: "new", Variant: "", Effort: "", Speed: "", ModelOptions: null, PermissionMode: "default", ApproveForMe: false,
  } };
  const outcomes = await Promise.allSettled([
    client.call("SetNextStepModelSelection", ["thread", intent]),
    client.call("SetNextStepModelSelection", ["thread", intent]),
  ]);
  assert.equal(outcomes.filter(result => result.status === "fulfilled").length, 1);
  const failed = outcomes.find(result => result.status === "rejected");
  assert.equal(failed?.status === "rejected" && failed.reason.code, "session_model_selection_changed");
  const [restored, found] = await sessionDomain(host).call("Find", ["thread"]);
  assert.ok(found);
  assert.equal(restored.model_selection_revision, 5);
  assert.equal(restored.model_selection_apply_at, "next_step");
  assert.equal(restored.model, "new");
  assert.equal(restored.permission_mode, "default");
  assert.equal(legacyWrites, 0);
});
