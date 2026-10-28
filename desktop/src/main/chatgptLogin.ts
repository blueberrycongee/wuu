import type { AuthChatGPTLoginResult, RuntimeContext } from "../shared/protocol";
import { AppServerResponseError } from "./appServerErrors";

type LoginWindow = {
  id: number;
  isDestroyed: () => boolean;
  once: (event: "destroyed", listener: () => void) => unknown;
  removeListener: (event: "destroyed", listener: () => void) => unknown;
};
type LoginRequest = (context: RuntimeContext, method: string, params?: { login_id?: string; account_id?: string; force_consent?: boolean }) => Promise<unknown>;
type LoginOwner = {
  loginID: string;
  senderID: number;
  context: RuntimeContext;
  dispose: () => void;
  returnAllowed: boolean;
};

// The credential store is user-wide although each project has its own server.
// Ownership survives transport errors until cancellation or backend expiry.
export class ChatGPTLoginCoordinator {
  private current: LoginOwner | undefined;
  constructor(private readonly request: LoginRequest, private readonly openBrowser: (url: string) => Promise<unknown>, private readonly onSuccess: (senderID: number) => void = () => {}) {}

  async start(window: LoginWindow, context: RuntimeContext, accountID?: string, enablePlanUsage?: boolean): Promise<AuthChatGPTLoginResult> {
    if (this.current) throw new Error("A ChatGPT login is already running; finish or cancel it first.");
    const owner: LoginOwner = { loginID: "", senderID: window.id, context, dispose: () => {}, returnAllowed: true };
    this.current = owner;
    let expiry: ReturnType<typeof setTimeout> | undefined;
    const cancel = () => {
      owner.returnAllowed = false;
      if (!owner.loginID) return;
      void this.request(context, "auth/chatgpt/login/cancel", { login_id: owner.loginID })
        .then(() => owner.dispose()).catch(() => {});
    };
    owner.dispose = () => {
      owner.returnAllowed = false;
      clearTimeout(expiry);
      window.removeListener("destroyed", cancel);
      if (this.current === owner) this.current = undefined;
    };
    window.once("destroyed", cancel);
    // If the start response is lost, there is no server-issued ID to cancel.
    // Allow the server's 15-minute authorization limit plus transport/shutdown margin.
    expiry = setTimeout(() => owner.dispose(), 16 * 60 * 1000);
    try {
      const result = await this.request(context, "auth/chatgpt/login/start", { account_id: accountID, ...(enablePlanUsage ? { force_consent: true } : {}) }) as AuthChatGPTLoginResult;
      owner.loginID = result.login_id;
      clearTimeout(expiry);
      expiry = setTimeout(() => {
        cancel();
        // The backend enforces its own lifetime even if transport is broken.
        owner.dispose();
      }, (result.expires_in + 5) * 1000);
      if (window.isDestroyed()) { cancel(); return this.publicResult(result); }
      if (result.status === "pending") {
        try {
          const url = new URL(result.authorization_url ?? "");
          if (url.origin !== "https://auth.openai.com" || url.pathname !== "/api/accounts/authorize" || url.username || url.password) {
            throw new Error("Invalid ChatGPT authorization URL");
          }
          await this.openBrowser(url.toString());
        } catch {
          await this.cancel(window.id, owner.loginID);
          throw new AppServerResponseError("Could not open ChatGPT sign-in. Please retry.");
        }
      }
      this.finish(owner, result);
      return this.publicResult(result);
    } catch (error) {
      if (error instanceof AppServerResponseError) {
        owner.dispose();
        throw error;
      }
      throw new Error("ChatGPT login status is unknown and it may still be running. Wait for it to expire, or close and restart Wuu before trying again.", { cause: error });
    }
  }

  async poll(senderID: number, loginID: string): Promise<AuthChatGPTLoginResult> {
    const owner = this.current;
    if (!owner || owner.loginID !== loginID || owner.senderID !== senderID) {
      throw new Error("ChatGPT login was not found; start again.");
    }
    try {
      const result = await this.request(owner.context, "auth/chatgpt/login/poll", { login_id: loginID }) as AuthChatGPTLoginResult;
      this.finish(owner, result);
      return this.publicResult(result);
    } catch (error) {
      owner.returnAllowed = false;
      // A failed poll does not mean the login attempt stopped.
      try {
        await this.request(owner.context, "auth/chatgpt/login/cancel", { login_id: loginID });
        owner.dispose();
      } catch { /* The expiry timer retains responsibility for cleanup. */ }
      throw error;
    }
  }

  private finish(owner: LoginOwner, result: AuthChatGPTLoginResult): void {
    if (result.status === "pending") return;
    const shouldReturn = result.status === "success" && this.current === owner && owner.returnAllowed;
    owner.dispose();
    if (shouldReturn) this.onSuccess(owner.senderID);
  }

  private publicResult(result: AuthChatGPTLoginResult): AuthChatGPTLoginResult {
    const { authorization_url: _url, ...safe } = result;
    return safe;
  }

  async cancel(senderID: number, loginID: string): Promise<{ ok: boolean }> {
    const owner = this.current;
    if (!owner || owner.loginID !== loginID || owner.senderID !== senderID) return { ok: true };
    owner.returnAllowed = false;
    const result = await this.request(owner.context, "auth/chatgpt/login/cancel", { login_id: loginID }) as { ok: boolean };
    owner.dispose();
    return result;
  }
}
