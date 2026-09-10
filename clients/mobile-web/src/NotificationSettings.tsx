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
  return <section className="account-panel" aria-label={t('mobile.notifications')}>
    <h2>{t('mobile.notifications')}</h2>
    <p>{t('mobile.notificationDescription')}</p>
    {!nativePushConfigured ? <p>{t('mobile.pushUnavailable')}</p> :
      <button type="button" disabled={state.busy} onClick={() => { setError(''); void (state.enabled ? disableNotifications() : enableNotifications()).catch(error => setError(String(error))); }}>{t(state.busy ? 'mobile.pushConnecting' : state.enabled ? 'mobile.pushDisable' : 'mobile.pushEnable')}</button>}
    {(error || state.error) && <p role="alert">{error || state.error}</p>}
  </section>;
}
