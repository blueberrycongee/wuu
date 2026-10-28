import type { HostServiceContracts, RuntimeHost, ServiceDescriptor } from "./index.js";

export const SESSION_TRANSPORT_SERVICE = "session.transport";
export const SESSION_TRANSPORT_DESCRIPTOR: ServiceDescriptor = {
  name: SESSION_TRANSPORT_SERVICE, version: "1.0.0",
  methods: [{ name: "dispatch", input_schema: "session.transport.dispatch.input.v1", output_schema: "session.transport.dispatch.output.v1" }],
};

/** Execution routing is independent of storage and presentation. The latter two
 * fields are descriptive, not credentials or authority to access another host. */
export interface SessionLocatorV1 {
  version: 1;
  execution_host: string;
  session_id?: string;
  storage_host?: string;
  display_host?: string;
}
export type SessionRouteOperation = "create" | "send" | "inspect" | "cancel" | "list";
export type SessionRouteRequest = {
  [Operation in SessionRouteOperation]: {
    version: 1;
    operation: Operation;
    /** Persist with the originating intent. Retry the same ID, never a fresh ID. */
    operation_id: string;
    locator: SessionLocatorV1;
    params: HostServiceContracts[`host.session.${Operation}`]["params"];
  };
}[SessionRouteOperation];
export interface SessionRouteResult {
  version: 1;
  operation_id: string;
  locator: SessionLocatorV1;
  /** completed describes the command receipt, not completion of an agent turn. */
  status: "completed" | "pending" | "unknown";
  result?: unknown;
  error?: { code: string; message: string };
}

function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
function token(value: unknown): value is string {
  return typeof value === "string" && value.trim() !== "" && value.length <= 256 && !value.includes("\0");
}

/** Validate before any network side effect. Host policy still independently
 * validates identities, resources, model choices and delegated permissions. */
export function validateSessionRoute(input: unknown): asserts input is SessionRouteRequest {
  if (!record(input) || input.version !== 1 || !token(input.operation_id)
    || !["create", "send", "inspect", "cancel", "list"].includes(String(input.operation))
    || !record(input.locator) || input.locator.version !== 1 || !token(input.locator.execution_host)
    || !record(input.params)) throw new TypeError("Invalid session route v1 request");
  const { params, locator } = input;
  for (const key of ["session_id", "storage_host", "display_host"]) {
    if (locator[key] !== undefined && !token(locator[key])) throw new TypeError(`Invalid session locator ${key}`);
  }
  if (["create", "list"].includes(String(input.operation)) && locator.session_id !== undefined) {
    throw new TypeError("Create and list routes do not identify an existing session");
  }
  if (["send", "inspect", "cancel"].includes(String(input.operation))
    && (!token(locator.session_id) || params.session_id !== locator.session_id)) {
    throw new TypeError("Session route must target the exact located session");
  }
  if (["send", "create"].includes(String(input.operation))
    && params.request_id !== undefined && params.request_id !== "" && params.request_id !== input.operation_id) {
    throw new TypeError("Session route operation and request IDs differ");
  }
  if (input.operation === "cancel" && !token(params.turn_id) && !token(params.queue_id)) {
    throw new TypeError("Session cancellation requires an exact turn or queue ID");
  }
}

export function sessionRouter(host: RuntimeHost) {
  return {
    async dispatch(request: SessionRouteRequest, signal?: AbortSignal): Promise<SessionRouteResult> {
      validateSessionRoute(request);
      return await host.call("host.session.route", request, { signal }) as SessionRouteResult;
    },
  };
}

/** Structural public interface: the existing encrypted RemoteClient satisfies
 * this without importing a repository-private package into an external plugin. */
export interface SessionRemoteConnection {
  isAttached(): boolean;
  call(method: string, params?: unknown, timeoutMs?: number, workdir?: string): Promise<unknown>;
}
export class SessionTransportError extends Error {
  constructor(readonly code: "session_host_unavailable" | "session_host_offline", message: string) {
    super(message); this.name = "SessionTransportError";
  }
}

function unknownReceipt(request: SessionRouteRequest, code: string): SessionRouteResult {
  return { version: 1, operation_id: request.operation_id, locator: { ...request.locator }, status: "unknown",
    error: { code, message: "Delivery may have been accepted. Reconcile or retry the same operation ID; do not recreate the operation." } };
}

function validReceipt(value: unknown, request: SessionRouteRequest): value is SessionRouteResult {
  if (!record(value) || value.version !== 1 || value.operation_id !== request.operation_id
    || !["completed", "pending", "unknown"].includes(String(value.status)) || !record(value.locator)
    || value.locator.version !== 1 || value.locator.execution_host !== request.locator.execution_host) return false;
  if (request.locator.session_id !== undefined && value.locator.session_id !== request.locator.session_id) return false;
  if (request.operation === "create" && value.status === "completed" && value.error === undefined && !token(value.locator.session_id)) return false;
  return value.error === undefined || (record(value.error) && typeof value.error.code === "string" && typeof value.error.message === "string");
}

/** A selected provider can forward dispatch to this adapter. It never pairs,
 * acquires permissions, substitutes hosts or automatically replays commands.
 * Aborting after dispatch ends the wait, not the already accepted remote work;
 * cancel that work with a separate exact-target cancel operation. */
export function remoteSessionTransport(
  connection: (executionHost: string) => SessionRemoteConnection | undefined,
  options: { timeoutMs?: number; workdir?: (executionHost: string) => string | undefined } = {},
) {
  return {
    async dispatch(request: SessionRouteRequest, signal?: AbortSignal): Promise<SessionRouteResult> {
      validateSessionRoute(request);
      signal?.throwIfAborted();
      const client = request.locator.execution_host === "local" ? undefined : connection(request.locator.execution_host);
      if (!client) throw new SessionTransportError("session_host_unavailable", "No transport is selected for this execution host");
      if (!client.isAttached()) throw new SessionTransportError("session_host_offline", "The selected execution host is offline; no request was sent");
      let onAbort: (() => void) | undefined;
      try {
        const response = client.call("session/federation", request, options.timeoutMs, options.workdir?.(request.locator.execution_host));
        const aborted = new Promise<SessionRouteResult>(resolve => {
          onAbort = () => resolve(unknownReceipt(request, "session_wait_cancelled"));
          signal?.addEventListener("abort", onAbort, { once: true });
          if (signal?.aborted) onAbort();
        });
        const value = await (signal ? Promise.race([response, aborted]) : response);
        return validReceipt(value, request) ? value : unknownReceipt(request, "session_invalid_receipt");
      } catch {
        return unknownReceipt(request, "session_delivery_unknown");
      } finally {
        if (onAbort) signal?.removeEventListener("abort", onAbort);
      }
    },
  };
}
