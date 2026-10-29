import type { SessionLocatorV1 } from "./session-transport.js";
import type { ApplicationDesktopContextV1 } from "./ui-desktop.js";

/** Desktop app-server connection selection, not the session.transport federation
 * service. Host placement is descriptive; it grants no authority or data access. */
export type DesktopSessionRouteV1 =
  | { kind: "local" }
  | { kind: "disabled"; reason: string }
  | {
      kind: "remote";
      execution_host: string;
      storage_host?: string;
      display_host?: string;
      /** Remote filesystem path, never resolved against the display filesystem. */
      workdir: string;
      /** References only: credentials and device seeds must not enter route files. */
      connector:
        | { kind: "keychain"; service: string }
        | { kind: "module"; path: string };
    };
export type DesktopRemoteSessionRouteV1 = Extract<DesktopSessionRouteV1, { kind: "remote" }>;

export type DesktopSessionContextV1 = (ApplicationDesktopContextV1 | {
  kind: "user";
  user_space_id: string;
  product_domain: "personal" | "coding";
  cwd: string;
}) & {
  /** Explicit host-configured profile, never inferred from domain or cwd. */
  composition_profile?: string;
  session_locator?: SessionLocatorV1;
};
export interface DesktopSessionRouteConfigV1 {
  version: 1;
  default: DesktopSessionRouteV1;
  /** Keys identify local display workspaces, not remote workdirs. */
  contexts: Record<string, DesktopSessionRouteV1>;
}

/** Only an explicit app-server refusal may use this discriminator. A missing
 * response, timeout or disconnect does not prove that a request was rejected. */
export interface DesktopSessionResponseErrorV1 {
  readonly kind: "app-server-response";
  readonly message: string;
  readonly code?: string;
}

export interface DesktopSessionRPCV1 {
  /** Send once without replay. Honor timeoutMs when supplied; expiry does not
   * cancel admitted work. Invoke onResult synchronously before later notifications
   * and promise resolution; a callback error rejects the call. Explicit refusals
   * use DesktopSessionResponseErrorV1; other failures leave the outcome uncertain. */
  call<T = unknown>(method: string, params?: unknown, timeoutMs?: number, workdir?: string, onResult?: (result: T) => void): Promise<T>;
  isClosed(): boolean;
}
export interface DesktopSessionReverseRequestV1 {
  id: string | number;
  method: string;
  params?: unknown;
}
export interface DesktopSessionReverseResultV1 {
  result?: unknown;
  error?: { code?: string; message: string };
}
export interface DesktopSessionConnectionHooksV1 {
  onNotification(method: string, params: unknown, workdir?: string): void;
  /** Deliver the desktop's response, including refusals, to the requesting host.
   * A connection does not grant that host local browser or computer control. */
  onServerRequest(request: DesktopSessionReverseRequestV1): Promise<DesktopSessionReverseResultV1>;
  /** A resumed attach keeps the same RPC object and pending calls. Before a fresh
   * attach, replace and close the old RPC object, rejecting its pending calls. */
  onAttach(event: { session: string; resumed: boolean }): void;
  onDetach(): void;
  onState(state: { running: { thread_id?: string; workdir?: string }[] }): void;
}
export interface DesktopSessionConnectionV1 {
  /** Start the connection/reconnection loop. Repeated calls are harmless. */
  start(): void;
  /** Stop reconnecting, settle pending waiters/calls, and detach this GUI only.
   * Never send app-server shutdown or terminate remote tasks. Idempotent. */
  stop(): Promise<void>;
  /** Resolve after authenticated attachment, or reject when the supplied timeout
   * expires. The desktop currently supplies 10s here and 30s to RPC calls. */
  waitAttached(timeoutMs?: number): Promise<void>;
  /** Null before attachment; object identity is the connection-generation fence.
   * Retain it across resumed attachments; never reuse a closed generation. */
  rpc(): DesktopSessionRPCV1 | null;
}

export interface ConnectDesktopSessionOptionsV1 {
  version: 1;
  route: DesktopRemoteSessionRouteV1;
  hooks: DesktopSessionConnectionHooksV1;
}
/** Export this function as connectDesktopSession from the configured module.
 * Return an unstarted connection. The trusted connector authenticates the exact
 * execution_host and owns credentials/authority; a module is not a sandbox. */
export type ConnectDesktopSessionV1 = (options: ConnectDesktopSessionOptionsV1) =>
  DesktopSessionConnectionV1 | Promise<DesktopSessionConnectionV1>;
export interface DesktopSessionConnectorV1 {
  connectDesktopSession: ConnectDesktopSessionV1;
}
export interface DesktopSessionRoutesV1 {
  /** Preserve the complete context identity. User spaces are not projects:
   * unsupported context kinds must fail or select an explicit disabled route,
   * never silently fall through to a Coding workspace/default connection. */
  select(context: DesktopSessionContextV1): DesktopSessionRouteV1;
  connect(route: DesktopRemoteSessionRouteV1, hooks: DesktopSessionConnectionHooksV1):
    DesktopSessionConnectionV1 | Promise<DesktopSessionConnectionV1>;
}

/** Shallow loading check only. It does not authenticate a host, inspect trusted
 * module behavior, or certify lifecycle/protocol conformance. */
export function isDesktopSessionConnectionV1(value: unknown): value is DesktopSessionConnectionV1 {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    && ["start", "stop", "waitAttached", "rpc"].every(key => typeof (value as Record<string, unknown>)[key] === "function");
}
