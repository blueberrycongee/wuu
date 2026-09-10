import { b64encode } from './b64';
import { Identity, encodeKey } from './secure';
import { utf8Encode } from './bytes';
import type { Credentials } from './client';

export interface AccountDevice { pub: string; account: string; name: string; role: 'host' | 'phone'; added_at: number; online: boolean }
export interface AccountSession { server: string; token: string; username: string; pub: string; device_seed: string }
export function accountOrigin(raw: string): string {
 const u = new URL(raw.trim());
 if (u.username || u.password || u.search || u.hash || (u.pathname !== '/' && u.pathname !== '') ||
   (u.protocol !== 'https:' && !(u.protocol === 'http:' && ['localhost','127.0.0.1','[::1]'].includes(u.hostname)))) throw new Error('请使用 HTTPS 服务端地址（本机开发可使用 localhost）');
 return u.origin;
}
export class AccountRequestError extends Error {
 constructor(message: string, readonly status: number) { super(message); this.name = "AccountRequestError"; }
}
export async function accountRequest<T>(server: string, token: string, method: string, path: string, data?: unknown): Promise<T> {
 const response = await fetch(accountOrigin(server) + '/v1/account' + path, {
  method, headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: 'Bearer ' + token } : {}) },
  body: data === undefined ? undefined : JSON.stringify(data), signal: AbortSignal.timeout(15_000), redirect: 'error', credentials: 'omit',
 });
 const result = await response.json() as { error?: string };
 if (!response.ok) throw new AccountRequestError(result.error || `账号服务错误 (${response.status})`, response.status);
 return result as T;
}
/** Pass a persisted identity to keep the same device across account logins. */
export async function loginAccount(server: string, username: string, password: string, name: string, register = false, id = Identity.generate()): Promise<{ session: AccountSession; recovery?: string }> {
 username = username.trim().toLowerCase(); server = accountOrigin(server);
 const result = await accountRequest<{ token: string; username: string; pub: string; recovery?: string }>(server, '', 'POST', register ? '/register' : '/login', {
  username, password, name, role: 'phone', pub: encodeKey(id.public_()),
  proof: b64encode(id.signRelayAuth(utf8Encode('wuu/account/enroll/v1:' + username), 'phone')),
 });
 return { session: { server, token: result.token, username: result.username, pub: result.pub, device_seed: b64encode(id.seed()) }, recovery: result.recovery };
}
export function accountCredentials(session: AccountSession, host: AccountDevice): Credentials {
 if (host.role !== 'host' || host.account !== session.username) throw new Error('该电脑不属于当前账号');
 return { v: 1, account_username: session.username, device_seed: session.device_seed, device_name: 'Wuu 手机', host_pub: host.pub, host_name: host.name,
  relay_url: session.server.replace(/^http/, 'ws') + '/v1/connect' };
}

export interface GitHubPending { server: string; request_id: string; verifier: string; oauth_url: string; expires: number; name?: string }
export async function startGitHubLogin(server: string, native = false): Promise<GitHubPending> {
 server = accountOrigin(server);
 if (!globalThis.crypto?.subtle) throw new Error('GitHub 登录需要 HTTPS 网页或 Wuu App');
 const bytes = crypto.getRandomValues(new Uint8Array(32));
 const verifier = b64encode(bytes);
 const hash = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier)));
 const result = await accountRequest<{ request_id: string; authorize_url: string; expires_in: number }>(server, '', 'POST', '/github/start', { challenge: b64encode(hash), native });
 const url = new URL(result.authorize_url);
 if (url.origin !== server || url.username || url.password || url.pathname !== '/v1/account/github/authorize' || typeof result.request_id !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(result.request_id) || url.searchParams.get('state') !== result.request_id) throw new Error('Invalid authorization URL');
 return { server, request_id: result.request_id, verifier, oauth_url: result.authorize_url, expires: Date.now() + 600_000 };
}
export async function pollGitHubLogin(pending: GitHubPending): Promise<{ status: 'pending' | 'authorized'; username?: string }> {
 if (Date.now() >= pending.expires) throw new Error('GitHub login expired; start again');
 return accountRequest(pending.server, '', 'POST', '/github/poll', { request_id: pending.request_id, verifier: pending.verifier });
}
export async function completeGitHubLogin(pending: GitHubPending, username: string, name: string, id: Identity): Promise<AccountSession> {
 const pub = encodeKey(id.public_());
 const result = await accountRequest<{ token: string; username: string; pub: string }>(pending.server, '', 'POST', '/github/complete', {
  request_id: pending.request_id, verifier: pending.verifier, pub, role: 'phone', name,
  proof: b64encode(id.signRelayAuth(utf8Encode('wuu/account/enroll/v1:' + username), 'phone')),
 });
 if (result.username !== username || result.pub !== pub) throw new Error('Invalid enrollment response');
 return { ...result, server: pending.server, device_seed: b64encode(id.seed()) };
}
