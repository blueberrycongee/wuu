export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  api.registerLocale({ id: "subagent-en", locale: "en-US", entries: {
    "subagent.aliases.title": "Model aliases",
    "subagent.aliases.help": "A delegated task that names an alias runs on its model. Changes apply to new child tasks.",
    "subagent.aliases.label": "Aliases (JSON)",
    "subagent.aliases.save": "Save aliases",
    "subagent.aliases.saved": "Saved",
    "subagent.aliases.invalid": "Enter a unique alias, service, and model for every row.",
    "subagent.aliases.add": "Add alias", "subagent.aliases.remove": "Remove alias",
    "subagent.aliases.name": "Alias", "subagent.aliases.provider": "Service name",
    "subagent.aliases.model": "Model ID", "subagent.aliases.effort": "Reasoning effort (optional)",
    "subagent.aliases.variant": "Variant (optional)", "subagent.aliases.advanced": "Model options",
    "subagent.aliases.empty": "No aliases yet"
  } });
  api.registerLocale({ id: "subagent-zh", locale: "zh-CN", entries: {
    "subagent.aliases.title": "模型别名",
    "subagent.aliases.help": "委派的子任务按别名选用模型，保存后对新建的子任务生效。",
    "subagent.aliases.label": "别名配置（JSON）",
    "subagent.aliases.save": "保存别名",
    "subagent.aliases.saved": "已保存",
    "subagent.aliases.invalid": "请为每行填写不重复的别名、服务和模型。",
    "subagent.aliases.add": "添加别名", "subagent.aliases.remove": "删除别名",
    "subagent.aliases.name": "别名", "subagent.aliases.provider": "服务名称",
    "subagent.aliases.model": "模型 ID", "subagent.aliases.effort": "思考强度（可选）",
    "subagent.aliases.variant": "模型变体（可选）", "subagent.aliases.advanced": "模型选项",
    "subagent.aliases.empty": "还没有模型别名"
  } });
  api.registerStyle({ id: "subagent-status", css: `
    .plugin-subagent-settings { min-width:0; }
    .plugin-subagent-aliases { border:1px solid var(--hairline); border-radius:var(--radius-sm); padding:0 var(--card-padding); }
    .plugin-subagent-alias { min-width:0; padding:var(--space-4) 0; border-bottom:1px solid var(--hairline-soft); }
    .plugin-subagent-alias:last-child { border-bottom:0; }
    .plugin-subagent-fields { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:var(--space-3); }
    .plugin-subagent-fields .plugin-ui-field-label { font-weight:var(--weight-regular); }
    .plugin-subagent-options { margin-top:var(--space-3); color:var(--ink-soft); font-size:var(--font-sm); }
    .plugin-subagent-options summary { cursor:pointer; margin-bottom:var(--space-3); }
    .plugin-subagent-alias-footer { display:flex; justify-content:space-between; align-items:baseline; gap:var(--space-3); }
    @container settings-page (max-width:560px) { .plugin-subagent-fields { grid-template-columns:minmax(0,1fr); } }
    .plugin-subagent-actions { align-items:center; gap:var(--space-2); }
    .plugin-subagent-saved { color:var(--ink-tertiary); font-size:var(--font-sm); }
    .plugin-subagent-settings-error { color:var(--danger); font-size:var(--font-ui); overflow-wrap:anywhere; }
  ` });

  const emptyStatusItems = Object.freeze([]);
  const statusItemsByThread = new Map();
  const statusListenersByThread = new Map();
  const refreshGenerationByThread = new Map();
  const statusState = (state) => {
    if (state === "running") return "running";
    if (state === "queued" || state === "pending") return "queued";
    if (state === "failed") return "error";
    if (state === "waiting_children" || state === "awaiting_report") return "waiting";
    return "idle";
  };
  const refreshStatus = async (threadId) => {
    if (!threadId) return;
    const generation = (refreshGenerationByThread.get(threadId) || 0) + 1;
    refreshGenerationByThread.set(threadId, generation);
    let tasks = [];
    try {
      const value = await api.invokeRuntime("status.list", { parent_session_id: threadId });
      tasks = Array.isArray(value?.sessions) ? value.sessions : [];
    } catch {
      tasks = [];
    }
    if (refreshGenerationByThread.get(threadId) !== generation) return;
    const items = tasks
      .filter((task) => task && task.session_id && task.state && task.state !== "completed")
      .map((task) => Object.freeze({
        id: task.session_id,
        label: task.name || "Child task",
        state: statusState(task.state),
        secondaryText: String(task.state).replaceAll("_", " "),
        tooltip: task.name || "Child task",
        updatedAt: typeof task.updated_at === "string" ? task.updated_at : undefined,
        action: Object.freeze({ kind: "open-session", sessionId: task.session_id }),
      }));
    statusItemsByThread.set(threadId, Object.freeze(items));
    for (const listener of statusListenersByThread.get(threadId) || []) listener();
  };
  api.onHostEvent((event) => {
    const method = event && typeof event === "object" && event.message && typeof event.message === "object" ? event.message.method : "";
    if (typeof method === "string" && [
      "thread/started", "thread/resumed", "thread/updated", "turn/started", "turn/queued",
      "turn/dequeued", "turn/held", "turn/completed", "turn/error",
    ].includes(method)) {
      for (const threadId of statusListenersByThread.keys()) void refreshStatus(threadId);
    }
  });
  api.registerComposerStatusSource({
    id: "subagent-status",
    order: 30,
    getSnapshot: ({ threadId }) => threadId ? statusItemsByThread.get(threadId) || emptyStatusItems : emptyStatusItems,
    subscribe: ({ threadId }, listener) => {
      if (!threadId) return () => {};
      let listeners = statusListenersByThread.get(threadId);
      if (!listeners) {
        listeners = new Set();
        statusListenersByThread.set(threadId, listeners);
      }
      listeners.add(listener);
      void refreshStatus(threadId);
      return () => {
        listeners.delete(listener);
        if (listeners.size === 0) {
          statusListenersByThread.delete(threadId);
          refreshGenerationByThread.set(threadId, (refreshGenerationByThread.get(threadId) || 0) + 1);
        }
      };
    },
  });

  function ModelAliases({ host, translate }) {
    const { Page, Section, Stack, Row, Button, TextInput, EmptyState } = api.ui;
    const tr = typeof translate === "function" ? translate : (key) => key;
    const settings = host.settings;
    const aliases = settings ? settings.getValue("runtime.modelAliases") : {};
    const nextId = React.useRef(0);
    const toRows = () => Object.entries(aliases).map(([name, value]) => ({ ...value, name, id: nextId.current++ }));
    const [rows, setRows] = React.useState(toRows);
    const [error, setError] = React.useState("");
    const [busy, setBusy] = React.useState(false);
    const [saved, setSaved] = React.useState(false);
    React.useEffect(() => { setRows(toRows()); }, [JSON.stringify(aliases)]);
    const edit = (id, key, value) => {
      setRows((current) => current.map((row) => row.id === id ? { ...row, [key]: value } : row));
      setSaved(false);
    };
    const save = async () => {
      setBusy(true); setError(""); setSaved(false);
      try {
        const names = rows.map((row) => row.name.trim());
        if (new Set(names).size !== names.length || rows.some((row) => !row.name.trim() || !row.provider.trim() || !row.model.trim())) {
          throw new Error(tr("subagent.aliases.invalid"));
        }
        const value = Object.fromEntries(rows.map(({ id, name, ...model }) => [name.trim(), {
          ...model, provider: model.provider.trim(), model: model.model.trim(),
        }]));
        if (!settings) throw new Error("Settings update is unavailable.");
        await settings.updateValue("runtime.modelAliases", value);
        setSaved(true);
      } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
      finally { setBusy(false); }
    };
    const field = (row, key, placeholder) => h(TextInput, {
      label: tr(`subagent.aliases.${key}`), value: row[key] || "", placeholder,
      disabled: busy, onChange: (event) => edit(row.id, key, event.target.value),
    });
    return h(Page, { className: "plugin-subagent-settings" }, h(Stack, { gap: "large" },
      h(Section, { title: tr("subagent.aliases.title"), description: tr("subagent.aliases.help") }, h(Stack, null,
        rows.length ? h("div", { className: "plugin-subagent-aliases" }, rows.map((row) =>
          h("div", { className: "plugin-subagent-alias", key: row.id },
            h("div", { className: "plugin-subagent-fields" }, field(row, "name", "research"), field(row, "provider"), field(row, "model")),
            h("div", { className: "plugin-subagent-alias-footer" },
              h("details", { className: "plugin-subagent-options" }, h("summary", null, tr("subagent.aliases.advanced")),
                h(Stack, null, field(row, "effort"), field(row, "variant"))),
              h(Button, { variant: "ghost", disabled: busy, onClick: () => { setRows((current) => current.filter((item) => item.id !== row.id)); setSaved(false); } }, tr("subagent.aliases.remove"))))))
          : h(EmptyState, { title: tr("subagent.aliases.empty") }),
        h(Row, { className: "plugin-subagent-actions" },
          h(Button, { disabled: busy, onClick: () => { setRows((current) => [...current, { id: nextId.current++, name: "", provider: "", model: "" }]); setSaved(false); } }, tr("subagent.aliases.add")),
          h(Button, { variant: "primary", disabled: busy, onClick: () => void save() }, tr("subagent.aliases.save")),
          saved ? h("span", { className: "plugin-subagent-saved", role: "status" }, tr("subagent.aliases.saved")) : null),
        error ? h("div", { className: "plugin-subagent-settings-error", role: "alert" }, error) : null))));
  }
  api.registerViewType({ id: "subagent.settings", title: "Subagent", icon: "bot", defaultRegion: "settings", persistence: "durable", render: ModelAliases });
}
