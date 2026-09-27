import { useState } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  CollapsedComposerPromptCard,
  useCollapsedComposerPrompt
} from "./ComposerCollapsedPrompt";

// The fold layout is persisted in a module-level registry keyed by the draft
// owner. These tests drive the hook directly so the registry can be exercised
// across prompt swaps and real unmount/remount cycles, which the composer
// integration tests cannot do.

let container: HTMLDivElement;
let root: Root | null = null;
let storageKeyCounter = 0;

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
  expose?.({ setPrompt });
  const fold = useCollapsedComposerPrompt({
    prompt,
    setPrompt,
    focusComposerSoon: () => {},
    storageKey
  });
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
