/** @typedef {import("@wuu/plugin-sdk").PluginCommandActionContext} ActionContext */

/** @param {unknown} input @returns {input is ActionContext} */
function isActionContext(input) {
  if (!input || typeof input !== "object" || !("contractVersion" in input) || input.contractVersion !== 1 || !("target" in input)) return false;
  if (input.target === "view.title") {
    return "viewId" in input && typeof input.viewId === "string"
      && "viewTypeId" in input && typeof input.viewTypeId === "string"
      && "viewPluginId" in input && typeof input.viewPluginId === "string";
  }
  return input.target === "conversation.message.actions"
    && "turnId" in input && typeof input.turnId === "string"
    && "item" in input && !!input.item && typeof input.item === "object"
    && "id" in input.item && typeof input.item.id === "string"
    && "text" in input.item && typeof input.item.text === "string";
}

/** @param {import("@wuu/plugin-sdk").PluginGenerationApi} api */
export function activate(api) {
  const React = api.react;
  /** @type {ReadonlyArray<{ key: string, text: string }>} */
  let notes = [];
  let refreshCount = 0;
  let active = true;
  let snapshot = { notes, refreshCount };
  /** @type {Set<() => void>} */
  const listeners = new Set();
  const publish = () => {
    snapshot = { notes, refreshCount };
    for (const notify of listeners) notify();
  };
  /** @param {() => void} notify */
  const subscribe = (notify) => {
    listeners.add(notify);
    return () => { listeners.delete(notify); };
  };
  const getSnapshot = () => snapshot;
  /** @param {ActionContext} context */
  const ownsView = (context) => context.target === "view.title"
    && context.viewPluginId === api.pluginId && context.viewTypeId === "notes";
  /** @param {ActionContext} context */
  const canSave = (context) => context.target === "conversation.message.actions"
    && (context.item.kind === "user-message" || context.item.kind === "assistant-message")
    && context.item.status !== "streaming" && context.item.status !== "pending"
    && !!context.item.text?.trim();

  api.registerViewType({
    id: "notes", title: "Action notes", defaultRegion: "auxiliary", persistence: "session",
    render() {
      const state = React.useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
      return React.createElement("section", { "aria-label": "Action notes" },
        React.createElement("p", { role: "status" }, `Refreshes: ${state.refreshCount}. Notes stay only until this plugin reloads or is disabled.`),
        state.notes.length === 0
          ? React.createElement("p", null, "Use Save to action notes on a conversation message.")
          : React.createElement("ul", null, ...state.notes.map(note =>
            React.createElement("li", { key: note.key }, note.text))),
      );
    },
  });
  api.registerCommand({
    id: "refresh-notes", title: "Refresh action notes", icon: "pulse", order: 10, placements: ["view.title"],
    when: ownsView,
    execute(input) {
      if (!active || !isActionContext(input) || !ownsView(input)) return;
      refreshCount += 1;
      publish();
    },
  });
  api.registerCommand({
    id: "save-message", title: "Save to action notes", icon: "file-text", order: 10, placements: ["conversation.message.actions"],
    when: context => context.target === "conversation.message.actions",
    enabled: canSave,
    execute(input) {
      if (!active || !isActionContext(input) || input.target !== "conversation.message.actions" || !canSave(input)) return;
      const key = JSON.stringify([input.threadId ?? "", input.turnId, input.item.id]);
      if (notes.some(note => note.key === key)) return;
      notes = [...notes, { key, text: input.item.text ?? "" }];
      publish();
    },
  });
  api.registerCleanup(() => {
    active = false;
    notes = [];
    snapshot = { notes, refreshCount };
    listeners.clear();
  });
}
