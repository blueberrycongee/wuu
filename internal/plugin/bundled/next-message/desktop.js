// Policy stays in the plugin. The host owns focus, keyboard priority, ghost
// rendering, revisions, and cancellation of the public completion provider.
export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const { Page, Section, Row, Button } = api.ui;
  const turns = new Map();
  const observations = new Map();
  let workspaces = [];
  let disposed = false;
  const keyFor = (root, thread, side) => JSON.stringify([root, thread, side ?? null]);
  const registered = (root, id) => workspaces.some((workspace) => workspace.available
    && workspace.root === root && (id === undefined || workspace.id === id));
  function invalidate(key) {
    const previous = turns.get(key);
    previous?.controller?.abort();
    turns.delete(key);
  }
  function invalidateRoot(root) {
    for (const [key, value] of turns) if (value.root === root) invalidate(key);
  }
  try { workspaces = (await api.listWorkspaces()).workspaces; } catch { /* Missing workspace access means no prediction. */ }
  api.signal.throwIfAborted();
  api.onHostEvent((event) => {
    if (disposed) return;
    if (event?.kind === "connection-reset") {
      for (const key of turns.keys()) invalidate(key);
      observations.clear(); provider.refresh(); return;
    }
    if (typeof event?.workdir !== "string" || event.replayed === true) return;
    const root = event.workdir;
    if (event.kind === "server-exit" || event.kind === "server-error") {
      invalidateRoot(root); provider.refresh(); return;
    }
    if (event.kind !== "notification") return;
    const { method, params } = event.message ?? {};
    if (method === "sideThread/event") {
      const sideId = params?.side_thread_id;
      const mainId = params?.main_thread_id;
      if (typeof sideId !== "string" || typeof mainId !== "string") return;
      const key = keyFor(root, mainId, sideId);
      if (params.type === "reset") { invalidate(key); provider.refresh(); return; }
      if (params.type === "status" && params.summary?.status === "running"
        && Number.isSafeInteger(params.summary.revision)) {
        invalidate(key);
        turns.set(key, { root, threadId: mainId, sideThreadId: sideId,
          revision: params.summary.revision, state: "running" });
        while (turns.size > 128) invalidate(turns.keys().next().value);
      } else if (params.type === "message" && params.message?.role === "assistant") {
        const current = turns.get(key);
        if (!current || current.state !== "running" || typeof params.message.id !== "string"
          || !Number.isSafeInteger(params.revision) || params.revision < current.revision) return;
        current.state = params.message.status === "completed" && typeof params.message.text === "string"
          && params.message.text.trim() ? "ready" : "spent";
        current.turnId = params.message.id;
        const observed = observations.get(key);
        if (observed) provider.refresh({ composerId: observed.composerId, revision: observed.revision });
      }
      return;
    }
    // History restoration and reconnect must never masquerade as live success.
    if (method === "thread/resumed") { invalidateRoot(root); provider.refresh(); return; }
    const threadId = params?.thread_id;
    if (typeof threadId !== "string") return;
    const key = keyFor(root, threadId);
    const turn = params.turn;
    if (method === "turn/started") {
      if (typeof turn?.id !== "string" || turns.get(key)?.turnId === turn.id) return;
      invalidate(key);
      const candidates = workspaces.filter((workspace) => workspace.available && workspace.root === root);
      // Root-only host events cannot prove a late retired runtime belongs to a
      // newly registered ID. Prefer the existing registration; disagreement
      // with the visible composer must abstain rather than reattribute text.
      const workspaceId = candidates.length === 1 ? candidates[0].id : observations.get(key)?.workspaceId;
      turns.set(key, { root, workspaceId, threadId, turnId: turn.id, state: "running" });
      // Keep only live, recent identities. Evicted completions cannot re-arm
      // themselves: a matching start is always required.
      while (turns.size > 128) invalidate(turns.keys().next().value);
    } else if (method === "turn/error") {
      invalidate(key); provider.refresh();
    } else if (method === "turn/completed") {
      const current = turns.get(key);
      if (!current || current.state !== "running" || current.turnId !== turn?.id) return;
      current.state = "spent";
      const text = visibleTurnText(turn);
      if (text) { current.state = "ready"; current.text = text; }
      // A completion notification can also change the host's running revision.
      // Refresh only the observation seen before the event: an unscoped delayed
      // refresh would abort the request that the new idle revision just started.
      const observed = observations.get(key);
      if (observed) provider.refresh({ composerId: observed.composerId, revision: observed.revision });
    }
  });
  const provider = api.registerComposerInlineCompletionProvider({
    id: "next-message",
    async provideCompletion(context, signal) {
      if (context.workspaceRoot && context.threadId && (context.kind === "main" || context.sideThreadId)) {
        const observationKey = keyFor(context.workspaceRoot, context.threadId, context.sideThreadId);
        const observation = { composerId: context.composerId, revision: context.revision, workspaceId: context.workspaceId };
        observations.set(observationKey, observation);
        while (observations.size > 128) observations.delete(observations.keys().next().value);
        signal.addEventListener("abort", () => {
          if (observations.get(observationKey) === observation) observations.delete(observationKey);
        }, { once: true });
      }
      if (disposed || signal.aborted || (context.kind === "side" && !context.sideThreadId)
        || context.running || context.draftText !== "" || !context.threadId || !context.workspaceRoot
        || context.attachmentCount !== 0 || context.queuedCount !== 0 || context.pendingCount !== 0) return null;
      const key = keyFor(context.workspaceRoot, context.threadId, context.sideThreadId);
      const current = turns.get(key);
      if (current?.state !== "ready" || (context.kind === "main" && current.workspaceId !== context.workspaceId)) return null;
      // Consumed before any await: null, errors and abort never purchase
      // another attempt for this source turn.
      current.state = "spent";
      const text = current.text;
      delete current.text;
      const controller = new AbortController();
      current.controller = controller;
      const abort = () => controller.abort(signal.reason);
      signal.addEventListener("abort", abort, { once: true });
      try {
        if (context.workspaceId) {
          const latest = await api.listWorkspaces();
          if (disposed || signal.aborted || controller.signal.aborted || turns.get(key) !== current) return null;
          workspaces = latest.workspaces;
          if (!registered(context.workspaceRoot, context.workspaceId)) return null;
        }
        const history = await api.readVisibleHistory({
          kind: context.kind, threadId: context.threadId, sideThreadId: context.sideThreadId,
          workspaceRoot: context.workspaceRoot, workspaceId: context.workspaceId,
          ...(context.kind === "main" ? { beforeTurnId: current.turnId } : {}),
        });
        if (disposed || signal.aborted || controller.signal.aborted || turns.get(key) !== current) return null;
        const turnsForModel = context.kind === "main"
          ? [...history.turns, { id: current.turnId, ...text }]
          : history.turns;
        if (!turnsForModel.length || turnsForModel.at(-1)?.id !== current.turnId) return null;
        const result = await api.invokeRuntime("next-message.predict", {
          threadId: context.sideThreadId ?? context.threadId, turnId: current.turnId, turns: turnsForModel,
        }, { workspaceId: context.workspaceId, workspaceRoot: context.workspaceRoot, signal: controller.signal });
        if (disposed || signal.aborted || controller.signal.aborted || turns.get(key) !== current) return null;
        const suggestion = validSuggestion(result);
        return suggestion === null ? null : { id: JSON.stringify([context.workspaceRoot, context.threadId, context.sideThreadId, current.turnId]), text: suggestion };
      } catch {
        // Optional predictions never interrupt composition or expose model text
        // through diagnostics. The runtime owns bounded inference and usage.
        return null;
      } finally {
        signal.removeEventListener("abort", abort);
        if (current.controller === controller) delete current.controller;
      }
    },
  });
  api.registerCleanup(() => {
    disposed = true;
    observations.clear();
    for (const key of turns.keys()) invalidate(key);
  });
  api.registerLocale({ id: "next-message-en", locale: "en-US", entries: {
    "nextMessage.title": "Next message",
    "nextMessage.help": "After a reply, suggest a message in the empty composer. Tab inserts it; Escape dismisses it.",
    "nextMessage.model": "Suggestion model", "nextMessage.inherit": "Follow workspace main model",
    "nextMessage.modelHelp": "Leave blank to follow the workspace's current main model. A configured alias stays selected when the main model changes.",
    "nextMessage.dataSummary": "Recent visible conversation text is sent to the selected model provider and may incur charges.",
    "nextMessage.workspaceRequired": "Open a conversation before configuring suggestions.",
    "nextMessage.workspaceChanged": "The workspace changed. Reopen these settings before saving.",
    "nextMessage.save": "Save changes", "nextMessage.saved": "Saved",
  } });
  api.registerLocale({ id: "next-message-zh", locale: "zh-CN", entries: {
    "nextMessage.title": "下一句建议",
    "nextMessage.help": "回复完成后，在空输入框显示建议。Tab 填入草稿，Escape 取消。",
    "nextMessage.model": "建议模型", "nextMessage.inherit": "跟随工作区主模型",
    "nextMessage.modelHelp": "留空时跟随工作区当前主模型；填写已配置的模型别名后，切换主模型不会改变这里的选择。",
    "nextMessage.dataSummary": "预测会把近期可见对话文字发送给所选模型提供商，并可能产生费用。",
    "nextMessage.workspaceRequired": "请先打开一个会话，再配置下一句建议。",
    "nextMessage.workspaceChanged": "工作区已变化，请重新打开此设置页后再保存。",
    "nextMessage.save": "保存更改", "nextMessage.saved": "已保存",
  } });
  api.registerStyle({ id: "next-message-settings", css: `
    .plugin-next-message .plugin-next-message-model-help,
    .plugin-next-message .plugin-next-message-data-summary { color: var(--ink-soft); font-size: var(--font-ui); line-height: 1.5; }
  ` });
  function Settings({ translate: tr }) {
    const [alias, setAlias] = React.useState(null);
    const [workspaceId, setWorkspaceId] = React.useState(null);
    const [workspaceRoot, setWorkspaceRoot] = React.useState(null);
    const [busy, setBusy] = React.useState(false);
    const [error, setError] = React.useState("");
    const [saved, setSaved] = React.useState(false);
    React.useEffect(() => {
      const controller = new AbortController();
      void (async () => {
        const snapshot = await api.listWorkspaces();
        const workspace = snapshot.workspaces.find((entry) => entry.id === snapshot.activeWorkspaceId && entry.available);
        const root = workspace?.root ?? snapshot.activeRoot;
        if (!root) throw new Error(tr("nextMessage.workspaceRequired"));
        const value = await api.invokeRuntime("next-message.get", {}, { workspaceId: workspace?.id, workspaceRoot: root, signal: controller.signal });
        if (!controller.signal.aborted) { setWorkspaceId(workspace?.id ?? ""); setWorkspaceRoot(root); setAlias(value.settings.model_alias ?? ""); }
      })().catch((reason) => { if (!controller.signal.aborted) setError(String(reason)); });
      return () => controller.abort();
    }, []);
    async function save() {
      setBusy(true); setError(""); setSaved(false);
      try {
        const snapshot = await api.listWorkspaces();
        if ((snapshot.activeWorkspaceId ?? "") !== workspaceId || snapshot.activeRoot !== workspaceRoot
          || (workspaceId && !snapshot.workspaces.some((entry) => entry.id === workspaceId && entry.available))) {
          throw new Error(tr("nextMessage.workspaceChanged"));
        }
        const value = await api.invokeRuntime("next-message.update", { model_alias: alias }, { workspaceId: workspaceId || undefined, workspaceRoot });
        setAlias(value.settings.model_alias ?? ""); setSaved(true);
        // A settings change does not retry old turns or preserve old results.
        for (const key of turns.keys()) invalidate(key);
        provider.refresh();
      } catch (reason) { setError(String(reason)); } finally { setBusy(false); }
    }
    return h(Page, { className: "plugin-next-message" },
      h(Section, { title: tr("nextMessage.title"), description: tr("nextMessage.help") },
        h("label", { className: "plugin-ui-field" }, h("span", { className: "plugin-ui-field-label" }, tr("nextMessage.model")),
          h("input", { className: "plugin-ui-input", value: alias ?? "", disabled: busy || alias === null,
            placeholder: tr("nextMessage.inherit"), onChange: (event) => { setAlias(event.target.value); setSaved(false); } })),
        h("div", { className: "plugin-next-message-model-help" }, tr("nextMessage.modelHelp")),
        h(Row, null, h(Button, { variant: "primary", disabled: busy || alias === null, onClick: () => void save() }, tr("nextMessage.save")),
          saved ? h("span", { role: "status" }, tr("nextMessage.saved")) : null,
          error ? h("span", { role: "alert" }, error) : null),
        h("div", { className: "plugin-next-message-data-summary" }, tr("nextMessage.dataSummary"))));
  }
  api.registerViewType({ id: "next-message.settings", title: "Next message", icon: "message-square",
    defaultRegion: "settings", persistence: "durable", render: (props) => h(Settings, props) });
}

function visibleTurnText(turn) {
  if (turn.status !== "completed" || (turn.kind !== undefined && turn.kind !== "user")
    || turn.error || turn.truncated || (turn.finish_reason && turn.finish_reason !== "stop")
    || turn.items_view !== "full" || !Array.isArray(turn.items) || turn.items.some((item) => !item || typeof item !== "object")) return null;
  const users = turn.items.filter((item) => item.type === "user_message");
  if (!users.length || users.some((item) => !completeText(item, true) || item.read_only
    || (item.origin && item.origin !== "user") || item.cause || item.presentation_kind)) return null;
  const answer = turn.items.filter((item) => item.type === "agent_message" && item.terminal === true).at(-1);
  if (!answer || !completeText(answer, false)) return null;
  const userText = users.map((item) => item.content_parts
    ? item.content_parts.filter((part) => part.type === "text").map((part) => part.text).join("")
    : item.text).join("\n");
  const imageCount = users.reduce((count, item) => count + (item.images?.length ?? 0) + (item.markdown_images?.length ?? 0), 0);
  const fileCount = users.reduce((count, item) => count + (item.files?.length ?? 0)
    + (item.content_parts?.filter((part) => part?.type === "file_selection").length ?? 0), 0);
  if (!userText.trim()) return null;
  return { userText, assistantText: answer.text, imageCount, fileCount };
}

function completeText(item, allowMedia) {
  return !item.hidden && !item.remote_content_ref && !item.truncated && !item.error
    && (item.status === undefined || item.status === "completed")
    && (!item.finish_reason || item.finish_reason === "stop")
    && typeof item.text === "string" && (allowMedia || Boolean(item.text.trim()))
    && [item.images, item.files, item.markdown_images].every((value) => value === undefined || Array.isArray(value))
    && (allowMedia || [item.images, item.files, item.markdown_images].every((value) => !value?.length))
    && (item.content_parts === undefined || (Array.isArray(item.content_parts)
      && item.content_parts.every((part) => part?.type === "text" || (allowMedia && part?.type === "file_selection"))));
}

function validSuggestion(result) {
  if (!result || typeof result !== "object" || Array.isArray(result)
    || Object.keys(result).length !== 1 || typeof result.suggestion !== "string") return null;
  const text = result.suggestion.trim();
  if (!text || [...text].length > 100 || /[\p{Cc}\u2028\u2029]/u.test(text)) return null;
  return text;
}
