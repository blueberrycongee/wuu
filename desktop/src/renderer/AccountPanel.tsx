import './AccountPanel.css';
import { useEffect, useRef, useState } from 'react';

export type AccountAction = 'status' | 'login' | 'register' | 'recover' | 'logout' | 'revoke' | 'password';
export type AccountDeviceView = { pub: string; account: string; name: string; role: 'host' | 'phone'; online: boolean; added_at: number };
export type AccountView = { username?: string; server?: string; pub?: string; devices?: AccountDeviceView[]; recovery?: string };
export type AccountDriver = (action: AccountAction, input?: Record<string, string>) => Promise<AccountView>;

/** Shared account forms; each host owns credential storage and transport. */
export function AccountPanel({ driver, onComputer }: { driver: AccountDriver; onComputer?: (device: AccountDeviceView) => void }): React.JSX.Element {
 const epoch = useRef(0);
 const [account, setAccount] = useState<AccountView>({});
 const [mode, setMode] = useState<'login' | 'register' | 'recover' | 'password'>('login');
 const [server, setServer] = useState(''); const [username, setUsername] = useState('');
 const [password, setPassword] = useState(''); const [secret, setSecret] = useState('');
 const [deviceName, setDeviceName] = useState('');
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
  if (busy) return; epoch.current++; setBusy(true); setError('');
  try {
   const result = await driver(action,input); if(result.recovery) setRecovery(result.recovery); else if(action === 'logout' || action === 'login') setRecovery('');
   setPassword(''); setSecret(''); setMode('login'); setAccount(await driver('status'));
  } catch(e) { setError(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
 };
 return <section className="account-panel" aria-label="自部署账号">
  <h2>{account.username ? `${account.username} 的设备` : '连接你的 Wuu'}</h2>
  <p>电脑运行 Agent。手机连接在线电脑，使用同一套会话。</p>
  {error && <p role="alert" className="settings-error">{error}</p>}
  {recovery && <div role="status"><p>请保存恢复码。忘记密码时可用它恢复账号，届时所有设备都会退出。</p><code style={{overflowWrap:'anywhere',userSelect:'all'}}>{recovery}</code><p><button type="button" onClick={() => setRecovery('')}>已保存恢复码</button></p></div>}
  {account.username && mode !== 'password' ? <>
   <p>{account.server}</p>
   <div className="account-devices">{(account.devices ?? []).map(d => <div className="account-device" key={d.pub}>
    <div><strong>{d.name || (d.role === 'host' ? '电脑' : '手机')}</strong><p>{d.role === 'host' ? '电脑' : '手机'} · {d.online ? '在线' : '离线'}{d.pub === account.pub ? ' · 当前设备' : ''}</p></div>
    {onComputer && d.role === 'host' && <button type="button" disabled={!d.online || busy} onClick={() => onComputer(d)}>打开</button>}
    {d.pub !== account.pub && <button type="button" disabled={busy} onClick={() => void perform('revoke',{pub:d.pub})}>移除设备</button>}
   </div>)}</div>
   {!(account.devices ?? []).some(d => d.role === 'host') && <p>在电脑的 Wuu「设置 → 手机访问」登录这个账号，电脑会出现在这里。</p>}
   <p>离线电脑需要在电脑上启动 Wuu 并连接网络。</p>
   <div className="account-actions"><button type="button" disabled={busy} onClick={() => setMode('password')}>修改密码</button><button type="button" disabled={busy} onClick={() => void perform('logout')}>退出账号</button></div>
  </> : <form onSubmit={e => {e.preventDefault();void perform(mode,{server,username:account.username || username,password,secret,name:deviceName});}}>
   {!account.username && <>
    <label>自部署服务端<input type="url" autoCapitalize="none" autoCorrect="off" placeholder="https://wuu.example.com" value={server} required onChange={e => setServer(e.target.value)}/></label>
    <label>用户名<input autoComplete="username" autoCapitalize="none" autoCorrect="off" value={username} required minLength={3} onChange={e => setUsername(e.target.value)}/></label>
   </>}
   {onComputer && (mode === 'login' || mode === 'register') && <label>此设备名称<input value={deviceName} maxLength={64} placeholder="例如：我的 iPhone" onChange={e => setDeviceName(e.target.value)}/></label>}
   {(mode === 'recover' || mode === 'password') && <label>{mode === 'recover' ? '恢复码' : '当前密码'}<input type="password" autoComplete={mode === 'password' ? 'current-password' : 'off'} value={secret} required onChange={e => setSecret(e.target.value)}/></label>}
   <label>{mode === 'login' ? '密码' : '新密码（至少 12 个字符）'}<input type="password" autoComplete={mode === 'login' ? 'current-password' : 'new-password'} value={password} required minLength={12} onChange={e => setPassword(e.target.value)}/></label>
   {mode === 'register' && <p>账号保存在你指定的服务端；服务端管理者负责身份与设备信任。注册后请保存恢复码。</p>}
   {mode === 'password' && <p>修改密码将退出所有设备，需要在各设备重新登录。</p>}
   <button disabled={busy} type="submit">{busy ? '处理中…' : ({login:'登录',register:'创建账号',recover:'恢复账号',password:'修改密码'}[mode])}</button>
   <div className="account-actions">{(['login','register','recover'] as const).filter(m => m !== mode).map(m => <button type="button" key={m} disabled={busy} onClick={() => {setMode(m);setError('');}}>{{login:'返回登录',register:'创建账号',recover:'忘记密码'}[m]}</button>)}</div>
  </form>}
 </section>;
}
