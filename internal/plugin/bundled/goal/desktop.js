export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const { Button, TextArea, ComposerDrawer } = api.ui;
  const labels = { active: "进行中", paused: "已暂停", blocked: "受阻", complete: "已完成" };
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
    return h("svg", { width: 16, height: 16, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.7, "aria-hidden": true },
      h("circle", { cx: 12, cy: 12, r: 9 }), h("circle", { cx: 12, cy: 12, r: 4 }), h("circle", { cx: 12, cy: 12, r: 1, fill: "currentColor", stroke: "none" }));
  }
  function ActionIcon({ kind }) {
    const paths = { send: "M12 19V5m-6 6 6-6 6 6", pause: "M9 5v14M15 5v14", resume: "m8 5 11 7-11 7Z", end: "m6 6 12 12M18 6 6 18" };
    return h("svg", { width: 18, height: 18, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.8, strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": true }, h("path", { d: paths[kind] }));
  }
  function Controls({ threadId, readOnly }) {
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
      "aria-label": "会话目标",
      expanded: open,
      onExpandedChange: setOpen,
      toggleLabel: open ? "收起目标" : "展开目标",
      tone: !goal || goal.status === "paused" || goal.status === "complete" ? "muted" : goal.status === "blocked" ? "warning" : "default",
      icon: h(GoalIcon),
      notice: visibleError ? h("p", { className: "plugin-goal-error", role: "alert" }, visibleError) : null,
      summary: h(React.Fragment, null,
        h("span", { className: "plugin-goal-label", "data-status": goal?.status }, goal ? labels[goal.status] || goal.status : "设置目标"),
        goal && !open ? h("span", { className: "plugin-goal-summary", title: goal.objective }, goal.objective) : null,
        goal && !open ? h("span", { className: "plugin-goal-status" }, `· ${Math.floor(goal.time_used_seconds)} 秒`) : null,
      ),
      actions: h(React.Fragment, null,
        goal && !canCreate ? h(Button, { variant: "ghost", className: "plugin-goal-icon-button", "aria-label": active ? "暂停" : "继续", title: active ? "暂停" : "继续", disabled, onClick: () => void act(active ? "pause" : "resume") }, h(ActionIcon, { kind: active ? "pause" : "resume" })) : null,
        goal ? h(Button, { variant: "ghost", className: "plugin-goal-icon-button", "aria-label": canCreate ? "清除目标" : "结束目标", title: canCreate ? "清除目标" : "结束目标", disabled, onClick: () => void act("clear") }, h(ActionIcon, { kind: "end" })) : null,
      ),
    },
      open ? h("div", { className: "plugin-goal-details" },
        goal ? h("div", { className: "plugin-goal-progress" },
          h("p", { className: "plugin-goal-objective" }, goal.objective),
          h("p", { className: "plugin-goal-usage" }, `${goal.tokens_used} tokens · ${Math.floor(goal.time_used_seconds)} 秒`),
        ) : null,
        canCreate ? h("form", { onSubmit: (event) => {
          event.preventDefault();
          if (!disabled && state.loaded && objective.trim()) void act("create_goal");
        } },
          h(TextArea, { label: goal ? "新目标" : "目标", className: "plugin-goal-input", "aria-label": goal ? "新目标" : "目标", value: objective, rows: 2, autoFocus: true, disabled,
            onChange: (event) => setObjective(event.target.value),
            onKeyDown: (event) => {
              if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && !event.nativeEvent.isComposing) {
                event.preventDefault(); event.currentTarget.form.requestSubmit();
              }
            },
          }),
          h("div", { className: "plugin-goal-footer" },
            h("span", { className: "plugin-goal-spacer" }),
            h(Button, { variant: "primary", type: "submit", className: "plugin-goal-icon-button", "aria-label": "开始目标", title: "开始目标", disabled: disabled || !state.loaded || !objective.trim() }, h(ActionIcon, { kind: "send" })),
          ),
        ) : null,
      ) : null,
    );
  }
  api.registerSlot("composer.above", {
    id: "goal-controls", title: "目标", order: 20,
    render: ({ threadId, mainConversation, readOnly }) => threadId && mainConversation
      ? h(Controls, { key: threadId, threadId, readOnly }) : null,
  });
  api.registerStyle({ id: "goal-controls", css: `
    .plugin-goal-label { font-weight:500; flex-shrink:0; }
    .plugin-goal-label[data-status="blocked"] { color:var(--warning); }
    .plugin-goal-summary { flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:var(--ink-soft); }
    .plugin-goal-status { flex-shrink:0; font-size:var(--font-sm,12px); color:var(--ink-muted); }
    .plugin-goal-details { display:grid; gap:10px; padding:4px 12px 12px; }
    .plugin-goal-progress { display:grid; gap:6px; }
    .plugin-goal-objective { margin:0; white-space:pre-wrap; overflow-wrap:anywhere; }
    .plugin-goal-usage { margin:0; color:var(--ink-soft); font-size:var(--font-sm,12px); }
    .plugin-goal-details form { display:grid; gap:10px; }
    .plugin-goal-details .plugin-ui-field-label { position:absolute; width:1px; height:1px; overflow:hidden; clip-path:inset(50%); }
    .plugin-goal .plugin-goal-input { field-sizing:content; min-height:48px; max-height:160px; resize:none; padding:8px 0; border:0; border-radius:0; background:transparent; box-shadow:none; }
    .plugin-goal .plugin-goal-input:focus-visible { outline:none; box-shadow:none; }
    .plugin-goal-footer .plugin-ui-button { min-height:30px; border-radius:16px; font-size:var(--font-sm,12px); }
    .plugin-goal .plugin-goal-icon-button { display:inline-flex; align-items:center; justify-content:center; flex-shrink:0; width:28px; height:28px; min-height:28px; padding:0; border-radius:50%; }
    .plugin-goal-footer { display:flex; align-items:center; flex-wrap:wrap; gap:6px; }
    .plugin-goal-spacer { flex:1; }
    .plugin-goal-error { margin:0; padding:8px 16px 12px; color:var(--danger); overflow-wrap:anywhere; }
    @media (max-width:480px) { .plugin-goal-status { display:none; } }
    @media (pointer:coarse) { .plugin-goal .plugin-ui-button { min-height:44px; } }
    @media (pointer:coarse) { .plugin-goal .plugin-goal-icon-button { width:44px; height:44px; } }
  ` });
}
