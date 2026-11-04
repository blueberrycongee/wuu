export function activate(api) {
  const React = api.react;
  const h = React.createElement;
  // Only the conversation slots consume this optional service; headless Peers
  // runtimes must remain usable without an application provider.
  const application = api.application;
  api.registerLocale({ id: "peer-en", locale: "en-US", entries: {
    "peers.open": "Open conversation", "peers.back": "Back", "peers.untitled": "Untitled conversation",
    "peers.accepted": "Sending", "peers.queued": "Queued", "peers.running": "In progress",
    "peers.completed": "Completed", "peers.failed": "Failed", "peers.interrupted": "Interrupted",
    "peers.discarded": "Cancelled", "peers.timed_out": "Timed out", "peers.reply_failed": "Reply failed",
    "peers.unknown": "Status unavailable", "peers.unavailable": "Conversation unavailable",
  } });
  api.registerLocale({ id: "peer-zh", locale: "zh-CN", entries: {
    "peers.open": "打开会话", "peers.back": "返回", "peers.untitled": "未命名会话",
    "peers.accepted": "发送中", "peers.queued": "排队中", "peers.running": "处理中",
    "peers.completed": "已完成", "peers.failed": "执行失败", "peers.interrupted": "已中断",
    "peers.discarded": "已取消", "peers.timed_out": "已超时", "peers.reply_failed": "回复未送达",
    "peers.unknown": "状态暂不可用", "peers.unavailable": "会话已不可用",
  } });

  // All mounted turns in a conversation share one request and polling lifetime.
  const sources = new Map();
  const empty = Object.freeze({ requests: [], failed: false });
  function observe(thread, listener) {
    const key = JSON.stringify([thread.workspace_id, thread.cwd, thread.id]);
    let source = sources.get(key);
    if (!source) {
      source = { value: empty, listeners: new Set(), controller: new AbortController(), timer: undefined };
      sources.set(key, source);
      source.refresh = async () => {
        if (source.pending || source.controller.signal.aborted) return;
        clearTimeout(source.timer);
        source.pending = true;
        try {
          const result = await api.invokeRuntime("requests.list", { source_session_id: thread.id }, {
            workspaceId: thread.workspace_id, workspaceRoot: thread.cwd, signal: source.controller.signal,
          });
          if (!source.controller.signal.aborted) source.value = { requests: result.requests, failed: false };
        } catch {
          // A failed refresh cannot leave an old running/completed label looking current.
          if (!source.controller.signal.aborted) source.value = { ...source.value, failed: true };
        } finally {
          source.pending = false;
          if (!source.controller.signal.aborted) {
            for (const notify of source.listeners) notify(source.value);
            source.timer = setTimeout(source.refresh, 3000);
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
    if (typeof method === "string" && (method.startsWith("turn/") || method.startsWith("thread/"))) {
      for (const source of sources.values()) void source.refresh();
    }
  });
  api.registerCleanup(() => {
    for (const source of sources.values()) {
      clearTimeout(source.timer);
      source.controller.abort();
    }
    sources.clear();
  });

  let navigation = { trail: [], focus: undefined };
  let navigating = false;
  const navigationListeners = new Set();
  const subscribeNavigation = listener => { navigationListeners.add(listener); return () => navigationListeners.delete(listener); };
  const getNavigation = () => navigation;
  const updateNavigation = value => { navigation = value; for (const listener of navigationListeners) listener(); };
  const useNavigation = () => React.useSyncExternalStore(subscribeNavigation, getNavigation);
  const useApplication = () => React.useSyncExternalStore(application.subscribe, application.getSnapshot);
  const run = action => {
    if (navigating) return;
    navigating = true;
    void action().catch(error => api.showNotice({ message: error.message, tone: "error" })).finally(() => { navigating = false; });
  };
  async function open(request, thread) {
    await application.invoke("thread.activate", { threadId: request.session_id, workspaceId: thread.workspace_id });
    if (application.getSnapshot().conversationWorkspace.primaryThreadId !== request.session_id) return;
    const prior = navigation.trail.at(-1);
    const trail = prior?.target === thread.id ? navigation.trail : [];
    updateNavigation({ trail: [...trail, { source: thread.id, target: request.session_id, workspaceId: thread.workspace_id,
      title: thread.title || thread.preview, requestId: request.id }], focus: undefined });
  }
  async function back() {
    const prior = navigation.trail.at(-1);
    await application.invoke("thread.activate", { threadId: prior.source, workspaceId: prior.workspaceId });
    if (application.getSnapshot().conversationWorkspace.primaryThreadId === prior.source) {
      updateNavigation({ trail: navigation.trail.slice(0, -1), focus: prior.requestId });
    }
  }
  function Icon({ kind, className }) {
    return h("svg", { className, viewBox: "0 0 24 24", width: 20, height: 20, fill: "none", stroke: "currentColor",
      strokeWidth: 1.6, strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": true },
      h("path", { d: kind === "back" ? "m12 19-7-7 7-7M5 12h14" : kind === "next" ? "m9 5 7 7-7 7"
        : "M21 11.5a8.4 8.4 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.4 8.4 0 0 1-3.8-.9L3 21l1.9-5.7a8.4 8.4 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.4 8.4 0 0 1 3.8-.9h.5a8.5 8.5 0 0 1 8 8z" }));
  }
  function Card({ request, thread, failed, translate: t }) {
    const nav = useNavigation();
    const ref = React.useRef(null);
    const title = request.name?.trim() || t("peers.untitled");
    const state = failed ? "unknown" : !request.available ? "unavailable" : request.state;
    const label = t(`peers.${state}`);
    React.useEffect(() => {
      if (nav.focus === request.id) ref.current?.focus({ preventScroll: true });
    }, [nav.focus, request.id]);
    return h("button", { ref, type: "button", className: "plugin-peer-card", "data-state": state, title,
      disabled: !request.available, "aria-label": `${t("peers.open")}: ${title}, ${label}`,
      onClick: () => run(() => open(request, thread)) },
      h(Icon, { className: "plugin-peer-icon" }),
      h("span", { className: "plugin-peer-copy" },
        h("span", { className: "plugin-peer-title" }, title),
        h("span", { className: "plugin-peer-status" }, h("span", { className: "plugin-peer-dot", "aria-hidden": true }), label)),
      request.available ? h(Icon, { kind: "next", className: "plugin-peer-chevron" }) : null);
  }
  function Cards({ context, translate }) {
    const snapshot = useApplication();
    const workspace = snapshot.conversationWorkspace;
    const thread = workspace.threads.find(item => item.id === context.threadId);
    const visible = context.threadId === workspace.primaryThreadId || context.threadId === workspace.secondaryThreadId;
    const [value, setValue] = React.useState(empty);
    React.useEffect(() => {
      setValue(empty);
      if (thread && visible) return observe(thread, setValue);
    }, [thread?.id, thread?.cwd, thread?.workspace_id, visible]);
    if (!thread || !visible) return null;
    return value.requests.filter(request => request.source_turn_id === context.turnId).map(request =>
      h(Card, { key: request.id, request, thread, failed: value.failed, translate }));
  }
  function Header({ children, translate: t }) {
    const snapshot = useApplication();
    const nav = useNavigation();
    const prior = nav.trail.at(-1);
    if (!prior || snapshot.conversationWorkspace.primaryThreadId !== prior.target) return children;
    const title = `${t("peers.back")}: ${prior.title || t("peers.untitled")}`;
    return h("div", { className: "plugin-peer-heading" },
      h("button", { type: "button", className: "plugin-peer-back", title, "aria-label": title, onClick: () => run(back) },
        h(Icon, { kind: "back" }), h("span", null, t("peers.back"))), children);
  }
  api.registerUIContribution({ id: "request-cards", target: "wuu.default-conversation/conversation.turn.after",
    render: props => h(Cards, props) });
  api.registerUIContribution({ id: "return-to-source", target: "wuu.default-shell/header.conversation", mode: "wrap",
    render: props => h(Header, props) });
  api.registerCSSSnippet({ id: "peer-cards", css: `
    .plugin-peer-card {
      display: flex; align-items: center; gap: var(--space-2); width: 100%; min-width: 0;
      box-sizing: border-box; margin: var(--space-3) 0 0;
      padding: var(--conversation-framed-text-inset-block) var(--card-padding); border: 0;
      border-radius: var(--user-message-radius); outline: 1px solid var(--hairline-soft); outline-offset: -1px;
      background: var(--message-flow-card-bg); color: var(--ink);
      font: inherit; font-size: var(--conversation-message-font-size); line-height: var(--conversation-process-line-height);
      text-align: start; cursor: pointer; transition: background-color 140ms ease;
    }
    .plugin-peer-card:hover:not(:disabled) { background: var(--message-flow-card-hover-bg); }
    .plugin-peer-card:active:not(:disabled) { background: var(--surface-3); }
    .plugin-peer-card:focus-visible, .plugin-peer-back:focus-visible { outline: 2px solid var(--focus-ring); outline-offset: 3px; }
    .plugin-peer-icon { flex: none; width: var(--icon-size); height: var(--icon-size); color: var(--ink-soft); }
    .plugin-peer-copy { display: flex; flex: 1; align-items: center; min-width: 0; min-height: calc(var(--conversation-process-line-height) + 6px); gap: var(--space-3); }
    .plugin-peer-title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: var(--weight-medium); }
    .plugin-peer-status { display: flex; flex: none; align-items: center; gap: 7px; color: var(--ink-soft); font-weight: 400; white-space: nowrap; }
    .plugin-peer-dot { width: 6px; height: 6px; border-radius: 50%; flex: none; background: currentColor; }
    .plugin-peer-card[data-state="running"] .plugin-peer-dot { background: var(--wuu-color-accent, var(--info)); }
    .plugin-peer-card[data-state="failed"] .plugin-peer-dot { background: var(--danger); }
    .plugin-peer-chevron { flex: none; width: var(--icon-size-xs); height: var(--icon-size-xs); color: var(--ink-soft); }
    .plugin-peer-card:disabled { cursor: default; }
    .plugin-peer-heading { display: flex; align-items: center; gap: var(--space-3); min-width: 0; flex: 1; }
    .plugin-peer-back { display: inline-flex; align-items: center; gap: 5px; flex: none; padding: 6px 8px;
      color: var(--ink-soft); font: inherit; background: transparent; border: 0; border-radius: var(--radius-sm); cursor: pointer; }
    .plugin-peer-back:hover { color: var(--ink); background: var(--message-flow-card-hover-bg); }
    @media (max-width: 700px) { .plugin-peer-back span { display: none; } .plugin-peer-heading { gap: var(--space-2); } }
    @media (prefers-reduced-motion: reduce) { .plugin-peer-card { transition: none; } }
  ` });
}
