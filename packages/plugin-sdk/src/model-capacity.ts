import type { RuntimeHost, ServiceDescriptor, ServiceRequirement } from "./index.js";
import type { ModelFailureV1 } from "./model.js";

export const MODEL_CAPACITY_SERVICE = "model.capacity";
export interface ModelCapacityAcquire { readonly scope: string; readonly profile?: string }
export interface ModelCapacityLease { readonly lease: string }
export interface ModelCapacitySettlement {
  readonly lease: string;
  readonly outcome: "succeeded" | "failed" | "transport_fallback" | "abandoned";
  readonly failure?: ModelFailureV1;
}
/** Capacity is one physical submission, not a logical retry budget. The host
 * receipt boundary applies this selected strategy and the separately shared
 * physical resource bound. Scope values are opaque identities, never secrets. */
export function modelCapacityServiceDescriptor(): ServiceDescriptor {
  return { name: MODEL_CAPACITY_SERVICE, version: "1.0.0", methods: [
    { name: "acquire", input_schema: "model.capacity.acquire.input.v1", output_schema: "model.capacity.acquire.output.v1" },
    { name: "settle", cleanup: true, input_schema: "model.capacity.settle.input.v1", output_schema: "model.capacity.settle.output.v1" },
  ] };
}
export function requireModelCapacityService(): ServiceRequirement {
  return { name: MODEL_CAPACITY_SERVICE, major_version: 1, required: true };
}
export function modelCapacity(host: RuntimeHost) {
  return {
    acquire: async (input: ModelCapacityAcquire): Promise<ModelCapacityLease> => {
      if (!input.scope.trim()) throw new Error("capacity scope is required");
      const result = await host.call("host.service.call", { service: MODEL_CAPACITY_SERVICE, method: "acquire", params: input }) as ModelCapacityLease;
      if (!result.lease) throw new Error("capacity provider returned no lease");
      return result;
    },
    settle: async (input: ModelCapacitySettlement): Promise<void> => {
      await host.call("host.service.call", { service: MODEL_CAPACITY_SERVICE, method: "settle", cleanup: true, params: input }, { cleanup: true });
    },
  };
}
