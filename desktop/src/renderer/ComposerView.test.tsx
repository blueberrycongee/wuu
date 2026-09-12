import * as React from "react";
import { Suspense, act, createRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  Composer,
  SplitPaneComposer,
  permissionModeFromSummary,
  type CodexModelLoadState,
  type CodexRuntimeMenu,
  type ComposerVariant,
  type PermissionMode,
} from "./ComposerView";
import { ImagePreviewProvider } from "./ImagePreview";
import { translateCurrent } from "./i18n";
import { WorkbenchConnectionContext } from "./WorkbenchConnectionContext";
import { ComposerTokenGauge } from "./ComposerTokenGauge";
import { WORKSPACE_FILE_DRAG_MIME, type QueuedComposerMessage } from "./ComposerMessages";
import { hoverTooltipText, unhoverTooltip } from "./tooltipTestUtils";
import { PluginHost } from "./plugins/PluginHost";
import type {
  DesktopProject,
  InitializeResult,
  MessageContentPart,
  PermissionSummary,
  RuntimeContext,
  SkillSummary,
  SpeechRecognitionEvent,
  WuuDesktopApi,
} from "../shared/protocol";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  unhoverTooltip();
  act(() => {
    root?.unmount();
  });
  root = null;
  delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  container.remove();
  document.body
    .querySelectorAll(
      "[data-floating-menu-owner=\"composer-access\"], [data-floating-menu-owner=\"composer-attach\"], [data-floating-menu-owner=\"composer-focus\"], [data-floating-menu-owner=\"composer-plus\"], [data-floating-menu-owner=\"composer-slash\"], [data-floating-menu-owner=\"composer-token-gauge\"], [data-floating-menu-owner=\"codex-runtime\"]",
    )
    .forEach((element) => element.remove());
});

function initialized(permissions?: PermissionSummary): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "fake",
    model: "fake-model",
    workspace_root: "/tmp/project",
    permissions,
    providers: [
      {
        name: "fake",
        type: "openai-compatible",
        model: "fake-model",
      },
    ],
  };
}

function handoffInitialized(): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "work",
    model: "grok-4.6",
    workspace_root: "/tmp/project",
    providers: [
      {
        name: "work",
        type: "openai-compatible",
        model: "grok-4.6",
        models: [
          {
            id: "grok-4.6",
            display_name: "Grok 4.6",
            default_effort: "medium",
            supported_efforts: ["low", "medium", "high", "xhigh"],
          },
        ],
      },
      {
        name: "openai",
        type: "openai-compatible",
        model: "gpt-5.5",
        models: [{ id: "gpt-5.5", display_name: "GPT-5.5" }],
      },
    ],
  };
}

function renderComposer(props: {
  accessMenuOpen?: boolean;
  variant?: ComposerVariant;
  canSelectProject?: boolean;
  onToggleMenu?: () => void;
  mainConversation?: boolean;
  prompt?: string;
  running?: boolean;
  queuedMessages?: QueuedComposerMessage[];
  guideMessages?: QueuedComposerMessage[];
  status?: string;
  statusLiveProgress?: boolean;
  runtimeControlsDisabled?: boolean;
  connectionAvailable?: boolean;
  initialized?: InitializeResult;
  readOnly?: boolean;
  onInterrupt?: () => void;
  onSend?: (promptOverride?: string) => void;
  onSteer?: (promptOverride?: string) => void;
  onPasteAttachmentFiles?: (files: File[]) => void;
  onQueue?: (promptOverride?: string) => void;
  onStartNewThread?: () => void;
  onHandoffSession?: (input: { provider: string; model: string; effort?: string; intent: string }) => void;
  onOpenContextComposition?: () => void;
  onOpenSideThread?: (prompt?: string) => void;
  sideThreadDisabledReason?: string;
  onRemoveQueuedMessage?: (id: string) => void;
  onRemoveGuideMessage?: (id: string) => void;
  onGuideQueuedMessage?: (id: string) => void;
  onEditQueuedMessage?: (id: string) => void;
  onEditGuideMessage?: (id: string) => void;
  permissions?: PermissionSummary;
  activeContext?: RuntimeContext;
  setPrompt?: (value: string) => void;
  pluginHost?: PluginHost;
  onSelectPermissionMode?: (mode: PermissionMode) => void;
  tokensPerSecond?: number;
  tokenSpeedSampledAt?: number;
  tokenSpeedSource?: "real" | "estimated" | "none";
  activeProject?: DesktopProject;
  projects?: DesktopProject[];
}): { onSelectPermissionMode: (mode: PermissionMode) => void } {
  const codexModels: CodexModelLoadState = {
    loading: false,
    error: "",
    models: [],
  };
  const onSelectPermissionMode = props.onSelectPermissionMode ?? vi.fn();
  act(() => {
    root = createRoot(container);
    root.render(
      <ImagePreviewProvider>
        <Suspense fallback={<div data-testid="composer-suspended" />}>
          <WorkbenchConnectionContext.Provider value={props.connectionAvailable ?? true}>
          <Composer
            variant={props.variant}
            canSelectProject={props.canSelectProject}
            mainConversation={props.mainConversation}
            prompt={props.prompt ?? ""}
            setPrompt={props.setPrompt ?? (() => {})}
            pluginHost={props.pluginHost}
          files={[]}
          images={[]}
          queuedMessages={props.queuedMessages ?? []}
          guideMessages={props.guideMessages ?? []}
          running={props.running ?? false}
          runtimeControlsDisabled={props.runtimeControlsDisabled}
          status={props.status ?? "ready"}
          statusLiveProgress={props.statusLiveProgress}
          readOnly={props.readOnly ?? false}
          initialized={props.initialized ?? initialized(props.permissions)}
          projects={props.projects ?? []}
          activeContext={props.activeContext}
          activeProject={props.activeProject}
          sideThreadDisabledReason={props.sideThreadDisabledReason}
          codexModels={codexModels}
          codexRuntimeMenu={null}
          codexRuntimeRef={createRef<HTMLDivElement>()}
          menuOpen={false}
          accessMenuOpen={props.accessMenuOpen ?? false}
          branchMenuOpen={false}
          menuRef={createRef<HTMLDivElement>()}
          accessMenuRef={createRef<HTMLDivElement>()}
          projectFilter=""
          setProjectFilter={() => {}}
          onToggleMenu={props.onToggleMenu ?? (() => {})}
          onToggleAccessMenu={() => {}}
          onToggleCodexRuntimeMenu={() => {}}
          onSelectRuntimeModel={() => {}}
          onSelectRuntimeEffort={() => {}}
          onSelectPermissionMode={onSelectPermissionMode}
          onToggleBranchMenu={() => {}}
          onOpenSettings={() => {}}
          onOpenSkillsCatalog={() => {}}
          onSelectProject={() => {}}
          onSelectNoProject={() => {}}
          onSelectGitBranch={() => {}}
          onCreateProject={() => {}}
          onOpenProject={() => {}}
          onStartNewThread={props.onStartNewThread ?? (() => {})}
          onHandoffSession={props.onHandoffSession}
          onOpenSideThread={props.onOpenSideThread}
          onOpenWorkspaceTool={() => {}}
          onOpenContextComposition={props.onOpenContextComposition ?? (() => {})}
          onPasteAttachmentFiles={props.onPasteAttachmentFiles ?? (() => {})}
          onRemoveFile={() => {}}
          onRemoveImage={() => {}}
          onRemoveQueuedMessage={props.onRemoveQueuedMessage ?? (() => {})}
          onRemoveGuideMessage={props.onRemoveGuideMessage ?? (() => {})}
          onGuideQueuedMessage={props.onGuideQueuedMessage ?? (() => {})}
          onEditQueuedMessage={props.onEditQueuedMessage ?? (() => {})}
          onEditGuideMessage={props.onEditGuideMessage ?? (() => {})}
          onSend={props.onSend ?? (() => {})}
          onSteer={props.onSteer}
          onQueue={props.onQueue}
          onInterrupt={props.onInterrupt ?? (() => {})}
          tokensPerSecond={props.tokensPerSecond ?? 0}
          tokenSpeedSampledAt={props.tokenSpeedSampledAt}
            tokenSpeedSource={props.tokenSpeedSource}
          />
          </WorkbenchConnectionContext.Provider>
        </Suspense>
      </ImagePreviewProvider>,
    );
  });
  return { onSelectPermissionMode };
}

describe("mobile composer attachments", () => {
  let hostKind: string | undefined;
  let mediaDescriptor: PropertyDescriptor | undefined;
  let dialogDescriptors: (PropertyDescriptor | undefined)[];

  beforeEach(() => {
    hostKind = document.documentElement.dataset.hostKind;
    dialogDescriptors = ['showModal', 'close'].map(key => Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, key));
    Object.defineProperties(HTMLDialogElement.prototype, {
      showModal: { configurable: true, value(this: HTMLDialogElement) { this.open = true; } },
      close: { configurable: true, value(this: HTMLDialogElement) { this.open = false; } },
    });
    document.documentElement.dataset.hostKind = "web";
    const originalMatchMedia = window.matchMedia.bind(window);
    vi.spyOn(window, "matchMedia").mockImplementation((query) => ({
      ...originalMatchMedia(query), matches: query === "(pointer: coarse)",
    }));
    mediaDescriptor = Object.getOwnPropertyDescriptor(navigator, "mediaDevices");
    Object.defineProperty(navigator, "mediaDevices", { configurable: true, get: () => undefined });
  });

  afterEach(() => {
    ['showModal', 'close'].forEach((key, index) => {
      const descriptor = dialogDescriptors[index];
      if (descriptor) Object.defineProperty(HTMLDialogElement.prototype, key, descriptor);
      else Reflect.deleteProperty(HTMLDialogElement.prototype, key);
    });
    if (hostKind === undefined) delete document.documentElement.dataset.hostKind;
    else document.documentElement.dataset.hostKind = hostKind;
    vi.restoreAllMocks();
    if (mediaDescriptor) Object.defineProperty(navigator, "mediaDevices", mediaDescriptor);
    else Reflect.deleteProperty(navigator, "mediaDevices");
  });

  it.each(["main", "split"])("preserves native long-press editing in the %s composer", (variant) => {
    if (variant === "main") renderComposer({ prompt: "Keep this draft" });
    else renderStatefulSplitPaneComposer({ initialPrompt: "Keep this draft" });
    const textarea = container.querySelector("textarea")!;
    textarea.setSelectionRange(0, 4);
    const event = new MouseEvent("contextmenu", { bubbles: true, cancelable: true });
    act(() => { textarea.dispatchEvent(event); });
    expect(event.defaultPrevented).toBe(false);
    expect(document.querySelector('.composer-textarea-context-menu')).toBeNull();
    expect(textarea.value).toBe("Keep this draft");
    expect([textarea.selectionStart, textarea.selectionEnd]).toEqual([0, 4]);
  });

  function chooseSource(key: "composer.takePhoto" | "composer.choosePhotos" | "composer.chooseFiles"): void {
    act(() => { container.querySelector<HTMLButtonElement>(".composer-plus-button, .composer-attach-button")!.click(); });
    const choice = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'))
      .find((button) => button.querySelector(".composer-plus-menu-item-title")?.textContent === translateCurrent(key));
    expect(choice).toBeDefined();
    act(() => { choice!.click(); });
  }

  it.each(["main", "split"])("keeps the %s draft when choosing photos, files, or native capture", (variant) => {
    const onPasteAttachmentFiles = vi.fn();
    const onSend = vi.fn();
    if (variant === "main") renderComposer({ prompt: "Explain this", onPasteAttachmentFiles, onSend });
    else renderStatefulSplitPaneComposer({ initialPrompt: "Explain this", onPasteAttachmentFiles, onSend });
    const textarea = container.querySelector("textarea")!;
    const photo = new File(["photo"], "image.jpg", { type: "image/jpeg" });
    const pdf = new File(["pdf"], "document.pdf", { type: "application/pdf" });
    const inputs = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="file"]'));
    const photoInput = inputs.find((input) => input.accept === "image/*")!;
    const fileInput = inputs.find((input) => input.accept.includes("application/pdf"))!;
    const photoClick = vi.spyOn(photoInput, "click").mockImplementation(() => {});
    const fileClick = vi.spyOn(fileInput, "click").mockImplementation(() => {});
    chooseSource("composer.choosePhotos");
    expect(photoClick).toHaveBeenCalledOnce();
    Object.defineProperty(photoInput, "files", { configurable: true, value: [photo] });
    act(() => { photoInput.dispatchEvent(new Event("change", { bubbles: true })); });
    chooseSource("composer.chooseFiles");
    expect(fileClick).toHaveBeenCalledOnce();
    Object.defineProperty(fileInput, "files", { configurable: true, value: [pdf] });
    act(() => { fileInput.dispatchEvent(new Event("change", { bubbles: true })); });
    act(() => { textarea.focus(); });
    chooseSource("composer.takePhoto");
    expect(document.activeElement).not.toBe(textarea);
    const nativeInput = container.querySelector<HTMLInputElement>('input[capture="environment"]')!;
    expect(nativeInput).not.toBeNull();
    Object.defineProperty(nativeInput, "files", { configurable: true, value: [photo] });
    act(() => { nativeInput.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(container.querySelector(".composer-camera")).toBeNull();
    expect(onPasteAttachmentFiles.mock.calls).toEqual([[[photo]], [[pdf]], [[photo]]]);
    expect(textarea.value).toBe("Explain this");
    expect(document.activeElement).toBe(textarea);
    expect(onSend).not.toHaveBeenCalled();
    chooseSource("composer.takePhoto");
    act(() => { container.querySelector<HTMLButtonElement>(".composer-camera-close")!.click(); });
    expect(container.querySelector(".composer-camera")).toBeNull();
    expect(textarea.value).toBe("Explain this");
    expect(onPasteAttachmentFiles).toHaveBeenCalledTimes(3);
  });
});

function installSkillList(skills: SkillSummary[]): void {
  (globalThis as { wuu?: Partial<WuuDesktopApi> }).wuu = {
    listSkills: vi.fn().mockResolvedValue({ skills }),
  };
}

function renderSplitPaneComposer(props: {
  connectionAvailable?: boolean;
  prompt?: string;
  running?: boolean;
  status?: string;
  statusLiveProgress?: boolean;
  onSend?: () => void;
  onInterrupt?: () => void;
}): void {
  act(() => {
    root = createRoot(container);
    root.render(
      <ImagePreviewProvider>
        <WorkbenchConnectionContext.Provider value={props.connectionAvailable ?? true}>
        <SplitPaneComposer
          prompt={props.prompt ?? ""}
          setPrompt={() => {}}
          files={[]}
          images={[]}
          running={props.running ?? false}
          readOnly={false}
          status={props.status ?? "ready"}
          statusLiveProgress={props.statusLiveProgress}
          onPasteAttachmentFiles={() => {}}
          onRemoveFile={() => {}}
          onRemoveImage={() => {}}
          onSend={props.onSend ?? (() => {})}
          onInterrupt={props.onInterrupt ?? (() => {})}
        />
        </WorkbenchConnectionContext.Provider>
      </ImagePreviewProvider>,
    );
  });
}

function renderStatefulSplitPaneComposer(props: {
  initialPrompt?: string;
  readOnly?: boolean;
  requestedHandoffIntent?: string;
  onPasteAttachmentFiles?: (files: File[]) => void;
  onSend?: (prompt: string, contentParts?: MessageContentPart[]) => void;
}): void {
  function Harness(): JSX.Element {
    const [prompt, setPrompt] = useState(props.initialPrompt ?? "");
    return (
      <ImagePreviewProvider>
        <SplitPaneComposer
          prompt={prompt}
          setPrompt={setPrompt}
          files={[]}
          images={[]}
          running={false}
          readOnly={props.readOnly ?? false}
          status="ready"
          requestedHandoffIntent={props.requestedHandoffIntent}
          onPasteAttachmentFiles={props.onPasteAttachmentFiles ?? (() => {})}
          onRemoveFile={() => {}}
          onRemoveImage={() => {}}
          onSend={(promptOverride, contentParts) => {
            const nextPrompt = promptOverride ?? prompt;
            if (contentParts) props.onSend?.(nextPrompt, contentParts);
            else props.onSend?.(nextPrompt);
          }}
          onInterrupt={() => {}}
        />
      </ImagePreviewProvider>
    );
  }

  act(() => {
    root = createRoot(container);
    root.render(<Harness />);
  });
}

function mockDataTransfer(init: {
  types: string[];
  files?: File[];
  pathData?: string;
}): DataTransfer {
  const data = new Map<string, string>();
  if (init.pathData !== undefined) {
    data.set(WORKSPACE_FILE_DRAG_MIME, init.pathData);
  }
  return {
    types: init.types,
    files: init.files ?? [],
    getData: (type: string) => data.get(type) ?? "",
    setData: (type: string, value: string) => {
      data.set(type, value);
    },
    dropEffect: "none",
    effectAllowed: "all",
  } as unknown as DataTransfer;
}

function dispatchDrag(
  target: Element,
  type: "dragover" | "dragleave" | "drop",
  dataTransfer: DataTransfer,
  relatedTarget?: EventTarget | null,
): void {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(event, "dataTransfer", { value: dataTransfer });
  if (relatedTarget !== undefined) {
    Object.defineProperty(event, "relatedTarget", { value: relatedTarget });
  }
  target.dispatchEvent(event);
}

function renderStatefulComposer(props: {
  initialPrompt?: string;
  onSend?: (prompt: string, contentParts?: MessageContentPart[]) => void;
  onCommitPrompt?: (prompt: string, commit: (prompt: string) => void) => void;
  activeContext?: RuntimeContext;
  readOnly?: boolean;
  textOnly?: boolean;
  onPasteAttachmentFiles?: (files: File[]) => void;
  queryHistorySessionID?: string;
  initialized?: InitializeResult;
  running?: boolean;
  handoffDisabledReason?: string;
  requestedHandoffIntent?: string;
  onHandoffSession?: (input: { provider: string; model: string; effort?: string; intent: string }) => void;
}): { replacePrompt: (prompt: string) => void } {
  const codexModels: CodexModelLoadState = {
    loading: false,
    error: "",
    models: [],
  };

  let replacePrompt: ((prompt: string) => void) | undefined;
  function Harness(): JSX.Element {
    const [prompt, setPrompt] = useState(props.initialPrompt ?? "");
    const [promptRevision, setPromptRevision] = useState(0);
    const [codexRuntimeMenu, setCodexRuntimeMenu] = useState<CodexRuntimeMenu>(null);
    replacePrompt = (nextPrompt) => {
      setPrompt(nextPrompt);
      setPromptRevision((current) => current + 1);
    };
    const commitPrompt = (nextPrompt: string): void => {
      if (props.onCommitPrompt) {
        props.onCommitPrompt(nextPrompt, setPrompt);
        return;
      }
      setPrompt(nextPrompt);
    };
    return (
      <ImagePreviewProvider>
        <Composer
          prompt={prompt}
          promptRevision={promptRevision}
          setPrompt={commitPrompt}
          files={[]}
          images={[]}
          queuedMessages={[]}
          guideMessages={[]}
          running={props.running ?? false}
          status="ready"
          readOnly={props.readOnly ?? false}
          textOnly={props.textOnly}
          handoffDisabledReason={props.handoffDisabledReason}
          requestedHandoffIntent={props.requestedHandoffIntent}
          initialized={props.initialized ?? initialized()}
          projects={[]}
          activeContext={props.activeContext}
          codexModels={codexModels}
          codexRuntimeMenu={codexRuntimeMenu}
          codexRuntimeRef={createRef<HTMLDivElement>()}
          menuOpen={false}
          accessMenuOpen={false}
          branchMenuOpen={false}
          menuRef={createRef<HTMLDivElement>()}
          accessMenuRef={createRef<HTMLDivElement>()}
          projectFilter=""
          setProjectFilter={() => {}}
          onToggleMenu={() => {}}
          onToggleAccessMenu={() => {}}
          onToggleCodexRuntimeMenu={(menu) => {
            setCodexRuntimeMenu((current) => (current === menu ? null : menu));
          }}
          onSelectRuntimeModel={() => {}}
          onSelectRuntimeEffort={() => {}}
          onSelectPermissionMode={() => {}}
          onToggleBranchMenu={() => {}}
          onOpenSettings={() => {}}
          onOpenSkillsCatalog={() => {}}
          onSelectProject={() => {}}
          onSelectNoProject={() => {}}
          onSelectGitBranch={() => {}}
          onCreateProject={() => {}}
          onOpenProject={() => {}}
          onStartNewThread={() => {}}
          onHandoffSession={props.onHandoffSession}
          onOpenWorkspaceTool={() => {}}
          onPasteAttachmentFiles={props.onPasteAttachmentFiles ?? (() => {})}
          onRemoveFile={() => {}}
          onRemoveImage={() => {}}
          onRemoveQueuedMessage={() => {}}
          onRemoveGuideMessage={() => {}}
          onGuideQueuedMessage={() => {}}
          onEditQueuedMessage={() => {}}
          onEditGuideMessage={() => {}}
          onSend={(promptOverride, contentParts) => {
            const nextPrompt = promptOverride ?? prompt;
            if (contentParts) props.onSend?.(nextPrompt, contentParts);
            else props.onSend?.(nextPrompt);
          }}
          onInterrupt={() => {}}
          tokensPerSecond={0}
          queryHistorySessionID={props.queryHistorySessionID}
        />
      </ImagePreviewProvider>
    );
  }

  act(() => {
    root = createRoot(container);
    root.render(<Harness />);
  });
  return {
    replacePrompt: (prompt) => {
      act(() => replacePrompt?.(prompt));
    },
  };
}

async function nextAnimationFrame(): Promise<void> {
  await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve()));
}

function longPastedPrompt(title = "# 交接提示词(直接粘贴)", label = "交接"): string {
  return [
    title,
    "",
    `这是第一段${label}内容。`,
    `这是第二段${label}内容。`,
    `这是第三段${label}内容。`,
    `这是第四段${label}内容。`,
    `这是第五段${label}内容。`,
    `这是第六段${label}内容。`,
    `这是第七段${label}内容。`,
    `这是第八段${label}内容。`,
    `这是第九段${label}内容。`,
    `这是第十段${label}内容。`,
    `这是第十一段${label}内容。`,
    `这是第十二段${label}内容。`,
    `这是第十三段${label}内容。`,
    `这是第十四段${label}内容。`,
    `这是第十五段${label}内容。`,
  ].join("\n");
}

function pastePlainText(textarea: HTMLTextAreaElement, text: string): void {
  const event = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", {
    value: {
      items: [],
      getData: (type: string) => (type === "text/plain" ? text : ""),
    },
  });
  textarea.dispatchEvent(event);
}

function setTextareaValue(textarea: HTMLTextAreaElement, value: string): void {
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
  setter?.call(textarea, value);
  textarea.dispatchEvent(new Event("input", { bubbles: true }));
}

describe("Composer send control", () => {
  const voiceInputTest = import.meta.env.VITE_ENABLE_VOICE_INPUT === "true" ? it : it.skip;

  it("hides voice input and BYOK polish by default", () => {
    renderComposer({ prompt: "" });

    expect(container.querySelector(".composer-voice-input")).toBeNull();
  });

  it("keeps the draft editable but disables send when the workbench is disconnected", () => {
    const commitPrompt = vi.fn();
    const onSend = vi.fn();
    renderComposer({
      prompt: "离线草稿",
      setPrompt: commitPrompt,
      onSend,
      connectionAvailable: false,
    });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    const sendButton = container.querySelector<HTMLButtonElement>(".composer-send-button");

    expect(textarea).not.toBeNull();
    expect(textarea?.disabled).toBe(false);

    act(() => {
      setTextareaValue(textarea!, "离线草稿已编辑");
    });
    expect(commitPrompt).toHaveBeenCalledWith("离线草稿已编辑");
    expect(sendButton?.disabled).toBe(true);
  });

  it("keeps a disconnected split-pane draft without sending or interrupting", () => {
    const onSend = vi.fn();
    const onInterrupt = vi.fn();
    renderSplitPaneComposer({ connectionAvailable: false, running: true, prompt: "offline draft", onSend, onInterrupt });
    const textarea = container.querySelector("textarea")!;
    act(() => {
      textarea.focus();
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    });
    expect(textarea.disabled).toBe(false);
    expect(textarea.readOnly).toBe(false);
    expect(textarea.value).toBe("offline draft");
    expect(document.activeElement).toBe(textarea);
    expect(onSend).not.toHaveBeenCalled();
    expect(onInterrupt).not.toHaveBeenCalled();
  });

  it("does not clear the draft when Enter is pressed while disconnected", () => {
    const onSend = vi.fn();
    renderComposer({ prompt: "离线草稿", onSend, connectionAvailable: false });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea")!;

    act(() => {
      textarea.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Enter",
          bubbles: true,
          cancelable: true,
        }),
      );
    });

    expect(onSend).not.toHaveBeenCalled();
    expect(textarea.value).toBe("离线草稿");
  });

  it("does not interrupt while disconnected", () => {
    const onInterrupt = vi.fn();
    renderComposer({ running: true, onInterrupt, connectionAvailable: false });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea")!;

    act(() => {
      textarea.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Escape",
          bubbles: true,
          cancelable: true,
        }),
      );
    });

    expect(onInterrupt).not.toHaveBeenCalled();
  });

  it("does not dispatch the stop action while disconnected", () => {
    const onInterrupt = vi.fn();
    renderComposer({ running: true, onInterrupt, connectionAvailable: false });
    const stopButton = container.querySelector<HTMLButtonElement>(".composer-stop-button")!;

    expect(stopButton).not.toBeNull();
    act(() => stopButton.click());
    expect(onInterrupt).not.toHaveBeenCalled();
  });

  voiceInputTest("stops recording and steers the running turn with the final transcript", async () => {
    let speechHandler: ((event: SpeechRecognitionEvent) => void) | undefined;
    const stopSpeechRecognition = vi.fn().mockResolvedValue({ ok: true });
    const onSend = vi.fn();
    const onSteer = vi.fn();
    const voiceInputSettings = {
      polish_enabled: true,
      language: "system" as const,
    };
    (window as unknown as { wuu: WuuDesktopApi }).wuu = {
      platform: "darwin",
      initialVoiceInputSettings: voiceInputSettings,
      getVoiceInputSettings: vi.fn().mockResolvedValue({
        settings: voiceInputSettings,
        microphone_permission: "granted",
        speech_permission: "granted",
      }),
      updateVoiceInputSettings: vi.fn().mockResolvedValue(voiceInputSettings),
      onVoiceInputSettingsChange: vi.fn(() => () => undefined),
      startSpeechRecognition: vi.fn().mockResolvedValue({
        ok: true,
        session_id: "speech-1",
      }),
      stopSpeechRecognition,
      onSpeechRecognitionEvent: vi.fn((handler) => {
        speechHandler = handler;
        return () => undefined;
      }),
      polishText: vi.fn().mockResolvedValue({ text: "润色后直接发送" }),
    } as unknown as WuuDesktopApi;
    renderComposer({ running: true, onSend, onSteer });

    await act(async () => {
      container.querySelector<HTMLButtonElement>(".composer-voice-button")?.click();
    });
    act(() => {
      speechHandler?.({ type: "state", state: "listening" });
      speechHandler?.({ type: "result", text: "直接发送这段话", is_final: false });
    });

    const sendButton = container.querySelector<HTMLButtonElement>(
      ".composer-action-button",
    );
    expect(sendButton?.classList.contains("composer-send-button")).toBe(true);
    expect(sendButton?.getAttribute("aria-label")).toBe("发送引导");
    expect(sendButton?.disabled).toBe(false);
    await act(async () => {
      sendButton?.click();
    });

    expect(stopSpeechRecognition).toHaveBeenCalledOnce();
    expect(onSteer).toHaveBeenCalledOnce();
    expect(onSteer).toHaveBeenCalledWith("润色后直接发送");
    expect(onSend).not.toHaveBeenCalled();
  });

  it("sends on Enter when Chromium leaves a stale IME keyCode", () => {
    const onSend = vi.fn();
    renderComposer({ prompt: "发送这条消息", onSend });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    const event = new KeyboardEvent("keydown", {
      key: "Enter",
      keyCode: 229,
      bubbles: true,
      cancelable: true,
    });

    act(() => {
      textarea?.dispatchEvent(event);
    });

    expect(event.defaultPrevented).toBe(true);
    expect(onSend).toHaveBeenCalledTimes(1);
  });

  it("keeps typing responsive while the parent draft update is pending", () => {
    const commitPrompt = vi.fn();
    const onSend = vi.fn();
    renderComposer({ prompt: "", setPrompt: commitPrompt, onSend });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (!textarea) throw new Error("composer textarea not rendered");

    act(() => {
      setTextareaValue(textarea, "刚刚输入的内容");
    });

    // The parent in this harness deliberately never feeds the new value back.
    // Composer must still paint the keystroke immediately and submit that
    // local value rather than waiting for the expensive App render to commit.
    expect(textarea.value).toBe("刚刚输入的内容");
    act(() => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      }));
    });
    expect(commitPrompt).toHaveBeenCalledWith("刚刚输入的内容");
    expect(onSend).toHaveBeenCalledWith("刚刚输入的内容");
  });

  it("commits keystrokes while deferred composer chrome is suspended", async () => {
    const host = new PluginHost({ react: React });
    const onSend = vi.fn();
    let released = false;
    let release: (() => void) | undefined;
    const pending = new Promise<void>((resolve) => {
      release = () => {
        released = true;
        resolve();
      };
    });
    await host.activateGeneration({
      pluginId: "slow-composer-chrome",
      generation: "one",
      register(api) {
        api.registerSlot("composer.toolbar", {
          id: "slow-toolbar",
          render(context) {
            if (context.hasDraft && !released) {
              throw pending;
            }
            return api.react.createElement("span", null, "toolbar ready");
          },
        });
      },
    });
    renderComposer({ prompt: "", setPrompt: () => {}, pluginHost: host, onSend });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (!textarea) throw new Error("composer textarea not rendered");

    act(() => {
      setTextareaValue(textarea, "输入不应等待工具栏");
    });

    expect(textarea.value).toBe("输入不应等待工具栏");
    expect(container.querySelector('[data-testid="composer-suspended"]')).toBeNull();
    expect(container.querySelector(".composer-frame")).not.toBeNull();
    act(() => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      }));
    });
    expect(onSend).toHaveBeenCalledWith("输入不应等待工具栏");

    release?.();
    await act(async () => pending);
    expect(container.textContent).toContain("toolbar ready");
  });

  it("does not rerender composer plugin slots for every non-semantic character", async () => {
    const host = new PluginHost({ react: React });
    const renderToolbar = vi.fn(() => React.createElement("span", null, "toolbar"));
    await host.activateGeneration({
      pluginId: "composer-toolbar",
      generation: "one",
      register(api) {
        api.registerSlot("composer.toolbar", {
          id: "toolbar",
          render: renderToolbar,
        });
      },
    });
    renderComposer({ prompt: "", setPrompt: () => {}, pluginHost: host });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (!textarea) throw new Error("composer textarea not rendered");
    const initialRenderCount = renderToolbar.mock.calls.length;

    act(() => setTextareaValue(textarea, "a"));
    expect(renderToolbar).toHaveBeenCalledTimes(initialRenderCount + 1);

    act(() => setTextareaValue(textarea, "ab"));
    act(() => setTextareaValue(textarea, "abc"));
    expect(renderToolbar).toHaveBeenCalledTimes(initialRenderCount + 1);
  });

  it("accepts a programmatic clear when the committed text is already empty", () => {
    const harness = renderStatefulComposer({
      onCommitPrompt: () => {},
    });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (!textarea) throw new Error("composer textarea not rendered");

    act(() => {
      setTextareaValue(textarea, "local draft newer than App");
    });
    expect(textarea.value).toBe("local draft newer than App");

    harness.replacePrompt("");

    expect(textarea.value).toBe("");
  });

  it("keeps a deferred programmatic clear after the IME final input", () => {
    const harness = renderStatefulComposer({
      onCommitPrompt: () => {},
    });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (!textarea) throw new Error("composer textarea not rendered");

    act(() => {
      textarea.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
      setTextareaValue(textarea, "yi");
    });
    harness.replacePrompt("");
    expect(textarea.value).toBe("yi");

    act(() => {
      textarea.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true }));
      // Chromium may deliver the composition's final input after
      // compositionend. It must not overwrite the pending clear.
      setTextareaValue(textarea, "已");
    });

    expect(textarea.value).toBe("");
  });

  it("clears after send when Chromium omits compositionend", () => {
    const onSend = vi.fn();
    renderStatefulComposer({
      onSend,
      onCommitPrompt: () => {},
    });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (!textarea) throw new Error("composer textarea not rendered");

    act(() => {
      textarea.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
      setTextareaValue(textarea, "发送这条消息");
    });
    act(() => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      }));
    });

    expect(onSend).toHaveBeenCalledWith("发送这条消息");
    expect(textarea.value).toBe("");
  });

  it("submits one copy when duplicate gestures race the draft clear", () => {
    const onSteer = vi.fn();
    renderComposer({
      prompt: "send this once",
      running: true,
      onSteer,
    });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (!textarea) throw new Error("composer textarea not rendered");

    act(() => {
      for (let index = 0; index < 2; index += 1) {
        textarea.dispatchEvent(new KeyboardEvent("keydown", {
          key: "Enter",
          bubbles: true,
          cancelable: true,
        }));
      }
    });

    expect(onSteer).toHaveBeenCalledTimes(1);
    expect(onSteer).toHaveBeenCalledWith("send this once");
    expect(textarea.value).toBe("");

    act(() => {
      setTextareaValue(textarea, "a genuinely new draft");
      textarea.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      }));
    });

    expect(onSteer).toHaveBeenCalledTimes(2);
    expect(onSteer).toHaveBeenLastCalledWith("a genuinely new draft");
  });

  it("does not replace active IME text with a delayed parent draft echo", () => {
    const pendingCommits: Array<() => void> = [];
    renderStatefulComposer({
      onCommitPrompt: (prompt, commit) => {
        pendingCommits.push(() => commit(prompt));
      },
    });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (!textarea) throw new Error("composer textarea not rendered");

    act(() => {
      textarea.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
      setTextareaValue(textarea, "y");
      setTextareaValue(textarea, "yi");
    });
    expect(pendingCommits).toHaveLength(2);
    expect(textarea.value).toBe("yi");

    act(() => {
      pendingCommits[0]?.();
    });
    expect(textarea.value).toBe("yi");

    act(() => {
      setTextareaValue(textarea, "yib");
      textarea.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true }));
    });
    expect(textarea.value).toBe("yib");

    act(() => {
      pendingCommits[1]?.();
    });
    expect(textarea.value).toBe("yib");
  });

  it("does not send when Enter confirms an active IME composition", async () => {
    const onSend = vi.fn();
    renderStatefulComposer({ initialPrompt: "正在", onSend });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    const event = new KeyboardEvent("keydown", {
      key: "Enter",
      isComposing: true,
      bubbles: true,
      cancelable: true,
    });

    act(() => {
      textarea?.dispatchEvent(event);
    });

    expect(event.defaultPrevented).toBe(false);
    expect(onSend).not.toHaveBeenCalled();

    await act(async () => {
      setTextareaValue(textarea as HTMLTextAreaElement, "正在输入");
      textarea?.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true }));
    });

    expect(onSend).not.toHaveBeenCalled();
    expect(textarea?.value).toBe("正在输入");

    act(() => {
      textarea?.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      }));
    });

    expect(onSend).toHaveBeenCalledOnce();
    expect(onSend).toHaveBeenCalledWith("正在输入");
  });

  it("does not send from the split composer when Enter confirms IME composition", async () => {
    const onSend = vi.fn();
    renderSplitPaneComposer({ prompt: "继续修改", onSend });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");

    act(() => {
      textarea?.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Enter",
        isComposing: true,
        bubbles: true,
        cancelable: true,
      }));
    });
    expect(onSend).not.toHaveBeenCalled();

    await act(async () => {
      textarea?.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true }));
    });

    expect(onSend).not.toHaveBeenCalled();

    act(() => {
      textarea?.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      }));
    });

    expect(onSend).toHaveBeenCalledOnce();
  });

  it("steers with Enter and queues with Tab while a turn is running", () => {
    const onSend = vi.fn();
    const onSteer = vi.fn();
    const onQueue = vi.fn();
    renderComposer({
      prompt: "change direction",
      running: true,
      onSend,
      onSteer,
      onQueue,
    });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");

    act(() => {
      textarea?.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    expect(onSteer).toHaveBeenCalledTimes(1);
    expect(onQueue).not.toHaveBeenCalled();
    expect(onSend).not.toHaveBeenCalled();

    act(() => {
      if (textarea) {
        setTextareaValue(textarea, "queue next");
      }
    });
    act(() => {
      textarea?.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true }));
    });
    expect(onQueue).toHaveBeenCalledTimes(1);
    expect(onSend).not.toHaveBeenCalled();
  });

  it("keeps Tab available for focus navigation when there is no running draft", () => {
    const onQueue = vi.fn();
    renderComposer({ prompt: "", running: true, onQueue });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    const event = new KeyboardEvent("keydown", {
      key: "Tab",
      bubbles: true,
      cancelable: true,
    });

    act(() => {
      textarea?.dispatchEvent(event);
    });

    expect(event.defaultPrevented).toBe(false);
    expect(onQueue).not.toHaveBeenCalled();
  });

  it("keeps Tab and Shift+Tab available outside a running draft", () => {
    const onQueue = vi.fn();
    renderComposer({ prompt: "not running", running: false, onQueue });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    const tabEvent = new KeyboardEvent("keydown", {
      key: "Tab",
      bubbles: true,
      cancelable: true,
    });
    const reverseTabEvent = new KeyboardEvent("keydown", {
      key: "Tab",
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    });

    act(() => {
      textarea?.dispatchEvent(tabEvent);
      textarea?.dispatchEvent(reverseTabEvent);
    });

    expect(tabEvent.defaultPrevented).toBe(false);
    expect(reverseTabEvent.defaultPrevented).toBe(false);
    expect(onQueue).not.toHaveBeenCalled();
  });

  it("interrupts a running turn with Escape while preserving a draft", () => {
    const onInterrupt = vi.fn();
    const onSteer = vi.fn();
    renderComposer({
      prompt: "keep this follow-up",
      running: true,
      onInterrupt,
      onSteer,
    });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    const event = new KeyboardEvent("keydown", {
      key: "Escape",
      bubbles: true,
      cancelable: true,
    });

    act(() => {
      textarea?.dispatchEvent(event);
    });

    expect(event.defaultPrevented).toBe(true);
    expect(onInterrupt).toHaveBeenCalledTimes(1);
    expect(onSteer).not.toHaveBeenCalled();
    expect(textarea?.value).toBe("keep this follow-up");
  });

  it("leaves Escape alone when no turn is running", () => {
    const onInterrupt = vi.fn();
    renderComposer({ prompt: "draft", running: false, onInterrupt });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    const event = new KeyboardEvent("keydown", {
      key: "Escape",
      bubbles: true,
      cancelable: true,
    });

    act(() => {
      textarea?.dispatchEvent(event);
    });

    expect(event.defaultPrevented).toBe(false);
    expect(onInterrupt).not.toHaveBeenCalled();
  });

  it("dismisses the slash menu before Escape can interrupt a running turn", () => {
    const onInterrupt = vi.fn();
    renderComposer({ prompt: "/", running: true, onInterrupt });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    expect(document.body.querySelector('[data-floating-menu-owner="composer-slash"]')).not.toBeNull();

    act(() => {
      textarea?.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Escape",
        bubbles: true,
        cancelable: true,
      }));
    });

    expect(document.body.querySelector('[data-floating-menu-owner="composer-slash"]')).toBeNull();
    expect(onInterrupt).not.toHaveBeenCalled();
  });

  it("interrupts the focused split-pane composer with Escape", () => {
    const onInterrupt = vi.fn();
    renderSplitPaneComposer({ running: true, onInterrupt });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");

    act(() => {
      textarea?.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Escape",
        bubbles: true,
        cancelable: true,
      }));
    });

    expect(onInterrupt).toHaveBeenCalledTimes(1);
  });

  it("uses the same steer action as Enter when the send button is clicked", () => {
    const onInterrupt = vi.fn();
    const onSend = vi.fn();
    const onSteer = vi.fn();
    renderComposer({
      prompt: "change direction",
      running: true,
      onInterrupt,
      onSend,
      onSteer,
    });

    // A draft typed mid-turn must stay sendable, not be forced into a stop
    // control. The button should use the same steer action as Enter.
    expect(
      container.querySelectorAll(".composer-action-button.composer-stop-button"),
    ).toHaveLength(0);
    expect(container.querySelector("button[aria-label=\"暂停\"]")).toBeNull();

    const sendButton = container.querySelector<HTMLButtonElement>(
      "button[aria-label=\"发送引导\"]",
    );
    expect(sendButton).not.toBeNull();
    expect(sendButton?.disabled).toBe(false);

    act(() => {
      sendButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(onSteer).toHaveBeenCalledTimes(1);
    expect(onSend).not.toHaveBeenCalled();
    expect(onInterrupt).not.toHaveBeenCalled();
  });

  it("shows a stop button while running only when the input is empty", () => {
    const onInterrupt = vi.fn();
    const onSend = vi.fn();
    renderComposer({
      prompt: "",
      running: true,
      onInterrupt,
      onSend,
    });

    expect(
      container.querySelectorAll(".composer-action-button.composer-stop-button"),
    ).toHaveLength(1);
    expect(container.querySelector("button[aria-label=\"发送\"]")).toBeNull();
    expect(container.querySelector("button[aria-label=\"排队发送\"]")).toBeNull();

    const stopButton = container.querySelector<HTMLButtonElement>("button[aria-label=\"暂停\"]");
    expect(stopButton).not.toBeNull();

    act(() => {
      stopButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(onInterrupt).toHaveBeenCalledTimes(1);
    expect(onSend).not.toHaveBeenCalled();
  });


  it("returns focus to the textarea within the send click without scrolling", () => {
    const onSend = vi.fn();
    renderComposer({
      prompt: "send this",
      onSend,
    });

    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    const sendButton = container.querySelector<HTMLButtonElement>("button[aria-label=\"发送\"]");
    expect(textarea).not.toBeNull();
    expect(sendButton).not.toBeNull();

    const focus = vi.spyOn(textarea!, "focus");
    act(() => {
      sendButton?.focus();
      sendButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
      expect(document.activeElement).toBe(textarea);
    });

    expect(focus).toHaveBeenCalledWith({ preventScroll: true });
    expect(onSend).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(textarea);
  });

  it("returns focus to the split-pane textarea within the send click", () => {
    const onSend = vi.fn();
    renderSplitPaneComposer({
      prompt: "continue this branch",
      onSend,
    });

    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    const sendButton = container.querySelector<HTMLButtonElement>("button[aria-label=\"发送\"]");
    expect(textarea).not.toBeNull();
    expect(sendButton).not.toBeNull();

    act(() => {
      sendButton?.focus();
      sendButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
      expect(document.activeElement).toBe(textarea);
    });

    expect(onSend).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(textarea);
  });

  it.each(["main", "split"] as const)("keeps %s editing focus through pointer send without a delayed focus steal", async (pane) => {
    const onSend = vi.fn();
    if (pane === "main") {
      renderComposer({ prompt: "send this", onSend });
    } else {
      renderSplitPaneComposer({ prompt: "send this", onSend });
    }
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea")!;
    const sendButton = container.querySelector<HTMLButtonElement>(".composer-send-button")!;
    const blur = vi.fn();
    textarea.addEventListener("blur", blur);
    textarea.focus();
    const focus = vi.spyOn(textarea, "focus");

    act(() => {
      // jsdom does not perform the browser's default pointer focus transfer.
      const down = new MouseEvent("pointerdown", { bubbles: true, cancelable: true, button: 0 });
      if (sendButton.dispatchEvent(down)) sendButton.focus();
      sendButton.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    expect(onSend).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(textarea);
    expect(blur).not.toHaveBeenCalled();
    expect(focus).not.toHaveBeenCalled();

    const otherInput = document.createElement("input");
    container.appendChild(otherInput);
    otherInput.focus();
    await act(async () => { await nextAnimationFrame(); });
    expect(document.activeElement).toBe(otherInput);
  });

  it("leaves pointer focus native when not editing", () => {
    renderComposer({ prompt: "send this" });
    const sendButton = container.querySelector<HTMLButtonElement>(".composer-send-button")!;
    expect(sendButton.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, cancelable: true, button: 0 }))).toBe(true);
  });

  it("leaves pointer focus native when stopping", () => {
    renderComposer({ prompt: "", running: true });
    container.querySelector<HTMLTextAreaElement>("textarea")!.focus();
    const stopButton = container.querySelector<HTMLButtonElement>(".composer-stop-button")!;
    expect(stopButton.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, cancelable: true, button: 0 }))).toBe(true);
  });

  it("hides the transient sending status from the composer bar", () => {
    renderComposer({
      prompt: "queued follow-up",
      running: true,
      status: "正在发送请求",
    });

    expect(container.querySelector(".status-label")).toBeNull();
    expect(container.textContent).not.toContain("正在发送请求");
  });

  it("keeps non-transient composer status visible", () => {
    renderComposer({
      prompt: "retry later",
      status: "发送失败",
    });

    expect(container.querySelector(".status-label")?.textContent).toBe("发送失败");
  });

  it("renders reconnect status with the shared live progress chip", () => {
    renderComposer({
      prompt: "retry later",
      running: true,
      status: "消息流重连中 1/3",
      statusLiveProgress: true,
    });

    expect(container.querySelector(".status-label")?.textContent).toBe("消息流重连中 1/3");
    expect(container.querySelector(".status-label-text")?.classList.contains("live-progress-chip")).toBe(true);
  });

  it("renders static fallback status without the live progress chip", () => {
    renderComposer({
      prompt: "retry later",
      running: true,
      status: "WebSocket 不可用，已切到 HTTP",
      statusLiveProgress: false,
    });

    expect(container.querySelector(".status-label")?.textContent).toBe("WebSocket 不可用，已切到 HTTP");
    expect(container.querySelector(".status-label-text")?.classList.contains("live-progress-chip")).toBe(false);
  });

  it("hides the transient sending status from the split-pane composer bar", () => {
    renderSplitPaneComposer({
      prompt: "continue this branch",
      running: true,
      status: "正在发送请求",
    });

    expect(container.querySelector(".split-composer-status")).toBeNull();
    expect(container.textContent).not.toContain("正在发送请求");
  });

  it("renders split-pane reconnect status with the shared live progress chip", () => {
    renderSplitPaneComposer({
      prompt: "continue this branch",
      running: true,
      status: "HTTP 消息流重连中 2/3",
      statusLiveProgress: true,
    });

    expect(container.querySelector(".split-composer-status")?.textContent).toBe("HTTP 消息流重连中 2/3");
    expect(container.querySelector(".split-composer-status-text")?.classList.contains("live-progress-chip")).toBe(true);
  });

  it("hides session context chips in the dock composer", () => {
    renderComposer({
      variant: "dock",
      prompt: "follow up",
    });

    expect(container.querySelector(".composer-context-bar")).toBeNull();
    expect(container.querySelector(".context-project-button")).toBeNull();
  });

  it("keeps dock composer content inside the visual frame", () => {
    renderComposer({
      variant: "dock",
      prompt: "follow up",
    });

    const shell = container.querySelector(".composer-shell");
    const frame = container.querySelector(".composer-frame");
    const composer = container.querySelector(".composer");

    expect(shell).not.toBeNull();
    expect(frame).not.toBeNull();
    expect(composer).not.toBeNull();
    expect(shell?.contains(frame)).toBe(true);
    expect(frame?.contains(composer)).toBe(true);
    expect(frame?.querySelector(".composer-context-bar")).toBeNull();
  });

  it("opens the handoff picker outside the composer above its frame", () => {
    const measure = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      const top = this.dataset.wuuComponent === "composer-frame" ? 500 : 620;
      return { top, bottom: top + 100, left: 100, right: 700, width: 600, height: 100, x: 100, y: top, toJSON() {} };
    });
    renderStatefulComposer({
      initialPrompt: "/handoff",
      onHandoffSession: vi.fn(),
      initialized: handoffInitialized(),
      activeContext: { kind: "project", project_id: "project-1", cwd: "/tmp/project" },
    });

    expect(document.body.querySelector('[data-floating-menu-owner="composer-slash"]')).toBeNull();
    const picker = document.body.querySelector<HTMLElement>('[data-floating-menu-owner="composer-handoff"]')!;
    expect(container.contains(picker)).toBe(false);
    expect(window.innerHeight - Number.parseFloat(picker.style.bottom)).toBeLessThan(500);
    expect(container.querySelector<HTMLButtonElement>(".codex-runtime-trigger")?.textContent).toContain("Grok 4.6");
    measure.mockRestore();
    expect(container.querySelector<HTMLTextAreaElement>('[data-wuu-component="composer-input"]')?.value).toBe("/handoff");
    expect(container.querySelector<HTMLButtonElement>(".composer-send-button")?.disabled).toBe(true);
  });

  it("opens the picker when typing handoff and fills the chosen destination before sending", () => {
    const onHandoffSession = vi.fn();
    renderStatefulComposer({ initialized: handoffInitialized(), onHandoffSession });
    const textarea = container.querySelector<HTMLTextAreaElement>('[data-wuu-component="composer-input"]')!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, "/handoff");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const portal = document.body.querySelector('[data-floating-menu-owner="composer-handoff"]')!;
    expect(portal).not.toBeNull();
    act(() => portal.querySelector<HTMLButtonElement>(".runtime-panel-context button:last-child")!.click());
    act(() => Array.from(portal.querySelectorAll<HTMLButtonElement>(".runtime-provider-option")).find((button) => button.textContent?.trim() === "work")!.click());
    expect(portal.querySelector('input[type="search"]')).not.toBeNull();
    act(() => portal.querySelector<HTMLButtonElement>(".codex-model-item")!.click());
    expect(textarea.value).toBe("/handoff work:grok-4.6:medium -- ");
    expect(onHandoffSession).not.toHaveBeenCalled();
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, textarea.value + "Implement the plan");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    act(() => container.querySelector<HTMLButtonElement>(".composer-send-button")!.click());
    expect(onHandoffSession).toHaveBeenCalledWith({ provider: "work", model: "grok-4.6", effort: "medium", intent: "Implement the plan" });
  });

  it("opens a requested handoff draft in the composer textarea", () => {
    renderStatefulComposer({
      requestedHandoffIntent: "keep the verified fix",
      initialized: handoffInitialized(),
      activeContext: { kind: "project", project_id: "project-1", cwd: "/tmp/project" },
    });

    expect(container.querySelector<HTMLTextAreaElement>('[data-wuu-component="composer-input"]')?.value).toBe("/handoff -- keep the verified fix");
    expect(container.querySelector<HTMLButtonElement>(".composer-send-button")?.disabled).toBe(true);
  });

  it("selects a handoff destination through the existing model selector", () => {
    const onHandoffSession = vi.fn();
    renderStatefulComposer({
      initialPrompt: "/handoff openai/",
      initialized: handoffInitialized(),
      onHandoffSession,
      activeContext: { kind: "project", project_id: "project-1", cwd: "/tmp/project" },
    });

    expect(container.querySelector<HTMLButtonElement>(".composer-send-button")?.disabled).toBe(true);

    const menu = document.body.querySelector('[data-floating-menu-owner="composer-handoff"] .codex-model-menu');
    expect(menu).not.toBeNull();
    expect(menu?.classList.contains("is-models")).toBe(true);

    const modelItem = Array.from(
      document.body.querySelectorAll<HTMLButtonElement>('[data-floating-menu-owner="composer-handoff"] .codex-model-item'),
    ).find((button) => button.textContent?.includes("GPT-5.5"));
    expect(modelItem).toBeDefined();
    act(() => modelItem!.click());

    expect(onHandoffSession).not.toHaveBeenCalled();
    expect(container.querySelector<HTMLButtonElement>(".composer-send-button")?.disabled).toBe(false);
    act(() => container.querySelector<HTMLButtonElement>(".composer-send-button")!.click());
    expect(onHandoffSession).toHaveBeenCalledWith({
      provider: "openai",
      model: "gpt-5.5",
      effort: undefined,
      intent: "",
    });
    expect(document.body.querySelector('[data-floating-menu-owner="composer-handoff"]')).toBeNull();
  });

  it("preserves multiline handoff instructions while changing the target and effort", () => {
    const onHandoffSession = vi.fn();
    renderStatefulComposer({
      initialPrompt: "/handoff openai/gpt-5.5 -- check recovery",
      initialized: handoffInitialized(),
      onHandoffSession,
      activeContext: { kind: "project", project_id: "project-1", cwd: "/tmp/project" },
    });
    const intent = "Keep the changes.\nCheck recovery before committing. ";
    const textarea = container.querySelector<HTMLTextAreaElement>('[data-wuu-component="composer-input"]')!;
    expect(textarea.value).toBe("/handoff openai/gpt-5.5 -- check recovery");
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, `/handoff openai:gpt-5.5: -- ${intent}`);
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });

    const portal = document.body.querySelector('[data-floating-menu-owner="composer-handoff"]')!;
    act(() => portal.querySelector<HTMLButtonElement>(".runtime-panel-context button:last-child")!.click());
    act(() => Array.from(portal.querySelectorAll<HTMLButtonElement>(".runtime-provider-option")).find((button) => button.textContent?.trim() === "work")!.click());
    act(() => portal.querySelector<HTMLButtonElement>(".codex-model-item")!.click());

    const effort = portal.querySelector<HTMLInputElement>('input[type="range"]')!;
    expect(effort).not.toBeNull();
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(effort, effort.max);
      effort.dispatchEvent(new Event("input", { bubbles: true }));
      effort.dispatchEvent(new KeyboardEvent("keyup", { key: "End", bubbles: true }));
    });

    expect(textarea.value).toBe(`/handoff work:grok-4.6:xhigh -- ${intent}`);
    expect(onHandoffSession).not.toHaveBeenCalled();
    act(() => container.querySelector<HTMLButtonElement>(".composer-send-button")!.click());
    expect(onHandoffSession).toHaveBeenCalledWith({ provider: "work", model: "grok-4.6", effort: "xhigh", intent });
  });

  it("cancels a handoff without starting a session", () => {
    const onHandoffSession = vi.fn();
    renderStatefulComposer({
      initialPrompt: "/handoff work/grok-4.6",
      initialized: handoffInitialized(),
      onHandoffSession,
    });
    const textarea = container.querySelector<HTMLTextAreaElement>('[data-wuu-component="composer-input"]')!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, "");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(document.body.querySelector('[data-floating-menu-owner="composer-handoff"]')).toBeNull();
    expect(container.querySelector<HTMLTextAreaElement>('[data-wuu-component="composer-input"]')?.value).toBe("");
    expect(onHandoffSession).not.toHaveBeenCalled();
  });

  it.each([
    { initialPrompt: "/handoff" },
    { running: true },
    { readOnly: true },
    { handoffDisabledReason: "No history to hand off" },
  ])("blocks handoff submission when unavailable: %j", (overrides) => {
    const onHandoffSession = vi.fn();
    renderStatefulComposer({
      initialPrompt: "/handoff work/grok-4.6",
      initialized: handoffInitialized(),
      onHandoffSession,
      ...overrides,
    });
    const send = container.querySelector<HTMLButtonElement>(".composer-send-button")!;
    expect(send.disabled).toBe(true);
    act(() => send.click());
    expect(onHandoffSession).not.toHaveBeenCalled();
  });

  it("renders the slash command menu through the same floating panel as the plus menu", () => {
    renderComposer({
      variant: "dock",
      prompt: "/",
    });

    const shell = container.querySelector(".composer-shell");
    const frame = container.querySelector(".composer-frame");
    const slashLayer = document.body.querySelector<HTMLElement>(
      '[data-floating-menu-owner="composer-slash"]',
    );
    const slashMenu = slashLayer?.querySelector(".slash-command-menu");

    expect(shell).not.toBeNull();
    expect(frame).not.toBeNull();
    expect(slashMenu).not.toBeNull();
    expect(shell?.contains(slashMenu ?? null)).toBe(false);
    expect(frame?.contains(slashMenu ?? null)).toBe(false);
    expect(slashLayer?.classList.contains("floating-menu-layer")).toBe(true);
    expect(slashMenu?.classList.contains("composer-context-menu")).toBe(true);
    expect(slashMenu?.classList.contains("composer-plus-menu")).toBe(true);
  });

  it("places the caret after a command inserted from the plus menu", async () => {
    renderStatefulComposer({ activeContext: { kind: "no_project", cwd: "/tmp" } });

    const plusButton = container.querySelector<HTMLButtonElement>(".composer-plus-button");
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");

    act(() => plusButton?.click());
    const reviewItem = Array.from(
      document.body.querySelectorAll<HTMLButtonElement>(
        '[data-floating-menu-owner="composer-plus"] button',
      ),
    ).find((button) => button.textContent?.includes("审查当前更改"));
    act(() => reviewItem?.click());
    await act(async () => nextAnimationFrame());

    expect(textarea?.value).toBe("/review ");
    expect(textarea?.selectionStart).toBe(8);
    expect(textarea?.selectionEnd).toBe(8);
  });

  it("shares the plus menu width and available-height contract", () => {
    renderComposer({
      variant: "dock",
      prompt: "/",
    });

    const slashLayer = document.body.querySelector<HTMLElement>(
      '[data-floating-menu-owner="composer-slash"]',
    );
    expect(slashLayer).not.toBeNull();
  });

  it("shows the hero project selector above the input card", () => {
    renderComposer({
      variant: "hero",
    });

    expect(container.querySelector(".composer-context-bar")).toBeNull();
    expect(container.querySelector(".context-project-button")).toBeNull();
    expect(container.querySelector(".composer-workspace-bar > .hero-project-pill-anchor")).not.toBeNull();
    expect(container.querySelector(".hero-project-pill")).not.toBeNull();
    expect(container.querySelector(".hero-project-pill")?.textContent).toContain("选择项目");
    expect(container.querySelector<HTMLButtonElement>("button[aria-label=\"打开项目\"]")).toBeNull();
  });

  it("opens project selection from a new session's bottom composer", () => {
    const onToggleMenu = vi.fn();
    renderComposer({ variant: "dock", canSelectProject: true, onToggleMenu });

    const selector = container.querySelector<HTMLButtonElement>(".hero-project-pill");
    expect(selector).not.toBeNull();
    act(() => selector?.click());
    expect(onToggleMenu).toHaveBeenCalledOnce();
  });

  it("hides the cwd control once a project conversation is sent (dock variant)", () => {
    renderComposer({
      variant: "dock",
      prompt: "follow up",
      activeContext: { kind: "project", project_id: "project-1", cwd: "/repo/wuu" },
    });

    // A sent conversation locks its cwd (backend session.CWD is immutable),
    // so neither the hero pill nor the old dock "+" project control renders.
    expect(container.querySelector(".composer-project-control")).toBeNull();
    expect(
      container.querySelector<HTMLButtonElement>("button[aria-label=\"打开项目\"]"),
    ).toBeNull();
    // The composer itself still renders — only the workspace/cwd control is gone.
    expect(container.querySelector(".composer-plus-button")).not.toBeNull();
  });

  it("uses the active project name in the hero project selector", async () => {
    renderComposer({
      variant: "hero",
      activeContext: { kind: "project", project_id: "project-1", cwd: "/repo/wuu" },
      activeProject: {
        id: "project-1",
        name: "wuu",
        path: "/repo/wuu",
        created_at: "2026-06-26T00:00:00.000Z",
        updated_at: "2026-06-26T00:00:00.000Z",
      },
    });

    const selector = container.querySelector<HTMLButtonElement>(".hero-project-pill");
    expect(selector).not.toBeNull();
    expect(selector?.textContent).toContain("wuu");
    expect(selector?.getAttribute("title")).toBeNull();
    expect(await hoverTooltipText(selector)).toBe("/repo/wuu");
  });

  it("labels the hero pill 对话 for the no-project workspace", () => {
    renderComposer({
      variant: "hero",
      activeContext: { kind: "no_project", cwd: "/scratch/default" },
    });

    const selector = container.querySelector<HTMLButtonElement>(".hero-project-pill");
    expect(selector).not.toBeNull();
    // Matches the sidebar group and the session tab, which both read "对话".
    expect(selector?.textContent).toContain("对话");
  });

  it("separates auxiliary controls from the send action for responsive collapse", () => {
    renderComposer({
      variant: "dock",
      prompt: "follow up",
    });

    const leftGroup = container.querySelector(".composer-bar-left");
    const rightGroup = container.querySelector(".composer-bar-right");
    const sendButton = container.querySelector("button[aria-label=\"发送\"]");

    expect(leftGroup).not.toBeNull();
    expect(rightGroup).not.toBeNull();
    // A sent project/对话 conversation (dock variant) locks its cwd, so no
    // project/cwd control renders in the leading slot anymore.
    expect(leftGroup?.querySelector(".composer-project-control")).toBeNull();
    expect(leftGroup?.querySelector(".composer-plus-button")).not.toBeNull();
    expect(leftGroup?.querySelector(".permission-menu-anchor")).not.toBeNull();
    // The token-speed gauge is temporarily hidden from the toolbar; flip this
    // back to not.toBeNull() when it is restored.
    expect(rightGroup?.querySelector(".composer-token-gauge")).toBeNull();
    expect(rightGroup?.querySelector(".codex-runtime-anchor")).not.toBeNull();
    expect(rightGroup?.contains(sendButton)).toBe(true);
  });

  it("folds attachment and slash commands into the plus menu", () => {
    renderComposer({
      variant: "dock",
      prompt: "follow up",
      activeContext: { kind: "no_project", cwd: "/tmp" },
    });

    const plusButton = container.querySelector<HTMLButtonElement>(".composer-plus-button");
    expect(plusButton).not.toBeNull();
    expect(container.querySelector(".composer-attachment-button")).toBeNull();
    expect(container.querySelector(".composer-slash-button")).toBeNull();

    const frame = container.querySelector<HTMLElement>(".composer-frame");
    vi.spyOn(frame as HTMLElement, "getBoundingClientRect").mockReturnValue({
      bottom: 500,
      height: 100,
      left: 80,
      right: 720,
      top: 400,
      width: 640,
      x: 80,
      y: 400,
      toJSON: () => ({}),
    });

    const fileInput = container.querySelector<HTMLInputElement>(".composer-file-input");
    expect(fileInput).not.toBeNull();
    const inputClickSpy = vi.spyOn(fileInput as HTMLInputElement, "click").mockImplementation(() => {});

    act(() => {
      plusButton?.click();
    });
    expect(plusButton?.getAttribute("aria-expanded")).toBe("true");

    const menu = document.body.querySelector<HTMLElement>('[data-floating-menu-owner="composer-plus"]');
    expect(menu?.style.width).toBe("640px");
    expect(menu?.style.left).toBe("80px");
    expect(menu?.style.bottom).toBe(`${window.innerHeight - 400 + 4}px`);
    expect(menu?.style.getPropertyValue("--floating-menu-available-height")).toBe("388px");
    expect(menu?.querySelectorAll(".composer-plus-menu-section")).toHaveLength(2);
    expect(menu?.textContent).toContain("添加");
    expect(menu?.textContent).toContain("添加附件");
    expect(menu?.textContent).toContain("图片或 PDF");
    expect(menu?.textContent).toContain("命令");
    expect(menu?.textContent).toContain("审查当前更改");
    expect(menu?.textContent).not.toContain("打开斜杠命令");

    const attachmentItem = Array.from(menu?.querySelectorAll("button") ?? []).find(
      (button) => button.textContent?.includes("添加附件"),
    );
    act(() => {
      attachmentItem?.click();
    });
    expect(inputClickSpy).toHaveBeenCalledTimes(1);
    expect(document.body.querySelector('[data-floating-menu-owner="composer-plus"]')).toBeNull();
  });

  it("runs prompt commands directly from the plus menu", () => {
    const setPrompt = vi.fn();
    renderComposer({
      variant: "dock",
      prompt: "",
      setPrompt,
      activeContext: { kind: "no_project", cwd: "/tmp" },
    });

    act(() => {
      container.querySelector<HTMLButtonElement>(".composer-plus-button")?.click();
    });
    const reviewItem = Array.from(
      document.body.querySelectorAll('[data-floating-menu-owner="composer-plus"] button'),
    ).find((button) => button.textContent?.includes("审查当前更改"));
    act(() => {
      (reviewItem as HTMLButtonElement | undefined)?.click();
    });

    expect(setPrompt).toHaveBeenCalledWith("/review ");
    expect(document.body.querySelector('[data-floating-menu-owner="composer-plus"]')).toBeNull();
  });

  it("runs action commands directly from the plus menu", () => {
    const onStartNewThread = vi.fn();
    renderComposer({
      variant: "dock",
      prompt: "",
      onStartNewThread,
      activeContext: { kind: "no_project", cwd: "/tmp" },
    });

    act(() => {
      container.querySelector<HTMLButtonElement>(".composer-plus-button")?.click();
    });
    const newThreadItem = Array.from(
      document.body.querySelectorAll('[data-floating-menu-owner="composer-plus"] button'),
    ).find((button) => button.textContent?.includes("新建对话"));
    act(() => {
      (newThreadItem as HTMLButtonElement | undefined)?.click();
    });

    expect(onStartNewThread).toHaveBeenCalledTimes(1);
    expect(document.body.querySelector('[data-floating-menu-owner="composer-plus"]')).toBeNull();
  });

  it("keeps runtime controls separate from composer send state", () => {
    renderComposer({
      variant: "dock",
      prompt: "follow up",
      running: false,
      runtimeControlsDisabled: true,
    });

    const runtimeButton = container.querySelector<HTMLButtonElement>(".codex-runtime-trigger");
    const sendButton = container.querySelector<HTMLButtonElement>("button[aria-label=\"发送\"]");

    expect(runtimeButton?.disabled).toBe(true);
    expect(sendButton?.disabled).toBe(false);
  });

  it("renders the conversation runtime selected by the app", () => {
    const runtime = initialized();
    runtime.provider = "provider-a";
    runtime.model = "model-a";
    runtime.providers = [
      { name: "provider-a", type: "openai-compatible", model: "model-a" },
      { name: "provider-b", type: "openai-compatible", model: "model-b" }
    ];

    renderComposer({
      variant: "dock",
      initialized: runtime,
      runtimeControlsDisabled: true
    });

    const runtimeButton = container.querySelector<HTMLButtonElement>(".codex-runtime-trigger");
    expect(runtimeButton?.textContent).toContain("model-a");
    expect(runtimeButton?.disabled).toBe(true);
  });

  it("renders the next-turn session runtime when controls unlock", () => {
    const runtime = initialized();
    runtime.provider = "provider-b";
    runtime.model = "model-b";
    runtime.providers = [
      { name: "provider-a", type: "openai-compatible", model: "model-a" },
      { name: "provider-b", type: "openai-compatible", model: "model-b" }
    ];

    renderComposer({
      variant: "dock",
      initialized: runtime,
      runtimeControlsDisabled: false
    });

    const runtimeButton = container.querySelector<HTMLButtonElement>(".codex-runtime-trigger");
    expect(runtimeButton?.textContent).toContain("model-b");
    expect(runtimeButton?.disabled).toBe(false);
  });

  it("inserts a selected skill slash command into the composer", async () => {
    const setPrompt = vi.fn();
    installSkillList([
      {
        name: "slides",
        description: "Create slide decks",
        source: "bundled",
        user_invocable: true,
        disable_model_invoke: false,
      },
    ]);
    renderComposer({
      prompt: "/sli",
      setPrompt,
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
    });

    await act(async () => {
      await Promise.resolve();
    });

    const skillButton = document.body.querySelector<HTMLButtonElement>(
      '.slash-command-item[data-command-name="slides"]',
    );
    expect(skillButton).not.toBeUndefined();

    act(() => {
      skillButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(setPrompt).toHaveBeenCalledWith("/slides ");
  });

  it("shows both the command name and the description on a skill row", async () => {
    installSkillList([
      {
        name: "slides",
        description: "Create slide decks",
        source: "bundled",
        user_invocable: true,
        disable_model_invoke: false,
      },
    ]);
    renderComposer({
      prompt: "/sli",
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
    });

    await act(async () => {
      await Promise.resolve();
    });

    const skillRow = document.body.querySelector<HTMLButtonElement>(
      '.slash-command-item[data-command-name="slides"]',
    );

    // Without the command name the row cannot be told apart from any other
    // skill that happens to share a description prefix.
    expect(skillRow?.querySelector(".slash-command-name")?.textContent).toBe("/slides");
    expect(skillRow?.querySelector(".slash-command-summary")?.textContent).toBe(
      "Create slide decks",
    );
  });

  it("leads every built-in row with the command the user types", () => {
    renderComposer({
      prompt: "/",
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
    });

    const reviewRow = document.body.querySelector<HTMLButtonElement>(
      '.slash-command-item[data-command-name="review"]',
    );

    expect(reviewRow?.querySelector(".slash-command-name")?.textContent).toBe("/review");
    expect(reviewRow?.querySelector(".slash-command-summary")?.textContent).toBe("审查当前更改");
    expect(reviewRow?.querySelector(".composer-plus-menu-item-title")).not.toBeNull();
    expect(reviewRow?.querySelector(".composer-plus-menu-item-desc")).not.toBeNull();
  });

  it("uses the plus menu row structure for slash commands", () => {
    renderComposer({ prompt: "/" });

    const reviewRow = document.body.querySelector<HTMLButtonElement>(
      '.slash-command-item[data-command-name="review"]',
    );
    expect(reviewRow?.children).toHaveLength(3);
    expect(reviewRow?.children[0]?.tagName).toBe("svg");
    expect(reviewRow?.children[1]?.classList.contains("composer-plus-menu-item-title")).toBe(true);
    expect(reviewRow?.children[2]?.classList.contains("composer-plus-menu-item-desc")).toBe(true);
    expect(reviewRow?.querySelector(".slash-command-label")).toBeNull();
  });

  it("sends an exact slash command with arguments on Enter", () => {
    const onSend = vi.fn();
    renderComposer({
      prompt: "/debug 登录失败",
      onSend,
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
    });

    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    expect(textarea).not.toBeNull();

    act(() => {
      textarea?.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Enter",
          bubbles: true,
          cancelable: true,
        }),
      );
    });

    expect(onSend).toHaveBeenCalledTimes(1);
  });

  it("runs /context as a local action when the send button is clicked", () => {
    const onSend = vi.fn();
    const onOpenContextComposition = vi.fn();
    const setPrompt = vi.fn();
    renderComposer({
      prompt: "/context",
      setPrompt,
      onSend,
      onOpenContextComposition,
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
    });

    const sendButton = container.querySelector<HTMLButtonElement>(
      'button[aria-label="发送"]',
    );
    expect(sendButton).not.toBeNull();

    act(() => {
      sendButton?.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });

    expect(onOpenContextComposition).toHaveBeenCalledTimes(1);
    expect(onSend).not.toHaveBeenCalled();
    expect(setPrompt).toHaveBeenCalledWith("");
  });

  it("runs /new as a new-thread action", () => {
    const setPrompt = vi.fn();
    const onStartNewThread = vi.fn();
    renderComposer({
      prompt: "/new",
      setPrompt,
      onStartNewThread,
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
    });

    const sendButton = container.querySelector<HTMLButtonElement>(
      'button[aria-label="发送"]',
    );
    act(() => sendButton?.click());

    expect(onStartNewThread).toHaveBeenCalledTimes(1);
    expect(setPrompt).toHaveBeenCalledWith("");
  });

  it("marks only an explicitly identified main-conversation composer", () => {
    renderComposer({ variant: "hero", mainConversation: true });

    expect(
      container.querySelector("[data-main-conversation-composer=\"hero\"]"),
    ).not.toBeNull();
  });


  it("keeps /side disabled on a draft without a persisted thread", () => {
    const setPrompt = vi.fn();
    const onOpenSideThread = vi.fn();
    renderComposer({
      prompt: "/side",
      setPrompt,
      onOpenSideThread,
      sideThreadDisabledReason: "先发送一条消息",
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
    });

    const side = document.body.querySelector<HTMLButtonElement>(
      '.slash-command-item[data-command-name="side"]',
    );
    expect(side?.disabled).toBe(true);
    expect(side?.textContent).toContain("先发送一条消息");

    act(() => side?.click());
    expect(onOpenSideThread).not.toHaveBeenCalled();
    expect(setPrompt).not.toHaveBeenCalled();
  });

  it("runs /side as an open action for a persisted thread", () => {
    const setPrompt = vi.fn();
    const onOpenSideThread = vi.fn();
    renderComposer({
      prompt: "/side",
      setPrompt,
      onOpenSideThread,
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
    });

    const sendButton = container.querySelector<HTMLButtonElement>(
      'button[aria-label="发送"]',
    );
    act(() => sendButton?.click());

    expect(onOpenSideThread).toHaveBeenCalledTimes(1);
    expect(onOpenSideThread).toHaveBeenCalledWith(undefined);
    expect(setPrompt).toHaveBeenCalledWith("");
  });

  it("passes /side args through as the side-thread prompt", () => {
    const setPrompt = vi.fn();
    const onOpenSideThread = vi.fn();
    renderComposer({
      prompt: "/side 现在进度怎么样",
      setPrompt,
      onOpenSideThread,
      activeContext: { kind: "project", project_id: "repo", cwd: "/repo" },
    });

    const sendButton = container.querySelector<HTMLButtonElement>(
      'button[aria-label="发送"]',
    );
    act(() => sendButton?.click());

    expect(onOpenSideThread).toHaveBeenCalledTimes(1);
    expect(onOpenSideThread).toHaveBeenCalledWith("现在进度怎么样");
    expect(setPrompt).toHaveBeenCalledWith("");
  });
});

describe("Composer long text folding", () => {
  it("folds a long paste while sending the original text plus follow-up", () => {
    const longText = longPastedPrompt();
    const onSend = vi.fn();
    renderStatefulComposer({ onSend });

    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    expect(textarea).not.toBeNull();

    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, longText);
    });

    expect(container.querySelector(".composer-collapsed-prompt-card")).not.toBeNull();
    expect(container.querySelector(".composer-collapsed-prompt-title")?.textContent).toBe("# 交接提示词(直接粘贴)");
    expect((textarea as HTMLTextAreaElement).value).toBe("");
    expect((textarea as HTMLTextAreaElement).placeholder).toBe("要求后续变更");

    act(() => {
      setTextareaValue(textarea as HTMLTextAreaElement, "\n要求后续变更");
    });

    const sendButton = container.querySelector<HTMLButtonElement>("button[aria-label=\"发送\"]");
    act(() => {
      sendButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(onSend).toHaveBeenCalledWith(`${longText}\n要求后续变更`, [
      { type: "pasted_text", text: longText },
      { type: "text", text: "\n要求后续变更" },
    ]);
  });

  it("reveals a folded long paste back into the textarea", () => {
    const longText = longPastedPrompt();
    renderStatefulComposer({});

    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    expect(textarea).not.toBeNull();

    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, longText);
    });

    const revealButton = container.querySelector<HTMLButtonElement>(".composer-collapsed-prompt-main");
    expect(revealButton).not.toBeNull();

    act(() => {
      revealButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(container.querySelector(".composer-collapsed-prompt-card")).toBeNull();
    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(longText);
  });

  it("reveals folded rows into the textarea in click order", () => {
    const firstLongText = longPastedPrompt("# A 交接提示词", "A");
    const secondLongText = longPastedPrompt("# B 交接提示词", "B");
    const thirdLongText = longPastedPrompt("# C 交接提示词", "C");
    const onSend = vi.fn();
    renderStatefulComposer({ onSend });

    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    expect(textarea).not.toBeNull();

    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, firstLongText);
    });
    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, secondLongText);
    });
    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, thirdLongText);
    });

    expect(container.querySelectorAll(".composer-collapsed-prompt-card")).toHaveLength(3);

    act(() => {
      foldedPromptButton("# B 交接提示词")?.click();
    });

    expect(container.querySelectorAll(".composer-collapsed-prompt-card")).toHaveLength(2);
    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(secondLongText);

    act(() => {
      foldedPromptButton("# A 交接提示词")?.click();
    });

    expect(container.querySelectorAll(".composer-collapsed-prompt-card")).toHaveLength(1);
    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(`${secondLongText}${firstLongText}`);

    act(() => {
      foldedPromptButton("# C 交接提示词")?.click();
    });

    expect(container.querySelector(".composer-collapsed-prompt-card")).toBeNull();
    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(
      `${secondLongText}${firstLongText}${thirdLongText}`,
    );

    const sendButton = container.querySelector<HTMLButtonElement>("button[aria-label=\"发送\"]");
    act(() => {
      sendButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(onSend).toHaveBeenCalledWith(`${secondLongText}${firstLongText}${thirdLongText}`);
  });

  it("folds repeated long pastes into sequential rows", () => {
    const firstLongText = longPastedPrompt("# A 交接提示词", "A");
    const secondLongText = longPastedPrompt("# B 交接提示词", "B");
    const onSend = vi.fn();
    renderStatefulComposer({ onSend });

    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    expect(textarea).not.toBeNull();

    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, firstLongText);
    });
    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, secondLongText);
    });

    const cards = Array.from(container.querySelectorAll(".composer-collapsed-prompt-card"));
    expect(cards).toHaveLength(2);
    expect(cards[0]?.textContent).toContain("# A 交接提示词");
    expect(cards[1]?.textContent).toContain("# B 交接提示词");
    expect((textarea as HTMLTextAreaElement).value).toBe("");

    act(() => {
      setTextareaValue(textarea as HTMLTextAreaElement, "\n要求后续变更");
    });

    const sendButton = container.querySelector<HTMLButtonElement>("button[aria-label=\"发送\"]");
    act(() => {
      sendButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(onSend).toHaveBeenCalledWith(`${firstLongText}${secondLongText}\n要求后续变更`, [
      { type: "pasted_text", text: firstLongText },
      { type: "pasted_text", text: secondLongText },
      { type: "text", text: "\n要求后续变更" },
    ]);
  });

  it("removes only the folded prefix and keeps the follow-up draft", () => {
    const longText = longPastedPrompt();
    const onSend = vi.fn();
    renderStatefulComposer({ onSend });

    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    expect(textarea).not.toBeNull();

    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, longText);
    });
    act(() => {
      setTextareaValue(textarea as HTMLTextAreaElement, "要求后续变更");
    });

    const removeButton = container.querySelector<HTMLButtonElement>(".composer-collapsed-prompt-remove");
    expect(removeButton).not.toBeNull();

    act(() => {
      removeButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(container.querySelector(".composer-collapsed-prompt-card")).toBeNull();
    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("要求后续变更");

    const sendButton = container.querySelector<HTMLButtonElement>("button[aria-label=\"发送\"]");
    act(() => {
      sendButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(onSend).toHaveBeenCalledWith("要求后续变更");
  });

  it("keeps the folded chip across a draft swap and return (tab switch)", () => {
    const longText = longPastedPrompt();
    const harness = renderStatefulComposer({ queryHistorySessionID: "session-a" });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    expect(textarea).not.toBeNull();

    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, longText);
    });
    expect(container.querySelector(".composer-collapsed-prompt-card")).not.toBeNull();

    // Switching to another tab replaces the draft with that tab's prompt and
    // clears the chip; the fold layout itself must survive the swap.
    harness.replacePrompt("另一个 tab 的草稿");
    expect(container.querySelector(".composer-collapsed-prompt-card")).toBeNull();

    harness.replacePrompt(longText);
    expect(container.querySelector(".composer-collapsed-prompt-card")).not.toBeNull();
    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("");
  });

  it("does not re-fold a revealed paste after a draft swap and return", () => {
    const longText = longPastedPrompt();
    const harness = renderStatefulComposer({ queryHistorySessionID: "session-b" });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    expect(textarea).not.toBeNull();

    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, longText);
    });
    act(() => {
      container
        .querySelector<HTMLButtonElement>(".composer-collapsed-prompt-main")
        ?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    expect(container.querySelector(".composer-collapsed-prompt-card")).toBeNull();

    harness.replacePrompt("另一个 tab 的草稿");
    harness.replacePrompt(longText);

    expect(container.querySelector(".composer-collapsed-prompt-card")).toBeNull();
    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(longText);
  });
});

function foldedPromptButton(title: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll<HTMLButtonElement>(".composer-collapsed-prompt-main")).find((button) =>
    button.textContent?.includes(title),
  );
}

function expandPendingMessages(): void {
  const button = container.querySelector<HTMLButtonElement>(
    'button[aria-label="展开待处理消息"]',
  );
  expect(button).not.toBeNull();
  act(() => {
    button?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

describe("Composer queue strip", () => {



  it("renders queued and guide messages in combined sequential order", () => {
    renderComposer({
      running: true,
      queuedMessages: [
        { id: "queue-1", text: "第一个排队消息", images: [], files: [] },
        { id: "queue-2", text: "第二个排队消息", images: [], files: [] }
      ],
      guideMessages: [
        { id: "guide-1", text: "唯一一条引导消息", images: [], files: [] }
      ]
    });
    expandPendingMessages();

    const rows = Array.from(
      container.querySelectorAll<HTMLLIElement>(".composer-queue-row")
    );
    expect(rows).toHaveLength(3);
    // guide (oldest, first) → queue items follow in queue order
    expect(rows[0]?.dataset.position).toBe("1");
    expect(rows[0]?.classList.contains("guide")).toBe(true);
    expect(rows[0]?.querySelector(".composer-queue-index")?.textContent).toBe("1");
    expect(rows[1]?.dataset.position).toBe("2");
    expect(rows[1]?.classList.contains("queue")).toBe(true);
    expect(rows[1]?.querySelector(".composer-queue-index")?.textContent).toBe("2");
    expect(rows[2]?.dataset.position).toBe("3");
    expect(rows[2]?.classList.contains("queue")).toBe(true);
    expect(rows[2]?.querySelector(".composer-queue-index")?.textContent).toBe("3");
  });

  it("shows interrupted held work in server order with per-item continue controls", () => {
    const onGuideQueuedMessage = vi.fn();
    renderComposer({
      running: false,
      queuedMessages: [
        {
          id: "queue-1",
          text: "第一个",
          images: [],
          files: [],
          held: true,
          heldPosition: 1,
          origin: "queue",
        },
        {
          id: "queue-2",
          text: "第二个",
          images: [],
          files: [],
          held: true,
          heldPosition: 2,
          origin: "queue",
        },
      ],
      guideMessages: [
        {
          id: "guide-1",
          text: "先前引导",
          images: [],
          files: [],
          held: true,
          heldPosition: 0,
          origin: "steer",
        },
      ],
      onGuideQueuedMessage,
    });

    expect(container.querySelector(".composer-pending-preview")?.textContent).toBe(
      "当前回复已中断；这些 Steer 和 Queue 不会自动执行。",
    );
    expect(container.querySelector(".composer-pending-title")).toBeNull();
    expect(container.querySelector(".composer-pending-drawer")?.classList.contains("expanded")).toBe(true);
    const rows = Array.from(
      container.querySelectorAll<HTMLLIElement>(".composer-queue-row"),
    );
    expect(rows.map((row) => row.querySelector(".composer-queue-preview")?.textContent)).toEqual([
      "先前引导",
      "第一个",
      "第二个",
    ]);
    const continueGuide = container.querySelector<HTMLButtonElement>(
      'button[aria-label="继续暂存任务 1"]',
    );
    act(() => {
      continueGuide?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onGuideQueuedMessage).toHaveBeenCalledWith("guide-1");
  });

  it("lives inside the composer shell so the drawer spans the input width", () => {
    renderComposer({
      running: true,
      queuedMessages: [
        { id: "queue-1", text: "排队宽度测试", images: [], files: [] }
      ]
    });

    const pending = container.querySelector(".composer-pending-drawer");
    const shell = container.querySelector(".composer-shell");
    expect(pending).not.toBeNull();
    expect(shell).not.toBeNull();
    expect(shell?.contains(pending ?? null)).toBe(true);
  });

  it("lets a queued message become a guide from a single inline button click", () => {
    const onGuideQueuedMessage = vi.fn();
    renderComposer({
      running: true,
      queuedMessages: [
        { id: "queue-1", text: "要求后续变更", images: [], files: [] }
      ],
      onGuideQueuedMessage
    });
    expandPendingMessages();

    const guideButton = container.querySelector<HTMLButtonElement>(
      'button[aria-label="将消息 1 加入当前回复"]',
    );
    expect(guideButton).not.toBeNull();

    act(() => {
      guideButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(onGuideQueuedMessage).toHaveBeenCalledWith("queue-1");
  });

  it("removes a queued message from the inline close button", () => {
    const onRemoveQueuedMessage = vi.fn();
    renderComposer({
      running: true,
      queuedMessages: [
        { id: "queue-1", text: "准备删除", images: [], files: [] }
      ],
      onRemoveQueuedMessage
    });
    expandPendingMessages();

    const removeButton = container.querySelector<HTMLButtonElement>(
      "button[aria-label=\"移除排队消息 1\"]"
    );
    expect(removeButton).not.toBeNull();

    act(() => {
      removeButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(onRemoveQueuedMessage).toHaveBeenCalledWith("queue-1");
  });

  it("edits a queued message by clicking the preview text", () => {
    const onEditQueuedMessage = vi.fn();
    renderComposer({
      running: true,
      queuedMessages: [
        { id: "queue-1", text: "要求后续变更", images: [], files: [] }
      ],
      onEditQueuedMessage
    });
    expandPendingMessages();

    const previewButton = container.querySelector<HTMLButtonElement>(
      "button[aria-label=\"编辑排队消息内容 1\"]"
    );
    expect(previewButton).not.toBeNull();

    act(() => {
      previewButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(onEditQueuedMessage).toHaveBeenCalledWith("queue-1");
  });

  it("edits a queued message from the explicit inline edit button", () => {
    const onEditQueuedMessage = vi.fn();
    renderComposer({
      running: true,
      queuedMessages: [
        { id: "queue-1", text: "要求后续变更", images: [], files: [] }
      ],
      onEditQueuedMessage
    });
    expandPendingMessages();

    const editButton = container.querySelector<HTMLButtonElement>(
      "button[aria-label=\"编辑排队消息 1\"]"
    );
    expect(editButton).not.toBeNull();

    act(() => {
      editButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(onEditQueuedMessage).toHaveBeenCalledWith("queue-1");
  });

  it("moves a guide back to the queue from the reverse action", () => {
    const onGuideQueuedMessage = vi.fn();
    const onRemoveGuideMessage = vi.fn();
    renderComposer({
      running: true,
      guideMessages: [
        { id: "guide-1", text: "已引导消息", images: [], files: [] }
      ],
      onGuideQueuedMessage,
      onRemoveGuideMessage,
    });
    expandPendingMessages();

    const requeueButton = container.querySelector<HTMLButtonElement>(
      'button[aria-label="将引导 1 改为下一条发送"]',
    );
    expect(requeueButton).not.toBeNull();

    act(() => {
      requeueButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(onGuideQueuedMessage).toHaveBeenCalledWith("guide-1");
    expect(onRemoveGuideMessage).not.toHaveBeenCalled();
  });

  it("does not render a per-row overflow menu (actions are inline)", () => {
    renderComposer({
      running: true,
      queuedMessages: [
        { id: "queue-1", text: "no menu", images: [], files: [] }
      ],
      guideMessages: [
        { id: "guide-1", text: "no menu either", images: [], files: [] }
      ]
    });

    expect(container.querySelector('[role="menu"]')).toBeNull();
    expect(
      container.querySelector('button[aria-label="待发送消息操作"]')
    ).toBeNull();
  });
});

describe("Composer permission menu", () => {
  it("maps permission summaries to mode chip states", () => {
    expect(permissionModeFromSummary()).toBe("standard");
    expect(permissionModeFromSummary({ mode: "standard" })).toBe("standard");
    expect(permissionModeFromSummary({ mode: "read_only" })).toBe("read_only");
    expect(permissionModeFromSummary({ mode: "unconfined" })).toBe("unconfined");
  });

  it("shows the everyday permission modes in the composer menu", () => {
    const onSelectPermissionMode = vi.fn();
    renderComposer({
      accessMenuOpen: true,
      permissions: { mode: "standard" },
      onSelectPermissionMode,
    });

    const chip = container.querySelector<HTMLButtonElement>(
      "button[aria-label=\"权限模式：标准\"]",
    );
    expect(chip).not.toBeNull();
    expect(chip?.disabled).toBe(false);

    const labels = Array.from(
      document.body.querySelectorAll<HTMLButtonElement>(
        "button[role=\"menuitemradio\"] strong",
      ),
    ).map((label) => label.textContent?.trim());
    expect(labels).toEqual(["工作区内完全信任", "只读", "无边界"]);
    expect(document.body.textContent).not.toContain("平衡");
    expect(document.body.textContent).not.toContain("严格");
    expect(document.body.textContent).not.toContain("替我审批");

    const checkedLabels = Array.from(
      document.body.querySelectorAll<HTMLButtonElement>(
        "button[role=\"menuitemradio\"][aria-checked=\"true\"] strong",
      ),
    ).map((label) => label.textContent?.trim());
    expect(checkedLabels).toEqual(["工作区内完全信任"]);
    expect(document.body.textContent).not.toContain("profile:");
    expect(document.body.textContent).not.toContain("reviewer:");
  });

  it("lets the user switch between the three permission modes", () => {
    const onSelectPermissionMode = vi.fn();
    renderComposer({
      accessMenuOpen: true,
      permissions: { mode: "standard" },
      onSelectPermissionMode,
    });

    const readOnlyOption = Array.from(
      document.body.querySelectorAll<HTMLButtonElement>(
        "button[role=\"menuitemradio\"]",
      ),
    ).find((button) => button.textContent?.includes("只读"));
    expect(readOnlyOption).not.toBeUndefined();

    act(() => {
      readOnlyOption?.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });

    expect(onSelectPermissionMode).toHaveBeenCalledWith("read_only");

    const unconfinedOption = Array.from(
      document.body.querySelectorAll<HTMLButtonElement>(
        "button[role=\"menuitemradio\"]",
      ),
    ).find((button) => button.textContent?.includes("无边界"));
    expect(unconfinedOption).not.toBeUndefined();

    act(() => {
      unconfinedOption?.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });

    expect(onSelectPermissionMode).toHaveBeenCalledWith("unconfined");
  });
});

describe("ComposerTokenGauge", () => {
  // The gauge is temporarily hidden from the composer toolbar (see
  // ComposerRuntimeMeters), so these tests mount the component directly
  // instead of going through the full composer. They stay valid so the
  // component behavior is covered until it is restored.
  function renderTokenGauge(props: {
    running: boolean;
    tokensPerSecond: number;
    tokenSpeedSampledAt?: number;
    tokenSpeedSource?: "real" | "estimated" | "none";
  }): void {
    act(() => {
      root = createRoot(container);
      root.render(
        <ComposerTokenGauge
          running={props.running}
          tokensPerSecond={props.tokensPerSecond}
          sampledAt={props.tokenSpeedSampledAt}
          source={props.tokenSpeedSource}
        />,
      );
    });
  }

  it("does not schedule animation frames while idle at zero", () => {
    const requestAnimationFrame = vi.spyOn(window, "requestAnimationFrame");

    try {
      renderTokenGauge({ running: false, tokensPerSecond: 0 });

      expect(requestAnimationFrame).not.toHaveBeenCalled();
    } finally {
      requestAnimationFrame.mockRestore();
    }
  });

  it("keeps the gauge visible with the speed label inline and a hidden hover tooltip", () => {
    renderTokenGauge({ running: false, tokensPerSecond: 0 });
    const gauge = container.querySelector(".composer-token-gauge");
    expect(gauge).not.toBeNull();
    expect(gauge?.getAttribute("data-state")).toBe("idle");
    expect(gauge?.getAttribute("aria-label")).toContain("0 token 每秒");

    // The label is still inline next to the dial when the toolbar is wide
    // enough, but the same value is also available through a hover tooltip
    // for narrow widths where the inline label is hidden by the container query.
    const label = container.querySelector(".composer-token-gauge-label");
    expect(label).not.toBeNull();
    expect(label?.textContent).toContain("0 tok/s");
    expect(label?.textContent).toContain("tok/s");
    expect(document.body.querySelector(".composer-token-gauge-tooltip")).toBeNull();
  });

  it("renders a live gauge without scheduling animation frames and shows the speed on hover", () => {
    const requestAnimationFrame = vi
      .spyOn(window, "requestAnimationFrame")
      .mockImplementation(() => 1);

    try {
      renderTokenGauge({ running: true, tokensPerSecond: 18.4 });

      const gauge = container.querySelector(".composer-token-gauge");
      expect(gauge).not.toBeNull();
      expect(gauge?.getAttribute("data-state")).toBe("running");
      expect(gauge?.getAttribute("title")).toBeNull();
      expect(requestAnimationFrame).not.toHaveBeenCalled();

      // Label is inline by default; hovering opens the tooltip portal.
      const label = container.querySelector(".composer-token-gauge-label");
      expect(label).not.toBeNull();
      expect(label?.textContent).toContain("18 tok/s");
      expect(label?.textContent).toContain("tok/s");
      expect(document.body.querySelector(".composer-token-gauge-tooltip")).toBeNull();

      act(() => {
        gauge?.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
      });
      const tooltip = document.body.querySelector(".composer-token-gauge-tooltip");
      expect(tooltip).not.toBeNull();
      expect(tooltip?.textContent).toContain("18");
      expect(tooltip?.textContent).toContain("token 每秒");

      // Dial components are still rendered.
      const svg = container.querySelector(".composer-token-gauge-svg");
      expect(svg?.getAttribute("width")).toBe("20");
      expect(svg?.getAttribute("height")).toBe("20");
      expect(container.querySelector(".composer-token-gauge-progress")).not.toBeNull();
      expect(container.querySelector(".composer-token-gauge-needle")).not.toBeNull();
      expect(container.querySelector(".composer-token-gauge-inner-arc")).toBeNull();
      expect(container.querySelectorAll(".composer-token-gauge-speed-dot")).toHaveLength(0);
    } finally {
      requestAnimationFrame.mockRestore();
    }
  });

  it("decays a stale token speed without animation frames", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
    const requestAnimationFrame = vi.spyOn(window, "requestAnimationFrame");

    try {
      renderTokenGauge({
        running: true,
        tokensPerSecond: 20,
        tokenSpeedSampledAt: Date.now(),
      });

      expect(container.querySelector(".composer-token-gauge-label")?.textContent).toContain(
        "20 tok/s",
      );

      act(() => {
        vi.advanceTimersByTime(3_800);
      });
      expect(container.querySelector(".composer-token-gauge-label")?.textContent).toContain(
        "10 tok/s",
      );

      act(() => {
        vi.advanceTimersByTime(3_000);
      });
      expect(container.querySelector(".composer-token-gauge-label")?.textContent).toContain(
        "0 tok/s",
      );
      expect(requestAnimationFrame).not.toHaveBeenCalled();
    } finally {
      requestAnimationFrame.mockRestore();
      vi.useRealTimers();
    }
  });

  it("marks fallback token speed as approximate in the inline label", () => {
    renderTokenGauge({
      running: true,
      tokensPerSecond: 18.4,
      tokenSpeedSource: "estimated",
    });

    const label = container.querySelector(".composer-token-gauge-label");
    expect(label?.textContent).toContain("约 18 tok/s");
  });
});

describe("Composer expand button", () => {
  it("anchors the expanded frame to the original bottom edge in the hero composer", () => {
    renderComposer({ variant: "hero" });
    const stack = container.querySelector(".composer-stack");
    const frame = container.querySelector<HTMLDivElement>(".composer-frame");
    const button = container.querySelector<HTMLButtonElement>(".composer-expand-button");
    expect(stack).not.toBeNull();
    expect(frame).not.toBeNull();
    expect(button).not.toBeNull();

    Object.defineProperty(frame!, "offsetHeight", {
      configurable: true,
      get: () => (stack?.classList.contains("is-expanded") ? 420 : 136),
    });

    act(() => {
      button?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(stack?.classList.contains("is-expanded")).toBe(true);
    expect(frame?.style.getPropertyValue("--composer-expanded-offset")).toBe("284px");

    act(() => {
      button?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(stack?.classList.contains("is-expanded")).toBe(false);
    expect(frame?.style.getPropertyValue("--composer-expanded-offset")).toBe("");
  });

  it("renders the expand button inside the composer input area", () => {
    renderComposer({});

    const frame = container.querySelector(".composer-frame");
    const composer = container.querySelector(".composer");
    const button = container.querySelector<HTMLButtonElement>(".composer-expand-button");

    expect(button).not.toBeNull();
    expect(button?.getAttribute("aria-label")).toBe("展开输入框");
    expect(button?.getAttribute("aria-pressed")).toBe("false");
    expect(button?.getAttribute("title")).toBe("展开输入框");
    expect(button?.parentElement).toBe(composer);
    expect(frame?.lastElementChild).toBe(composer);
    expect(button?.querySelector("svg")).not.toBeNull();
  });

  it("keeps the expand button anchored to the input area when messages are queued", () => {
    renderComposer({
      running: true,
      queuedMessages: [
        { id: "queue-1", text: "排队时按钮应该跟输入区对齐", images: [], files: [] },
      ],
    });

    const pending = container.querySelector(".composer-pending-drawer");
    const composer = container.querySelector(".composer");
    const button = container.querySelector<HTMLButtonElement>(".composer-expand-button");

    expect(pending).not.toBeNull();
    expect(composer).not.toBeNull();
    expect(button).not.toBeNull();
    expect(button?.parentElement).toBe(composer);
    expect(pending?.contains(button ?? null)).toBe(false);
  });

  it("toggles the expanded composer state from one click", async () => {
    renderComposer({});
    const stack = container.querySelector(".composer-stack");
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    const button = container.querySelector<HTMLButtonElement>(".composer-expand-button");
    expect(stack).not.toBeNull();
    expect(textarea).not.toBeNull();
    expect(button).not.toBeNull();

    act(() => {
      button?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    await act(async () => {
      await nextAnimationFrame();
    });

    expect(stack?.classList.contains("is-expanded")).toBe(true);
    expect(button?.getAttribute("aria-label")).toBe("收起输入框");
    expect(button?.getAttribute("aria-pressed")).toBe("true");
    expect(button?.getAttribute("title")).toBe("收起输入框");
    expect(document.activeElement).toBe(textarea);

    act(() => {
      button?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(stack?.classList.contains("is-expanded")).toBe(false);
    expect(button?.getAttribute("aria-label")).toBe("展开输入框");
    expect(button?.getAttribute("aria-pressed")).toBe("false");
  });

  it("disables expansion in read-only mode", () => {
    renderComposer({ readOnly: true });
    const stack = container.querySelector(".composer-stack");
    const button = container.querySelector<HTMLButtonElement>(".composer-expand-button");
    expect(button).not.toBeNull();
    expect(button?.disabled).toBe(true);
    expect(button?.getAttribute("title")).toBe("只读会话不可展开");
    expect(stack?.classList.contains("is-expanded")).toBe(false);
  });
});

describe("composer drag and drop", () => {
  function composerFrame(): HTMLElement {
    const frame = container.querySelector<HTMLElement>(".composer-frame");
    if (!frame) throw new Error("missing composer frame");
    return frame;
  }

  it("appends a dragged workspace path to the prompt as plain text", () => {
    renderStatefulComposer({ initialPrompt: "看看这个文件" });

    act(() => {
      dispatchDrag(composerFrame(), "drop", mockDataTransfer({
        types: [WORKSPACE_FILE_DRAG_MIME, "text/plain"],
        pathData: "src/index.ts",
      }));
    });

    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(
      "看看这个文件 src/index.ts ",
    );
  });

  it("inserts a path into an empty prompt without a leading space", () => {
    renderStatefulComposer({});

    act(() => {
      dispatchDrag(composerFrame(), "drop", mockDataTransfer({
        types: [WORKSPACE_FILE_DRAG_MIME],
        pathData: "src/components/",
      }));
    });

    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(
      "src/components/ ",
    );
  });

  it("forwards dropped external files to the attachment pipeline", () => {
    const onPasteAttachmentFiles = vi.fn();
    renderStatefulComposer({ onPasteAttachmentFiles });
    const image = new File(["png"], "shot.png", { type: "image/png" });
    const pdf = new File(["%PDF"], "brief.pdf", { type: "application/pdf" });

    act(() => {
      dispatchDrag(composerFrame(), "drop", mockDataTransfer({
        types: ["Files"],
        files: [image, pdf],
      }));
    });

    expect(onPasteAttachmentFiles).toHaveBeenCalledTimes(1);
    expect(onPasteAttachmentFiles.mock.calls[0][0]).toEqual([image, pdf]);
  });

  it("ignores drops while read-only", () => {
    const onPasteAttachmentFiles = vi.fn();
    renderStatefulComposer({ initialPrompt: "draft", readOnly: true, onPasteAttachmentFiles });

    act(() => {
      dispatchDrag(composerFrame(), "drop", mockDataTransfer({
        types: [WORKSPACE_FILE_DRAG_MIME],
        pathData: "src/index.ts",
      }));
      dispatchDrag(composerFrame(), "drop", mockDataTransfer({
        types: ["Files"],
        files: [new File(["png"], "shot.png", { type: "image/png" })],
      }));
    });

    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("draft");
    expect(onPasteAttachmentFiles).not.toHaveBeenCalled();
  });

  it("accepts path drops on a text-only composer but ignores external files", () => {
    const onPasteAttachmentFiles = vi.fn();
    renderStatefulComposer({ textOnly: true, onPasteAttachmentFiles });

    act(() => {
      dispatchDrag(composerFrame(), "drop", mockDataTransfer({
        types: [WORKSPACE_FILE_DRAG_MIME],
        pathData: "src/index.ts",
      }));
    });

    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("src/index.ts ");

    act(() => {
      dispatchDrag(composerFrame(), "drop", mockDataTransfer({
        types: ["Files"],
        files: [new File(["png"], "shot.png", { type: "image/png" })],
      }));
    });

    expect(onPasteAttachmentFiles).not.toHaveBeenCalled();
  });

  it("highlights the frame during an accepted dragover and clears on dragleave and drop", () => {
    renderStatefulComposer({});
    const frame = composerFrame();

    act(() => {
      dispatchDrag(frame, "dragover", mockDataTransfer({ types: ["Files"] }));
    });
    expect(container.querySelector(".composer-frame-drop-active")).not.toBeNull();

    // Moving between children keeps the highlight: the related target is
    // still inside the frame.
    const child = frame.querySelector("textarea");
    act(() => {
      dispatchDrag(frame, "dragleave", mockDataTransfer({ types: ["Files"] }), child);
    });
    expect(container.querySelector(".composer-frame-drop-active")).not.toBeNull();

    act(() => {
      dispatchDrag(frame, "dragleave", mockDataTransfer({ types: ["Files"] }), document.body);
    });
    expect(container.querySelector(".composer-frame-drop-active")).toBeNull();

    act(() => {
      dispatchDrag(frame, "dragover", mockDataTransfer({ types: ["Files"] }));
    });
    expect(container.querySelector(".composer-frame-drop-active")).not.toBeNull();
    act(() => {
      dispatchDrag(frame, "drop", mockDataTransfer({ types: [WORKSPACE_FILE_DRAG_MIME], pathData: "a.ts" }));
    });
    expect(container.querySelector(".composer-frame-drop-active")).toBeNull();
  });

  it("does not highlight for payloads the composer does not accept", () => {
    renderStatefulComposer({});
    const frame = composerFrame();

    act(() => {
      dispatchDrag(frame, "dragover", mockDataTransfer({ types: ["text/plain"] }));
    });

    expect(container.querySelector(".composer-frame-drop-active")).toBeNull();
  });

  it("fills a requested handoff draft in the split pane composer", () => {
    renderStatefulSplitPaneComposer({ requestedHandoffIntent: "keep the verified fix" });

    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(
      "/handoff -- keep the verified fix",
    );
  });

  it("appends a dragged workspace path in the split pane composer", () => {
    renderStatefulSplitPaneComposer({ initialPrompt: "review" });
    const shell = container.querySelector<HTMLElement>(".split-composer-shell");
    if (!shell) throw new Error("missing split composer shell");

    act(() => {
      dispatchDrag(shell, "drop", mockDataTransfer({
        types: [WORKSPACE_FILE_DRAG_MIME],
        pathData: "src/Button.tsx",
      }));
    });

    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(
      "review src/Button.tsx ",
    );
  });

  it("folds a long paste in the split pane composer while sending the original text plus follow-up", () => {
    const longText = longPastedPrompt();
    const onSend = vi.fn();
    renderStatefulSplitPaneComposer({ onSend });
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    expect(textarea).not.toBeNull();

    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, longText);
    });

    expect(container.querySelector(".composer-collapsed-prompt-card")).not.toBeNull();
    expect(container.querySelector(".composer-collapsed-prompt-title")?.textContent).toBe("# 交接提示词(直接粘贴)");
    expect((textarea as HTMLTextAreaElement).value).toBe("");
    expect((textarea as HTMLTextAreaElement).placeholder).toBe("要求后续变更");

    act(() => {
      setTextareaValue(textarea as HTMLTextAreaElement, "要求后续变更");
    });
    act(() => {
      textarea?.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    });

    expect(onSend).toHaveBeenCalledWith(`${longText}要求后续变更`, [
      { type: "pasted_text", text: longText },
      { type: "text", text: "要求后续变更" },
    ]);
  });

  it("reveals a folded long paste back into the split pane textarea", () => {
    const longText = longPastedPrompt();
    renderStatefulSplitPaneComposer({});
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    expect(textarea).not.toBeNull();

    act(() => {
      pastePlainText(textarea as HTMLTextAreaElement, longText);
    });

    const revealButton = container.querySelector<HTMLButtonElement>(".composer-collapsed-prompt-main");
    expect(revealButton).not.toBeNull();

    act(() => {
      revealButton?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(container.querySelector(".composer-collapsed-prompt-card")).toBeNull();
    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(longText);
  });

  it("forwards dropped files in the split pane composer and highlights its shell", () => {
    const onPasteAttachmentFiles = vi.fn();
    renderStatefulSplitPaneComposer({ onPasteAttachmentFiles });
    const shell = container.querySelector<HTMLElement>(".split-composer-shell");
    if (!shell) throw new Error("missing split composer shell");

    act(() => {
      dispatchDrag(shell, "dragover", mockDataTransfer({ types: ["Files"] }));
    });
    expect(container.querySelector(".split-composer-shell-drop-active")).not.toBeNull();

    const image = new File(["png"], "shot.png", { type: "image/png" });
    act(() => {
      dispatchDrag(shell, "drop", mockDataTransfer({ types: ["Files"], files: [image] }));
    });

    expect(onPasteAttachmentFiles).toHaveBeenCalledTimes(1);
    expect(onPasteAttachmentFiles.mock.calls[0][0]).toEqual([image]);
    expect(container.querySelector(".split-composer-shell-drop-active")).toBeNull();
  });
});
