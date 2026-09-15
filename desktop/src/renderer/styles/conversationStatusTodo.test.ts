import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const css = readFileSync(resolve(__dirname, "conversation-shell.css"), "utf-8");
const baseCss = readFileSync(resolve(__dirname, "base.css"), "utf-8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const matches = [...css.matchAll(new RegExp(`${escaped}\\s*\\{([^}]+)\\}`, "g"))];
  expect(matches.length, `Missing rule: ${selector}`).toBeGreaterThan(0);
  return matches.map((match) => match[1]).join("\n");
}

describe("TODO hover card styles", () => {
  it("pairs the compact shell inset with concentric inner row corners", () => {
    const card = rule(".conversation-status-todo-card");
    expect(card).toMatch(/padding:\s*var\(--menu-inset\)/);
    expect(card).toMatch(/border-radius:\s*var\(--menu-shell-radius\)/);
    expect(baseCss).toMatch(
      /--menu-shell-radius:\s*calc\(var\(--radius-xs\) \+ var\(--menu-inset\)\)/,
    );
    expect(rule(".conversation-status-todo-list li")).toMatch(
      /border-radius:\s*var\(--radius-xs\)/,
    );
  });

  it("highlights only the current task using the shared theme surface", () => {
    expect(rule(".conversation-status-todo-list li.is-in_progress")).toMatch(
      /background:\s*var\(--menu-hover\)/,
    );
    expect(rule(".conversation-status-todo-list li")).not.toMatch(/background:/);
    expect(rule(".conversation-status-todo-list li.is-completed")).not.toMatch(/background:/);
  });

  it("wraps long task text and explanations inside the scrollable card", () => {
    expect(rule(".conversation-status-todo-list li")).toMatch(/overflow-wrap:\s*anywhere/);
    expect(rule(".conversation-status-todo-explanation")).toMatch(/overflow-wrap:\s*anywhere/);
    expect(rule(".conversation-status-todo-card")).toMatch(/overflow:\s*auto/);
  });
});
