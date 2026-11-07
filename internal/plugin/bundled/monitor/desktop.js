export function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const application = api.application;
  api.registerLocale({ id: "monitor-en", locale: "en-US", entries: {
    "monitor.single": "Still monitoring · {description}",
    "monitor.multiple": "Still monitoring {count} tasks · {description}", "monitor.separator": ", ",
  } });
  api.registerLocale({ id: "monitor-zh", locale: "zh-CN", entries: {
    "monitor.single": "仍在监控 · {description}",
    "monitor.multiple": "仍在监控 {count} 项 · {description}", "monitor.separator": "、",
  } });
  const sources = new Map();
  const empty = Object.freeze([]);
  function observe(thread, listener) {
    const key = JSON.stringify([thread.workspace_id, thread.cwd, thread.id]);
    let source = sources.get(key);
    if (!source) {
      source = { value: empty, listeners: new Set(), controller: new AbortController() };
      sources.set(key, source);
      source.refresh = async () => {
        if (source.pending) { source.dirty = true; return; }
        if (source.controller.signal.aborted) return;
        clearTimeout(source.timer);
        source.pending = true;
        try {
          const result = await api.invokeRuntime("monitor.list", { thread_id: thread.id }, {
            workspaceId: thread.workspace_id, workspaceRoot: thread.cwd, signal: source.controller.signal,
          });
          source.value = result.tasks;
        } catch {
          // Retry failed reads without discarding the last known running watches.
          source.dirty = true;
        } finally {
          source.pending = false;
          if (!source.controller.signal.aborted) {
            for (const notify of source.listeners) notify(source.value);
            if (source.dirty || source.value.length) source.timer = setTimeout(source.refresh, source.dirty ? 1000 : 5000);
            source.dirty = false;
          }
        }
      };
    }
    source.listeners.add(listener);
    listener(source.value);
    void source.refresh();
    return () => {
      source.listeners.delete(listener);
      if (!source.listeners.size) {
        clearTimeout(source.timer);
        source.controller.abort();
        sources.delete(key);
      }
    };
  }
  api.onHostEvent(event => {
    const method = event?.message?.method;
    const params = event?.message?.params;
    if ((method === "plugin/client/invalidated" && params?.plugin_id === api.pluginId && params?.topic === "monitors")
      || (typeof method === "string" && (method.startsWith("turn/") || method.startsWith("thread/")))) {
      for (const source of sources.values()) void source.refresh();
    }
  });
  api.registerCleanup(() => {
    for (const source of sources.values()) { clearTimeout(source.timer); source.controller.abort(); }
    sources.clear();
  });
  function Indicator({ tasks, translate: t }) {
    const [retained, setRetained] = React.useState(tasks);
    React.useEffect(() => {
      if (tasks.length) { setRetained(tasks); return; }
      const timer = setTimeout(() => setRetained(empty), window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 220);
      return () => clearTimeout(timer);
    }, [tasks]);
    const shown = tasks.length ? tasks : retained;
    if (!shown.length) return null;
    const description = shown.slice(0, 2).map(task => task.description).join(t("monitor.separator")) + (shown.length > 2 ? "…" : "");
    return h("div", { className: "plugin-monitor-presence", "data-exiting": !tasks.length },
      h("div", { className: "plugin-monitor-clip" },
        h("div", { className: "plugin-monitor-row", role: "status", "aria-live": "polite", title: shown.map(task => task.command).join("\n") },
          h(api.ui.Avatar, { size: 28, status: "waiting", motion: "ambient" }),
          h("span", { className: "plugin-monitor-label" }, t(shown.length === 1 ? "monitor.single" : "monitor.multiple", { count: shown.length, description })))));
  }
  function Contribution({ context, translate }) {
    const snapshot = React.useSyncExternalStore(application.subscribe, application.getSnapshot);
    const workspace = snapshot.conversationWorkspace;
    const thread = workspace.threads.find(item => item.id === context.threadId);
    const visible = context.threadId === workspace.primaryThreadId || context.threadId === workspace.secondaryThreadId;
    const [tasks, setTasks] = React.useState(empty);
    React.useEffect(() => {
      setTasks(empty);
      if (thread && visible) return observe(thread, setTasks);
    }, [thread?.id, thread?.workspace_id, thread?.cwd, visible]);
    const latest = thread?.turns.at(-1);
    if (!visible || !latest || latest.id !== context.turnId || latest.status === "in_progress" || context.snapshot.status === "in_progress" || thread.status === "in_progress") return null;
    return h(Indicator, { tasks, translate });
  }
  for (const target of ["wuu.default-conversation", "wuu.default-preview"]) {
    api.registerUIContribution({ id: `${target}-monitor`, target: `${target}/conversation.turn.after`, render: props => application ? h(Contribution, props) : null });
  }
  api.registerCSSSnippet({ id: "monitor-presence", css: `
    .plugin-monitor-presence { display: grid; grid-template-rows: 1fr; opacity: 1; animation: monitor-enter 180ms ease-out; transition: grid-template-rows 220ms ease, opacity 180ms ease; }
    .plugin-monitor-presence[data-exiting="true"] { grid-template-rows: 0fr; opacity: 0; }
    .plugin-monitor-clip { min-height: 0; overflow: hidden; }
    .plugin-monitor-row { display: flex; align-items: center; gap: 8px; min-height: 28px; padding-block: var(--space-2); color: var(--ink-soft); font-size: var(--conversation-process-font-size, 1em); line-height: var(--conversation-process-line-height, 1.5); }
    .plugin-monitor-row > :first-child { flex-shrink: 0; }
    .plugin-monitor-label { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    @keyframes monitor-enter { from { opacity: 0; transform: translateY(2px); } to { opacity: 1; transform: translateY(0); } }
    @media (prefers-reduced-motion: reduce) { .plugin-monitor-presence { animation: none; transition: none; } }
  ` });
}
