import type * as React from "react";
import type { PluginUIButtonProps } from "../../../packages/plugin-sdk/src/index";

export type ReviewStatus = "proposed" | "ready" | "awaiting_confirmation" | "approved" | "blocked" | "denied" | "revoked" | "expired" | "stale" | "executing" | "succeeded" | "uncertain";
export type ReviewRecord = {
  revision: number;
  status: ReviewStatus;
  outcome: "allow" | "ask" | "deny";
  reason: string;
  action: {
    id: string; digest: string; operation: string;
    scope: { space_id: string; session_id: string; task_id: string; requirements_revision: number };
    effect: "internal_bookkeeping" | "read_only" | "data_write" | "external_write" | "sensitive_authority" | "unknown";
    summary: { title: string; account: string; target: string; consequence: string };
    arguments_json: string;
    expires_at: string;
  };
};
export type ReviewSnapshot = { schema: "personal-review.v1" } & ({ kind: "unavailable"; reason: string } | { kind: "ready"; requests: ReviewRecord[] });
export type ReviewResolution = { space_id: string; id: string; digest: string; expected_revision: number; choice: "approve_once" | "reject" };
// Implement only against an authenticated host route. The host must bind
// identity outside payloads and return persisted, versioned owner snapshots.
// A GenUI callback or invokeRuntime payload is not an authenticated owner route.
export type ReviewService = {
  list(): Promise<unknown>;
  resolve(command: ReviewResolution): Promise<unknown>;
  revoke(command: Omit<ReviewResolution, "choice">): Promise<unknown>;
};

class ReviewContractError extends Error {}
function exactObject(value: unknown, keys: string[]): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value) && Object.keys(value).length === keys.length && keys.every(key => Object.hasOwn(value, key));
}
function nonempty(value: unknown): value is string { return typeof value === "string" && value.length > 0; }
function exactRevision(value: unknown): value is number { return typeof value === "number" && Number.isSafeInteger(value) && value > 0; }
function parametersText(value: unknown): value is string {
  if (typeof value !== "string" || new TextEncoder().encode(value).length > 64 * 1024) return false;
  // Parse only to reject malformed/non-object JSON. Display the original text:
  // even valid JSON numbers may be rounded or overflow in JavaScript.
  try { const parsed: unknown = JSON.parse(value); return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed); }
  catch { return false; }
}
function reviewRecord(value: unknown): value is ReviewRecord {
  if (!exactObject(value, ["revision", "status", "outcome", "reason", "action"]) || !exactRevision(value.revision) || typeof value.status !== "string" || !Object.hasOwn(copy.en.states, value.status) || typeof value.outcome !== "string" || !["allow", "ask", "deny"].includes(value.outcome) || typeof value.reason !== "string") return false;
  const action = value.action;
  if (!exactObject(action, ["id", "digest", "operation", "scope", "effect", "summary", "arguments_json", "expires_at"]) || !nonempty(action.id) || typeof action.digest !== "string" || !/^[a-f0-9]{64}$/.test(action.digest) || !nonempty(action.operation) || typeof action.effect !== "string" || !Object.hasOwn(copy.en.effects, action.effect) || !parametersText(action.arguments_json) || typeof action.expires_at !== "string" || !Number.isFinite(Date.parse(action.expires_at))) return false;
  const scope = action.scope, summary = action.summary;
  return exactObject(scope, ["space_id", "session_id", "task_id", "requirements_revision"]) && nonempty(scope.space_id) && nonempty(scope.session_id) && nonempty(scope.task_id) && exactRevision(scope.requirements_revision) && exactObject(summary, ["title", "account", "target", "consequence"]) && typeof summary.title === "string" && typeof summary.account === "string" && typeof summary.target === "string" && typeof summary.consequence === "string";
}
export function decodeReviewSnapshot(value: unknown): ReviewSnapshot {
  if (exactObject(value, ["schema", "kind", "reason"]) && value.schema === "personal-review.v1" && value.kind === "unavailable" && typeof value.reason === "string") return { schema: value.schema, kind: value.kind, reason: value.reason };
  if (exactObject(value, ["schema", "kind", "requests"]) && value.schema === "personal-review.v1" && value.kind === "ready" && Array.isArray(value.requests) && value.requests.every(reviewRecord)) {
    // Keep the command bound to what was rendered even if a service later
    // reuses or mutates its response object.
    return { schema: value.schema, kind: value.kind, requests: structuredClone(value.requests) };
  }
  throw new ReviewContractError("Action data could not be verified. Refresh to check the saved state.");
}
type UI = {
  Button: React.ComponentType<React.ButtonHTMLAttributes<HTMLButtonElement> & Pick<PluginUIButtonProps,"variant"> & { "data-review-choice"?: string }>;
  EmptyState: React.ComponentType<{title:React.ReactNode;description?:React.ReactNode}>;
  Section: React.ComponentType<{title?:React.ReactNode;description?:React.ReactNode;children?:React.ReactNode}>;
};

const copy = {
  en: {
    title: "Action approvals", description: "Checks each action against your request before it runs.",
    mode: "Auto Review", scope: "Applies to twin actions in this personal space.",
    automatic: "Actions covered by a current, specific instruction can proceed after review. Missing or unclear authorization asks you first.",
    pending: "Waiting for you", recent: "Recent activity", refresh: "Refresh", approve: "Approve once", reject: "Reject", revoke: "Withdraw approval", withdraw: "Withdraw action",
    account: "Account", target: "Destination", effect: "Effect", task: "Task version", parameters: "Full action parameters", expires: "Expires", digest: "Action digest",
    noActions: "No actions to review", noActionsBody: "Actions needing your decision will appear here.",
    unavailable: "Not connected", working: "Saving…", failed: "Could not save. Refresh to check the current state.", invalidData: "Action data could not be verified. Refresh to check the saved state.",
    single: "Your decision applies only to this action and these parameters. Account connections and lasting permissions require their own confirmation.",
    states: { proposed:"Awaiting review",ready:"Authorized · awaiting execution",awaiting_confirmation:"Needs your decision",approved:"Approved once · awaiting review",blocked:"Review unavailable · not executed",denied:"Denied · not executed",revoked:"Withdrawn · not executed",expired:"Expired · not executed",stale:"Task changed · not executed",executing:"Started · result pending",succeeded:"Completed",uncertain:"Result unknown · will not retry" },
    effects: { internal_bookkeeping:"Internal bookkeeping",read_only:"Read only",data_write:"Changes data",external_write:"Sends outside twin",sensitive_authority:"Account or lasting authority",unknown:"Unknown effect" },
  },
  zh: {
    title:"动作审批",description:"执行前检查具体动作是否在你的授权范围内。",
    mode:"Auto Review",scope:"仅用于当前个人空间中的 twin 动作。",
    automatic:"已有明确且有效授权的动作，经审核后继续。授权缺失或不清楚时，再请你确认。",
    pending:"等你决定",recent:"最近记录",refresh:"刷新",approve:"仅批准这次",reject:"拒绝",revoke:"撤回批准",withdraw:"撤回动作",
    account:"账号",target:"目标",effect:"影响",task:"需求版本",parameters:"完整动作参数",expires:"有效期至",digest:"动作摘要",
    noActions:"没有待审批动作",noActionsBody:"需要你决定的动作会显示在这里。",
    unavailable:"尚未接入",working:"正在保存…",failed:"保存失败，请刷新核对当前状态。",invalidData:"动作数据格式无法验证，请刷新核对保存状态。",
    single:"决定仅适用于此动作和这些参数。账号连接与长期权限需要单独确认。",
    states:{proposed:"等待审核",ready:"授权匹配，等待执行",awaiting_confirmation:"需要你确认",approved:"已批准本次，等待复核",blocked:"审核不可用，未执行",denied:"已拒绝，未执行",revoked:"已撤回，未执行",expired:"已过期，未执行",stale:"任务已变化，未执行",executing:"已经开始，等待结果",succeeded:"已完成",uncertain:"结果未知，不会自动重试"},
    effects:{internal_bookkeeping:"内部记账",read_only:"只读",data_write:"修改数据",external_write:"向外发送",sensitive_authority:"账号或长期权限",unknown:"未知影响"},
  },
};

export const personalReviewStyles = `
.personal-review { font:inherit;color:var(--ink);min-width:0; }
.personal-review p { margin:0;line-height:1.6; }
.personal-review-overview { display:grid;gap:var(--space-3);padding-bottom:var(--space-5); }
.personal-review-heading { display:flex;align-items:center;justify-content:space-between;gap:var(--space-4); }
.personal-review-heading h3 { margin:0;font:inherit;font-weight:600; }
.personal-review-muted { color:var(--ink-muted); }
.personal-review-list { display:grid;gap:var(--space-5); }
.personal-review-item { min-width:0;padding:var(--space-4) 0;border-top:1px solid var(--hairline); }
.personal-review-item h4 { margin:0;font:inherit;font-weight:600;overflow-wrap:anywhere; }
.personal-review-status { display:block;color:var(--ink-muted);margin-top:var(--space-2);line-height:1.5; }
.personal-review-item[data-review-state="awaiting_confirmation"] .personal-review-status { color:var(--ink); }
.personal-review-fields { display:grid;grid-template-columns:minmax(5em,auto) minmax(0,1fr);gap:var(--space-2) var(--space-4);margin:var(--space-4) 0; }
.personal-review-fields dt { color:var(--ink-muted); }
.personal-review-fields dd { margin:0;overflow-wrap:anywhere; }
.personal-review-parameters { margin-top:var(--space-3); }
.personal-review-parameters summary { cursor:pointer; }
.personal-review-parameters pre { white-space:pre-wrap;overflow-wrap:anywhere;font:var(--font-code,13px)/1.6 var(--font-mono,monospace);padding:var(--space-3);background:var(--surface-2);border-radius:var(--radius-sm,6px); }
.personal-review-actions { display:flex;gap:var(--space-3);flex-wrap:wrap;margin-top:var(--space-4); }
.personal-review-error { color:var(--danger-text,var(--ink));margin-top:var(--space-3)!important; }
.personal-review-footnote { margin-top:var(--space-5)!important;color:var(--ink-muted); }
@media(max-width:480px){.personal-review-fields { grid-template-columns:1fr;gap:var(--space-1); }.personal-review-fields dd { margin-bottom:var(--space-2); }.personal-review-actions>* { flex:1; }}
`;

// Uses the same public controls as the existing Personal contribution. This
// factory can be mounted inside personal.preferences without a new navigation API.
export function createPersonalReviewPanel(react: typeof React, ui: UI) {
  const h = react.createElement;
  return function PersonalReviewPanel({ service, locale = "en" }: { service: ReviewService; locale?: string }): React.ReactNode {
    const words = locale.startsWith("zh") ? copy.zh : copy.en;
    const [loaded, setLoaded] = react.useState<{service:ReviewService;snapshot:ReviewSnapshot}>();
    const [failure, setFailure] = react.useState<{service:ReviewService;message:string}>();
    const snapshot = loaded?.service === service ? loaded.snapshot : undefined;
    const error = failure?.service === service ? failure.message : "";
    const setSnapshot = (next?:ReviewSnapshot) => setLoaded(next ? {service,snapshot:next} : undefined);
    const setError = (message:string) => setFailure(message ? {service,message} : undefined);
    const showFailure = (cause: unknown) => {
      if (cause instanceof ReviewContractError) { setSnapshot(undefined); setError(words.invalidData); }
      else setError(cause instanceof Error ? cause.message : words.failed);
    };
    const [busy, setBusy] = react.useState<string>();
    const [now, setNow] = react.useState(Date.now);
    const active = react.useRef(true), epoch = react.useRef(0), submitting = react.useRef(false);
    const refresh = async () => {
      if (submitting.current) return;
      const generation = ++epoch.current;
      setError("");
      try { const next = decodeReviewSnapshot(await service.list()); if (active.current && generation === epoch.current) setSnapshot(next); }
      catch (cause) { if (active.current && generation === epoch.current) showFailure(cause); }
    };
    react.useEffect(() => {
      active.current = true; submitting.current = false; setBusy(undefined); setSnapshot(undefined); void refresh();
      const timer = setInterval(() => setNow(Date.now()), 1000);
      return () => { active.current = false; epoch.current++; clearInterval(timer); };
    }, [service]);
    const decide = async (record: ReviewRecord, choice: "approve_once" | "reject" | "revoke") => {
      if (submitting.current || !Number.isFinite(Date.parse(record.action.expires_at)) || Date.now() >= Date.parse(record.action.expires_at)) return;
      submitting.current = true; setBusy(record.action.id); setError("");
      const generation = ++epoch.current;
      const command = { space_id: record.action.scope.space_id, id: record.action.id, digest: record.action.digest, expected_revision: record.revision };
      try {
        const next = decodeReviewSnapshot(choice === "revoke" ? await service.revoke(command) : await service.resolve({ ...command, choice }));
        if (active.current && generation === epoch.current) setSnapshot(next);
      } catch (cause) { if (active.current && generation === epoch.current) showFailure(cause); }
      finally { if (active.current && generation === epoch.current) { submitting.current = false; setBusy(undefined); } }
    };
    const renderRecord = (record: ReviewRecord) => {
      const action = record.action;
      const expiry = Date.parse(action.expires_at), expired = !Number.isFinite(expiry) || now >= expiry;
      const canConfirm = record.status === "awaiting_confirmation" && ["data_write", "external_write"].includes(action.effect);
      const canRevoke = record.status === "approved" || record.status === "ready" || record.status === "blocked";
      const fields = [[words.account,action.summary.account||"—"],[words.target,action.summary.target],[words.effect,words.effects[action.effect]],[words.task,`${action.scope.task_id} · ${action.scope.requirements_revision}`],[words.expires,Number.isFinite(expiry)?new Date(expiry).toLocaleString(locale):"—"]];
      return h("article", { key:action.id,className:"personal-review-item","data-review-id":action.id,"data-review-state":record.status },
        h("h4",null,action.summary.title),h("span",{className:"personal-review-status",role:"status"},words.states[expired && canConfirm ? "expired" : record.status]),
        h("dl",{className:"personal-review-fields"},fields.flatMap(([label,value])=>[h("dt",{key:`${label}-label`},label),h("dd",{key:label},value)])),
        h("p",null,action.summary.consequence),h("p",{className:"personal-review-muted"},record.reason),
        h("details",{className:"personal-review-parameters"},h("summary",null,words.parameters),h("pre",null,action.arguments_json),h("p",{className:"personal-review-muted",style:{overflowWrap:"anywhere"}},`${words.digest}: ${action.digest}`)),
        (canConfirm || canRevoke) && h("div",{className:"personal-review-actions"},
          canConfirm && h(ui.Button,{variant:"primary",disabled:Boolean(busy)||expired,"data-review-choice":"approve_once",onClick:()=>void decide(record,"approve_once")},busy===action.id?words.working:words.approve),
          canConfirm && h(ui.Button,{variant:"secondary",disabled:Boolean(busy)||expired,"data-review-choice":"reject",onClick:()=>void decide(record,"reject")},words.reject),
          canRevoke && h(ui.Button,{variant:"secondary",disabled:Boolean(busy)||expired,"data-review-choice":"revoke",onClick:()=>void decide(record,"revoke")},record.status === "blocked" ? words.withdraw : words.revoke)));
    };
    const records = snapshot?.kind === "ready" ? snapshot.requests : [];
    const pending = records.filter(record=>record.status === "awaiting_confirmation");
    const history = records.filter(record=>record.status !== "awaiting_confirmation");
    return h("div",{className:"personal-review","data-personal-action-review":true},h("style",null,personalReviewStyles),
      h(ui.Section,{title:words.title,description:words.description},
        h("div",{className:"personal-review-overview"},h("div",{className:"personal-review-heading"},h("h3",null,words.mode),h(ui.Button,{variant:"ghost",disabled:Boolean(busy),onClick:()=>void refresh()},words.refresh)),h("p",{className:"personal-review-muted"},words.scope),snapshot?.kind === "ready" && h("p",null,words.automatic)),
        error && h("p",{role:"alert",className:"personal-review-error"},error),
        !snapshot && !error && h("p",{role:"status"},"…"),
        snapshot?.kind === "unavailable" && h(ui.EmptyState,{title:words.unavailable,description:snapshot.reason}),
        snapshot?.kind === "ready" && !records.length && h(ui.EmptyState,{title:words.noActions,description:words.noActionsBody}),
        pending.length>0 && h("section",{className:"personal-review-list","aria-label":words.pending},h("h3",null,words.pending),pending.map(renderRecord)),
        history.length>0 && h("section",{className:"personal-review-list","aria-label":words.recent},h("h3",null,words.recent),history.map(renderRecord)),
        snapshot?.kind === "ready" && h("p",{className:"personal-review-footnote"},words.single)));
  };
}
