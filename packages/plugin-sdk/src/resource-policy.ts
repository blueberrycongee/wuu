import type { ServiceDescriptor, ServiceRequirement, ServiceInvokeParams } from "./index.js";
import type { PrincipalIdentity } from "./identity.js";

export interface ResourceReference { readonly kind: string; readonly id?: string; }
export interface ResourceAuthorizationRequest {
 readonly principal: PrincipalIdentity;
 /** Established by the host transport, never copied from a caller's JSON. */
 readonly owner: boolean;
 /** Exact RPC action, event:<name>, or request:<reverse-RPC name>. */
 readonly action: string;
 /** A decision must cover EVERY resource. Global is {kind:"global"}, and does
  * not imply access to a named session, share, workspace or execution. */
 readonly resources: readonly ResourceReference[];
 /** Requested effects at initial dispatch; resolved provider, model,
  * permission_mode and engine at execution admission and subsequent delivery.
  * Restrictive policies must not treat an absent effect as matching a ceiling.
  * Tool bounds are supplied only by adapters that enforce those bounds (such
  * as federation), never by ignored generic RPC fields. No prompt or credential
  * content is included. Adapters may add documented namespaced effects. */
 readonly attributes?: Readonly<Record<string, unknown>>;
}
export interface ResourceAuthorizationDecision { readonly allow: boolean; readonly reason?: string; }
export const RESOURCE_AUTHORIZATION_SERVICE = "resource.authorization";
export function resourceAuthorizationDescriptor(): ServiceDescriptor {
 return {name:RESOURCE_AUTHORIZATION_SERVICE,version:"1.0.0",methods:[{name:"authorize",input_schema:"resource.authorization.request.v1",output_schema:"resource.authorization.decision.v1"}]};
}
export function requireResourceAuthorization(): ServiceRequirement {
 return {name:RESOURCE_AUTHORIZATION_SERVICE,major_version:1,required:true};
}
/** One replaceable resource decision point, separate from identity verification
 * and security.authorize (tool execution). No allow decision is cached by the
 * host; unavailable/removed providers, errors and malformed decisions deny. */
export function resourceAuthorizationProvider(authorize: (input: ResourceAuthorizationRequest) => ResourceAuthorizationDecision | Promise<ResourceAuthorizationDecision>) {
 return {
  descriptor:resourceAuthorizationDescriptor(),
  async invoke(request:ServiceInvokeParams):Promise<ResourceAuthorizationDecision> {
   if(request.service!==RESOURCE_AUTHORIZATION_SERVICE||request.method!=="authorize")throw new Error("unknown resource authorization method");
   const input=request.params as ResourceAuthorizationRequest;
   if(!input||!input.principal||!Array.isArray(input.resources)||input.resources.length===0||typeof input.action!=="string"||!input.action||typeof input.owner!=="boolean")throw new Error("invalid resource authorization request");
   for(const resource of input.resources)if(!resource||!resource.kind||(resource.kind==="global"?resource.id!==undefined&&resource.id!=="":typeof resource.id!=="string"||!resource.id.trim()))throw new Error("invalid resource identity");
   const decision=await authorize(input);
   if(!decision||typeof decision.allow!=="boolean"||decision.reason!==undefined&&typeof decision.reason!=="string")throw new Error("invalid resource authorization decision");
   return decision;
  },
 };
}
