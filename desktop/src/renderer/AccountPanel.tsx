import './AccountPanel.css';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { ArrowLeft, ChevronRight, Github, Monitor, Settings2 } from 'lucide-react';
import { useI18n } from './i18n';
import { defaultAccountServer } from './accountServer';
import { WuuMascot } from './WuuMascot';

export type AccountAction = 'status' | 'login' | 'register' | 'recover' | 'logout' | 'revoke' | 'password' | 'config' | 'github-start' | 'github-poll' | 'github-cancel';
export type AccountDeviceView = { pub: string; account: string; name: string; role: 'host' | 'phone'; online: boolean; added_at: number };
export type AccountView = { username?: string; server?: string; pub?: string; devices?: AccountDeviceView[]; recovery?: string; localLogoutOnly?: boolean; oauth_url?: string; github?: boolean; registration?: boolean; auth_method?: string; display_name?: string; unavailable?: boolean };
export type AccountDriver = (action: AccountAction, input?: Record<string, string>) => Promise<AccountView>;

/** Shared account forms; each host owns credential storage and transport. */
export function AccountPanel({ driver, onComputer, onPair, managementContent, onSignedIn, reserveAuthorization, presentation, active: panelActive = true }: { active?: boolean; presentation?: 'mobile'; driver: AccountDriver; onComputer?: (device: AccountDeviceView) => void; onPair?: () => void; managementContent?: ReactNode; onSignedIn?: () => void; reserveAuthorization?: () => { open: (url: string) => Promise<void>; close: () => void } }): React.JSX.Element {
 const { t } = useI18n();
 const epoch = useRef(0);
 const heading = useRef<HTMLHeadingElement>(null);
 const [account, setAccount] = useState<AccountView>({});
 const [page, setPage] = useState<'computers' | 'manage'>('computers');
 const initialServer = () => localStorage.getItem('wuu.account.server') || defaultAccountServer;
 const [authPage, setAuthPage] = useState<'form' | 'connection' | 'options'>(() => initialServer() ? 'form' : 'connection');
 const [loading, setLoading] = useState(true);
 const [mode, setMode] = useState<'login' | 'register' | 'recover' | 'password'>('login');
 const [server, setServer] = useState(initialServer); const [username, setUsername] = useState('');
 const [password, setPassword] = useState(''); const [secret, setSecret] = useState('');
 const [deviceName, setDeviceName] = useState('');
 const [connectionDraft, setConnectionDraft] = useState(() => ({ server: initialServer(), name: '' }));
 const [config, setConfig] = useState<AccountView | null>(null);
 const [configError, setConfigError] = useState('');
 const [configRevision, setConfigRevision] = useState(0);
 const [legacy, setLegacy] = useState(false);
 const [oauthURL, setOAuthURL] = useState('');
 const oauthEpoch = useRef(0);
 const [localLogoutOnly, setLocalLogoutOnly] = useState(false);
 const [error, setError] = useState(''); const [busy, setBusy] = useState(false); const [recovery, setRecovery] = useState('');
 useEffect(() => {
  if (!panelActive) return;
  let active = true; let running = false;
  const refresh = async () => { if (running || document.visibilityState === 'hidden') return; running = true; const generation = epoch.current;
   try { const next = await driver('status'); if (active && generation === epoch.current) { setAccount(next); if (next.oauth_url) setOAuthURL(next.oauth_url); } } catch (e) { if (active && generation === epoch.current) setError(String(e instanceof Error ? e.message : e)); } finally { running = false; if (active) setLoading(false); }
  };
  void refresh(); const timer = setInterval(() => void refresh(), 5000);
  document.addEventListener('visibilitychange', refresh); window.addEventListener('online', refresh);
  return () => { active = false; clearInterval(timer); document.removeEventListener('visibilitychange', refresh); window.removeEventListener('online', refresh); };
 }, [driver, panelActive]);
 useEffect(() => {
  if (!panelActive || !server) return;
  let active = true; setConfig(null); setConfigError('');
  void driver('config', { server }).then(next => { if (active) setConfig(next); }).catch(e => { if (active) setConfigError(String(e instanceof Error ? e.message : e)); });
  return () => { active = false; };
 }, [driver, server, configRevision, panelActive]);
 useEffect(() => {
  if (!panelActive || !oauthURL) return;
  let active = true; let running = false; const generation = oauthEpoch.current;
  const poll = async () => {
   if (running || document.visibilityState === 'hidden') return;
   running = true;
   try {
    const result = await driver('github-poll');
    if (!active || generation !== oauthEpoch.current) return;
    if (result.username) {
     epoch.current++; setOAuthURL(''); setAccount(result); setError(''); setPage('computers');
     window.dispatchEvent(new Event('wuu:account-changed')); onSignedIn?.();
     const next = await driver('status'); if (active) setAccount(next);
    }
   } catch(e) { if (active) setError(e instanceof Error ? e.message : String(e)); }
   finally { running = false; }
  };
  void poll(); const timer = setInterval(() => void poll(), 2000);
  window.addEventListener('online', poll); document.addEventListener('visibilitychange', poll);
  return () => { active = false; clearInterval(timer); window.removeEventListener('online', poll); document.removeEventListener('visibilitychange', poll); };
 }, [driver, oauthURL, panelActive]);
 const cancelGithub = async () => { epoch.current++; oauthEpoch.current++; setOAuthURL(''); setError(''); await driver('github-cancel'); };
 const startGithub = async () => {
  let browser: ReturnType<NonNullable<typeof reserveAuthorization>> | undefined; setBusy(true); setError(''); epoch.current++; const generation = ++oauthEpoch.current;
  try {
   browser = reserveAuthorization?.();
   const result = await driver('github-start', { server, name: deviceName });
   if (generation !== oauthEpoch.current) { browser?.close(); return; }
   if (!result.oauth_url) throw new Error('Missing authorization URL');
   setOAuthURL(result.oauth_url);
   if (browser) await browser.open(result.oauth_url); else await window.wuu?.openExternal(result.oauth_url);
  } catch(e) { browser?.close(); setError(e instanceof Error ? e.message : String(e)); }
  finally { setBusy(false); }
 };
 useEffect(() => { if (panelActive) heading.current?.focus(); }, [page, mode, authPage, panelActive]);
 const back = () => {
  if (oauthURL) { void cancelGithub().catch(e => setError(String(e))); return; }
  if (!account.username && authPage !== 'form') setAuthPage('form');
  else if (mode !== 'login') { setMode('login'); setAuthPage(account.username ? 'form' : 'options'); setPassword(''); setSecret(''); }
  else setPage('computers');
  setError('');
 };
 useEffect(() => {
  const nested = !!oauthURL || (account.username ? page !== 'computers' || mode === 'password' : (!!server && authPage !== 'form') || mode !== 'login');
  if (!panelActive || !onComputer || !nested) return;
  const handler = (event: Event) => { event.preventDefault(); if (!busy) back(); };
  window.addEventListener('wuu:native-back', handler);
  return () => window.removeEventListener('wuu:native-back', handler);
 }, [onComputer, account.username, page, mode, authPage, busy, oauthURL, panelActive]);
 const perform = async (action: AccountAction, input?: Record<string,string>) => {
  if (busy) return; epoch.current++; setBusy(true); setError(''); setLocalLogoutOnly(false);
  try {
   const result = await driver(action,input); if(result.recovery) setRecovery(result.recovery); else if(action === 'logout' || action === 'login') setRecovery('');
   setLocalLogoutOnly(result.localLogoutOnly === true);
   if (action === 'login' || action === 'register' || action === 'logout') setPage('computers');
   setPassword(''); setSecret(''); setMode('login'); setAuthPage('form'); const next = await driver('status'); setAccount(next); window.dispatchEvent(new Event('wuu:account-changed'));
   if (action === 'login' && next.username) onSignedIn?.();
  } catch(e) { setError(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
 };
 const devices = account.devices ?? [];
 const computers = devices.filter(d => d.role === 'host').sort((a, b) => Number(b.online) - Number(a.online) || a.added_at - b.added_at);
 const choosing = !!account.username && !!onComputer && page === 'computers' && mode !== 'password';
 const managing = !!account.username && !choosing && mode !== 'password';
 const welcome = presentation === 'mobile' && !account.username && authPage === 'form' && mode === 'login';
 const pairAction = onPair && <button className="account-text-action" type="button" onClick={onPair}>{t('account.pairLink')}<ChevronRight size={18} aria-hidden="true" /></button>;
 const openConnection = () => { setConnectionDraft({ server, name: deviceName }); setAuthPage('connection'); };
 if (loading) return <section className="account-panel" aria-busy="true"><p role="status">{t('account.restoring')}</p></section>;
 return <section className={`account-panel${presentation === 'mobile' ? ' account-mobile' : ''}${!account.username || mode === 'password' ? ' account-auth' : ''}${welcome ? ' account-welcome' : ''}`} aria-label={t(choosing ? 'account.computers' : 'account.label')}>
  <header className="account-page-header">
   {account.username && !choosing && (onComputer || mode === 'password') && <button className="account-back" type="button" aria-label={t(mode === 'password' ? 'account.manage' : 'account.computers')} disabled={busy} onClick={back}><ArrowLeft size={20} aria-hidden="true" /></button>}
   {!account.username && (authPage !== 'form' || mode !== 'login') && !!server && <button className="account-back" type="button" aria-label={t('common.back')} disabled={busy} onClick={back}><ArrowLeft size={20} aria-hidden="true" /></button>}
   <h2 ref={heading} tabIndex={-1}>{t(choosing ? 'account.computers' : mode === 'password' ? 'account.password' : managing ? 'account.manage' : authPage === 'connection' ? 'account.connectionSettings' : authPage === 'options' ? 'account.moreOptions' : mode === 'register' ? 'account.register' : mode === 'recover' ? 'account.forgotPassword' : 'account.connect')}</h2>
   {welcome && <WuuMascot className="account-title-mascot" accessory="none" aria-hidden="true" />}
   {choosing && <button className="account-manage-link" type="button" aria-label={t('account.manage')} onClick={() => setPage('manage')}><Settings2 size={20} aria-hidden="true" /></button>}
  </header>
  <div className="account-page-body">
  {error && <p role="alert" className="settings-error">{error}</p>}
  {account.unavailable && <p role="status">{t('account.directoryUnavailable')}</p>}
  {localLogoutOnly && <p role="status">{t('account.localLogout')}</p>}
  {recovery && <div role="status"><p>{t(presentation === 'mobile' ? 'account.recoveryCode' : 'account.saveRecovery')}</p><code style={{overflowWrap:'anywhere',userSelect:'all'}}>{recovery}</code><p><button type="button" onClick={() => { setRecovery(''); if (account.username) onSignedIn?.(); }}>{t('account.savedRecovery')}</button></p></div>}
  {oauthURL ? <div className="account-oauth-pending"><p role="status">{t('account.githubWaiting')}</p><a href={oauthURL} target="_blank" rel="noreferrer" onClick={e => { if (reserveAuthorization) { e.preventDefault(); try { void reserveAuthorization().open(oauthURL).catch(e => setError(String(e))); } catch (e) { setError(String(e)); } } }}>{t('account.githubOpen')}</a><button type="button" onClick={() => void cancelGithub().catch(e => setError(String(e)))}>{t('common.cancel')}</button></div> : choosing ? <div className="account-computers">
   {computers.length === 0 ? <div className="account-empty"><Monitor size={32} aria-hidden="true" /><p>{t('account.noComputers')}</p>{pairAction}</div> : ([true, false] as const).map(online => {
    const group = computers.filter(d => d.online === online);
    if (!group.length) return null;
    return <section className="account-computer-group" key={String(online)} aria-label={t(online ? 'account.available' : 'account.offline')}>
     <h3>{t(online ? 'account.available' : 'account.offline')}<span>{group.length}</span></h3>
     <div className="account-computer-list">{group.map(d => <button className="account-computer-card" type="button" key={d.pub} disabled={!online || busy} aria-label={t('account.connectTo', { name: d.name || t('account.computer') })} onClick={() => onComputer?.(d)}>
      <span className="account-computer-icon"><Monitor size={24} aria-hidden="true" /></span>
      <span className="account-computer-name">{d.name || t('account.computer')}</span>
      {online && <ChevronRight size={20} aria-hidden="true" />}
     </button>)}</div>
    </section>;
   })}
  </div> : managing ? <>
   <div className="account-profile"><strong>{account.display_name || account.username}</strong><p className="account-server">{account.server}</p></div>
   {(['host', 'phone'] as const).map(role => <section className="account-device-group" key={role} aria-label={t(role === 'host' ? 'account.computers' : 'account.phones')}>
    <h3>{t(role === 'host' ? 'account.computers' : 'account.phones')}</h3>
    <div className="account-devices">{(account.devices ?? []).filter(d => d.role === role).sort((a,b) => Number(b.pub === account.pub) - Number(a.pub === account.pub) || Number(b.online) - Number(a.online) || a.added_at - b.added_at).map(d => <div className="account-device" key={d.pub}>
     <div className="account-device-info"><strong>{d.name || t(role === 'host' ? 'account.computer' : 'account.phone')}</strong><p><span className="account-device-status" data-online={d.online}>{t(d.online ? 'account.online' : 'account.offline')}</span>{d.pub === account.pub && <span className="account-current">{t('account.current')}</span>}</p></div>
     <div className="account-device-actions">
      {d.pub !== account.pub && <button className="account-remove" type="button" disabled={busy} onClick={() => void perform('revoke',{pub:d.pub})}>{t('account.remove')}</button>}
     </div>
    </div>)}</div>
    {role === 'host' && !(account.devices ?? []).some(d => d.role === 'host') && <p>{t('account.noComputers')}</p>}
   </section>)}
   <div className="account-management-actions">{pairAction}{managementContent}{account.auth_method !== 'github' && <button type="button" disabled={busy} onClick={() => setMode('password')}>{t('account.password')}<ChevronRight size={18} aria-hidden="true" /></button>}</div>
   <button className="account-signout" type="button" disabled={busy} onClick={() => void perform('logout')}>{t('account.logout')}</button>
  </> : !account.username && authPage === 'connection' ? <form onSubmit={e => { e.preventDefault(); setServer(connectionDraft.server.trim()); localStorage.setItem('wuu.account.server', connectionDraft.server.trim()); setLegacy(false); setDeviceName(connectionDraft.name); setAuthPage('form'); }}>
   <label>{t('account.server')}<input type="url" autoCapitalize="none" autoCorrect="off" placeholder="https://wuu.example.com" value={connectionDraft.server} required onChange={e => setConnectionDraft(current => ({ ...current, server: e.target.value }))}/></label>
   {onComputer && <label>{t('account.deviceName')}<input value={connectionDraft.name} maxLength={64} placeholder={t(presentation === 'mobile' ? 'account.phone' : 'account.deviceNameExample')} onChange={e => setConnectionDraft(current => ({ ...current, name: e.target.value }))}/></label>}
   <button className="account-primary" type="submit">{t('common.save')}</button><div className="account-secondary-actions">{defaultAccountServer && <button type="button" onClick={() => { setServer(defaultAccountServer); localStorage.setItem('wuu.account.server', defaultAccountServer); setAuthPage('form'); setLegacy(false); }}>{t('account.officialServer')}</button>}{pairAction}</div>
  </form> : !account.username && authPage === 'options' ? <div className="account-auth-options">
   {(['register', 'recover'] as const).filter(next => next !== 'register' || config?.registration !== false).map(next => <button type="button" key={next} onClick={() => { setMode(next); setAuthPage('form'); setPassword(''); setSecret(''); setError(''); }}>{t(next === 'recover' ? 'account.forgotPassword' : 'account.register')}<ChevronRight size={18} aria-hidden="true" /></button>)}
   {config?.github && <button type="button" onClick={() => { setLegacy(true); setAuthPage('form'); }}>{t('account.legacyLogin')}</button>}{pairAction}
  </div> : !account.username && mode === 'login' && !legacy && (config?.github || !config || !!configError) ? <div className="account-login-methods">
   <p className="account-server">{server}</p>
   {configError ? <><p role="alert">{configError}</p><button onClick={() => setConfigRevision(v => v + 1)}>{t('account.retryServer')}</button></> : !config ? <p role="status">{t('account.checkingServer')}</p> : <button className="account-primary" disabled={busy} onClick={() => void startGithub()}><Github size={20} aria-hidden="true" />{t('account.githubContinue')}</button>}
   <button type="button" onClick={openConnection}>{t('account.changeServer')}</button>
   <button type="button" onClick={() => setAuthPage('options')}>{t('account.moreOptions')}</button>{pairAction}
  </div> : <><form onSubmit={e => {e.preventDefault(); if (!account.username && !server.trim()) { openConnection(); return; } void perform(mode,{server,username:account.username || username,password,secret,name:deviceName});}}>
   {!account.username && <>
    <label>{t('account.username')}<input autoComplete="username" autoCapitalize="none" autoCorrect="off" value={username} required minLength={3} onChange={e => setUsername(e.target.value)}/></label>
   </>}
   {(mode === 'recover' || mode === 'password') && <label>{t(mode === 'recover' ? 'account.recoveryCode' : 'account.currentPassword')}<input type="password" autoComplete={mode === 'password' ? 'current-password' : 'off'} value={secret} required onChange={e => setSecret(e.target.value)}/></label>}
   <label>{t(mode === 'login' ? 'account.loginPassword' : 'account.newPassword')}<input type="password" autoComplete={mode === 'login' ? 'current-password' : 'new-password'} value={password} required minLength={12} onChange={e => setPassword(e.target.value)}/></label>
   {mode === 'password' && presentation !== 'mobile' && <p>{t('account.passwordHint')}</p>}

   <button className="account-primary" disabled={busy} type="submit">{t(busy ? 'account.busy' : presentation === 'mobile' && mode === 'password' ? 'account.passwordAndSignOut' : `account.${mode}`)}</button>
  </form>{!account.username && <div className="account-auth-footer"><button className="account-connection-link" type="button" disabled={busy} onClick={openConnection}><span>{t('account.connectionSettings')}</span><span className="account-connection-value">{server || t('account.notConfigured')}</span><ChevronRight size={16} aria-hidden="true" /></button>{mode === 'login' && <button className="account-auth-more" type="button" disabled={busy} onClick={() => setAuthPage('options')}>{t('account.moreOptions')}<ChevronRight size={16} aria-hidden="true" /></button>}</div>}</>}
 </div>
 </section>;
}
