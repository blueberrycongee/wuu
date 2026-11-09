/** Independent session.sharing@1 provider; run this file directly with
 * `wuu plugin dev`. Select its service and remove wuu.session-sharing.default.
 * It owns its storage namespace and format/policy; there are no internal imports.
 * This example adds a 24-hour maximum expiry and plain-text redaction policy.
 * The text transform is not a complete secret detector; explicit review remains.
 */
import { createHash, randomBytes } from 'node:crypto';
import {
  documentStore, requireDocumentStore, SESSION_SHARING_DESCRIPTOR, RuntimeHostServiceError,
  type RuntimeHost, type RuntimePlugin, type SharePrincipal, type ShareSource,
  type ShareSelection, type SharePreviewRequest, type SharePreview, type ShareCreate,
  type ShareRead, type SharedDocument, type SharedItem, type SessionShare,
  type ShareAuthorization,
} from '@wuu/plugin-sdk';

const namespace = 'example.restricted-shares.v1';
interface Stored extends SessionShare {
  creator: SharePrincipal; recipients: readonly SharePrincipal[]; selection: ShareSelection;
  frozen?: ShareSource; revoked?: boolean; operation_id: string; intent_digest: string;
}
function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).sort(([a], [b]) => a.localeCompare(b)).map(([k, v]) => [k, canonical(v)]));
  return value;
}
const digest = (value: unknown) => createHash('sha256').update(JSON.stringify(canonical(value))).digest('hex');
const same = (a: SharePrincipal, b: SharePrincipal) => a.kind === b.kind && a.issuer === b.issuer && a.tenant === b.tenant && a.subject === b.subject;
const valid = (p: SharePrincipal) => p && ['user', 'device', 'agent'].includes(p.kind)
  && [p.issuer, p.tenant, p.subject].every(v => typeof v === 'string' && v.trim().length > 0);
const unavailable = () => new Error('share unavailable or access denied');

function project(source: ShareSource, selection: ShareSelection): { document: SharedDocument; selected: ShareSource } {
  const selected = source.items.filter(item => {
    const ids = item.kind === 'message' ? selection.message_ids : item.kind === 'tool_result' ? selection.tool_result_ids : selection.attachment_ids;
    return ids?.includes(item.id) && (!selection.after || Date.parse(item.at) >= Date.parse(selection.after))
      && (!selection.before || Date.parse(item.at) < Date.parse(selection.before));
  }).map(item => ({ ...item, ...(item.text ? { text: item.text.replace(/\bsk-[A-Za-z0-9_-]{16,}\b/g, '[redacted by example policy]') } : {}) }));
  if (selected.length > 2000 || new Set(selected.map(item => item.id)).size !== selected.length) throw new Error('invalid selected items');
  const items: SharedItem[] = selected.map(item => {
    if (item.kind === 'attachment') {
      const bytes = Buffer.from(item.data ?? '', 'base64');
      if (bytes.length > 8 * 1024 * 1024) throw new Error('attachment exceeds protocol limit');
      return { id: item.id, turn_id: item.turn_id, kind: item.kind, at: item.at,
        attachment: { id: item.id, sha256: createHash('sha256').update(bytes).digest('hex'), media_type: item.media_type ?? 'application/octet-stream', bytes: bytes.length } };
    }
    if (!['message', 'tool_result'].includes(item.kind) || (item.kind === 'message' && !['user', 'assistant'].includes(item.role ?? ''))) throw new Error('unsupported source kind');
    return { id: item.id, turn_id: item.turn_id, kind: item.kind, role: item.role, text: item.text, at: item.at };
  });
  const document: SharedDocument = { version: 1, session_id: source.session_id, items };
  if (Buffer.byteLength(JSON.stringify(document)) > 2 * 1024 * 1024) throw new Error('projection too large');
  return { document, selected: { session_id: source.session_id, items: selected } };
}
function preview(input: SharePreviewRequest): SharePreview {
  const { document, selected } = project(input.source, input.selection);
  return { document, review_digest: digest(selected), warning: 'Review all selected text and attachment contents. Pattern redaction is incomplete; downloaded copies cannot be revoked.' };
}
async function load(host: RuntimeHost, id: string) {
  const view = await documentStore(host).read<Stored>({ namespace, keys: [id] });
  const record = view.documents[0]?.value;
  if (!record || record.id !== id) throw unavailable();
  return { record, revision: view.revision };
}
function allowed(record: Stored, actor: SharePrincipal) {
  return valid(actor) && !record.revoked && Date.now() < Date.parse(record.expires_at)
    && (same(record.creator, actor) || record.recipients.some(recipient => same(recipient, actor)));
}
async function readable(host: RuntimeHost, input: ShareRead) {
  const loaded = await load(host, input.share_id);
  if (loaded.record.session_id !== input.session_id || !allowed(loaded.record, input.actor)) throw unavailable();
  return loaded;
}
function current(record: Stored, input: ShareRead) {
  const source = record.mode === 'snapshot' ? record.frozen : input.source;
  if (!source || source.session_id !== record.session_id) throw new Error('live source required');
  return project(source, record.selection);
}
export default {
  initialize: () => ({ protocol_version: 3, lifecycle_version: 2,
    provided_services: [SESSION_SHARING_DESCRIPTOR], required_services: [requireDocumentStore()] }),
  async invokeService({ method, params }, host, execution) {
    if (method === 'preview') return preview(params as SharePreviewRequest);
    if (method === 'create') {
      const input = params as ShareCreate;
      if (!valid(input.actor) || !input.operation_id?.trim() || input.operation_id.length > 256) throw new Error('stable actor-scoped operation_id required');
      const id = digest({ actor: input.actor, operation_id: input.operation_id });
      const { source, ...intent } = input;
      const intentDigest = digest({ ...intent, session_id: source.session_id });
      const store = documentStore(host);
      for (;;) {
        execution.signal.throwIfAborted();
        const view = await store.read<Stored>({ namespace, keys: [id] });
        const existing = view.documents[0]?.value;
        if (existing) {
          if (!same(existing.creator, input.actor) || existing.operation_id !== input.operation_id || existing.intent_digest !== intentDigest) throw new Error('operation ID already has different intent');
          return { id: existing.id, session_id: existing.session_id, mode: existing.mode, expires_at: existing.expires_at };
        }
        if (!input.recipients?.length || input.recipients.length > 100 || input.recipients.some(p => !valid(p))) throw new Error('named recipients required');
        if (!input.reviewed || !input.review_digest || preview(input).review_digest !== input.review_digest) throw new Error('review a current preview first');
        if (!['snapshot', 'live'].includes(input.mode) || (input.mode === 'live' && !input.live_updates)) throw new Error('explicit live-update consent required');
        const expires = Date.parse(input.expires_at);
        if (!Number.isFinite(expires) || expires <= Date.now() || expires > Date.now() + 24 * 60 * 60 * 1000) throw new Error('example policy: expiry must be within 24 hours');
        const { selected } = project(input.source, input.selection);
        if (!selected.items.length) throw new Error('select at least one item');
        const grant: SessionShare = { id, session_id: input.source.session_id, mode: input.mode, expires_at: input.expires_at };
        const record: Stored = { ...grant, operation_id: input.operation_id, intent_digest: intentDigest, creator: input.actor, recipients: input.recipients, selection: input.selection,
          ...(input.mode === 'snapshot' ? { frozen: selected } : {}) };
        try {
          await store.commit({ id: randomBytes(16).toString('hex'), expected_revision: view.revision, writes: [{ namespace, key: id, value: record }] });
          return grant;
        } catch (error) {
          // Only an explicitly rejected revision conflict is retried. A lost
          // acknowledgement propagates; retrying the same operation_id recovers.
          if (!(error instanceof RuntimeHostServiceError) || error.code !== 'state_conflict') throw error;
        }
      }
    }
    if (method === 'authorize') {
      const input = params as ShareAuthorization;
      if (!['session/share/read', 'session/share/attachment/read'].includes(input.action)
        || input.resources.length !== 1 || input.resources[0].kind !== 'session-share' || !input.resources[0].id) return { allow: false };
      try { const { record } = await load(host, input.resources[0].id); return { allow: allowed(record, input.principal) }; }
      catch { return { allow: false }; }
    }
    if (method === 'revoke') {
      const input = params as ShareRead;
      for (;;) {
        execution.signal.throwIfAborted();
        const { record, revision } = await load(host, input.share_id);
        if (!same(record.creator, input.actor) || record.session_id !== input.session_id) throw unavailable();
        if (record.revoked) return {};
        try {
          await documentStore(host).commit({ id: randomBytes(16).toString('hex'), expected_revision: revision,
            writes: [{ namespace, key: record.id, value: { ...record, revoked: true } }] });
          return {};
        } catch (error) {
          if (!(error instanceof RuntimeHostServiceError) || error.code !== 'state_conflict') throw error;
        }
      }
    }
    const input = params as ShareRead & { attachment_id?: string };
    const { record } = await readable(host, input);
    if (method === 'resolve') return { mode: record.mode, selection: record.selection };
    const { document, selected } = current(record, input);
    if (method === 'read') return document;
    if (method === 'attachment') {
      const index = selected.items.findIndex(item => item.kind === 'attachment' && item.id === input.attachment_id);
      if (index < 0) throw unavailable();
      return { ...document.items[index].attachment, data: selected.items[index].data };
    }
    throw new Error(`unsupported sharing operation ${method}`);
  },
} satisfies RuntimePlugin;
