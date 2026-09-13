import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ThreadItem } from "../shared/protocol";

const textProbe = vi.hoisted(() => ({ renders: 0 }));

vi.mock("./LightweightStreamingText", () => ({
  LightweightStreamingText: ({ text }: { text: string }): JSX.Element => {
    textProbe.renders += 1;
    return <span>{text}</span>;
  },
}));

import { ToolActivityTimeline } from "./ToolActivity";
import { I18nProvider, setActiveLocale } from "./i18n";
import type { LanguagePreference } from "../shared/protocol";

let container: HTMLDivElement | undefined;
let root: Root | undefined;

afterEach(() => {
  if (root) {
    act(() => root?.unmount());
  }
  root = undefined;
  container?.remove();
  container = undefined;
  textProbe.renders = 0;
  setActiveLocale("zh-CN");
});

function readTool(id: string, path: string, status: "completed" | "in_progress"): ThreadItem {
  return {
    id,
    type: "tool_call",
    name: "read_file",
    status,
    arguments: JSON.stringify({ path }),
  };
}

describe("ToolActivityTimeline memoization", () => {
  it("updates localized names in existing memoized rows when the app language changes", () => {
    const display = { label: "Working notes", label_translations: { "zh-CN": "工作笔记" } };
    const items: ThreadItem[] = [{
      id: "notes", type: "tool_call", name: "plugin_notes_work_0123456789abcdef",
      status: "completed", arguments: "{}", display,
    }];
    let changeLanguage: ((locale: LanguagePreference) => void) | undefined;
    const preferenceStore = {
      get: (): LanguagePreference => "en-US",
      set: async () => undefined,
      subscribe: (listener: (locale: LanguagePreference) => void) => {
        changeLanguage = listener;
        return () => { changeLanguage = undefined; };
      },
    };
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    act(() => root?.render(
      <I18nProvider preferenceStore={preferenceStore}>
        <ToolActivityTimeline items={items} />
      </I18nProvider>,
    ));
    expect(container.textContent).toContain(display.label);
    act(() => changeLanguage?.("zh-CN"));
    expect(container.textContent).toContain(display.label_translations["zh-CN"]);
    expect(container.textContent).not.toContain(items[0].name);
  });

  it("renders only the appended row when a long process timeline grows", () => {
    const first = readTool("tool-1", "first.ts", "completed");
    const second = readTool("tool-2", "second.ts", "completed");
    const third = readTool("tool-3", "third.ts", "in_progress");
    const render = (items: ThreadItem[]): void => {
      root?.render(<ToolActivityTimeline items={items} streaming />);
    };

    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);

    act(() => render([first, second]));
    expect(textProbe.renders).toBe(2);

    act(() => render([first, second, third]));
    expect(textProbe.renders).toBe(3);
  });
});
