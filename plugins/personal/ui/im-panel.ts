import type * as React from 'react';
import type { createPersonalReviewPanel } from './action-review-panel.js';

export type IMUI = Parameters<typeof createPersonalReviewPanel>[1] & { TextInput: React.ComponentType<Omit<React.InputHTMLAttributes<HTMLInputElement>, "children" | "value" | "defaultValue"> & { label: React.ReactNode; value?: string | number; defaultValue?: string | number; description?: React.ReactNode }> };

type Platform = 'slack' | 'feishu';
type Binding = { tenant_id: string; sender_id: string; conversation_id: string };
type Target = { platform: Platform };
export type IMService = {
  status(): Promise<unknown>;
  pending(): Promise<unknown>;
  resolve(input: Target & { kind: 'input' | 'output'; id: string; decision: 'discard' }): Promise<unknown>;
  configure(input: Target & { binding: Binding }): Promise<unknown>;
  test(input: Target): Promise<unknown>;
  enable(input: Target): Promise<unknown>;
  disable(input: Target): Promise<unknown>;
  retry(input: Target): Promise<unknown>;
};
type Connection = Target & { enabled: boolean; state: string; configured: boolean; credential_ready: boolean; binding: Binding; app_id?: string; last_error?: string; pending_outbound: number; unknown_delivery: number };
const states = ['unconfigured', 'disabled', 'connecting', 'connected', 'error', 'reconnecting'];
function object(value: unknown): Record<string, unknown> { if (!value || typeof value !== 'object' || Array.isArray(value)) throw Error('invalid_im_status'); return value as Record<string, unknown>; }
function connections(raw: unknown): Connection[] {
  const value = object(raw);
  if (!Array.isArray(value.connections) || value.connections.length > 2) throw Error('invalid_im_status');
  const seen = new Set<string>();
  return value.connections.map(rawRow => {
    const row = object(rawRow), binding = object(row.binding);
    if (row.platform !== 'slack' && row.platform !== 'feishu' || seen.has(row.platform)) throw Error('invalid_im_platform');
    seen.add(row.platform);
    if (typeof row.state !== 'string' || !states.includes(row.state)) throw Error('invalid_im_state');
    for (const key of ['enabled', 'configured', 'credential_ready']) if (typeof row[key] !== 'boolean') throw Error('invalid_im_status');
    for (const key of ['tenant_id', 'sender_id', 'conversation_id']) if (typeof binding[key] !== 'string') throw Error('invalid_im_binding');
    for (const key of ['pending_outbound', 'unknown_delivery']) if (!Number.isSafeInteger(row[key]) || (row[key] as number) < 0) throw Error('invalid_im_delivery');
    for (const key of ['app_id', 'last_error']) if (row[key] !== undefined && typeof row[key] !== 'string') throw Error('invalid_im_status');
    if (row.state === 'connected' && (!row.enabled || !row.configured || !row.credential_ready)) throw Error('invalid_im_connection');
    return structuredClone(row) as Connection;
  });
}
type Pending = Target & { kind: 'input' | 'output'; id: string; state: string; error?: string; preview: string };
function pendingRows(raw: unknown): Pending[] {
  const value = object(raw), result: Pending[] = [], seen = new Set<string>();
  for (const [key, kind] of [['inputs', 'input'], ['outputs', 'output']] as const) {
    const items = value[key];
    if (!Array.isArray(items)) throw Error('invalid_im_pending');
    for (const item of items) {
      const row = object(item);
      if ((row.platform !== 'slack' && row.platform !== 'feishu') || typeof row.id !== 'string' || !row.id || typeof row.state !== 'string' || typeof row.preview !== 'string' || row.error !== undefined && typeof row.error !== 'string') throw Error('invalid_im_pending');
      const identity = `${row.platform}:${kind}:${row.id}`;
      if (seen.has(identity)) throw Error('duplicate_im_pending');
      seen.add(identity); result.push({ ...row, kind } as Pending);
    }
  }
  return result;
}
const copy = {
  en: {
    pending: 'Messages needing attention', input: 'Incoming request', output: 'Outgoing reply', preview: 'Message preview', discard: 'Abandon this message', confirmDiscard: 'Confirm abandon', cancel: 'Cancel', discardWarning: 'This stops local attempts only. The message may already have reached Personal or the chat. It does not undo work or resend anything.', title: 'Chat with Personal', intro: 'Receive messages from your enrolled identity and reply in the same chat. This is separate from read-only work connectors.', refresh: 'Refresh status', loading: 'Checking…', unavailable: 'Connection status could not be verified. Refresh before making changes.', uncertain: 'The operation receipt was not received. Refresh saved state before trying again; do not assume the change failed.',
    credentials: 'Host credentials', ready: 'Available', missing: 'Not configured', setup: 'Configure these secrets in the host environment, then restart. The host saves them in its selected credential store; removing an environment value does not revoke it. Never paste tokens into plugin settings or messages.', binding: 'Owner identity binding', bindingHint: 'Only messages matching all three IDs are admitted. Disable before changing a binding. Pending or unknown deliveries must be resolved first.', team: 'Slack workspace ID', tenant: 'Feishu tenant key', user: 'Slack owner user ID', sender: 'Feishu owner open ID', channel: 'Slack conversation/channel ID', chat: 'Feishu chat ID', save: 'Save binding', test: 'Test connection', testHint: 'Checks credentials and binding without sending a message.', tested: 'Connection test finished. Check the current host status below; no message was sent.', enable: 'Enable', disable: 'Disable', retry: 'Reconnect', retryHint: 'Reconnect restarts transport only. It does not replay messages with unknown delivery.', queued: 'Pending outbound', unknown: 'Unknown delivery', unknownHint: 'A provider may already have accepted these messages. Check the original chat; they will not be resent automatically.', failure: 'Last failure', unsaved: 'Unsaved binding — save before testing or enabling.', app: 'App ID',
    unverified: 'Status unverified', unconfigured: 'Not configured', disabled: 'Disabled', connecting: 'Connecting', connected: 'Connected', error: 'Connection failed', reconnecting: 'Reconnecting',
  },
  zh: {
    pending: '需要处理的消息', input: '收到的请求', output: '待投递的回复', preview: '消息预览', discard: '放弃这条消息', confirmDiscard: '确认放弃', cancel: '取消', discardWarning: '只停止本地后续尝试。消息可能已经进入 Personal 或原会话，不会撤销工作，也不会重发。', title: '通过聊天联系 Personal', intro: '接收已绑定身份的消息，并在原会话回复。此入口与只读工作资料连接器独立。', refresh: '刷新状态', loading: '正在核对…', unavailable: '暂时无法核实连接状态。请刷新后再修改。', uncertain: '未收到操作回执。请先刷新已保存状态，不要假定修改失败或重复操作。',
    credentials: '宿主凭据', ready: '已配置', missing: '未配置', setup: '在宿主环境中配置这些秘密值并重启。宿主会将它们保存到所选凭据库；移除环境值不等于撤销凭据。不要把令牌粘贴到插件设置或消息中。', binding: '主人身份绑定', bindingHint: '只有同时匹配这三个 ID 的消息才会进入。更改绑定前请先停用，并处理尚未完成或投递结果未知的消息。', team: 'Slack 工作区 ID', tenant: '飞书租户 tenant key', user: 'Slack 主人用户 ID', sender: '飞书主人 open ID', channel: 'Slack 会话/频道 ID', chat: '飞书会话 chat ID', save: '保存绑定', test: '测试连接', testHint: '核实凭据和绑定，不发送消息。', tested: '连接测试已完成，请核对下方宿主状态；未发送消息。', enable: '启用', disable: '停用', retry: '重新连接', retryHint: '重新连接只重启传输，不会重发投递结果未知的消息。', queued: '待发送', unknown: '投递结果未知', unknownHint: '平台可能已经接收这些消息。请到原会话核对；系统不会自动重发。', failure: '最近失败', unsaved: '绑定尚未保存；请先保存再测试或启用。', app: '应用 ID',
    unverified: '状态未核实', unconfigured: '未配置', disabled: '已停用', connecting: '连接中', connected: '已连接', error: '连接失败', reconnecting: '重新连接中',
  },
};

export function createIMPanel(react: typeof React, ui: IMUI) {
  const h = react.createElement, Button = ui.Button;
  function PlatformPanel({ row, service, locale, locked, verified, pending, run }: { row: Connection; service: IMService; locale: keyof typeof copy; locked: boolean; verified: boolean; pending: Pending[]; run: (work: () => Promise<unknown>, done?: () => void) => Promise<void> }) {
    const t = copy[locale];
    const [draft, setDraft] = react.useState(row.binding), [dirty, setDirty] = react.useState(false), [tested, setTested] = react.useState(false), [confirming, setConfirming] = react.useState('');
    react.useEffect(() => { if (!dirty) setDraft(row.binding); }, [row.binding.tenant_id, row.binding.sender_id, row.binding.conversation_id, dirty]);
    const bindingLocked = row.enabled || row.pending_outbound > 0 || row.unknown_delivery > 0 || pending.some(item => item.kind === 'input' || ['pending', 'sending', 'uncertain'].includes(item.state));
    const complete = Object.values(draft).every(value => value.trim().length > 0);
    const target = { platform: row.platform };
    const action = (name: string, title: string, disabled: boolean, work: () => Promise<unknown>, done?: () => void) => h(Button, { 'data-im-action': name, disabled: locked || disabled, onClick: () => void run(work, done) } as React.ComponentProps<typeof Button>, title);
    const field = (name: keyof Binding, label: string) => h(ui.TextInput, { key: name, label, name, value: draft[name], autoComplete: 'off', spellCheck: false, disabled: locked || bindingLocked, onChange: (event: React.ChangeEvent<HTMLInputElement>) => { setDraft(current => ({ ...current, [name]: event.target.value })); setDirty(true); setTested(false); } });
    return h('article', { className: 'personal-im-platform', 'data-im-platform': row.platform },
      h('div', { className: 'personal-im-heading' }, h('h4', null, row.platform === 'slack' ? 'Slack' : '飞书 / Feishu'), h('span', { 'data-im-state': verified ? row.state : 'unverified' }, verified ? t[row.state as keyof typeof t] : t.unverified)),
      h('p', null, `${t.credentials}: ${row.credential_ready ? t.ready : t.missing}`),
      row.app_id ? h('p', null, `${t.app}: ${row.app_id}`) : null,
      h('details', { open: !row.credential_ready }, h('summary', null, t.credentials), h('p', null, t.setup), h('code', null, row.platform === 'slack' ? 'WUU_SLACK_APP_TOKEN · WUU_SLACK_BOT_TOKEN' : 'WUU_FEISHU_APP_ID · WUU_FEISHU_APP_SECRET')),
      h('fieldset', { disabled: locked || bindingLocked }, h('legend', null, t.binding), h('p', null, t.bindingHint), h('div', { className: 'personal-im-fields' }, field('tenant_id', row.platform === 'slack' ? t.team : t.tenant), field('sender_id', row.platform === 'slack' ? t.user : t.sender), field('conversation_id', row.platform === 'slack' ? t.channel : t.chat))),
      dirty ? h('p', { role: 'status' }, t.unsaved) : null,
      h('div', { className: 'personal-im-controls' },
        action('configure', t.save, bindingLocked || !complete || !dirty, () => service.configure({ ...target, binding: { tenant_id: draft.tenant_id.trim(), sender_id: draft.sender_id.trim(), conversation_id: draft.conversation_id.trim() } }), () => setDirty(false)),
        action('test', t.test, dirty || !row.configured || !row.credential_ready, () => service.test(target), () => setTested(true)),
        row.enabled ? action('disable', t.disable, false, () => service.disable(target)) : action('enable', t.enable, dirty || !row.configured || !row.credential_ready, () => service.enable(target)),
        row.enabled ? action('retry', t.retry, false, () => service.retry(target)) : null),
      h('p', null, t.testHint), tested ? h('p', { role: 'status' }, t.tested) : null,
      row.enabled ? h('p', null, t.retryHint) : null,
      h('dl', { className: 'personal-im-delivery' }, h('div', null, h('dt', null, t.queued), h('dd', null, row.pending_outbound)), h('div', { 'data-im-unknown': true }, h('dt', null, t.unknown), h('dd', null, row.unknown_delivery))),
      row.unknown_delivery > 0 ? h('p', { role: 'status' }, t.unknownHint) : null,
      row.last_error ? h('p', { role: 'status' }, `${t.failure}: ${row.last_error}`) : null,
      pending.length ? h('section', { className: 'personal-im-pending' }, h('h4', null, t.pending), ...pending.map(item => {
        const identity = `${item.kind}-${item.id}`, canDiscard = item.kind === 'input' ? item.state === 'pending' : ['pending', 'failed', 'uncertain'].includes(item.state);
        return h('article', { key: identity }, h('p', null, `${item.kind === 'input' ? t.input : t.output} · ${item.state}`), h('code', null, item.id),
          item.error ? h('p', null, item.error) : null,
          h('details', null, h('summary', null, t.preview), h('pre', null, item.preview)),
          confirming === identity && canDiscard ? h('div', null, h('p', { role: 'status' }, t.discardWarning), h('div', { className: 'personal-im-controls' },
            action(`confirm-discard-${identity}`, t.confirmDiscard, false, () => service.resolve({ ...target, kind: item.kind, id: item.id, decision: 'discard' }), () => setConfirming('')),
            h(Button, { 'data-im-action': `cancel-discard-${identity}`, disabled: locked, onClick: () => setConfirming('') } as React.ComponentProps<typeof Button>, t.cancel))) :
            h(Button, { 'data-im-action': `discard-${identity}`, disabled: locked || !canDiscard, onClick: () => setConfirming(identity) } as React.ComponentProps<typeof Button>, t.discard));
      })) : null);
  }
  return function IMPanel({ service, locale }: { service: IMService; locale?: string }) {
    const language = locale?.startsWith('zh') ? 'zh' : 'en', t = copy[language];
    const [rows, setRows] = react.useState<Connection[]>([]), [pending, setPending] = react.useState<Pending[]>([]), [loading, setLoading] = react.useState(true), [busy, setBusy] = react.useState(false), [error, setError] = react.useState(''), [uncertain, setUncertain] = react.useState(false);
    const alive = react.useRef(false), gate = react.useRef(false), epoch = react.useRef(0), lifetime = react.useRef(0), reading = react.useRef(false), uncertainty = react.useRef(false);
    const load = async (manual = false) => {
      if (reading.current) return;
      reading.current = true;
      const current = ++epoch.current, owner = lifetime.current;
      setLoading(true);
      try {
        const [status, queued] = await Promise.all([service.status(), service.pending()]);
        const next = connections(status), attention = pendingRows(queued);
        if (!alive.current || current !== epoch.current || owner !== lifetime.current) return;
        setRows(next); setPending(attention); setError('');
        if (manual) { uncertainty.current = false; setUncertain(false); }
      } catch { if (alive.current && current === epoch.current && owner === lifetime.current) setError(t.unavailable); }
      finally { if (alive.current && current === epoch.current && owner === lifetime.current) { reading.current = false; setLoading(false); } }
    };
    react.useEffect(() => {
      alive.current = true; reading.current = false; gate.current = false; uncertainty.current = false; setRows([]); setPending([]); setBusy(false); setUncertain(false); void load(true);
      const timer = setInterval(() => { if (!gate.current && !uncertainty.current) void load(); }, 5000);
      return () => { alive.current = false; lifetime.current++; epoch.current++; clearInterval(timer); };
    }, [service]);
    const run = async (work: () => Promise<unknown>, done?: () => void) => {
      if (gate.current || uncertainty.current || reading.current) return;
      gate.current = true; setBusy(true); setError('');
      const owner = lifetime.current;
      try { await work(); if (alive.current && owner === lifetime.current) { done?.(); await load(true); } }
      catch { if (alive.current && owner === lifetime.current) { uncertainty.current = true; setUncertain(true); setError(t.uncertain); } }
      finally { if (alive.current && owner === lifetime.current) { gate.current = false; setBusy(false); } }
    };
    return h('section', { className: 'personal-im', 'data-personal-im': true },
      h('div', { className: 'personal-im-heading' }, h('h3', null, t.title), h(Button, { 'data-im-refresh': true, disabled: busy || loading, onClick: () => void load(true) } as React.ComponentProps<typeof Button>, loading ? t.loading : t.refresh)),
      h('p', null, t.intro), error ? h('p', { role: 'alert' }, error) : null,
      ...rows.map(row => h(PlatformPanel, { key: row.platform, row, service, locale: language, locked: busy || loading || uncertain || Boolean(error), verified: !error && !uncertain, pending: pending.filter(item => item.platform === row.platform), run })));
  };
}
export const imStyles = `.personal-im{font:inherit;min-width:0}.personal-im h3{margin:0;font-size:max(var(--font-ui,15px),var(--font-title,16px));font-weight:var(--weight-semibold,600)}.personal-im h4{margin:0;font:inherit;font-weight:var(--weight-medium,500)}.personal-im p{margin:var(--space-2,8px) 0;color:var(--ink-tertiary);overflow-wrap:anywhere}.personal-im-heading,.personal-im-controls{display:flex;align-items:center;justify-content:space-between;gap:var(--space-3,12px);flex-wrap:wrap}.personal-im-controls{justify-content:flex-start}.personal-im-platform{border-top:1px solid var(--hairline-soft);padding-block:var(--space-4,16px);margin-top:var(--space-3,12px)}.personal-im fieldset{border:0;padding:0;margin:var(--space-4,16px) 0;min-width:0}.personal-im legend{font-weight:var(--weight-medium,500);padding:0}.personal-im-fields{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,15em),1fr));gap:var(--space-3,12px)}.personal-im .plugin-ui-field{min-width:0}.personal-im .plugin-ui-input{width:100%;min-width:0;box-sizing:border-box}.personal-im summary:focus-visible{outline:2px solid var(--accent);outline-offset:2px}.personal-im summary{cursor:pointer}.personal-im code{font:var(--font-code,13px)/1.5 var(--font-mono,monospace);overflow-wrap:anywhere}.personal-im-delivery{display:flex;flex-wrap:wrap;gap:var(--space-4,16px);margin-block:var(--space-3,12px)}.personal-im-delivery>div{display:flex;gap:var(--space-2,8px)}.personal-im dd{margin:0}.personal-im [role=alert]{color:var(--danger);border-left:2px solid var(--danger);padding-left:var(--space-3,12px)}.personal-im button{white-space:normal}.personal-im-pending>article{border-top:1px solid var(--hairline-soft);padding-block:var(--space-3,12px)}.personal-im-pending pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:20em;overflow:auto;font:var(--font-code,13px)/1.5 var(--font-mono,monospace);padding:var(--space-3,12px);background:var(--surface-2)}`;
