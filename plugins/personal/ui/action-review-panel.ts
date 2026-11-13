import type * as React from "react";
import type { PluginUIButtonProps, PluginUICheckboxProps, PluginUIContainerProps, PluginUISectionProps, PluginUISelectProps } from "../../../packages/plugin-sdk/src/index";

export type ReviewStatus = "proposed" | "ready" | "awaiting_confirmation" | "approved" | "blocked" | "denied" | "revoked" | "expired" | "stale" | "executing" | "succeeded" | "uncertain";
export type ReviewRecord = {
  revision: number;
  status: ReviewStatus;
  outcome: "allow" | "ask" | "deny";
  reason: string;
  receipt_json?: string;
  action: {
    id: string; digest: string; operation: string;
    scope: { space_id: string; session_id: string; task_id: string; requirements_revision: number; context_digest?: string };
    effect: "internal_bookkeeping" | "read_only" | "data_write" | "external_write" | "sensitive_authority" | "unknown";
    summary: { title: string; account: string; target: string; consequence: string; source?: string };
    arguments_json: string;
    expires_at: string | null;
  };
};
export type ReviewSettings = { auto_review_enabled: boolean; revision: number; policy_version: string; scope: string[] };
export type ReviewSnapshot = { schema: "personal-review.v1" } & ({ kind: "unavailable"; reason: string } | { kind: "ready"; requests: ReviewRecord[]; settings?: ReviewSettings });
export type ReviewResolution = { space_id: string; id: string; digest: string; expected_revision: number; choice: "approve_once" | "reject" };
// Implement only against an authenticated host route. The host must bind
// identity outside payloads and return persisted, versioned owner snapshots.
// A GenUI callback or invokeRuntime payload is not an authenticated owner route.
export type ReviewBinding = Omit<ReviewResolution, "choice">;
export type ReviewRule = { id: string; description: string; operation: string; target: string; decision: "allow" | "ask"; enabled: boolean; created_at: string; updated_at: string };
export type ReviewRules = { schema: "personal-review-rules.v1"; revision: number; rules: ReviewRule[] };
export type ReviewRulePreview = ReviewRules & { preview: { description: string; operation: string; target: string; decision: "allow" } };
export type ReviewRuleCommand = { expected_revision: number } & (
  { action: "save"; source: ReviewBinding; decision: "allow" | "ask" } |
  { action: "update"; id: string; decision: "allow" | "ask"; enabled: boolean } |
  { action: "delete"; id: string }
);
export type ReviewService = {
  list(): Promise<unknown>;
  rules?(command?: ReviewRuleCommand): Promise<unknown>;
  previewRule?(command: ReviewBinding): Promise<unknown>;
  settings?(command: { enabled: boolean; expected_revision: number }): Promise<unknown>;
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
  if (!exactObject(value, ["revision", "status", "outcome", "reason", "action", ...(typeof value === "object" && value !== null && Object.hasOwn(value, "receipt_json") ? ["receipt_json"] : [])]) || (value.receipt_json !== undefined && !parametersText(value.receipt_json)) || !exactRevision(value.revision) || typeof value.status !== "string" || !Object.hasOwn(copy.en.states, value.status) || typeof value.outcome !== "string" || !["allow", "ask", "deny"].includes(value.outcome) || typeof value.reason !== "string") return false;
  const action = value.action;
  if (!exactObject(action, ["id", "digest", "operation", "scope", "effect", "summary", "arguments_json", "expires_at"]) || !nonempty(action.id) || typeof action.digest !== "string" || !/^[a-f0-9]{64}$/.test(action.digest) || !nonempty(action.operation) || typeof action.effect !== "string" || !Object.hasOwn(copy.en.effects, action.effect) || !parametersText(action.arguments_json) || (action.expires_at !== null && (typeof action.expires_at !== "string" || !Number.isFinite(Date.parse(action.expires_at))))) return false;
  const scope = action.scope, summary = action.summary;
  return exactObject(scope, ["space_id", "session_id", "task_id", "requirements_revision", ...(typeof scope === "object" && scope !== null && Object.hasOwn(scope, "context_digest") ? ["context_digest"] : [])]) && (scope.context_digest === undefined || typeof scope.context_digest === "string" && /^[a-f0-9]{64}$/.test(scope.context_digest)) && nonempty(scope.space_id) && nonempty(scope.session_id) && nonempty(scope.task_id) && exactRevision(scope.requirements_revision) && exactObject(summary, ["title", "account", "target", "consequence", ...(typeof summary === "object" && summary !== null && Object.hasOwn(summary, "source") ? ["source"] : [])]) && (summary.source === undefined || typeof summary.source === "string") && typeof summary.title === "string" && typeof summary.account === "string" && typeof summary.target === "string" && typeof summary.consequence === "string";
}
export function decodeReviewSnapshot(value: unknown): ReviewSnapshot {
  if (exactObject(value, ["schema", "kind", "reason"]) && value.schema === "personal-review.v1" && value.kind === "unavailable" && typeof value.reason === "string") return { schema: value.schema, kind: value.kind, reason: value.reason };
  if (exactObject(value, ["schema", "kind", "requests", ...(typeof value === "object" && value !== null && Object.hasOwn(value, "settings") ? ["settings"] : [])]) && value.schema === "personal-review.v1" && value.kind === "ready" && Array.isArray(value.requests) && value.requests.every(reviewRecord)) {
    const settings = value.settings;
    if (settings !== undefined && (!exactObject(settings, ["auto_review_enabled", "revision", "policy_version", "scope"]) || typeof settings.auto_review_enabled !== "boolean" || !exactRevision(settings.revision) || !nonempty(settings.policy_version) || !Array.isArray(settings.scope) || !settings.scope.every(nonempty))) throw new ReviewContractError("Invalid Personal review settings");
    // Keep commands bound to the rendered snapshot, even if the service reuses
    // its objects. Missing settings never imply that Auto Review is available.
    return { schema: value.schema, kind: value.kind, requests: structuredClone(value.requests), ...(settings === undefined ? {} : {settings: structuredClone(settings) as ReviewSettings}) };
  }
  throw new ReviewContractError("Action data could not be verified. Refresh to check the saved state.");
}
function reviewRule(value: unknown): value is ReviewRule {
  return exactObject(value, ["id", "description", "operation", "target", "decision", "enabled", "created_at", "updated_at"])
    && nonempty(value.id) && nonempty(value.description) && nonempty(value.operation) && typeof value.target === "string"
    && (value.decision === "allow" || value.decision === "ask") && typeof value.enabled === "boolean"
    && typeof value.created_at === "string" && Number.isFinite(Date.parse(value.created_at))
    && typeof value.updated_at === "string" && Number.isFinite(Date.parse(value.updated_at));
}
export function decodeReviewRules(value: unknown): ReviewRules {
  if (!exactObject(value, ["schema", "revision", "rules", ...(typeof value === "object" && value !== null && Object.hasOwn(value, "preview") ? ["preview"] : [])])
    || value.schema !== "personal-review-rules.v1" || !exactRevision(value.revision) || !Array.isArray(value.rules) || !value.rules.every(reviewRule)) throw new ReviewContractError("Saved rules could not be verified. Refresh to check the saved state.");
  return { schema: value.schema, revision: value.revision, rules: structuredClone(value.rules) };
}
export function decodeRulePreview(value: unknown): ReviewRulePreview {
  const rules = decodeReviewRules(value);
  if (typeof value !== "object" || value === null || !("preview" in value) || !exactObject(value.preview, ["description", "operation", "target", "decision"])) throw new ReviewContractError("Rule preview could not be verified.");
  const preview = value.preview;
  if (!nonempty(preview.description) || !nonempty(preview.operation) || typeof preview.target !== "string" || preview.decision !== "allow") throw new ReviewContractError("Rule preview could not be verified.");
  return { ...rules, preview: { description: preview.description, operation: preview.operation, target: preview.target, decision: preview.decision } };
}
function actionBinding(record: ReviewRecord): ReviewBinding {
  return { space_id: record.action.scope.space_id, id: record.action.id, digest: record.action.digest, expected_revision: record.revision };
}
function actionParameters(record: ReviewRecord): Record<string, unknown> {
  const value: unknown = JSON.parse(record.action.arguments_json);
  if (typeof value !== "object" || value === null || Array.isArray(value)) return {};
  if ("arguments" in value && typeof value.arguments === "object" && value.arguments !== null && !Array.isArray(value.arguments)) return {...value.arguments, before_content: "before_content" in value ? value.before_content : undefined, exists: "exists" in value ? value.exists : undefined};
  return value as Record<string, unknown>;
}
function fileEdits(parameters: Record<string, unknown>): Array<{old_text: string; new_text: string; replace_all: boolean}> {
  const entries = Array.isArray(parameters.edits) ? parameters.edits : [parameters];
  const edits = [];
  for (const entry of entries) {
    if (typeof entry !== "object" || entry === null || !("old_text" in entry) || typeof entry.old_text !== "string" || !("new_text" in entry) || typeof entry.new_text !== "string") return [];
    edits.push({old_text:entry.old_text,new_text:entry.new_text,replace_all:"replace_all" in entry && entry.replace_all === true});
  }
  return edits;
}
function ruleDescription(rule: Pick<ReviewRule, "operation" | "target" | "description" | "decision">, locale: string): string {
  if (!locale.startsWith("zh")) return rule.description;
  if (rule.operation === "write_file" || rule.operation === "edit_file") return rule.decision === "ask" ? `修改“${rule.target.split(/[\\/]/).pop()}”前，每次先问我。` : `以后允许修改“${rule.target.split(/[\\/]/).pop()}”的内容，仅限这个文件。`;
  if (["bash", "process_start"].includes(rule.operation)) return rule.decision === "ask" ? "在相同位置重复这条完全相同的操作前，每次先问我。" : "以后允许在相同位置，使用相同权限重复这条完全相同的操作。";
  return rule.description;
}
// Settings rows carry the decision in their own control, so the label names only the action.
function ruleSubject(rule: Pick<ReviewRule, "operation" | "target" | "description">, locale: string): string {
  const zh = locale.startsWith("zh");
  if (rule.operation === "write_file" || rule.operation === "edit_file") return `${zh ? "修改" : "Edit"} ${rule.target.split(/[\\/]/).pop()}`;
  if (zh && ["bash", "process_start"].includes(rule.operation)) return "在相同位置重复这条操作";
  return rule.description;
}
const systemReasonsZH: Record<string, string> = {
  "Review what will happen before allowing this action": "请确认这项操作和你想做的一致。",
  "Original authenticated user request is unavailable; confirm this exact action manually": "暂时无法核对你最初的要求，请确认这项具体操作。",
  "Personal state unavailable; manual review required": "暂时无法核对任务状态，需要你确认。",
  "Personal model unavailable; manual review required": "自动审核暂时不可用，需要你确认。",
  "AutoReview unavailable; nothing executed. Review this exact action manually": "自动审核暂时不可用，尚未执行。请你确认这项操作。",
  "Exact owner confirmation is required; automatic intent review is unavailable": "暂时无法自动判断是否符合你的要求，需要你确认。",
  "Your saved rule requires confirmation for this action": "你设置了“每次先问我”，请确认这次操作。",
  "The saved permission changed. Confirm this action again": "之前的许可已改变，需要重新确认这次操作。",
  "Authenticated owner confirmed this exact action": "你已允许这次操作。",
  "Allowed by your saved rule": "已按你保存的规则允许。",
  "Automatic review already approved this exact action": "自动审核已通过。",
  "Awaiting action assessment": "正在检查这项操作。",
  "Owner rejected this action": "你没有允许这次操作，尚未执行。",
  "Confirmed once; fresh assessment required before execution": "你已允许这次操作，开始前会核对操作内容是否变化。",
  "Durable dispatch claim; result not yet known": "操作已开始，正在等待结果。",
  "Execution returned a receipt": "操作已完成。",
  "Execution result is unknown; reconcile without replay": "暂时无法确认结果，需要先核对，不会自动重试。",
  "Host interrupted after dispatch; reconcile without replay": "执行时连接中断，需要先核对结果，不会自动重试。",
  "Owner revoked the pending action": "你已撤回这项操作，尚未执行。",
  "Owning execution was cancelled; pending action was not executed": "任务已取消，这项操作没有执行。",
  "Action confirmation expired": "这次确认已过期，操作没有执行。",
  "Task, owner, execution or policy changed": "任务或许可已经变化，需要重新检查，尚未执行。",
  "Operation contract changed": "操作方式已经变化，需要重新检查，尚未执行。",
  "Bound user authorization was withdrawn, expired or changed": "之前的授权已撤回、过期或改变，这项操作没有执行。",
  "Reviewer unavailable; action not executed": "自动审核暂时不可用，操作没有执行。",
  "Reviewer omitted its rationale; action not executed": "没有收到完整审核结果，操作没有执行。",
  "No valid safety verdict; action not executed": "没有收到有效审核结果，操作没有执行。",
  "Native file runtime unavailable; action not executed": "文件操作暂时不可用，文件没有修改。",
  "Host file effect unavailable; action not executed": "文件操作暂时不可用，文件没有修改。",
  "Command runtime unavailable; nothing executed": "运行程序的服务暂时不可用，尚未执行。",
  "Host command effect unavailable; nothing executed": "运行程序的服务暂时不可用，尚未执行。",
  "Frozen file action changed; no effect dispatched": "文件操作的内容已经改变，文件没有修改。",
  "Frozen command changed; nothing executed": "操作内容已经改变，尚未执行。",
};
export function reviewReason(reason: string, locale: string): string {
  if (!locale.startsWith("zh")) return reason;
  if (Object.hasOwn(systemReasonsZH, reason)) return systemReasonsZH[reason];
  if (reason.startsWith("Host boundary denied this action: ")) return "这项操作超出了当前允许的范围，尚未执行。";
  if (reason.startsWith("Command or execution environment changed; nothing executed: ")) return "操作内容或运行环境已经变化，尚未执行。";
  if (reason.startsWith("Frozen file action cannot be validated; no effect was dispatched: ")) return "文件或操作内容已经变化，文件没有修改。";
  return reason;
}
type UI = {
  Button: React.ComponentType<React.ButtonHTMLAttributes<HTMLButtonElement> & Pick<PluginUIButtonProps,"variant"> & { "data-review-choice"?: string; "data-review-refresh"?: boolean; "data-review-rule-delete"?: string }>;
  Card: React.ComponentType<PluginUIContainerProps>;
  Checkbox: React.ComponentType<PluginUICheckboxProps>;
  Section: React.ComponentType<PluginUISectionProps>;
  Select: React.ComponentType<PluginUISelectProps>;
};

const reviewResponseWaitMs = 10000;

const copy = {
  en: {
    mode: "Auto Review", modeHelp: "Actions that match your request run without asking.", 
    receipt: "Execution receipt", recentResults: "Recent action results",
    pending: "Waiting for you", recent: "Recent activity", refresh: "Refresh", approve: "Allow once", reject: "Don’t allow", revoke: "Withdraw approval", withdraw: "Withdraw action",
    account: "Account", target: "Destination", effect: "Effect", task: "Task version", parameters: "Technical details", expires: "Expires", digest: "Action digest",
    checking: "Checking approval status…", readFailed: "Could not check the saved status. Refresh when the connection returns.", unknown: "The submission result is not yet confirmed. Refresh checks its status without submitting again.", working: "Saving…", failed: "Could not save. Refresh to check the current state.", invalidData: "Action data could not be verified. Refresh to check the saved state.",
    remember: "Handle this automatically…", ruleTitle: "Remember this permission?", ruleScope: "This exact scope applies to future tasks in this Personal space.", saveRule: "Save and allow once", cancel: "Cancel", ruleSaved: "Rule saved. This action still needs your decision.", ruleSaveFailed: "The rule save could not be confirmed. Refresh to check it; you can then allow just this action.", rules: "Saved rules", askRule: "Always ask first", allowRule: "Allow automatically", deleteRule: "Remove", pausedRule: "Paused", changes: "View changes", before: "Before", after: "After", newContent: "Content to save", replaceAll: "Replace every matching occurrence", ruleDetails: "Exact permission scope", originalUnavailable: "The full original content is not included in this request. Replacing the file overwrites its current contents.", writeTitle: "Save this file?", replaceTitle: "Replace this file?", editTitle: "Update this file?", commandTitle: "Run this task step?", commandBody: "Personal wants to run a program. Review its exact command and working folder in the details before allowing it.", reason: "Why confirmation is needed", pendingCount: "waiting for you", fromRequest: "From your request: ", noExpiry: "Until this task changes or you cancel",
    states: { proposed:"Awaiting review",ready:"Authorized · awaiting execution",awaiting_confirmation:"Needs your decision",approved:"Allowed once · waiting to start",blocked:"Review unavailable · not executed",denied:"Denied · not executed",revoked:"Withdrawn · not executed",expired:"Expired · not executed",stale:"Task changed · not executed",executing:"Started · result pending",succeeded:"Completed",uncertain:"Result unknown · will not retry" },
    effects: { internal_bookkeeping:"Internal bookkeeping",read_only:"Read only",data_write:"Changes data",external_write:"Sends outside twin",sensitive_authority:"Account or lasting authority",unknown:"Unknown effect" },
  },
  zh: {
    mode:"自动审核",modeHelp:"符合你要求的操作直接执行，不再逐一询问。",
    receipt:"执行回执",recentResults:"最近动作结果",
    pending:"等你决定",recent:"最近记录",refresh:"刷新",approve:"允许这次",reject:"不允许",revoke:"撤回批准",withdraw:"撤回动作",
    account:"账号",target:"目标",effect:"影响",task:"需求版本",parameters:"技术详情",expires:"有效期至",digest:"动作摘要",
    checking:"正在核对审批结果…",readFailed:"暂时无法核对后台状态，请在连接恢复后刷新。",unknown:"提交结果暂未确认。刷新只核对状态，不会重复提交。",working:"正在保存…",failed:"保存失败，请刷新核对当前状态。",invalidData:"动作数据格式无法验证，请刷新核对保存状态。",
    remember:"以后自动处理…",ruleTitle:"记住这项许可？",ruleScope:"仅对下列范围生效，适用于此 Personal 空间今后的任务。",saveRule:"保存并允许这次",cancel:"取消",ruleSaved:"规则已保存；这次操作仍需要你的决定。",ruleSaveFailed:"未能确认规则是否保存。请刷新核对，之后也可以只允许这次操作。",rules:"已保存的规则",askRule:"每次先问我",allowRule:"自动允许",deleteRule:"移除",pausedRule:"已暂停",changes:"查看修改内容",before:"修改前",after:"修改后",newContent:"将保存的内容",replaceAll:"替换每一处匹配的内容",ruleDetails:"查看许可范围",originalUnavailable:"这次请求没有包含完整原文。替换文件会覆盖它现有的内容。",writeTitle:"保存这个文件？",replaceTitle:"替换这个文件？",editTitle:"修改这个文件？",commandTitle:"执行这一步操作？",commandBody:"运行一段程序，可能在已授权范围内读取或修改文件。请确认这符合当前任务。",reason:"为什么需要确认",pendingCount:"项等你确认",fromRequest:"来自你的请求：",noExpiry:"直到任务变化或你取消",
    states:{proposed:"等待审核",ready:"授权匹配，等待执行",awaiting_confirmation:"需要你确认",approved:"已允许这次，等待开始",blocked:"审核不可用，未执行",denied:"已拒绝，未执行",revoked:"已撤回，未执行",expired:"已过期，未执行",stale:"任务已变化，未执行",executing:"已经开始，等待结果",succeeded:"已完成",uncertain:"结果未知，不会自动重试"},
    effects:{internal_bookkeeping:"内部记账",read_only:"只读",data_write:"修改数据",external_write:"向外发送",sensitive_authority:"账号或长期权限",unknown:"未知影响"},
  },
};

export const personalReviewStyles = `
.personal-review { font:inherit;color:var(--ink);min-width:0; }
.personal-review p { margin:0;line-height:1.6; }
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
.personal-review-conversation { margin:var(--space-5) 0; }
.personal-review-history { margin-top:var(--space-5); }
.personal-review-history>summary { cursor:pointer;color:var(--ink-muted); }
.personal-review-consequence { margin-top:var(--space-3)!important;overflow-wrap:anywhere; }
.personal-review-rule-preview { margin-top:var(--space-4);padding:var(--space-4);background:var(--surface-2);border-radius:var(--radius-md,8px); }
.personal-review-rule-preview h5 { margin:0 0 var(--space-3);font:inherit;font-weight:600; }
.personal-review-settings { display:flex;flex-direction:column; }
.personal-review-settings>.plugin-ui-section { margin-top:var(--settings-gap,var(--space-8)); }
.personal-review-settings .plugin-ui-card>p[role="alert"] { padding-block:var(--settings-row-pad-block,var(--space-3));color:var(--danger-text,var(--ink));font-size:var(--font-sm); }
.personal-review-settings .plugin-ui-card>.personal-review-item { padding:var(--space-1) 0;border-top:0; }
.personal-review-settings .personal-review-parameters summary { color:var(--ink-soft);font-size:var(--font-sm); }
.personal-review-rule { display:flex;flex-wrap:wrap;align-items:center;gap:0 var(--space-2); }
.personal-review-rule>.plugin-ui-field { flex:1 1 10em;min-width:0; }
.personal-review-rule .plugin-ui-field-label { overflow-wrap:anywhere; }
.personal-review-failure { display:flex;align-items:center;justify-content:space-between;gap:var(--space-4);margin-top:var(--space-3); }
.personal-review-failure .personal-review-error { margin-top:0!important; }
.personal-review-notice { margin-top:var(--space-3)!important;color:var(--ink-muted); }
.personal-review-diff { display:grid;gap:var(--space-3); }
.personal-review-diff pre { margin:0; }
@media(max-width:480px){.personal-review-fields { grid-template-columns:1fr;gap:var(--space-1); }.personal-review-fields dd { margin-bottom:var(--space-2); }.personal-review-actions>* { flex:0 1 auto;white-space:nowrap; }}
`;

// Uses the same public controls as the existing Personal contribution. This
// factory can be mounted inside personal.preferences without a new navigation API.
export function createPersonalReviewPanel(react: typeof React, ui: UI) {
  const h = react.createElement;
  return function PersonalReviewPanel({ service, locale = "en", variant = "settings", leading }: { service: ReviewService; locale?: string; variant?: "settings" | "conversation"; leading?: React.ReactNode }): React.ReactNode {
    const conversation = variant === "conversation";
    const words = locale.startsWith("zh") ? copy.zh : copy.en;
    const hadActions = react.useRef(false);
    const [loaded, setLoaded] = react.useState<{service:ReviewService;snapshot:ReviewSnapshot}>();
    const [failure, setFailure] = react.useState<{service:ReviewService;message:string}>();
    const snapshot = loaded?.service === service ? loaded.snapshot : undefined;
    const error = failure?.service === service ? failure.message : "";
    const setSnapshot = (next?:ReviewSnapshot) => {
      if (next?.kind === "ready" && next.requests.some(record=>["data_write", "external_write"].includes(record.action.effect))) hadActions.current = true;
      setLoaded(next ? {service,snapshot:next} : undefined);
    };
    const setError = (message:string) => setFailure(message ? {service,message} : undefined);
    const showFailure = (cause: unknown) => {
      if (cause instanceof ReviewContractError) { setSnapshot(undefined); setError(words.invalidData); }
      else setError(cause instanceof Error ? cause.message : words.failed);
    };
    const [unverified, setUnverified] = react.useState(false);
    const [busy, setBusy] = react.useState<string>();
    const [savedRules, setSavedRules] = react.useState<{service: ReviewService; value: ReviewRules}>();
    const rules = savedRules?.service === service ? savedRules.value : undefined;
    const [rulePreview, setRulePreview] = react.useState<{service: ReviewService; record: ReviewRecord; value: ReviewRulePreview}>();
    const preview = rulePreview?.service === service ? rulePreview : undefined;
    const [notice, setNotice] = react.useState("");
    const [now, setNow] = react.useState(Date.now);
    const pendingAction = react.useRef<{record: ReviewRecord; started: number} | undefined>(undefined);
    const active = react.useRef(true), epoch = react.useRef(0), submitting = react.useRef(false), refreshing = react.useRef(false);
    const refresh = async () => {
      if ((submitting.current && !pendingAction.current) || refreshing.current) return;
      refreshing.current = true;
      const generation = ++epoch.current;
      try {
        let timeout: ReturnType<typeof setTimeout> | undefined;
        const next = decodeReviewSnapshot(await Promise.race([
          service.list(),
          new Promise<never>((_, reject) => { timeout = setTimeout(() => reject(new Error(words.unknown)), reviewResponseWaitMs); }),
        ]).finally(() => clearTimeout(timeout)));
        const nextRules = !pendingAction.current && !conversation && service.rules ? decodeReviewRules(await service.rules()) : undefined;
        if (active.current && generation === epoch.current) {
          const pending = pendingAction.current;
          const saved = pending && next.kind === "ready" ? next.requests.find(item => item.action.id === pending.record.action.id && item.action.scope.space_id === pending.record.action.scope.space_id && item.action.digest === pending.record.action.digest && item.action.scope.requirements_revision === pending.record.action.scope.requirements_revision) : undefined;
          // A still-pending row does not prove that an in-flight write was rejected.
          if (pending && saved && saved.revision > pending.record.revision && !["proposed", "awaiting_confirmation"].includes(saved.status)) {
            pendingAction.current = undefined; submitting.current = false; setBusy(undefined);
          }
          setSnapshot(next); if (nextRules) setSavedRules({service,value:nextRules}); setUnverified(false); setError(""); }
      }
      catch (cause) { if (active.current && generation === epoch.current) { setUnverified(true); if (pendingAction.current && !(cause instanceof ReviewContractError)) setError(words.readFailed); else showFailure(cause); } }
      finally { if (active.current && generation === epoch.current) refreshing.current = false; }
    };
    react.useEffect(() => {
      active.current = true; pendingAction.current = undefined; hadActions.current = false; submitting.current = false; refreshing.current = false; setBusy(undefined); setUnverified(false); setSnapshot(undefined); setSavedRules(undefined); setRulePreview(undefined); setNotice(""); void refresh();
      const timer = setInterval(() => setNow(Date.now()), 1000);
      const poll = setInterval(() => { void refresh(); }, 5000);
      return () => { active.current = false; pendingAction.current = undefined; epoch.current++; clearInterval(timer); clearInterval(poll); };
    }, [service]);
    const decide = async (record: ReviewRecord, choice: "approve_once" | "reject" | "revoke") => {
      if (submitting.current || unverified || (record.action.expires_at !== null && Date.now() >= Date.parse(record.action.expires_at))) return;
      submitting.current = true; setBusy(record.action.id); setError("");
      ++epoch.current; refreshing.current = false;
      const pending = {record, started: Date.now()};
      pendingAction.current = pending;
      const command = actionBinding(record);
      try {
        const next = decodeReviewSnapshot(choice === "revoke" ? await service.revoke(command) : await service.resolve({ ...command, choice }));
        if (active.current && pendingAction.current === pending) { ++epoch.current; setSnapshot(next); setUnverified(false); }
      } catch (cause) { if (active.current && pendingAction.current === pending) { ++epoch.current; setUnverified(true); showFailure(cause); } }
      finally { if (active.current && pendingAction.current === pending) { pendingAction.current = undefined; submitting.current = false; refreshing.current = false; setBusy(undefined); } }
    };
    const changeSettings = async () => {
      if (submitting.current || unverified || snapshot?.kind !== "ready" || !snapshot.settings || !service.settings) return;
      submitting.current = true; setBusy("settings"); setError("");
      const generation = ++epoch.current;
      try {
        const next = decodeReviewSnapshot(await service.settings({enabled: !snapshot.settings.auto_review_enabled, expected_revision: snapshot.settings.revision}));
        if (active.current && generation === epoch.current) { setSnapshot(next); setUnverified(false); }
      } catch (cause) { if (active.current && generation === epoch.current) { setUnverified(true); showFailure(cause); } }
      finally { if (active.current && generation === epoch.current) { submitting.current = false; refreshing.current = false; setBusy(undefined); } }
    };
    const remember = async (record: ReviewRecord) => {
      if (!service.previewRule || submitting.current || unverified) return;
      submitting.current = true; setBusy(record.action.id); setError(""); setNotice("");
      const generation = ++epoch.current;
      try {
        const value = decodeRulePreview(await service.previewRule(actionBinding(record)));
        if (active.current && generation === epoch.current) setRulePreview({service,record:structuredClone(record),value});
      } catch (cause) { if (active.current && generation === epoch.current) showFailure(cause); }
      finally { if (active.current && generation === epoch.current) { submitting.current = false; refreshing.current = false; setBusy(undefined); } }
    };
    const saveRule = async () => {
      if (!preview || !service.rules || submitting.current || unverified) return;
      const current = snapshot?.kind === "ready" ? snapshot.requests.find(item => item.action.id === preview.record.action.id) : undefined;
      if (!current || current.revision !== preview.record.revision || current.action.digest !== preview.record.action.digest || current.status !== "awaiting_confirmation" || (current.action.expires_at !== null && Date.now() >= Date.parse(current.action.expires_at))) return;
      submitting.current = true; setBusy(current.action.id); setError("");
      const generation = ++epoch.current;
      let saved = false;
      try {
        const value = decodeReviewRules(await service.rules({expected_revision:preview.value.revision,action:"save",source:actionBinding(preview.record),decision:"allow"}));
        if (!active.current || generation !== epoch.current) return;
        saved = true; setSavedRules({service,value}); setRulePreview(undefined); setNotice(words.ruleSaved);
        submitting.current = false; setNotice("");
        await decide(preview.record, "approve_once");
      } catch (cause) {
        if (active.current && generation === epoch.current) { setUnverified(true); setError(saved ? words.ruleSaved : words.ruleSaveFailed); }
      } finally { if (active.current && generation === epoch.current) { submitting.current = false; refreshing.current = false; setBusy(undefined); } }
    };
    const changeRule = async (command: ReviewRuleCommand) => {
      if (!service.rules || submitting.current || unverified) return;
      submitting.current = true; setBusy("rules"); setError("");
      const generation = ++epoch.current;
      try {
        const value = decodeReviewRules(await service.rules(command));
        if (active.current && generation === epoch.current) setSavedRules({service,value});
      } catch (cause) { if (active.current && generation === epoch.current) { setUnverified(true); showFailure(cause); } }
      finally { if (active.current && generation === epoch.current) { submitting.current = false; refreshing.current = false; setBusy(undefined); } }
    };
    const renderRecord = (record: ReviewRecord) => {
      const action = record.action;
      const expiry = action.expires_at === null ? Infinity : Date.parse(action.expires_at), expired = now >= expiry;
      const canConfirm = record.status === "awaiting_confirmation" && ["data_write", "external_write"].includes(action.effect);
      const canRevoke = record.status === "approved" || record.status === "ready" || record.status === "blocked";
      const fields = [[words.account,action.summary.account||"—"],[words.target,action.summary.target],[words.effect,words.effects[action.effect]],[words.task,`${action.scope.task_id} · ${action.scope.requirements_revision}`],[words.expires,Number.isFinite(expiry)?new Date(expiry).toLocaleString(locale):words.noExpiry]];
      const parameters = actionParameters(record);
      const edits = fileEdits(parameters);
      const fileAction = action.operation === "write_file" || action.operation === "edit_file";
      const commandAction = ["bash", "shell", "process", "process_start", "process_write_stdin"].includes(action.operation);
      const filename = Array.from(action.summary.target.split(/[\\/]/).pop() || "");
      const fileLabel = filename.length > 40 ? filename.slice(0,40).join("")+"…" : filename.join("");
      const title = fileAction ? `${action.operation === "write_file" ? (parameters.exists === true ? words.replaceTitle : words.writeTitle) : words.editTitle} ${fileLabel}` : commandAction ? words.commandTitle : action.summary.title;
      const disabled = Boolean(busy) || unverified || expired;
      return h("article", { key:action.id,className:"personal-review-item","data-review-id":action.id,"data-review-state":record.status },
        h("h4",null,title),h("span",{className:"personal-review-status",role:"status"},pendingAction.current?.record.action.id === action.id ? words.checking : words.states[expired && canConfirm ? "expired" : record.status]),
        action.summary.source && h("p",{className:"personal-review-muted personal-review-consequence","data-review-source":true},`${words.fromRequest}${action.summary.source}`),
        h("p",{className:"personal-review-consequence"},commandAction ? words.commandBody : action.summary.consequence),
        canConfirm && h("p",{className:"personal-review-muted"},reviewReason(record.reason,locale)),
        fileAction && h("details",{className:"personal-review-parameters","data-review-changes":true},h("summary",null,words.changes),
          action.operation === "edit_file" && edits.length ? edits.map((edit,index)=>h("div",{key:index,className:"personal-review-diff"},edit.replace_all && h("p",{className:"personal-review-muted"},words.replaceAll),h("strong",null,words.before),h("pre",null,edit.old_text),h("strong",null,words.after),h("pre",null,edit.new_text))) : typeof parameters.content === "string" ? h("div",{className:"personal-review-diff"},typeof parameters.before_content === "string" ? h(react.Fragment,null,h("strong",null,words.before),h("pre",null,parameters.before_content)) : parameters.exists !== false && h("p",{className:"personal-review-muted"},words.originalUnavailable),h("strong",null,words.newContent),h("pre",null,parameters.content)) : h("pre",null,action.arguments_json)),
        h("details",{className:"personal-review-parameters"},h("summary",null,words.parameters),h("p",{className:"personal-review-muted"},record.reason),h("dl",{className:"personal-review-fields"},fields.flatMap(([label,value])=>[h("dt",{key:`${label}-label`},label),h("dd",{key:label},value)])),h("pre",null,action.arguments_json),h("p",{className:"personal-review-muted",style:{overflowWrap:"anywhere"}},`${words.digest}: ${action.digest}`)),
        record.receipt_json && h("details",{className:"personal-review-parameters","data-review-receipt":true},h("summary",null,words.receipt),h("pre",null,record.receipt_json)),
        (canConfirm || canRevoke) && h("div",{className:"personal-review-actions"},
          canConfirm && h(ui.Button,{variant:"primary",disabled:Boolean(busy)||unverified||expired,"data-review-choice":"approve_once",onClick:()=>void decide(record,"approve_once")},busy===action.id?(pendingAction.current && now - pendingAction.current.started >= reviewResponseWaitMs ? words.checking : words.working):words.approve),
          canConfirm && service.previewRule && service.rules && h(ui.Button,{variant:"secondary",disabled,"data-review-choice":"remember",onClick:()=>void remember(record)},words.remember),
          canConfirm && h(ui.Button,{variant:"secondary",disabled:Boolean(busy)||unverified||expired,"data-review-choice":"reject",onClick:()=>void decide(record,"reject")},words.reject),
          canRevoke && h(ui.Button,{variant:"secondary",disabled:Boolean(busy)||unverified||expired,"data-review-choice":"revoke",onClick:()=>void decide(record,"revoke")},record.status === "blocked" ? words.withdraw : words.revoke)),
        preview?.record.action.id === action.id && h("section",{className:"personal-review-rule-preview","data-review-rule-preview":true,"aria-label":words.ruleTitle},
          h("h5",null,words.ruleTitle),h("p",null,ruleDescription(preview.value.preview,locale)),h("p",{className:"personal-review-muted"},words.ruleScope),h("details",{className:"personal-review-parameters"},h("summary",null,words.ruleDetails),h("p",null,preview.value.preview.target),h("p",null,preview.value.preview.description),h("pre",null,action.arguments_json)),
          h("div",{className:"personal-review-actions"},h(ui.Button,{variant:"primary",disabled:disabled || record.revision !== preview.record.revision,"data-review-choice":"save_rule",onClick:()=>void saveRule()},words.saveRule),h(ui.Button,{variant:"secondary",disabled:Boolean(busy),onClick:()=>setRulePreview(undefined)},words.cancel))));
    };
    const records = snapshot?.kind === "ready" ? snapshot.requests.filter(record=>!conversation || ["data_write", "external_write"].includes(record.action.effect)) : [];
    const pending = records.filter(record=>["awaiting_confirmation", "approved", "ready", "blocked", "executing", "uncertain"].includes(record.status));
    const history = records.filter(record=>!pending.includes(record)).sort((a,b)=>(b.action.expires_at === null ? 0 : Date.parse(b.action.expires_at))-(a.action.expires_at === null ? 0 : Date.parse(a.action.expires_at)));
    const settings = snapshot?.kind === "ready" ? snapshot.settings : undefined;
    // Chat only carries decisions that still need the owner. Completed action
    // history remains in Personal settings instead of trailing every reply.
    if (conversation && !pendingAction.current && !pending.length && (!error || !hadActions.current)) return null;
    const failureRow = error && h("div",{className:"personal-review-failure"},h("p",{role:"alert",className:"personal-review-error"},error),h(ui.Button,{variant:"ghost",disabled:Boolean(busy)&&!pendingAction.current,"data-review-refresh":true,onClick:()=>void refresh()},words.refresh));
    if (conversation) {
      const awaiting = pending.filter(record=>record.status === "awaiting_confirmation" && record.action.id !== pendingAction.current?.record.action.id).length;
      return h("div",{className:"personal-review personal-review-conversation","data-personal-action-review":true,"data-review-variant":variant},h("style",null,personalReviewStyles),
        h("div",{className:"personal-review-heading"},h("h3",null,awaiting ? `${awaiting} ${words.pendingCount}` : words.recentResults),h(ui.Button,{variant:"ghost",disabled:Boolean(busy)&&!pendingAction.current,"data-review-refresh":true,onClick:()=>void refresh()},words.refresh)),
        pendingAction.current && now - pendingAction.current.started >= reviewResponseWaitMs && h("p",{role:"status","data-review-reconciling":true},words.unknown),
        notice && h("p",{role:"status"},notice),
        error && h("p",{role:"alert",className:"personal-review-error"},error),
        pending.length>0 && h("section",{className:"personal-review-list","aria-label":words.pending},pending.map(renderRecord)));
    }
    const ruleChoice = (rule: ReviewRule) => !rule.enabled ? "paused" : rule.decision;
    const changeRuleChoice = (rule: ReviewRule, choice: string) => rules && void changeRule({action:"update",expected_revision:rules.revision,id:rule.id,decision:choice === "paused" ? rule.decision : choice === "ask" ? "ask" : "allow",enabled:choice !== "paused"});
    return h("div",{className:"personal-review personal-review-settings","data-personal-action-review":true,"data-review-variant":variant},h("style",null,personalReviewStyles),
      h(ui.Card,null,leading,
        settings && service.settings ? h(ui.Checkbox,{label:words.mode,description:words.modeHelp,role:"switch","aria-checked":settings.auto_review_enabled,checked:settings.auto_review_enabled,disabled:Boolean(busy)||unverified||(!settings.auto_review_enabled&&settings.scope.length===0),onChange:()=>void changeSettings()})
          : h(ui.Checkbox,{label:words.mode,description:snapshot?.kind === "unavailable" ? snapshot.reason : undefined,checked:false,disabled:true})),
      failureRow,
      pendingAction.current && now - pendingAction.current.started >= reviewResponseWaitMs && h("p",{role:"status","data-review-reconciling":true},words.unknown,h(ui.Button,{variant:"ghost","data-review-refresh":true,onClick:()=>void refresh()},words.refresh)),
      notice && h("p",{role:"status",className:"personal-review-notice"},notice),
      rules && rules.rules.length>0 && h(ui.Section,{title:words.rules,"aria-label":words.rules},h(ui.Card,null,
        rules.rules.map(rule=>h("div",{key:rule.id,className:"personal-review-rule","data-review-rule":rule.id},
          h(ui.Select,{label:ruleSubject(rule,locale),title:rule.target,value:ruleChoice(rule),disabled:Boolean(busy)||unverified,"data-review-rule-choice":rule.id,onChange:(event: React.ChangeEvent<HTMLSelectElement>)=>changeRuleChoice(rule,event.target.value)},
            h("option",{value:"allow"},words.allowRule),h("option",{value:"ask"},words.askRule),h("option",{value:"paused"},words.pausedRule)),
          h(ui.Button,{variant:"ghost",disabled:Boolean(busy)||unverified,"data-review-rule-delete":rule.id,onClick:()=>void changeRule({action:"delete",expected_revision:rules.revision,id:rule.id})},words.deleteRule))))),
      pending.length>0 && h(ui.Section,{title:words.pending},pending.map(record=>h(ui.Card,{key:record.action.id},renderRecord(record)))),
      history.length>0 && h(ui.Section,{title:words.recent},history.map(record=>h(ui.Card,{key:record.action.id},renderRecord(record)))));
  };
}
