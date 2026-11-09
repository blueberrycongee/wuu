import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ComposerInput } from "./ComposerInput";
import { composerSkillPrompt } from "./ComposerSlashCommands";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => root?.unmount());
  root = null;
  container.remove();
});

function renderInput(value: string): void {
  act(() => {
    root ??= createRoot(container);
    root.render(<ComposerInput value={value} onValueChange={() => {}} />);
  });
}

describe("composer skill references", () => {
  it("shows an explicit skill draft as a chip without exposing its identity JSON", () => {
    const value = composerSkillPrompt({
      name: "change-validation",
      source: "project",
      project: { root: "/repo", path: ".agents/skills/change-validation/SKILL.md" },
    }, "Check this change");
    renderInput(value);

    const chip = container.querySelector<HTMLElement>(".composer-skill-reference");
    expect(chip).not.toBeNull();
    expect(chip?.querySelector(".composer-reference-name")?.textContent).toBe("/change-validation");
    expect(chip?.dataset.skillToken).toBe(value.slice(0, value.indexOf("\n\n")));
    expect(container.querySelector(".composer-rich-input")?.textContent).toContain("Check this change");
    expect(container.querySelector(".composer-rich-input")?.textContent).not.toContain('"source"');
  });

  it("leaves an incomplete skill header editable as ordinary text", () => {
    const value = "/skill not-json\n\nKeep editing";
    renderInput(value);

    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(value);
    expect(container.querySelector(".composer-skill-reference")).toBeNull();
  });
});
