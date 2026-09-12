import { useEffect, useRef, useState } from 'react';
import type { Credentials } from '@wuu/remote-core';
import { loadAccount, revalidateAccount } from './lib/accountStore';
import { ConversationHistory, emptyHistory, type HistoryCache } from './lib/conversationCache';
import PairedApp from './App';
import './conversationHistory.css';

export default function ConversationWorkspace({ credentials, back }: { credentials: Credentials; back: () => void }): React.JSX.Element {
  const [cache, setCache] = useState<HistoryCache>(emptyHistory);
  const [active, setActive] = useState<string>();
  const activeRef = useRef(active); activeRef.current = active;
  const service = useRef<ConversationHistory>(undefined);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [live, setLive] = useState(false);
  const [started, setStarted] = useState(false);
  useEffect(() => {
    let closed = false, syncing = false;
    const sync = async () => {
      if (closed || syncing || document.hidden || !service.current) return;
      syncing = true; setBusy(true);
      try { await service.current.sync(activeRef.current); if (!closed) setError(''); }
      catch (error) { if (!closed) setError(String(error)); }
      finally { syncing = false; if (!closed) setBusy(false); }
    };
    void loadAccount().then(async session => {
      if (closed) return;
      if (!session) { back(); return; }
      const history = new ConversationHistory(session, credentials.host_pub, state => { if (!closed) setCache(state); }, async () => {
        try { await revalidateAccount(session); }
        finally { if (!closed) back(); }
      });
      service.current = history;
      try { await history.load(); } catch (error) { if (!closed) setError('本地历史缓存不可用：' + String(error)); }
      await sync();
    }).catch(error => { if (!closed) setError(String(error)); });
    const wake = () => { void sync(); };
    const accountChanged = () => { service.current?.close(); setCache(emptyHistory()); back(); };
    document.addEventListener('visibilitychange', wake);
    window.addEventListener('online', wake);
    window.addEventListener('wuu:account-change', accountChanged);
    const timer = setInterval(wake, 10_000);
    return () => { closed = true; service.current?.close(); service.current = undefined; clearInterval(timer); document.removeEventListener('visibilitychange', wake); window.removeEventListener('online', wake); window.removeEventListener('wuu:account-change', accountChanged); };
  }, [credentials.host_pub]);
  const run = async (work: () => Promise<void>) => {
    setBusy(true);
    try { await work(); setError(''); } catch (error) { setError(String(error)); }
    finally { setBusy(false); }
  };
  const entries = Object.values(cache.entries).sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  const remembered = active || cache.selectedID;
  const id = remembered && cache.entries[remembered] ? remembered : entries[0]?.id;
  const body = id ? cache.bodies[id] : undefined;
  return <section className="conversation-workspace">
    <header className="conversation-toolbar">
      <button onClick={back}>设备</button>
      <button aria-pressed={!live} onClick={() => setLive(false)}>对话历史</button>
      <button aria-pressed={live} onClick={() => { setStarted(true); setLive(true); }}>连接电脑</button>
    </header>
    {started && <div className="conversation-live" hidden={!live}><PairedApp onAccountBack={back} /></div>}
    <main className="conversation-history" hidden={live}>
      <h1>{credentials.host_name || '电脑'} · 对话历史</h1>
      <p>历史副本可在电脑离线时读取。发送消息和执行任务请连接电脑。</p>
      <details><summary>同步设置{cache.settings ? (cache.settings.enabled ? ' · 已开启' : ' · 未开启') : ''}</summary>
        <p>开启后，此电脑的现有及后续对话文字、标题和运行状态会保存到账号服务器，服务器运营方可以读取。不会自动上传附件、工具输出或工作区文件。关闭会删除服务器副本和本机历史缓存。</p>
        <button disabled={busy || !service.current} onClick={() => void run(async () => { await service.current!.setEnabled(!cache.settings?.enabled); await service.current!.sync(active); })}>{cache.settings?.enabled ? '关闭并删除副本' : '开启对话同步'}</button>
      </details>
      <p role="status">{busy ? '正在同步…' : cache.checkedAt ? `本地副本 · 上次同步 ${new Date(cache.checkedAt).toLocaleString()}` : '等待同步'}</p>
      {error && <p role="alert">{error}。已缓存的内容仍可阅读。</p>}
      {cache.storageError && <p role="alert">本地缓存不可用，关闭页面后可能无法离线读取：{cache.storageError}</p>}
      <button disabled={busy} onClick={() => void run(() => service.current?.sync(active) || Promise.resolve())}>刷新</button>
      <nav aria-label="对话列表">{entries.map(entry => <button key={entry.id} aria-current={id === entry.id ? 'true' : undefined} onClick={() => { setActive(entry.id); void run(() => service.current!.read(entry.id)); }}>{entry.title || '未命名对话'}</button>)}</nav>
      {!entries.length && <p>{cache.settings?.enabled ? '还没有同步的对话。请保持电脑的手机访问功能开启，等待首次上传。' : '开启同步后，可在这里读取对话。'}</p>}
      {body ? <article aria-label="对话内容">
        <h2>{body.thread.title || '未命名对话'}</h2>
        <p>电脑上次报告的状态：{body.thread.status === 'in_progress' ? '运行中' : '空闲'} · {body.thread.updated_at && new Date(body.thread.updated_at).toLocaleString()}</p>
        {id && body.revision !== cache.entries[id]?.revision && <p>正在获取较新的内容，当前显示本地副本。</p>}
        {body.thread.messages.map(message => <section key={message.turn_id + ':' + message.id}><strong>{message.role === 'user' ? '你' : '助手'}</strong><div className="conversation-text">{message.text}</div></section>)}
      </article> : id && <p>此对话尚未缓存，请联网后刷新。</p>}
    </main>
  </section>;
}
