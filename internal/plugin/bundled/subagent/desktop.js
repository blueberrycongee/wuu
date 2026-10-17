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

  registerTaskViews(api);

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

function registerTaskViews(api) {
  const React = api.react;
  const h = React.createElement;
  const active = (task) => ["created", "running", "queued", "pending"].includes(task.state);
  const sources = new Map();
  let lastTrigger;
  const words = {
    en: { started: "started work", updated: "received an update", stopped: "stop requested", other: "other agents", title: "Subagents", active: "Active", finished: "Finished", empty: "No subagents yet", none: "No active subagents", all: "All subagents", back: "Back to all subagents", more: "Show more", less: "Show less", loading: "Loading…", retry: "Retry", task: "Task", records: "Work record", result: "Result", earlier: "Earlier records", latest: "Latest records", next: "Continue records", truncated: "Record shortened", noRecords: "No work records yet", running: "Working", queued: "Waiting", created: "Starting", completed: "Completed", failed: "Failed", interrupted: "Interrupted", discarded: "Discarded", unknown: "Unavailable" },
    zh: { started: "开始工作", updated: "已收到补充要求", stopped: "已请求停止", other: "个子代理", title: "子代理", active: "进行中", finished: "已结束", empty: "还没有子代理", none: "当前没有正在工作的子代理", all: "全部子代理", back: "返回全部子代理", more: "显示更多", less: "收起", loading: "正在加载…", retry: "重试", task: "任务", records: "工作记录", result: "返回结果", earlier: "更早的记录", latest: "最新记录", next: "继续查看记录", truncated: "此条记录已截短", noRecords: "暂无工作记录", running: "工作中", queued: "等待中", created: "正在启动", completed: "已完成", failed: "失败", interrupted: "已中断", discarded: "已取消", unknown: "暂不可用" },
  };
  const getWords = (locale) => words[String(locale).startsWith("zh") ? "zh" : "en"];
  const parse = (raw) => { try { return JSON.parse(raw || "null"); } catch { return null; } };
  function source(parent) {
    if (!sources.has(parent)) sources.set(parent, { tasks: [], loading: true, error: "", listeners: new Set(), pending: false, dirty: false });
    return sources.get(parent);
  }
  async function refresh(parent) {
    const state = source(parent);
    if (state.pending) { state.dirty = true; return; }
    state.pending = true;
    try {
      const result = await api.invokeRuntime("status.list", { parent_session_id: parent });
      if (api.signal.aborted || sources.get(parent) !== state) return;
      state.tasks = result.sessions.slice().sort((a, b) => String(a.created_at).localeCompare(String(b.created_at)) || a.session_id.localeCompare(b.session_id));
      state.error = "";
    } catch (error) {
      if (api.signal.aborted || sources.get(parent) !== state) return;
      state.error = String(error.message || error);
    } finally {
      state.pending = false;
      state.loading = false;
      for (const listener of state.listeners) listener();
      if (state.dirty && state.listeners.size && !api.signal.aborted) { state.dirty = false; void refresh(parent); }
    }
  }
  function useTasks(parent) {
    const [, update] = React.useState(0);
    React.useEffect(() => {
      if (!parent) return;
      const state = source(parent);
      const listener = () => update(value => value + 1);
      state.listeners.add(listener);
      if (state.listeners.size === 1) void refresh(parent);
      return () => { state.listeners.delete(listener); if (!state.listeners.size) sources.delete(parent); };
    }, [parent]);
    return parent ? source(parent) : { tasks: [], loading: false, error: "" };
  }
  api.onHostEvent(event => {
    if (/^(thread\/(started|resumed|updated)|turn\/(started|queued|dequeued|held|completed|error))$/.test(event?.message?.method || "")) {
      for (const [parent, state] of sources) if (state.listeners.size) void refresh(parent);
    }
  });
  api.registerCleanup(() => { sources.clear(); lastTrigger = undefined; });

  function Avatar({ id, small }) {
    let hash = 0;
    for (const char of id) hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
    return h("span", { className: `subagent-avatar subagent-tone-${hash % 5}${small ? " subagent-avatar-small" : ""}`, "aria-hidden": true },
      h("svg", { viewBox: "0 0 24 24", fill: "none" },
        h("path", { d: "M3 12Q3 4 12 3Q21 4 21 12L20 19Q12 23 4 19Z", fill: "currentColor" }),
        h("circle", { cx: 8.5, cy: 12, r: 1.35, fill: "var(--paper)" }),
        h("circle", { cx: 15.5, cy: 12, r: 1.35, fill: "var(--paper)" }),
        h("path", { d: "M9 16Q12 18 15 16", stroke: "var(--paper)", strokeWidth: 1.2, strokeLinecap: "round" })));
  }
  async function open(host, parent, selected, trigger) {
    lastTrigger = trigger;
    await host.openView("subagent.tasks", { region: "auxiliary", context: { parent, selected }, persistence: "session" });
  }
  function InlineTask({ activity, host, fallback }) {
    const raw = activity.structuredResult?.structuredContent
      || parse(activity.structuredResult?.content?.find(part => part.type === "text")?.text) || parse(activity.resultText);
    const result = raw?.session_id ? raw : parse(raw?.content?.find(part => part.type === "text")?.text);
    const [error, setError] = React.useState("");
    const text = getWords(activity.locale);
    const parent = result?.parent_session_id || activity.threadId;
    const state = useTasks(parent);
    const self = state.tasks.find(task => task.session_id === result?.session_id || task.call_id === (activity.callId || activity.id))
      || (result?.session_id ? { session_id: result.session_id, name: result.task_name, description: result.description, parent_turn_id: result.parent_turn_id } : null);
    if (!self || !parent || activity.status === "failed") return fallback;
    const group = activity.capability === "subagent.spawn" && self.parent_turn_id
      ? state.tasks.filter(task => task.parent_turn_id === self.parent_turn_id) : [];
    const tasks = group.length ? group : [self];
    if (group.length && group[0].session_id !== self.session_id) return null;
    const named = tasks.length > 3 ? tasks.slice(0, 2) : tasks;
    const choose = (selected, event) => { setError(""); void open(host, parent, selected, event.currentTarget).catch(error => setError(String(error.message || error))); };
    return h("div", { className: "subagent-inline" },
      h("span", { className: "subagent-inline-avatars" }, tasks.slice(0, 4).map(task => h(Avatar, { id: task.session_id, small: true, key: task.session_id }))),
      h("span", { className: "subagent-inline-sentence" }, named.map((task, index) => h(React.Fragment, { key: task.session_id },
        index ? ", " : "", h("button", { type: "button", className: "subagent-name", title: task.description || task.name, onClick: event => choose(task.session_id, event) }, task.name || text.task))),
      tasks.length > 3 ? h("button", { type: "button", className: "subagent-name subagent-overflow", onClick: event => choose(null, event) }, `+${tasks.length - 2} ${text.other}`) : null,
      " ", h("span", null, activity.capability === "subagent.send" ? text.updated : activity.capability === "subagent.close" ? text.stopped : text.started),
      error ? h("span", { className: "subagent-error", role: "alert" }, error) : null));
  }
  for (const key of ["subagent.spawn", "subagent.send", "subagent.close"]) {
    api.registerToolActivityPresenter({ id: key, key, render: InlineTask });
  }

  function Completion({ snapshot, host, fallback }) {
    const [detail, setDetail] = React.useState(null);
    const [error, setError] = React.useState("");
    const id = snapshot.origin === "plugin" ? snapshot.relatedSessionId : undefined;
    React.useEffect(() => {
      let disposed = false;
      setDetail(null);
      if (id) void api.invokeRuntime("session.info", { session_id: id }).then(value => {
        if (!disposed && !api.signal.aborted) setDetail(value);
      }, () => { /* Messages from other plugins retain their native presentation. */ });
      return () => { disposed = true; };
    }, [id]);
    if (!detail?.session.parent_session_id) return fallback;
    const text = getWords(snapshot.locale);
    return h("div", { className: "subagent-inline" }, h(Avatar, { id, small: true }),
      h("span", { className: "subagent-inline-sentence" },
        h("button", { type: "button", className: "subagent-name", onClick: event => {
          setError("");
          void open(host, detail.session.parent_session_id, id, event.currentTarget).catch(error => setError(String(error.message || error)));
        } }, snapshot.text || detail.session.name || snapshot.sourceName || text.task),
        error ? h("span", { className: "subagent-error", role: "alert" }, error) : null));
  }
  api.registerPresenter({ id: "subagent-completion", target: "conversation.item", key: "user-message", mode: "replace", priority: 20, render: Completion });

  function TaskDetail({ id, text, liveState }) {
    const [page, setPage] = React.useState({});
    const [revision, retry] = React.useState(0);
    const [state, setState] = React.useState({ loading: true, data: null, error: "" });
    React.useEffect(() => {
      let disposed = false;
      let timer;
      setState({ loading: true, data: null, error: "" });
      const read = async () => {
        try {
          const data = await api.invokeRuntime("session.read", { session_id: id, ...page });
          if (disposed || api.signal.aborted) return;
          setState({ loading: false, data, error: "" });
          // Only the visible, current page follows live execution. Historical
          // pages stay still while the user reads them.
          if (!page.before_seq && !page.cursor && active({ state: data.turn?.state || data.session.state })) timer = setTimeout(read, 2000);
        } catch (error) {
          if (!disposed && !api.signal.aborted) setState(current => ({ ...current, loading: false, error: String(error.message || error) }));
        }
      };
      void read();
      return () => { disposed = true; clearTimeout(timer); };
    }, [id, page, revision, !page.before_seq && !page.cursor ? liveState : undefined]);
    const data = state.data;
    const records = data?.history.records || [];
    return h("div", { className: "subagent-detail" },
      state.loading ? h("p", { role: "status" }, text.loading) : null,
      state.error ? h("div", { className: "subagent-error", role: "alert" }, state.error, " ", h("button", { type: "button", onClick: () => retry(value => value + 1) }, text.retry)) : null,
      data ? h(React.Fragment, null,
        h("h3", null, data.description || data.session.name),
        h("p", { className: "subagent-state", "data-state": data.turn?.state || data.session.state }, text[data.turn?.state || data.session.state] || text.unknown),
        h("div", { className: "subagent-record-navigation" },
          data.has_earlier ? h("button", { type: "button", onClick: () => setPage({ before_seq: data.start_seq }) }, text.earlier) : null,
          page.before_seq || page.cursor ? h("button", { type: "button", onClick: () => setPage({}) }, text.latest) : null),
        h("h4", null, text.records),
        records.length ? records.filter(record => ["user", "assistant", "tool"].includes(record.role)).map(record => {
          const calls = parse(record.tool_calls);
          return h("section", { key: record.seq, className: `subagent-record subagent-record-${record.role}` },
            record.role === "tool" ? h("details", null, h("summary", null, record.name || record.tool_call_id), h("pre", null, record.display_content || record.content || record.tool_result))
              : h(React.Fragment, null,
                record.display_content || record.content ? h("p", null, record.display_content || record.content) : null,
                Array.isArray(calls) ? calls.map((call, index) => h("div", { className: "subagent-work-step", key: call.id || index }, call.function?.name || call.name)) : null),
            record.truncated ? h("p", { className: "subagent-muted" }, text.truncated) : null);
        }) : h("p", { className: "subagent-muted" }, text.noRecords),
        data.history.next_cursor ? h("button", { type: "button", onClick: () => setPage({ ...page, cursor: data.history.next_cursor }) }, text.next) : null,
        data.turn?.error ? h("p", { className: "subagent-error" }, data.turn.error) : null,
        data.turn?.final_output && !records.some(record => record.content === data.turn.final_output) ? h("section", { className: "subagent-record" }, h("h4", null, text.result), h("p", null, data.turn.final_output)) : null) : null);
  }
  function TaskPanel({ host, context, locale }) {
    const text = getWords(locale);
    const [selected, select] = React.useState(context.selected || null);
    const [expanded, expand] = React.useState(false);
    const state = useTasks(context.parent);
    const heading = React.useRef(null);
    React.useEffect(() => { select(context.selected || null); expand(false); }, [context]);
    React.useEffect(() => { heading.current?.focus(); }, [selected, context]);
    React.useEffect(() => () => { lastTrigger?.isConnected && lastTrigger.focus({ preventScroll: true }); }, []);
    const current = state.tasks.find(task => task.session_id === selected);
    const working = state.tasks.filter(active);
    const ended = state.tasks.filter(task => !active(task));
    const row = task => h("button", { type: "button", className: "subagent-task-row", key: task.session_id, onClick: () => select(task.session_id) },
      h(Avatar, { id: task.session_id }), h("span", { className: "subagent-task-copy" },
        h("span", { className: "subagent-task-title" }, h("span", null, task.name), h("span", { className: "subagent-state", "data-state": task.state }, text[task.state] || text.unknown)),
        task.description ? h("span", null, task.description) : null,
        task.preview ? h("span", { className: "subagent-task-preview" }, task.preview) : null));
    return h("div", { className: "subagent-panel", onKeyDown: event => { if (event.key === "Escape") { event.stopPropagation(); void host.closeView(); } } },
      h("div", { className: "subagent-panel-heading" },
        selected ? h("button", { type: "button", className: "subagent-back", "aria-label": text.back, title: text.back, onClick: () => select(null) }, "←") : null,
        selected ? h(Avatar, { id: selected }) : null,
        h("h2", { ref: heading, tabIndex: -1 }, selected ? current?.name || text.task : text.title)),
      selected ? h(TaskDetail, { id: selected, text, liveState: current?.state, key: selected }) : h("div", { className: "subagent-overview" },
        state.loading ? h("p", { role: "status" }, text.loading) : null,
        state.error ? h("div", { className: "subagent-error", role: "alert" }, state.error, h("button", { type: "button", onClick: () => void refresh(context.parent) }, text.retry)) : null,
        !state.loading && !state.error && !state.tasks.length ? h("p", null, text.empty) : null,
        state.tasks.length ? h(React.Fragment, null,
          h("section", null, h("h3", null, `${text.active} ${working.length}`),
            working.length ? (expanded ? working : working.slice(0, 4)).map(row) : h("p", { className: "subagent-muted" }, text.none),
            working.length > 4 ? h("button", { type: "button", className: "subagent-show-more", onClick: () => expand(value => !value) }, expanded ? text.less : `${text.more} (${working.length - 4})`) : null),
          ended.length ? h("section", null, h("h3", null, `${text.finished} ${ended.length}`), ended.map(row)) : null) : null));
  }
  api.registerViewType({ id: "subagent.tasks", title: "Subagent", icon: "bot", defaultRegion: "auxiliary", persistence: "session", render: TaskPanel });
  api.registerStyle({ id: "subagent-tasks", css: `
    .subagent-inline { display:flex; align-items:baseline; gap:8px; padding:6px 0; color:var(--ink-soft); line-height:1.8; }
    .subagent-inline-avatars { display:flex; gap:2px; flex-shrink:0; transform:translateY(3px); }
    .subagent-inline-sentence { min-width:0; overflow-wrap:anywhere; }
    .subagent-name { font:inherit; padding:0; border:0; border-radius:3px; background:transparent; color:var(--ink); cursor:pointer; text-decoration:underline; text-underline-offset:4px; text-decoration-color:var(--hairline-strong); }
    .subagent-name:hover { text-decoration-color:currentColor; }
    .subagent-overflow { margin-left:6px; }
    .subagent-avatar { display:inline-flex; width:27px; height:27px; flex-shrink:0; }
    .subagent-avatar svg { width:100%; height:100%; }
    .subagent-avatar-small { width:18px; height:18px; }
    .subagent-tone-0 { color:#7276a2; } .subagent-tone-1 { color:#b78457; } .subagent-tone-2 { color:#699c96; } .subagent-tone-3 { color:#a17a93; } .subagent-tone-4 { color:#7f91ae; }
    :root[data-theme=dark] .subagent-tone-0 { color:#a6a8d5; } :root[data-theme=dark] .subagent-tone-1 { color:#d5ab85; } :root[data-theme=dark] .subagent-tone-2 { color:#92bdb7; } :root[data-theme=dark] .subagent-tone-3 { color:#c6a1b9; } :root[data-theme=dark] .subagent-tone-4 { color:#a7b9d4; }
    .subagent-panel { display:flex; flex-direction:column; min-height:0; font-size:var(--font-ui); overflow:hidden !important; }
    .subagent-panel button { font:inherit; cursor:pointer; color:inherit; }
    .subagent-panel button:focus-visible, .subagent-name:focus-visible { outline:2px solid var(--ink-soft); outline-offset:3px; }
    .subagent-panel-heading { display:flex; align-items:center; gap:8px; padding:15px 20px; border-bottom:1px solid var(--hairline-soft); flex-shrink:0; }
    .subagent-panel-heading h2 { font:inherit; font-weight:500; margin:0; min-width:0; overflow-wrap:anywhere; }
    .subagent-panel-heading h2:focus { outline:none; }
    .subagent-back { display:grid; place-items:center; width:32px; height:32px; border:0; border-radius:6px; background:transparent; flex-shrink:0; }
    .subagent-back:hover, .subagent-task-row:hover { background:var(--ink-overlay-4); }
    .subagent-overview, .subagent-detail { min-height:0; overflow:auto; overscroll-behavior:contain; flex:1; padding:24px 14px; }
    .subagent-overview > section + section { margin-top:28px; }
    .subagent-overview h3 { font:inherit; color:var(--ink-tertiary); margin:0 10px 9px; }
    .subagent-task-row { display:flex; align-items:flex-start; gap:10px; padding:12px 10px; width:100%; border:0; border-radius:8px; background:transparent; text-align:left; }
    .subagent-task-copy { display:flex; flex:1; min-width:0; flex-direction:column; gap:4px; color:var(--ink-soft); overflow-wrap:anywhere; }
    .subagent-task-title { display:flex; justify-content:space-between; align-items:baseline; gap:8px; color:var(--ink); }
    .subagent-state { color:var(--ink-tertiary); white-space:nowrap; }
    .subagent-state[data-state=failed], .subagent-error { color:var(--danger); white-space:pre-wrap; overflow-wrap:anywhere; }
    .subagent-task-preview { color:var(--ink-tertiary); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .subagent-show-more, .subagent-record-navigation button { border:0; background:transparent; padding:6px 10px; border-radius:6px; }
    .subagent-muted { color:var(--ink-tertiary); }
    .subagent-detail { padding:24px; }
    .subagent-detail h3 { font-size:calc(var(--font-ui) + 2px); font-weight:500; margin:0 0 7px; overflow-wrap:anywhere; }
    .subagent-detail h4 { font:inherit; color:var(--ink-tertiary); margin:24px 0 12px; }
    .subagent-record { margin:0 0 20px; line-height:1.8; overflow-wrap:anywhere; }
    .subagent-record p { white-space:pre-wrap; margin:0 0 10px; }
    .subagent-record-user { background:var(--surface-1); padding:12px; border-radius:12px; }
    .subagent-record pre { font-size:var(--font-code); white-space:pre-wrap; overflow-wrap:anywhere; }
    .subagent-work-step, .subagent-record summary { color:var(--ink-soft); }
    .subagent-record-navigation { display:flex; justify-content:space-between; gap:8px; }
  ` });
}
