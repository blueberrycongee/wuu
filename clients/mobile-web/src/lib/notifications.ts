import { Capacitor } from '@capacitor/core';
import { PushNotifications } from '@capacitor/push-notifications';
import { accountRequest } from '@wuu/remote-core';
import { loadAccount } from './accountStore';

const preference = 'wuu.push.enabled';
export const nativePushConfigured = Capacitor.isNativePlatform() &&
  String(import.meta.env.VITE_WUU_PUSH_PLATFORMS || '').split(',').includes(Capacitor.getPlatform());
let started = false;
let registrationWork: Promise<void> = Promise.resolve();
let pendingHost = '';
let pending: { resolve: () => void; reject: (error: Error) => void } | undefined;
let state = { enabled: false, error: '', busy: false };
const publish = () => window.dispatchEvent(new Event('wuu:push-state'));
export const notificationState = () => state;
export function consumeNotificationHost(): string { const host = pendingHost; pendingHost = ''; return host; }
const fail = (error: unknown) => {
  state = { ...state, busy: false, error: error instanceof Error ? error.message : String(error) };
  pending?.reject(new Error(state.error)); pending = undefined; publish();
};

async function registerToken(token: string): Promise<void> {
  if (localStorage.getItem(preference) !== 'true') return;
  const session = await loadAccount();
  if (!session) return;
  await accountRequest(session.server, session.token, 'POST', '/push', { platform: Capacitor.getPlatform(), token });
  const current = await loadAccount();
  if (current?.token !== session.token || current.server !== session.server) throw new Error('账号已切换，请重新开启通知。');
  state = { enabled: true, busy: false, error: '' }; pending?.resolve(); pending = undefined; publish();
}
export async function startPushLifecycle(): Promise<void> {
  if (started || !nativePushConfigured) return;
  started = true;
  await PushNotifications.addListener('registration', token => { registrationWork = registrationWork.then(() => registerToken(token.value)).catch(fail); });
  await PushNotifications.addListener('registrationError', error => fail(error.error));
  await PushNotifications.addListener('pushNotificationActionPerformed', action => {
    const host = action.notification.data?.host;
    if (typeof host === 'string' && host.length <= 128) { pendingHost = host; window.dispatchEvent(new Event('wuu:notification-open')); }
  });
  const refresh = () => {
    if (localStorage.getItem(preference) === 'true') void enableNotifications(false).catch(fail);
    else { state = { enabled: false, busy: false, error: '' }; publish(); }
  };
  window.addEventListener('wuu:account-change', refresh);
  window.addEventListener('online', refresh);
  refresh();
}
export async function enableNotifications(ask = true): Promise<void> {
  if (!nativePushConfigured) throw new Error('此安装包未配置系统推送，请按构建文档启用 APNs 或 FCM。');
  if (state.busy) return;
  state = { ...state, busy: true, error: '' }; publish();
  try {
    const session = await loadAccount(); if (!session) throw new Error('请先登录账号');
    const config = await accountRequest<{ push_platforms?: string[] }>(session.server, '', 'GET', '/config');
    if (!config.push_platforms?.includes(Capacitor.getPlatform())) throw new Error('此服务端尚未配置当前平台的推送凭据。');
    let permission = await PushNotifications.checkPermissions();
    if (ask && (permission.receive === 'prompt' || permission.receive === 'prompt-with-rationale')) permission = await PushNotifications.requestPermissions();
    if (permission.receive !== 'granted') throw new Error('系统通知权限未开启，请在系统设置中允许 Wuu 通知。');
    localStorage.setItem(preference, 'true');
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => { pending = undefined; reject(new Error('系统推送注册超时，请检查平台配置后重试。')); }, 15000);
      pending = { resolve: () => { clearTimeout(timer); resolve(); }, reject: error => { clearTimeout(timer); reject(error); } };
      void PushNotifications.register().catch(error => { pending?.reject(error); pending = undefined; });
    });
  } catch (error) { fail(error); throw error; }
}
export async function disableNotifications(): Promise<void> {
  if (state.busy) return;
  state = { ...state, busy: true, error: '' }; publish();
  localStorage.removeItem(preference);
  try {
    // Drain an already submitted token refresh before deleting its destination.
    await registrationWork;
    const session = await loadAccount();
    if (session) await accountRequest(session.server, session.token, 'DELETE', '/push');
    if (nativePushConfigured) await PushNotifications.unregister();
    state = { enabled: false, busy: false, error: '' }; publish();
  } catch (error) { fail(error); throw error; }
}
