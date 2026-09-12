export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const { Button, TextArea } = api.ui;
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
  function Controls({ threadId, readOnly }) {
    const state = React.useSyncExternalStore(
      React.useCallback((listener) => subscribe(threadId, listener), [threadId]),
      React.useCallback(() => cache.get(threadId) || empty, [threadId]),
    );
    const [objective, setObjective] = React.useState("");
    const [busy, setBusy] = React.useState(false);
    const [error, setError] = React.useState("");
    const [open, setOpen] = React.useState(false);
    const triggerRef = React.useRef(null);
    const panelId = React.useId();
    const goal = state.goal;
    const canCreate = !goal || goal.status === "complete";
    const active = goal?.status === "active";
    const disabled = busy || readOnly;
    function close() {
      setOpen(false);
      triggerRef.current?.focus();
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
    return h("section", {
      className: `plugin-goal${goal || open || visibleError ? " is-card" : ""}`,
      "aria-label": "会话目标",
      onKeyDown: (event) => {
        if (event.key === "Escape" && open) { event.stopPropagation(); close(); }
      },
    },
      h("div", { className: "plugin-goal-bar" },
        h("button", {
          type: "button", className: "plugin-goal-trigger", ref: triggerRef,
          "aria-expanded": open, "aria-controls": open ? panelId : undefined,
          onClick: () => setOpen((current) => !current),
        },
          h(GoalIcon),
          h("span", { className: "plugin-goal-label" }, goal ? "目标" : "设置目标"),
          goal ? h("span", { className: "plugin-goal-summary", title: goal.objective }, goal.objective) : null,
          goal ? h("span", { className: "plugin-goal-status", "data-status": goal.status }, labels[goal.status] || goal.status) : null,
          h("svg", { className: "plugin-goal-chevron", width: 14, height: 14, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.7, "aria-hidden": true }, h("path", { d: open ? "m6 15 6-6 6 6" : "m6 9 6 6 6-6" })),
        ),
        goal && !canCreate ? h(Button, { variant: "ghost", className: "plugin-goal-action", disabled, onClick: () => void act(active ? "pause" : "resume") }, active ? "暂停" : "继续") : null,
      ),
      open ? h("div", { id: panelId, className: "plugin-goal-details" },
        goal ? h("div", { className: "plugin-goal-progress" },
          h("p", { className: "plugin-goal-objective" }, goal.objective),
          h("p", { className: "plugin-goal-usage", title: "Token 用量在当前回合结束后更新，包含输入和输出。" }, `已结算 ${goal.tokens_used} tokens · ${Math.floor(goal.time_used_seconds)} 秒`),
        ) : null,
        canCreate ? h("form", { onSubmit: (event) => {
          event.preventDefault();
          if (!disabled && state.loaded && objective.trim()) void act("create_goal");
        } },
          h("p", { className: "plugin-goal-hint" }, "写下完成条件，Wuu 会跨回合持续推进。"),
          h(TextArea, { label: goal ? "新目标" : "目标", className: "plugin-goal-input", "aria-label": goal ? "新目标" : "目标", value: objective, rows: 3, autoFocus: true, placeholder: "你希望完成什么？", disabled,
            onChange: (event) => setObjective(event.target.value),
            onKeyDown: (event) => {
              if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && !event.nativeEvent.isComposing) {
                event.preventDefault(); event.currentTarget.form.requestSubmit();
              }
            },
          }),
          h("div", { className: "plugin-goal-footer" },
            !state.loaded ? h("span", { className: "plugin-goal-hint" }, "正在读取目标…") : null,
            goal ? h(Button, { variant: "ghost", disabled, onClick: () => void act("clear") }, "清除目标") : null,
            h("span", { className: "plugin-goal-spacer" }),
            h(Button, { variant: "ghost", onClick: close }, "取消"),
            h(Button, { variant: "primary", type: "submit", disabled: disabled || !state.loaded || !objective.trim() }, "开始目标"),
          ),
        ) : h("div", { className: "plugin-goal-footer" },
          h(Button, { variant: "ghost", disabled, onClick: () => void act("clear") }, "清除目标"),
        ),
      ) : null,
      visibleError ? h("p", { className: "plugin-goal-error", role: "alert" }, visibleError) : null,
    );
  }
  api.registerSlot("composer.above", {
    id: "goal-controls", title: "目标", order: 20,
    render: ({ threadId, mainConversation, readOnly }) => threadId && mainConversation
      ? h(Controls, { key: threadId, threadId, readOnly }) : null,
  });
  api.registerStyle({ id: "goal-controls", css: `
    .plugin-goal { min-width:0; margin:0 0 8px; color:var(--ink); font-size:var(--font-ui,13px); line-height:1.5; }
    .plugin-goal.is-card { border:1px solid var(--hairline); border-radius:16px; background:var(--paper); box-shadow:var(--shadow-soft); }
    .plugin-goal-bar { display:flex; align-items:center; gap:4px; min-width:0; padding:3px 8px; }
    .plugin-goal-trigger { display:flex; align-items:center; gap:8px; flex:1; min-width:0; min-height:32px; padding:4px; border:0; border-radius:8px; background:transparent; color:var(--ink-soft); font:inherit; text-align:left; cursor:pointer; }
    .plugin-goal:not(.is-card) .plugin-goal-trigger { flex:0 1 auto; }
    .plugin-goal-trigger:hover { color:var(--ink-strong); background:var(--surface-1); }
    .plugin-goal-trigger:focus-visible { outline:2px solid var(--focus-ring); outline-offset:2px; }
    .plugin-goal-trigger svg, .plugin-goal-label, .plugin-goal-status { flex-shrink:0; }
    .plugin-goal-label { font-weight:500; }
    .plugin-goal-summary { flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:var(--ink); }
    .plugin-goal-status { font-size:var(--font-sm,12px); color:var(--ink-muted); }
    .plugin-goal-status[data-status="active"] { color:var(--success); }
    .plugin-goal-status[data-status="blocked"] { color:var(--warning); }
    .plugin-goal .plugin-goal-action { flex-shrink:0; min-height:30px; padding:3px 8px; }
    .plugin-goal-details { display:grid; gap:12px; max-height:min(360px,45dvh); overflow-y:auto; padding:12px 16px 14px; border-top:1px solid var(--hairline-soft); }
    .plugin-goal-progress { display:grid; gap:6px; }
    .plugin-goal-objective { margin:0; white-space:pre-wrap; overflow-wrap:anywhere; }
    .plugin-goal-usage, .plugin-goal-hint { margin:0; color:var(--ink-soft); font-size:var(--font-sm,12px); }
    .plugin-goal-details form { display:grid; gap:10px; }
    .plugin-goal-details .plugin-ui-field-label { position:absolute; width:1px; height:1px; overflow:hidden; clip-path:inset(50%); }
    .plugin-goal .plugin-goal-input { min-height:80px; max-height:160px; resize:vertical; border-radius:10px; }
    .plugin-goal-footer { display:flex; align-items:center; flex-wrap:wrap; gap:6px; }
    .plugin-goal-spacer { flex:1; }
    .plugin-goal-error { margin:0; padding:8px 16px 12px; color:var(--danger); overflow-wrap:anywhere; }
    @media (pointer:coarse) { .plugin-goal-trigger, .plugin-goal .plugin-ui-button { min-height:44px; } }
  ` });
}
