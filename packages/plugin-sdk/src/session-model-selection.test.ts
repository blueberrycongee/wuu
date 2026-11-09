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

// Receipt admission is an independent selected-domain transaction; byte payloads,
// ownership, null controls, and uncertain results must cross the public wire intact.
test("session client receipts retain scoped admission bytes without fallback or replay", async () => {
  const receipt = {request_id:"input",receipt_id:"receipt",host_request_id:"host-input",client_id:"client",queue_id:"queue",session_id:"session",plugin_id:"consumer",owner:"owner",receipt_state:"accepted",execution_state:"held",accepted_at:"2026-10-27T00:00:00Z",revision:1} as const;
  const accepted = {Receipt:receipt,Fingerprint:"fingerprint",MessageJSON:"AAEC",RuntimeJSON:"",SelectionJSON:null,LiveOwner:"owner"};
  const scope = {Binding:{user_space_id:"space",product_domain:"personal"},SessionID:"session",PluginID:"consumer",LiveOwner:"owner"};
  let admissions = 0, changes = 0;
  const provider = sessionDomainProvider({
    AcceptClientInput: ([input, control, baseline], context) => {
      admissions++; assert.equal(context.caller, "admitted-host");
      assert.deepEqual(input, accepted); assert.equal(control, null); assert.equal(baseline, null);
      return [input, true];
    },
    ReadClientInput: ([bound, request]) => { assert.deepEqual(bound, scope); assert.equal(request, "host-input"); return [accepted, true]; },
    ChangeClientInput: ([input, state, owner, bytes, control, baseline]) => {
      changes++; assert.deepEqual(input,accepted); assert.equal(state,"queued"); assert.equal(owner,"next-owner");
      assert.equal(bytes,null); assert.equal(control,null); assert.equal(baseline,null);
      throw Object.assign(new Error("unknown commit outcome"), {code:"session_outcome_unknown"});
    },
  });
  const host = {call: async (_method: string, request: ServiceInvokeParams) =>
    JSON.parse(JSON.stringify(await provider.invoke(JSON.parse(JSON.stringify({...request,caller:"admitted-host"}))))),
  } as unknown as RuntimeHost;
  const client = sessionDomain(host);
  assert.deepEqual(await client.call("AcceptClientInput", [accepted,null,null]), [accepted,true]);
  assert.deepEqual(await client.call("ReadClientInput", [scope,"host-input"]), [accepted,true]);
  await assert.rejects(client.call("ChangeClientInput", [accepted,"queued","next-owner",null,null,null]), error => (error as {code:string}).code === "session_outcome_unknown");
  await assert.rejects(client.call("RecoverClientInput", [scope,accepted]), error => (error as {code:string}).code === "method_not_found");
  assert.equal(admissions,1); assert.equal(changes,1);
});

// Inspection lifetime is independent from the execution it fences; release is
// cleanup authority and must remain routable after cancellation.
test("session inspection preserves the selected guard and cleanup routing", async () => {
  const calls: Array<{method:string;cleanup?:boolean}> = [];
  const provider = sessionDomainProvider({
    AcquireInspection: ([id]) => [{id,namespaces:["session"],revision:"9007199254740993"}],
    ReleaseInspection: ([id]) => { assert.equal(id,"guard"); return []; },
  });
  const host = {call: async (_method:string, request:ServiceInvokeParams, options?:{cleanup?:boolean}) => {
    calls.push({method:request.method,cleanup:options?.cleanup});
    return provider.invoke(request);
  }} as unknown as RuntimeHost;
  const client = sessionDomain(host);
  const [guard] = await client.call("AcquireInspection", ["guard"]);
  assert.equal(guard.revision,"9007199254740993");
  assert.deepEqual(await client.call("ReleaseInspection", [guard.id]),[]);
  assert.deepEqual(calls,[{method:"AcquireInspection",cleanup:undefined},{method:"ReleaseInspection",cleanup:true}]);
});
