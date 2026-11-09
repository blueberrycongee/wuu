import { describe, expect, it, vi } from "vitest";

import { createDeepLinkGate, type DeepLink } from "../src/lib/deepLink";

describe("createDeepLinkGate", () => {
  it("holds a cold-start link until stored credentials are ready", () => {
    const dispatch = vi.fn<(link: DeepLink) => void>();
    const gate = createDeepLinkGate(dispatch);

    gate.receive({ kind: "thread", threadId: "thread-1" });
    expect(dispatch).not.toHaveBeenCalled();

    gate.completeStartup(true);
    expect(dispatch).toHaveBeenCalledWith({ kind: "thread", threadId: "thread-1" });
  });

  it("drops a cold-start link when the device has no credentials", () => {
    const dispatch = vi.fn<(link: DeepLink) => void>();
    const gate = createDeepLinkGate(dispatch);

    gate.receive({ kind: "thread", threadId: "thread-1" });
    gate.completeStartup(false);
    gate.markPaired();

    expect(dispatch).not.toHaveBeenCalled();
  });

  it("dispatches warm links after startup or pairing", () => {
    const dispatch = vi.fn<(link: DeepLink) => void>();
    const gate = createDeepLinkGate(dispatch);

    gate.completeStartup(false);
    gate.receive({ kind: "home" });
    gate.markPaired();
    gate.receive({ kind: "home" });

    expect(dispatch).toHaveBeenCalledTimes(1);
    expect(dispatch).toHaveBeenCalledWith({ kind: "home" });
  });
});
