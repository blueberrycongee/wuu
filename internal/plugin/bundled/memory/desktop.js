export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const { Page, Section, Stack, EmptyState } = api.ui;

  api.registerLocale({ id: "memory-en", locale: "en-US", entries: {
    "memory.raw": "Notebook files",
    "memory.empty": "The memory notebook is empty.",
    "memory.readFailed": "Could not read memory files. Try again."
  }});
  api.registerLocale({ id: "memory-zh", locale: "zh-CN", entries: {
    "memory.raw": "笔记本原文",
    "memory.empty": "记忆笔记本还是空的。",
    "memory.readFailed": "无法读取记忆文件，请重试。"
  }});
  api.registerStyle({ id: "memory-settings", css: `
    .plugin-memory { min-width:0; }

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

  function MemorySettings(props) {
    const tr = props.translate;
    const [raw, setRaw] = React.useState({ index_raw: "", files: [] });
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
    React.useEffect(() => { void refreshRaw(); }, [refreshRaw]);

    const files = [];
    if (raw.index_raw) files.push({ name: "MEMORY.md", content: raw.index_raw });
    files.push(...raw.files);
    return h(Page, { className: "plugin-memory" }, h(Stack, { gap: "large" },
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
