import * as React from "react";
import { createPluginUIKit, type PluginUIKit } from "../../../shared/workbench";
import { evaluate, initialState, persistentState, validateSpec, validateState, type Node, type Scalar, type ScalarField, type State, type Spec } from "../../../../../plugins/personal/src/genui/schema";
import type { ActionRequest, ActionResult } from "../../../../../plugins/personal/src/genui/service";
import "./personal-genui.css";

const defaultUI = createPluginUIKit(React);
type Controls = Pick<PluginUIKit, "Stack" | "Row" | "Card" | "Button" | "TextInput" | "TextArea" | "Select" | "Checkbox">;
export type SurfaceTransport = { onStateChange?(state: State): void; sync?(request: { surfaceId: string; expectedRevision: number; state: State }): Promise<ActionResult>; dispatch(request: ActionRequest): Promise<ActionResult>; read(): Promise<ActionResult>; retry(resumeHeld?: boolean): Promise<ActionResult> };
type Props = { initial: ActionResult; transport: SurfaceTransport; ui?: Controls; submittedNotice?: string; submissionHelp?: string };
type RowContext = { field: string; index: number; item: Record<string, Scalar> };

class Boundary extends React.Component<{ fallback: string; recoveryKey: string | undefined; children: React.ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidUpdate(previous: Readonly<{ recoveryKey: string | undefined }>) {
    if (this.state.failed && previous.recoveryKey !== this.props.recoveryKey) this.setState({ failed: false });
  }
  render() { return this.state.failed ? <p role="status">{this.props.fallback}</p> : this.props.children; }
}

/** Used only inside Personal's isolated conversation host. No global presenters. */
export function PersonalGenUISurface(props: Props): React.ReactNode {
  const fallback = typeof props.initial?.surface?.spec?.fallbackText === "string" ? props.initial.surface.spec.fallbackText.slice(0, 4000) : "这个交互界面暂时无法显示，请用文字继续。";
  return <Boundary key={props.initial?.surface?.surfaceId} recoveryKey={JSON.stringify(props.initial)} fallback={fallback}><ValidatedSurface {...props} /></Boundary>;
}

type DraftView = { result: ActionResult; state: State; conflict: boolean; pending?: ActionRequest; error: string; notice: string };
function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (value && typeof value === "object") return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonical((value as Record<string, unknown>)[key])}`).join(",")}}`;
  return JSON.stringify(value);
}
function withTemporary(spec: Spec, saved: State, draft: State): State {
  return { ...initialState(spec, saved), ...Object.fromEntries(Object.entries(draft).filter(([key]) => spec.fields[key]?.scope === "temporary")) };
}
function ValidatedSurface({ initial, transport, ui = defaultUI, submittedNotice = "选择已由当前对话接收，等待文字解释。", submissionHelp = "解释这些选择只请求文字回应；这一轮不调用工具、生成新界面或执行任务。" }: Props): React.ReactNode {
  const [view, setView] = React.useState<DraftView>(() => ({ result: initial, state: initialState(validateSpec(initial.surface.spec), validateState(initial.surface.spec, initial.surface.state)), conflict: false, error: "", notice: "" }));
  const { result, state, conflict, pending, error, notice } = view;
  const spec = React.useMemo(() => validateSpec(result.surface.spec), [result.surface.spec]);
  const [busy, setBusy] = React.useState(false);
  const draft = React.useRef(state), authority = React.useRef(result);
  const syncWork = React.useRef<Promise<void> | undefined>(undefined), editVersion = React.useRef(0), syncBlocked = React.useRef(false);
  const [syncing, setSyncing] = React.useState(false);
  draft.current = state; authority.current = result;
  React.useEffect(() => { transport.onStateChange?.(state); }, [state, transport.onStateChange]);
  const inFlight = React.useRef(false), alive = React.useRef(true);
  React.useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const receive = React.useCallback((next: ActionResult, mode: "observe" | "ack" | "replace" = "observe") => {
    setView(previous => {
      const old = previous.result;
      if (next.surface.revision < old.surface.revision || (next.surface.revision === old.surface.revision && (next.deliveryRevision ?? 0) < (old.deliveryRevision ?? 0))) return previous;
      const advanced = next.surface.revision > old.surface.revision;
      const dirty = canonical(persistentState(old.surface.spec, previous.state)) !== canonical(persistentState(old.surface.spec, old.surface.state));
      const matches = canonical(persistentState(old.surface.spec, previous.state)) === canonical(persistentState(next.surface.spec, next.surface.state));
      const conflict = mode !== "replace" && !matches && (previous.conflict || (advanced && dirty));
      return { result: next, state: conflict || (!advanced && mode === "observe") ? previous.state : withTemporary(next.surface.spec, next.surface.state, previous.state), conflict,
        pending: mode !== "observe" || advanced ? undefined : previous.pending,
        error: mode === "ack" || mode === "replace" || advanced ? "" : previous.error,
        notice: mode === "ack" && !next.surface.submitted ? next.delivery === "rejected" ? "提交未被接收，可修改选择后重新提交。" : "选择已保存。" : previous.notice };
    });
  }, []);
  React.useEffect(() => { if (!syncWork.current) receive(initial); }, [initial, receive]);
  const disabled = busy || result.surface.submitted || Boolean(pending);
  const deliveryNotice = (next: ActionResult): string => {
    switch (next.delivery) {
      case "pending": return "选择已保存，发送结果待确认。可安全重试。";
      case "held": return "选择已接收，尚未运行。请明确继续文字解释。";
      case "unconfirmed": return "选择已进入对话，但回复结果未确认。不会自动重放。";
      case "stopped": return "选择已接收，但回复已停止。不会自动重新执行。";
      case "rejected": return "提交未被接收，可修改选择后重新提交。";
      case "accepted": return submittedNotice;
      default: return "";
    }
  };
  const execute = async (operation: () => Promise<ActionResult>, mode: "observe" | "ack" | "replace" = "ack") => {
    if (inFlight.current) return;
    inFlight.current = true; setBusy(true); setView(previous => ({ ...previous, error: "" }));
    try { const next = await operation(); if (alive.current) { if (mode === "replace") syncBlocked.current = false; receive(next, mode); } }
    catch (cause) {
      const message = String(cause), stale = message.includes("revision_conflict"), tooLarge = message.includes("submission_too_large");
      const rejected = tooLarge || stale || message.includes("submission_rejected") || message.includes("invalid_genui");
      if (alive.current) setView(previous => ({ ...previous, pending: rejected ? undefined : previous.pending,
        error: tooLarge ? "选择内容太多，尚未提交。请缩短内容后重试。" : stale ? "另一处已更新这个界面。请载入已保存的选择后重试。" : "操作未确认。重试会沿用同一请求，不会重复提交。" }));
    }
    finally { inFlight.current = false; if (alive.current) setBusy(false); }
  };
  const synchronize = (): Promise<void> => {
    if (!transport.sync || syncBlocked.current) return Promise.resolve();
    if (syncWork.current) return syncWork.current;
    setSyncing(true);
    const run = async () => {
      let version: number;
      do {
        version = editVersion.current;
        const current = authority.current;
        const next = await transport.sync!({ surfaceId: current.surface.surfaceId, expectedRevision: current.surface.revision, state: structuredClone(draft.current) });
        authority.current = next;
        if (alive.current) setView(previous => ({ ...previous, result: next, error: "", notice: "已同步" }));
      } while (version !== editVersion.current);
    };
    syncWork.current = run().catch(cause => {
      syncBlocked.current = true;
      if (alive.current) setView(previous => ({ ...previous, error: String(cause).includes("revision_conflict") ? "另一处已更新这个界面。请载入已保存的选择后重试。" : "自动同步未完成。当前填写仍会随下一条消息发送，请重试同步以保存。" }));
    }).finally(() => { syncWork.current = undefined; if (alive.current) setSyncing(false); });
    return syncWork.current;
  };
  const changeState = (next: State) => {
    draft.current = next; editVersion.current++;
    transport.onStateChange?.(next);
    setView(previous => ({ ...previous, state: next, notice: "" }));
    void synchronize();
  };
  const dispatch = (actionId: string) => {
    if (disabled || conflict || inFlight.current) return;
    void execute(async () => {
      await syncWork.current;
      const request = { surfaceId: result.surface.surfaceId, actionId, expectedRevision: authority.current.surface.revision, eventId: `event-${crypto.randomUUID()}`, state: structuredClone(draft.current) };
      setView(previous => ({ ...previous, pending: request }));
      return transport.dispatch(request);
    });
  };
  const update = (key: string, value: Scalar, row?: RowContext, itemField?: string) => {
    if (disabled) return;
    const previous = draft.current;
    changeState(itemField && row
      ? { ...previous, [row.field]: (previous[row.field] as Record<string, Scalar>[]).map((item, index) => index === row.index ? { ...item, [itemField]: value } : item) }
      : { ...previous, [key]: value });
  };
  const render = (node: Node, row?: RowContext): React.ReactNode => {
    if (node.visible !== undefined) {
      try {
        const visible = evaluate(node.visible, state, row?.item);
        if (typeof visible !== "boolean") throw new Error("visibility must be boolean");
        if (!visible) return null;
      } catch { return <p key={`${node.id}-${row?.index ?? "root"}`} role="status">这个条件暂时无法计算，请调整输入。</p>; }
    }
    const key = row ? `${node.id}-${row.index}` : node.id;
    const attrs = { "data-genui-node": node.id };
    switch (node.type) {
      case "stack": return <ui.Stack key={key} {...attrs}>{node.children.map(child => render(child, row))}</ui.Stack>;
      case "row": return <ui.Row key={key} {...attrs}>{node.children.map(child => render(child, row))}</ui.Row>;
      case "card": return <ui.Card key={key} {...attrs}>{node.children.map(child => render(child, row))}</ui.Card>;
      case "text": {
        let value: string;
        try { const calculated = evaluate(node.value, state, row?.item); value = typeof calculated === "number" ? new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(calculated) : typeof calculated === "object" ? "" : String(calculated); }
        catch { value = "暂时无法计算，请调整输入。"; }
        return <p key={key} {...attrs}>{value}</p>;
      }
      case "repeat": return <ui.Stack key={key} {...attrs}>{(state[node.field] as Record<string, Scalar>[]).map((item, index) => <React.Fragment key={`${node.id}-${index}`}>{node.children.map(child => render(child, { field: node.field, index, item }))}</React.Fragment>)}</ui.Stack>;
      case "button": {
        const action = node.action ? spec.actions.find(a => a.id === node.action) : undefined;
        // Autosynced conversations continue through the ordinary composer.
        // Standalone previews without sync still need explicit persistence.
        if (transport.sync && action) return null;
        const field = node.field ? spec.fields[node.field] : undefined;
        const atLimit = node.local === "append" && field?.type === "list" && (state[node.field] as Record<string, Scalar>[]).length >= field.maxItems;
        return <ui.Button key={key} {...attrs} disabled={disabled || atLimit || Boolean(action && conflict)} variant={action?.kind === "submit" ? "primary" : "secondary"} onClick={() => {
          if (node.action !== undefined) { dispatch(node.action); return; }
          if (field?.type !== "list") return;
          const rows = state[node.field] as Record<string, Scalar>[];
          const next = node.local === "append" ? [...rows, Object.fromEntries(Object.entries(field.item).map(([name, value]) => [name, value.initial]))] : rows.filter((_, index) => index !== row?.index);
          changeState({ ...draft.current, [node.field]: next });
        }}>{action?.kind === "submit" ? "解释这些选择" : action?.kind === "save" ? "保存选择" : node.label}</ui.Button>;
      }
      case "input": case "textarea": case "checkbox": case "slider": case "select": {
        const binding = controlBinding(spec, state, node, row);
        const props = { ...attrs, disabled, label: node.label, "aria-label": node.label };
        const change = (value: Scalar) => update(node.field ?? "", value, row, node.itemField);
        if (node.type === "checkbox") return <ui.Checkbox key={key} {...props} checked={binding.value === true} onChange={event => change(event.currentTarget.checked)} />;
        if (node.type === "select") return <ui.Select key={key} {...props} value={String(binding.value)} onChange={event => change(event.currentTarget.value)}>{binding.field.options?.map(value => <option key={value} value={value}>{value}</option>)}</ui.Select>;
        if (node.type === "slider") return <ui.TextInput key={key} {...props} type="range" min={binding.field.min} max={binding.field.max} step={1} value={Number(binding.value)} description={String(binding.value)} onChange={event => change(Number(event.currentTarget.value))} />;
        if (node.type === "textarea") return <ui.TextArea key={key} {...props} rows={3} value={String(binding.value)} maxLength={binding.field.maxLength ?? 1000} onChange={event => change(event.currentTarget.value)} />;
        return <ui.TextInput key={key} {...props} value={String(binding.value)} maxLength={binding.field.maxLength ?? 1000} onChange={event => change(event.currentTarget.value)} />;
      }
      default: { const exhaustive: never = node; return exhaustive; }
    }
  };
  return <section className="personal-genui" aria-label={spec.title} data-surface-id={result.surface.surfaceId} aria-busy={busy}>
    <h3>{spec.title}</h3>
    {transport.sync && <p className="personal-genui-feedback">填写内容会自动同步到下一轮对话。</p>}
    {!transport.sync && spec.actions.some(action => action.kind === "submit") && <p className="personal-genui-feedback">{submissionHelp}</p>}
    {conflict && <div data-genui-conflict role="status">
      <p>{result.surface.submitted ? "另一处已提交。这里仍保留你的未保存草稿供查看，不能再次提交到此界面。" : "已保存选择有更新。你的未保存草稿仍保留，请选择使用哪一份。"}</p>
      <ui.Button disabled={busy || syncing} data-action="use-genui-authority" onClick={() => { syncBlocked.current = false; receive(result, "replace"); }}>放弃草稿，使用已保存选择</ui.Button>
      {!result.surface.submitted && <ui.Button disabled={busy || syncing} data-action="keep-genui-draft" onClick={() => { syncBlocked.current = false; setView(previous => ({ ...previous, conflict: false, error: "", notice: "草稿已保留。重新保存会替换已保存选择。" })); void synchronize(); }}>保留草稿并重新编辑</ui.Button>}
    </div>}
    {render(spec.tree)}
    <div className="personal-genui-feedback" role="status" aria-live="polite">{deliveryNotice(result) || (syncing ? "正在同步…" : notice)}</div>
    {error && <p role="alert">{error}</p>}
    {(pending || result.submission) && <ui.Button data-action="retry-genui" disabled={busy} onClick={() => void execute(() => pending ? transport.dispatch(pending) : transport.retry(result.delivery === "held"))}>{result.delivery === "held" ? "继续文字解释" : "重试确认"}</ui.Button>}
    {result.surface.submitted && <ui.Button disabled={busy} onClick={() => void execute(() => transport.read(), "observe")}>刷新状态</ui.Button>}
    {error && transport.sync && <ui.Button disabled={busy || syncing} onClick={() => { syncBlocked.current = false; void synchronize(); }}>重试同步</ui.Button>}
    {error && <ui.Button disabled={busy || syncing} onClick={() => void execute(() => transport.read(), "replace")}>载入已保存选择</ui.Button>}
  </section>;
}

function controlBinding(spec: Spec, state: State, node: Extract<Node, { type: "input" | "textarea" | "checkbox" | "slider" | "select" }>, row?: RowContext): { field: ScalarField; value: Scalar } {
  if (node.itemField !== undefined && row) {
    const field = spec.fields[row.field];
    if (field.type === "list") return { field: field.item[node.itemField], value: row.item[node.itemField] };
  }
  if (node.field !== undefined) {
    const field = spec.fields[node.field];
    if (field.type !== "list") return { field, value: state[node.field] as Scalar };
  }
  throw new Error("invalid control binding");
}
