import * as React from "react";
import { ConversationControlsTestProvider } from "../ConversationControlsTestProvider";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PluginUIConversationMessageProps } from "../../shared/workbench";
import { ConversationAttachmentPreviewContext } from "../ConversationAttachmentPreview";
import { ConversationBubbleContext } from "./ConversationBubbles";
import { PluginConversationAttachment, PluginConversationMessage } from "./PluginConversation";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function renderMessage(
  bubbles: boolean,
  props: Partial<PluginUIConversationMessageProps> = {},
): HTMLElement {
  act(() => root.render(
    <ConversationControlsTestProvider><ConversationBubbleContext.Provider value={{ enabled: bubbles, revealed: false }}>
      <PluginConversationMessage
        id="message-with-files"
        role="assistant"
        content="Here are the files."
        attachments={["notes.txt", "report.pdf"].map(name => (
          <PluginConversationAttachment
            key={name}
            resourceId={name}
            name={name}
            mediaType={name.endsWith(".pdf") ? "application/pdf" : "text/plain"}
            size={1024}
            loadLabel={`Download ${name}`}
            downloadLabel={`Save ${name}`}
            onLoad={() => {}}
          />
        ))}
        {...props}
      />
    </ConversationBubbleContext.Provider></ConversationControlsTestProvider>,
  ));
  return container.querySelector<HTMLElement>('[data-wuu-component="plugin-ui-conversation-message"]')!;
}

describe("conversation attachment layout", () => {
  it.each(["user", "assistant"] as const)(
    "places %s file cards before and outside the text bubble",
    role => {
      const message = renderMessage(true, { role });
      const attachments = message.querySelector(".plugin-conversation-attachments")!;
      const bubble = message.querySelector(".bubble-message-content")!;

      expect(attachments).not.toBeNull();
      expect(bubble).not.toBeNull();
      expect(message.contains(attachments)).toBe(true);
      expect(attachments.parentElement).toBe(bubble.parentElement);
      expect(attachments.compareDocumentPosition(bubble) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      expect(bubble.querySelector('[data-wuu-component="conversation-attachment"]')).toBeNull();
      expect(bubble.textContent).toContain("Here are the files.");
      expect(message.querySelectorAll('[data-wuu-component="conversation-attachment"]')).toHaveLength(2);
      expect([...attachments.querySelectorAll("strong")].map(node => node.textContent)).toEqual(["notes.txt", "report.pdf"]);
      expect(attachments.querySelectorAll('button[data-action="open-attachment"]')).toHaveLength(2);
      expect(attachments.querySelectorAll('button[data-action="load-attachment"]')).toHaveLength(0);
    },
  );

  it.each(["user", "assistant"] as const)(
    "does not add an empty text bubble to attachment-only %s messages",
    role => {
      const message = renderMessage(true, { role, content: "" });

      expect(message.querySelector(".bubble-message-content")).toBeNull();
      expect(message.querySelector(".plugin-conversation-attachments")).not.toBeNull();
      expect(message.querySelectorAll('[data-wuu-component="conversation-attachment"]')).toHaveLength(2);
    },
  );

  it.each(["user", "assistant"] as const)(
    "keeps standard %s attachments inside the existing message renderer",
    role => {
      const message = renderMessage(false, { role });
      const body = message.querySelector('[data-wuu-component="message"]')!;
      const attachments = message.querySelector(".plugin-conversation-attachments")!;

      expect(message.querySelector(".bubble-message-content")).toBeNull();
      expect(body).not.toBeNull();
      expect(body.contains(attachments)).toBe(true);
      expect(body.textContent).toContain("Here are the files.");
      expect(body.querySelectorAll('[data-wuu-component="conversation-attachment"]')).toHaveLength(2);
    },
  );

  it.each([
    { name: "page.html", mediaType: "text/html", icon: "html" },
    { name: "clip.mp4", mediaType: "video/mp4", icon: "video" },
    { name: "files.zip", mediaType: "application/zip", icon: "archive" },
  ])("distinguishes $name in bubbles without changing its standard icon", ({ name, mediaType, icon }) => {
    const attachments = (
      <PluginConversationAttachment
        resourceId={name}
        name={name}
        mediaType={mediaType}
        size={1024}
        loadLabel={`Download ${name}`}
        downloadLabel={`Save ${name}`}
        onLoad={() => {}}
      />
    );
    const bubbleMessage = renderMessage(true, { attachments });
    const bubbleOpen = bubbleMessage.querySelector('[data-action="open-attachment"]')!;

    expect(bubbleOpen.querySelectorAll("[data-attachment-icon]")).toHaveLength(1);
    expect(bubbleOpen.querySelector(`[data-attachment-icon="${icon}"]`)).not.toBeNull();

    const standardMessage = renderMessage(false, { attachments });
    const standardOpen = standardMessage.querySelector('[data-action="open-attachment"]')!;

    expect(standardOpen.querySelector('[data-icon="file-text"]')).not.toBeNull();
    expect(standardMessage.querySelector("[data-attachment-icon]")).toBeNull();
  });

  it("places bubble attachments before the assistant process shell without duplicating them", () => {
    const message = renderMessage(true, {
      process: { label: "Finished work", content: "Prepared both files." },
    });
    const attachments = message.querySelector(".plugin-conversation-attachments")!;
    const bubble = message.querySelector(".bubble-message-content")!;
    const processShell = bubble.querySelector(".assistant-turn-shell")!;

    expect(processShell).not.toBeNull();
    expect(processShell.textContent).toContain("Here are the files.");
    expect(message.contains(attachments)).toBe(true);
    expect(attachments.compareDocumentPosition(bubble) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(processShell.querySelector(".plugin-conversation-attachments")).toBeNull();
    expect(message.querySelectorAll('[data-wuu-component="conversation-attachment"]')).toHaveLength(2);
  });

  it("keeps standard process attachments after the assistant answer inside its shell", () => {
    const message = renderMessage(false, {
      process: { label: "Finished work", content: "Prepared both files." },
    });
    const processShell = message.querySelector(".assistant-turn-shell")!;
    const answer = processShell.querySelector(".turn-answer-body")!;
    const attachments = message.querySelector(".plugin-conversation-attachments")!;

    expect(message.querySelector(".bubble-message-content")).toBeNull();
    expect(attachments.parentElement).toBe(processShell);
    expect(answer.compareDocumentPosition(attachments) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(processShell.querySelectorAll('[data-wuu-component="conversation-attachment"]')).toHaveLength(2);
  });
});

for (const url of [undefined, "blob:already-verified"]) {
  it(`keeps a long Twin filename unobstructed and only opens the internal tab (${url ? "loaded" : "unloaded"})`, async () => {
    const open = vi.fn(), load = vi.fn(), read = vi.fn(async () => new Blob(["verified"]));
    const name = "A long attachment filename that must not be covered by a download action.txt";
    await act(async () => root.render(
      <ConversationControlsTestProvider><ConversationBubbleContext.Provider value={{ enabled: true, revealed: false }}>
        <ConversationAttachmentPreviewContext.Provider value={open}>
          <PluginConversationAttachment resourceId="long-file" name={name} mediaType="text/plain" size={8} url={url} onRead={read} onLoad={load} loadLabel="Read" downloadLabel="Download" />
        </ConversationAttachmentPreviewContext.Provider>
      </ConversationBubbleContext.Provider></ConversationControlsTestProvider>,
    ));
    const card = container.querySelector(".conversation-file-card")!;
    const button = card.querySelector<HTMLButtonElement>("button")!;
    expect(card.querySelectorAll("button,a")).toHaveLength(1);
    expect(card.querySelector(".conversation-file-download,[download]")).toBeNull();
    await act(async () => { button.focus(); button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true })); });
    expect(document.activeElement).toBe(button);
    expect(card.querySelector("strong")?.textContent).toBe(name);
    expect(card.querySelector(".conversation-file-download,[download]")).toBeNull();
    await act(async () => button.click());
    expect(open).toHaveBeenCalledOnce();
    expect(open.mock.calls[0][0]).toMatchObject({ resourceId: "long-file", name });
    expect(load).not.toHaveBeenCalled();
  });
}
