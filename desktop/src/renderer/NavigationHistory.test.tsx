import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it } from "vitest";
import { useNavigationHistory } from "./NavigationHistory";

let root: Root;
let container: HTMLDivElement;
afterEach(() => {
  act(() => root?.unmount());
  container?.remove();
});

function setup() {
  let visit!: (key: string | undefined) => void;
  let history!: ReturnType<typeof useNavigationHistory<{ key: string }>>;
  let current: string | undefined;
  let complete: (() => void) | undefined;
  let fail = false;
  const restored: string[] = [];
  function Probe() {
    const [page, setPage] = useState<string | undefined>("A");
    visit = setPage;
    current = page;
    history = useNavigationHistory(page ? { key: page } : undefined, async destination => {
      restored.push(destination.key);
      await new Promise<void>(resolve => { complete = resolve; });
      if (!fail) setPage(destination.key);
    });
    return null;
  }
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => root.render(<Probe />));
  return {
    visit: (key: string | undefined) => act(() => visit(key)),
    get history() { return history; },
    get current() { return current; },
    restored,
    finish: async (succeed = true) => {
      fail = !succeed;
      await act(async () => { complete?.(); });
    },
  };
}

it("replays history without duplicates and discards the forward branch after a new visit", async () => {
  const app = setup();
  expect(app.history.canGoBack).toBe(false);
  expect(app.history.canGoForward).toBe(false);
  app.visit("B"); app.visit("C");
  act(() => { void app.history.back(); }); await app.finish();
  expect(app.current).toBe("B");
  expect(app.history.canGoForward).toBe(true);
  act(() => { void app.history.forward(); }); await app.finish();
  expect(app.current).toBe("C");
  expect(app.history.canGoForward).toBe(false);
  act(() => { void app.history.back(); }); await app.finish();
  app.visit("D");
  expect(app.history.canGoForward).toBe(false);
  act(() => { void app.history.back(); }); await app.finish();
  expect(app.current).toBe("B");
  act(() => { void app.history.back(); }); await app.finish();
  expect(app.current).toBe("A");
  expect(app.history.canGoBack).toBe(false);
});

it("ignores loading states and serializes rapid history clicks", async () => {
  const app = setup();
  app.visit(undefined);
  expect(app.history.canGoBack).toBe(false);
  app.visit("B");
  act(() => { void app.history.back(); void app.history.back(); });
  expect(app.restored).toEqual(["A"]);
  expect(app.history.canGoBack).toBe(false);
  expect(app.history.canGoForward).toBe(false);
  app.visit(undefined);
  await app.finish();
  expect(app.current).toBe("A");
  expect(app.history.canGoBack).toBe(false);
  expect(app.history.canGoForward).toBe(true);
});

it("keeps the cursor on the displayed page after a failed restore and permits retry", async () => {
  const app = setup();
  app.visit("B");
  act(() => { void app.history.back(); }); await app.finish(false);
  expect(app.current).toBe("B");
  expect(app.history.canGoBack).toBe(true);
  expect(app.history.canGoForward).toBe(false);
  act(() => { void app.history.back(); }); await app.finish();
  expect(app.current).toBe("A");
  expect(app.history.canGoForward).toBe(true);
});
