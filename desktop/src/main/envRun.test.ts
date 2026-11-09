import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const envRun = resolve(process.cwd(), "scripts/env-run.cjs");

function runThrough(args: string[]): string {
  return execFileSync(process.execPath, [envRun, ...args], {
    encoding: "utf8",
  }).trim();
}

describe("env-run", () => {
  it("passes leading KEY=value tokens to the child environment", () => {
    const output = runThrough([
      "WUU_ENV_RUN_PROBE=hello",
      "EMPTY_OK=",
      process.execPath,
      "-e",
      "console.log(process.env.WUU_ENV_RUN_PROBE + '|' + JSON.stringify(process.env.EMPTY_OK))",
    ]);
    expect(output).toBe('hello|""');
  });

  it("stops env parsing at the first non-assignment token", () => {
    const output = runThrough([
      "A=1",
      process.execPath,
      "-e",
      // B=2 after the command must arrive as an argv token, not an env var.
      "console.log(process.argv[1] + '|' + (process.env.B ?? 'unset'))",
      "B=2",
    ]);
    expect(output).toBe("B=2|unset");
  });

  it("propagates the child's exit code", () => {
    expect(() =>
      runThrough([process.execPath, "-e", "process.exit(3)"]),
    ).toThrowError(expect.objectContaining({ status: 3 }));
  });
});
