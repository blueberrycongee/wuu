import assert from "node:assert/strict";
import { activate } from "../desktop.js";

const commands = new Map();
const views = new Map();
const cleanups = [];
const api = {
  pluginId: "action-notes-example",
  react: {
    createElement: (type, props, ...children) => ({ type, props, children }),
    useSyncExternalStore: (_subscribe, snapshot) => snapshot(),
  },
  registerCommand(command) { commands.set(command.id, command); },
  registerViewType(view) { views.set(view.id, view); },
  registerCleanup(cleanup) { cleanups.push(cleanup); },
};
activate(api);
const refresh = commands.get("refresh-notes");
const save = commands.get("save-message");
const view = views.get("notes");
const ownView = { contractVersion: 1, target: "view.title", viewPluginId: api.pluginId, viewTypeId: "notes", viewId: "notes-instance", region: "auxiliary" };
const message = { contractVersion: 1, target: "conversation.message.actions", threadId: "synthetic-thread", turnId: "synthetic-turn", item: { contractVersion: 1, id: "synthetic-message", kind: "assistant-message", status: "completed", text: "A synthetic note" } };
const output = () => JSON.stringify(view.render());
assert.deepEqual(refresh.placements, ["view.title"]);
assert.deepEqual(save.placements, ["conversation.message.actions"]);
assert.equal(refresh.when(ownView), true);
assert.equal(refresh.when({ ...ownView, viewPluginId: "another-plugin" }), false);
assert.equal(refresh.when({ ...ownView, viewTypeId: "another-view" }), false);
assert.equal(save.when(ownView), false);
assert.equal(save.enabled({ ...message, item: { ...message.item, text: " " } }), false);
assert.equal(save.enabled({ ...message, item: { ...message.item, status: "streaming" } }), false);
const before = output();
refresh.execute();
save.execute();
assert.equal(output(), before, "palette invocation without context is inert");
save.execute(message);
assert.match(output(), /A synthetic note/);
const saved = output();
save.execute(message);
assert.equal(output(), saved, "same message is not added twice");
refresh.execute(ownView);
assert.notEqual(output(), saved, "own view refresh updates its snapshot");
for (const cleanup of cleanups) cleanup();
const retired = output();
save.execute({ ...message, item: { ...message.item, id: "late", text: "Late callback" } });
refresh.execute(ownView);
assert.equal(output(), retired, "retired generation rejects retained callbacks");
assert.doesNotMatch(retired, /A synthetic note/);
console.log("action-notes command contracts passed");
