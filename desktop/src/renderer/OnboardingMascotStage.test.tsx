import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { OnboardingMascotStage } from "./OnboardingMascotStage";
import { ONBOARDING_PLUGIN_ORDER } from "./onboardingCatalog";

describe("onboarding companion", () => {
  let container: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  async function render(ids: readonly string[]): Promise<void> {
    await act(async () => root.render(<OnboardingMascotStage pluginIDs={ids} />));
  }

  it("keeps the same character while capabilities and shared equipment are added and removed", async () => {
    await render([]);
    const face = container.querySelector("[data-wuu-mascot-activity]");
    expect(face).not.toBeNull();
    // Exercise every capability entering and leaving a populated stage,
    // including both owners of the shared memory/dream equipment. Rendering
    // the entire power set grows exponentially without adding new boundaries.
    const selections: readonly string[][] = [
      [...ONBOARDING_PLUGIN_ORDER],
      ...ONBOARDING_PLUGIN_ORDER.map((id) => ONBOARDING_PLUGIN_ORDER.filter((candidate) => candidate !== id)),
      ...ONBOARDING_PLUGIN_ORDER.map((id) => [id]),
      ["memory", "dream"], ["dream"], ["memory", "dream"], ["memory"], [],
    ];
    for (const ids of selections) {
      await render(ids);
      const rendered = [...container.querySelectorAll("[data-onboarding-capability]")]
        .map((node) => node.getAttribute("data-onboarding-capability"));
      expect(rendered.sort()).toEqual(ids.filter((id) => id !== "subagent").sort());
      expect(container.querySelectorAll("[data-onboarding-companion]:not([hidden])"))
        .toHaveLength(ids.includes("subagent") ? 3 : 1);
      expect(container.querySelector("[data-wuu-mascot-activity]")).toBe(face);
    }
  });

  it("deduplicates equipment, ignores unknown plugins, and stays inert", async () => {
    await render(["memory", "memory", "unknown-plugin"]);
    expect(container.querySelectorAll("[data-onboarding-capability]")).toHaveLength(1);
    expect(container.querySelectorAll("button, input, [tabindex='0']")).toHaveLength(0);
    expect(container.querySelector("[data-testid=onboarding-mascot-stage]")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("wears the Codex or Claude mark and leaves Wuu undecorated", async () => {
    await act(async () => root.render(<OnboardingMascotStage engineID="wuu" />));
    const face = container.querySelector("[data-wuu-mascot-follows-pointer]");
    expect(face).not.toBeNull();
    expect(container.querySelector("[data-onboarding-engine-mark]")).toBeNull();

    await act(async () => root.render(<OnboardingMascotStage engineID="codex" />));
    expect(container.querySelector("[data-wuu-mascot-follows-pointer]")).toBe(face);
    expect(container.querySelector("[data-onboarding-engine-mark]")?.getAttribute("data-onboarding-engine-mark")).toBe("codex");

    await act(async () => root.render(<OnboardingMascotStage engineID="claude" />));
    expect(container.querySelector("[data-onboarding-engine-mark]")?.getAttribute("data-onboarding-engine-mark")).toBe("claude");

    await act(async () => root.render(<OnboardingMascotStage engineID="wuu" />));
    expect(container.querySelector("[data-onboarding-engine-mark]")).toBeNull();
  });
});
