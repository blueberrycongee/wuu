import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
vi.mock("./i18n", () => ({ useI18n: () => ({ t: (key: string) => key }) }));
vi.mock("./WuuIconMascot", () => ({ WuuIconMascot: () => null }));
vi.mock("./WuuMascot", () => ({ WuuMascot: () => null }));
import { RuntimeLoading } from "./LoadingViews";
it("exposes independent recovery only on failure, without requiring a plugin surface", async () => {
  const container = document.createElement("div"); const root = createRoot(container); const recover = vi.fn();
  try {
    await act(async () => root.render(<RuntimeLoading status="connecting" onRecovery={recover} />));
    expect(container.querySelector("button")).toBeNull();
    await act(async () => root.render(<RuntimeLoading status="Application unavailable" onRecovery={recover} />));
    const button = container.querySelector("button")!;
    expect(button.textContent).toBe("loading.openRecovery");
    await act(async () => button.click()); expect(recover).toHaveBeenCalledTimes(1);
    await act(async () => root.render(<RuntimeLoading status="Application unavailable" />));
    expect(container.querySelector("button")).toBeNull();
  } finally { await act(async () => root.unmount()); }
});

it("offers non-error recovery without an endless switching animation while activation is still pending", async () => {
  const container = document.createElement("div"), root = createRoot(container), recover = vi.fn();
  try {
    await act(async () => root.render(<RuntimeLoading status="connecting" recovering onRecovery={recover} />));
    expect(container.querySelector('[role="status"]')).not.toBeNull();
    expect(container.querySelector('[role="alert"]')).toBeNull();
    expect(container.textContent).not.toContain('loading.failed');
    expect(container.querySelector('.loading-rail')).toBeNull();
    await act(async () => container.querySelector('button')!.click());
    expect(recover).toHaveBeenCalledOnce();
  } finally { await act(async () => root.unmount()); }
});
