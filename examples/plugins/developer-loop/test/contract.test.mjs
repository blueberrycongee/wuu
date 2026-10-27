import assert from "node:assert/strict";
import * as React from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
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
  react: { Fragment: Symbol("Fragment"), createElement, useSyncExternalStore: (_subscribe, getSnapshot) => getSnapshot() },
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
  registerUIContribution: register("uiContributions"),
  registerPresenter: register("presenters"),
};

renderer.activate(api);
const presenter = registrations.get("uiContributions").find(item => item.target === "wuu.default-conversation/conversation.tool-activity");
const presented = presenter.render({
  context: { snapshot: {
    id: "call-1",
    toolName: "developer-loop-echo",
    capability: "developer-loop.tool.echo",
    status: "completed",
    argumentsText: "{}",
    resultText: "developer-loop tool ok",
  },
  },
  children: createElement("span", null, "native"),
});
assert.equal(presented.type, "section");
assert.equal(presented.props["data-developer-loop-tool"], "completed");
assert.equal(presented.props["data-tool-id"], "call-1");
assert.equal(presented.children[1].children[0], "developer-loop tool ok");

const presenterByTarget = new Map(registrations.get("presenters").map((definition) => [definition.target, definition]));

const invokedActions = [];
const presentationHost = {
  actions: ["status.activate-item"],
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

const messageContribution = registrations.get("uiContributions").find(item => item.target === "wuu.default-conversation/conversation.message");
const itemOutput = messageContribution.render({ children: fallback, context: { snapshot: {
  contractVersion: 1,
  id: "message-1",
  kind: "assistant-message",
  status: "completed",
  text: "Accepted answer",
} } });
assert.equal(itemOutput.children[0], fallback);
assert.equal(itemOutput.children[1].props["data-item-id"], "message-1");
assert.equal(itemOutput.children[1].props["data-item-status"], "completed");

const application = { subscribe: () => () => {}, getSnapshot: () => ({
  composer: { threadId: "thread-1", activeSubmissionMode: "send" },
  navigation: { activeNodeId: "thread-1", nodes: [{ id: "thread-1" }] },
}) };
const composerContribution = registrations.get("uiContributions").find(item => item.target === "wuu.default-composer/composer.above");
const composerOutput = composerContribution.render({ application });
assert.equal(composerOutput.props["data-thread-id"], "thread-1");
const navigationContribution = registrations.get("uiContributions").find(item => item.target === "wuu.default-navigation/sidebar.footer");
const navigationOutput = navigationContribution.render({ application });
assert.equal(navigationOutput.props["data-node-count"], "1");

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
await renderer.invokePresentationAction(presentationHost, "status.activate-item", { id: "ready" });
assert.deepEqual(invokedActions, [
  ["status.activate-item", { id: "ready" }],
]);
assert.equal(await renderer.invokePresentationAction(presentationHost, "unsupported.action"), undefined);

const dom = new JSDOM('<div id="root"></div>');
Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true });
const realViews = [];
renderer.activate({ ...api, react: React, ui: { ...api.ui, Section: ({ title, children }) => React.createElement("section", null, title, children) }, registerViewType: value => { realViews.push(value); return { dispose() {} }; } });
let stored = "4";
let releaseRestore;
const restoreReady = new Promise(resolve => { releaseRestore = resolve; });
const storedWrites = [];
const host = {
  getStorage: async () => { await restoreReady; return stored; },
  setStorage: async (key, value) => { storedWrites.push([key, value]); stored = value; },
  getSetting: async key => ({ enabled: true, label: "Verified", step: 3, density: "compact" })[key],
  executeCommand: async () => undefined,
  openView: async () => undefined,
};
const root = createRoot(document.getElementById("root"));
const View = realViews[0].render;
await React.act(async () => root.render(React.createElement(View, { host })));
assert.equal(document.querySelector("[data-counter-button]").disabled, true, "cannot overwrite storage before restoration completes");
await React.act(async () => releaseRestore());
assert.equal(document.querySelector("[data-counter-value]").textContent, "4");
assert.equal(document.querySelector("[data-counter-label]").textContent, "Verified");
await React.act(async () => document.querySelector("[data-counter-button]").click());
assert.deepEqual(storedWrites, [["counter", "7"]]);
assert.equal(document.querySelector("[data-counter-value]").textContent, "7");
await React.act(async () => root.render(React.createElement(View, { host, context: { refreshed: true } })));
assert.equal(document.querySelector("[data-counter-value]").textContent, "7", "ordinary renders retain the saved counter");
await React.act(async () => root.render(null));
await React.act(async () => root.render(React.createElement(View, { host })));
assert.equal(document.querySelector("[data-counter-value]").textContent, "7", "new view restores public storage");
await React.act(async () => root.unmount());
dom.window.close();
console.log("developer-loop public SDK contract ok");
