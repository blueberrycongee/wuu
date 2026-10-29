export async function activate(api) {
  const React = api.react, h = React.createElement;
  const { Button, Checkbox, EmptyState, LoadingState } = api.ui;
  const route = { userSpaceId: "local", productDomain: "personal" };
  const call = (method, input = {}) => api.invokeRuntime(`calendar.${method}`, input, route);
  const words = {
    en: { title: "Calendar", close: "Back to conversation", refresh: "Refresh", loading: "Reading calendar operations", empty: "No calendar changes to review", emptyText: "Your assistant will prepare a proposal when a task needs a calendar change.", unconnected: "Calendar reader is not connected", readOnly: "Calendar changes are unavailable. No authorized write adapter is configured on this host.", ready: "Calendar reader available", reconnect_required: "Calendar reader needs reconnection", error: "Calendar reader needs attention", refreshing: "Refreshing calendar", fresh: "Fresh", stale: "Cached information may be out of date", unavailable: "Personal Calendar is unavailable because this host did not meet its configured isolation requirement.", inspect: "Choose a proposal to review", create: "Create event", reschedule: "Reschedule event", proposed: "Waiting for your confirmation", confirmed: "Confirmed · awaiting execution", running: "Request in progress", uncertain: "Outcome not confirmed", completed: "Event verified on calendar", failed: "Needs attention", cancelled: "Cancelled before dispatch", proposedEvent: "Proposed event", original: "Original event", summary: "Event", location: "Location", start: "Start", end: "End", attendees: "Attendees", optional: "optional", required: "required", none: "None", updates: "Requested invitation updates", account: "Account", calendar: "Calendar", availability: "Availability check", scope: "Only this calendar was checked. Other attendees' availability has not been checked.", checked: "Checked at", conflicts: "Conflicting events", notifications: "Invitation and notification effects", review: "I reviewed this exact change, its recipients, and notification effects", confirmCreate: "Confirm event creation", confirmMove: "Confirm reschedule", invalid: "The preview is incomplete or out of date. Refresh before confirming.", binding: "Full immutable preview and execution reference", cancel: "Cancel operation", cancelTitle: "Cancel this operation?", cancelText: "Cancellation stops further attempts where possible. A request already sent may have changed the calendar. It does not delete or undo an event; check the outcome before taking another action.", confirmCancel: "Request cancellation", keep: "Keep operation", reconcile: "Check outcome", uncertainText: "The calendar may already have changed. Check the outcome; do not repeat the write.", cancelPending: "Cancellation requested; the remote outcome still needs checking.", proof: "Verified result", notificationUnverified: "The event was verified. Invitation delivery has not been verified.", digest: "Preview digest", revision: "Revision" },
    zh: { title: "日历", close: "返回对话", refresh: "刷新", loading: "正在读取日历操作", empty: "暂时没有需要确认的日历变更", emptyText: "事情需要调整日历时，助理会先准备一份提案。", unconnected: "日历读取尚未连接", readOnly: "当前不能修改日历。宿主尚未配置已获授权的写入适配器。", ready: "日历读取可用", reconnect_required: "日历读取需要重新连接", error: "日历读取需要处理", refreshing: "正在刷新日历", fresh: "最新读取", stale: "缓存信息可能已过期", unavailable: "当前宿主未满足已配置的隔离要求，Personal 日历暂不可用。", inspect: "选择一项提案，查看完整内容", create: "创建日程", reschedule: "调整日程", proposed: "等待你确认", confirmed: "已确认 · 等待执行", running: "请求处理中", uncertain: "结果尚未确认", completed: "已在日历核实日程", failed: "需要处理", cancelled: "已在发出前取消", proposedEvent: "拟修改的日程", original: "原日程", summary: "主题", location: "地点", start: "开始", end: "结束", attendees: "参与者", optional: "可选", required: "必选", none: "无", updates: "请求发送的邀请更新", account: "账号", calendar: "日历", availability: "空闲时间检查", scope: "只检查了这一本日历，尚未检查其他参与者是否有空。", checked: "检查时间", conflicts: "冲突日程", notifications: "邀请与通知影响", review: "我已查看这项具体变更、接收者和通知影响", confirmCreate: "确认创建日程", confirmMove: "确认调整日程", invalid: "预览不完整或已过期，请刷新后再确认。", binding: "完整不可变预览与执行关联", cancel: "取消操作", cancelTitle: "取消这项操作？", cancelText: "取消会尽可能停止后续尝试。已经发出的请求可能已更改日历，这不会删除日程，也不会撤销已完成的修改。继续操作前请先核实结果。", confirmCancel: "请求取消", keep: "保留操作", reconcile: "核实结果", uncertainText: "日历可能已经更改。请先核实结果，不要重复执行写入。", cancelPending: "已请求取消，仍需核实远端结果。", proof: "已核实结果", notificationUnverified: "已核实日程，但尚未核实邀请是否送达。", digest: "预览摘要", revision: "版本" }
  };
  api.registerStyle({ id: "personal-calendar", css: `
    .pcal { height:100%;min-height:0;display:flex;flex-direction:column;color:var(--ink);font:var(--font-ui)/1.55 var(--appearance-ui-font,system-ui,sans-serif);container-type:inline-size; }
    .pcal * { box-sizing:border-box; }
    .pcal button,.pcal input { font:inherit; }
    .pcal-head { display:flex;justify-content:space-between;align-items:center;gap:var(--space-4);padding:var(--space-5) var(--space-6);border-bottom:1px solid var(--hairline-soft); }
    .pcal h1 { font-size:calc(var(--font-ui) * 1.2);font-weight:var(--weight-medium,500);margin:0; }
    .pcal h2 { font-size:var(--font-ui);font-weight:var(--weight-medium,500);margin:0 0 var(--space-3); }
    .pcal p { margin:0 0 var(--space-3);overflow-wrap:anywhere; }
    .pcal-status { color:var(--ink-muted);padding:var(--space-3) var(--space-6);border-bottom:1px solid var(--hairline-soft); }
    .pcal-status p:last-child { margin-bottom:0; }
    .pcal-layout { flex:1;min-height:0;display:grid;grid-template-columns:240px minmax(0,1fr); }
    .pcal-list { padding:var(--space-3);border-right:1px solid var(--hairline-soft);overflow:auto; }
    .pcal-row { border:0;border-radius:var(--radius-sm);padding:var(--space-3);background:transparent;text-align:left;color:var(--ink);width:100%;display:flex;flex-direction:column;gap:var(--space-2);cursor:pointer;overflow-wrap:anywhere; }
    .pcal-row:hover { background:var(--surface-1); }
    .pcal-row[aria-current="true"] { background:var(--selection-surface); }
    .pcal-row span:last-child { color:var(--ink-muted); }
    .pcal-detail { min-width:0;overflow:auto;padding:var(--space-6); }
    .pcal-preview { width:min(100%,760px);margin-inline:auto; }
    .pcal-section { margin-bottom:var(--space-6); }
    .pcal dl { display:grid;grid-template-columns:minmax(90px,1fr) minmax(0,3fr);gap:var(--space-2) var(--space-4);margin:0; }
    .pcal dt { color:var(--ink-muted); }
    .pcal dd { margin:0;overflow-wrap:anywhere; }
    .pcal ul { margin:0;padding-left:var(--space-5); }
    .pcal-notice { border:1px solid var(--hairline);border-radius:var(--radius-sm);padding:var(--space-4);margin-bottom:var(--space-4); }
    .pcal-error { background:var(--danger-soft);padding:var(--space-3) var(--space-6);overflow-wrap:anywhere; }
    .pcal-actions { display:flex;flex-wrap:wrap;gap:var(--space-3);margin-block:var(--space-4); }
    .pcal pre { white-space:pre-wrap;overflow-wrap:anywhere;font:var(--font-mono-size,13px)/1.55 var(--font-mono,monospace); }
    .pcal summary { cursor:pointer;color:var(--ink-muted); }
    .pcal-empty { padding:var(--space-6);display:grid;place-items:center;flex:1; }
    @container (max-width:640px) { .pcal-layout { grid-template-columns:minmax(0,1fr);grid-template-rows:auto minmax(0,1fr); } .pcal-list { max-height:180px;border-right:0;border-bottom:1px solid var(--hairline-soft); } .pcal-detail,.pcal-head,.pcal-status { padding:var(--space-4); } .pcal dl { grid-template-columns:minmax(0,1fr);gap:var(--space-1); } .pcal dd { margin-bottom:var(--space-3); } }
  ` });
  const timeValid = value => value && typeof value.timezone === "string" && Boolean(value.date_time || value.date);
  const fieldsValid = (value, observed = false) => value && typeof value.summary === "string" && typeof value.location === "string" && timeValid(value.start) && timeValid(value.end) && Array.isArray(value.attendees) && value.attendees.every(person => typeof person.email === "string" && typeof person.optional === "boolean") && (observed || ["all", "externalOnly", "none"].includes(value.send_updates));
  const validPreview = op => op && op.user_space_id === "local" && ["create", "reschedule"].includes(op.kind)
    && ["operation_id", "account_id", "calendar_id", "agent_id", "session_id", "task_id", "event_id", "notification_notice"].every(key => typeof op[key] === "string" && op[key].length > 0)
    && Number.isInteger(op.revision) && op.revision > 0 && Number.isInteger(op.requirements_revision) && op.requirements_revision > 0
    && /^[a-f0-9]{64}$/.test(op.preview_digest || "") && fieldsValid(op.fields)
    && typeof op.availability?.checked_at === "string" && Array.isArray(op.availability?.conflicting_event_ids)
    && (op.kind !== "reschedule" || (typeof op.original_event_etag === "string" && fieldsValid(op.original_event?.fields, true) && op.original_event.etag === op.original_event_etag && op.original_event.id === op.event_id));
  function CalendarOperations({ locale, host, context }) {
    const t = key => (locale?.startsWith("zh") ? words.zh : words.en)[key] || key;
    const [status, setStatus] = React.useState(null), [operations, setOperations] = React.useState([]), [selected, setSelected] = React.useState(typeof context?.operationId === "string" ? context.operationId : "");
    const [loading, setLoading] = React.useState(true), [busy, setBusy] = React.useState(false), [error, setError] = React.useState("");
    const [reviewed, setReviewed] = React.useState(false), [cancelReview, setCancelReview] = React.useState(false);
    const mounted = React.useRef(true), loadVersion = React.useRef(0), isolationBlocked = React.useRef(false), mutating = React.useRef(false);
    // Context selects only an ID in the scoped ledger; it never supplies a proposal.
    const operation = operations.find(item => item.operation_id === selected);
    const report = cause => { if (mounted.current) { const message = cause instanceof Error ? cause.message : String(cause); isolationBlocked.current = message.includes("user_host_isolation_required:"); setError(message); } };
    const refresh = async () => {
      const version = ++loadVersion.current;
      try {
        const [nextStatus, result] = await Promise.all([call("status"), call("operations.list")]);
        if (!mounted.current || version !== loadVersion.current) return;
        isolationBlocked.current = false; setStatus(nextStatus); setOperations(result.operations || []); setError("");
      } catch (cause) { report(cause); }
      finally { if (mounted.current) setLoading(false); }
    };
    React.useEffect(() => { mounted.current = true; void refresh(); const timer = setInterval(() => { if (!isolationBlocked.current && !mutating.current) void refresh(); }, 15000); return () => { mounted.current = false; loadVersion.current++; clearInterval(timer); }; }, []);
    React.useEffect(() => { setReviewed(false); setCancelReview(false); }, [operation?.operation_id, operation?.revision, operation?.preview_digest, status?.read_only]);
    const mutate = async (method, input) => {
      if (mutating.current) return;
      mutating.current = true; loadVersion.current++; setBusy(true); setError(""); setReviewed(false); setCancelReview(false);
      try { const result = await call(method, input); if (mounted.current) setOperations(current => current.map(item => item.operation_id === result.operation_id ? result : item)); }
      catch (cause) { report(cause); }
      finally { mutating.current = false; if (mounted.current) setBusy(false); }
    };
    const fieldRows = (fields, observed = false) => {
      const time = value => [value?.date_time || value?.date, value?.timezone].filter(Boolean).join(" · ");
      return h("dl", null, h("dt", null, t("summary")), h("dd", null, fields?.summary), h("dt", null, t("location")), h("dd", null, fields?.location || t("none")), h("dt", null, t("start")), h("dd", null, time(fields?.start)), h("dt", null, t("end")), h("dd", null, time(fields?.end)), h("dt", null, t("attendees")), h("dd", null, fields?.attendees?.length ? h("ul", null, fields.attendees.map((person, index) => h("li", { key: index }, `${person.email} · ${t(person.optional ? "optional" : "required")}`))) : t("none")), !observed && h("dt", null, t("updates")), !observed && h("dd", null, fields?.send_updates || t("none")));
    };
    const canConfirm = status?.read_only === false && operation?.status === "proposed" && validPreview(operation);
    return h("div", { className: "pcal" }, h("header", { className: "pcal-head" }, h("h1", null, t("title")), h("div", { className: "pcal-actions" }, h(Button, { "data-action": "refresh", disabled: busy, onClick: refresh }, t("refresh")), h(Button, { "data-action": "close-calendar", onClick: () => { void host.closeView().catch(report); } }, t("close")))),
      status && h("section", { className: "pcal-status", "data-calendar-state": status.state }, h("p", null, t(status.state)), status.read_only && h("p", { role: "status" }, t("readOnly")), status.freshness === "stale" && h("p", null, t("stale")), status.error_code && h("p", { role: "alert" }, status.error_code)),
      error && h("div", { className: "pcal-error", role: "alert" }, error.includes("user_host_isolation_required:") ? t("unavailable") : error),
      loading ? h(LoadingState, { label: t("loading") }) : !operations.length ? h("div", { className: "pcal-empty" }, h(EmptyState, { title: t("empty"), description: t("emptyText") })) : h("div", { className: "pcal-layout" },
        h("nav", { className: "pcal-list", "aria-label": t("title") }, operations.map(item => h("button", { className: "pcal-row", key: item.operation_id, "data-operation-id": item.operation_id, "aria-current": item.operation_id === selected ? "true" : undefined, onClick: () => setSelected(item.operation_id) }, h("span", null, item.fields?.summary || item.operation_id), h("span", null, t(item.status))))),
        h("main", { className: "pcal-detail" }, !operation ? h(EmptyState, { title: t("inspect") }) : h("article", { className: "pcal-preview", "data-calendar-preview": true },
          h("section", { className: "pcal-section" }, h("h2", null, t(operation.kind)), h("p", { "data-operation-status": operation.status }, t(operation.status)), h("dl", null, h("dt", null, t("account")), h("dd", null, operation.account_id), h("dt", null, t("calendar")), h("dd", null, operation.calendar_id), h("dt", null, t("revision")), h("dd", null, operation.revision))),
          h("section", { className: "pcal-section" }, h("h2", null, t("proposedEvent")), fieldRows(operation.fields)),
          operation.original_event && h("section", { className: "pcal-section" }, h("h2", null, t("original")), fieldRows(operation.original_event.fields, true)),
          h("section", { className: "pcal-section" }, h("h2", null, t("availability")), h("p", null, `${t("checked")} · ${operation.availability?.checked_at || ""}`), h("p", null, t("scope")), h("p", null, `${t("conflicts")} · ${operation.availability?.conflicting_event_ids?.join(", ") || t("none")}`)),
          h("section", { className: "pcal-notice" }, h("h2", null, t("notifications")), h("p", null, operation.notification_notice)),
          h("details", { className: "pcal-section" }, h("summary", null, t("binding")), h("pre", null, JSON.stringify(operation, null, 2))),
          operation.error_code && h("p", { role: "alert" }, operation.error_code),
          (operation.reconciliation_required || operation.status === "uncertain") && h("p", { role: "status" }, t(operation.cancel_requested ? "cancelPending" : "uncertainText")),
          operation.status === "completed" && operation.result && h("section", { className: "pcal-section" }, h("h2", null, t("proof")), h("p", null, t("notificationUnverified")), h("pre", null, JSON.stringify(operation.result, null, 2))),
          operation.status === "proposed" && h(React.Fragment, null, !validPreview(operation) && h("p", { role: "alert" }, t("invalid")), h(Checkbox, { "data-action": "review-preview", label: t("review"), checked: reviewed, disabled: !canConfirm || busy, onChange: event => setReviewed(event.target.checked) }), h("div", { className: "pcal-actions" }, h(Button, { "data-action": "confirm", variant: "primary", disabled: !canConfirm || !reviewed || busy, onClick: () => { if (canConfirm && reviewed) void mutate("operations.confirm", { operation_id: operation.operation_id, expected_revision: operation.revision, preview_digest: operation.preview_digest }); } }, t(operation.kind === "create" ? "confirmCreate" : "confirmMove")))),
          h("div", { className: "pcal-actions" }, ["running", "uncertain"].includes(operation.status) && h(Button, { "data-action": "reconcile", disabled: busy || status?.read_only !== false, onClick: () => mutate("operations.reconcile", { operation_id: operation.operation_id }) }, t("reconcile")), ["proposed", "confirmed", "running", "uncertain"].includes(operation.status) && !operation.cancel_requested && h(Button, { "data-action": "cancel", disabled: busy, onClick: () => setCancelReview(true) }, t("cancel"))),
          cancelReview && h("section", { className: "pcal-notice", role: "alertdialog", "aria-label": t("cancelTitle") }, h("h2", null, t("cancelTitle")), h("p", null, t("cancelText")), h("div", { className: "pcal-actions" }, h(Button, { "data-action": "confirm-cancel", disabled: busy, onClick: () => mutate("operations.cancel", { operation_id: operation.operation_id, expected_revision: operation.revision }) }, t("confirmCancel")), h(Button, { onClick: () => setCancelReview(false) }, t("keep"))))))));
  }
  api.registerViewType({ id: "personal-calendar.confirmation", title: "Calendar", icon: "calendar", defaultRegion: "overlay", persistence: "session", layout: "user-space", render: props => h(CalendarOperations, { ...props, key: typeof props.context?.operationId === "string" ? props.context.operationId : "" }) });
}
