import type { HostServiceContracts, RuntimeHost, ServiceDescriptor } from './index.js';

export const PROJECT_COORDINATION_SERVICE = 'project.coordination';
export const MANAGED_SESSION_SERVICE = 'host.managed-session';
function descriptor(name: string, methods: readonly string[]): ServiceDescriptor {
  return {name, version:'1.0.0', methods:methods.map(method => ({
    name:method, input_schema:`${name}.${method}.input.v1`, output_schema:`${name}.${method}.output.v1`,
  }))};
}
export const PROJECT_COORDINATION_DESCRIPTOR = descriptor(PROJECT_COORDINATION_SERVICE, [
  'scheduler', 'session', 'work', 'membership', 'create', 'turn', 'user-input',
  'inbox', 'schedule', 'recover', 'instructions', 'member-work', 'coordinator',
  'enqueue', 'capacity', 'work-list', 'tool-policy', 'tool-validate', 'tool-config', 'admission',
]);
export const MANAGED_SESSION_DESCRIPTOR = descriptor(MANAGED_SESSION_SERVICE, ['create', 'submit', 'interrupt']);

export interface ProjectWorkRequest {
  operation: string;
  work_id?: string;
  revision?: number;
  title?: string;
  brief?: string;
  acceptance?: string;
  authority?: string;
  source_refs?: string;
  model_alias?: string;
  summary?: string;
  evidence?: string;
  code_ref?: string;
  delivery?: string;
}
export interface ProjectSessionRequest {
  action: string;
  work?: ProjectWorkRequest;
  role?: string;
  model_alias?: string;
  wake?: boolean;
  session_id?: string;
  title?: string;
  prompt?: string;
  workspace?: string;
  query?: string;
  limit?: number;
  before?: number;
  block?: boolean;
  turn_id?: string;
  timeout_ms?: number;
}
export interface ProjectSessionCommand { actor_id: string; call_id?: string; session: ProjectSessionRequest }
export interface ProjectWorkCommand { actor_id: string; call_id?: string; work: ProjectWorkRequest }
export interface ProjectMembershipCommand { project_id: string; session_id: string; action: 'adopt' | 'release' }
export interface ProjectEnqueueCommand {
  project_id: string;
  session_id: string;
  client_id: string;
  cause: string;
  content: string;
  wake: boolean;
}
export interface ProjectAdmissionPolicy { max_parallel: number }
export interface ProjectToolPolicy { lease_key?: string; work_id?: string; revision?: number; phase?: string }
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
export interface ManagedSessionSubmission { admitted: boolean; capacity_full: boolean }

async function call<T>(host: RuntimeHost, service: string, method: string, params: object): Promise<T> {
  return await host.call('host.service.call', {service,method,params}) as T;
}
/** Common business commands. Session/work/membership results vary by action
 * and remain unknown until the caller validates them. Provider callbacks such
 * as instructions and tool-policy use their own native payloads, not these
 * command envelopes. See the public provider guide before implementing them.
 */
export function projectCoordination(host: RuntimeHost) {
  return {
    session: (input: ProjectSessionCommand) => call<unknown>(host, PROJECT_COORDINATION_SERVICE, 'session', input),
    work: (input: ProjectWorkCommand) => call<unknown>(host, PROJECT_COORDINATION_SERVICE, 'work', input),
    membership: (input: ProjectMembershipCommand) => call<unknown>(host, PROJECT_COORDINATION_SERVICE, 'membership', input),
    enqueue: (input: ProjectEnqueueCommand) => call<Record<string, never>>(host, PROJECT_COORDINATION_SERVICE, 'enqueue', input),
    admission: () => call<ProjectAdmissionPolicy>(host, PROJECT_COORDINATION_SERVICE, 'admission', {}),
  };
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
