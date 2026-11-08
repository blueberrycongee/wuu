/* Wuu Design Mode bridge. Injected by the plugin's loopback server into the
 * pages it serves. Talks to the desktop view over postMessage: the parent
 * sends commands tagged "wuu-design", the bridge replies with
 * "wuu-design-bridge" messages carrying the session nonce it was given. */
(() => {
  if (window.__wuuDesignBridge) return;
  window.__wuuDesignBridge = true;

  const MARKER = "data-wuu-design-ui";
  const state = {
    nonce: null,
    mode: "off",
    accent: "#3b82f6",
    selected: null,
    hover: null,
    editing: null,
    comments: [],
    drag: null,
    seq: 0,
  };

  // ---- overlay layer ------------------------------------------------------
  const overlay = document.createElement("div");
  overlay.setAttribute(MARKER, "");
  overlay.style.cssText = [
    "position:fixed", "inset:0", "z-index:2147483646", "pointer-events:none",
    "font:12px/1.4 -apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif",
  ].join(";");
  const hoverBox = document.createElement("div");
  const selectBox = document.createElement("div");
  const label = document.createElement("div");
  const pinLayer = document.createElement("div");
  for (const box of [hoverBox, selectBox]) {
    box.style.cssText = "position:fixed;display:none;border-radius:2px;";
    overlay.append(box);
  }
  function paintAccent() {
    hoverBox.style.outline = `1.5px dashed ${state.accent}`;
    hoverBox.style.background = "color-mix(in srgb, " + state.accent + " 7%, transparent)";
    selectBox.style.outline = `2px solid ${state.accent}`;
    label.style.background = state.accent;
  }
  label.style.cssText = [
    "position:fixed", "display:none", "padding:2px 8px", "border-radius:6px",
    "background:#3b82f6", "color:#fff", "font-size:11px", "font-weight:600",
    "letter-spacing:.01em", "white-space:nowrap", "box-shadow:0 2px 8px rgba(0,0,0,.25)",
    "max-width:70vw", "overflow:hidden", "text-overflow:ellipsis",
  ].join(";");
  paintAccent();
  pinLayer.style.cssText = "position:fixed;inset:0;";
  overlay.append(label, pinLayer);

  function mount() {
    if (!overlay.isConnected) (document.body ?? document.documentElement).append(overlay);
    requestAnimationFrame(() => { if (!overlay.isConnected) (document.body ?? document.documentElement).append(overlay); });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mount, { once: true });
  else mount();

  // ---- geometry + selectors ----------------------------------------------
  function place(node, rect) {
    node.style.left = `${rect.left}px`;
    node.style.top = `${rect.top}px`;
    node.style.width = `${rect.width}px`;
    node.style.height = `${rect.height}px`;
    node.style.display = "block";
  }
  function paint() {
    if (state.hover && state.hover !== state.selected && state.mode === "edit") place(hoverBox, state.hover.getBoundingClientRect());
    else hoverBox.style.display = "none";
    if (state.selected) {
      const rect = state.selected.getBoundingClientRect();
      place(selectBox, rect);
      label.style.display = "block";
      label.textContent = `${tagName(state.selected)}  ·  ${Math.round(rect.width)} × ${Math.round(rect.height)}`;
      const labelRect = label.getBoundingClientRect();
      label.style.left = `${Math.max(4, rect.left)}px`;
      label.style.top = rect.top - labelRect.height - 6 > 0 ? `${rect.top - labelRect.height - 6}px` : `${Math.min(innerHeight - labelRect.height - 4, rect.bottom + 6)}px`;
    } else {
      selectBox.style.display = "none";
      label.style.display = "none";
    }
    for (const pin of pinLayer.children) {
      const comment = state.comments.find((item) => item.id === pin.dataset.id);
      const el = comment && document.querySelector(comment.selector);
      if (!comment || !el) { pin.style.display = "none"; continue; }
      const rect = el.getBoundingClientRect();
      pin.style.display = "grid";
      if (comment.anchor === "tr") {
        pin.style.left = `${Math.min(rect.right, innerWidth - 12) + (comment.offset?.x ?? 0)}px`;
        pin.style.top = `${rect.top + (comment.offset?.y ?? 0)}px`;
      } else {
        pin.style.left = `${rect.left + (comment.offset?.x ?? 8)}px`;
        pin.style.top = `${rect.top + (comment.offset?.y ?? -10)}px`;
      }
    }
  }
  let paintQueued = false;
  function schedulePaint() {
    if (paintQueued) return;
    paintQueued = true;
    requestAnimationFrame(() => { paintQueued = false; paint(); });
  }
  addEventListener("scroll", schedulePaint, { capture: true, passive: true });
  addEventListener("resize", () => {
    schedulePaint();
    if (state.selected) {
      const rect = state.selected.getBoundingClientRect();
      post({ type: "metrics", rect: { x: Math.round(rect.left), y: Math.round(rect.top), w: Math.round(rect.width), h: Math.round(rect.height) } });
    }
  }, { passive: true });
  new MutationObserver(schedulePaint).observe(document.documentElement, { subtree: true, childList: true, attributes: true, characterData: true });

  function tagName(el) {
    const id = el.id ? `#${el.id}` : "";
    const cls = typeof el.className === "string" && el.className.trim() ? `.${el.className.trim().split(/\s+/).slice(0, 2).join(".")}` : "";
    return `${el.tagName.toLowerCase()}${id}${cls}`;
  }
  function selectorFor(el) {
    if (!(el instanceof Element)) return "";
    if (el.id) return `#${CSS.escape(el.id)}`;
    const parts = [];
    let node = el;
    while (node && node !== document.documentElement && parts.length < 6) {
      if (node.id) { parts.unshift(`#${CSS.escape(node.id)}`); break; }
      const tag = node.tagName.toLowerCase();
      const parent = node.parentElement;
      if (!parent) { parts.unshift(tag); break; }
      const siblings = [...parent.children].filter((child) => child.tagName === node.tagName);
      parts.unshift(siblings.length > 1 ? `${tag}:nth-of-type(${siblings.indexOf(node) + 1})` : tag);
      node = parent;
    }
    return parts.join(" > ") || el.tagName.toLowerCase();
  }
  function summarize(el) {
    const style = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    const px = (v) => Math.round(parseFloat(v) || 0);
    return {
      selector: selectorFor(el),
      tag: tagName(el),
      text: (el.innerText ?? el.textContent ?? "").trim().slice(0, 400),
      rect: { x: Math.round(rect.left), y: Math.round(rect.top), w: Math.round(rect.width), h: Math.round(rect.height) },
      styles: {
        color: style.color,
        backgroundColor: style.backgroundColor,
        fontSize: style.fontSize,
        fontWeight: style.fontWeight,
        lineHeight: style.lineHeight,
        padding: `${px(style.paddingTop)} ${px(style.paddingRight)} ${px(style.paddingBottom)} ${px(style.paddingLeft)}`,
        margin: `${px(style.marginTop)} ${px(style.marginRight)} ${px(style.marginBottom)} ${px(style.marginLeft)}`,
        width: style.width,
        height: style.height,
        borderRadius: style.borderRadius,
        opacity: style.opacity,
      },
    };
  }

  // ---- messaging -----------------------------------------------------------
  function post(message) {
    if (!state.nonce) return;
    parent.postMessage({ source: "wuu-design-bridge", nonce: state.nonce, ...message }, "*");
  }
  function emitOp(op) { post({ type: "op", op }); }

  // ---- modes ---------------------------------------------------------------
  function isOurs(el) { return el instanceof Element && Boolean(el.closest?.(`[${MARKER}]`)); }
  function deselect() {
    state.selected = null;
    endTextEdit(false);
    post({ type: "deselect" });
    schedulePaint();
  }
  function select(el) {
    if (state.selected === el) return;
    endTextEdit(false);
    state.selected = el;
    post({ type: "select", element: summarize(el) });
    schedulePaint();
  }

  function endTextEdit(commit) {
    const el = state.editing;
    if (!el) return;
    state.editing = null;
    el.removeAttribute("contenteditable");
    el.removeAttribute("data-wuu-design-editing");
    if (commit) {
      const text = (el.innerText ?? "").replace(/\n{3,}/g, "\n\n").trimEnd();
      if (text !== el.__wuuDesignOriginal) emitOp({ kind: "text", selector: selectorFor(el), text });
    }
    delete el.__wuuDesignOriginal;
    schedulePaint();
  }
  function beginTextEdit(el) {
    if (state.editing === el) return;
    endTextEdit(true);
    state.editing = el;
    el.__wuuDesignOriginal = (el.innerText ?? "").trimEnd();
    el.setAttribute("data-wuu-design-editing", "");
    el.contentEditable = "true";
    el.focus();
  }

  document.addEventListener("mouseover", (event) => {
    if (state.mode !== "edit" && state.mode !== "comment") return;
    if (!(event.target instanceof Element) || isOurs(event.target)) return;
    state.hover = event.target;
    schedulePaint();
  }, true);
  document.addEventListener("mouseout", (event) => {
    if (state.hover === event.target) { state.hover = null; schedulePaint(); }
  }, true);
  document.addEventListener("click", (event) => {
    if (state.mode === "off" || !(event.target instanceof Element) || isOurs(event.target)) return;
    event.preventDefault();
    event.stopPropagation();
    if (state.editing) { endTextEdit(true); return; }
    if (state.mode === "edit") {
      if (event.target === state.selected) return;
      select(event.target);
    } else if (state.mode === "comment") {
      addComment(event.target);
    }
  }, true);
  document.addEventListener("dblclick", (event) => {
    if (state.mode !== "edit" || !(event.target instanceof Element) || isOurs(event.target)) return;
    const el = event.target === state.selected ? event.target : event.target.closest("p,h1,h2,h3,h4,h5,h6,span,a,li,button,label,td,th,figcaption,blockquote,strong,em,div");
    if (!el) return;
    event.preventDefault();
    select(el);
    beginTextEdit(el);
  }, true);
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      if (state.editing) { endTextEdit(false); event.stopPropagation(); }
      else if (state.selected) { deselect(); event.stopPropagation(); }
    }
  }, true);
  document.addEventListener("focusout", (event) => {
    if (state.editing && event.target === state.editing) endTextEdit(true);
  }, true);

  // ---- drag to move ---------------------------------------------------------
  document.addEventListener("pointerdown", (event) => {
    if (state.mode !== "edit" || event.button !== 0 || state.editing || !(event.target instanceof Element) || isOurs(event.target)) return;
    const el = event.target === state.selected ? state.selected : null;
    if (!el || !el.isConnected) return;
    state.drag = { el, x0: event.clientX, y0: event.clientY, base: el.style.transform || "", moved: false };
  }, true);
  document.addEventListener("pointermove", (event) => {
    const drag = state.drag;
    if (!drag) return;
    const dx = event.clientX - drag.x0;
    const dy = event.clientY - drag.y0;
    if (!drag.moved && Math.hypot(dx, dy) < 3) return;
    drag.moved = true;
    drag.dx = dx; drag.dy = dy;
    const translate = `translate(${Math.round(dx)}px, ${Math.round(dy)}px)`;
    drag.el.style.transform = drag.base ? `${drag.base} ${translate}` : translate;
    schedulePaint();
  }, true);
  document.addEventListener("pointerup", () => {
    const drag = state.drag;
    state.drag = null;
    if (!drag?.moved) return;
    emitOp({ kind: "style", selector: selectorFor(drag.el), prop: "transform", value: drag.el.style.transform });
  }, true);

  // ---- comments --------------------------------------------------------------
  function pinElement(comment) {
    const pin = document.createElement("div");
    pin.dataset.id = comment.id;
    pin.textContent = String(comment.index);
    pin.style.cssText = [
      "position:fixed", "display:grid", "place-items:center", "width:22px", "height:22px",
      "border-radius:11px", "background:#f59e0b", "color:#fff", "font-size:12px",
      "font-weight:700", "box-shadow:0 1px 6px rgba(0,0,0,.3), 0 0 0 2px rgba(255,255,255,.92)",
      "transform:translate(-50%,-50%)", "transition:transform .15s ease",
      "pointer-events:none",
    ].join(";");
    pinLayer.append(pin);
    return pin;
  }
  function addComment(el) {
    const comment = {
      id: `c${++state.seq}`,
      index: state.comments.length + 1,
      selector: selectorFor(el),
      tag: tagName(el),
      snippet: (el.innerText ?? el.textContent ?? "").trim().slice(0, 80),
      anchor: "tr",
      offset: { x: -2, y: -2 },
    };
    state.comments.push(comment);
    pinElement(comment);
    post({ type: "comment-draft", comment });
    schedulePaint();
  }
  function removeComment(id) {
    const index = state.comments.findIndex((item) => item.id === id);
    if (index === -1) return;
    state.comments.splice(index, 1);
    state.comments.forEach((item, i) => { item.index = i + 1; });
    pinLayer.querySelector(`[data-id="${id}"]`)?.remove();
    for (const pin of pinLayer.children) {
      const comment = state.comments.find((item) => item.id === pin.dataset.id);
      if (comment) pin.textContent = String(comment.index);
    }
    schedulePaint();
  }
  function restoreComments(comments) {
    state.comments = [];
    pinLayer.replaceChildren();
    for (const item of comments ?? []) {
      const comment = {
        id: item.id ?? `c${++state.seq}`,
        index: state.comments.length + 1,
        selector: item.selector,
        tag: item.tag,
        snippet: item.snippet,
        text: item.text,
        anchor: item.anchor,
        offset: item.offset ?? (item.anchor === "tr" ? { x: -2, y: -2 } : { x: 8, y: -10 }),
      };
      if (!comment.selector || !document.querySelector(comment.selector)) continue;
      state.comments.push(comment);
      pinElement(comment);
      const numeric = /^c(\d+)$/.exec(comment.id ?? "");
      if (numeric) state.seq = Math.max(state.seq, Number(numeric[1]));
    }
    schedulePaint();
  }

  // ---- ops + serialize --------------------------------------------------------
  function applyOp(op) {
    if (!op || typeof op !== "object") return { ok: false, error: "invalid op" };
    const el = document.querySelector(op.selector ?? "");
    if (!el) return { ok: false, error: "element no longer exists" };
    switch (op.kind) {
      case "style":
        if (typeof op.prop !== "string") return { ok: false, error: "style op requires prop" };
        if (op.value === null || op.value === "") el.style.removeProperty(op.prop);
        else el.style.setProperty(op.prop, String(op.value), op.priority === "important" ? "important" : "");
        break;
      case "text":
        el.innerText = String(op.text ?? "");
        break;
      case "html":
        el.innerHTML = String(op.html ?? "");
        break;
      default:
        return { ok: false, error: `unknown op ${op.kind}` };
    }
    schedulePaint();
    return { ok: true };
  }
  function serialize() {
    const clone = document.documentElement.cloneNode(true);
    clone.querySelectorAll(`[${MARKER}]`).forEach((node) => node.remove());
    clone.querySelectorAll("script[data-wuu-design-bridge]").forEach((node) => node.remove());
    clone.querySelectorAll("[contenteditable],[data-wuu-design-editing]").forEach((node) => {
      node.removeAttribute("contenteditable");
      node.removeAttribute("data-wuu-design-editing");
    });
    const doctype = document.doctype ? `<!DOCTYPE ${document.doctype.name}>` : "<!DOCTYPE html>";
    return `${doctype}\n${clone.outerHTML}`;
  }

  // ---- parent commands ---------------------------------------------------------
  addEventListener("message", (event) => {
    const message = event.data;
    if (!message || message.source !== "wuu-design") return;
    if (message.type === "init") {
      state.nonce = message.nonce;
      if (typeof message.accent === "string" && message.accent) { state.accent = message.accent; paintAccent(); }
      state.mode = message.mode === "comment" ? "comment" : message.mode === "off" ? "off" : "edit";
      post({ type: "ready", url: location.pathname, title: document.title });
      schedulePaint();
      return;
    }
    if (!state.nonce || message.nonce !== state.nonce) return;
    switch (message.type) {
      case "set-mode":
        state.mode = message.mode === "comment" ? "comment" : message.mode === "off" ? "off" : "edit";
        if (state.mode !== "edit") { state.hover = null; endTextEdit(state.mode !== "off"); }
        schedulePaint();
        break;
      case "apply":
        post({ type: "applied", id: message.id, result: applyOp(message.op) });
        break;
      case "serialize":
        post({ type: "serialized", id: message.id, html: serialize() });
        break;
      case "select": {
        const el = document.querySelector(message.selector ?? "");
        if (el) select(el);
        break;
      }
      case "deselect": deselect(); break;
      case "comment-remove": removeComment(message.id); break;
      case "comments-restore": restoreComments(message.comments); break;
      case "comment-focus":
        for (const pin of pinLayer.children) {
          const focused = pin.dataset.id === message.id;
          pin.style.transform = focused ? "translate(-50%,-130%) scale(1.28)" : "translate(-50%,-115%)";
        }
        break;
    }
  });
  // Announce ourselves so the parent can (re)initialize after navigation.
  if (parent !== window) parent.postMessage({ source: "wuu-design-bridge", type: "hello", url: location.pathname }, "*");
})();
