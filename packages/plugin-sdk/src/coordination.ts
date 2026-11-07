import type { HostServiceContracts, RuntimeHost, ServiceDescriptor } from './index.js';

export const MANAGED_SESSION_SERVICE = 'host.managed-session';
function descriptor(name: string, methods: readonly string[]): ServiceDescriptor {
  return {name, version:'1.0.0', methods:methods.map(method => ({
    name:method, input_schema:`${name}.${method}.input.v1`, output_schema:`${name}.${method}.output.v1`,
  }))};
}
export const MANAGED_SESSION_DESCRIPTOR = descriptor(MANAGED_SESSION_SERVICE, ['create', 'submit', 'interrupt']);

export interface ManagedSessionControl {
  session_id: string;
  manager_id: string;
  revision: number;
  state: string;
}
/** session.InboxMessage uses exported Go field names on the wire. Omitted
 * fields retain their zero values; preserve a stable ClientID across delivery.
 */
export interface ManagedSessionInbox {
  ClientID: string;
  SessionID: string;
  RelatedSessionID?: string;
  Cause?: string;
  Content: string;
  Wake?: boolean;
  CreatedAt?: string;
  Controls?: ManagedSessionControl[] | null;
  WorkID?: string;
  WorkRevision?: number;
  WorkVersion?: number;
}
export type ManagedSessionCreate = HostServiceContracts['host.session.create']['params'] & {
  session_id?: string;
  owner: string;
  source: string;
  role?: string;
  shared_workspace_session_id?: string;
  initial_input?: ManagedSessionInbox;
};
/** Text-input subset of providers.ChatMessage, not ModelMessageV1. Its native
 * JSON field names are preserved; display_content is explicitly tagged.
 */
export interface ManagedSessionTextMessage {
  Role: string;
  Content: string;
  Name?: string;
  ClientID?: string;
  display_content?: string;
  Origin?: string;
  OriginID?: string;
  Cause?: string;
  PresentationKind?: string;
  RelatedSessionID?: string;
  ReadOnly?: boolean;
  Hidden?: boolean;
}
export interface ManagedSessionSubmit {
  session_id: string;
  message: ManagedSessionTextMessage;
  control?: ManagedSessionControl;
  allow_start: boolean;
}
export interface ManagedSessionSubmission { admitted: boolean; /** Always false; retained for v1 clients. */ capacity_full: boolean }

async function call<T>(host: RuntimeHost, service: string, method: string, params: object): Promise<T> {
  return await host.call('host.service.call', {service,method,params}) as T;
}
/** Native execution mechanisms used by a declared coordinator dependency.
 * The callback's RuntimeHost supplies execution authority, never owner/session
 * IDs in these payloads. No business scheduling or retry policy is added.
 */
export function managedSessions(host: RuntimeHost) {
  return {
    create: (input: ManagedSessionCreate) => call<HostServiceContracts['host.session.create']['result']>(host, MANAGED_SESSION_SERVICE, 'create', input),
    submit: (input: ManagedSessionSubmit) => call<ManagedSessionSubmission>(host, MANAGED_SESSION_SERVICE, 'submit', input),
    interrupt: (session_id: string) => call<Record<string, never>>(host, MANAGED_SESSION_SERVICE, 'interrupt', {session_id}),
  };
}
