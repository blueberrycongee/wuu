export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  api.registerLocale({ id: "subagent-en", locale: "en-US", entries: {
    "subagent.aliases.title": "Model aliases",
    "subagent.aliases.help": "The agent picks a model by alias when delegating a task. Next message and Dream can use aliases too.",
    "subagent.aliases.name": "Alias", "subagent.aliases.namePlaceholder": "e.g. fast",
    "subagent.aliases.model": "Model", "subagent.aliases.chooseModel": "Choose a model",
    "subagent.aliases.unavailable": "(unavailable)",
    "subagent.aliases.level": "Reasoning level", "subagent.aliases.defaultLevel": "Default level",
    "subagent.level.none": "None", "subagent.level.minimal": "Minimal", "subagent.level.low": "Low",
    "subagent.level.medium": "Medium", "subagent.level.high": "High", "subagent.level.xhigh": "Very high",
    "subagent.level.max": "Max", "subagent.level.ultra": "Ultra",
    "subagent.aliases.add": "Add alias", "subagent.aliases.remove": "Remove alias",
    "subagent.aliases.empty": "No aliases yet. Add one to run child tasks on a faster or stronger model.",
    "subagent.aliases.noModels": "Connect a model service under Connections before adding aliases.",
    "subagent.aliases.invalidName": "Start with a lowercase letter; use only lowercase letters, digits, - or _.",
    "subagent.aliases.duplicateName": "Another alias already uses this name.",
    "subagent.aliases.saveFailed": "Could not save model aliases. Try again."
  } });
  api.registerLocale({ id: "subagent-zh", locale: "zh-CN", entries: {
    "subagent.aliases.title": "模型别名",
    "subagent.aliases.help": "Agent 委派子任务时按别名选用模型，下一句建议和 Dream 也可使用。",
    "subagent.aliases.name": "别名", "subagent.aliases.namePlaceholder": "如 fast",
    "subagent.aliases.model": "模型", "subagent.aliases.chooseModel": "选择模型",
    "subagent.aliases.unavailable": "（不可用）",
    "subagent.aliases.level": "思考强度", "subagent.aliases.defaultLevel": "默认强度",
    "subagent.level.none": "无", "subagent.level.minimal": "极低", "subagent.level.low": "低",
    "subagent.level.medium": "中", "subagent.level.high": "高", "subagent.level.xhigh": "超高",
    "subagent.level.max": "最高", "subagent.level.ultra": "极高",
    "subagent.aliases.add": "添加别名", "subagent.aliases.remove": "删除别名",
    "subagent.aliases.empty": "还没有别名。添加一个，让子任务用更快或更强的模型。",
    "subagent.aliases.noModels": "请先在「外部连接」中接入模型服务，再添加别名。",
    "subagent.aliases.invalidName": "以小写字母开头，只能包含小写字母、数字、- 和 _。",
    "subagent.aliases.duplicateName": "已有同名别名。",
    "subagent.aliases.saveFailed": "无法保存模型别名，请重试。"
  } });
  api.registerStyle({ id: "subagent-settings", css: `
    .plugin-subagent-settings { min-width:0; }
    .plugin-subagent-list { display:flex; flex-direction:column; min-width:0; margin:0; padding:0 var(--settings-card-pad-inline, var(--space-4)); list-style:none; border:1px solid var(--settings-card-border, var(--hairline)); border-radius:var(--settings-card-radius, var(--radius-md)); background:var(--settings-card-bg, var(--paper)); }
    .plugin-subagent-list > li + li { border-top:1px solid var(--settings-divider, var(--hairline-soft)); }
    .plugin-subagent-alias { display:grid; grid-template-columns:minmax(0,9em) minmax(0,1fr) minmax(0,9em) var(--control-field-height); grid-template-areas:"name model level remove" "hint hint hint hint"; align-items:center; column-gap:var(--space-3); padding:var(--settings-row-pad-block, var(--space-3)) 0; }
    .plugin-subagent-alias .plugin-ui-input { width:100%; min-width:0; }
    .plugin-subagent-name { grid-area:name; font-weight:var(--weight-medium); }
    .plugin-subagent-alias .plugin-subagent-name:not(:hover):not(:focus):not([aria-invalid]) { border-color:transparent; background:transparent; }
    .plugin-subagent-model { grid-area:model; }
    .plugin-subagent-level { grid-area:level; }
    .plugin-subagent-remove { grid-area:remove; display:inline-grid; place-items:center; width:var(--control-field-height); height:var(--control-field-height); padding:0 !important; color:var(--ink-tertiary); }
    .plugin-subagent-remove:hover:not(:disabled) { color:var(--ink); }
    .plugin-subagent-remove svg { width:14px; height:14px; }
    .plugin-subagent-hint { grid-area:hint; margin:var(--space-2) 0 0; color:var(--danger); font-size:var(--font-sm); line-height:1.4; }
    .plugin-subagent-empty { margin:0; padding:var(--settings-row-pad-block, var(--space-3)) 0; color:var(--ink-tertiary); font-size:var(--font-ui); line-height:1.5; }
    .plugin-subagent-add { align-self:flex-start; display:inline-flex; align-items:center; gap:var(--space-2); }
    .plugin-subagent-add svg { width:14px; height:14px; }
    @container settings-page (max-width:32em) {
      .plugin-subagent-alias { grid-template-columns:minmax(0,3fr) minmax(0,2fr) var(--control-field-height); grid-template-areas:"name name remove" "model level ." "hint hint hint"; row-gap:var(--space-2); }
    }
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

  const icon = (d) => h("svg", { viewBox: "0 0 16 16", fill: "none", "aria-hidden": true },
    h("path", { d, stroke: "currentColor", strokeWidth: "1.5", strokeLinecap: "round" }));
  const aliasNamePattern = /^[a-z][a-z0-9_-]*$/;
  const modelKey = (provider, model) => JSON.stringify([provider, model]);
  // Go serializes the alias map with sorted keys; compare in the same order.
  const canonical = (aliases) => JSON.stringify(Object.keys(aliases).sort().map((name) => [name, aliases[name]]));
  const aliasValue = (row) => {
    const value = { provider: row.provider, model: row.model };
    if (row.variant) value.variant = row.variant;
    if (row.effort) value.effort = row.effort;
    return value;
  };

  function ModelAliases({ host, translate }) {
    const { Page, Section, Stack, Button } = api.ui;
    const tr = typeof translate === "function" ? translate : (key) => key;
    const settings = host.settings;
    const aliases = settings ? settings.getValue("runtime.modelAliases") : {};
    const models = settings?.listAvailableModels?.() ?? [];
    const providers = [...new Set(models.map((model) => model.provider))];
    const nextId = React.useRef(0);
    const persisted = React.useRef(canonical(aliases));
    const toRows = () => Object.keys(aliases).sort().map((name) => {
      const value = aliases[name];
      return { id: nextId.current++, name, provider: value.provider, model: value.model, variant: value.variant || "", effort: value.effort || "", saved: { name, value } };
    });
    const [rows, setRows] = React.useState(toRows);
    const [touched, setTouched] = React.useState(() => new Set());
    const [busy, setBusy] = React.useState(false);
    // Only an outside change replaces the rows; our own saves keep unsaved drafts.
    React.useEffect(() => {
      const next = canonical(aliases);
      if (next === persisted.current) return;
      persisted.current = next;
      setRows(toRows());
    }, [canonical(aliases)]);

    const nameProblem = (row, all) => {
      const name = row.name.trim();
      if (!aliasNamePattern.test(name)) return "subagent.aliases.invalidName";
      if (all.some((other) => other.id !== row.id && other.name.trim() === name)) return "subagent.aliases.duplicateName";
      return "";
    };
    const complete = (row, all) => !nameProblem(row, all) && row.provider && row.model;
    const persist = async (next) => {
      setRows(next);
      // An unfinished edit keeps the alias's last saved value instead of deleting it.
      const value = {};
      for (const row of next) {
        if (complete(row, next)) value[row.name.trim()] = aliasValue(row);
        else if (row.saved && !Object.hasOwn(value, row.saved.name)) value[row.saved.name] = row.saved.value;
      }
      const target = canonical(value);
      if (target === persisted.current || !settings) return;
      const previous = persisted.current;
      // The host can publish the new aliases before updateValue resolves.
      persisted.current = target;
      setBusy(true);
      try {
        await settings.updateValue("runtime.modelAliases", value);
        setRows((current) => current.map((row) => complete(row, current) ? { ...row, name: row.name.trim(), saved: { name: row.name.trim(), value: aliasValue(row) } } : row));
      } catch {
        persisted.current = previous;
        api.showNotice({ message: tr("subagent.aliases.saveFailed"), tone: "error", key: `subagent-aliases-${Date.now()}` });
      } finally { setBusy(false); }
    };
    const patch = (id, change) => rows.map((row) => row.id === id ? { ...row, ...change } : row);
    const commitName = (id) => {
      setTouched((current) => new Set(current).add(id));
      void persist(patch(id, {}).map((row) => row.id === id ? { ...row, name: row.name.trim().toLowerCase() } : row));
    };
    const levelLabel = (level) => ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"].includes(level) ? tr(`subagent.level.${level}`) : level;

    const renderRow = (row) => {
      const selected = models.find((model) => model.provider === row.provider && model.model === row.model);
      const level = row.variant || row.effort;
      const levels = selected?.variants ?? [];
      const problem = touched.has(row.id) && row.name.trim() ? nameProblem(row, rows) : "";
      const hintId = `plugin-subagent-hint-${row.id}`;
      return h("li", { key: row.id, className: "plugin-subagent-alias" },
        h("input", {
          className: "plugin-ui-input plugin-subagent-name", value: row.name, placeholder: tr("subagent.aliases.namePlaceholder"),
          "aria-label": tr("subagent.aliases.name"), "aria-invalid": problem ? true : undefined, "aria-describedby": problem ? hintId : undefined,
          spellCheck: false, autoCapitalize: "off", autoComplete: "off",
          onChange: (event) => setRows(patch(row.id, { name: event.target.value })),
          onBlur: () => commitName(row.id),
          onKeyDown: (event) => { if (event.key === "Enter") event.currentTarget.blur(); },
        }),
        h("select", {
          className: "plugin-ui-input plugin-ui-select plugin-subagent-model", "aria-label": tr("subagent.aliases.model"),
          value: row.model ? modelKey(row.provider, row.model) : "", disabled: busy,
          onChange: (event) => {
            const [provider, model] = JSON.parse(event.target.value);
            void persist(patch(row.id, { provider, model, variant: "", effort: "" }));
          },
        },
        h("option", { value: "", disabled: true }, tr("subagent.aliases.chooseModel")),
        providers.map((provider) => h("optgroup", { key: provider, label: provider },
          models.filter((model) => model.provider === provider).map((model) =>
            h("option", { key: model.model, value: modelKey(model.provider, model.model) }, model.label)))),
        row.model && !selected ? h("option", { value: modelKey(row.provider, row.model) }, `${row.model} · ${row.provider} ${tr("subagent.aliases.unavailable")}`) : null),
        h("select", {
          className: "plugin-ui-input plugin-ui-select plugin-subagent-level", "aria-label": tr("subagent.aliases.level"),
          value: level, disabled: busy || (levels.length === 0 && !level),
          onChange: (event) => void persist(patch(row.id, { variant: event.target.value, effort: "" })),
        },
        h("option", { value: "" }, tr("subagent.aliases.defaultLevel")),
        levels.map((id) => h("option", { key: id, value: id }, levelLabel(id))),
        level && !levels.includes(level) ? h("option", { value: level }, levelLabel(level)) : null),
        h(Button, {
          className: "plugin-subagent-remove", variant: "ghost", disabled: busy,
          "aria-label": tr("subagent.aliases.remove"), title: tr("subagent.aliases.remove"),
          onClick: () => void persist(rows.filter((item) => item.id !== row.id)),
        }, icon("m4 4 8 8m0-8-8 8")),
        problem ? h("p", { id: hintId, className: "plugin-subagent-hint", role: "alert" }, tr(problem)) : null);
    };

    return h(Page, { className: "plugin-subagent-settings" },
      h(Section, { title: tr("subagent.aliases.title"), description: tr("subagent.aliases.help") }, h(Stack, null,
        rows.length ? h("ul", { className: "plugin-subagent-list" }, rows.map(renderRow))
          : h("div", { className: "plugin-subagent-list" }, h("p", { className: "plugin-subagent-empty" },
            tr(models.length ? "subagent.aliases.empty" : "subagent.aliases.noModels"))),
        models.length ? h(Button, {
          className: "plugin-subagent-add", disabled: busy,
          onClick: () => setRows([...rows, { id: nextId.current++, name: "", provider: "", model: "", variant: "", effort: "", saved: null }]),
        }, icon("M8 3.5v9M3.5 8h9"), tr("subagent.aliases.add")) : null)));
  }
  api.registerViewType({ id: "subagent.settings", title: "Subagent", icon: "bot", defaultRegion: "settings", persistence: "durable", render: ModelAliases });
}
