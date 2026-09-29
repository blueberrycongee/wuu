export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const { Page, Section, Stack, Row, Button, TextArea, EmptyState } = api.ui;

  api.registerLocale({ id: "memory-en", locale: "en-US", entries: {
    "memory.overview": "Overview", "memory.refresh": "Regenerate", "memory.refreshing": "Regenerating…",
    "memory.raw": "Notebook files",
    "memory.empty": "The memory notebook is empty.",
    "memory.chat": "Update memory",
    "memory.message": "What should the Agent remember, correct, or forget?", "memory.send": "Send",
    "memory.changed": "Changed files", "memory.failed": "Memory task failed",
    "memory.thinking": "Updating memory…",
    "memory.loadFailed": "Memory overview could not be generated. Try again.", "memory.errorDetails": "Error details"
  }});
  api.registerLocale({ id: "memory-zh", locale: "zh-CN", entries: {
    "memory.overview": "概览", "memory.refresh": "重新生成", "memory.refreshing": "正在生成…",
    "memory.raw": "笔记本原文",
    "memory.empty": "记忆笔记本还是空的。",
    "memory.chat": "更新记忆",
    "memory.message": "希望 Agent 记住、修正或忘记什么？", "memory.send": "发送",
    "memory.changed": "变更文件", "memory.failed": "记忆任务失败",
    "memory.thinking": "正在更新记忆…",
    "memory.loadFailed": "暂时无法生成记忆概览，请重试。", "memory.errorDetails": "错误详情"
  }});
  api.registerStyle({ id: "memory-settings", css: `
    /* The page speaks the settings language: section titles from the kit,
     * prose on the canvas, one hairline group for the notebook files, and
     * the host's entrance, pulse and radius roles. */
    .plugin-memory { min-width:0; }
    .plugin-memory-overview { display:flex; flex-direction:column; gap:var(--space-3); overflow-wrap:anywhere; --enter-y:2px; animation:wuu-enter var(--motion-base) var(--ease-out) both; }
    .plugin-memory-overview h3 { margin:var(--space-2) 0 0; color:var(--ink); font-size:var(--font-ui); font-weight:var(--weight-semibold); line-height:1.4; }
    .plugin-memory-overview h3:first-child { margin-top:0; }
    .plugin-memory-overview p { margin:0; max-width:68ch; color:var(--ink); font-size:var(--font-ui); line-height:1.6; }
    .plugin-memory-overview ul { display:flex; flex-direction:column; gap:var(--space-1); margin:0; padding-left:1.25em; }
    .plugin-memory-overview li { color:var(--ink); font-size:var(--font-ui); line-height:1.55; }
    .plugin-memory-overview li::marker { color:var(--ink-muted); }
    .plugin-memory-overview code { font-family:var(--wuu-font-family-mono, ui-monospace, monospace); font-size:0.92em; }
    .plugin-memory-overview-actions { justify-content:flex-start; }
    .plugin-memory-overview-actions .plugin-ui-button { margin-inline-start:calc(var(--control-padding-inline) * -1); }

    .plugin-memory-skeleton { display:flex; flex-direction:column; gap:var(--space-3); padding:var(--space-1) 0; --pulse-opacity:0.55; animation:wuu-pulse 2.4s ease-in-out infinite; }
    .plugin-memory-skeleton-bar { height:10px; border-radius:var(--radius-xs); background:var(--surface-2); }
    .plugin-memory-skeleton-bar:nth-child(2) { width:82%; }
    .plugin-memory-skeleton-bar:nth-child(3) { width:64%; }
    .plugin-memory-skeleton-bar:nth-child(4) { width:74%; }
    @container style(--motion-reduced: 1) { .plugin-memory-skeleton { animation:none; } }

    .plugin-memory-chat-log { display:flex; flex-direction:column; gap:var(--space-3); }
    .plugin-memory-chat-entry { display:flex; --enter-y:2px; animation:wuu-enter var(--motion-base) var(--ease-out) both; }
    .plugin-memory-chat-entry.user { justify-content:flex-end; }
    .plugin-memory-chat-bubble { max-width:min(72%, 420px); padding:var(--compact-padding-block) var(--compact-padding-inline); border-radius:var(--message-flow-card-radius); background:var(--wuu-color-surface-muted, var(--surface-2)); color:var(--ink); font-size:var(--font-ui); line-height:1.55; white-space:pre-wrap; overflow-wrap:anywhere; }
    .plugin-memory-chat-reply { max-width:68ch; margin:0; color:var(--ink); font-size:var(--font-ui); line-height:1.6; white-space:pre-wrap; overflow-wrap:anywhere; }
    .plugin-memory-chat-pending, .plugin-memory-changes { color:var(--ink-muted); font-size:var(--font-sm); }
    .plugin-memory-changes summary { cursor:pointer; }
    .plugin-memory-changes ul { margin:var(--space-1) 0 0; padding-left:1.25em; }
    .plugin-memory-changes code { font-size:var(--font-sm); }
    .plugin-memory-composer { flex-direction:column; align-items:stretch; gap:var(--space-2); }
    .plugin-memory-composer .plugin-ui-field { flex:1; min-width:0; }
    .plugin-memory-composer .plugin-ui-field-label { position:absolute; width:1px; height:1px; overflow:hidden; clip-path:inset(50%); }
    .plugin-memory-composer textarea { min-height:96px; max-height:200px; }
    .plugin-memory-composer .plugin-ui-button { flex:none; align-self:flex-end; }

    .plugin-memory-files { display:flex; flex-direction:column; border:1px solid var(--hairline); border-radius:var(--radius-sm); padding:0 var(--card-padding); }
    .plugin-memory-file { min-width:0; padding:var(--space-3) 0; border-bottom:1px solid var(--hairline-soft); }
    .plugin-memory-file:last-child { border-bottom:0; }
    .plugin-memory-file-head { display:flex; flex-wrap:wrap; align-items:baseline; gap:var(--space-2); min-width:0; cursor:pointer; list-style:none; }
    .plugin-memory-file-head::-webkit-details-marker { display:none; }
    .plugin-memory-file-head::before { content:"›"; color:var(--ink-muted); transition:transform var(--motion-fast) var(--ease-out); }
    .plugin-memory-file[open] .plugin-memory-file-head::before { transform:rotate(90deg); }
    .plugin-memory-file-name { overflow:hidden; color:var(--ink); font-size:var(--font-ui); text-overflow:ellipsis; white-space:nowrap; }
    .plugin-memory-file-type { flex:none; padding:0 var(--space-2); border-radius:var(--radius-xs); background:var(--wuu-color-surface-muted, var(--surface-2)); color:var(--ink-muted); font-size:var(--font-xs); line-height:1.6; }
    .plugin-memory-file-desc { overflow:hidden; color:var(--ink-muted); font-size:var(--font-sm); text-overflow:ellipsis; white-space:nowrap; }
    .plugin-memory-file pre { max-height:300px; margin:var(--space-3) 0 0; padding:var(--compact-padding-block) var(--compact-padding-inline); overflow:auto; border-radius:var(--radius-sm); background:var(--surface-1); color:var(--ink-soft); white-space:pre-wrap; overflow-wrap:anywhere; font:var(--font-sm)/1.6 var(--wuu-font-family-mono, ui-monospace, monospace); }

    .plugin-memory-error { display:grid; gap:var(--space-1); padding:var(--compact-padding-block) var(--compact-padding-inline); border-radius:var(--radius-sm); background:var(--danger-soft); color:var(--ink); font-size:var(--font-ui); overflow-wrap:anywhere; }
    .plugin-memory-error summary { cursor:pointer; color:var(--ink-soft); font-size:var(--font-sm); }
    .plugin-memory-error pre { max-height:160px; margin:var(--space-1) 0 0; overflow:auto; white-space:pre-wrap; color:var(--ink-soft); font:var(--font-sm)/1.5 var(--wuu-font-family-mono, ui-monospace, monospace); }
  ` });

  const terminalStates = new Set(["completed", "failed", "interrupted", "discarded"]);
  const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  // Render the overview's markdown-ish text as lightweight structure:
  // "##" lines become group titles, "-" lines list items, the rest prose.
  function OverviewProse({ text }) {
    const blocks = [];
    let list = null;
    const flushList = () => { if (list) { blocks.push({ kind: "ul", items: list }); list = null; } };
    for (const rawLine of String(text).split("\n")) {
      const line = rawLine.trim();
      if (!line) { flushList(); continue; }
      const heading = line.match(/^#{1,3}\s+(.*)$/);
      if (heading) { flushList(); blocks.push({ kind: "h3", text: heading[1] }); continue; }
      const item = line.match(/^[-*]\s+(.*)$/);
      if (item) { (list ||= []).push(item[1]); continue; }
      flushList();
      blocks.push({ kind: "p", text: line });
    }
    flushList();
    return h("div", { className: "plugin-memory-overview" }, blocks.map((block, index) => {
      if (block.kind === "h3") return h("h3", { key: index }, inline(block.text));
      if (block.kind === "ul") return h("ul", { key: index }, block.items.map((item, itemIndex) => h("li", { key: itemIndex }, inline(item))));
      return h("p", { key: index }, inline(block.text));
    }));
  }

  // **strong** and `code` spans, so emphasis reads as emphasis instead of
  // literal asterisks.
  function inline(text) {
    return String(text).split(/(\*\*[^*]+\*\*|`[^`]+`)/).filter(Boolean).map((part, index) =>
      part.startsWith("**") && part.endsWith("**") && part.length > 4 ? h("strong", { key: index }, part.slice(2, -2))
        : part.startsWith("`") && part.endsWith("`") && part.length > 2 ? h("code", { key: index }, part.slice(1, -1))
        : part);
  }

  function OverviewSkeleton() {
    return h("div", { className: "plugin-memory-skeleton", "aria-hidden": true },
      h("div", { className: "plugin-memory-skeleton-bar" }),
      h("div", { className: "plugin-memory-skeleton-bar" }),
      h("div", { className: "plugin-memory-skeleton-bar" }),
      h("div", { className: "plugin-memory-skeleton-bar" }));
  }

  function MemorySettings(props) {
    const tr = props.translate;
    const [raw, setRaw] = React.useState({ index_raw: "", files: [] });
    const [overview, setOverview] = React.useState("");
    const [draft, setDraft] = React.useState("");
    const [messages, setMessages] = React.useState([]);
    const [busy, setBusy] = React.useState(false);
    const [chatBusy, setChatBusy] = React.useState(false);
    const [error, setError] = React.useState("");
    const started = React.useRef(false);

    const refreshRaw = React.useCallback(async () => {
      try {
        const value = await api.invokeRuntime("memory.read", {});
        setRaw({ index_raw: typeof value?.index_raw === "string" ? value.index_raw : "", files: Array.isArray(value?.files) ? value.files : [] });
      } catch (reason) { setError(String(reason)); }
    }, []);
    const waitForJob = React.useCallback(async (id) => {
      for (;;) {
        const value = await api.invokeRuntime("memory.job.get", { id });
        if (terminalStates.has(value?.state)) return value;
        await delay(750);
      }
    }, []);
    const refreshOverview = React.useCallback(async () => {
      setBusy(true); setError("");
      try {
        const startedJob = await api.invokeRuntime("memory.overview.start", {});
        const result = await waitForJob(startedJob.id);
        if (result.state !== "completed") throw new Error(result.error || tr("memory.failed"));
        setOverview(result.output || tr("memory.empty"));
        await refreshRaw();
      } catch (reason) { setError(String(reason)); } finally { setBusy(false); }
    }, [refreshRaw, tr, waitForJob]);
    React.useEffect(() => {
      if (started.current) return;
      started.current = true;
      // The raw notebook must render even when the LLM overview fails, so it
      // loads independently instead of being gated behind overview success.
      void refreshRaw();
      void refreshOverview();
    }, [refreshRaw, refreshOverview]);

    const send = async () => {
      const message = draft.trim();
      if (!message || chatBusy) return;
      setDraft(""); setMessages((current) => [...current, { role: "user", text: message }]); setChatBusy(true); setError("");
      try {
        const startedJob = await api.invokeRuntime("memory.chat.start", { message });
        const result = await waitForJob(startedJob.id);
        if (result.state !== "completed") throw new Error(result.error || tr("memory.failed"));
        setMessages((current) => [...current, { role: "assistant", text: result.output || "", changed: result.changed_files || [] }]);
        await refreshRaw();
        await refreshOverview();
      } catch (reason) { setError(String(reason)); } finally { setChatBusy(false); }
    };

    const files = [];
    if (raw.index_raw) files.push({ name: "MEMORY.md", content: raw.index_raw });
    files.push(...raw.files);
    return h(Page, { className: "plugin-memory" }, h(Stack, { gap: "large" },
      h(Section, { title: tr("memory.overview") },
        busy && !overview ? h(OverviewSkeleton) : h(OverviewProse, { text: overview || tr("memory.empty") }),
        h(Row, { className: "plugin-memory-overview-actions" },
          h(Button, { variant: "ghost", disabled: busy, onClick: () => { void refreshRaw(); void refreshOverview(); } },
            tr(busy ? "memory.refreshing" : "memory.refresh")))),
      h(Section, { title: tr("memory.chat") }, h(Stack, null,
        messages.length || chatBusy ? h("div", { className: "plugin-memory-chat-log" },
          messages.map((entry, index) => h("div", { key: index, className: `plugin-memory-chat-entry ${entry.role}` },
            entry.role === "user"
              ? h("div", { className: "plugin-memory-chat-bubble" }, entry.text)
              : h("div", null,
                h("p", { className: "plugin-memory-chat-reply" }, entry.text),
                entry.changed?.length ? h("details", { className: "plugin-memory-changes" },
                  h("summary", null, `${tr("memory.changed")} · ${entry.changed.length}`),
                  h("ul", null, entry.changed.map((item, itemIndex) => h("li", { key: itemIndex }, h("code", null, item.path))))) : null))),
          chatBusy ? h("div", { className: "plugin-memory-chat-entry" }, h("span", { className: "plugin-memory-chat-pending" }, tr("memory.thinking"))) : null) : null,
        h(Row, { className: "plugin-memory-composer" },
          h(TextArea, { label: tr("memory.message"), placeholder: tr("memory.message"), value: draft, disabled: chatBusy, onChange: (event) => setDraft(event.target.value), onKeyDown: (event) => { if ((event.metaKey || event.ctrlKey) && event.key === "Enter") void send(); } }),
          h(Button, { variant: "primary", disabled: chatBusy || !draft.trim(), onClick: () => void send() }, tr("memory.send"))))),
      error ? h("div", { className: "plugin-memory-error", role: "alert" }, h("strong", null, tr("memory.loadFailed")), h("details", null, h("summary", null, tr("memory.errorDetails")), h("pre", null, error))) : null,
      h(Section, { title: tr("memory.raw") },
        files.length ? h("div", { className: "plugin-memory-files" }, files.map((file) => h("details", { className: "plugin-memory-file", key: file.name },
          h("summary", { className: "plugin-memory-file-head" },
            h("span", { className: "plugin-memory-file-name" }, file.name),
            file.type ? h("span", { className: "plugin-memory-file-type" }, file.type) : null,
            file.description ? h("span", { className: "plugin-memory-file-desc" }, file.description) : null),
          h("pre", null, file.content)))) : h(EmptyState, { title: tr("memory.empty") }))
    ));
  }

  api.registerViewType({ id: "memory.settings", title: "Memory", icon: "brain", defaultRegion: "settings", persistence: "durable", render: (props) => h(MemorySettings, props) });
}
