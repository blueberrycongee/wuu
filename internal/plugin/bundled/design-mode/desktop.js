/* Wuu Design Mode — desktop module.
 *
 * A live canvas for HTML design pages: the plugin runtime serves a chosen
 * workspace directory over loopback with an editing bridge injected, this
 * view renders it inside a device frame, and the inspector applies direct
 * manipulations (text, styles, drag moves) and comment pins back through
 * postMessage. Saving serializes the edited document back to the real file;
 * "Send to agent" hands collected comments to a conversation as a revision
 * brief. */

const TEXTUAL = /^(P|H[1-6]|SPAN|A|LI|BUTTON|LABEL|TD|TH|FIGCAPTION|BLOCKQUOTE|STRONG|EM|SMALL|DT|DD|LEGEND|SUMMARY|OPTION)$/;

export async function activate(api) {
  api.registerLocale({ id: "design-mode-en", locale: "en-US", entries: { ...EN_STRINGS } });
  api.registerLocale({ id: "design-mode-zh", locale: "zh-CN", entries: { ...ZH_STRINGS } });
  api.registerStyle({ id: "design-mode", css: DESIGN_CSS });
  api.registerViewType({
    id: "design.studio",
    title: "Design",
    icon: "layout-grid",
    defaultRegion: "primary",
    persistence: "durable",
    render: (props) => api.react.createElement(Studio, { api, ...props }),
  });
}

/** Standalone component preview: `?design-server=http://127.0.0.1:PORT/t/<token>`
 * drives the same Studio through the server's HTTP API instead of invokeRuntime. */
export function preview(api) {
  const params = new URLSearchParams(location.search);
  const devServer = params.get("design-server")?.replace(/\/+$/, "") || undefined;
  const devWorkspace = params.get("design-workspace") || "Design preview";
  return api.react.createElement(Studio, {
    api,
    devServer,
    devWorkspace,
    locale: "en-US",
    translate: (key, values) => translateFrom(EN_STRINGS, key, values),
    host: {
      getStorage: async () => null,
      setStorage: async () => {},
      getSetting: async () => null,
      executeCommand: async () => ({}),
      openView: async () => {},
      closeView: async () => {},
    },
  });
}

const EN_STRINGS = {
  "design.title": "Design",
  "design.workspace": "Workspace",
  "design.page": "Page",
  "design.viewport.mobile": "Mobile",
  "design.viewport.tablet": "Tablet",
  "design.viewport.desktop": "Desktop",
  "design.viewport.fill": "Fill",
  "design.mode.edit": "Edit",
  "design.mode.comment": "Comment",
  "design.save": "Save",
  "design.saving": "Saving…",
  "design.revert": "Revert",
  "design.revert.confirm": "Discard edits?",
  "design.reload": "Reload page",
  "design.external": "Open in browser",
  "design.send": "Send to agent",
  "design.send.done": "Sent {count} comments to the agent.",
  "design.send.failed": "Could not send the comments. The conversation was left unchanged.",
  "design.loading": "Preparing the design canvas…",
  "design.empty.workspaces": "No project workspaces",
  "design.empty.workspacesHelp": "Add a project workspace in Wuu, then open its pages here.",
  "design.empty.pages": "No HTML pages in this workspace",
  "design.empty.pagesHelp": "Ask your agent to sketch a page — for example “Design a landing page in this folder as a single index.html” — then check again.",
  "design.empty.refresh": "Check again",
  "design.error.title": "Design canvas unavailable",
  "design.error.runtime": "The design-mode runtime did not respond. Reload the plugin or check its diagnostics.",
  "design.bridge.lost": "Live editing is unavailable on this page.",
  "design.conflict.title": "The file changed on disk",
  "design.conflict.help": "Another writer — usually the agent — updated this page. Reload to take the disk version, or overwrite it with your canvas edits.",
  "design.conflict.reload": "Load disk version",
  "design.conflict.force": "Overwrite anyway",
  "design.comment.hint": "Click an element to pin a comment · Esc exits",
  "design.comment.title": "Comments",
  "design.comment.placeholder": "What should change here?",
  "design.comment.empty": "Switch to Comment mode and click anywhere on the page to pin feedback for the agent.",
  "design.inspector.element": "Element",
  "design.inspector.styles": "Styles",
  "design.inspector.text": "Text",
  "design.style.color": "Color",
  "design.style.background": "Bg",
  "design.style.font": "Font",
  "design.style.weight": "Weight",
  "design.style.fontSize": "Size",
  "design.style.leading": "Leading",
  "design.style.tracking": "Tracking",
  "design.style.align": "Align",
  "design.style.padding": "Padding",
  "design.style.margin": "Margin",
  "design.style.radius": "Radius",
  "design.style.border": "Border",
  "design.style.stroke": "Stroke",
  "design.style.borderStyle": "Line",
  "design.style.opacity": "Opacity",
  "design.style.width": "Width",
  "design.style.height": "Height",
  "design.viewport.aria": "Viewport",
  "design.newTab.failed": "Could not open a conversation for the revision brief.",
  "design.removeComment": "Remove comment",
  "design.retry": "Try again",
  "design.save.failed": "The page could not be saved.",
  "design.apply.failed": "That edit could not be applied to the page.",
};

const ZH_STRINGS = {
  "design.title": "设计",
  "design.workspace": "工作区",
  "design.page": "页面",
  "design.viewport.mobile": "手机",
  "design.viewport.tablet": "平板",
  "design.viewport.desktop": "桌面",
  "design.viewport.fill": "填满",
  "design.mode.edit": "编辑",
  "design.mode.comment": "评论",
  "design.save": "保存",
  "design.saving": "保存中…",
  "design.revert": "还原",
  "design.revert.confirm": "放弃修改？",
  "design.reload": "重新加载页面",
  "design.external": "在浏览器中打开",
  "design.send": "发给 Agent",
  "design.send.done": "已向 Agent 发送 {count} 条评论。",
  "design.send.failed": "评论未能发送，对话保持不变。",
  "design.loading": "正在准备设计画布…",
  "design.empty.workspaces": "没有可用的项目工作区",
  "design.empty.workspacesHelp": "先在 Wuu 中添加项目工作区，再回到这里打开页面。",
  "design.empty.pages": "此工作区还没有 HTML 页面",
  "design.empty.pagesHelp": "可以让 Agent 先画一版，例如「在这个文件夹里设计一个 landing page，输出单个 index.html」，然后再检查一次。",
  "design.empty.refresh": "重新检查",
  "design.error.title": "设计画布不可用",
  "design.error.runtime": "design-mode 运行时没有响应。请重载插件或查看诊断信息。",
  "design.bridge.lost": "此页面无法使用实时编辑。",
  "design.conflict.title": "磁盘上的文件已变化",
  "design.conflict.help": "通常是 Agent 刚改写了这个页面。可以重新载入磁盘版本，或用画布上的修改覆盖它。",
  "design.conflict.reload": "载入磁盘版本",
  "design.conflict.force": "仍然覆盖",
  "design.comment.hint": "点击元素即可钉上评论 · Esc 退出",
  "design.comment.title": "评论",
  "design.comment.placeholder": "这里需要怎么改？",
  "design.comment.empty": "切到评论模式，点击页面任意位置为 Agent 钉一条反馈。",
  "design.inspector.element": "元素",
  "design.inspector.styles": "样式",
  "design.inspector.text": "文本",
  "design.style.color": "颜色",
  "design.style.background": "底色",
  "design.style.font": "字体",
  "design.style.weight": "字重",
  "design.style.fontSize": "字号",
  "design.style.leading": "行高",
  "design.style.tracking": "字距",
  "design.style.align": "对齐",
  "design.style.padding": "内距",
  "design.style.margin": "外距",
  "design.style.radius": "圆角",
  "design.style.border": "描边",
  "design.style.stroke": "边色",
  "design.style.borderStyle": "线型",
  "design.style.opacity": "不透明",
  "design.style.width": "宽度",
  "design.style.height": "高度",
  "design.viewport.aria": "视口",
  "design.newTab.failed": "无法为修订说明打开对话。",
  "design.removeComment": "删除评论",
  "design.retry": "重试",
  "design.save.failed": "页面未能保存。",
  "design.apply.failed": "修改未能应用到页面。",
};

function translateFrom(table, key, values) {
  let text = table[key] ?? key;
  for (const [name, value] of Object.entries(values ?? {})) text = text.replaceAll(`{${name}}`, String(value));
  return text;
}

// ---------------------------------------------------------------------------
// Backends
// ---------------------------------------------------------------------------

function runtimeBackend(api) {
  return {
    async open(root) {
      return (await api.invokeRuntime("design.open", { root })).base;
    },
    async pages(root) {
      return (await api.invokeRuntime("design.pages", { root })).pages;
    },
    async save(root, input) {
      return api.invokeRuntime("design.save", { root, ...input });
    },
  };
}

function httpBackend(base) {
  return {
    open: async () => base,
    pages: async () => (await (await fetch(`${base}/__design__/pages`)).json()).pages,
    save: async (_root, input) => (await (await fetch(`${base}/__design__/save`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(input),
    })).json()),
  };
}

// ---------------------------------------------------------------------------
// Studio view
// ---------------------------------------------------------------------------

const VIEWPORTS = [
  { id: "mobile", width: 390 },
  { id: "tablet", width: 834 },
  { id: "desktop", width: 1280 },
  { id: "fill", width: null },
];

function Studio({ api, host, context, translate, devServer, devWorkspace }) {
  const React = api.react;
  const h = React.createElement;
  const { useCallback, useEffect, useMemo, useRef, useState } = React;
  const { Button, Select, TextArea, EmptyState, LoadingState, ErrorState } = api.ui;
  const t = useCallback((key, values) => (translate ?? ((k, v) => translateFrom(EN_STRINGS, k, v)))(key, values), [translate]);

  // A `?design-server=` URL hint lets component previews and dev shells drive
  // the view without a runtime; production renders always use invokeRuntime.
  const devBase = devServer ?? (typeof location === "undefined" ? undefined
    : new URLSearchParams(location.search).get("design-server")?.replace(/\/+$/, "") || undefined);
  const backend = useMemo(() => (devBase ? httpBackend(devBase) : runtimeBackend(api)), [api, devBase]);

  const [phase, setPhase] = useState("boot"); // boot | error | ready
  const [bootNonce, setBootNonce] = useState(0);
  const [error, setError] = useState("");
  const [workspaces, setWorkspaces] = useState([]);
  const [workspaceId, setWorkspaceId] = useState(context?.workspaceId ?? "");
  const workspace = workspaces.find((item) => item.id === workspaceId) ?? workspaces[0];
  const [base, setBase] = useState("");
  const [pages, setPages] = useState([]);
  const [path, setPath] = useState("");
  const [sha, setSha] = useState("");
  const [viewport, setViewport] = useState("fill");
  const [mode, setMode] = useState("edit");
  const [selected, setSelected] = useState(null);
  const [textDraft, setTextDraft] = useState("");
  const [ops, setOps] = useState([]);
  const [comments, setComments] = useState([]);
  const [conflict, setConflict] = useState(null);
  const [busy, setBusy] = useState(""); // "" | "save" | "send" | "reload"
  const [bridgeReady, setBridgeReady] = useState(false);
  const [bridgeLost, setBridgeLost] = useState(false);
  const [revertArmed, setRevertArmed] = useState(false);
  const [frameKey, setFrameKey] = useState(0);

  const frameRef = useRef(null);
  const bridgeRef = useRef({ nonce: "", ready: false, pending: new Map(), seq: 0 });
  const commentsKeyRef = useRef("");
  const commentsReadyRef = useRef(false);
  const commentsRef = useRef(comments);
  commentsRef.current = comments;
  const dirty = ops.length > 0;

  const notify = useCallback((message, tone = "info") => {
    try { api.showNotice?.({ message, tone, key: `design-${tone}` }); }
    catch { console.warn(`[design-mode] ${message}`); }
  }, [api]);

  // ---- bridge plumbing -----------------------------------------------------
  const command = useCallback((type, extra = {}) => {
    const frame = frameRef.current;
    const bridge = bridgeRef.current;
    if (!frame?.contentWindow || !bridge.nonce) return false;
    let origin = "*";
    try { origin = new URL(frame.src).origin; } catch { /* keep the wildcard */ }
    frame.contentWindow.postMessage({ source: "wuu-design", nonce: bridge.nonce, type, ...extra }, origin);
    return true;
  }, []);

  const request = useCallback((type, extra = {}) => new Promise((resolve, reject) => {
    const bridge = bridgeRef.current;
    const id = `r${++bridge.seq}`;
    bridge.pending.set(id, { resolve, reject });
    setTimeout(() => {
      if (bridge.pending.delete(id)) reject(new Error("design bridge timed out"));
    }, 8000);
    if (!command(type, { id, ...extra })) {
      bridge.pending.delete(id);
      reject(new Error("design bridge is not connected"));
    }
  }), [command]);

  const initializeBridge = useCallback(() => {
    const bridge = bridgeRef.current;
    for (const pending of bridge.pending.values()) pending.reject(new Error("design bridge reloaded"));
    bridge.pending.clear();
    bridge.nonce = (crypto.randomUUID?.() ?? `${Date.now()}-${Math.random()}`).toString();
    bridge.ready = false;
    setBridgeReady(false);
    setBridgeLost(false);
    // Let the in-page chrome share the host accent so selection and inspector agree.
    let accent = "";
    try { accent = getComputedStyle(document.documentElement).getPropertyValue("--wuu-color-accent").trim(); } catch { /* keep default */ }
    command("init", { mode, accent });
  }, [command, mode]);

  useEffect(() => {
    const onMessage = (event) => {
      const frame = frameRef.current;
      if (!frame?.contentWindow || event.source !== frame.contentWindow) return;
      const message = event.data;
      if (!message || message.source !== "wuu-design-bridge") return;
      const bridge = bridgeRef.current;
      if (message.type === "hello") { initializeBridge(); return; }
      if (!bridge.nonce || message.nonce !== bridge.nonce) return;
      if (message.id && bridge.pending.has(message.id)) {
        bridge.pending.get(message.id).resolve(message);
        bridge.pending.delete(message.id);
      }
      switch (message.type) {
        case "ready":
          bridge.ready = true;
          setBridgeReady(true);
          command("set-mode", { mode });
          command("comments-restore", { comments: commentsRef.current });
          break;
        case "select": setSelected(message.element); break;
        case "metrics": setSelected((current) => current ? { ...current, rect: message.rect } : current); break;
        case "escape": if (mode === "comment") setMode("edit"); break;
        case "deselect": setSelected(null); break;
        case "op": setOps((list) => upsertOp(list, message.op)); break;
        case "comment-draft":
          setComments((list) => [...list, { ...message.comment, text: "" }]);
          break;
      }
    };
    addEventListener("message", onMessage);
    return () => removeEventListener("message", onMessage);
  }, [command, mode, initializeBridge]);

  // If the bridge never arrives the page left the server or failed to inject.
  useEffect(() => {
    if (bridgeReady) return;
    const timer = setTimeout(() => { if (!bridgeRef.current.ready) setBridgeLost(true); }, 5000);
    return () => clearTimeout(timer);
  }, [bridgeReady, frameKey, path]);

  useEffect(() => { if (bridgeRef.current.ready) command("set-mode", { mode }); }, [mode, command]);

  // Esc outside the iframe leaves comment mode or clears the selection.
  useEffect(() => {
    const onKey = (event) => {
      if (event.key !== "Escape") return;
      if (mode === "comment") setMode("edit");
      else if (selected) { command("deselect"); setSelected(null); }
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [mode, selected, command]);

  // ---- data flow ------------------------------------------------------------
  const storageGet = useCallback(async (key) => {
    try { return await host.getStorage(key, "workspace"); } catch { return null; }
  }, [host]);
  const storageSet = useCallback(async (key, value) => {
    try { await host.setStorage(key, value, "workspace"); } catch { /* storage is best-effort */ }
  }, [host]);

  const refreshPages = useCallback(async (root) => {
    const { pages: list } = await backend.pages(root);
    setPages(list);
    return list;
  }, [backend]);

  const openWorkspace = useCallback(async (ws) => {
    if (!ws) return;
    setError("");
    try {
      const serverBase = await backend.open(ws.root);
      setBase(serverBase);
      const list = await backend.pages(ws.root);
      setPages(list);
      const preferred = (await storageGet(`design.page.${hash(ws.id)}`)) || "index.html";
      const entry = list.find((item) => item.path === preferred) ?? list[0];
      setPath(entry?.path ?? "");
      setSha(entry?.sha256 ?? "");
      setOps([]);
      setConflict(null);
      setSelected(null);
    } catch (err) {
      setError(String(err?.message ?? err));
    }
  }, [backend, storageGet]);

  // Boot: workspaces → open.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        let list;
        if (devBase) {
          const name = devWorkspace ?? new URLSearchParams(location.search).get("design-workspace") ?? "Design preview";
          list = [{ id: "dev", name, root: "", available: true }];
        } else {
          const snapshot = await api.listWorkspaces();
          list = (snapshot?.workspaces ?? []).filter((item) => item.available);
        }
        if (cancelled) return;
        setWorkspaces(list);
        const remembered = devBase ? null : await storageGet("design.workspace");
        const ws = list.find((item) => item.id === workspaceId)
          ?? list.find((item) => item.id === remembered)
          ?? list[0];
        if (ws) setWorkspaceId(ws.id);
        if (ws) await openWorkspace(ws);
        if (!cancelled) setPhase("ready");
      } catch (err) {
        if (!cancelled) { setError(String(err?.message ?? err)); setPhase("error"); }
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bootNonce]);

  // Workspace switch reloads the server session and page list.
  const chooseWorkspace = useCallback((id) => {
    if (id === workspace?.id) return;
    setWorkspaceId(id);
    const ws = workspaces.find((item) => item.id === id);
    if (ws) void openWorkspace(ws);
  }, [workspace?.id, workspaces, openWorkspace]);

  // Persist choices; load per-page comments when the page changes.
  useEffect(() => { if (workspaceId) void storageSet("design.workspace", workspaceId); }, [workspaceId, storageSet]);
  useEffect(() => {
    if (!workspace || !path) return;
    void storageSet(`design.page.${hash(workspace.id)}`, path);
    commentsKeyRef.current = `design.comments.${hash(`${workspace.id}:${path}`)}`;
    commentsReadyRef.current = false;
    let cancelled = false;
    void storageGet(commentsKeyRef.current).then((raw) => {
      if (cancelled) return;
      commentsReadyRef.current = true;
      if (!raw) { setComments([]); return; }
      try { setComments(JSON.parse(raw) ?? []); } catch { setComments([]); }
    });
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspace?.id, path]);
  useEffect(() => {
    if (!commentsReadyRef.current || !commentsKeyRef.current) return;
    void storageSet(commentsKeyRef.current, JSON.stringify(comments));
    if (bridgeRef.current.ready) command("comments-restore", { comments });
  }, [comments, storageSet, command]);

  useEffect(() => { setTextDraft(selected?.text ?? ""); }, [selected?.selector]);

  const url = base && path ? `${base}/${path}` : "";

  // ---- actions ---------------------------------------------------------------
  const save = useCallback(async (force = false) => {
    if (!url || busy) return false;
    setBusy("save");
    try {
      const { html } = await request("serialize");
      const result = await backend.save(workspace?.root ?? "", {
        path, html, base_sha256: force ? "" : sha,
      });
      if (result.status === "conflict") { setConflict(result); return false; }
      if (result.status !== "saved") throw new Error(result.status ?? "save failed");
      setSha(result.sha256);
      setOps([]);
      setConflict(null);
      return true;
    } catch (err) {
      notify(`${t("design.save.failed")} ${String(err?.message ?? err)}`, "error");
      return false;
    } finally { setBusy(""); }
  }, [url, busy, backend, workspace, path, sha, request, notify, t]);

  const reload = useCallback(() => {
    setBusy("reload");
    setOps([]);
    setConflict(null);
    setSelected(null);
    setRevertArmed(false);
    const entry = pages.find((item) => item.path === path);
    if (entry) setSha(entry.sha256);
    command("comments-restore", { comments: [] });
    setFrameKey((key) => key + 1); // remounts the iframe and re-handshakes
    void refreshPages(workspace?.root ?? "").finally(() => setBusy(""));
  }, [pages, path, workspace, refreshPages, command]);

  const choosePage = useCallback((next) => {
    if (next === path) return;
    const entry = pages.find((item) => item.path === next);
    setPath(next);
    setSha(entry?.sha256 ?? "");
    setOps([]);
    setConflict(null);
    setSelected(null);
    command("comments-restore", { comments: [] });
  }, [pages, path, command]);

  const sendToAgent = useCallback(async () => {
    if (comments.length === 0 || busy) return;
    setBusy("send");
    try {
      if (dirty && !(await save())) return;
      const lines = comments.map((comment, index) =>
        `${index + 1}. On \`${comment.selector}\`${comment.snippet ? ` (“${truncate(comment.snippet, 60)}”)` : ""}: ${comment.text?.trim() || "See pinned position."}`);
      const brief = [
        `Design feedback for \`${path}\` in workspace “${workspace?.name ?? workspaceId}”:`,
        "",
        ...lines,
        "",
        `The file lives at \`${workspace?.root ? `${workspace.root}/${path}` : path}\`. Apply the requested changes directly to that file.`,
      ].join("\n");
      const threadKey = `design.thread.${hash(workspace?.id ?? "")}`;
      let threadId = (await storageGet(threadKey)) || "";
      let accepted = false;
      if (threadId) {
        const result = await api.application.invoke("conversation.continue", { threadId, text: brief }).catch(() => ({ accepted: false }));
        accepted = result?.accepted === true;
      }
      if (!accepted) {
        const tab = await api.application.invoke("tab.new", { workspaceId: workspace?.id }).catch(() => undefined);
        if (!tab?.tabId) throw new Error(t("design.newTab.failed"));
        await api.application.invoke("composer.set-draft", { text: brief, ownerTabId: tab.tabId });
        const submitted = await api.application.invoke("composer.submit", { ownerTabId: tab.tabId, text: brief });
        if (!submitted?.accepted) throw new Error(t("design.send.failed"));
        threadId = submitted.threadId ?? "";
        if (threadId) await storageSet(threadKey, threadId);
      }
      const count = comments.length;
      setComments([]);
      notify(t("design.send.done", { count }));
    } catch (err) {
      notify(String(err?.message ?? err) || t("design.send.failed"), "error");
    } finally { setBusy(""); }
  }, [comments, busy, dirty, save, path, workspace, workspaceId, api, storageGet, storageSet, notify, t]);

  const recordOpAsync = useCallback((op, after) => {
    void request("apply", { op })
      .then((reply) => {
        if (reply?.result?.ok === false) throw new Error(reply.result.error ?? "apply failed");
        setOps((list) => upsertOp(list, op));
        after?.();
      })
      .catch(() => notify(t("design.apply.failed"), "error"));
  }, [request, notify, t]);

  const applyStyle = useCallback((prop, value) => {
    if (!selected) return;
    recordOpAsync({ kind: "style", selector: selected.selector, prop, value },
      () => setSelected((current) => current ? { ...current, styles: { ...current.styles, [prop]: value } } : current));
  }, [selected, recordOpAsync]);

  const textTimerRef = useRef(0);
  const applyTextNow = useCallback((value) => {
    if (!selected || value === (selected.text ?? "")) return;
    recordOpAsync({ kind: "text", selector: selected.selector, text: value },
      () => setSelected((current) => current ? { ...current, text: value } : current));
  }, [selected, recordOpAsync]);
  const changeText = useCallback((value) => {
    setTextDraft(value);
    clearTimeout(textTimerRef.current);
    textTimerRef.current = setTimeout(() => applyTextNow(value), 450);
  }, [applyTextNow]);

  const frameWidth = VIEWPORTS.find((item) => item.id === viewport)?.width ?? null;
  const hintText = bridgeLost ? t("design.bridge.lost")
    : mode === "comment" && bridgeReady ? t("design.comment.hint")
    : null;

  // ---- render -----------------------------------------------------------------
  if (phase === "boot") {
    return h("div", { className: "plugin-design plugin-design-center" },
      h(LoadingState, { title: t("design.loading") }));
  }
  if (phase === "error") {
    return h("div", { className: "plugin-design plugin-design-center" },
      h(ErrorState, {
        title: t("design.error.title"), description: error || t("design.error.runtime"),
        actions: h(Button, {
          variant: "secondary",
          onClick: () => { setError(""); setPhase("boot"); setBootNonce((n) => n + 1); },
        }, t("design.retry")),
      }));
  }
  if (workspaces.length === 0) {
    return h("div", { className: "plugin-design plugin-design-center" },
      h(EmptyState, { title: t("design.empty.workspaces"), description: t("design.empty.workspacesHelp") }));
  }

  return h("div", { className: "plugin-design" },
    h("div", { className: "plugin-design-topbar" },
      workspaces.length > 1 ? h(Select, {
        className: "plugin-design-picker", label: t("design.workspace"),
        value: workspace?.id ?? "", onChange: (event) => chooseWorkspace(event.currentTarget.value),
      }, workspaces.map((ws) => h("option", { key: ws.id, value: ws.id }, ws.name))) : null,
      h(Select, {
        className: "plugin-design-picker plugin-design-page-picker", label: t("design.page"),
        value: path, disabled: pages.length === 0,
        onChange: (event) => choosePage(event.currentTarget.value),
      }, pages.map((page) => h("option", { key: page.path, value: page.path }, page.path))),
      h("span", { className: "plugin-design-divider" }),
      h(ViewportMenu, { h, React, viewport, onPick: setViewport, t }),
      h("span", { className: "plugin-design-divider" }),
      h("div", { className: "plugin-design-tools", role: "group", "aria-label": "Canvas mode" },
        h("button", {
          type: "button", className: "plugin-design-iconbutton", "data-active": mode === "edit" || undefined,
          onClick: () => setMode("edit"), "aria-pressed": mode === "edit",
          title: t("design.mode.edit"), "aria-label": t("design.mode.edit"),
        }, h(Glyph, { h, d: ICON_PATHS.cursor })),
        h("button", {
          type: "button", className: "plugin-design-iconbutton", "data-active": mode === "comment" || undefined,
          onClick: () => setMode("comment"), "aria-pressed": mode === "comment",
          title: t("design.mode.comment"), "aria-label": t("design.mode.comment"),
        }, h(Glyph, { h, d: ICON_PATHS.comment }), comments.length > 0 ? h("span", { className: "plugin-design-badge" }, comments.length) : null)),
      h("div", { className: "plugin-design-topbar-spacer" }),
      h("button", {
        type: "button", className: "plugin-design-iconbutton", title: t("design.reload"),
        "aria-label": t("design.reload"), disabled: !url || busy === "reload",
        onClick: reload,
      }, h(Glyph, { h, d: ICON_PATHS.refresh })),
      h("button", {
        type: "button", className: "plugin-design-iconbutton", title: t("design.external"),
        "aria-label": t("design.external"), disabled: !url,
        onClick: () => {
          void Promise.resolve(api.application?.invoke?.("preview.open-url", { url }))
            .catch(() => window.open(url, "_blank", "noopener"));
        },
      }, h(Glyph, { h, d: ICON_PATHS.external })),
      h(Button, {
        variant: "ghost", disabled: (!dirty && !conflict) || Boolean(busy),
        "data-armed": revertArmed || undefined,
        onClick: () => {
          if (!revertArmed) { setRevertArmed(true); setTimeout(() => setRevertArmed(false), 2600); return; }
          reload();
        },
      }, revertArmed ? t("design.revert.confirm") : t("design.revert")),
      h(Button, {
        variant: "secondary", disabled: comments.length === 0 || Boolean(busy),
        onClick: () => void sendToAgent(),
      }, h(Glyph, { h, d: ICON_PATHS.send }), " ", t("design.send")),
      h(Button, {
        variant: "primary", disabled: !dirty || Boolean(busy) || !bridgeReady,
        onClick: () => void save(),
      }, busy === "save" ? t("design.saving") : dirty ? `${t("design.save")} · ${ops.length}` : t("design.save"))),
    h("div", { className: "plugin-design-body" },
      h("div", {
        className: "plugin-design-stage", "data-mode": mode, "data-flush": frameWidth == null || undefined,
        onClick: (event) => { if (event.target === event.currentTarget && mode === "edit") command("deselect"); },
      },
        url
          ? h("div", {
              className: "plugin-design-frame",
              "data-flush": frameWidth == null || undefined,
              style: frameWidth ? { width: `${frameWidth}px`, maxWidth: "100%" } : { width: "100%" },
            },
              h("iframe", {
                key: `${frameKey}:${url}`,
                ref: frameRef,
                className: "plugin-design-iframe",
                src: url,
                sandbox: "allow-scripts allow-same-origin allow-forms allow-popups",
                referrerPolicy: "no-referrer",
                title: path,
                onLoad: () => initializeBridge(),
              }))
          : h("div", { className: "plugin-design-center" },
              h(EmptyState, {
                title: t("design.empty.pages"),
                description: t("design.empty.pagesHelp"),
                actions: h(Button, { variant: "secondary", onClick: () => void openWorkspace(workspace) }, t("design.empty.refresh")),
              })),
        url && hintText ? h("div", { className: "plugin-design-hint" }, hintText) : null,
        conflict ? h("div", { className: "plugin-design-conflict" },
          h("div", { className: "plugin-design-conflict-card" },
            h("strong", null, t("design.conflict.title")),
            h("p", null, t("design.conflict.help")),
            h("div", { className: "plugin-design-conflict-actions" },
              h(Button, { variant: "secondary", onClick: reload }, t("design.conflict.reload")),
              h(Button, { variant: "danger", onClick: () => void save(true) }, t("design.conflict.force")))),
        ) : null),
      (mode === "comment" || selected) ? h("aside", { className: "plugin-design-inspector", "data-mode": mode },
        mode === "comment" ? commentsSection() : null,
        selected ? h(React.Fragment, null,
          h("section", { className: "plugin-design-section" },
            h("div", { className: "plugin-design-elemhead" },
              h("span", { className: "plugin-design-tag" }, selected.tag),
              h("span", { className: "plugin-design-rect" }, `${selected.rect.w} × ${selected.rect.h} px`)),
            h("code", { className: "plugin-design-selector", title: selected.selector }, selected.selector),
            (TEXTUAL.test((selected.tag ?? "").toUpperCase().split(/[.#]/)[0]) || selected.text)
              ? h("div", { className: "plugin-design-field" },
                  h(TextArea, {
                    label: t("design.inspector.text"), value: textDraft, rows: 3,
                    onChange: (event) => changeText(event.currentTarget.value),
                    onBlur: () => { clearTimeout(textTimerRef.current); applyTextNow(textDraft); },
                  }))
              : null,
          ),
          h("section", { className: "plugin-design-section" },
            h("h2", { className: "plugin-design-section-title" }, t("design.inspector.styles")),
            h(StyleRows, { h, React, t, selected, onApply: applyStyle })),
        ) : null,
        mode === "comment" ? null : commentsSection()) : null));

  function commentsSection() {
    return h("section", { className: "plugin-design-section plugin-design-comments" },
      h("h2", { className: "plugin-design-section-title" }, t("design.comment.title"), comments.length ? h("span", { className: "plugin-design-count" }, comments.length) : null),
      comments.length === 0
        ? h("p", { className: "plugin-design-muted" }, t("design.comment.empty"))
        : h("ol", { className: "plugin-design-comment-list" },
            comments.map((comment, index) => h("li", {
              key: comment.id, className: "plugin-design-comment",
              onMouseEnter: () => command("comment-focus", { id: comment.id }),
              onMouseLeave: () => command("comment-focus", { id: null }),
              onClick: (event) => { if (event.target === event.currentTarget || event.target.closest(".plugin-design-selector")) command("scroll-to", { selector: comment.selector }); },
            },
              h("span", { className: "plugin-design-pin", "aria-hidden": true }, index + 1),
              h("div", { className: "plugin-design-comment-body" },
                h("code", { className: "plugin-design-selector", title: comment.selector }, comment.tag ?? comment.selector),
                h(TextArea, {
                  label: comment.tag ?? comment.selector, value: comment.text, rows: 2, placeholder: t("design.comment.placeholder"),
                  onChange: (event) => {
                    const text = event.currentTarget.value;
                    setComments((list) => list.map((item) => item.id === comment.id ? { ...item, text } : item));
                  },
                })),
              h("button", {
                type: "button", className: "plugin-design-iconbutton", "aria-label": t("design.removeComment"),
                onClick: () => { setComments((list) => list.filter((item) => item.id !== comment.id)); command("comment-remove", { id: comment.id }); },
              }, h(Glyph, { h, d: ICON_PATHS.trash }))))));
  }
}

// ---------------------------------------------------------------------------
// Inspector style controls
// ---------------------------------------------------------------------------

const FONT_STACKS = [
  { label: "Inter", value: "Inter, system-ui, sans-serif" },
  { label: "Space Grotesk", value: "\"Space Grotesk\", Inter, system-ui, sans-serif" },
  { label: "Serif", value: "Georgia, \"Times New Roman\", serif" },
  { label: "Mono", value: "ui-monospace, SFMono-Regular, Consolas, monospace" },
];
const WEIGHT_OPTIONS = ["", "300", "400", "500", "600", "700", "800"];
const BORDER_STYLES = ["", "solid", "dashed", "dotted", "none"];
const ALIGN_OPTIONS = [
  { value: "left", icon: "alignLeft" },
  { value: "center", icon: "alignCenter" },
  { value: "right", icon: "alignRight" },
  { value: "justify", icon: "alignJustify" },
];
const QUAD_AXES = ["Top", "Right", "Bottom", "Left"];

function StyleRows({ h, React, t, selected, onApply }) {
  const styles = selected.styles ?? {};
  const numeric = (value) => String(value ?? "").replace(/px$/, "");
  // Uncontrolled fields remount per element so stale drafts never leak across selections.
  const field = { reset: selected.selector, h };
  const key = (prop) => `${selected.selector}:${prop}`;
  const pxCommit = (prop) => (value) => onApply(prop, value ? `${value}px` : "");
  const fontOptions = [...FONT_STACKS];
  if (styles.fontFamily && !fontOptions.some((item) => item.value === styles.fontFamily)) {
    fontOptions.unshift({ label: String(styles.fontFamily).split(",")[0].replace(/["']/g, "").trim() || t("design.style.font"), value: styles.fontFamily });
  }
  return h("div", { className: "plugin-design-stylegrid" },
    h(ColorField, { ...field, key: key("color"), label: t("design.style.color"), value: styles.color, onCommit: (value) => onApply("color", value) }),
    h(ColorField, { ...field, key: key("background"), label: t("design.style.background"), value: styles.backgroundColor, onCommit: (value) => onApply("background-color", value) }),
    h(SelectField, {
      ...field, key: key("font"), label: t("design.style.font"), full: true,
      value: styles.fontFamily ?? "", options: fontOptions, onCommit: (value) => onApply("font-family", value),
    }),
    h(Field, { ...field, key: key("fontSize"), label: t("design.style.fontSize"), value: numeric(styles.fontSize), suffix: "px", onCommit: pxCommit("font-size") }),
    h(SelectField, {
      ...field, key: key("weight"), label: t("design.style.weight"),
      value: styles.fontWeight ?? "", options: WEIGHT_OPTIONS.map((w) => ({ label: w || "—", value: w })),
      onCommit: (value) => onApply("font-weight", value),
    }),
    h(Field, { ...field, key: key("lineHeight"), label: t("design.style.leading"), value: styles.lineHeight, placeholder: "1.5", onCommit: (value) => onApply("line-height", value) }),
    h(Field, { ...field, key: key("letterSpacing"), label: t("design.style.tracking"), value: numeric(styles.letterSpacing), suffix: "px", onCommit: pxCommit("letter-spacing") }),
    h(Field, { ...field, key: key("radius"), label: t("design.style.radius"), value: numeric(styles.borderRadius), suffix: "px", onCommit: pxCommit("border-radius") }),
    h(Field, { ...field, key: key("borderWidth"), label: t("design.style.border"), value: numeric(styles.borderWidth), suffix: "px", onCommit: pxCommit("border-width") }),
    h(ColorField, { ...field, key: key("stroke"), label: t("design.style.stroke"), value: styles.borderColor, onCommit: (value) => onApply("border-color", value) }),
    h(SelectField, {
      ...field, key: key("borderStyle"), label: t("design.style.borderStyle"),
      value: styles.borderStyle ?? "", options: BORDER_STYLES.map((s) => ({ label: s || "—", value: s })),
      onCommit: (value) => onApply("border-style", value),
    }),
    h(Field, { ...field, key: key("width"), label: t("design.style.width"), value: styles.width, placeholder: `${Math.round(selected.rect?.w ?? 0)}px`, onCommit: (value) => onApply("width", value) }),
    h(Field, { ...field, key: key("height"), label: t("design.style.height"), value: styles.height, placeholder: `${Math.round(selected.rect?.h ?? 0)}px`, onCommit: (value) => onApply("height", value) }),
    h(QuadField, {
      ...field, key: key("padding"), label: t("design.style.padding"), full: true,
      values: { Top: styles.paddingTop, Right: styles.paddingRight, Bottom: styles.paddingBottom, Left: styles.paddingLeft },
      onCommit: (axis, value) => onApply(`padding-${axis.toLowerCase()}`, value ? `${value}px` : ""),
    }),
    h(QuadField, {
      ...field, key: key("margin"), label: t("design.style.margin"), full: true,
      values: { Top: styles.marginTop, Right: styles.marginRight, Bottom: styles.marginBottom, Left: styles.marginLeft },
      onCommit: (axis, value) => onApply(`margin-${axis.toLowerCase()}`, value ? `${value}px` : ""),
    }),
    h("div", { className: "plugin-design-row plugin-design-row-full" },
      h("span", { className: "plugin-design-row-label" }, t("design.style.align")),
      h("span", { className: "plugin-design-row-value plugin-design-align" },
        ALIGN_OPTIONS.map((item) => h("button", {
          key: item.value, type: "button", className: "plugin-design-align-button",
          "data-active": (styles.textAlign ?? "").startsWith(item.value) || undefined,
          title: item.value, "aria-label": `Align ${item.value}`, "aria-pressed": (styles.textAlign ?? "").startsWith(item.value),
          onClick: () => onApply("text-align", (styles.textAlign ?? "").startsWith(item.value) ? "" : item.value),
        }, h(Glyph, { h, d: ICON_PATHS[item.icon] }))))),
    h("div", { className: "plugin-design-row plugin-design-row-full plugin-design-opacity" },
      h("span", { className: "plugin-design-row-label" }, t("design.style.opacity")),
      h("span", { className: "plugin-design-row-value" },
        h("input", {
          type: "range", min: "0", max: "1", step: "0.05",
          value: String(parseFloat(styles.opacity ?? "1") || 0), "aria-label": t("design.style.opacity"),
          onChange: (event) => onApply("opacity", event.currentTarget.value),
        }),
        h("span", { className: "plugin-design-opacity-value" }, `${Math.round((parseFloat(styles.opacity ?? "1") || 0) * 100)}%`))));
}

/** Single 34px field row: muted label left, right-aligned value — the cc-row idiom. */
function Row({ h, label, full, reset, children }) {
  return h("div", { className: `plugin-design-row${full ? " plugin-design-row-full" : ""}`, "data-reset": reset },
    h("span", { className: "plugin-design-row-label" }, label),
    h("span", { className: "plugin-design-row-value" }, children));
}

/** Uncontrolled text field: commits on blur or Enter; remounts when `reset` changes. */
function Field({ h, label, value, placeholder, suffix, reset, full, onCommit }) {
  return h(Row, { h, label, full, reset },
    h("input", {
      defaultValue: value ?? "", placeholder,
      onBlur: (event) => { if (event.currentTarget.value.trim() !== String(value ?? "")) onCommit(event.currentTarget.value.trim()); },
      onKeyDown: (event) => { if (event.key === "Enter") event.currentTarget.blur(); },
    }),
    suffix ? h("span", { className: "plugin-design-suffix" }, suffix) : null);
}

function SelectField({ h, label, value, options, reset, full, onCommit }) {
  return h(Row, { h, label, full, reset },
    h("select", {
      value: value ?? "", onChange: (event) => onCommit(event.currentTarget.value),
    }, options.map((option) => h("option", { key: option.value, value: option.value }, option.label))));
}

function ColorField({ h, label, value, reset, full, onCommit }) {
  const empty = !value || value === "transparent" || /^rgba\(0,\s*0,\s*0,\s*0\)$/.test(value);
  const hex = empty ? null : toHex(value);
  return h(Row, { h, label, full, reset },
    h("span", {
      className: "plugin-design-swatch", "data-empty": empty || undefined,
      style: hex ? { background: hex } : undefined,
    }, h("input", {
      type: "color", value: hex ?? "#000000", "aria-label": label,
      onChange: (event) => onCommit(event.currentTarget.value),
    })),
    h("input", {
      defaultValue: empty ? "" : String(value), placeholder: "—",
      onBlur: (event) => { if (event.currentTarget.value.trim() !== String(empty ? "" : value ?? "")) onCommit(event.currentTarget.value.trim()); },
      onKeyDown: (event) => { if (event.key === "Enter") event.currentTarget.blur(); },
    }));
}

/** Four numeric cells (T/R/B/L) sharing one row; each commits its own axis. */
function QuadField({ h, label, values, reset, full, onCommit }) {
  return h("div", { className: `plugin-design-row${full ? " plugin-design-row-full" : ""} plugin-design-quad`, "data-reset": reset },
    h("span", { className: "plugin-design-row-label" }, label),
    h("span", { className: "plugin-design-quad-cells" },
      QUAD_AXES.map((axis) => h("label", { key: axis, className: "plugin-design-quad-cell", title: axis },
        h("i", { "aria-hidden": true }, axis[0].toLowerCase()),
        h("input", {
          defaultValue: values?.[axis] ?? "", "aria-label": `${label} ${axis}`,
          onBlur: (event) => { const next = event.currentTarget.value.trim(); if (next !== String(values?.[axis] ?? "")) onCommit(axis, next); },
          onKeyDown: (event) => { if (event.key === "Enter") event.currentTarget.blur(); },
        })))));
}

/** Viewport picker: one trigger showing the active device, menu for the rest. */
function ViewportMenu({ h, React, viewport, onPick, t }) {
  const { useState, useRef, useEffect } = React;
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState(null);
  const rootRef = useRef(null);
  const triggerRef = useRef(null);
  useEffect(() => {
    if (!open) return;
    const down = (event) => { if (!rootRef.current?.contains(event.target)) setOpen(false); };
    const key = (event) => { if (event.key === "Escape") setOpen(false); };
    document.addEventListener("pointerdown", down);
    document.addEventListener("keydown", key);
    return () => { document.removeEventListener("pointerdown", down); document.removeEventListener("keydown", key); };
  }, [open]);
  const active = VIEWPORTS.find((item) => item.id === viewport) ?? VIEWPORTS[VIEWPORTS.length - 1];
  const toggle = () => {
    if (!open && triggerRef.current) {
      const rect = triggerRef.current.getBoundingClientRect();
      setPos({ x: rect.left, y: rect.bottom + 4 });
    }
    setOpen((value) => !value);
  };
  return h("div", { className: "plugin-design-viewport", ref: rootRef },
    h("button", {
      type: "button", className: "plugin-design-iconbutton plugin-design-viewport-trigger",
      "aria-haspopup": "listbox", "aria-expanded": open, "data-open": open || undefined,
      title: t(active ? `design.viewport.${active.id}` : "design.viewport.aria"),
      "aria-label": `${t("design.viewport.aria")}: ${active ? t(`design.viewport.${active.id}`) : ""}`,
      ref: triggerRef, onClick: toggle,
    },
      h(ViewportIcon, { h, id: active.id }),
      h("span", { className: "plugin-design-viewport-name" }, t(`design.viewport.${active.id}`)),
      h(Glyph, { h, d: ICON_PATHS.chevron })),
    open ? h("div", {
      className: "plugin-design-menu", role: "listbox", "aria-label": t("design.viewport.aria"),
      style: pos ? { position: "fixed", top: `${pos.y}px`, left: `${pos.x}px`, insetInlineStart: "auto" } : undefined,
    },
      VIEWPORTS.map((item) => h("button", {
        key: item.id, type: "button", role: "option", "aria-selected": viewport === item.id,
        className: "plugin-design-menu-item", "data-active": viewport === item.id || undefined,
        onClick: () => { onPick(item.id); setOpen(false); },
      },
        h(ViewportIcon, { h, id: item.id }),
        h("span", null, t(`design.viewport.${item.id}`)),
        viewport === item.id ? h(Glyph, { h, d: ICON_PATHS.check }) : null))) : null);
}

// ---------------------------------------------------------------------------
// Small components + helpers
// ---------------------------------------------------------------------------

function upsertOp(list, op) {
  const key = `${op.kind}:${op.selector}:${op.prop ?? ""}`;
  const next = list.filter((item) => `${item.kind}:${item.selector}:${item.prop ?? ""}` !== key);
  next.push(op);
  return next;
}

function hash(text) {
  let value = 5381;
  for (let i = 0; i < text.length; i++) value = ((value << 5) + value + text.charCodeAt(i)) | 0;
  return (value >>> 0).toString(36);
}

function truncate(text, limit) {
  return text.length > limit ? `${text.slice(0, limit - 1)}…` : text;
}

function toHex(color) {
  const text = String(color ?? "").trim();
  const short = /^#([0-9a-f]{6})$/i.exec(text);
  if (short) return short[0].toLowerCase();
  const rgb = /rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/.exec(text);
  if (rgb) return `#${[rgb[1], rgb[2], rgb[3]].map((v) => Number(v).toString(16).padStart(2, "0")).join("")}`;
  return null;
}

function ViewportIcon({ h, id }) {
  const shapes = {
    mobile: [h("rect", { key: "a", x: 9, y: 3.5, width: 6, height: 17, rx: 1.8 })],
    tablet: [h("rect", { key: "a", x: 6.5, y: 4, width: 11, height: 16, rx: 1.8 })],
    desktop: [
      h("rect", { key: "a", x: 3.5, y: 4.5, width: 17, height: 11.5, rx: 1.6 }),
      h("path", { key: "b", d: "M9.5 19.5h5M12 16v3.5" }),
    ],
    fill: [
      h("rect", { key: "a", x: 3.5, y: 4.5, width: 17, height: 15, rx: 1.8 }),
      h("path", { key: "b", d: "M8 12h8M12 8v8", strokeDasharray: "2.5 2.5" }),
    ],
  };
  return h("svg", { viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.7, strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": true },
    shapes[id]);
}

const ICON_PATHS = {
  cursor: "M6.5 3.8l12.2 9.4-7 1.3-3.6 6.2L6.5 3.8z",
  comment: "M12 4c-4.9 0-8.5 3.2-8.5 7.2 0 2.3 1.3 4.3 3.2 5.5L6 20l3.9-1.9c.7.1 1.4.2 2.1.2 4.9 0 8.5-3.2 8.5-7.1S16.9 4 12 4z",
  refresh: "M19.5 12a7.5 7.5 0 1 1-2.2-5.3M19.5 4v4h-4",
  external: "M14 4.5h5.5V10M19.5 4.5L11 13M9 6H6.5A2.5 2.5 0 0 0 4 8.5v9A2.5 2.5 0 0 0 6.5 20h9a2.5 2.5 0 0 0 2.5-2.5V15",
  send: "M4.5 11.5L20 4l-4.5 16-3.4-6.6L4.5 11.5zM12.1 13.4L20 4",
  trash: "M4.5 6.5h15M9.5 6V4.5A1.5 1.5 0 0 1 11 3h2a1.5 1.5 0 0 1 1.5 1.5V6m3 0l-.8 12.2a2 2 0 0 1-2 1.8H9.3a2 2 0 0 1-2-1.8L6.5 6.5M10 10.5v6M14 10.5v6",
  chevron: "M7 10l5 5 5-5",
  check: "M5 12.5l4.5 4.5L19 7.5",
  alignLeft: "M4.5 6h15M4.5 10.5h9M4.5 15h15M4.5 19.5h9",
  alignCenter: "M4.5 6h15M7.5 10.5h9M4.5 15h15M7.5 19.5h9",
  alignRight: "M4.5 6h15M10.5 10.5h9M4.5 15h15M10.5 19.5h9",
  alignJustify: "M4.5 6h15M4.5 10.5h15M4.5 15h15M4.5 19.5h15",
};

function Glyph({ h, d }) {
  return h("svg", { viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.7, strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": true },
    h("path", { d }));
}

// ---------------------------------------------------------------------------
// Styles
// ---------------------------------------------------------------------------

const DESIGN_CSS = `
.plugin-design {
  height: 100%; min-height: 0; display: flex; flex-direction: column;
  color: var(--wuu-color-text, var(--ink));
  font-size: var(--wuu-font-size-ui, 15px);
  background: var(--wuu-color-canvas, var(--paper));
  container-type: inline-size;
}
.plugin-design *, .plugin-design *::before { box-sizing: border-box; }
.plugin-design-center { display: grid; place-items: center; height: 100%; min-height: 0; padding: var(--space-8, 32px); }
.plugin-design-muted { color: var(--ink-tertiary); font-size: .84em; margin: 0; line-height: 1.55; opacity: .9; }

/* -- top bar -- */
.plugin-design-topbar {
  flex: none; display: flex; align-items: center; gap: var(--space-2, 8px);
  min-height: 44px; padding-block: 4px; padding-inline: var(--pane-inset, var(--space-3, 12px));
  background: var(--wuu-color-surface, transparent);
  overflow-x: auto; scrollbar-width: none;
  container-type: inline-size;
}
.plugin-design-topbar::-webkit-scrollbar { display: none; }
/* The class lands on the <select> itself; hide its sibling field label so the
 * pickers stay topbar-compact in both the host and preview kits. */
.plugin-design-topbar label:has(> .plugin-design-picker) { display: inline-flex; align-items: center; margin: 0; font-size: 0; }
.plugin-design-picker { font-size: var(--wuu-font-size-ui, 15px); max-width: 190px; min-width: 0; }
.plugin-design-page-picker { max-width: 230px; }
.plugin-design-divider { flex: none; width: 1px; height: 18px; background: var(--wuu-border-subtle, var(--wuu-hairline, rgba(127,127,127,.25))); margin-inline: var(--space-1, 4px); }
.plugin-design-topbar-spacer { flex: 1; min-width: 8px; }
.plugin-design-tools { flex: none; display: inline-flex; align-items: center; gap: 2px; }

.plugin-design-iconbutton {
  position: relative; flex: none; display: inline-grid; place-items: center; width: 28px; height: 28px;
  border: 0; border-radius: var(--radius-sm, 8px); background: transparent;
  color: var(--ink-tertiary); cursor: pointer;
}
.plugin-design-iconbutton:hover:not([disabled]) { background: var(--wuu-nav-item-hover-background, rgba(127,127,127,.14)); color: var(--ink); }
.plugin-design-iconbutton[data-active] { background: var(--wuu-color-surface-muted, rgba(127,127,127,.16)); color: var(--ink); }
.plugin-design-iconbutton[disabled] { opacity: .45; cursor: default; }
.plugin-design-iconbutton svg { width: 15px; height: 15px; }
.plugin-design-count {
  display: inline-grid; place-items: center; min-width: 18px; height: 18px;
  padding-inline: 5px; margin-inline-start: 4px; border-radius: 9px;
  background: var(--wuu-color-surface-muted, rgba(127,127,127,.2)); color: var(--ink-soft);
  font-size: .78em; font-weight: 650;
}
.plugin-design-badge {
  position: absolute; top: -2px; right: -4px;
  display: inline-grid; place-items: center; min-width: 15px; height: 15px;
  padding-inline: 4px; border-radius: 8px;
  background: var(--ink, #26272b); color: var(--wuu-color-canvas, var(--paper, #fff));
  font-size: 10px; font-weight: 650; line-height: 1;
}
.plugin-design-viewport { position: relative; flex: none; }
.plugin-design-viewport-trigger { display: inline-flex; align-items: center; width: auto; padding-inline: 7px; gap: 5px; font: inherit; font-size: .85em; }
.plugin-design-viewport-trigger .plugin-design-viewport-name { color: var(--ink-soft); }
.plugin-design-viewport-trigger svg:last-child { width: 12px; height: 12px; opacity: .7; }
.plugin-design-menu {
  position: absolute; z-index: 40; top: calc(100% + 4px); inset-inline-start: 0; min-width: 148px;
  padding: 4px; border-radius: var(--wuu-radius-menu, 10px);
  background: var(--wuu-color-surface-elevated, var(--paper, #fff));
  border: 1px solid var(--wuu-border-subtle, var(--wuu-hairline, rgba(127,127,127,.2)));
  box-shadow: 0 12px 32px rgba(15,23,42,.16), 0 2px 6px rgba(15,23,42,.08);
}
.plugin-design-menu-item {
  display: flex; align-items: center; gap: 8px; width: 100%; height: 30px;
  padding-inline: 8px; border: 0; border-radius: 6px; background: transparent;
  color: var(--ink-soft); font: inherit; font-size: .85em; cursor: pointer; text-align: start;
}
.plugin-design-menu-item svg { width: 15px; height: 15px; flex: none; }
.plugin-design-menu-item:hover { background: var(--wuu-nav-item-hover-background, rgba(127,127,127,.12)); color: var(--ink); }
.plugin-design-menu-item[data-active] { color: var(--ink); font-weight: 550; }
.plugin-design-menu-item svg:last-child { margin-inline-start: auto; width: 13px; height: 13px; }
.plugin-design-menu-item span { flex: 1; }
.plugin-design-topbar .plugin-ui-button svg,
.plugin-design-topbar button[data-variant] svg { width: 15px; height: 15px; flex: none; }
.plugin-design-topbar button[data-variant] { display: inline-flex; align-items: center; gap: 6px; }
.plugin-design-topbar [data-armed] { color: var(--wuu-color-danger, #c33); }
.plugin-design button[disabled] { opacity: .4; filter: saturate(.2); }

/* -- body -- */
.plugin-design-body { position: relative; flex: 1; min-height: 0; display: flex; }

/* -- stage -- */
.plugin-design-stage {
  position: relative; flex: 1; min-width: 0; min-height: 0;
  display: grid; place-items: center; overflow: auto;
  padding: var(--space-6, 24px);
  background-image: radial-gradient(circle, var(--wuu-border-subtle, rgba(127,127,127,.3)) 1px, transparent 1.15px);
  background-size: 22px 22px;
}
.plugin-design-stage[data-mode="comment"] { cursor: crosshair; }
.plugin-design-stage[data-flush] { padding: 0; }
.plugin-design-frame {
  display: flex; flex-direction: column; height: 100%; max-height: 100%;
  border-radius: var(--wuu-radius-panel, var(--radius-md, 12px));
  border: 1px solid var(--wuu-border-subtle, var(--wuu-hairline, rgba(127,127,127,.2)));
  background: var(--wuu-color-surface-elevated, #fff);
  box-shadow: 0 24px 60px -18px rgba(15,23,42,.32), 0 3px 10px rgba(15,23,42,.08);
  overflow: hidden;
  transition: width .28s var(--environment-panel-motion-easing, ease);
}
.plugin-design-frame[data-flush] { border: 0; border-radius: 0; box-shadow: none; }
.plugin-design-iframe { flex: 1; width: 100%; border: 0; background: #fff; }
.plugin-design-hint {
  position: absolute; z-index: 3; bottom: calc(var(--space-6, 24px) + 10px); left: 50%; transform: translateX(-50%);
  padding: 7px 14px; border-radius: var(--radius-pill, 999px);
  background: var(--wuu-color-surface-elevated, var(--paper, #fff));
  border: 1px solid var(--wuu-border-subtle, var(--wuu-hairline, rgba(127,127,127,.2)));
  box-shadow: var(--shadow-soft, 0 4px 16px rgba(0,0,0,.14));
  color: var(--ink-soft); font-size: .84em; pointer-events: none; white-space: nowrap;
}
.plugin-design-conflict {
  position: absolute; z-index: 4; inset: 0; display: grid; place-items: center;
  background: var(--wuu-color-overlay-backdrop, rgba(15,23,42,.32));
}
.plugin-design-conflict-card {
  width: min(400px, 88%); padding: var(--space-5, 20px);
  border-radius: var(--wuu-radius-panel, 12px);
  background: var(--wuu-color-surface-elevated, var(--paper, #fff));
  border: 1px solid var(--wuu-border-subtle, var(--wuu-hairline, rgba(127,127,127,.2)));
  box-shadow: 0 24px 60px -12px rgba(0,0,0,.4);
}
.plugin-design-conflict-card p { color: var(--ink-soft); font-size: .92em; line-height: 1.55; margin: var(--space-2, 8px) 0 var(--space-4, 16px); }
.plugin-design-conflict-actions { display: flex; gap: var(--space-2, 8px); justify-content: flex-end; }

/* -- inspector -- */
.plugin-design-inspector {
  flex: none; width: 288px; min-height: 0; overflow-y: auto;
  border-left: 1px solid var(--wuu-border-subtle, var(--wuu-hairline, rgba(127,127,127,.2)));
  background: var(--wuu-color-surface, transparent);
}
.plugin-design-section { padding: var(--space-4, 16px) var(--space-3, 12px); }
.plugin-design-section + .plugin-design-section { padding-top: var(--space-2, 8px); }
.plugin-design-section-title {
  display: flex; align-items: center; gap: var(--space-2, 8px);
  margin: 0 0 var(--space-2, 8px); font-size: .74em; font-weight: 650;
  letter-spacing: .07em; text-transform: uppercase; color: var(--ink-tertiary);
}
.plugin-design-elemhead { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2, 8px); margin-block-end: 4px; }
.plugin-design-tag {
  display: inline-block; padding: 3px 8px; border-radius: var(--radius-xs, 6px);
  background: var(--wuu-color-surface-muted, rgba(127,127,127,.14));
  color: var(--ink-soft);
  font-family: var(--wuu-font-family-mono, ui-monospace, monospace); font-size: .8em; font-weight: 600;
  width: fit-content; max-width: 100%; overflow: hidden; text-overflow: ellipsis;
}
.plugin-design-selector {
  display: block; font-family: var(--wuu-font-family-mono, ui-monospace, monospace); font-size: .74em;
  color: var(--ink-tertiary); opacity: .78; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.plugin-design-rect { font-size: .78em; color: var(--ink-faint, var(--ink-tertiary)); font-variant-numeric: tabular-nums; }
.plugin-design-field { display: grid; gap: 4px; margin-block-start: var(--space-2, 8px); }
.plugin-design-field textarea { font-size: .9em; }

/* Inspector fields are single rows — muted label inside-left, value right. */
.plugin-design-stylegrid { display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-2, 8px); }
.plugin-design-row {
  display: flex; align-items: center; gap: 6px; height: 34px; min-width: 0;
  padding: 0 8px 0 10px; border-radius: var(--wuu-radius-control, var(--radius-sm, 8px));
  border: 1px solid var(--wuu-border-subtle, var(--control-boundary, rgba(127,127,127,.28)));
  background: var(--wuu-control-field-background, var(--wuu-color-surface, transparent));
  transition: border-color .12s ease;
}
.plugin-design-row:hover { border-color: var(--wuu-border-strong, rgba(127,127,127,.45)); }
.plugin-design-row:focus-within { border-color: var(--wuu-color-accent, #4a63d8); }
.plugin-design-row-full { grid-column: 1 / -1; }
.plugin-design-row-label {
  flex: none; max-width: 42%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  color: var(--ink-tertiary); font-size: .78em; font-weight: 600;
}
.plugin-design-row-value {
  flex: 1; display: flex; align-items: center; justify-content: flex-end; gap: 4px; min-width: 0; height: 100%;
}
.plugin-design-row-value input,
.plugin-design-row-value select {
  flex: 1; min-width: 0; height: 100%; padding: 0; border: 0; background: transparent;
  color: var(--ink); font: inherit; font-size: .84em; text-align: end; outline: none;
}
.plugin-design-row-value select { cursor: pointer; }
.plugin-design-row-value input[type="range"] { cursor: default; }
.plugin-design-suffix { flex: none; font-size: .75em; color: var(--ink-faint, var(--ink-tertiary)); }
.plugin-design-swatch {
  position: relative; flex: none; width: 18px; height: 18px;
  border-radius: 50%; overflow: hidden;
  border: 1px solid var(--wuu-border-subtle, var(--control-boundary, rgba(127,127,127,.3)));
}
.plugin-design-swatch[data-empty] {
  background: conic-gradient(#bbb 0 25%, #fff 0 50%, #bbb 0 75%, #fff 0) 0 0/8px 8px;
}
.plugin-design-swatch input[type="color"] {
  position: absolute; inset: -10px; width: 40px; height: 40px; padding: 0;
  opacity: 0; cursor: pointer;
}
.plugin-design-quad-cells { flex: 1; display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 2px; min-width: 0; }
.plugin-design-quad-cell { display: flex; align-items: center; gap: 2px; min-width: 0; }
.plugin-design-quad-cell i { flex: none; font-style: normal; font-size: .66em; color: var(--ink-faint, var(--ink-tertiary)); }
.plugin-design-quad-cell input {
  width: 100%; min-width: 0; height: 100%; padding: 0; border: 0; background: transparent;
  color: var(--ink); font: inherit; font-size: .82em; text-align: end; outline: none; font-variant-numeric: tabular-nums;
}
.plugin-design-align { gap: 2px; }
.plugin-design-align-button {
  display: inline-grid; place-items: center; width: 24px; height: 24px;
  border: 0; border-radius: 5px; background: transparent; color: var(--ink-tertiary); cursor: pointer;
}
.plugin-design-align-button:hover { color: var(--ink); }
.plugin-design-align-button[data-active] { background: var(--wuu-color-surface-muted, rgba(127,127,127,.18)); color: var(--ink); }
.plugin-design-align-button svg { width: 13px; height: 13px; }
.plugin-design-opacity input[type="range"] { flex: 1; accent-color: var(--wuu-color-accent, #4a63d8); }
.plugin-design-opacity-value { flex: none; min-width: 34px; text-align: end; font-size: .8em; color: var(--ink-tertiary); font-variant-numeric: tabular-nums; }

/* -- comments -- */
.plugin-design-comment-list { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--space-3, 12px); }
.plugin-design-comment { display: grid; grid-template-columns: auto 1fr auto; gap: var(--space-2, 8px); align-items: start; }
.plugin-design-pin {
  display: grid; place-items: center; width: 20px; height: 20px; margin-block-start: 2px;
  border-radius: 50%; background: #f59e0b; color: #fff;
  font-size: .72em; font-weight: 700;
}
.plugin-design-comment-body { display: grid; gap: 4px; min-width: 0; }
.plugin-design-comment-body label { display: grid; margin: 0; font-size: 0; }
.plugin-design-comment-body label .plugin-ui-field-label { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
.plugin-design-comment-body textarea { font-size: calc(var(--wuu-font-size-ui, 15px) * .9); }
.plugin-design-comment .plugin-ui-textarea { min-height: 44px; }
.plugin-design-field .plugin-ui-textarea { min-height: 64px; }

@container (max-width: 960px) {
  .plugin-design-page-picker { max-width: 170px; }
  .plugin-design-viewport-name { display: none; }
  .plugin-design-inspector { width: 252px; }
}
@container (max-width: 720px) {
  .plugin-design-inspector {
    position: absolute; right: 0; top: 0; bottom: 0; z-index: 5;
    width: 280px; box-shadow: -16px 0 40px rgba(0,0,0,.18);
    background: var(--wuu-color-surface-elevated, var(--paper, #fff));
  }
}
`;
