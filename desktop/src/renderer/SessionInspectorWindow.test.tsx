import { act, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useSessionInspectorWindow } from "./SessionInspectorWindow";
import { clearToasts } from "./Toast";

let root: Root;
let host: HTMLDivElement;
let inspector: ReturnType<typeof useSessionInspectorWindow>;
const native = vi.fn();
function Harness() {
  const ref = useRef<HTMLDivElement>(null);
  inspector = useSessionInspectorWindow(ref);
  return <div ref={ref} style={{ width: inspector.extension?.baseWidth }}><div className="channel-message-stream" style={{paddingLeft: 20}} /></div>;
}
beforeEach(() => {
  native.mockReset();
  Object.defineProperty(window, "innerWidth", { configurable: true, value: 900 });
  Object.defineProperty(window, "wuu", { configurable: true, value: { setSessionInspectorExpansion: native } });
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  act(() => root.render(<Harness />));
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); clearToasts(); });

it("locks the original layout before expansion and does not add space when switching sessions", async () => {
  native.mockImplementation(async ({ open }) => {
    if (open) {
      expect((host.firstChild as HTMLElement).style.width).toBe("900px");
      Object.defineProperty(window, "innerWidth", { configurable: true, value: 1380 });
    } else Object.defineProperty(window, "innerWidth", { configurable: true, value: 900 });
    return { expanded: open, panelWidth: open ? 480 : 0 };
  });
  await act(async () => inspector.open({roomID: "room", sessionRef: "first", name: "Alpha"}));
  expect(inspector.extension?.baseWidth).toBe(900);
  expect(inspector.extension?.gutter).toBe("20px");
  await act(async () => inspector.open({roomID: "room", sessionRef: "second", name: "Beta"}));
  expect(inspector.extension?.baseWidth).toBe(900);
  expect(inspector.extension?.target?.sessionRef).toBe("second");
  await act(async () => inspector.close());
  expect(inspector.extension).toBeNull();
  expect(window.innerWidth).toBe(900);
});

it("leaves the existing layout untouched when the native window cannot expand", async () => {
  native.mockResolvedValue({expanded: false, panelWidth: 0, reason: "insufficient-space"});
  await act(async () => inspector.open({roomID: "room", sessionRef: "first", name: "Alpha"}));
  expect(inspector.extension).toBeNull();
  expect((host.firstChild as HTMLElement).style.width).toBe("");
});

it("serializes closing against an in-flight opening", async () => {
  let resolve!: (result: {expanded: boolean; panelWidth: number}) => void;
  native.mockImplementationOnce(() => new Promise((done) => { resolve = done; })).mockResolvedValue({expanded: false, panelWidth: 0});
  let opened!: Promise<void>;
  await act(async () => { opened = inspector.open({roomID: "room", sessionRef: "first", name: "Alpha"}); await Promise.resolve(); });
  let closed!: Promise<void>;
  await act(async () => { closed = inspector.close(); resolve({expanded: true, panelWidth: 480}); await opened; await closed; });
  expect(native.mock.calls.map(([params]) => params.open)).toEqual([true, false]);
  expect(inspector.extension).toBeNull();
});
