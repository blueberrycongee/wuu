import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SelectMenu, type SelectMenuGroup } from "./SelectMenu";

let container: HTMLDivElement;
let root: Root | null = null;

function stubMatchMedia(matches: boolean): void {
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockReturnValue({ matches }),
  );
}

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
  delete document.documentElement.dataset.hostKind;
  vi.unstubAllGlobals();
});

// jsdom has no real layout; the FloatingMenuPortal reads getBoundingClientRect
// during its layout effect, so hand it zeros rather than let it crash.
Element.prototype.getBoundingClientRect = function (): DOMRect {
  return {
    x: 0,
    y: 0,
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    width: 0,
    height: 0,
    toJSON() {
      return this;
    },
  } as DOMRect;
};

function mount(node: React.ReactElement): void {
  act(() => {
    root = createRoot(container);
    root.render(node);
  });
}

function trigger(): HTMLButtonElement {
  return container.querySelector<HTMLButtonElement>(".select-menu-trigger")!;
}

function openMenu(): void {
  act(() => {
    trigger().dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
}

function panel(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".select-menu-panel");
}

function items(): HTMLButtonElement[] {
  return Array.from(
    document.querySelectorAll<HTMLButtonElement>(".select-menu-item"),
  );
}

function keyOn(element: Element, key: string): void {
  act(() => {
    element.dispatchEvent(
      new KeyboardEvent("keydown", { key, bubbles: true }),
    );
  });
}

const FRUIT = [
  { value: "apple", label: "苹果", priorityKeywords: ["China"] },
  {
    value: "pear",
    label: "梨",
    keywords: ["green fruit", "Indochina"],
    priorityKeywords: ["Thailand"],
  },
  { value: "plum", label: "李子" },
];

describe("SelectMenu", () => {
  it("shows the selected option's label on the trigger and opens on click", () => {
    mount(<SelectMenu value="pear" onChange={() => {}} options={FRUIT} />);
    expect(trigger().querySelector(".select-menu-value")?.textContent).toBe(
      "梨",
    );
    expect(panel()).toBeNull();
    openMenu();
    expect(panel()).not.toBeNull();
    expect(items().map((item) => item.getAttribute("data-value"))).toEqual([
      "apple",
      "pear",
      "plum",
    ]);
    // The current selection carries the checked state.
    const checked = items().find(
      (item) => item.getAttribute("aria-checked") === "true",
    );
    expect(checked?.getAttribute("data-value")).toBe("pear");
  });

  it("falls back to the placeholder when the value matches no option", () => {
    mount(
      <SelectMenu
        value="durian"
        onChange={() => {}}
        options={FRUIT}
        placeholder="选择水果"
      />,
    );
    const value = trigger().querySelector(".select-menu-value");
    expect(value?.textContent).toBe("选择水果");
    expect(trigger().hasAttribute("data-placeholder")).toBe(true);
  });

  it("commits a click on an option and closes the menu", () => {
    const onChange = vi.fn();
    mount(<SelectMenu value="apple" onChange={onChange} options={FRUIT} />);
    openMenu();
    const plum = items().find(
      (item) => item.getAttribute("data-value") === "plum",
    )!;
    act(() => {
      plum.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onChange).toHaveBeenCalledWith("plum");
    expect(panel()).toBeNull();
  });

  it("renders group headers and separators for grouped options", () => {
    const groups: SelectMenuGroup[] = [
      { options: [{ value: "", label: "跟随全局" }] },
      { label: "anthropic", options: [{ value: "a:1", label: "Claude" }] },
      { label: "openai", options: [{ value: "o:1", label: "GPT" }] },
    ];
    mount(<SelectMenu value="" onChange={() => {}} groups={groups} />);
    openMenu();
    expect(
      Array.from(
        document.querySelectorAll(".select-menu-group-label"),
      ).map((label) => label.textContent),
    ).toEqual(["anthropic", "openai"]);
  });

  it("does not commit a disabled option", () => {
    const onChange = vi.fn();
    mount(
      <SelectMenu
        value="apple"
        onChange={onChange}
        options={[
          { value: "apple", label: "苹果" },
          { value: "pear", label: "梨", disabled: true },
        ]}
      />,
    );
    openMenu();
    const pear = items().find(
      (item) => item.getAttribute("data-value") === "pear",
    )!;
    expect(pear.disabled).toBe(true);
    act(() => {
      pear.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onChange).not.toHaveBeenCalled();
  });

  it("filters a searchable option list by label, value, or keywords", () => {
    mount(
      <SelectMenu
        value="apple"
        onChange={() => {}}
        options={FRUIT}
        searchable
        searchPlaceholder="搜索水果"
        emptyMessage="没有匹配的水果"
      />,
    );
    openMenu();
    const search = document.querySelector<HTMLInputElement>(".select-menu-search input")!;
    expect(document.activeElement).toBe(search);

    act(() => {
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(search, "green");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(items().map((item) => item.getAttribute("data-value"))).toEqual(["pear"]);

    act(() => {
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(search, "china");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(items().map((item) => item.getAttribute("data-value"))).toEqual(["apple"]);

    act(() => {
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(search, "indochina");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(items().map((item) => item.getAttribute("data-value"))).toEqual(["pear"]);

    act(() => {
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(search, "orange");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(items()).toHaveLength(0);
    expect(panel()?.textContent).toContain("没有匹配的水果");
  });

  it("does not focus the search field when a searchable menu opens on the touch web shell", () => {
    document.documentElement.dataset.hostKind = "web";
    stubMatchMedia(true);
    mount(
      <SelectMenu
        value="apple"
        onChange={() => {}}
        options={FRUIT}
        searchable
        searchPlaceholder="搜索水果"
      />,
    );
    openMenu();
    const search = document.querySelector<HTMLInputElement>(".select-menu-search input")!;
    expect(search).toBeTruthy();
    expect(panel()).not.toBeNull();
    // The field stays unfocused, so the software keyboard is not summoned.
    expect(document.activeElement).not.toBe(search);
  });

  it("closes on Escape and on an outside pointerdown", () => {
    mount(<SelectMenu value="apple" onChange={() => {}} options={FRUIT} />);
    openMenu();
    expect(panel()).not.toBeNull();
    keyOn(panel()!, "Escape");
    expect(panel()).toBeNull();

    openMenu();
    expect(panel()).not.toBeNull();
    act(() => {
      // jsdom lacks the PointerEvent constructor; a plain event of the same
      // type reaches the component's pointerdown listener just the same.
      document.body.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    });
    expect(panel()).toBeNull();
  });

  it("selects the arrow-focused option with Enter via the keyboard", () => {
    const onChange = vi.fn();
    mount(<SelectMenu value="apple" onChange={onChange} options={FRUIT} />);
    // ArrowDown on the trigger opens the menu.
    keyOn(trigger(), "ArrowDown");
    expect(panel()).not.toBeNull();
    // Cursor starts on the current selection (apple); ArrowDown → pear.
    keyOn(panel()!, "ArrowDown");
    const focused = document.activeElement as HTMLElement | null;
    expect(focused?.getAttribute("data-value")).toBe("pear");
    // Enter activates the focused option button.
    act(() => {
      focused?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onChange).toHaveBeenCalledWith("pear");
  });

  it("stays disabled: no menu opens when the control is disabled", () => {
    mount(
      <SelectMenu value="apple" onChange={() => {}} options={FRUIT} disabled />,
    );
    expect(trigger().disabled).toBe(true);
    openMenu();
    expect(panel()).toBeNull();
  });
});
