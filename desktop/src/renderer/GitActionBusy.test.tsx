import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApplicationEnvironment } from "./plugins/ApplicationEnvironment";
import { useGitActionBusy } from "./GitActionBusy";

const originalWuu = (window as unknown as { wuu?: unknown }).wuu;
let root: Root | null = null;
let container: HTMLElement | null = null;

afterEach(() => {
  act(() => root?.unmount());
  root = null;
  container?.remove();
  container = null;
  if (originalWuu === undefined) {
    delete (window as unknown as { wuu?: unknown }).wuu;
  } else {
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: originalWuu,
    });
  }
});

describe("useGitActionBusy", () => {
  it("refreshes on server exit and ignores the superseded response", async () => {
    const resolvers: Array<(busy: boolean) => void> = [];
    const gitActionBusy = vi.fn(
      () => new Promise<boolean>((resolve) => resolvers.push(resolve)),
    );

    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: {
        gitActionBusy,

      },
    });

    const environment = new ApplicationEnvironment({ getSource: () => ({ contextKey: "project-1", root: "/repo" }),
      publishStatus: () => {}, assertActive: () => {}, reportError: () => {} });
    let busy = false;
    function Harness(): null {
      busy = useGitActionBusy(environment);
      return null;
    }
    container = document.createElement("div");
    document.body.appendChild(container);
    await act(async () => {
      root = createRoot(container!);
      root.render(<Harness />);
    });

    expect(busy).toBe(true);
    expect(gitActionBusy).toHaveBeenCalledTimes(1);
    expect(gitActionBusy).toHaveBeenLastCalledWith("/repo");

    await act(async () => {
      environment.receive({
        kind: "server-exit",
        workdir: "/repo",
        code: 1,
        message: "crashed",
      });
    });
    expect(gitActionBusy).toHaveBeenCalledTimes(2);
    expect(busy).toBe(true);

    await act(async () => {
      resolvers[1]?.(false);
      await Promise.resolve();
    });
    expect(busy).toBe(false);

    await act(async () => {
      resolvers[0]?.(true);
      await Promise.resolve();
    });
    expect(busy).toBe(false);
    environment.dispose();
  });
});
