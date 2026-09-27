import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { WuuDesktopApi, EngineAuthResult } from "../shared/protocol";
import { EngineAuthentication } from "./EngineAuthentication";

let container: HTMLDivElement;
let root: Root | undefined;
const methods: EngineAuthResult = { authenticated: false, methods: [{ id: "browser", name: "Browser" }] };
const discover = vi.fn<WuuDesktopApi["listEngineAuthMethods"]>();
const authenticate = vi.fn<WuuDesktopApi["authenticateEngine"]>();
const cancel = vi.fn<WuuDesktopApi["cancelEngineAuth"]>();

beforeEach(() => {
  discover.mockReset().mockResolvedValue(methods);
  authenticate.mockReset().mockResolvedValue({ methods: [], authenticated: true });
  cancel.mockReset().mockResolvedValue({ ok: true });
  vi.stubGlobal("wuu", { listEngineAuthMethods: discover, authenticateEngine: authenticate, cancelEngineAuth: cancel });
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => root!.render(<EngineAuthentication engineID="devin" />));
});

afterEach(() => {
  act(() => root?.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

async function click(id: string): Promise<void> {
  const button = container.querySelector<HTMLButtonElement>(`[data-testid="${id}"]`);
  expect(button).not.toBeNull();
  await act(async () => button!.click());
}

it("separates opening settings, method discovery, and explicit login", async () => {
  expect(discover).not.toHaveBeenCalled();
  expect(authenticate).not.toHaveBeenCalled();
  await click("engine-auth-discover");
  expect(discover).toHaveBeenCalledExactlyOnceWith("devin");
  expect(authenticate).not.toHaveBeenCalled();
  await click("engine-auth-method");
  expect(authenticate).toHaveBeenCalledExactlyOnceWith("devin", "browser");
  expect(container.querySelector('[aria-busy="true"]')).toBeNull();
  expect(cancel).not.toHaveBeenCalled();
});

it("keeps cancellation responsive while login is pending and permits retry after failure", async () => {
  let rejectLogin!: (reason: Error) => void;
  authenticate.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectLogin = reject; }));
  await click("engine-auth-discover");
  await click("engine-auth-method");
  expect(container.querySelector<HTMLButtonElement>('[data-testid="engine-auth-discover"]')!.disabled).toBe(true);
  await click("engine-auth-cancel");
  expect(cancel).toHaveBeenCalledExactlyOnceWith("devin");
  await act(async () => rejectLogin(new Error("native login cancelled")));
  expect(container.querySelector('[role="status"]')!.textContent).toContain("native login cancelled");
  await click("engine-auth-discover");
  expect(discover).toHaveBeenCalledTimes(2);
});

it("cancels an admitted discovery when the panel closes and ignores its late result", async () => {
  let resolveDiscovery!: (result: EngineAuthResult) => void;
  discover.mockImplementationOnce(() => new Promise((resolve) => { resolveDiscovery = resolve; }));
  await click("engine-auth-discover");
  act(() => root!.unmount());
  root = undefined;
  expect(cancel).toHaveBeenCalledExactlyOnceWith("devin");
  await act(async () => resolveDiscovery(methods));
  expect(authenticate).not.toHaveBeenCalled();
});
