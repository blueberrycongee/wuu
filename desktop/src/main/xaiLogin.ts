import type { AuthXAILoginPollResult, AuthXAILoginStartResult } from "../shared/protocol";

type Request = (method: string, params?: unknown) => Promise<unknown>;
type Owner = {
  id: number;
  isDestroyed(): boolean;
  once(event: "destroyed", listener: () => void): unknown;
  removeListener(event: "destroyed", listener: () => void): unknown;
};
type Attempt = {
  owner: number;
  loginID: string;
  request: Request;
  returnAllowed: boolean;
  dispose(): void;
};

/** Device authorization stays with its initiating window and server across workspace changes. */
export class XAILoginCoordinator {
  private current?: Attempt;
  constructor(private readonly onSuccess: (senderID: number) => void) {}

  async start(owner: Owner, bind: () => Request): Promise<AuthXAILoginStartResult> {
    if (this.current) throw new Error("A SuperGrok sign-in is already running; finish or cancel it first.");
    const attempt: Attempt = { owner: owner.id, loginID: "", request: bind(), returnAllowed: true, dispose: () => {} };
    this.current = attempt;
    let expiry: ReturnType<typeof setTimeout> | undefined;
    const cancel = () => {
      attempt.returnAllowed = false;
      if (attempt.loginID) void this.cancel(owner.id, attempt.loginID).catch(() => {});
    };
    attempt.dispose = () => {
      attempt.returnAllowed = false;
      clearTimeout(expiry);
      owner.removeListener("destroyed", cancel);
      if (this.current === attempt) this.current = undefined;
    };
    owner.once("destroyed", cancel);
    try {
      const result = await attempt.request("auth/xai/login/start") as AuthXAILoginStartResult;
      attempt.loginID = result.login_id;
      expiry = setTimeout(() => { cancel(); attempt.dispose(); }, (result.expires_in + 5) * 1000);
      if (owner.isDestroyed()) cancel();
      return result;
    } catch (error) {
      attempt.dispose();
      throw error;
    }
  }

  async poll(senderID: number, loginID: string): Promise<AuthXAILoginPollResult> {
    const attempt = this.current;
    if (!attempt || attempt.owner !== senderID || attempt.loginID !== loginID) throw new Error("SuperGrok sign-in was not found; start again.");
    try {
      const result = await attempt.request("auth/xai/login/poll", { login_id: loginID }) as AuthXAILoginPollResult;
      if (result.status !== "pending") {
        const shouldReturn = result.status === "success" && this.current === attempt && attempt.returnAllowed;
        attempt.dispose();
        if (shouldReturn) this.onSuccess(senderID);
      }
      return result;
    } catch (error) {
      attempt.returnAllowed = false;
      // Retain ownership if cleanup cannot reach the original server.
      try { await this.cancel(senderID, loginID); } catch { /* Expiry still releases the attempt. */ }
      throw error;
    }
  }

  async cancel(senderID: number, loginID: string): Promise<{ ok: boolean }> {
    const attempt = this.current;
    if (!attempt || attempt.owner !== senderID || attempt.loginID !== loginID) return { ok: false };
    attempt.returnAllowed = false;
    const result = await attempt.request("auth/xai/login/cancel", { login_id: loginID }) as { ok: boolean };
    attempt.dispose();
    return result;
  }
}
