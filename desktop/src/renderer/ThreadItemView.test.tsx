import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ThreadItem, Turn } from "../shared/protocol";
import { streamTextKey, streamTextStore } from "./StreamText";
import { ImagePreviewProvider } from "./ImagePreview";
import { ArtifactThreadContext } from "./ArtifactPreviewContext";
import { ThreadItemView } from "./ThreadItemView";
import { groupProjectEvents } from "./ProjectViews";
import { clearToasts, ToastViewport } from "./Toast";
import { ConversationControlsTestProvider, createConversationTestApplication } from "./ConversationControlsTestProvider";
import type { UICompositionV1 } from "../../../packages/plugin-sdk/src/ui";
import { WuuUIRoot } from "./ui/layers/UILayerHost";
import { navigateToResponseSelection } from "./ResponseSelection";
import { createComposerMessage } from "./ComposerMessages";
import type { ResponseSelection } from "../shared/protocol";
import { PluginHost, type PluginGenerationApi } from "./plugins/PluginHost";
import { createUIComposition } from "./plugins/ApplicationShell";
import { ApplicationController } from "./plugins/ApplicationController.test-support";
import { registerConversationSlots } from "./ConversationSlots";
import { applyAppearance, readAppearance } from "./AppearancePreferences";

beforeEach(() => {
  window.wuu = { ...window.wuu, reportPluginDesktopActivation: vi.fn().mockResolvedValue(undefined) };
});

let composition: UICompositionV1 = { Slot: ({ fallback }) => fallback };
let application: ReturnType<typeof createConversationTestApplication>;

beforeEach(() => {
  application = createConversationTestApplication();
});
let container: HTMLDivElement | undefined;
let root: Root | undefined;
const rangeBoundsDescriptor = Object.getOwnPropertyDescriptor(Range.prototype, "getBoundingClientRect");

function makeFinalAnswer(status: ThreadItem["status"]): ThreadItem {
  return {
    id: "final-1",
    type: "agent_message",
    status,
    terminal: true,
    text: "Final answer text.",
  };
}

function makeUserMessage(text: string, id = "user-1"): ThreadItem {
  return {
    id,
    type: "user_message",
    status: "completed",
    text,
  };
}

function render({
  item,
  owningThreadID,
  turnStatus,
  turnStartedAt,
  actionableAgentMessageID,
  latestAgentMessageID,
  streaming,
  onEditMessage,
  onForkMessage,
}: {
  item: ThreadItem;
  owningThreadID?: string;
  turnStatus: Turn["status"];
  turnStartedAt?: string;
  actionableAgentMessageID?: string;
  latestAgentMessageID?: string;
  streaming: boolean;
  onEditMessage?: (turnID: string, item: ThreadItem) => void;
  onForkMessage?: (turnID: string, itemID: string) => void;
}): void {
  if (!container) {
    container = document.createElement("div");
    container.dataset.threadId = "owner";
    document.body.appendChild(container);
    root = createRoot(container);
  }
  act(() => {
    root!.render(
      <ConversationControlsTestProvider composition={composition} service={application}>{<WuuUIRoot>
        <ImagePreviewProvider>
          <ArtifactThreadContext.Provider value={owningThreadID}>
            <ThreadItemView
              turnID="turn-1"
              turnStatus={turnStatus}
              turnStartedAt={turnStartedAt}
              item={item}
              streaming={streaming}
              actionableAgentMessageID={actionableAgentMessageID}
              latestAgentMessageID={latestAgentMessageID}
              onStreamFrame={() => {}}
              onEditMessage={onEditMessage}
              onForkMessage={onForkMessage}
            />
          </ArtifactThreadContext.Provider>
          <ToastViewport />
        </ImagePreviewProvider>
      </WuuUIRoot>}</ConversationControlsTestProvider>,
    );
  });
}

function actionBar(): HTMLElement {
  const node = container?.querySelector<HTMLElement>(".agent-message-actions");
  if (!node) {
    throw new Error("expected agent action bar");
  }
  return node;
}

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  container?.remove();
  streamTextStore.clearItem("turn-1", "final-1");
  clearToasts();
  composition = { Slot: ({ fallback }) => fallback };
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  if (rangeBoundsDescriptor) Object.defineProperty(Range.prototype, "getBoundingClientRect", rangeBoundsDescriptor);
  else Reflect.deleteProperty(Range.prototype, "getBoundingClientRect");
  root = undefined;
  container = undefined;
});

// The additive package runner supplies copies checked outside the checkout
// against the packed public SDK; ordinary renderer runs use the same sources.
function loadMessageAddon(name: string): (api: PluginGenerationApi) => void {
  const directory = process.env.WUU_MESSAGE_ACTION_ADDONS ?? resolve(process.cwd(), "../packages/plugin-sdk/test/fixtures");
  const source = readFileSync(resolve(directory, `${name}.js`), "utf8");
  return Function(source.replace("export function activate(api)", "return function activate(api)"))();
}

describe("ThreadItemView", () => {
  it("supplies the rendered message and live text directly to public contributions", () => {
    const snapshots: unknown[] = [];
    composition = { Slot: ({ name, context, fallback }) => {
      if (name.endsWith("/conversation.message")) snapshots.push(context?.snapshot);
      return fallback;
    } };
    const item = { ...makeFinalAnswer("in_progress"), source_id: "private-source" };
    render({ item, owningThreadID: "side-thread", turnStatus: "in_progress", streaming: true });
    expect(snapshots.at(-1)).toMatchObject({ id: item.id, kind: "assistant-message", text: item.text, status: "streaming" });
    expect(Object.isFrozen(snapshots.at(-1))).toBe(true);
    expect(JSON.stringify(snapshots.at(-1))).not.toContain("private-source");
    act(() => { streamTextStore.set(streamTextKey("turn-1", item.id, "text"), "Live answer"); });
    render({ item: { ...item }, owningThreadID: "side-thread", turnStatus: "in_progress", streaming: true });
    expect(snapshots.at(-1)).toMatchObject({ text: "Live answer" });
  });
  it.each(["user_message", "agent_message"] as const)("mounts public contributed actions for %s with the rendered item and owning address", async (type) => {
    const execute = vi.fn(); let enabled = true; let address: Readonly<Record<string, unknown>> | undefined;
    composition = { Slot: ({ name, context, fallback }) => name.endsWith("message.actions") && enabled
      ? <button aria-label="Inspect plugin message" onClick={() => { address = context; execute(); }}>Inspect</button> : fallback };
    const item = type === "user_message" ? makeUserMessage("") : makeFinalAnswer("completed");
    render({ item, owningThreadID: "public-thread", turnStatus: "completed", streaming: false });
    const action = container!.querySelector<HTMLButtonElement>('button[aria-label="Inspect plugin message"]')!;
    expect(action.closest(type === "user_message" ? ".user-message-actions" : ".agent-message-actions")).not.toBeNull();
    await act(async () => action.click());
    expect(execute).toHaveBeenCalledOnce();
    expect(address).toMatchObject({ threadId: "public-thread", turnId: "turn-1", itemId: item.id, snapshot: { id: item.id, text: item.text } });
    enabled = false;
    render({ item: { ...item }, owningThreadID: "public-thread", turnStatus: "completed", streaming: false });
    expect(container!.querySelector('button[aria-label="Inspect plugin message"]')).toBeNull();
  });

  // Independent addons must coexist, preserve local state when their peer changes,
  // and leave the shared action row empty only when neither contributes actions.
  it.each(["user_message", "agent_message"] as const)("composes command and raw-slot addons on %s without remounting peers", async type => {
    const invokeRuntime = vi.fn<NonNullable<ConstructorParameters<typeof PluginHost>[0]["invokeRuntime"]>>(async () => undefined);
    const host = new PluginHost({ react: React, application: new ApplicationController(), invokeRuntime });
    await host.activateGeneration({ pluginId: "wuu.default-conversation", generation: "one", register: registerConversationSlots });
    composition = createUIComposition(host);
    const commandAddon = loadMessageAddon("message-command-addon");
    const rawAddon = loadMessageAddon("message-slot-addon");
    const appearance = readAppearance();
    const activate = async (pluginId: string, register: (api: PluginGenerationApi) => void) => {
      await act(async () => { await host.activateGeneration({ pluginId, generation: "one", register }); });
    };
    const unload = async (pluginId: string) => { await act(async () => { host.unload(pluginId); }); };
    const item: ThreadItem = { id: "addon-message", type, status: "completed", text: "" };
    const view = (value = item) => render({ item: value, owningThreadID: "side-thread", turnStatus: "completed", streaming: false,
      actionableAgentMessageID: value.id, onEditMessage: vi.fn(), onForkMessage: vi.fn() });
    const row = () => container!.querySelector<HTMLElement>(type === "user_message" ? ".user-message-actions" : ".agent-message-actions")!;
    const raw = () => row().querySelector<HTMLButtonElement>('button[aria-label="Inspect raw message"]');
    const command = () => row().querySelector<HTMLButtonElement>('button[aria-label^="Inspect command message"]');
    try {
      view();
      expect(row().style.display).toBe("none");
      await activate("raw-addon", rawAddon);
      const originalRaw = raw()!;
      expect(row().style.display).toBe("");
      await act(async () => { originalRaw.click(); });
      expect(originalRaw.textContent).toBe("Raw 1");
      expect(invokeRuntime).toHaveBeenCalledWith(expect.objectContaining({ pluginId: "raw-addon", method: "inspect",
        input: expect.objectContaining({ threadId: "side-thread", turnId: "turn-1", itemId: item.id,
          snapshot: expect.objectContaining({ id: item.id, text: "" }) }) }));

      act(() => originalRaw.focus());
      await activate("command-addon", commandAddon);
      expect(document.activeElement).toBe(originalRaw);
      expect(raw()).toBe(originalRaw);
      expect(raw()?.textContent).toBe("Raw 1");
      expect(command()).not.toBeNull();
      expect(row().style.display).toBe("");
      for (const messageStyle of ["bubble", "im"] as const) {
        applyAppearance({ ...appearance, messageStyle });
        await act(async () => { command()!.click(); });
        expect(raw()).toBe(originalRaw);
        expect(invokeRuntime).toHaveBeenLastCalledWith(expect.objectContaining({ pluginId: "command-addon", method: "inspect",
          input: expect.objectContaining({ threadId: "side-thread", turnId: "turn-1",
            item: expect.objectContaining({ id: item.id, text: "" }) }) }));
      }
      expect(row().querySelectorAll("button")).toHaveLength(2);
      invokeRuntime.mockRejectedValueOnce(new Error("Synthetic command failure"));
      await act(async () => { command()!.click(); });
      expect(host.getGenerationDiagnostics("command-addon", "one").some(row => row.kind === "render")).toBe(true);
      expect(raw()).toBe(originalRaw);
      expect(document.activeElement).toBe(originalRaw);
      await act(async () => { command()!.click(); });
      expect(command()?.disabled).toBe(false);
      const originalCommandWithPeer = command();
      const logging = vi.spyOn(console, "error").mockImplementation(() => {});
      try {
        await activate("broken-addon", api => api.registerUIContribution({ id: "broken",
          target: "wuu.default-conversation/conversation.message.actions",
          render: () => { throw new Error("Synthetic message addon render failure"); },
        }));
        expect(host.getGenerationDiagnostics("broken-addon", "one").some(row => row.kind === "render")).toBe(true);
        expect(raw()).toBe(originalRaw);
        expect(command()).toBe(originalCommandWithPeer);
        expect(document.activeElement).toBe(originalRaw);
      } finally { await unload("broken-addon"); logging.mockRestore(); }

      // Native copy and edit/fork remain single actions alongside both addons.
      view({ ...item, text: "Native actions", terminal: true });
      expect(row().querySelectorAll("button")).toHaveLength(4);
      view();
      await unload("command-addon");
      expect(document.activeElement).toBe(originalRaw);
      expect(raw()).toBe(originalRaw);
      expect(row().style.display).toBe("");
      await activate("command-addon", commandAddon);
      expect(raw()).toBe(originalRaw);
      expect(raw()?.textContent).toBe("Raw 1");
      expect(invokeRuntime.mock.calls.filter(([request]) => request.method === "mounted")).toHaveLength(1);

      const originalCommand = command();
      await unload("raw-addon");
      expect(command()).toBe(originalCommand);
      expect(row().style.display).toBe("");
      view({ ...item, status: "in_progress" });
      expect(command()?.disabled).toBe(true);
      expect(row().style.display).toBe("");
      view({ ...item, id: "without-commands" });
      expect(command()).toBeNull();
      expect(row().style.display).toBe("none");
      view();
      expect(command()?.disabled).toBe(false);
      expect(row().style.display).toBe("");
      await unload("command-addon");
      expect(row().querySelectorAll("button")).toHaveLength(0);
      expect(row().style.display).toBe("none");
      await activate("command-addon", commandAddon);
      const restoredCommand = command();
      expect(restoredCommand).not.toBeNull();
      expect(row().style.display).toBe("");
      await activate("raw-addon", rawAddon);
      expect(raw()?.textContent).toBe("Raw 0");
      expect(command()).toBe(restoredCommand);
      await unload("command-addon");
      expect(raw()).not.toBeNull();
      expect(row().style.display).toBe("");
      await unload("raw-addon");
      expect(row().style.display).toBe("none");
    } finally {
      await unload("raw-addon");
      await unload("command-addon");
      await unload("wuu.default-conversation");
      applyAppearance(appearance);
    }
  });

  it("keeps selection actions within the viewport and falls below a top-edge passage", () => {
    vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 280, 40));
    Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => new DOMRect(window.innerWidth - 108, 12, 100, 24) });
    render({ item: makeFinalAnswer("completed"), turnStatus: "completed", streaming: false });
    const range = document.createRange(); range.selectNodeContents(container!.querySelector(".agent-text p")!);
    act(() => { window.getSelection()!.removeAllRanges(); window.getSelection()!.addRange(range); document.dispatchEvent(new Event("selectionchange")); });
    const actions = document.querySelector<HTMLElement>(".response-selection-toolbar")!;
    expect(Number.parseFloat(actions.style.left)).toBe(window.innerWidth - 288);
    expect(Number.parseFloat(actions.style.top)).toBe(44);
  });

  it.each(["completed", "in_progress"] as const)("captures rendered UTF-16 selection and rejects changed or hidden sources while %s", async (status) => {
    render({ item: { ...makeFinalAnswer(status), text: "A😀 **bold** tail" }, turnStatus: status, streaming: status === "in_progress" });
    container!.dataset.threadId = "owner";
    vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
    Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => new DOMRect(20, 80, 160, 20) });
    const textRoot = container!.querySelector<HTMLElement>(".agent-text")!;
    const bold = textRoot.querySelector("strong")!.firstChild!;
    const range = document.createRange();
    range.setStart(textRoot.querySelector("p")!.firstChild!, 1);
    range.setEnd(bold, 4);
    const received: ResponseSelection[] = [];
    vi.spyOn(window.getSelection()!, "toString").mockReturnValue("😀 bold\n");
    const listener = (event: Event) => received.push((event as CustomEvent<ResponseSelection>).detail);
    window.addEventListener("wuu:add-response-selection", listener);
    act(() => {
      window.getSelection()!.removeAllRanges();
      window.getSelection()!.addRange(range);
      document.dispatchEvent(new Event("selectionchange"));
      document.dispatchEvent(new MouseEvent("pointerup"));
    });
    const toolbar = document.querySelector<HTMLDivElement>(".response-selection-toolbar")!;
    expect(toolbar).not.toBeNull();
    expect(container!.contains(toolbar)).toBe(false);
    await act(async () => { container!.hidden = true; });
    expect(document.querySelector(".response-selection-toolbar")).toBeNull();
    await act(async () => {
      container!.hidden = false;
      document.dispatchEvent(new Event("selectionchange"));
    });
    act(() => document.querySelector<HTMLButtonElement>(".response-selection-toolbar button")!.click());
    window.removeEventListener("wuu:add-response-selection", listener);
    expect(received).toHaveLength(1);
    expect(received[0]).toMatchObject({ text: "😀 bold\n", source: { thread_id: "owner", turn_id: "turn-1", item_id: "final-1", start_offset: 1, end_offset: 8, range_text: "😀 bold" } });
    expect(window.getSelection()!.rangeCount).toBe(0);
    const scroll = vi.fn();
    textRoot.querySelector("p")!.scrollIntoView = scroll;
    expect(navigateToResponseSelection(received[0])).toBe(true);
    expect(scroll).toHaveBeenCalled();
    act(() => expect(navigateToResponseSelection({ ...received[0], source: { ...received[0].source, thread_id: "other" } })).toBe(false));
    await act(async () => {
      container!.hidden = true;
      expect(navigateToResponseSelection(received[0])).toBe(false);
      container!.hidden = false;
      bold.textContent = "changed";
      expect(navigateToResponseSelection(received[0])).toBe(false);
    });
  });

  it.each([
    ["in_progress", true],
    ["completed", true],
    ["completed", false],
  ] as const)("quotes a visible answer before turn completion (item %s, streaming %s)", (status, streaming) => {
    render({ item: makeFinalAnswer(status), turnStatus: "in_progress", streaming });
    container!.dataset.threadId = "owner";
    vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
    Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => new DOMRect(20, 80, 160, 20) });
    const range = document.createRange();
    range.selectNodeContents(container!.querySelector(".agent-text p")!);
    const received: ResponseSelection[] = [];
    const listener = (event: Event) => received.push((event as CustomEvent<ResponseSelection>).detail);
    window.addEventListener("wuu:add-response-selection", listener);
    act(() => {
      window.getSelection()!.removeAllRanges();
      window.getSelection()!.addRange(range);
      document.dispatchEvent(new Event("selectionchange"));
    });
    expect(document.querySelector(".response-selection-toolbar")).not.toBeNull();
    act(() => document.querySelector<HTMLButtonElement>(".response-selection-toolbar button")!.click());
    window.removeEventListener("wuu:add-response-selection", listener);
    expect(received).toHaveLength(1);
    expect(received[0]).toMatchObject({ text: "Final answer text.", source: { thread_id: "owner", turn_id: "turn-1", item_id: "final-1", start_offset: 0, end_offset: 18 } });
    expect(document.querySelector(".response-selection-toolbar")).toBeNull();
  });

  it.each(["completed", "in_progress"] as const)("adds an optional comment while %s and keeps it through turn completion", async (status) => {
    render({ item: makeFinalAnswer(status), turnStatus: status, streaming: status === "in_progress" });
    vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
    Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => new DOMRect(20, 80, 160, 20) });
    const range = document.createRange();
    range.selectNodeContents(container!.querySelector(".agent-text p")!);
    act(() => {
      window.getSelection()!.removeAllRanges();
      window.getSelection()!.addRange(range);
      document.dispatchEvent(new Event("selectionchange"));
    });
    const received: ResponseSelection[] = [];
    const listener = (event: Event) => received.push((event as CustomEvent<ResponseSelection>).detail);
    window.addEventListener("wuu:add-response-selection", listener);
    act(() => document.querySelector<HTMLButtonElement>(".response-selection-toolbar .selection-action-comment-toggle")!.click());
    let input = document.querySelector<HTMLTextAreaElement>(".response-selection-toolbar .selection-action-comment-input")!;
    expect(document.activeElement).toBe(input);
    expect(received).toHaveLength(0);
    expect(document.querySelector(".response-selection-toolbar")).not.toBeNull();
    act(() => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(document.querySelector(".response-selection-toolbar .selection-action-comment-input")).toBeNull();
    expect(document.activeElement).toBe(document.querySelector(".response-selection-toolbar .selection-action-comment-toggle"));
    act(() => document.querySelector<HTMLButtonElement>(".response-selection-toolbar .selection-action-comment-toggle")!.click());
    input = document.querySelector<HTMLTextAreaElement>(".response-selection-toolbar .selection-action-comment-input")!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "  Explain this choice  ");
      input.dispatchEvent(new Event("input", { bubbles: true }));
      document.dispatchEvent(new Event("selectionchange"));
    });
    expect(document.querySelector(".response-selection-toolbar .selection-action-comment-input")).toBe(input);
    await act(async () => {
      render({ item: { ...makeFinalAnswer("completed"), text: "Final answer text. More output." }, turnStatus: "completed", streaming: false });
    });
    expect(document.querySelector(".response-selection-toolbar .selection-action-comment-input")).toBe(input);
    expect(input.value).toBe("  Explain this choice  ");
    act(() => document.querySelector<HTMLButtonElement>(".response-selection-toolbar .selection-action-comment-submit")!.click());
    expect(received).toHaveLength(1);
    expect(received[0]).toMatchObject({ text: "Final answer text.", comment: "Explain this choice", source: { thread_id: "owner", start_offset: 0, end_offset: 18 } });
    expect(document.querySelector(".response-selection-toolbar")).toBeNull();
    window.removeEventListener("wuu:add-response-selection", listener);
  });

  it("does not quote assistant controls or editable embedded content", () => {
    render({ item: makeFinalAnswer("completed"), turnStatus: "completed", streaming: false });
    vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
    Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => new DOMRect(20, 80, 160, 20) });
    const textRoot = container!.querySelector(".agent-text")!;
    for (const html of ['<button>Copy code</button>', '<span contenteditable="true">Editable</span>']) {
      textRoot.innerHTML = html;
      const range = document.createRange();
      range.selectNodeContents(textRoot);
      act(() => {
        window.getSelection()!.removeAllRanges();
        window.getSelection()!.addRange(range);
        document.dispatchEvent(new Event("selectionchange"));
      });
      expect(document.querySelector(".response-selection-toolbar")).toBeNull();
    }
  });

  it("keeps a long response selection that crosses a code control without quoting the control", () => {
    render({ item: makeFinalAnswer("completed"), turnStatus: "completed", streaming: false });
    vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
    Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => new DOMRect(20, 80, 160, 20) });
    const root = container!.querySelector<HTMLElement>(".agent-text")!;
    root.innerHTML = "<p>Opening paragraph</p><button>Copy code</button><p>Closing paragraph</p>";
    const range = document.createRange();
    range.setStart(root.querySelector("p")!.firstChild!, 0);
    range.setEnd(root.querySelectorAll("p")[1].firstChild!, "Closing paragraph".length);
    const received: ResponseSelection[] = [];
    const listener = (event: Event) => received.push((event as CustomEvent<ResponseSelection>).detail);
    window.addEventListener("wuu:add-response-selection", listener);
    act(() => {
      window.getSelection()!.removeAllRanges();
      window.getSelection()!.addRange(range);
      document.dispatchEvent(new Event("selectionchange"));
    });
    expect(document.querySelector(".response-selection-toolbar")).not.toBeNull();
    act(() => root.dispatchEvent(new Event("scroll")));
    expect(document.querySelector(".response-selection-toolbar")).not.toBeNull();
    act(() => document.querySelector<HTMLButtonElement>(".response-selection-toolbar button")!.click());
    expect(received).toHaveLength(1);
    expect(received[0].text).toContain("Opening paragraph");
    expect(received[0].text).toContain("Closing paragraph");
    expect(received[0].text).not.toContain("Copy code");
    window.removeEventListener("wuu:add-response-selection", listener);
  });

  it("renders references readably and preserves reference metadata and raw prompt during inline edits", () => {
    const selection: ResponseSelection = { id: "quote", text: "Quoted passage", comment: "Explain this", source: { thread_id: "owner", turn_id: "turn-1", item_id: "final-1", start_offset: 0, end_offset: 14 } };
    const parts = [{ type: "pasted_text" as const, text: "notes\n", title: "Notes" }, { type: "text" as const, text: "  raw prompt  " }];
    const message = createComposerMessage("notes\n  raw prompt  ", [], [], parts, [selection])!;
    const item = { ...makeUserMessage(message.text), content_parts: message.contentParts };
    render({ item, turnStatus: "completed", streaming: false });
    expect(container!.textContent).toContain(selection.text);
    expect(container!.textContent).toContain(selection.comment);
    expect(container!.querySelector(".response-selection-source")).not.toBeNull();
    const submit = vi.fn();
    act(() => root!.render(<ConversationControlsTestProvider composition={composition} service={application}>{<ThreadItemView item={item} turnID="turn-1" turnStatus="completed" streaming={false} onStreamFrame={() => {}} editing onSubmitEditMessage={submit} />}</ConversationControlsTestProvider>));
    expect(container!.querySelector("textarea")!.value).toBe("  raw prompt  ");
    act(() => container!.querySelector<HTMLButtonElement>(".composer-send-button")!.click());
    expect(submit).toHaveBeenCalledWith("turn-1", item, message.text, [], [], message.contentParts);
  });

  it("renders public message slots with explicit identity and no business callback payload", () => {
    const contexts: Readonly<Record<string, unknown>>[] = [];
    composition = { Slot: ({ name, context, fallback }) => {
      if (name.endsWith("message.before") || name.endsWith("message.after")) { contexts.push(context!); return <span data-slot={name} />; }
      return fallback;
    } };
    render({ item: makeFinalAnswer("completed"), owningThreadID: "parent", turnStatus: "completed", streaming: false });
    expect(container!.querySelectorAll("[data-slot]")).toHaveLength(2);
    expect(contexts[0]).toEqual({ threadId: "parent", turnId: "turn-1", itemId: "final-1", snapshot: expect.objectContaining({ id: "final-1", kind: "assistant-message" }) });
    expect(Object.isFrozen(contexts[0])).toBe(true);
  });

  it("lets the optional public message contribution replace the default body", () => {
    composition = { Slot: ({ name, context, fallback }) => name.endsWith("/conversation.message") ? <section data-message>{String(context?.itemId)}</section> : fallback };
    render({ item: makeFinalAnswer("completed"), turnStatus: "completed", streaming: false });
    expect(container!.querySelector("[data-message]")?.textContent).toBe("final-1");
    expect(container!.querySelector(".agent-block")).toBeNull();
  });

  it("does not invent a thread identity for a standalone message", () => {
    let received: Readonly<Record<string, unknown>> | undefined;
    composition = { Slot: ({ name, context, fallback }) => { if (name.endsWith("/conversation.message")) { received = context; return null; } return fallback; } };
    render({ item: makeFinalAnswer("completed"), turnStatus: "completed", streaming: false });
    expect(received).toEqual({ threadId: undefined, turnId: "turn-1", itemId: "final-1", snapshot: expect.objectContaining({ id: "final-1", kind: "assistant-message" }) });
  });

  it("shows short user messages in full without a collapse control", () => {
    render({
      item: makeUserMessage("Short query."),
      turnStatus: "completed",
      streaming: false,
    });

    expect(container?.querySelector(".user-message-expand-toggle")).toBeNull();
    expect(container?.querySelector(".user-message-long-card")).toBeNull();
    expect(container?.textContent).toContain("Short query.");
  });

  it("renders pasted text beside a separate regular-text bubble", () => {
    const pastedText = "pasted line\n".repeat(80);
    render({
      item: {
        ...makeUserMessage(`${pastedText}Follow-up question.`),
        content_parts: [
          { type: "pasted_text", text: pastedText, title: "Pasted notes" },
          { type: "text", text: "Follow-up question." },
        ],
      },
      turnStatus: "completed",
      streaming: false,
    });

    const attachments = container?.querySelector(".user-message-attachments");
    const textBubble = container?.querySelector(".message.user-message");
    expect(attachments).not.toBeNull();
    expect(attachments?.nextElementSibling).toBe(textBubble);
    expect(textBubble?.querySelector(".user-message-pasted-text")).toBeNull();
    expect(container?.querySelector(".user-message-long-card")).toBeNull();
    expect(container?.querySelector(".user-message-pasted-text")?.textContent).toContain("Pasted notes");
    expect(container?.textContent).toContain("Follow-up question.");

    act(() => {
      container?.querySelector<HTMLButtonElement>(".user-message-pasted-text-toggle")?.click();
    });
    expect(container?.querySelector(".user-message-pasted-text-content")?.textContent).toBe(pastedText);
  });

  it.each([false, true])("collapses structured text across parts and preserves attachments (split=%s)", (split) => {
    const paragraphs = Array.from({ length: 16 }, (_, index) =>
      `段落 ${index + 1}：懂，你是说**折叠后的预览还是太长，占的空间太多**，不是想取消折叠。`,
    );
    const text = paragraphs.join("\n\n");
    render({
      item: {
        ...makeUserMessage(text),
        content_parts: [
          { type: "pasted_text", text: "separate attachment", title: "Notes" },
          ...(split ? paragraphs : [text]).map((part) => ({ type: "text" as const, text: part })),
        ],
      },
      turnStatus: "completed",
      streaming: false,
    });

    const bubble = container?.querySelector(".user-message");
    const toggle = bubble?.querySelector<HTMLButtonElement>(".user-message-expand-toggle");
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
    expect(bubble?.textContent).toContain("段落 1：");
    expect(bubble?.textContent).not.toContain("段落 16：");
    expect(container?.querySelector(".user-message-pasted-text")).not.toBeNull();
    expect(bubble?.textContent).not.toContain("separate attachment");

    act(() => toggle?.click());
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");
    expect(bubble?.textContent).toContain("段落 16：");
    expect(bubble?.querySelector("strong")).not.toBeNull();

    act(() => toggle?.click());
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
    expect(bubble?.textContent).not.toContain("段落 16：");
  });

  it("collapses long wrapped user messages without explicit line breaks", () => {
    const longSingleParagraph = "pasted query ".repeat(150);

    render({
      item: makeUserMessage(longSingleParagraph),
      turnStatus: "completed",
      streaming: false,
    });

    const bubble = container?.querySelector<HTMLElement>(".user-message");
    const rawQuery = container?.querySelector<HTMLElement>(
      ".user-message-raw-query",
    );
    const toggle = container?.querySelector<HTMLButtonElement>(
      ".user-message-expand-toggle",
    );
    expect(bubble?.classList.contains("user-message-long-card")).toBe(true);
    expect(bubble?.contains(toggle ?? null)).toBe(true);
    expect(rawQuery?.textContent?.endsWith("...")).toBe(true);
    expect((rawQuery?.textContent?.length ?? 0)).toBeLessThan(
      longSingleParagraph.length,
    );
  });

  it("shows long query previews as raw pasted text", () => {
    const markdownQuery = [
      "# Final plan",
      "",
      "## 0. Metadata",
      "",
      "- **Goal**: keep DefaultSystemPrompt() readable",
      "- **Reference**: default.md / gpt_5_1_prompt.md / prompt_with_apply_patch_instructions.md",
      "- **Scope**: internal/config/config.go / internal/runtime/session.go",
      "- **Step**: preserve the copied query shape",
      "- **Step**: avoid rendered markdown changing the preview",
      "- **Step**: keep a full rounded bubble",
      "- **Step**: wrap long paths and identifiers",
      "- **Step**: expose a clear show more control",
      "- **Step**: keep short queries unchanged",
      "- **Step**: keep copy and edit using the full original text",
      "- **Step**: each query starts collapsed",
      "- **Step**: expanded queries can collapse again",
    ].join("\n");

    render({
      item: makeUserMessage(markdownQuery),
      turnStatus: "completed",
      streaming: false,
    });

    const rawQuery = container?.querySelector<HTMLElement>(
      ".user-message-raw-query",
    );
    expect(rawQuery?.textContent).toContain("# Final plan");
    expect(rawQuery?.textContent).toContain("- **Goal**");
    expect(container?.querySelector(".rich-heading")).toBeNull();
  });

  it("collapses long user messages and toggles the full text", () => {
    const longText = Array.from(
      { length: 20 },
      (_, index) => `line ${index + 1}`,
    ).join("\n");

    render({
      item: makeUserMessage(longText),
      turnStatus: "completed",
      streaming: false,
    });

    const toggle = container?.querySelector<HTMLButtonElement>(
      ".user-message-expand-toggle",
    );
    const rawQuery = container?.querySelector<HTMLElement>(
      ".user-message-raw-query",
    );

    expect(rawQuery?.textContent).not.toContain("line 20");
    expect(rawQuery?.textContent?.endsWith("...")).toBe(true);
    expect(rawQuery?.textContent).toContain("line 5");
    expect(rawQuery?.textContent).not.toContain("line 6");
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");

    act(() => {
      toggle?.click();
    });

    expect(rawQuery?.textContent).toBe(longText);
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");

    act(() => {
      toggle?.click();
    });

    expect(rawQuery?.textContent).not.toContain("line 20");
  });

  it("defaults a different long user query back to collapsed", () => {
    const firstLongText = Array.from(
      { length: 20 },
      (_, index) => `first query line ${index + 1}`,
    ).join("\n");
    const secondLongText = Array.from(
      { length: 20 },
      (_, index) => `second query line ${index + 1}`,
    ).join("\n");

    render({
      item: makeUserMessage(firstLongText, "user-1"),
      turnStatus: "completed",
      streaming: false,
    });

    const firstToggle = container?.querySelector<HTMLButtonElement>(
      ".user-message-expand-toggle",
    );
    act(() => {
      firstToggle?.click();
    });

    expect(
      container?.querySelector(".user-message-raw-query")?.textContent,
    ).toContain("first query line 20");

    render({
      item: makeUserMessage(secondLongText, "user-2"),
      turnStatus: "completed",
      streaming: false,
    });

    const secondToggle = container?.querySelector<HTMLButtonElement>(
      ".user-message-expand-toggle",
    );
    expect(
      container?.querySelector(".user-message-raw-query")?.textContent,
    ).not.toContain("second query line 20");
    expect(secondToggle?.getAttribute("aria-expanded")).toBe("false");
  });

  it("enables copy and fork as soon as the final item completes", () => {
    const onForkMessage = vi.fn();
    render({
      item: makeFinalAnswer("in_progress"),
      turnStatus: "in_progress",
      streaming: true,
      onForkMessage,
    });

    expect(actionBar().getAttribute("aria-hidden")).toBe("true");
    expect(actionBar().querySelector("button, [tabindex]")).toBeNull();

    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "in_progress",
      latestAgentMessageID: "final-1",
      streaming: false,
      onForkMessage,
    });

    const finalizingActions = actionBar();
    expect(finalizingActions.getAttribute("aria-hidden")).toBeNull();
    expect(finalizingActions.dataset.wuuPlacement).toBe("persistent");
    expect(finalizingActions.querySelectorAll("button")).toHaveLength(2);
    const finalizingButtons = finalizingActions.querySelectorAll<HTMLButtonElement>("button");
    expect(finalizingButtons[0]?.disabled).toBe(false);
    expect(finalizingButtons[1]?.disabled).toBe(false);
    finalizingButtons[1]?.click();
    expect(onForkMessage).toHaveBeenCalledWith("turn-1", "final-1");

    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "completed",
      actionableAgentMessageID: "final-1",
      latestAgentMessageID: "final-1",
      streaming: false,
      onForkMessage,
    });

    const visibleActions = actionBar();
    expect(visibleActions.dataset.wuuPlacement).toBe("persistent");
    expect(visibleActions.querySelectorAll("button")).toHaveLength(2);
    expect(visibleActions.querySelectorAll<HTMLButtonElement>("button")[1]?.disabled).toBe(false);
    const block = container?.querySelector(".agent-block");
    expect(block?.classList.contains("agent-actions-persistent")).toBe(true);
    expect(block?.classList.contains("agent-actions-overlay")).toBe(false);
    expect(block?.classList.contains("agent-actions-enter")).toBe(true);
  });

  it.each([undefined, false])("does not reserve answer actions on live process text (%s)", (terminal) => {
    render({
      item: { ...makeFinalAnswer("in_progress"), terminal },
      turnStatus: "in_progress",
      latestAgentMessageID: "final-1",
      streaming: true,
    });
    expect((container?.querySelector(".agent-message-actions") as HTMLElement)?.style.display).toBe("none");
    expect(container?.querySelector(".agent-block-with-action-slot")).toBeNull();
  });

  it("keeps historical answer actions persistent", () => {
    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "completed",
      actionableAgentMessageID: "final-1",
      latestAgentMessageID: "final-99",
      streaming: false,
    });

    expect(actionBar().dataset.wuuPlacement).toBe("persistent");
    const block = container?.querySelector(".agent-block");
    expect(block?.classList.contains("agent-actions-overlay")).toBe(false);
    expect(block?.classList.contains("agent-actions-persistent")).toBe(true);
  });

  it("leaves host stream ownership intact after rendering the settled answer", async () => {
    const key = streamTextKey("turn-1", "final-1", "text");
    streamTextStore.set(key, "Final answer text.");

    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "completed",
      actionableAgentMessageID: "final-1",
      latestAgentMessageID: "final-1",
      streaming: false,
    });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });

    expect(container?.textContent).toContain("Final answer text.");
    expect(streamTextStore.has(key)).toBe(true);
  });

  it("groups host events with legacy project history without folding generic host messages", () => {
    const legacy = { ...makeUserMessage("Old result", "legacy"), origin: "plugin", cause: "project_result" };
    const current = { ...makeUserMessage("New result", "host"), origin: "host", cause: "project_message" };
    const generic = { ...makeUserMessage("Host message", "generic"), origin: "host", cause: "unrelated" };
    expect(groupProjectEvents([legacy, current, generic])).toEqual([[legacy, current], generic]);
    render({ item: current, turnStatus: "completed", streaming: false });
    expect(container?.querySelector(".project-event")).not.toBeNull();
    expect(container?.querySelector(".user-message")).toBeNull();
  });

  it("renders a plugin-generated query as a read-only user message", () => {
    const onEditMessage = vi.fn();
    render({
      item: {
        id: "plugin-query-1",
        type: "user_message",
        text: "子任务 太阳 已更新",
        read_only: true,
        origin: "plugin",
        origin_id: "subagent",
        cause: "subagent.completion",
        presentation_kind: "query_bubble",
      },
      turnStatus: "completed",
      streaming: false,
      onEditMessage,
    });

    expect(container?.querySelector(".user-message")?.textContent).toBe("子任务 太阳 已更新");
    const actions = container?.querySelectorAll<HTMLButtonElement>(".user-message-actions button");
    expect(actions).toHaveLength(1);
    expect(container?.querySelector<HTMLElement>(".user-message-actions")?.dataset.wuuPlacement).toBe("overlay");
    expect(onEditMessage).not.toHaveBeenCalled();
  });

  it.each(["host", "plugin"])("expands and copies a %s session message body and opens its source without exposing internal input", async (origin) => {
    const invoke = vi.spyOn(application, "invoke").mockResolvedValue(undefined);
    const copy = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: copy } });
    const body = "Coordination update.\n".repeat(100) + "End of update.";
    const onEditMessage = vi.fn();
    render({
      item: {
        id: "peer-message", type: "user_message", text: body,
        input_text: "Private delivery instructions", related_session_id: "source-session",
        name: "Source task", origin, origin_id: "alternative-messenger",
        presentation_kind: "session_message", read_only: true,
      },
      turnStatus: "completed", streaming: false, onEditMessage,
    });
    expect(container?.textContent).toContain("Source task");
    const source = container!.querySelector<HTMLButtonElement>(".session-message-source")!;
    vi.useFakeTimers();
    try {
      act(() => {
        source.dispatchEvent(new Event("pointerover", { bubbles: true }));
        vi.advanceTimersByTime(600);
      });
      expect(document.querySelector(".tooltip-layer")).toBeNull();
      act(() => {
        source.querySelector("span")!.dispatchEvent(new Event("pointerover", { bubbles: true }));
        vi.advanceTimersByTime(600);
      });
      expect(document.querySelector(".tooltip-layer")).toBeNull();
    } finally { vi.useRealTimers(); }

    expect(container?.textContent).not.toContain("Private delivery instructions");
    expect(container?.textContent).not.toContain("End of update.");
    const toggle = container!.querySelector<HTMLButtonElement>("button[aria-expanded]")!;
    act(() => toggle.click());
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(container?.textContent).toContain("End of update.");
    const actions = container!.querySelectorAll<HTMLButtonElement>(".user-message-actions button");
    expect(actions).toHaveLength(1);
    await act(async () => actions[0].click());
    expect(copy).toHaveBeenCalledWith(body);
    act(() => container!.querySelector<HTMLButtonElement>(".session-message-source")!.click());
    expect(invoke).toHaveBeenCalledExactlyOnceWith("conversation.open-split", { threadId: "source-session" });
    expect(onEditMessage).not.toHaveBeenCalled();
    act(() => toggle.click());
    expect(container?.textContent).not.toContain("End of update.");
  });

  it("shows the user message time before its copy action", () => {
    const sentAt = new Date(2026, 7, 31, 9, 7).toISOString();
    render({
      item: makeUserMessage("带发送时间的消息"),
      turnStatus: "completed",
      turnStartedAt: sentAt,
      streaming: false,
    });

    const actions = container?.querySelector(".user-message-actions");
    const time = actions?.querySelector<HTMLTimeElement>(".user-message-time");
    const copy = actions?.querySelector<HTMLButtonElement>("button");
    expect(time?.dateTime).toBe(sentAt);
    expect(time?.textContent).toBe("09:07");
    expect(time?.nextElementSibling).toBe(copy);
  });

  it("opens the related session in a split pane", () => {
    const invoke = vi.spyOn(application, "invoke").mockResolvedValue(undefined);
    render({
      item: {
        id: "plugin-query-1",
        type: "user_message",
        text: "子任务 太阳 已更新",
        input_text: "子任务 太阳（session 20260817-171746-edd1069c0780f11a）已完成。请检查并整合以下交接结果：\n\n三个命令均已成功执行",
        related_session_id: "20260817-171746-edd1069c0780f11a",
        read_only: true,
        origin: "plugin",
        origin_id: "subagent",
        cause: "subagent.completion",
        presentation_kind: "query_bubble",
      },
      turnStatus: "completed",
      streaming: false,
    });

    const actions = container?.querySelectorAll<HTMLButtonElement>(".user-message-actions button");
    expect(actions).toHaveLength(2);
    const openRelated = [...(actions ?? [])].find((button) => button.getAttribute("aria-label") === "打开关联会话");
    expect(openRelated).toBeDefined();

    act(() => openRelated!.click());
    expect(invoke).toHaveBeenCalledExactlyOnceWith("conversation.open-split", { threadId: "20260817-171746-edd1069c0780f11a" });
  });

});

it("renders an image-only assistant response using the attachment preview", () => {
  render({
    item: { ...makeFinalAnswer("completed"), text: "", images: [{ media_type: "image/png", data: "aW1hZ2U=" }] },
    turnStatus: "completed",
    streaming: false,
  });
  const image = container!.querySelector("img");
  expect(image?.getAttribute("src")).toBe("data:image/png;base64,aW1hZ2U=");
});
