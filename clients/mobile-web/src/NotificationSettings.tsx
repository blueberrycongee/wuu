import { useEffect, useState } from 'react';
import { isNative } from './lib/native';
import { disableNotifications, enableNotifications, nativePushConfigured, notificationState } from './lib/notifications';

export function NotificationSettings(): React.JSX.Element | null {
  const [state, setState] = useState(notificationState);
  const [error, setError] = useState('');
  useEffect(() => { const update = () => setState(notificationState()); window.addEventListener('wuu:push-state', update); return () => window.removeEventListener('wuu:push-state', update); }, []);
  if (!isNative) return null;
  return <section className="account-panel" aria-label="系统通知">
    <h2>系统通知</h2>
    <p>在后台收到任务完成或需要输入的提醒。通知不包含会话正文。</p>
    {!nativePushConfigured ? <p>此安装包未配置推送。返回 App 后仍会恢复电脑上的最新会话。</p> :
      <button type="button" disabled={state.busy} onClick={() => { setError(''); void (state.enabled ? disableNotifications() : enableNotifications()).catch(error => setError(String(error))); }}>{state.busy ? '正在连接…' : state.enabled ? '关闭通知' : '开启通知'}</button>}
    {(error || state.error) && <p role="alert">{error || state.error}</p>}
  </section>;
}
