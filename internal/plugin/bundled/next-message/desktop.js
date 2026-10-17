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
  function invalidate(key) {
    const previous = turns.get(key);
    previous?.controller?.abort();
    turns.delete(key);
  }
  function invalidateRoot(root) {
    for (const [key, value] of turns) if (value.root === root) invalidate(key);
  }
  function refreshObserved(key) {
    const observed = observations.get(key);
    if (observed) provider.refresh({ composerId: observed.composerId, revision: observed.revision });
  }
  function predict(key, current, text) {
    const controller = new AbortController();
    current.controller = controller;
    current.state = "pending";
    current.promise = (async () => {
      try {
        const latest = await api.listWorkspaces();
        if (disposed || controller.signal.aborted || turns.get(key) !== current) return null;
        workspaces = latest.workspaces;
        const candidates = workspaces.filter((workspace) => workspace.available && workspace.root === current.root);
        if (current.workspaceId) {
          if (!candidates.some((workspace) => workspace.id === current.workspaceId)) return null;
        } else if (candidates.length === 1) {
          current.workspaceId = candidates[0].id;
        } else if (candidates.length > 1) {
          return null;
        }
        const history = await api.readVisibleHistory({
          kind: current.sideThreadId ? "side" : "main", threadId: current.threadId,
          sideThreadId: current.sideThreadId, workspaceRoot: current.root, workspaceId: current.workspaceId,
          ...(current.sideThreadId ? {} : { beforeTurnId: current.turnId }),
        });
        if (disposed || controller.signal.aborted || turns.get(key) !== current) return null;
        const turnsForModel = current.sideThreadId
          ? history.turns : [...history.turns, { id: current.turnId, ...text }];
        if (!turnsForModel.length || turnsForModel.at(-1)?.id !== current.turnId) return null;
        const result = await api.invokeRuntime("next-message.predict", {
          threadId: current.sideThreadId ?? current.threadId, turnId: current.turnId, turns: turnsForModel,
        }, { workspaceId: current.workspaceId, workspaceRoot: current.root, signal: controller.signal });
        if (disposed || controller.signal.aborted || turns.get(key) !== current) return null;
        return validSuggestion(result);
      } catch {
        // Optional predictions never interrupt composition or expose model text
        // through diagnostics. The runtime owns bounded inference and usage.
        return null;
      }
    })().then((suggestion) => {
      if (disposed || controller.signal.aborted || turns.get(key) !== current) return null;
      current.state = suggestion === null ? "spent" : "ready";
      current.suggestion = suggestion;
      delete current.controller;
      refreshObserved(key);
      return suggestion;
    });
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
        current.turnId = params.message.id;
        if (params.message.status === "completed" && typeof params.message.text === "string"
          && params.message.text.trim()) predict(key, current);
        else current.state = "spent";
        refreshObserved(key);
      }
      return;
    }
    // A new conversation can resume its snapshot after its first live turn
    // starts. Keep only that exact in-progress turn; restored history cannot
    // arm a suggestion without a live start event.
    if (method === "thread/resumed") {
      const snapshot = params?.thread;
      if (typeof snapshot?.id !== "string" || !Array.isArray(snapshot.turns)) {
        invalidateRoot(root); provider.refresh(); return;
      }
      let changed = false;
      for (const [key, current] of turns) {
        if (current.root !== root || current.threadId !== snapshot.id) continue;
        if (current.sideThreadId) continue;
        const source = snapshot.turns.find((turn) => turn?.id === current.turnId);
        const latest = snapshot.turns.at(-1);
        const matches = source && latest?.id === current.turnId
          && (current.state === "running" ? source.status === "in_progress" : source.status === "completed");
        if (!matches) { invalidate(key); changed = true; }
      }
      if (changed) provider.refresh();
      return;
    }
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
      if (text) predict(key, current, text);
      // A completion notification can also change the host's running revision.
      // Refresh only the observation seen before the event: an unscoped delayed
      // refresh would abort the request that the new idle revision just started.
      refreshObserved(key);
    }
  });
  const provider = api.registerComposerInlineCompletionProvider({
    id: "next-message",
    onDidInteract(event) {
      if (event.type !== "accepted" && event.type !== "dismissed") return;
      let identity;
      try { identity = JSON.parse(event.suggestionId); } catch { return; }
      if (!Array.isArray(identity) || identity.length !== 4) return;
      const [root, threadId, sideThreadId, turnId] = identity;
      const current = turns.get(keyFor(root, threadId, sideThreadId));
      if (current?.turnId === turnId && current.state === "ready") current.state = "spent";
    },
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
      if (!current || (context.kind === "main" && current.workspaceId !== context.workspaceId)) return null;
      const suggestion = current.state === "pending" ? await current.promise
        : current.state === "ready" ? current.suggestion : null;
      if (disposed || signal.aborted || turns.get(key) !== current || !suggestion) return null;
      return { id: JSON.stringify([context.workspaceRoot, context.threadId, context.sideThreadId, current.turnId]), text: suggestion };
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
    "nextMessage.model": "Suggestion model", "nextMessage.choose": "Choose a model",
    "nextMessage.variant": "Reasoning level", "nextMessage.defaultVariant": "Model default",
    "nextMessage.fixedVariant": "Set by the model alias",
    "nextMessage.level.none": "None", "nextMessage.level.minimal": "Minimal",
    "nextMessage.level.low": "Low", "nextMessage.level.medium": "Medium",
    "nextMessage.level.high": "High", "nextMessage.level.xhigh": "Very high",
    "nextMessage.level.max": "Max", "nextMessage.level.ultra": "Ultra",
    "nextMessage.invalidVariant": "This level is no longer available for the selected model. Choose another level.",
    "nextMessage.slowVariant": "Higher reasoning levels may exceed the 30-second suggestion limit.",
    "nextMessage.modelHelp": "Choose and save a model to enable suggestions.",
    "nextMessage.noModels": "Configure a model provider before enabling suggestions.",
    "nextMessage.removedModel": "The selected model is no longer configured. Choose another model.",
    "nextMessage.aliases": "Model aliases",
    "nextMessage.dataSummary": "Recent visible conversation text is sent to the selected model provider and may incur charges.",
    "nextMessage.workspaceRequired": "Open a conversation before configuring suggestions.",
    "nextMessage.workspaceChanged": "The workspace changed. Reopen these settings before saving.",
    "nextMessage.save": "Save changes", "nextMessage.saved": "Saved",
  } });
  api.registerLocale({ id: "next-message-zh", locale: "zh-CN", entries: {
    "nextMessage.title": "下一句建议",
    "nextMessage.help": "回复完成后，在空输入框显示建议。Tab 填入草稿，Escape 取消。",
    "nextMessage.model": "建议模型", "nextMessage.choose": "选择模型",
    "nextMessage.variant": "推理档位", "nextMessage.defaultVariant": "模型默认",
    "nextMessage.fixedVariant": "由模型别名决定",
    "nextMessage.level.none": "无", "nextMessage.level.minimal": "极低",
    "nextMessage.level.low": "低", "nextMessage.level.medium": "中",
    "nextMessage.level.high": "高", "nextMessage.level.xhigh": "超高",
    "nextMessage.level.max": "最高", "nextMessage.level.ultra": "极高",
    "nextMessage.invalidVariant": "所选档位已不受此模型支持，请重新选择。",
    "nextMessage.slowVariant": "较高的推理档位可能超过建议的 30 秒时限。",
    "nextMessage.modelHelp": "请选择并保存一个模型，之后才会生成建议。",
    "nextMessage.noModels": "请先配置模型提供商，再启用建议。",
    "nextMessage.removedModel": "所选模型已不在配置中，请重新选择。",
    "nextMessage.aliases": "模型别名",
    "nextMessage.dataSummary": "预测会把近期可见对话文字发送给所选模型提供商，并可能产生费用。",
    "nextMessage.workspaceRequired": "请先打开一个会话，再配置下一句建议。",
    "nextMessage.workspaceChanged": "工作区已变化，请重新打开此设置页后再保存。",
    "nextMessage.save": "保存更改", "nextMessage.saved": "已保存",
  } });
  api.registerStyle({ id: "next-message-settings", css: `
    .plugin-next-message .plugin-next-message-model-row { display:grid; width:100%; max-width:48ch; grid-template-columns:repeat(2, minmax(0, 1fr)); gap:var(--space-3); align-items:end; }
    .plugin-next-message .plugin-next-message-model-help,
    .plugin-next-message .plugin-next-message-data-summary { color: var(--ink-soft); font-size: var(--font-ui); line-height: 1.5; }
    @media (max-width: 760px) { .plugin-next-message .plugin-next-message-model-row { grid-template-columns:minmax(0, 1fr); } }
  ` });
  function Settings({ host, translate: tr }) {
    const levelLabel = (level) => ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"].includes(level)
      ? tr(`nextMessage.level.${level}`) : level;
    const models = host.settings?.listAvailableModels?.() ?? [];
    const aliases = Object.entries(host.settings?.getValue("runtime.modelAliases") ?? {}).sort(([a], [b]) => a.localeCompare(b));
    const [selection, setSelection] = React.useState(null);
    const [variant, setVariant] = React.useState(null);
    const [workspaceId, setWorkspaceId] = React.useState(null);
    const [workspaceRoot, setWorkspaceRoot] = React.useState(null);
    const [busy, setBusy] = React.useState(false);
    const [error, setError] = React.useState("");
    const [saved, setSaved] = React.useState(false);
    const selectedModel = models.find((model) => JSON.stringify([model.provider, model.model]) === selection);
    const selectedAlias = aliases.find(([name]) => `alias:${name}` === selection);
    const availableVariants = selectedModel?.variants ?? [];
    const selectedVariant = variant === undefined ? availableVariants.includes("low") ? "low" : "" : variant;
    const validVariant = selectedVariant === "" || availableVariants.includes(selectedVariant);
    const aliasVariant = selectedAlias?.[1].variant || selectedAlias?.[1].effort || "";
    React.useEffect(() => {
      const controller = new AbortController();
      void (async () => {
        const snapshot = await api.listWorkspaces();
        const workspace = snapshot.workspaces.find((entry) => entry.id === snapshot.activeWorkspaceId && entry.available);
        const root = workspace?.root ?? snapshot.activeRoot;
        if (!root) throw new Error(tr("nextMessage.workspaceRequired"));
        const value = await api.invokeRuntime("next-message.get", {}, { workspaceId: workspace?.id, workspaceRoot: root, signal: controller.signal });
        if (!controller.signal.aborted) {
          setWorkspaceId(workspace?.id ?? ""); setWorkspaceRoot(root);
          setSelection(value.settings.model_alias ? `alias:${value.settings.model_alias}`
            : value.settings.provider && value.settings.model ? JSON.stringify([value.settings.provider, value.settings.model]) : "");
          setVariant(Object.hasOwn(value.settings, "variant") ? value.settings.variant
            : undefined);
        }
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
        if (!selectedModel && !selectedAlias) throw new Error(tr("nextMessage.removedModel"));
        if (selectedModel && !validVariant) throw new Error(tr("nextMessage.invalidVariant"));
        const input = selectedAlias ? { model_alias: selectedAlias[0] }
          : { provider: selectedModel.provider, model: selectedModel.model, variant: selectedVariant };
        const value = await api.invokeRuntime("next-message.update", input, { workspaceId: workspaceId || undefined, workspaceRoot });
        setSelection(value.settings.model_alias ? `alias:${value.settings.model_alias}`
          : JSON.stringify([value.settings.provider, value.settings.model]));
        setVariant(value.settings.variant ?? ""); setSaved(true);
        // A settings change does not retry old turns or preserve old results.
        for (const key of turns.keys()) invalidate(key);
        provider.refresh();
      } catch (reason) { setError(String(reason)); } finally { setBusy(false); }
    }
    return h(Page, { className: "plugin-next-message" },
      h(Section, { title: tr("nextMessage.title"), description: tr("nextMessage.help") },
        h("div", { className: "plugin-next-message-model-row" },
          h("label", { className: "plugin-ui-field" }, h("span", { className: "plugin-ui-field-label" }, tr("nextMessage.model")),
            h("select", { className: "plugin-ui-input", value: selection ?? "", disabled: busy || selection === null || (models.length === 0 && aliases.length === 0),
              onChange: (event) => {
                setSelection(event.target.value);
                const model = models.find((entry) => JSON.stringify([entry.provider, entry.model]) === event.target.value);
                setVariant(model?.variants.includes("low") ? "low" : "");
                setSaved(false);
              } },
              h("option", { value: "", disabled: true }, tr("nextMessage.choose")),
              ...[...new Set(models.map((model) => model.provider))].map((provider) =>
                h("optgroup", { key: provider, label: provider }, models.filter((model) => model.provider === provider).map((model) =>
                  h("option", { key: model.model, value: JSON.stringify([model.provider, model.model]) }, model.label)))),
              aliases.length ? h("optgroup", { label: tr("nextMessage.aliases") }, aliases.map(([name]) =>
                h("option", { key: name, value: `alias:${name}` }, name))) : null)),
          h("label", { className: "plugin-ui-field" }, h("span", { className: "plugin-ui-field-label" }, tr("nextMessage.variant")),
            h("select", { className: "plugin-ui-input", value: selectedAlias ? "" : selectedVariant ?? "",
              disabled: busy || variant === null || !selectedModel || availableVariants.length === 0,
              onChange: (event) => { setVariant(event.target.value); setSaved(false); } },
              h("option", { value: "" }, selectedAlias
                ? `${tr("nextMessage.fixedVariant")}${aliasVariant ? ` (${levelLabel(aliasVariant)})` : ""}` : tr("nextMessage.defaultVariant")),
              ...availableVariants.map((level) => h("option", { key: level, value: level }, levelLabel(level)))))),
        !selectedModel && !selectedAlias ? h("div", { className: "plugin-next-message-model-help" },
          tr(models.length || aliases.length ? selection ? "nextMessage.removedModel" : "nextMessage.modelHelp" : "nextMessage.noModels")) : null,
        selectedModel && !validVariant ? h("div", { role: "alert" }, tr("nextMessage.invalidVariant")) : null,
        selectedModel && ["high", "xhigh", "max", "ultra"].includes(selectedVariant)
          ? h("div", { className: "plugin-next-message-model-help" }, tr("nextMessage.slowVariant")) : null,
        h(Row, null, h(Button, { variant: "primary", disabled: busy || selection === null || (!selectedModel && !selectedAlias) || (selectedModel && !validVariant), onClick: () => void save() }, tr("nextMessage.save")),
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
