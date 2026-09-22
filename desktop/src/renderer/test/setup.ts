(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// @pierre/trees wraps user CSS in cascade layers. jsdom 25 cannot parse
// @layer and reports a stylesheet error every time a real file tree mounts.
// Unwrap only the library's explicitly marked unsafe style element so tests
// keep exercising the real tree model and DOM without hiding unrelated CSS
// parser failures.
const nodeTextContentDescriptor = Object.getOwnPropertyDescriptor(
  Node.prototype,
  "textContent",
);
if (nodeTextContentDescriptor?.get && nodeTextContentDescriptor.set) {
  const getNodeTextContent = nodeTextContentDescriptor.get;
  const setNodeTextContent = nodeTextContentDescriptor.set;
  Object.defineProperty(HTMLStyleElement.prototype, "textContent", {
    configurable: true,
    enumerable: nodeTextContentDescriptor.enumerable,
    get: function getTextContent(this: HTMLStyleElement): string | null {
      return getNodeTextContent.call(this);
    },
    set: function setTextContent(
      this: HTMLStyleElement,
      value: string | null,
    ): void {
      const layeredCSS =
        typeof value === "string" &&
        this.hasAttribute("data-file-tree-unsafe-css")
          ? value.match(
              /^@layer base, unsafe;\s*@layer unsafe\s*\{\s*([\s\S]*)\s*\}\s*$/,
            )
          : null;
      setNodeTextContent.call(this, layeredCSS?.[1] ?? value);
    },
  });
}

if (typeof globalThis.matchMedia !== "function") {
  Object.defineProperty(globalThis, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}

if (typeof document.queryCommandSupported !== "function") {
  Object.defineProperty(document, "queryCommandSupported", {
    configurable: true,
    value: () => false,
  });
}

if (typeof globalThis.CSS !== "object" || globalThis.CSS === null) {
  Object.defineProperty(globalThis, "CSS", {
    configurable: true,
    value: {},
  });
}

if (typeof globalThis.CSS.escape !== "function") {
  Object.defineProperty(globalThis.CSS, "escape", {
    configurable: true,
    value: (value: string) => String(value).replace(/[^a-zA-Z0-9_-]/g, "\\$&"),
  });
}

const clipboard = (navigator as unknown as { clipboard?: Record<string, unknown> })
  .clipboard ?? {};
if (typeof clipboard.write !== "function") {
  clipboard.write = (items: Array<{ items?: Record<string, unknown> }>) => {
    for (const item of items ?? []) {
      for (const value of Object.values(item.items ?? {})) {
        void Promise.resolve(value).catch(() => undefined);
      }
    }
    return Promise.resolve();
  };
}
if (typeof clipboard.writeText !== "function") {
  clipboard.writeText = () => Promise.resolve();
}
if (typeof clipboard.readText !== "function") {
  clipboard.readText = () => Promise.resolve("");
}
Object.defineProperty(navigator, "clipboard", {
  configurable: true,
  value: clipboard,
});

if (typeof globalThis.ClipboardItem !== "function") {
  Object.defineProperty(globalThis, "ClipboardItem", {
    configurable: true,
    value: class ClipboardItem {
      readonly items: Record<string, unknown>;

      constructor(items: Record<string, unknown>) {
        this.items = items;
      }
    },
  });
}

// jsdom has no layout engine; suites that inspect resizing supply their own observer.
if (typeof globalThis.ResizeObserver !== "function") {
  globalThis.ResizeObserver = class {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  };
}
