import {
  isDesktopSessionConnectionV1,
  type ConnectDesktopSessionOptionsV1,
  type ConnectDesktopSessionV1,
  type DesktopRemoteSessionRouteV1,
  type DesktopSessionConnectionHooksV1,
  type DesktopSessionConnectionV1,
  type DesktopSessionConnectorV1,
  type DesktopSessionContextV1,
  type DesktopSessionRouteConfigV1,
  type DesktopSessionRoutesV1,
  type DesktopSessionRPCV1,
  type DesktopSessionResponseErrorV1,
} from "@wuu/plugin-sdk";

// This synthetic structural adapter has no private client or protocol import.
// It exercises the author contract, not authentication or a network transport.
class IndependentConnection implements DesktopSessionConnectionV1 {
  private attached = false;
  private closed = false;
  private readonly protocol: DesktopSessionRPCV1 = {
    call: async <T = unknown>(method: string, params?: unknown, _timeoutMs?: number, workdir?: string, onResult?: (result: T) => void): Promise<T> => {
      if (this.closed || !this.attached) throw new Error("connection unavailable");
      if (method !== "thread/read") {
        throw Object.assign(new Error("unsupported fixture method"), {
          kind: "app-server-response", code: "method_not_found",
        } satisfies Omit<DesktopSessionResponseErrorV1, "message">);
      }
      const result = { method, params, workdir } as T;
      onResult?.(result);
      this.hooks.onNotification("fixture/after-response", {}, workdir);
      return result;
    },
    isClosed: () => this.closed,
  };

  constructor(private readonly hooks: DesktopSessionConnectionHooksV1) {}
  start(): void {
    this.attached = true;
    this.hooks.onAttach({ session: "synthetic-generation", resumed: false });
    this.hooks.onState({ running: [] });
  }
  async stop(): Promise<void> {
    this.closed = true;
    this.attached = false;
    this.hooks.onDetach();
  }
  async waitAttached(_timeoutMs?: number): Promise<void> {
    if (!this.attached) throw new Error("not attached");
  }
  rpc(): DesktopSessionRPCV1 | null { return this.attached ? this.protocol : null; }
}

export const connectDesktopSession: ConnectDesktopSessionV1 = ({ version, hooks }) => {
  if (version !== 1) throw new Error("unsupported connector version");
  return new IndependentConnection(hooks);
};
export const connector = { connectDesktopSession } satisfies DesktopSessionConnectorV1;
export const asyncConnector: ConnectDesktopSessionV1 = async options => connectDesktopSession(options);

const route: DesktopRemoteSessionRouteV1 = {
  kind: "remote", execution_host: "synthetic-host", workdir: "/remote/workspace",
  connector: { kind: "module", path: "/trusted/connector.mjs" },
};
export const config: DesktopSessionRouteConfigV1 = { version: 1, default: { kind: "local" }, contexts: { "/display/workspace": route } };
export const context: DesktopSessionContextV1 = {
  kind: "project", project_id: "project", cwd: "/display/workspace",
  session_locator: { version: 1, execution_host: "synthetic-host", session_id: "session" },
};
export const routes: DesktopSessionRoutesV1 = {
  select: context => config.contexts[context.cwd] ?? config.default,
  connect: (route, hooks) => connectDesktopSession({ version: 1, route, hooks }),
};

export function compileBoundary(value: unknown, options: ConnectDesktopSessionOptionsV1): void {
  if (isDesktopSessionConnectionV1(value)) {
    const connection: DesktopSessionConnectionV1 = value;
    connection.rpc()?.isClosed();
  }
  // @ts-expect-error A future connector version must not silently match v1.
  const future: ConnectDesktopSessionOptionsV1 = { ...options, version: 2 };
  // @ts-expect-error Route files carry credential references, not device secrets.
  const secret: DesktopRemoteSessionRouteV1["connector"] = { kind: "keychain", service: "reference", device_seed: "not-a-credential" };
  void future; void secret;
}
