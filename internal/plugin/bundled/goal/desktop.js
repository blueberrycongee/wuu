export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const { Button, TextArea, ComposerDrawer } = api.ui;
  api.registerLocale({ id: "goal-en", locale: "en-US", entries: {
    "goal.status.active": "Active", "goal.status.paused": "Paused", "goal.status.blocked": "Blocked", "goal.status.complete": "Complete",
    "goal.label": "Session goal", "goal.expand": "Show goal", "goal.collapse": "Hide goal", "goal.set": "Set a goal",
    "goal.pause": "Pause", "goal.resume": "Resume", "goal.clear": "Clear goal", "goal.end": "End goal",
    "goal.objective": "Goal", "goal.next": "Next goal", "goal.start": "Start goal",
    "goal.usage": "{tokens} tokens · {duration}",
    "goal.duration.second": "{count}s", "goal.duration.minute": "{count}m", "goal.duration.hour": "{count}h",
  } });
  api.registerLocale({ id: "goal-zh", locale: "zh-CN", entries: {
    "goal.status.active": "进行中", "goal.status.paused": "已暂停", "goal.status.blocked": "受阻", "goal.status.complete": "已完成",
    "goal.label": "会话目标", "goal.expand": "展开目标", "goal.collapse": "收起目标", "goal.set": "设置目标",
    "goal.pause": "暂停", "goal.resume": "继续", "goal.clear": "清除目标", "goal.end": "结束目标",
    "goal.objective": "目标", "goal.next": "新目标", "goal.start": "开始目标",
    "goal.usage": "{tokens} tokens · 用时 {duration}",
    "goal.duration.second": "{count} 秒", "goal.duration.minute": "{count} 分", "goal.duration.hour": "{count} 小时",
  } });
  // The two largest units, as the host words turn durations: 9 秒, 1 分 30 秒, 2 小时 5 分.
  function formatDuration(seconds, tr) {
    const total = Math.max(0, Math.floor(seconds));
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const rest = total % 60;
    const unit = (count, name) => tr(`goal.duration.${name}`, { count });
    if (hours > 0) return [unit(hours, "hour"), minutes > 0 ? unit(minutes, "minute") : ""].filter(Boolean).join(" ");
    if (minutes > 0) return [unit(minutes, "minute"), rest > 0 ? unit(rest, "second") : ""].filter(Boolean).join(" ");
    return unit(rest, "second");
  }
  const statusKeys = new Set(["active", "paused", "blocked", "complete"]);
  const empty = Object.freeze({ goal: null, error: "", loaded: false });
  const cache = new Map();
  const listeners = new Map();
  const revisions = new Map();
  let disposed = false;
  function publish(id, value) {
    cache.set(id, value);
    for (const listener of listeners.get(id) || []) listener();
  }
  async function refresh(id) {
    if (!id || disposed) return;
    const revision = (revisions.get(id) || 0) + 1;
    revisions.set(id, revision);
    try {
      const value = await api.invokeRuntime("get_goal", { thread_id: id });
      if (disposed || revisions.get(id) !== revision) return;
      const goal = value?.goal || null;
      publish(id, { goal, error: "", loaded: true });
    } catch (error) {
      if (!disposed && revisions.get(id) === revision) {
        publish(id, { ...(cache.get(id) || empty), loaded: true, error: String(error.message || error) });
      }
    }
  }
  function subscribe(id, listener) {
    if (!id) return () => {};
    if (!listeners.has(id)) listeners.set(id, new Set());
    listeners.get(id).add(listener);
    void refresh(id);
    return () => {
      const current = listeners.get(id);
      current?.delete(listener);
      if (current?.size === 0) listeners.delete(id);
    };
  }
  // Observer writes can settle after the host's terminal notification.
  const timer = setInterval(() => { for (const id of listeners.keys()) void refresh(id); }, 1500);
  api.registerCleanup(() => { disposed = true; clearInterval(timer); listeners.clear(); cache.clear(); });
  api.onHostEvent((event) => {
    const method = event?.message?.method;
    if (["thread/started", "thread/resumed", "turn/started", "turn/completed", "turn/error"].includes(method)) {
      for (const id of listeners.keys()) void refresh(id);
    }
  });
  function GoalIcon() {
    return h("svg", { width: 16, height: 16, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.75, "aria-hidden": true },
      h("circle", { cx: 12, cy: 12, r: 9 }), h("circle", { cx: 12, cy: 12, r: 4 }), h("circle", { cx: 12, cy: 12, r: 1, fill: "currentColor", stroke: "none" }));
  }
  function ActionIcon({ kind }) {
    const paths = { send: "M12 19V5m-6 6 6-6 6 6", pause: "M9 5v14M15 5v14", resume: "m8 5 11 7-11 7Z", end: "m6 6 12 12M18 6 6 18" };
    return h("svg", { width: 14, height: 14, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.75, strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": true }, h("path", { d: paths[kind] }));
  }
  function Controls({ threadId, readOnly, tr, locale }) {
    const state = React.useSyncExternalStore(
      React.useCallback((listener) => subscribe(threadId, listener), [threadId]),
      React.useCallback(() => cache.get(threadId) || empty, [threadId]),
    );
    const [objective, setObjective] = React.useState("");
    const [busy, setBusy] = React.useState(false);
    const [error, setError] = React.useState("");
    const [open, setOpen] = React.useState(false);
    const goal = state.goal;
    const canCreate = !goal || goal.status === "complete";
    const active = goal?.status === "active";
    const disabled = busy || readOnly;
    function close() {
      setOpen(false);
    }
    async function act(method) {
      if (disabled) return;
      setBusy(true); setError("");
      try {
        const input = { thread_id: threadId };
        if (method === "create_goal") input.objective = objective.trim();
        await api.invokeRuntime(method, input);
        if (method === "create_goal" || method === "clear") setObjective("");
        await refresh(threadId);
        if (method === "create_goal" || method === "clear") close();
      } catch (cause) { setError(String(cause.message || cause)); }
      finally { setBusy(false); }
    }
    const visibleError = error || state.error || goal?.error;
    if (!goal && !open) return null;
    return h(ComposerDrawer, {
      className: "plugin-goal",
      "aria-label": tr("goal.label"),
      expanded: open,
      onExpandedChange: setOpen,
      toggleLabel: tr(open ? "goal.collapse" : "goal.expand"),
      tone: !goal || goal.status === "paused" || goal.status === "complete" ? "muted" : goal.status === "blocked" ? "warning" : "default",
      icon: h(GoalIcon),
      notice: visibleError ? h("p", { className: "plugin-goal-error", role: "alert" }, visibleError) : null,
      summary: h(React.Fragment, null,
        h("span", { className: "plugin-goal-label", "data-status": goal?.status }, goal ? (statusKeys.has(goal.status) ? tr(`goal.status.${goal.status}`) : goal.status) : tr("goal.set")),
        goal && !open ? h("span", { className: "plugin-goal-summary", title: goal.objective }, goal.objective) : null,
        goal && !open ? h("span", { className: "plugin-goal-status" }, `· ${formatDuration(goal.time_used_seconds, tr)}`) : null,
      ),
      actions: h(React.Fragment, null,
        goal && !canCreate ? h(Button, { variant: "ghost", className: "plugin-goal-icon-button", "aria-label": tr(active ? "goal.pause" : "goal.resume"), title: tr(active ? "goal.pause" : "goal.resume"), disabled, onClick: () => void act(active ? "pause" : "resume") }, h(ActionIcon, { kind: active ? "pause" : "resume" })) : null,
        goal ? h(Button, { variant: "ghost", className: "plugin-goal-icon-button", "aria-label": tr(canCreate ? "goal.clear" : "goal.end"), title: tr(canCreate ? "goal.clear" : "goal.end"), disabled, onClick: () => void act("clear") }, h(ActionIcon, { kind: "end" })) : null,
      ),
    },
      open ? h("div", { className: "plugin-goal-details" },
        goal ? h("div", { className: "plugin-goal-progress" },
          h("p", { className: "plugin-goal-objective" }, goal.objective),
          h("p", { className: "plugin-goal-usage" }, tr("goal.usage", { tokens: new Intl.NumberFormat(locale).format(goal.tokens_used || 0), duration: formatDuration(goal.time_used_seconds, tr) })),
        ) : null,
        canCreate ? h("form", { onSubmit: (event) => {
          event.preventDefault();
          if (!disabled && state.loaded && objective.trim()) void act("create_goal");
        } },
          h(TextArea, { label: tr(goal ? "goal.next" : "goal.objective"), className: "plugin-goal-input", "aria-label": tr(goal ? "goal.next" : "goal.objective"), value: objective, rows: 2, autoFocus: true, disabled,
            onChange: (event) => setObjective(event.target.value),
            onKeyDown: (event) => {
              if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && !event.nativeEvent.isComposing) {
                event.preventDefault(); event.currentTarget.form.requestSubmit();
              }
            },
          }),
          h("div", { className: "plugin-goal-footer" },
            h("span", { className: "plugin-goal-spacer" }),
            h(Button, { variant: "primary", type: "submit", className: "plugin-goal-submit", "aria-label": tr("goal.start"), title: tr("goal.start"), disabled: disabled || !state.loaded || !objective.trim() }, h(ActionIcon, { kind: "send" })),
          ),
        ) : null,
      ) : null,
    );
  }
  api.registerSlot("composer.above", {
    id: "goal-controls", title: "目标", order: 20,
    render: ({ threadId, mainConversation, readOnly, translate, locale }) => threadId && mainConversation
      ? h(Controls, { key: threadId, threadId, readOnly, tr: translate, locale }) : null,
  });
  api.registerStyle({ id: "goal-controls", css: `
    /* The drawer's tone already marks a blocked goal on its symbol, so the
     * label stays ink; status color on small text would miss contrast. */
    .plugin-goal-label { flex-shrink:0; font-weight:var(--weight-medium); }
    .plugin-goal-summary { flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:var(--ink-soft); }
    .plugin-goal-status { flex-shrink:0; color:var(--ink-muted); font-variant-numeric:tabular-nums; }
    .plugin-goal-details { display:grid; gap:var(--space-2); padding:var(--space-1) var(--space-3) var(--space-3); }
    .plugin-goal-progress { display:grid; gap:var(--space-1); }
    .plugin-goal-objective { margin:0; white-space:pre-wrap; overflow-wrap:anywhere; }
    .plugin-goal-usage { margin:0; color:var(--ink-soft); font-variant-numeric:tabular-nums; }
    .plugin-goal-details form { display:grid; gap:var(--space-2); }
    .plugin-goal-details .plugin-ui-field-label { position:absolute; width:1px; height:1px; overflow:hidden; clip-path:inset(50%); }
    .plugin-goal .plugin-goal-input { field-sizing:content; min-height:48px; max-height:160px; resize:none; padding:var(--space-2) 0; border:0; border-radius:0; background:transparent; box-shadow:none; }
    .plugin-goal .plugin-goal-input:focus-visible { outline:none; box-shadow:none; }
    /* Drawer actions match the host's composer drawer actions; the submit
     * echoes the composer's round send button. */
    .plugin-goal .plugin-goal-icon-button { display:inline-grid; place-items:center; flex-shrink:0; width:28px; height:28px; min-height:28px; padding:0; border-radius:var(--radius-sm); color:var(--ink-muted); }
    .plugin-goal .plugin-goal-icon-button:not(:disabled):hover { color:var(--ink); }
    .plugin-goal .plugin-goal-submit { display:inline-grid; place-items:center; width:28px; height:28px; min-height:28px; padding:0; border-radius:var(--radius-circle); }
    .plugin-goal-footer { display:flex; align-items:center; gap:var(--space-2); }
    .plugin-goal-spacer { flex:1; }
    .plugin-goal-error { margin:0; padding:var(--space-2) var(--space-3) var(--space-3); color:var(--danger); overflow-wrap:anywhere; }
    @media (max-width:480px) { .plugin-goal-status { display:none; } }
    @media (pointer:coarse) { .plugin-goal .plugin-goal-icon-button, .plugin-goal .plugin-goal-submit { width:44px; height:44px; } }
  ` });
}
