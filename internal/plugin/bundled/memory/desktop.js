export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const { Page, Section, Stack, Row, Button, TextArea, EmptyState } = api.ui;

  api.registerLocale({ id: "memory-en", locale: "en-US", entries: {
    "memory.raw": "Notebook files",
    "memory.empty": "The memory notebook is empty.",
    "memory.chat": "Update memory",
    "memory.message": "What should the Agent remember, correct, or forget?", "memory.send": "Send",
    "memory.changed": "Changed files", "memory.failed": "Memory task failed",
    "memory.thinking": "Updating memory…",
    "memory.readFailed": "Could not read memory files. Try again.", "memory.updateFailed": "Could not update memory. Try again."
  }});
  api.registerLocale({ id: "memory-zh", locale: "zh-CN", entries: {
    "memory.raw": "笔记本原文",
    "memory.empty": "记忆笔记本还是空的。",
    "memory.chat": "更新记忆",
    "memory.message": "希望 Agent 记住、修正或忘记什么？", "memory.send": "发送",
    "memory.changed": "变更文件", "memory.failed": "记忆任务失败",
    "memory.thinking": "正在更新记忆…",
    "memory.readFailed": "无法读取记忆文件，请重试。", "memory.updateFailed": "无法更新记忆，请重试。"
  }});
  api.registerStyle({ id: "memory-settings", css: `
    .plugin-memory { min-width:0; }

    .plugin-memory-chat-log { display:flex; flex-direction:column; gap:var(--space-3); padding-top:var(--space-2); border-top:1px solid var(--hairline-soft); }
    .plugin-memory-chat-entry { display:flex; --enter-y:2px; animation:wuu-enter var(--motion-base) var(--ease-out) both; }
    .plugin-memory-chat-entry.user { justify-content:flex-end; }
    .plugin-memory-chat-bubble { max-width:min(72%, 420px); padding:var(--compact-padding-block) var(--compact-padding-inline); border-radius:var(--radius-md); background:var(--wuu-color-surface-muted, var(--surface-2)); color:var(--ink); font-size:var(--font-ui); line-height:1.55; white-space:pre-wrap; overflow-wrap:anywhere; }
    .plugin-memory-chat-reply { max-width:68ch; margin:0; color:var(--ink); font-size:var(--font-ui); line-height:1.6; white-space:pre-wrap; overflow-wrap:anywhere; }
    .plugin-memory-chat-pending, .plugin-memory-changes { color:var(--ink-tertiary); font-size:var(--font-sm); }
    .plugin-memory-changes summary { cursor:pointer; }
    .plugin-memory-changes ul { margin:var(--space-1) 0 0; padding-left:1.25em; }
    .plugin-memory-changes code { font-size:var(--font-sm); }
    .plugin-memory-composer { flex-direction:column; align-items:stretch; gap:var(--space-2); padding-top:var(--space-3); border-top:1px solid var(--hairline-soft); }
    .plugin-memory-composer .plugin-ui-field { flex:1; min-width:0; }
    .plugin-memory-composer .plugin-ui-field-label { position:absolute; width:1px; height:1px; overflow:hidden; clip-path:inset(50%); }
    .plugin-memory-composer textarea { min-height:88px; max-height:200px; }
    .plugin-memory-composer .plugin-ui-button { flex:none; align-self:flex-end; }

    .plugin-memory-files { display:flex; flex-direction:column; border-top:1px solid var(--hairline-soft); }
    .plugin-memory-file { min-width:0; padding:var(--settings-row-pad-block, 12px) 0; border-bottom:1px solid var(--hairline-soft); }
    .plugin-memory-file-head { display:flex; align-items:center; gap:var(--space-2); min-width:0; cursor:pointer; list-style:none; }
    .plugin-memory-file-head::-webkit-details-marker { display:none; }
    .plugin-memory-file-head::before { content:"›"; color:var(--ink-muted); transition:transform var(--motion-fast) var(--ease-out); }
    .plugin-memory-file[open] .plugin-memory-file-head::before { transform:rotate(90deg); }
    .plugin-memory-file-name { overflow:hidden; color:var(--ink); font-size:var(--font-ui); font-weight:var(--weight-medium); text-overflow:ellipsis; white-space:nowrap; }
    .plugin-memory-file-type { flex:none; margin-left:auto; padding:1px 8px; border-radius:var(--radius-pill); background:var(--wuu-color-surface-muted, var(--surface-2)); color:var(--ink-soft); font-size:var(--font-xs); line-height:1.5; }
    .plugin-memory-file-desc { overflow:hidden; color:var(--ink-tertiary); font-size:var(--font-sm); text-overflow:ellipsis; white-space:nowrap; }
    .plugin-memory-file pre { max-height:300px; margin:var(--space-3) 0 0; padding:var(--compact-padding-block) var(--compact-padding-inline); overflow:auto; border:1px solid var(--hairline); border-radius:var(--radius-sm); background:var(--surface-1); color:var(--ink-soft); white-space:pre-wrap; overflow-wrap:anywhere; font:var(--font-sm)/1.6 var(--wuu-font-family-mono, ui-monospace, monospace); }

  ` });

  const terminalStates = new Set(["completed", "failed", "interrupted", "discarded"]);
  const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  function MemorySettings(props) {
    const tr = props.translate;
    const [raw, setRaw] = React.useState({ index_raw: "", files: [] });
    const [draft, setDraft] = React.useState("");
    const [messages, setMessages] = React.useState([]);
    const [chatBusy, setChatBusy] = React.useState(false);
    const notify = React.useCallback((key, reason) => {
      console.error(`memory ${key}:`, reason);
      if (!api.signal.aborted) api.showNotice({ message: tr(`memory.${key}`), tone: "error", key: `memory-${key}` });
    }, [tr]);

    const refreshRaw = React.useCallback(async () => {
      try {
        const value = await api.invokeRuntime("memory.read", {});
        setRaw({ index_raw: typeof value?.index_raw === "string" ? value.index_raw : "", files: Array.isArray(value?.files) ? value.files : [] });
      } catch (reason) { notify("readFailed", reason); }
    }, [notify]);
    const waitForJob = React.useCallback(async (id) => {
      for (;;) {
        const value = await api.invokeRuntime("memory.job.get", { id });
        if (terminalStates.has(value?.state)) return value;
        await delay(750);
      }
    }, []);
    React.useEffect(() => { void refreshRaw(); }, [refreshRaw]);

    const send = async () => {
      const message = draft.trim();
      if (!message || chatBusy) return;
      const entry = { role: "user", text: message };
      setDraft(""); setMessages((current) => [...current, entry]); setChatBusy(true);
      try {
        const startedJob = await api.invokeRuntime("memory.chat.start", { message });
        const result = await waitForJob(startedJob.id);
        if (result.state !== "completed") throw new Error(result.error || tr("memory.failed"));
        setMessages((current) => [...current, { role: "assistant", text: result.output || "", changed: result.changed_files || [] }]);
      } catch (reason) {
        setMessages((current) => current.filter((item) => item !== entry));
        setDraft((current) => current || message);
        notify("updateFailed", reason);
        setChatBusy(false);
        return;
      }
      setChatBusy(false);
      await refreshRaw();
    };

    const files = [];
    if (raw.index_raw) files.push({ name: "MEMORY.md", content: raw.index_raw });
    files.push(...raw.files);
    return h(Page, { className: "plugin-memory" }, h(Stack, { gap: "large" },
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
