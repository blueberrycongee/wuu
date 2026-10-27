import { execFile } from "node:child_process";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { promisify } from "node:util";
import { pathToFileURL } from "node:url";
import { RemoteClient, type Credentials } from "../../../clients/core/src/client";
import {
  isDesktopSessionConnectionV1,
  type ConnectDesktopSessionOptionsV1,
  type DesktopSessionRouteV1 as SessionRoute,
  type DesktopRemoteSessionRouteV1 as RemoteSessionRoute,
  type DesktopSessionConnectionHooksV1 as SessionConnectionHooks,
  type DesktopSessionConnectionV1 as DesktopSessionConnection,
  type DesktopSessionRoutesV1 as DesktopSessionRoutes,
  type DesktopSessionRouteConfigV1 as SessionRouteConfig,
} from "../../../packages/plugin-sdk/src/desktop-session-transport";

// Native and external connections use the same public contract. Keep the local
// aliases for existing pool callers, not a second set of private definitions.
export type {
  DesktopSessionRouteV1 as SessionRoute,
  DesktopRemoteSessionRouteV1 as RemoteSessionRoute,
  DesktopSessionRPCV1 as SessionRPC,
  DesktopSessionReverseRequestV1 as SessionReverseRequest,
  DesktopSessionReverseResultV1 as SessionReverseResult,
  DesktopSessionConnectionHooksV1 as SessionConnectionHooks,
  DesktopSessionConnectionV1 as DesktopSessionConnection,
  DesktopSessionRoutesV1 as DesktopSessionRoutes,
  DesktopSessionRouteConfigV1 as SessionRouteConfig,
} from "../../../packages/plugin-sdk/src/desktop-session-transport";

function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
function requiredString(value: unknown, field: string): string {
  if (typeof value !== "string" || !value.trim()) throw new Error(`Invalid desktop session route: ${field} is required`);
  return value;
}
function parseRoute(value: unknown): SessionRoute {
  if (!record(value)) throw new Error("Invalid desktop session route");
  if (value.kind === "local") return { kind: "local" };
  if (value.kind === "disabled") return { kind: "disabled", reason: typeof value.reason === "string" ? value.reason : "selected connection is disabled" };
  if (value.kind !== "remote") throw new Error("Unknown desktop session route kind");
  const execution_host = requiredString(value.execution_host, "execution_host");
  if (execution_host === "local") throw new Error("Remote execution_host must not use reserved local identity");
  const route: RemoteSessionRoute = {
    kind: "remote", execution_host,
    workdir: requiredString(value.workdir, "workdir"),
    connector: parseConnector(value.connector),
  };
  if (value.storage_host !== undefined) route.storage_host = requiredString(value.storage_host, "storage_host");
  if (value.display_host !== undefined) route.display_host = requiredString(value.display_host, "display_host");
  return route;
}
function parseConnector(value: unknown): RemoteSessionRoute["connector"] {
  if (!record(value)) throw new Error("A desktop session connector is required");
  switch (value.kind) {
    case "keychain": return { kind: "keychain", service: requiredString(value.service, "connector service") };
    case "module": return { kind: "module", path: resolve(requiredString(value.path, "connector module path")) };
    default: throw new Error("Unknown desktop session connector");
  }
}
export function parseSessionRoutes(value: unknown): SessionRouteConfig {
  if (!record(value) || value.version !== 1) throw new Error("Unsupported desktop session routes version");
  const contexts: Record<string, SessionRoute> = {};
  if (value.contexts !== undefined) {
    if (!record(value.contexts)) throw new Error("Invalid desktop session routes contexts");
    for (const [cwd, route] of Object.entries(value.contexts)) contexts[resolve(cwd)] = parseRoute(route);
  }
  return { version: 1, default: parseRoute(value.default), contexts };
}

const execFileAsync = promisify(execFile);
async function configuredConnection(route: RemoteSessionRoute, hooks: SessionConnectionHooks): Promise<DesktopSessionConnection> {
  if (route.connector.kind === "module") {
    const connector: unknown = await import(/* @vite-ignore */ pathToFileURL(route.connector.path).href);
    if (!record(connector) || typeof connector.connectDesktopSession !== "function") throw new Error("Desktop session connector must export connectDesktopSession");
    const connection: unknown = await connector.connectDesktopSession({ version: 1, route, hooks } satisfies ConnectDesktopSessionOptionsV1);
    if (!isDesktopSessionConnectionV1(connection)) throw new Error("Invalid desktop session connection interface");
    return connection;
  }
  if (process.platform !== "darwin") throw new Error("The bundled remote desktop connector requires macOS Keychain");
  try {
    const { stdout } = await execFileAsync("/usr/bin/security", ["find-generic-password", "-s", route.connector.service, "-w"], { timeout: 10_000, maxBuffer: 16_384 });
    const value: unknown = JSON.parse(stdout);
    if (!record(value) || value.v !== 1) throw new Error("Invalid remote desktop credential version");
    const credentials: Credentials = {
      v: 1,
      device_seed: requiredString(value.device_seed, "credential device_seed"),
      host_pub: requiredString(value.host_pub, "credential host_pub"),
      relay_url: requiredString(value.relay_url, "credential relay_url"),
    };
    // A configured name cannot claim a different authenticated host identity.
    if (credentials.host_pub !== route.execution_host) throw new Error("Remote route does not match the credential's authenticated host");
    return new RemoteClient(credentials, hooks);
  } catch {
    // A malformed secret must never be included in a JSON parse diagnostic.
    throw new Error("The selected remote desktop credential is unavailable or does not match the host");
  }
}
export function defaultDesktopSessionRoutes(): DesktopSessionRoutes {
  let previous = "", config: SessionRouteConfig | undefined;
  return {
    select(context) {
      const path = process.env.WUU_DESKTOP_SESSION_ROUTES;
      if (!path) return { kind: "local" };
      const content = readFileSync(path, "utf8");
      if (content !== previous || !config) {
        config = parseSessionRoutes(JSON.parse(content));
        previous = content;
      }
      return config.contexts[resolve(context.cwd)] ?? config.default;
    },
    connect: configuredConnection,
  };
}

export function sessionRouteHost(route: SessionRoute): string {
  return route.kind === "remote" ? route.execution_host : "local";
}
export function sessionRouteKey(route: SessionRoute): string {
  return JSON.stringify(route);
}

/** No automatic replay: a transport failure can follow remote admission. */
export class SessionOutcomeUnknownError extends Error {
  readonly code = "session_outcome_unknown";
  constructor(detail: string) { super(`Session request outcome is unknown: ${detail}. Inspect the session before retrying.`); }
}
