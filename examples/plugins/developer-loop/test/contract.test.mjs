import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const manifest = JSON.parse(await readFile(new URL("../plugin.json", import.meta.url), "utf8"));
const output = await readFile(new URL("../dist/renderer.js", import.meta.url), "utf8");
const renderer = await import(new URL("../dist/renderer.js", import.meta.url));

assert.doesNotMatch(output, /react(?:-dom)?["'/]|node_modules\/react/);

const registrations = new Map();
const register = (kind) => (value) => {
  const values = registrations.get(kind) ?? [];
  values.push(value);
  registrations.set(kind, values);
  return { dispose() {} };
};
const createElement = (type, props, ...children) => ({ type, props: props ?? {}, children });
const api = {
  pluginId: manifest.id,
  generation: "acceptance-generation",
  react: { Fragment: Symbol("Fragment"), createElement },
  ui: {
    Page: "section",
    Panel: "section",
    Card: "article",
    Section: "section",
    Stack: "div",
    Row: "div",
    Button: "button",
    TextInput: "input",
    EmptyState: "section",
    LoadingState: "section",
    ErrorState: "section",
  },
  registerViewType: register("views"),
  registerViewPlacement: register("viewPlacements"),
  registerThemeTokens: register("themes"),
  registerCommand: register("commands"),
  registerStatusItem: register("status"),
  registerLocale: register("locales"),
  registerSlot: (_slot, contribution) => register("slots")(contribution),
  registerPresenter: register("presenters"),
  registerToolActivityPresenter: register("toolActivityPresenters"),
};

renderer.activate(api);
const presenter = registrations.get("toolActivityPresenters")[0];
const presented = presenter.render({
  activity: {
    id: "call-1",
    toolName: "developer-loop-echo",
    capability: presenter.key,
    status: "completed",
    argumentsText: "{}",
    resultText: "developer-loop tool ok",
  },
  host: {},
  fallback: createElement("span", null, "native"),
});
assert.equal(presented.type, "section");
assert.equal(presented.props["data-developer-loop-tool"], "completed");
assert.equal(presented.props["data-tool-id"], "call-1");
assert.equal(presented.children[1].children[0], "developer-loop tool ok");

const presenterByTarget = new Map(registrations.get("presenters").map((definition) => [definition.target, definition]));

const invokedActions = [];
const presentationHost = {
  actions: ["conversation.composer.submit", "navigation.activate-node", "status.activate-item"],
  invoke: async (action, input) => {
    invokedActions.push([action, input]);
    return { accepted: true };
  },
};
const fallback = createElement("span", { "data-native-fallback": "" }, "native");
const renderPresenter = (target, snapshot, key) => {
  const definition = presenterByTarget.get(target);
  assert.equal(definition.key, key);
  return definition.render({ contractVersion: 1, target, key, snapshot, host: presentationHost, fallback });
};

const itemOutput = renderPresenter("conversation.item", {
  contractVersion: 1,
  id: "message-1",
  kind: "assistant-message",
  status: "completed",
  text: "Accepted answer",
}, "assistant-message");
assert.equal(itemOutput.children[0], fallback);
assert.equal(itemOutput.children[1].props["data-item-id"], "message-1");
assert.equal(itemOutput.children[1].props["data-item-status"], "completed");

const composerOutput = renderPresenter("conversation.composer", {
  contractVersion: 1,
  threadId: "thread-1",
  draftText: "Ship it",
  activeSubmissionMode: "send",
  running: false,
});
assert.equal(composerOutput.children[0], fallback);
assert.equal(composerOutput.children[1].props["data-thread-id"], "thread-1");

const navigationOutput = renderPresenter("navigation.primary", {
  contractVersion: 1,
  activeNodeId: "thread-1",
  nodes: [{ id: "thread-1", kind: "thread", label: "Acceptance thread", active: true }],
});
assert.equal(navigationOutput.children[0], fallback);
assert.equal(navigationOutput.children[1].props["data-node-count"], "1");

const previewOutput = renderPresenter("content.preview", {
  contractVersion: 1,
  resourceId: "resource-1",
  workspaceRelativePath: "acceptance.md",
  contentType: "text/markdown",
  text: "# Accepted",
  readOnly: true,
}, "text/markdown");
assert.equal(previewOutput.children[0], fallback);
assert.equal(previewOutput.children[1].props["data-content-type"], "text/markdown");

const statusOutput = renderPresenter("app.status", {
  contractVersion: 1,
  items: [{ id: "ready", label: "Ready", kind: "success", actionId: "open" }],
});
assert.equal(statusOutput.children[0], fallback);
assert.equal(statusOutput.children[1].props["data-status-count"], "1");

const headerOutput = renderPresenter("header.conversation", {
  contractVersion: 1,
  scope: "conversation",
  title: "Acceptance conversation",
  activeTabId: "tab-1",
});
assert.equal(headerOutput.children[0], fallback);
assert.equal(headerOutput.children[1].props["data-active-tab"], "tab-1");
await renderer.invokePresentationAction(presentationHost, "conversation.composer.submit");
await renderer.invokePresentationAction(presentationHost, "navigation.activate-node", { id: "thread-1" });
await renderer.invokePresentationAction(presentationHost, "status.activate-item", { id: "ready" });
assert.deepEqual(invokedActions, [
  ["conversation.composer.submit", undefined],
  ["navigation.activate-node", { id: "thread-1" }],
  ["status.activate-item", { id: "ready" }],
]);
assert.equal(await renderer.invokePresentationAction(presentationHost, "unsupported.action"), undefined);

const storedWrites = [];
const host = {
  getStorage: async (key) => {
    assert.equal(key, "counter");
    return "4";
  },
  setStorage: async (key, value) => storedWrites.push([key, value]),
  getSetting: async (key) => ({ enabled: true, label: "Verified", step: 3, density: "compact" })[key],
  executeCommand: async () => undefined,
  openView: async () => undefined,
};
const nodes = new Map([
  ["[data-counter-value]", { textContent: "" }],
  ["[data-counter-label]", { textContent: "" }],
  ["[data-counter-button]", { textContent: "", disabled: false }],
]);
const root = { dataset: {}, querySelector: (selector) => nodes.get(selector) ?? null };
const view = registrations.get("views")[0].render({ host });
view.props.ref(root);
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(nodes.get("[data-counter-value]").textContent, "4");
assert.equal(nodes.get("[data-counter-label]").textContent, "Verified");
assert.equal(root.dataset.wuuDensity, "compact");
const findElement = (node, predicate) => {
  if (!node || typeof node !== "object") return undefined;
  if (predicate(node)) return node;
  for (const child of node.children ?? []) {
    const found = findElement(child, predicate);
    if (found) return found;
  }
  return undefined;
};
const button = findElement(view, (node) => node.props?.["data-counter-button"] === "");
assert.ok(button);
await button.props.onClick();
assert.deepEqual(storedWrites, [["counter", "7"]]);
assert.equal(nodes.get("[data-counter-value]").textContent, "7");

console.log("developer-loop public SDK contract ok");
