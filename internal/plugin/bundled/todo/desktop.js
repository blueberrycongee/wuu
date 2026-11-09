export async function activate(api) {
  const h = api.react.createElement;

  api.registerUIContribution({
    id: "current-todo",
    target: "wuu.default-shell/inspector.sections",
    order: 10,
    render({ context }) {
      const items = context.snapshot.todo?.items;
      if (!items?.length) return null;
      return h("section", { className: "plugin-inspector-section", "data-plugin-id": api.pluginId, "data-plugin-generation": api.generation },
        h("h2", null, "TODO"),
        h("div", { className: "plugin-inspector-section-content" }, renderTodos(h, items)));
    },
  });

  for (const owner of ["wuu.default-conversation", "wuu.default-preview"]) {
    api.registerUIContribution({
      id: `todo-tool-${owner}`,
      target: `${owner}/conversation.tool-activity`,
      mode: "wrap",
      order: 20,
      render({ context, children }) {
        const snapshot = context.snapshot;
        if ((snapshot.capability ?? snapshot.toolName) !== "todo") return children;
        const items = parseTodos(snapshot.argumentsText);
        return items ? renderTodos(h, items) : children;
      },
    });
  }

  api.registerCSSSnippet({
    id: "todo-layout",
    css: `
      .plugin-todo-list {
        display: grid;
        gap: calc(var(--wuu-space-unit, 4px) * 1.5 * var(--wuu-space-density, 1));
        margin: 0;
        padding: 0;
        list-style: none;
        line-height: 1.5;
      }
      .plugin-todo-item {
        display: grid;
        grid-template-columns: 14px minmax(0, 1fr);
        column-gap: 8px;
        align-items: start;
        min-width: 0;
        overflow-wrap: anywhere;
      }
      .plugin-todo-marker {
        display: grid;
        height: 1lh;
        place-items: center;
        color: var(--wuu-color-text-muted, currentColor);
      }
      .plugin-todo-marker svg {
        display: block;
        width: 14px;
        height: 14px;
      }
      .plugin-todo-item[data-status="in_progress"] .plugin-todo-marker {
        color: var(--wuu-color-text, currentColor);
      }
      .plugin-todo-item[data-status="completed"] {
        color: var(--wuu-color-text-muted, currentColor);
      }
    `,
  });
}

function parseTodos(raw) {
  if (typeof raw !== "string" || raw.trim() === "") return undefined;
  try {
    const value = JSON.parse(raw);
    return Array.isArray(value?.todos) ? value.todos : undefined;
  } catch {
    return undefined;
  }
}

function renderTodos(h, items) {
  if (!Array.isArray(items) || items.length === 0) return null;
  return h(
    "ol",
    { className: "plugin-todo-list" },
    items.map((item, index) => h(
      "li",
      { className: "plugin-todo-item", "data-status": item.status, key: `${index}:${item.content}` },
      h("span", { className: "plugin-todo-marker", "aria-hidden": true }, marker(h, item.status)),
      h("span", null, item.content),
    )),
  );
}

function marker(h, status) {
  // SVG keeps all three states centered independently of font glyph metrics.
  return h("svg", {
    viewBox: "0 0 24 24", width: 14, height: 14, fill: "none",
    stroke: "currentColor", strokeWidth: 1.6, strokeLinecap: "round",
    strokeLinejoin: "round", focusable: "false",
  }, status === "completed"
    ? h("path", { d: "m5 12 4 4L19 6" })
    : h("circle", { cx: 12, cy: 12, r: 8, fill: status === "in_progress" ? "currentColor" : "none" }));
}
