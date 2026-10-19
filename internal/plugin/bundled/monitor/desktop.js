export async function activate(api) {
  const React = api.react, h = React.createElement;
  const { Button, ComposerDrawer } = api.ui;
  api.registerLocale({ id: "monitor-en", locale: "en-US", entries: {
    "monitor.title": "Monitors", "monitor.running": "{count} monitors still running", "monitor.show": "Show monitors", "monitor.hide": "Hide monitors",
    "monitor.stop": "Stop monitor", "monitor.stopping": "Stopping…", "monitor.lines": "{count} lines", "monitor.events": "{count} events",
    "monitor.command": "Command", "monitor.output": "Output", "monitor.empty": "Waiting for events…", "monitor.persistent": "Session watch",
    "monitor.deadline": "Deadline", "monitor.suppressed": "{count} event batches suppressed", "monitor.tail": "Showing the retained output; earlier output was truncated.",
    "monitor.status.running": "Watching", "monitor.status.completed": "Completed", "monitor.status.failed": "Failed", "monitor.status.stopped": "Stopped",
    "monitor.status.timed_out": "Timed out", "monitor.status.overloaded": "Stopped · too much output", "monitor.status.interrupted": "Interrupted",
    "monitor.details": "Show monitor details", "monitor.close": "Hide monitor details", "monitor.retry": "Retry", "monitor.follow": "Follow output",
  } });
  api.registerLocale({ id: "monitor-zh", locale: "zh-CN", entries: {
    "monitor.title": "监控", "monitor.running": "{count} 个监控仍在运行", "monitor.show": "展开监控", "monitor.hide": "收起监控",
    "monitor.stop": "停止监控", "monitor.stopping": "正在停止…", "monitor.lines": "{count} 行", "monitor.events": "{count} 个事件",
    "monitor.command": "命令", "monitor.output": "输出", "monitor.empty": "等待事件…", "monitor.persistent": "持续监控",
    "monitor.deadline": "截止时间", "monitor.suppressed": "已抑制 {count} 批过量事件", "monitor.tail": "当前显示保留的输出，较早的内容已截断。",
    "monitor.status.running": "监控中", "monitor.status.completed": "已完成", "monitor.status.failed": "失败", "monitor.status.stopped": "已停止",
    "monitor.status.timed_out": "已超时", "monitor.status.overloaded": "已停止 · 输出过量", "monitor.status.interrupted": "已中断",
    "monitor.details": "展开监控详情", "monitor.close": "收起监控详情", "monitor.retry": "重试", "monitor.follow": "跟随输出",
  } });
  let disposed = false;
  const empty = Object.freeze({ tasks: [], error: "" });
  const cache = new Map(), listeners = new Map(), requests = new Map();
  function publish(id, state) { cache.set(id, state); for (const listener of listeners.get(id) || []) listener(); }
  async function refresh(id) {
    if (!id || disposed || requests.has(id)) return;
    const request = (async () => {
      try {
        const response = await api.invokeRuntime("monitor.tasks", { action: "list", thread_id: id });
        if (!disposed) publish(id, { tasks: response.tasks, error: "" });
      } catch (error) { if (!disposed) publish(id, { ...(cache.get(id) || empty), error: String(error.message || error) }); }
      finally { requests.delete(id); }
    })();
    requests.set(id, request);
  }
  function subscribe(id, listener) {
    if (!listeners.has(id)) listeners.set(id, new Set());
    listeners.get(id).add(listener);
    void refresh(id);
    return () => { const set = listeners.get(id); set?.delete(listener); if (!set?.size) listeners.delete(id); };
  }
  const timer = setInterval(() => { for (const id of listeners.keys()) void refresh(id); }, 1000);
  api.registerCleanup(() => { disposed = true; clearInterval(timer); listeners.clear(); cache.clear(); });
  api.onHostEvent(event => {
    if (["turn/started", "turn/completed", "turn/error", "thread/resumed"].includes(event?.message?.method)) {
      for (const id of listeners.keys()) void refresh(id);
    }
  });
  function Icon({ stop = false }) {
    return h("svg", { width: 16, height: 16, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.6, strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": true },
      stop ? h("rect", { x: 6, y: 6, width: 12, height: 12, rx: 1 }) : h("path", { d: "M2 12h5l3-8 4 16 3-8h5" }));
  }
  function Output({ task, tr }) {
    const [state, setState] = React.useState({ text: "", truncated: false, error: "" });
    const [following, setFollowing] = React.useState(true);
    const [retry, setRetry] = React.useState(0);
    const outputRef = React.useRef(null);
    React.useEffect(() => {
      let stopped = false, polling = false, offset = 0, text = "", truncated = false;
      async function read() {
        if (stopped || polling || disposed) return;
        polling = true;
        try {
          const value = await api.invokeRuntime("monitor.tasks", { action: "read", task_id: task.id, thread_id: task.thread_id, offset_bytes: offset });
          if (stopped || disposed) return;
          text += value.output;
          // Keep the live viewer bounded, independently of the full process log.
          if (text.length > 131072) { text = text.slice(-131072); truncated = true; }
          truncated ||= !!value.truncated;
          offset = value.next_offset;
          setState({ text, truncated, error: "" });
        } catch (error) { if (!stopped) setState(current => ({ ...current, error: String(error.message || error) })); }
        finally { polling = false; }
      }
      void read();
      const interval = setInterval(() => void read(), 1000);
      return () => { stopped = true; clearInterval(interval); };
    }, [task.id, retry]);
    React.useEffect(() => { if (following && outputRef.current) outputRef.current.scrollTop = outputRef.current.scrollHeight; }, [state.text, following]);
    return h("div", { className: "plugin-monitor-output" },
      h("div", { className: "plugin-monitor-output-heading" }, h("span", null, tr("monitor.output")),
        !following ? h(Button, { variant: "ghost", onClick: () => setFollowing(true) }, tr("monitor.follow")) : null),
      state.error ? h("div", { role: "alert", className: "plugin-monitor-error" }, state.error, h(Button, { variant: "ghost", onClick: () => setRetry(value => value + 1) }, tr("monitor.retry"))) : null,
      state.truncated ? h("p", { className: "plugin-monitor-muted" }, tr("monitor.tail")) : null,
      h("pre", { ref: outputRef, className: "plugin-monitor-log", tabIndex: 0, "aria-label": tr("monitor.output"), onScroll: event => {
        const node = event.currentTarget; setFollowing(node.scrollHeight - node.scrollTop - node.clientHeight < 24);
      } }, state.text || tr("monitor.empty")),
    );
  }
  function Row({ task, tr, locale, readOnly }) {
    const [open, setOpen] = React.useState(false), [busy, setBusy] = React.useState(false), [error, setError] = React.useState("");
    const running = task.status === "running";
    const detailsID = `monitor-${task.id}`;
    async function stop() {
      setBusy(true); setError("");
      try {
        await api.invokeRuntime("monitor.tasks", { action: "stop", task_id: task.id, thread_id: task.thread_id });
        await refresh(task.thread_id);
      } catch (cause) { setError(String(cause.message || cause)); }
      finally { setBusy(false); }
    }
    return h("li", { className: "plugin-monitor-row", "data-state": task.status },
      h("div", { className: "plugin-monitor-row-heading" },
        h(Button, { variant: "ghost", className: "plugin-monitor-select", "aria-expanded": open, "aria-controls": detailsID, onClick: () => setOpen(value => !value) },
          h("span", { className: "plugin-monitor-dot", "aria-hidden": true }),
          h("span", { className: "plugin-monitor-description" }, task.description),
          h("span", { className: "plugin-monitor-lines" }, tr("monitor.lines", { count: task.lines }))),
        running ? h(Button, { variant: "ghost", className: "plugin-monitor-stop", disabled: readOnly || busy, title: tr("monitor.stop"), "aria-label": `${tr("monitor.stop")}: ${task.description}`, onClick: () => void stop() }, h(Icon, { stop: true })) : h("span", { className: "plugin-monitor-stop", "aria-hidden": true })),
      h("div", { className: "plugin-monitor-meta" }, h("span", null, tr(busy ? "monitor.stopping" : `monitor.status.${task.status}`)), h("code", null, task.id)),
      error || task.error ? h("p", { role: "alert", className: "plugin-monitor-error" }, error || task.error) : null,
      open ? h("div", { id: detailsID, className: "plugin-monitor-details" },
        h("div", { className: "plugin-monitor-meta" }, h("span", null, tr("monitor.events", { count: task.events })),
          task.persistent ? h("span", null, tr("monitor.persistent")) : h("span", null, `${tr("monitor.deadline")}: ${new Date(Date.parse(task.started_at) + task.timeout_ms).toLocaleString(locale)}`)),
        task.suppressed ? h("p", { className: "plugin-monitor-muted" }, tr("monitor.suppressed", { count: task.suppressed })) : null,
        h("span", { className: "plugin-monitor-muted" }, tr("monitor.command")), h("pre", { className: "plugin-monitor-command", tabIndex: 0 }, task.command),
        h(Output, { task, tr }),
      ) : null,
    );
  }
  function Controls({ threadId, readOnly, tr, locale }) {
    const state = React.useSyncExternalStore(React.useCallback(listener => subscribe(threadId, listener), [threadId]), React.useCallback(() => cache.get(threadId) || empty, [threadId]));
    const [open, setOpen] = React.useState(false);
    if (!state.tasks.length && !state.error) return null;
    const running = state.tasks.filter(task => task.status === "running").length;
    const sorted = [...state.tasks].sort((a, b) => Number(b.status === "running") - Number(a.status === "running") || Date.parse(b.started_at) - Date.parse(a.started_at));
    return h(ComposerDrawer, { className: "plugin-monitor", "aria-label": tr("monitor.title"), expanded: open, onExpandedChange: setOpen,
      toggleLabel: tr(open ? "monitor.hide" : "monitor.show"), icon: h(Icon), tone: running ? "default" : "muted",
      summary: h("span", { className: "plugin-monitor-summary" }, running ? tr("monitor.running", { count: running }) : tr("monitor.title")),
      notice: state.error ? h("p", { role: "alert", className: "plugin-monitor-error" }, state.error) : null,
    }, open ? h("ul", { className: "plugin-monitor-list" }, sorted.map(task => h(Row, { key: task.id, task, tr, locale, readOnly }))) : null);
  }
  api.registerSlot("composer.above", { id: "monitor-tasks", title: "Monitors", order: 30,
    render: ({ threadId, mainConversation, readOnly, translate, locale }) => threadId && mainConversation ? h(Controls, { key: threadId, threadId, readOnly, tr: translate, locale }) : null,
  });
  api.registerStyle({ id: "monitor-tasks", css: `
    .plugin-monitor { --composer-drawer-max-height:min(55dvh,36rem); --monitor-indent:calc(7px + var(--space-2)); font-size:var(--font-ui); }
    .plugin-monitor-summary { min-width:0; overflow-wrap:anywhere; }
    .plugin-monitor-list { list-style:none; margin:0; padding:0 var(--space-3) var(--space-3); display:grid; gap:var(--space-3); }
    .plugin-monitor-row { min-width:0; }
    .plugin-monitor-row-heading { display:flex; align-items:center; gap:var(--space-2); }
    .plugin-monitor .plugin-monitor-select { flex:1; min-width:0; display:flex; align-items:center; gap:var(--space-2); text-align:start; padding:var(--space-1) 0; font-weight:var(--weight-regular); }
    .plugin-monitor-description { flex:1; min-width:0; overflow-wrap:anywhere; font-weight:var(--weight-regular); }
    .plugin-monitor-dot { width:7px; height:7px; flex-shrink:0; border-radius:50%; background:var(--ink-muted); }
    .plugin-monitor-row[data-state="running"] .plugin-monitor-dot { background:var(--success); }
    .plugin-monitor-row[data-state="failed"] .plugin-monitor-dot, .plugin-monitor-row[data-state="overloaded"] .plugin-monitor-dot { background:var(--danger); }
    .plugin-monitor-lines { flex-shrink:0; color:var(--ink-soft); font-variant-numeric:tabular-nums; }
    .plugin-monitor .plugin-monitor-stop { display:inline-grid; place-items:center; padding:0; width:28px; height:28px; min-height:28px; flex-shrink:0; border-radius:var(--radius-sm); }
    .plugin-monitor-meta { display:flex; flex-wrap:wrap; gap:var(--space-2); color:var(--ink-soft); overflow-wrap:anywhere; }
    .plugin-monitor-row > .plugin-monitor-meta { padding-inline-start:var(--monitor-indent); }
    .plugin-monitor-meta code { font:inherit; }
    .plugin-monitor-details { display:grid; gap:var(--space-2); padding-top:var(--space-2); padding-inline-start:var(--monitor-indent); }
    .plugin-monitor-command, .plugin-monitor-log { margin:0; padding:var(--space-2); overflow:auto; max-height:16rem; white-space:pre-wrap; overflow-wrap:anywhere; background:var(--surface-2); border-radius:var(--radius-sm); font-family:var(--font-mono); font-size:var(--appearance-code-size); line-height:1.5; }
    .plugin-monitor-output { min-width:0; display:grid; gap:var(--space-2); }
    .plugin-monitor-output-heading { display:flex; align-items:center; justify-content:space-between; gap:var(--space-2); color:var(--ink-soft); }
    .plugin-monitor-error { margin:0; color:var(--danger); overflow-wrap:anywhere; }
    .plugin-monitor-muted { margin:0; color:var(--ink-soft); }
    @media (pointer:coarse) { .plugin-monitor .plugin-monitor-stop { width:44px; height:44px; } }
  ` });
}
