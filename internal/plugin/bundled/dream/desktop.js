export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const { Page, Section, Stack, Row, Button, Checkbox } = api.ui;
  api.registerLocale({ id: "dream-en", locale: "en-US", entries: {
    "dream.settings": "Background consolidation",
    "dream.settingsHelp": "Turns completed sessions into durable workspace memory while Wuu is open.",
    "dream.enabled": "Consolidate in the background",
    "dream.interval": "Days between runs",
    "dream.minimum": "Completed sessions before a run",
    "dream.model": "Model alias", "dream.modelDefault": "Default model",
    "dream.save": "Save changes", "dream.saved": "Saved", "dream.run": "Run now", "dream.activity": "Activity",
    "dream.candidates": "Pending sessions", "dream.status": "Last status",
    "dream.status.running": "Running", "dream.status.completed": "Completed",
    "dream.status.failed": "Failed", "dream.status.skipped": "Skipped"
  }});
  api.registerLocale({ id: "dream-zh", locale: "zh-CN", entries: {
    "dream.settings": "后台整合",
    "dream.settingsHelp": "Wuu 运行时，把已完成的会话整理为工作区长期记忆。",
    "dream.enabled": "在后台整合记忆",
    "dream.interval": "运行间隔（天）",
    "dream.minimum": "累计完成会话数",
    "dream.model": "模型别名", "dream.modelDefault": "默认模型",
    "dream.save": "保存更改", "dream.saved": "已保存", "dream.run": "立即运行", "dream.activity": "运行情况",
    "dream.candidates": "待整理会话", "dream.status": "上次状态",
    "dream.status.running": "运行中", "dream.status.completed": "已完成",
    "dream.status.failed": "运行失败", "dream.status.skipped": "已跳过"
  }});
  api.registerStyle({ id: "dream-settings", css: `
    /* One settings group: the label on the left, a compact control pinned
     * right, soft separators, the settings row height. */
    .plugin-dream { min-width:0; }
    .plugin-dream-rows, .plugin-dream-stats { display:flex; flex-direction:column; border:1px solid var(--hairline); border-radius:var(--radius-sm); padding:0 var(--card-padding); }
    .plugin-dream-row, .plugin-dream-stat, .plugin-dream-rows > .plugin-ui-checkbox { min-height:calc(var(--control-field-height) + var(--space-3) * 2); padding:var(--space-3) 0; border-bottom:1px solid var(--hairline-soft); }
    .plugin-dream-row:last-child, .plugin-dream-stat:last-child { border-bottom:0; }
    .plugin-dream-row { display:grid; grid-template-columns:minmax(0,1fr) auto; align-items:center; gap:var(--space-6); }
    .plugin-dream-rows .plugin-ui-field-label, .plugin-dream-row-title { color:var(--ink); font-size:var(--font-ui); font-weight:var(--weight-regular); line-height:1.4; }
    .plugin-dream-num { max-width:100%; width:6.5em; text-align:right; font-variant-numeric:tabular-nums; }
    .plugin-dream-model { width:12em; max-width:100%; }
    .plugin-dream-actions { justify-content:flex-start; align-items:center; gap:var(--space-2); }
    .plugin-dream-saved { color:var(--ink-tertiary); font-size:var(--font-sm); }
    .plugin-dream-stat { display:grid; grid-template-columns:minmax(0,1fr) auto; align-items:center; gap:var(--space-6); }
    .plugin-dream-stat-label { color:var(--ink); font-size:var(--font-ui); }
    .plugin-dream-stat-value { display:inline-flex; align-items:center; gap:var(--space-2); overflow-wrap:anywhere; color:var(--ink-soft); font-size:var(--font-ui); font-variant-numeric:tabular-nums; text-align:right; }
    .plugin-dream-status-dot { width:6px; height:6px; flex:none; border-radius:var(--radius-circle); background:var(--ink-faint); }
    .plugin-dream-status-dot[data-tone="ok"] { background:var(--success); }
    .plugin-dream-status-dot[data-tone="bad"] { background:var(--danger); }
    .plugin-dream-status-dot[data-tone="busy"] { background:var(--warning); }
    .plugin-dream-error { color:var(--danger); font-size:var(--font-ui); }
    .plugin-dream-loading { color:var(--ink-tertiary); font-size:var(--font-ui); }
    @container settings-page (max-width:480px) {
      .plugin-dream-row { grid-template-columns:minmax(0,1fr); gap:var(--space-2); }
      .plugin-dream-num, .plugin-dream-model { width:100%; text-align:left; }
      .plugin-dream-stat { grid-template-columns:minmax(0,1fr); gap:var(--space-1); }
      .plugin-dream-stat-value { text-align:left; }
    }
  `});
  function DreamSettings(props) {
    const tr = props.translate;
    const [state, setState] = React.useState(null);
    const [draft, setDraft] = React.useState(null);
    const [busy, setBusy] = React.useState(false);
    const [savedTick, setSavedTick] = React.useState(0);
    const [error, setError] = React.useState("");
    const refresh = React.useCallback(async () => {
      const value = await api.invokeRuntime("dream.get", {});
      setState(value);
      setDraft(value.settings);
    }, []);
    React.useEffect(() => { void refresh().catch((reason) => setError(String(reason))); }, [refresh]);
    const act = async (method, input, { markSaved = false } = {}) => {
      setBusy(true); setError("");
      try {
        const value = await api.invokeRuntime(method, input);
        setState(value);
        setDraft(value.settings);
        if (markSaved) setSavedTick((tick) => tick + 1);
      } catch (reason) { setError(String(reason)); } finally { setBusy(false); }
    };
    if (!draft) return h(Page, { className: "plugin-dream" }, h("div", { className: error ? "plugin-dream-error" : "plugin-dream-loading", role: error ? "alert" : "status" }, error || "…"));
    const number = (event) => Number.parseInt(event.target.value, 10) || 0;
    const candidateCount = Object.keys(state?.candidates || {}).length;
    const status = state?.last_status || "—";
    const statusLabel = ["running", "completed", "failed", "skipped"].includes(status) ? tr(`dream.status.${status}`) : status;
    const statusTone = state?.last_error ? "bad" : status === "running" ? "busy" : status === "completed" ? "ok" : "";
    // "Saved" stays until the next edit instead of fading on a timer.
    const edit = (patch) => { setSavedTick(0); setDraft({ ...draft, ...patch }); };
    const row = (label, control) => h("label", { className: "plugin-dream-row" }, h("span", { className: "plugin-dream-row-title" }, label), control);
    return h(Page, { className: "plugin-dream" }, h(Stack, { gap: "large" },
      h(Section, { title: tr("dream.settings"), description: tr("dream.settingsHelp") }, h("div", { className: "plugin-dream-rows" },
        h(Checkbox, { label: tr("dream.enabled"), checked: draft.enabled, disabled: busy, onChange: (event) => edit({ enabled: event.target.checked }) }),
        row(tr("dream.interval"), h("input", { className: "plugin-ui-input plugin-dream-num", type: "number", min: 1, max: 365, value: draft.interval_days, disabled: busy, onChange: (event) => edit({ interval_days: number(event) }) })),
        row(tr("dream.minimum"), h("input", { className: "plugin-ui-input plugin-dream-num", type: "number", min: 1, max: 100, value: draft.min_sessions, disabled: busy, onChange: (event) => edit({ min_sessions: number(event) }) })),
        row(tr("dream.model"), h("input", { className: "plugin-ui-input plugin-dream-model", type: "text", placeholder: tr("dream.modelDefault"), value: draft.model_alias || "", disabled: busy, onChange: (event) => edit({ model_alias: event.target.value }) })))),
      h(Row, { className: "plugin-dream-actions" },
        h(Button, { variant: "primary", disabled: busy, onClick: () => void act("dream.update", draft, { markSaved: true }) }, tr("dream.save")),
        h(Button, { disabled: busy || !draft.enabled || candidateCount === 0, onClick: () => void act("dream.run", {}) }, tr("dream.run")),
        savedTick ? h("span", { className: "plugin-dream-saved", role: "status" }, tr("dream.saved")) : null,
        error ? h("span", { className: "plugin-dream-error", role: "alert" }, error) : null),
      h(Section, { title: tr("dream.activity") }, h("div", { className: "plugin-dream-stats" },
        h("div", { className: "plugin-dream-stat" },
          h("span", { className: "plugin-dream-stat-label" }, tr("dream.candidates")),
          h("span", { className: "plugin-dream-stat-value" }, String(candidateCount))),
        h("div", { className: "plugin-dream-stat" },
          h("span", { className: "plugin-dream-stat-label" }, tr("dream.status")),
          h("span", { className: "plugin-dream-stat-value" },
            statusTone ? h("span", { className: "plugin-dream-status-dot", "data-tone": statusTone, "aria-hidden": true }) : null,
            `${statusLabel}${state?.last_error ? ` · ${state.last_error}` : ""}`))))
    ));
  }
  api.registerViewType({ id: "dream.settings", title: "Dream", icon: "moon", defaultRegion: "settings", persistence: "durable", render: (props) => h(DreamSettings, props) });
}
