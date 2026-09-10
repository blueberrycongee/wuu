import './AccountPanel.css';
import { useEffect, useRef, useState } from 'react';
import { useI18n } from './i18n';

export type AccountAction = 'status' | 'login' | 'register' | 'recover' | 'logout' | 'revoke' | 'password';
export type AccountDeviceView = { pub: string; account: string; name: string; role: 'host' | 'phone'; online: boolean; added_at: number };
export type AccountView = { username?: string; server?: string; pub?: string; devices?: AccountDeviceView[]; recovery?: string; localLogoutOnly?: boolean };
export type AccountDriver = (action: AccountAction, input?: Record<string, string>) => Promise<AccountView>;

/** Shared account forms; each host owns credential storage and transport. */
export function AccountPanel({ driver, onComputer }: { driver: AccountDriver; onComputer?: (device: AccountDeviceView) => void }): React.JSX.Element {
 const { t } = useI18n();
 const epoch = useRef(0);
 const [account, setAccount] = useState<AccountView>({});
 const [mode, setMode] = useState<'login' | 'register' | 'recover' | 'password'>('login');
 const [server, setServer] = useState(''); const [username, setUsername] = useState('');
 const [password, setPassword] = useState(''); const [secret, setSecret] = useState('');
 const [deviceName, setDeviceName] = useState('');
 const [localLogoutOnly, setLocalLogoutOnly] = useState(false);
 const [error, setError] = useState(''); const [busy, setBusy] = useState(false); const [recovery, setRecovery] = useState('');
 useEffect(() => {
  let active = true; let running = false;
  const refresh = async () => { if (running || document.visibilityState === 'hidden') return; running = true; const generation = epoch.current;
   try { const next = await driver('status'); if (active && generation === epoch.current) setAccount(next); } catch (e) { if (active && generation === epoch.current) setError(String(e instanceof Error ? e.message : e)); } finally { running = false; }
  };
  void refresh(); const timer = setInterval(() => void refresh(), 5000);
  document.addEventListener('visibilitychange', refresh); window.addEventListener('online', refresh);
  return () => { active = false; clearInterval(timer); document.removeEventListener('visibilitychange', refresh); window.removeEventListener('online', refresh); };
 }, [driver]);
 const perform = async (action: AccountAction, input?: Record<string,string>) => {
  if (busy) return; epoch.current++; setBusy(true); setError(''); setLocalLogoutOnly(false);
  try {
   const result = await driver(action,input); if(result.recovery) setRecovery(result.recovery); else if(action === 'logout' || action === 'login') setRecovery('');
   setLocalLogoutOnly(result.localLogoutOnly === true);
   setPassword(''); setSecret(''); setMode('login'); setAccount(await driver('status'));
  } catch(e) { setError(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
 };
 return <section className="account-panel" aria-label={t('account.label')}>
  <h2>{account.username ? t('account.title', {username: account.username}) : t('account.connect')}</h2>
  <p>{t('account.description')}</p>
  {error && <p role="alert" className="settings-error">{error}</p>}
  {localLogoutOnly && <p role="status">{t('account.localLogout')}</p>}
  {recovery && <div role="status"><p>{t('account.saveRecovery')}</p><code style={{overflowWrap:'anywhere',userSelect:'all'}}>{recovery}</code><p><button type="button" onClick={() => setRecovery('')}>{t('account.savedRecovery')}</button></p></div>}
  {account.username && mode !== 'password' ? <>
   <p className="account-server">{account.server}</p>
   {(['host', 'phone'] as const).map(role => <section className="account-device-group" key={role} aria-label={t(role === 'host' ? 'account.computers' : 'account.phones')}>
    <h3>{t(role === 'host' ? 'account.computers' : 'account.phones')}</h3>
    <div className="account-devices">{(account.devices ?? []).filter(d => d.role === role).sort((a,b) => Number(b.pub === account.pub) - Number(a.pub === account.pub) || Number(b.online) - Number(a.online) || a.added_at - b.added_at).map(d => <div className="account-device" key={d.pub}>
     <div className="account-device-info"><strong>{d.name || t(role === 'host' ? 'account.computer' : 'account.phone')}</strong><p><span className="account-device-status" data-online={d.online}>{t(d.online ? 'account.online' : 'account.offline')}</span>{d.pub === account.pub && <span className="account-current">{t('account.current')}</span>}</p></div>
     <div className="account-device-actions">
      {onComputer && role === 'host' && <button className="account-primary" type="button" disabled={!d.online || busy} onClick={() => onComputer(d)}>{t('account.open')}</button>}
      {d.pub !== account.pub && <button className="account-remove" type="button" disabled={busy} onClick={() => void perform('revoke',{pub:d.pub})}>{t('account.remove')}</button>}
     </div>
    </div>)}</div>
    {role === 'host' && <p>{t((account.devices ?? []).some(d => d.role === 'host') ? 'account.offlineHint' : 'account.noComputers')}</p>}
   </section>)}
   <div className="account-actions"><button type="button" disabled={busy} onClick={() => setMode('password')}>{t('account.password')}</button><button type="button" disabled={busy} onClick={() => void perform('logout')}>{t('account.logout')}</button></div>
  </> : <form onSubmit={e => {e.preventDefault();void perform(mode,{server,username:account.username || username,password,secret,name:deviceName});}}>
   {!account.username && <>
    <label>{t('account.server')}<input type="url" autoCapitalize="none" autoCorrect="off" placeholder="https://wuu.example.com" value={server} required onChange={e => setServer(e.target.value)}/></label>
    <label>{t('account.username')}<input autoComplete="username" autoCapitalize="none" autoCorrect="off" value={username} required minLength={3} onChange={e => setUsername(e.target.value)}/></label>
   </>}
   {onComputer && (mode === 'login' || mode === 'register') && <label>{t('account.deviceName')}<input value={deviceName} maxLength={64} placeholder={t('account.deviceNameExample')} onChange={e => setDeviceName(e.target.value)}/></label>}
   {(mode === 'recover' || mode === 'password') && <label>{t(mode === 'recover' ? 'account.recoveryCode' : 'account.currentPassword')}<input type="password" autoComplete={mode === 'password' ? 'current-password' : 'off'} value={secret} required onChange={e => setSecret(e.target.value)}/></label>}
   <label>{t(mode === 'login' ? 'account.loginPassword' : 'account.newPassword')}<input type="password" autoComplete={mode === 'login' ? 'current-password' : 'new-password'} value={password} required minLength={12} onChange={e => setPassword(e.target.value)}/></label>
   {mode === 'register' && <p>{t('account.registerHint')}</p>}
   {mode === 'password' && <p>{t('account.passwordHint')}</p>}
   <button className="account-primary" disabled={busy} type="submit">{t(busy ? 'account.busy' : `account.${mode}`)}</button>
   <div className="account-actions">{(['login','register','recover'] as const).filter(m => m !== mode).map(m => <button type="button" key={m} disabled={busy} onClick={() => {setMode(m);setError('');}}>{t(m === 'login' ? 'account.backToLogin' : m === 'recover' ? 'account.forgotPassword' : 'account.register')}</button>)}</div>
  </form>}
 </section>;
}
