import type { EngineAuthParams, EngineAuthResult } from "../shared/protocol";

type Request = (method: string, params?: unknown) => Promise<unknown>;
type Owner = {
  id: number;
  once(event: "destroyed", listener: () => void): unknown;
  removeListener(event: "destroyed", listener: () => void): unknown;
};

/** CLI and ACP login belong to the initiating window and process, even after
 * a workspace switch. Engine credentials may be shared across workspaces. */
export class EngineLogin {
  private current?: { owner: number; engineID: string; request: Request; cancelled: boolean; cancellation?: Promise<{ ok: boolean }> };

  constructor(private readonly onSuccess: (senderID: number) => void = () => {}) {}

  get active(): boolean { return this.current !== undefined; }

  async start(owner: Owner, bind: () => Request, params: EngineAuthParams): Promise<EngineAuthResult> {
    if (this.current) throw new Error("An engine sign-in is already running");
    const attempt = { owner: owner.id, engineID: params.engine_id, request: bind(), cancelled: false };
    this.current = attempt;
    const destroyed = () => {
      void this.cancel(owner.id).catch(() => {
        console.warn("Engine sign-in cancellation could not reach its original service");
      });
    };
    owner.once("destroyed", destroyed);
    try {
      const result = await attempt.request("engine/authenticate", params) as EngineAuthResult;
      if (result.authenticated && this.current === attempt && !attempt.cancelled) this.onSuccess(owner.id);
      return result;
    } finally {
      owner.removeListener("destroyed", destroyed);
      this.current = undefined;
    }
  }

  cancel(owner: number): Promise<{ ok: boolean }> {
    const attempt = this.current;
    if (!attempt || attempt.owner !== owner) return Promise.resolve({ ok: false });
    attempt.cancelled = true;
    return attempt.cancellation ??= attempt.request("engine/auth/cancel", { engine_id: attempt.engineID }) as Promise<{ ok: boolean }>;
  }
}
