import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { TurnContextUsage } from "./AppState";
import { ComposerRuntimeMeters } from "./ComposerRuntimeMeters";

let container: HTMLDivElement;
let root: Root | null = null;

const usage: TurnContextUsage = {
  turnID: "turn-1",
  used: 45_000,
  window: 200_000,
  inputTokens: 30_000,
  cacheCreationTokens: 3_000,
  cacheReadTokens: 12_000,
};

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
});

function renderMeters(activeEngine?: string): void {
  act(() => {
    if (!root) {
      root = createRoot(container);
    }
    root.render(
      <ComposerRuntimeMeters
        running={false}
        fallbackContextUsage={usage}
        activeEngine={activeEngine}
      />,
    );
  });
}

describe("ComposerRuntimeMeters", () => {
  it("keeps the context ring for the built-in Wuu engine", () => {
    renderMeters();
    expect(container.querySelector(".composer-context-meter")).not.toBeNull();

    renderMeters("wuu");
    expect(container.querySelector(".composer-context-meter")).not.toBeNull();
  });

  it("hides the context ring for Codex and other external engines", () => {
    renderMeters("codex");
    expect(container.querySelector(".composer-context-meter")).toBeNull();

    renderMeters("claude");
    expect(container.querySelector(".composer-context-meter")).toBeNull();
  });
});
