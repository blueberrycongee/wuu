import { useState } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ResponseSelection } from "../shared/protocol";
import { ComposerAttachmentTray } from "./ComposerAttachmentTray";
import {
  CollapsedComposerPromptCard,
  getCollapsedPromptRevision,
  readCollapsedPromptParts,
  rememberCollapsedPromptParts,
  subscribeCollapsedPromptParts,
  useCollapsedComposerPrompt
} from "./ComposerCollapsedPrompt";
import { buildFileSelectionPart } from "./FileSelectionContext";

// The fold layout is persisted in a module-level registry keyed by the draft
// owner. These tests drive the hook directly so the registry can be exercised
// across prompt swaps and real unmount/remount cycles, which the composer
// integration tests cannot do.

let container: HTMLDivElement;
let root: Root | null = null;
let storageKeyCounter = 0;

it("keeps each quote as its own tray card and edits or removes one without losing the other", async () => {
  const quote: ResponseSelection = {
    id: "quote-1", text: `${longText()}\nFinal quoted line`,
    source: { thread_id: "thread", turn_id: "turn", item_id: "item", start_offset: 0, end_offset: 200 },
  };
  const second = { ...quote, id: "quote-2", text: "Another passage" };
  let latest: ResponseSelection | undefined;
  function Harness(): JSX.Element {
    const [selections, setSelections] = useState([quote, second]);
    return <ComposerAttachmentTray images={[]} files={[]} pastedTexts={[]} selections={selections}
      onRemoveImage={() => {}} onRemoveFile={() => {}} onRevealText={() => {}} onRemoveText={() => {}}
      onChangeSelection={(selection) => { latest = selection; setSelections((current) => current.map((item) => item.id === selection.id ? selection : item)); }}
      onRemoveSelection={(id) => setSelections((current) => current.filter((selection) => selection.id !== id))} />;
  }
  act(() => { root = createRoot(container); root.render(<Harness />); });
  const cards = () => container.querySelectorAll(".composer-attachment-tray .composer-response-selection-card");
  expect(cards()).toHaveLength(2);
  expect(cards()[0].querySelector(".composer-document-card-title")?.textContent).toBe(quote.text.replace(/\s+/g, " "));
  act(() => cards()[0].querySelector<HTMLButtonElement>(".composer-document-card-main")!.click());
  expect(document.querySelector("[role=dialog] blockquote")?.textContent).toBe(quote.text);
  const textarea = document.querySelector<HTMLTextAreaElement>("[role=dialog] textarea")!;
  await act(async () => {
    await vi.waitFor(() => expect(document.activeElement).toBe(textarea));
  });
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, "Explain this part");
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  });
  expect(latest).toEqual({ ...quote, comment: "Explain this part" });
  expect(cards()[0].querySelector(".composer-document-card-meta")?.textContent).toBe("Explain this part");
  act(() => document.querySelector<HTMLButtonElement>("[role=dialog] .composer-response-selection-remove")!.click());
  expect(document.querySelector("[role=dialog]")).toBeNull();
  expect(cards()).toHaveLength(1);
  expect(cards()[0].querySelector(".composer-document-card-title")?.textContent).toBe(second.text);
  act(() => cards()[0].querySelector<HTMLButtonElement>(".composer-attachment-card-remove")!.click());
  expect(container.querySelector("ul")).toBeNull();
});

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
});

function nextStorageKey(): string {
  storageKeyCounter += 1;
  return `fold-hook-test-${storageKeyCounter}`;
}

function longText(): string {
  return [
    "# 交接提示词(直接粘贴)",
    "",
    "这是第一段交接内容。",
    "这是第二段交接内容。",
    "这是第三段交接内容。",
    "这是第四段交接内容。",
    "这是第五段交接内容。",
    "这是第六段交接内容。",
    "这是第七段交接内容。",
    "这是第八段交接内容。",
    "这是第九段交接内容。",
    "这是第十段交接内容。",
    "这是第十一段交接内容。",
    "这是第十二段交接内容。",
    "这是第十三段交接内容。",
    "这是第十四段交接内容。",
    "这是第十五段交接内容。"
  ].join("\n");
}

function pasteText(textarea: HTMLTextAreaElement, text: string): void {
  const event = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", {
    value: {
      items: [],
      getData: (type: string) => (type === "text/plain" ? text : "")
    }
  });
  textarea.dispatchEvent(event);
}

type FoldHarnessController = {
  setPrompt: (value: string) => void;
  setStorageKey: (value: string | undefined) => void;
  prompt: string;
  fold: ReturnType<typeof useCollapsedComposerPrompt>;
};

function FoldHarness({
  storageKey,
  initialPrompt = "",
  expose
}: {
  storageKey?: string;
  initialPrompt?: string;
  expose?: (api: FoldHarnessController) => void;
}): JSX.Element {
  const [prompt, setPrompt] = useState(initialPrompt);
  const [ownerKey, setStorageKey] = useState(storageKey);
  const fold = useCollapsedComposerPrompt({
    prompt,
    setPrompt,
    focusComposerSoon: () => {},
    storageKey: ownerKey
  });
  expose?.({ setPrompt, setStorageKey, prompt, fold });
  return (
    <div>
      {fold.hasBlocks ? (
        <div>
          {fold.blocks.map((block, index) => (
            <CollapsedComposerPromptCard
              key={block.id}
              text={block.text}
              onReveal={() => fold.revealBlock(index)}
              onRemove={() => fold.removeBlock(index)}
            />
          ))}
        </div>
      ) : null}
      <textarea
        value={fold.visiblePrompt}
        onChange={(event) =>
          setPrompt(
            fold.hasBlocks
              ? `${fold.prefix}${event.target.value}`
              : event.target.value
          )
        }
        onPaste={(event) =>
          fold.handlePaste(event, {
            readOnly: false,
            fileAttachmentsEnabled: false,
            onPasteAttachmentFiles: () => {}
          })
        }
      />
    </div>
  );
}

function mountHarness(options: {
  storageKey?: string;
  initialPrompt?: string;
  expose?: (api: FoldHarnessController) => void;
}): void {
  act(() => {
    root = createRoot(container);
    root.render(<FoldHarness {...options} />);
  });
}

function unmountHarness(): void {
  act(() => {
    root?.unmount();
  });
  root = null;
}

function textarea(): HTMLTextAreaElement {
  const element = container.querySelector<HTMLTextAreaElement>("textarea");
  if (!element) {
    throw new Error("missing hook harness textarea");
  }
  return element;
}

function foldedCard(): Element | null {
  return container.querySelector(".composer-collapsed-prompt-card");
}

describe("useCollapsedComposerPrompt persistence", () => {
  it("restores folded chips when the same draft returns after a prompt swap", () => {
    const storageKey = nextStorageKey();
    const text = longText();
    let controller: FoldHarnessController | undefined;
    mountHarness({ storageKey, expose: (api) => (controller = api) });

    act(() => {
      pasteText(textarea(), text);
    });
    expect(foldedCard()).not.toBeNull();
    expect(textarea().value).toBe("");

    // Draft swap away: the chip clears because the prompt no longer starts
    // with the folded prefix.
    act(() => {
      controller?.setPrompt("另一个 tab 的草稿");
    });
    expect(foldedCard()).toBeNull();

    // Draft swap back: the fold layout is restored from the registry.
    act(() => {
      controller?.setPrompt(text);
    });
    expect(foldedCard()).not.toBeNull();
    expect(textarea().value).toBe("");
  });

  it("restores folded chips after an unmount and remount", () => {
    const storageKey = nextStorageKey();
    const text = longText();
    mountHarness({ storageKey });

    act(() => {
      pasteText(textarea(), text);
    });
    expect(foldedCard()).not.toBeNull();

    unmountHarness();
    mountHarness({ storageKey, initialPrompt: text });

    expect(foldedCard()).not.toBeNull();
    expect(textarea().value).toBe("");
  });

  it("does not resurrect chips the user revealed before a draft swap", () => {
    const storageKey = nextStorageKey();
    const text = longText();
    let controller: FoldHarnessController | undefined;
    mountHarness({ storageKey, expose: (api) => (controller = api) });

    act(() => {
      pasteText(textarea(), text);
    });
    act(() => {
      container
        .querySelector<HTMLButtonElement>(".composer-collapsed-prompt-card .composer-document-card-main")
        ?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    expect(foldedCard()).toBeNull();
    expect(textarea().value).toBe(text);

    act(() => {
      controller?.setPrompt("另一个 tab 的草稿");
    });
    act(() => {
      controller?.setPrompt(text);
    });

    expect(foldedCard()).toBeNull();
    expect(textarea().value).toBe(text);
  });
});

function selection(id = "selection-1", comment = "Explain this branch") {
  return buildFileSelectionPart({
    workspace: "/workspace",
    path: "src/main.ts",
    start_line: 3,
    start_column: 2,
    end_line: 4,
    end_column: 12,
    quote: "return result;",
    revision: "revision-1",
  }, "comment", comment, id);
}

describe("collapsed file selection metadata", () => {
  it("restores a trimmed comment-only queue entry over the same owner's untrimmed metadata", () => {
    const owner = nextStorageKey();
    const file = selection();
    rememberCollapsedPromptParts(owner, file.text, [file]);
    let controller!: FoldHarnessController;
    mountHarness({ storageKey: owner, initialPrompt: "", expose: (api) => { controller = api; } });
    act(() => {
      rememberCollapsedPromptParts(owner, file.text.trim(), [file]);
      controller.setPrompt(file.text.trim());
    });
    expect(controller.fold.hasBlocks).toBe(true);
    expect(controller.fold.prefix).toBe(file.text);
    expect(textarea().value).toBe("");
    expect(controller.fold.contentPartsForPrompt(controller.prompt)).toEqual([file]);
    act(() => controller.fold.updateFileComment(file.id, "Updated after restore"));
    const edited = buildFileSelectionPart(file.source, file.intent, "Updated after restore", file.id);
    expect(controller.prompt).toBe(edited.text);
    expect(controller.fold.contentPartsForPrompt(controller.prompt)).toEqual([edited]);
  });

  it.each(["Follow up", "  Indented question\n  Second line  ", "\n\nKeep leading blank lines"])("rebuilds the original prefix when typing %j after a trimmed comment-only restore", (followUp) => {
    const owner = nextStorageKey();
    const file = selection();
    rememberCollapsedPromptParts(owner, file.text.trim(), [file]);
    let controller!: FoldHarnessController;
    mountHarness({ storageKey: owner, initialPrompt: file.text.trim(), expose: (api) => { controller = api; } });
    expect(textarea().value).toBe("");
    act(() => {
      const input = textarea();
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, followUp);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(controller.prompt).toBe(file.text + followUp);
    expect(textarea().value).toBe(followUp);
    const parts = controller.fold.contentPartsForPrompt(controller.prompt);
    expect(parts).toEqual([file, { type: "text", text: followUp }]);
    expect(parts!.map((part) => part.text).join("")).toBe(controller.prompt);
    // Registry reads used by the provider must consume the rebuilt separator.
    expect(readCollapsedPromptParts(owner, controller.prompt)).toEqual(parts);
  });

  it("matches leading paste whitespace without consuming whitespace in the visible question", () => {
    const owner = nextStorageKey();
    const pasted = { type: "pasted_text" as const, text: ` \n${longText()}\n\n` };
    const file = selection();
    const question = "  What happens here?\n  Keep both lines.";
    const restoredPrompt = (pasted.text + file.text + question).trim();
    rememberCollapsedPromptParts(owner, restoredPrompt, [pasted, file, { type: "text", text: question }]);
    let controller!: FoldHarnessController;
    mountHarness({ storageKey: owner, initialPrompt: restoredPrompt, expose: (api) => { controller = api; } });
    expect(controller.fold.blocks.map((block) => block.part)).toEqual([pasted, file]);
    expect(textarea().value).toBe(question);
    expect(controller.fold.contentPartsForPrompt(restoredPrompt)).toEqual([pasted, file, { type: "text", text: question }]);
    act(() => controller.fold.removeFileSelection(file.id));
    expect(textarea().value).toBe(question);
    expect(controller.prompt).toBe(pasted.text + question);
  });

  it("accepts a quote appended externally to canonical parts after a trimmed restore", () => {
    const owner = nextStorageKey();
    const pasted = { type: "pasted_text" as const, text: ` \n${longText()}\n\n` };
    const file = selection();
    const restoredPrompt = (pasted.text + file.text).trim();
    rememberCollapsedPromptParts(owner, restoredPrompt, [pasted, file]);
    let controller!: FoldHarnessController;
    mountHarness({ storageKey: owner, initialPrompt: restoredPrompt, expose: (api) => { controller = api; } });
    const canonicalPrompt = readCollapsedPromptParts(owner, controller.prompt)!.map((part) => part.text).join("");
    const quote = "  return next;\n";
    act(() => controller.setPrompt(canonicalPrompt + quote));
    expect(controller.fold.hasBlocks).toBe(true);
    expect(textarea().value).toBe(quote);
    expect(readCollapsedPromptParts(owner, controller.prompt)).toEqual([
      pasted, file, { type: "text", text: quote },
    ]);
    expect(controller.prompt).toBe(pasted.text + file.text + quote);
  });

  it("reveals the exact original paste after restoring a prompt trimmed at both boundaries", () => {
    const owner = nextStorageKey();
    const pasted = { type: "pasted_text" as const, text: ` \n${longText()}\n\n` };
    rememberCollapsedPromptParts(owner, pasted.text.trim(), [pasted]);
    let controller!: FoldHarnessController;
    mountHarness({ storageKey: owner, initialPrompt: pasted.text.trim(), expose: (api) => { controller = api; } });
    expect(foldedCard()).not.toBeNull();
    expect(textarea().value).toBe("");
    expect(controller.fold.contentPartsForPrompt(controller.prompt)).toEqual([pasted]);
    act(() => controller.fold.revealBlock(0));
    expect(textarea().value).toBe(pasted.text);
  });

  it("rejects changes to whitespace between folded blocks or before a visible question", () => {
    const file = selection();
    const pasted = { type: "pasted_text" as const, text: ` \n${longText()}\n\n` };
    const owner = nextStorageKey();
    const changedBoundary = pasted.text.trim() + file.text.trim();
    rememberCollapsedPromptParts(owner, changedBoundary, [pasted, file]);
    expect(readCollapsedPromptParts(owner, changedBoundary)).toBeUndefined();
    const joinedQuestion = file.text.trim() + "Visible question";
    rememberCollapsedPromptParts(owner, joinedQuestion, [file]);
    expect(readCollapsedPromptParts(owner, joinedQuestion)).toBeUndefined();
  });

  it("notifies metadata changes without notifying equivalent writes or visible typing", () => {
    const owner = nextStorageKey();
    const file = selection();
    const listener = vi.fn();
    const unsubscribe = subscribeCollapsedPromptParts(listener);
    const revision = getCollapsedPromptRevision();
    try {
      rememberCollapsedPromptParts(owner, file.text, [file]);
      expect(listener).toHaveBeenCalledTimes(1);
      expect(getCollapsedPromptRevision()).toBeGreaterThan(revision);
      rememberCollapsedPromptParts(owner, `${file.text}Follow up`, [{ ...file }]);
      expect(listener).toHaveBeenCalledTimes(1);
      expect(readCollapsedPromptParts(owner, `${file.text}Follow up`)).toEqual([
        file, { type: "text", text: "Follow up" },
      ]);
      expect(readCollapsedPromptParts(owner, "Unrelated draft")).toBeUndefined();
      rememberCollapsedPromptParts(owner, "", []);
      expect(listener).toHaveBeenCalledTimes(2);
      expect(readCollapsedPromptParts(owner, file.text)).toBeUndefined();
    } finally {
      unsubscribe();
    }
    rememberCollapsedPromptParts(owner, file.text, [file]);
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it("accepts same-owner external additions before the canonical prompt arrives", () => {
    const owner = nextStorageKey();
    const file = selection();
    const nextFile = selection("selection-2", "Review the error case");
    rememberCollapsedPromptParts(owner, file.text, [file]);
    let controller!: FoldHarnessController;
    mountHarness({ storageKey: owner, initialPrompt: file.text, expose: (api) => { controller = api; } });
    expect(controller.fold.blocks[0].part).toEqual(file);
    const nextPrompt = file.text + nextFile.text + "Follow up";
    act(() => {
      rememberCollapsedPromptParts(owner, nextPrompt, [file, nextFile]);
    });
    expect(readCollapsedPromptParts(owner, nextPrompt)).toEqual([file, nextFile, { type: "text", text: "Follow up" }]);
    act(() => controller.setPrompt(nextPrompt));
    expect(controller.fold.blocks.map((block) => block.part)).toEqual([file, nextFile]);
    expect(textarea().value).toBe("Follow up");
    // A metadata-only update must also render, even if canonical text is unchanged.
    const replacement = { ...nextFile, id: "replacement-id" };
    act(() => rememberCollapsedPromptParts(owner, nextPrompt, [file, replacement]));
    expect(controller.fold.blocks[1].part).toEqual(replacement);
    expect(controller.fold.contentPartsForPrompt(nextPrompt)?.[1]).toEqual(replacement);
  });

  it("edits and removes selections while preserving paste metadata and the follow-up", () => {
    const owner = nextStorageKey();
    const file = selection();
    const pasted = { type: "pasted_text" as const, text: longText(), title: "Original title" };
    const prompt = pasted.text + file.text + "Follow up";
    rememberCollapsedPromptParts(owner, prompt, [pasted, file]);
    let controller!: FoldHarnessController;
    mountHarness({ storageKey: owner, initialPrompt: prompt, expose: (api) => { controller = api; } });
    act(() => controller.fold.updateFileComment(file.id, "Check the return value"));
    const edited = buildFileSelectionPart(file.source, file.intent, "Check the return value", file.id);
    expect(controller.prompt).toBe(pasted.text + edited.text + "Follow up");
    expect(controller.fold.contentPartsForPrompt(controller.prompt)).toEqual([
      pasted, edited, { type: "text", text: "Follow up" },
    ]);
    expect(textarea().value).toBe("Follow up");
    act(() => controller.fold.revealBlock(1));
    expect(textarea().value).toBe("Follow up");
    act(() => controller.fold.removeFileSelection(file.id));
    expect(controller.fold.blocks.map((block) => block.part)).toEqual([pasted]);
    expect(controller.prompt).toBe(pasted.text + "Follow up");
    act(() => controller.fold.revealBlock(0));
    expect(readCollapsedPromptParts(owner, controller.prompt)).toBeUndefined();
    expect(textarea().value).toBe("Follow up" + pasted.text);
  });

  it("clears the registry when the last selection is removed", () => {
    const owner = nextStorageKey();
    const file = selection();
    rememberCollapsedPromptParts(owner, file.text, [file]);
    let controller!: FoldHarnessController;
    mountHarness({ storageKey: owner, initialPrompt: file.text + "Keep this", expose: (api) => { controller = api; } });
    act(() => controller.fold.removeFileSelection(file.id));
    expect(controller.prompt).toBe("Keep this");
    expect(controller.fold.hasBlocks).toBe(false);
    expect(readCollapsedPromptParts(owner, file.text)).toBeUndefined();
  });

  it("retains metadata across send-clear and restores queued parts into a mounted hook", () => {
    const owner = nextStorageKey();
    const file = selection();
    rememberCollapsedPromptParts(owner, file.text, [file]);
    let controller!: FoldHarnessController;
    mountHarness({ storageKey: owner, initialPrompt: file.text, expose: (api) => { controller = api; } });
    act(() => controller.setPrompt(""));
    expect(controller.fold.hasBlocks).toBe(false);
    expect(readCollapsedPromptParts(owner, file.text)).toEqual([file]);
    const queued = selection("queued-id", "Queued comment");
    act(() => {
      rememberCollapsedPromptParts(owner, queued.text, [queued]);
      controller.setPrompt(queued.text);
    });
    expect(controller.fold.blocks[0].part).toEqual(queued);
    expect(textarea().value).toBe("");
    act(() => rememberCollapsedPromptParts(owner, queued.text, undefined));
    expect(controller.fold.hasBlocks).toBe(false);
    expect(readCollapsedPromptParts(owner, queued.text)).toBeUndefined();
  });

  it("isolates owners with identical prompt text when switching a mounted composer", () => {
    const firstOwner = nextStorageKey();
    const secondOwner = nextStorageKey();
    const first = selection("first-id");
    const second = { ...first, id: "second-id" };
    rememberCollapsedPromptParts(firstOwner, first.text, [first]);
    rememberCollapsedPromptParts(secondOwner, second.text, [second]);
    let controller!: FoldHarnessController;
    mountHarness({ storageKey: firstOwner, initialPrompt: first.text, expose: (api) => { controller = api; } });
    act(() => controller.setStorageKey(secondOwner));
    expect(controller.fold.blocks[0].part).toEqual(second);
    act(() => controller.fold.removeFileSelection(second.id));
    expect(readCollapsedPromptParts(firstOwner, first.text)).toEqual([first]);
    expect(readCollapsedPromptParts(secondOwner, second.text)).toBeUndefined();
    act(() => {
      controller.setStorageKey(firstOwner);
      controller.setPrompt(first.text);
    });
    expect(controller.fold.blocks[0].part).toEqual(first);
  });

  it("keeps unkeyed paste state local when another owner changes", () => {
    const owner = nextStorageKey();
    let controller!: FoldHarnessController;
    mountHarness({ expose: (api) => { controller = api; } });
    act(() => pasteText(textarea(), longText()));
    const file = selection();
    act(() => rememberCollapsedPromptParts(owner, file.text, [file]));
    expect(controller.fold.contentPartsForPrompt(controller.prompt)).toEqual([{ type: "pasted_text", text: longText() }]);
    act(() => controller.fold.removeBlock(0));
    expect(controller.prompt).toBe("");
    expect(readCollapsedPromptParts(owner, file.text)).toEqual([file]);
  });
});
