import { act, useContext, useEffect, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { Turn } from "../shared/protocol";
import { I18nProvider, setActiveLocale } from "./i18n";
import {
  WorkspaceDocumentDrawerContext,
  WorkspaceDocumentTurnDock,
} from "./WorkspaceDocumentTurnDock";

function turn(
  id: string,
  status: Turn["status"] = "in_progress",
  userText = "Rewrite the weak section.",
): Turn {
  return {
    id,
    items_view: "full",
    status,
    items: [
      { id: `${id}-user`, type: "user_message", text: userText },
      {
        id: `${id}-agent`,
        type: "agent_message",
        terminal: true,
        status: "completed",
        text: "I am **tightening** that section now.",
      },
    ],
  };
}

function CoordinatedAccessoryProbe(): JSX.Element {
  const documentDrawer = useContext(WorkspaceDocumentDrawerContext);
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    if (documentDrawer?.documentResultExpanded) {
      setExpanded(false);
    }
  }, [documentDrawer?.documentResultExpanded]);

  return (
    <button
      type="button"
      data-testid="coordinated-accessory"
      aria-expanded={expanded}
      onClick={() => {
        setExpanded(true);
        documentDrawer?.collapseDocumentResult();
      }}
    >
      Accessory
    </button>
  );
}

describe("WorkspaceDocumentTurnDock", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    setActiveLocale("en-US");
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });

  function render(turns: Turn[], key = "thread-a", waitingQuery?: string): void {
    act(() => {
      root.render(
        <I18nProvider>
          <WorkspaceDocumentTurnDock key={key} turns={turns} waitingQuery={waitingQuery}>
            <textarea data-testid="composer" data-wuu-component="composer-input" defaultValue="Keep my draft" />
          </WorkspaceDocumentTurnDock>
        </I18nProvider>,
      );
    });
  }

  it("shows a compact turn peek and expands only the rich final answer", () => {
    render([turn("turn-1")]);

    const toggle = container.querySelector<HTMLButtonElement>(
      ".workspace-document-turn-summary",
    );
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
    expect(container.querySelector(".workspace-document-turn-details")).toBeNull();

    act(() => toggle?.click());

    expect(toggle?.getAttribute("aria-expanded")).toBe("true");
    const details = container.querySelector(".workspace-document-turn-details");
    expect(details?.textContent).not.toContain("Rewrite the weak section.");
    expect(details?.textContent).toContain("I am tightening that section now.");
    expect(details?.querySelector("strong")?.textContent).toBe("tightening");
    expect(details?.querySelector(".workspace-document-turn-message")).toBeNull();
  });

  it("keeps the previous result visible while a newer turn only has commentary", () => {
    const previousTurn = turn("turn-previous", "completed");
    const runningTurn = turn("turn-running");
    runningTurn.items = [
      runningTurn.items[0],
      {
        id: "turn-running-commentary",
        type: "agent_message",
        terminal: false,
        status: "in_progress",
        text: "Internal progress that does not belong in the result.",
      },
    ];
    render([previousTurn, runningTurn]);

    expect(container.querySelector('[data-testid="workspace-document-turn-drawer"]')).not.toBeNull();
    expect(container.textContent).not.toContain("Internal progress");
    act(() => {
      container.querySelector<HTMLButtonElement>(".workspace-document-turn-summary")?.click();
    });
    expect(container.querySelector(".workspace-document-turn-details")?.textContent).toContain(
      "I am tightening that section now.",
    );
    expect(container.querySelector('[data-testid="composer"]')).not.toBeNull();
  });

  it("renders the waiting query inside the exposed result drawer summary", () => {
    render([turn("turn-1")], "thread-a", "好的 做完");

    const summary = container.querySelector(".workspace-document-turn-summary");
    let waitingQuery = summary?.querySelector(
      ".workspace-document-turn-waiting-query",
    );
    expect(waitingQuery?.textContent).toBe("好的 做完");
    expect(waitingQuery?.getAttribute("role")).toBe("status");
    expect(waitingQuery?.children).toHaveLength(0);

    act(() => (summary as HTMLButtonElement | null)?.click());

    waitingQuery = summary?.querySelector(
      ".workspace-document-turn-waiting-query",
    );
    expect(waitingQuery).toBeNull();
    expect(container.querySelector(".workspace-document-turn-details")?.textContent).toContain(
      "I am tightening that section now.",
    );

    act(() => (summary as HTMLButtonElement | null)?.click());
    expect(
      summary?.querySelector(".workspace-document-turn-waiting-query")?.textContent,
    ).toBe("好的 做完");
  });

  it("appears and expands automatically when final text starts", () => {
    const runningTurn = turn("turn-live-final");
    runningTurn.items = [runningTurn.items[0]];
    render([runningTurn]);
    expect(container.querySelector('[data-testid="workspace-document-turn-drawer"]')).toBeNull();

    runningTurn.items = [
      runningTurn.items[0],
      {
        id: "turn-live-final-agent",
        type: "agent_message",
        terminal: true,
        status: "completed",
        text: "The **final result** is ready.",
      },
    ];
    render([{ ...runningTurn, items: [...runningTurn.items] }]);

    const toggle = container.querySelector(".workspace-document-turn-summary");
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");
    expect(container.querySelector(".workspace-document-turn-details")?.textContent).toContain(
      "The final result is ready.",
    );
  });

  it("resets the drawer when the active session changes", () => {
    render([turn("turn-shared")]);
    act(() => {
      container.querySelector<HTMLButtonElement>(".workspace-document-turn-summary")?.click();
    });
    expect(
      container
        .querySelector(".workspace-document-turn-summary")
        ?.getAttribute("aria-expanded"),
    ).toBe("true");

    render([turn("turn-shared")], "thread-b");

    expect(
      container
        .querySelector(".workspace-document-turn-summary")
        ?.getAttribute("aria-expanded"),
    ).toBe("false");
  });

  it("coordinates document result expansion with composer accessory drawers", () => {
    act(() => {
      root.render(
        <I18nProvider>
          <WorkspaceDocumentTurnDock turns={[turn("turn-coordinated")]}>
            <CoordinatedAccessoryProbe />
          </WorkspaceDocumentTurnDock>
        </I18nProvider>,
      );
    });

    const resultToggle = container.querySelector<HTMLButtonElement>(
      ".workspace-document-turn-summary",
    );
    const accessory = container.querySelector<HTMLButtonElement>(
      '[data-testid="coordinated-accessory"]',
    );

    act(() => resultToggle?.click());
    expect(resultToggle?.getAttribute("aria-expanded")).toBe("true");
    expect(accessory?.getAttribute("aria-expanded")).toBe("false");

    act(() => accessory?.click());
    expect(resultToggle?.getAttribute("aria-expanded")).toBe("false");
    expect(accessory?.getAttribute("aria-expanded")).toBe("true");

    act(() => resultToggle?.click());
    expect(resultToggle?.getAttribute("aria-expanded")).toBe("true");
    expect(accessory?.getAttribute("aria-expanded")).toBe("false");
  });

  it("keeps the Composer clean when the thread has no user turn", () => {
    render([
      {
        id: "internal-turn",
        items_view: "full",
        status: "completed",
        items: [{ id: "internal-agent", type: "agent_message", text: "Internal" }],
      },
    ]);

    expect(container.querySelector('[data-testid="composer"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="workspace-document-turn-drawer"]')).toBeNull();
  });

  it("reveals the header on editor focus without expanding the result", () => {
    render([turn("focus")]);
    const header = container.querySelector(".workspace-document-turn-header");
    expect(header?.getAttribute("aria-hidden")).toBe("true");
    act(() => container.querySelector<HTMLTextAreaElement>("textarea")?.focus());
    expect(header?.getAttribute("aria-hidden")).toBe("false");
    expect(container.querySelector(".workspace-document-turn-summary")?.getAttribute("aria-expanded")).toBe("false");
  });

  it("does not remount the real composer when the first final answer arrives", () => {
    const pending = turn("first-final");
    pending.items = [pending.items[0]];
    render([pending]);
    const input = container.querySelector("textarea")!;
    act(() => input.focus());
    input.value = "An unsent draft";
    input.setSelectionRange(3, 8);
    render([turn("first-final")]);
    expect(container.querySelector("textarea")).toBe(input);
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe("An unsent draft");
    expect(input.selectionStart).toBe(3);
  });

  it("preserves the draft and expanded presentation through minimize and restore", () => {
    render([turn("minimize")]);
    const input = container.querySelector("textarea")!;
    act(() => input.focus());
    act(() => container.querySelector<HTMLButtonElement>(".workspace-document-turn-summary")?.click());
    act(() => container.querySelector<HTMLButtonElement>('[data-testid="document-chat-minimize"]')?.click());
    expect(container.querySelector("textarea")).toBe(input);
    expect(container.querySelector(".workspace-document-turn-content")?.hasAttribute("inert")).toBe(true);
    act(() => container.querySelector<HTMLButtonElement>('[data-testid="document-chat-restore"]')?.click());
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe("Keep my draft");
    expect(container.querySelector(".workspace-document-turn-summary")?.getAttribute("aria-expanded")).toBe("true");
  });

  it("keeps owned composer popovers open and collapses on an outside pointer", () => {
    render([turn("owned-focus")]);
    act(() => container.querySelector<HTMLTextAreaElement>("textarea")?.focus());
    act(() => container.querySelector<HTMLButtonElement>(".workspace-document-turn-summary")?.click());
    const portal = document.createElement("button");
    portal.dataset.floatingMenuOwner = "composer-plus";
    document.body.appendChild(portal);
    act(() => portal.focus());
    act(() => portal.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true })));
    expect(container.querySelector(".workspace-document-turn-summary")?.getAttribute("aria-expanded")).toBe("true");
    act(() => document.body.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true })));
    expect(container.querySelector(".workspace-document-turn-summary")?.getAttribute("aria-expanded")).toBe("false");
    portal.remove();
  });

  it("collapses the result on Escape without clearing or blurring the editor", () => {
    render([turn("escape")]);
    const input = container.querySelector("textarea")!;
    act(() => input.focus());
    act(() => container.querySelector<HTMLButtonElement>(".workspace-document-turn-summary")?.click());
    act(() => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(container.querySelector(".workspace-document-turn-summary")?.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe("Keep my draft");
  });

  it("keeps a newly arriving result minimized until the owner restores it", () => {
    render([turn("before-minimize")]);
    act(() => container.querySelector<HTMLTextAreaElement>("textarea")?.focus());
    act(() => container.querySelector<HTMLButtonElement>('[data-testid="document-chat-minimize"]')?.click());
    const restore = container.querySelector<HTMLButtonElement>('[data-testid="document-chat-restore"]')!;
    render([turn("before-minimize"), turn("after-minimize")]);
    expect(document.activeElement).toBe(restore);
    expect(container.querySelector(".workspace-document-turn-content")?.hasAttribute("inert")).toBe(true);
    act(() => restore.click());
    expect(container.querySelector(".workspace-document-turn-summary")?.getAttribute("aria-expanded")).toBe("true");
  });

  it("opens only the real conversation callback and supports menu keyboard dismissal", () => {
    let opened = 0;
    act(() => root.render(<I18nProvider>
      <WorkspaceDocumentTurnDock turns={[turn("menu")]} onOpenConversation={() => { opened += 1; }}>
        <textarea data-wuu-component="composer-input" defaultValue="A draft" />
      </WorkspaceDocumentTurnDock>
    </I18nProvider>));
    act(() => container.querySelector<HTMLTextAreaElement>("textarea")?.focus());
    const options = container.querySelector<HTMLButtonElement>('[aria-haspopup="menu"]')!;
    act(() => options.click());
    const menu = container.querySelector('[role="menu"]')!;
    const items = menu.querySelectorAll<HTMLButtonElement>('[role="menuitem"]');
    expect(document.activeElement).toBe(items[0]);
    act(() => items[0].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })));
    expect(document.activeElement).toBe(items[1]);
    act(() => items[1].dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(container.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(options);
    expect(opened).toBe(0);
    act(() => options.click());
    act(() => container.querySelector<HTMLButtonElement>('[role="menuitem"]')?.click());
    expect(opened).toBe(1);
    expect(container.querySelector("textarea")?.value).toBe("A draft");
  });

});
