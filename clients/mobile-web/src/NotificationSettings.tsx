import { useEffect, useState } from 'react';
import { isNative } from './lib/native';
import { useI18n } from '../../../desktop/src/renderer/i18n';
import { disableNotifications, enableNotifications, nativePushConfigured, notificationState } from './lib/notifications';

export function NotificationSettings(): React.JSX.Element | null {
  const { t } = useI18n();
  const [state, setState] = useState(notificationState);
  const [error, setError] = useState('');
  useEffect(() => { const update = () => setState(notificationState()); window.addEventListener('wuu:push-state', update); return () => window.removeEventListener('wuu:push-state', update); }, []);
  if (!isNative) return null;
  return <section className="account-notifications" aria-label={t('mobile.notifications')}>
    <div className="account-notification-row"><span>{t('mobile.notifications')}</span>
    {!nativePushConfigured ? <span className="account-setting-value">{t('mobile.pushUnavailable')}</span> :
      <button className="account-switch" type="button" role="switch" aria-checked={state.enabled} aria-label={t('mobile.notifications')} aria-busy={state.busy} disabled={state.busy} onClick={() => { setError(''); void (state.enabled ? disableNotifications() : enableNotifications()).catch(error => setError(String(error))); }}><span aria-hidden="true" /></button>}
    </div>
    {(error || state.error) && <p role="alert">{error || state.error}</p>}
  </section>;
}
