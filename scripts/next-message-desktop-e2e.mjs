// Deterministic public desktop-plugin contract scenarios. The rendered App fixture
// separately checks keyboard behavior and pixels; this driver uses no model.
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const moduleURL = pathToFileURL(
  resolve("internal/plugin/bundled/next-message/desktop.js"),
).href;
const { activate } = await import(moduleURL);
const workspaces = [
  { id: "a", root: "/a", available: true },
  { id: "b", root: "/b", available: true },
];
async function fixture() {
  let provider, listener;
  const cleanup = [];
  const calls = [];
  const historyRequests = [];
  let history = async () => ({ turns: [] });
  const refreshes = [];
  let response = async () => ({ suggestion: "Run the relevant tests" });
  const lifetime = new AbortController();
  const api = {
    react: { createElement() {} },
    ui: {},
    signal: lifetime.signal,
    listWorkspaces: async () => ({ workspaces }),
    readVisibleHistory: async (request) => {
      historyRequests.push(request);
      return history(request);
    },
    invokeRuntime: async (method, input, options) => {
      calls.push({ method, input, options });
      return response(method, input, options);
    },
    registerComposerInlineCompletionProvider(p) {
      provider = p;
      return {
        refresh(scope) {
          refreshes.push(scope);
        },
        dispose() {},
      };
    },
    onHostEvent(f) {
      listener = f;
      return { dispose() {} };
    },
    registerCleanup(f) {
      cleanup.push(f);
    },
    registerLocale() {},
    registerStyle() {},
    registerViewType() {},
  };
  await activate(api);
  const context = (patch = {}) => ({
    contractVersion: 1,
    kind: "main",
    composerId: "c",
    revision: 1,
    threadId: "same",
    workspaceId: "a",
    workspaceRoot: "/a",
    attachmentCount: 0,
    queuedCount: 0,
    pendingCount: 0,
    draftText: "",
    selectionStart: 0,
    selectionEnd: 0,
    running: false,
    ...patch,
  });
  const event = (method, turn, root = "/a") =>
    listener({
      kind: "notification",
      workdir: root,
      message: { method, params: { thread_id: "same", turn } },
    });
  const turn = (id = "t", patch = {}) => ({
    id,
    kind: "user",
    items_view: "full",
    status: "completed",
    items: [
      {
        id: "u",
        type: "user_message",
        text: "Fix error handling",
        status: "completed",
      },
      { id: "r", type: "reasoning", text: "DO NOT TRANSMIT" },
      { id: "x", type: "tool_call", text: "DO NOT TRANSMIT" },
      {
        id: "m",
        type: "agent_message",
        terminal: true,
        status: "completed",
        text: "Fixed it; tests have not run.",
      },
    ],
    ...patch,
  });
  const start = (id = "t", root = "/a") =>
    event("turn/started", turn(id, { status: "in_progress" }), root);
  const complete = (id = "t", patch = {}, root = "/a") =>
    event("turn/completed", turn(id, patch), root);
  const provide = (patch = {}, controller = new AbortController()) =>
    provider.provideCompletion(context(patch), controller.signal);
  return {
    api,
    calls,
    historyRequests,
    refreshes,
    context,
    event,
    turn,
    start,
    complete,
    provide,
    provider,
    emit: (value) => listener(value),
    setResponse: (f) => {
      response = f;
    },
    setHistory: (f) => { history = f; },
    close() {
      lifetime.abort();
      cleanup.forEach((f) => f());
    },
  };
}
const receipts = [];
async function scenario(name, run) {
  const f = await fixture();
  try {
    await run(f);
    receipts.push({ name, passed: true });
  } finally {
    f.close();
  }
}
await scenario("historical completion does not trigger", async (f) => {
  f.complete();
  assert.equal(await f.provide(), null);
  assert.equal(f.calls.length, 0);
});
await scenario(
  "live success is one tools-free visible-turn request",
  async (f) => {
    f.start();
    f.complete();
    const got = await f.provide();
    assert.equal(got.text, "Run the relevant tests");
    assert.deepEqual(f.calls[0].input, {
      threadId: "same",
      turnId: "t",
      turns: [{ id: "t", userText: "Fix error handling", assistantText: "Fixed it; tests have not run.", imageCount: 0, fileCount: 0 }],
    });
    assert.equal(f.calls[0].options.workspaceId, "a");
    f.complete();
    assert.deepEqual(await f.provide({ revision: 2 }), got);
    assert.equal(f.calls.length, 1);
    f.provider.onDidInteract({ type: "dismissed", suggestionId: got.id });
    assert.equal(await f.provide({ revision: 3 }), null);
  },
);
await scenario(
  "failed interrupted internal incomplete and generated turns abstain",
  async (f) => {
    for (const [id, patch] of [
      ["fail", { status: "failed" }],
      ["stop", { status: "interrupted" }],
      ["internal", { kind: "internal" }],
      ["compact", { kind: "compact" }],
      ["missing", { items: [] }],
      [
        "no-final",
        {
          items: [
            { type: "user_message", text: "User" },
            { type: "agent_message", text: "Commentary" },
          ],
        },
      ],
      [
        "generated",
        {
          items: [
            { type: "user_message", text: "Wake", origin: "plugin" },
            { type: "agent_message", terminal: true, text: "Answer" },
          ],
        },
      ],
      [
        "remote",
        {
          items: [
            { type: "user_message", text: "User", remote_content_ref: "ref" },
            { type: "agent_message", terminal: true, text: "Answer" },
          ],
        },
      ],
    ]) {
      f.start(id);
      f.complete(id, patch);
      assert.equal(await f.provide(), null);
    }
    assert.equal(f.calls.length, 0);
  },
);
await scenario(
  "truncated or partial source text is never forwarded",
  async (f) => {
    const cases = [
      { truncated: true },
      { finish_reason: "length" },
      { items_view: "conversation" },
      { items_view: undefined },
      {
        items: [
          { type: "user_message", text: "User", status: "failed" },
          { type: "agent_message", terminal: true, text: "Answer" },
        ],
      },
      {
        items: [
          { type: "user_message", text: "User" },
          {
            type: "agent_message",
            terminal: true,
            text: "Answer",
            truncated: true,
          },
        ],
      },
      {
        items: [
          { type: "user_message", text: "User" },
          {
            type: "agent_message",
            terminal: true,
            text: "Answer",
            status: "interrupted",
          },
        ],
      },
      {
        items: [
          { type: "user_message", text: "User", files: {} },
          { type: "agent_message", terminal: true, text: "Answer" },
        ],
      },
    ];
    for (const [i, patch] of cases.entries()) {
      f.start(String(i));
      f.complete(String(i), patch);
      assert.equal(await f.provide(), null);
    }
    assert.equal(f.calls.length, 0);
  },
);
await scenario("only exact empty ordinary inputs qualify", async (f) => {
  f.start();
  f.complete();
  for (const patch of [
    { draftText: " " },
    { draftText: "x" },
    { kind: "side" },
    { running: true },
    { attachmentCount: 1 },
    { queuedCount: 1 },
    { pendingCount: 1 },
    { workspaceRoot: "/unregistered" },
  ])
    assert.equal(await f.provide(patch), null);
  assert.ok(await f.provide());
  assert.equal(f.calls.length, 1);
});
await scenario("recent visible turns precede the live source turn", async (f) => {
  const older = { id: "older", userText: "First question", assistantText: "First answer", imageCount: 0, fileCount: 0 };
  f.setHistory(async () => ({ turns: [older] }));
  f.start();
  f.complete();
  assert.ok(await f.provide());
  assert.deepEqual(f.calls[0].input.turns.map((turn) => turn.id), ["older", "t"]);
  assert.equal(f.historyRequests[0].beforeTurnId, "t");
});
await scenario("no-project live conversation uses its own root without a project ID", async (f) => {
  f.start("scratch", "/scratch");
  f.complete("scratch", {}, "/scratch");
  assert.ok(await f.provide({ workspaceId: undefined, workspaceRoot: "/scratch" }));
  assert.equal(f.calls[0].options.workspaceId, undefined);
  assert.equal(f.historyRequests[0].workspaceRoot, "/scratch");
});
await scenario("side conversation uses only its own completed message history", async (f) => {
  f.setHistory(async () => ({ turns: [{ id: "reply", userText: "Side question", assistantText: "Side answer", imageCount: 0, fileCount: 0 }] }));
  const emit = (params) => f.emit({ kind: "notification", workdir: "/a", message: { method: "sideThread/event", params: {
    main_thread_id: "same", side_thread_id: "side-one", ...params,
  } } });
  emit({ type: "status", summary: { status: "running", revision: 1 } });
  emit({ type: "message", revision: 2, message: { id: "reply", role: "assistant", text: "Side answer", status: "completed" } });
  const got = await f.provide({ kind: "side", sideThreadId: "side-one" });
  assert.ok(got);
  assert.equal(f.calls[0].input.threadId, "side-one");
  assert.equal(f.historyRequests[0].kind, "side");
  assert.deepEqual(await f.provide({ kind: "side", sideThreadId: "side-one", revision: 2 }), got);
});
await scenario("attachment source sends category counts but never media bytes", async (f) => {
  f.start();
  f.complete("t", { items: [
    { type: "user_message", text: "What next?", images: [{ data: "SECRET_IMAGE" }] },
    { type: "agent_message", terminal: true, text: "I summarized it." },
  ] });
  assert.ok(await f.provide());
  assert.equal(f.calls[0].input.turns.at(-1).imageCount, 1);
  assert.equal(JSON.stringify(f.calls[0].input).includes("SECRET_IMAGE"), false);
  f.start("selection");
  f.complete("selection", { items: [
    { type: "user_message", text: "SECRET_FILE_PATH\nWhat next?", content_parts: [
      { type: "file_selection", text: "SECRET_FILE_PATH\n" },
      { type: "text", text: "What next?" },
    ] },
    { type: "agent_message", terminal: true, text: "I reviewed it." },
  ] });
  assert.ok(await f.provide());
  assert.deepEqual(f.calls[1].input.turns.at(-1), {
    id: "selection", userText: "What next?", assistantText: "I reviewed it.", imageCount: 0, fileCount: 1,
  });
  assert.equal(JSON.stringify(f.calls[1].input).includes("SECRET_FILE_PATH"), false);
  f.start("image-only");
  f.complete("image-only", { items: [
    { type: "user_message", text: "", images: [{ data: "SECRET_IMAGE" }] },
    { type: "agent_message", terminal: true, text: "I looked at it." },
  ] });
  assert.equal(await f.provide(), null);
  assert.equal(f.calls.length, 2);
});
await scenario("same thread ID is isolated across workspaces", async (f) => {
  f.start();
  f.complete();
  assert.equal(
    await f.provide({ workspaceId: "b", workspaceRoot: "/b" }),
    null,
  );
  f.start("bt", "/b");
  f.complete("bt", {}, "/b");
  assert.ok(await f.provide({ workspaceId: "b", workspaceRoot: "/b" }));
  assert.equal(f.calls[1].input.turnId, "bt");
  assert.equal(f.calls[1].options.workspaceId, "b");
  assert.ok(await f.provide());
  assert.equal(f.calls[0].options.workspaceId, "a");
});
await scenario(
  "draft cancellation does not cancel background generation",
  async (f) => {
    let settle;
    f.setResponse(() => new Promise((r) => (settle = r)));
    f.start();
    f.complete();
    const c = new AbortController();
    const pending = f.provide({}, c);
    await new Promise((r) => setImmediate(r));
    assert.equal(f.calls.length, 1);
    c.abort();
    assert.equal(f.calls[0].options.signal.aborted, false);
    settle({ suggestion: "STALE" });
    assert.equal(await pending, null);
    assert.equal((await f.provide({ revision: 2 })).text, "STALE");
    assert.equal(f.calls.length, 1);
  },
);
await scenario(
  "new turn aborts pending prediction and stale result",
  async (f) => {
    let settle;
    f.setResponse(() => new Promise((r) => (settle = r)));
    f.start();
    f.complete();
    const pending = f.provide();
    await new Promise((r) => setImmediate(r));
    f.start("new");
    assert.equal(f.calls[0].options.signal.aborted, true);
    settle({ suggestion: "STALE" });
    assert.equal(await pending, null);
  },
);
await scenario(
  "server disconnect and history resume fence live turn",
  async (f) => {
    f.start();
    f.event("thread/resumed", f.turn());
    f.complete();
    assert.equal(await f.provide(), null);
    f.start("two");
    f.complete("two");
    f.emit({
      kind: "server-exit",
      workdir: "/a",
      code: 1,
      message: "synthetic disconnect",
    });
    assert.equal(await f.provide(), null);
    f.complete("two");
    assert.equal(await f.provide(), null);
    assert.equal(f.calls.length, 0);
  },
);
await scenario(
  "completion refresh is pinned to the pre-event observation",
  async (f) => {
    await f.provide({ running: true, revision: 4 });
    f.start();
    f.complete();
    assert.deepEqual(f.refreshes.at(-1), { composerId: "c", revision: 4 });
    assert.ok(await f.provide({ revision: 5 }));
    assert.equal(f.calls.length, 1);
  },
);
await scenario(
  "unavailable registered workspace never receives inference",
  async (f) => {
    f.api.listWorkspaces = async () => ({ workspaces: [] });
    f.start();
    f.complete();
    assert.equal(await f.provide(), null);
    assert.equal(f.calls.length, 0);
  },
);
await scenario(
  "completed observation cancellation cannot be undone by a late runtime",
  async (f) => {
    let settle;
    f.setResponse(() => new Promise((r) => (settle = r)));
    f.start();
    f.complete();
    const pending = f.provide();
    await new Promise((r) => setImmediate(r));
    f.emit({
      kind: "server-error",
      workdir: "/a",
      message: "synthetic disconnect",
    });
    assert.equal(f.calls[0].options.signal.aborted, true);
    settle({ suggestion: "STALE" });
    assert.equal(await pending, null);
    assert.equal(await f.provide({ revision: 9 }), null);
  },
);
await scenario(
  "newly registered workspace is verified without reloading plugin",
  async (f) => {
    f.api.listWorkspaces = async () => ({
      workspaces: [...workspaces, { id: "c", root: "/c", available: true }],
    });
    await f.provide({ workspaceId: "c", workspaceRoot: "/c" });
    f.start("new", "/c");
    f.complete("new", {}, "/c");
    assert.ok(await f.provide({ workspaceId: "c", workspaceRoot: "/c" }));
    assert.equal(f.calls[0].options.workspaceId, "c");
  },
);
await scenario(
  "re-registering a root cannot reattribute an old turn",
  async (f) => {
    f.start();
    f.complete();
    f.api.listWorkspaces = async () => ({
      workspaces: [{ id: "new", root: "/a", available: true }],
    });
    assert.equal(await f.provide({ workspaceId: "new" }), null);
    assert.equal(f.calls.length, 0);
  },
);
await scenario("late retired-root events cannot adopt a newer composer workspace ID", async (f) => {
  f.api.listWorkspaces = async () => ({ workspaces: [{ id: "new", root: "/a", available: true }] });
  await f.provide({ workspaceId: "new" });
  f.start("late-old-runtime");
  f.complete("late-old-runtime");
  assert.equal(await f.provide({ workspaceId: "new" }), null);
  assert.equal(f.calls.length, 0);
});
await scenario(
  "connection reset cancels every workspace and replay cannot rearm it",
  async (f) => {
    let settle;
    f.setResponse(() => new Promise((r) => (settle = r)));
    f.start();
    f.complete();
    const pending = f.provide();
    await new Promise((r) => setImmediate(r));
    f.emit({ kind: "connection-reset", workdir: "" });
    assert.equal(f.calls[0].options.signal.aborted, true);
    settle({ suggestion: "STALE" });
    assert.equal(await pending, null);
    for (const method of ["turn/started", "turn/completed"])
      f.emit({
        kind: "notification",
        replayed: true,
        workdir: "/a",
        message: {
          method,
          params: { thread_id: "same", turn: f.turn("replay") },
        },
      });
    assert.equal(await f.provide({ revision: 9 }), null);
    assert.equal(f.calls.length, 1);
  },
);
await scenario("null and malformed runtime results never retry", async (f) => {
  for (const [i, value] of [
    null,
    { suggestion: null },
    { suggestion: "a\nb" },
    { suggestion: "x".repeat(241) },
    { suggestion: "ok", extra: 1 },
  ].entries()) {
    f.setResponse(async () => value);
    f.start(String(i));
    f.complete(String(i));
    assert.equal(await f.provide(), null);
    assert.equal(await f.provide({ revision: 2 }), null);
  }
  assert.equal(f.calls.length, 5);
});
console.log(
  JSON.stringify(
    {
      scope: "actual desktop plugin, synthetic public API; no UI or inference",
      receipts,
    },
    null,
    2,
  ),
);
