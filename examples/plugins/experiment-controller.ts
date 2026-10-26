// Compile as a standalone ESM bundle. Only the public SDK and Node APIs are used.
// This is an evaluation controller, never installed in the candidate composition.
import { dispatchExperimentController, type ExperimentController } from "@wuu/plugin-sdk";
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

const controller: ExperimentController = {
  async tasks() {
    return { tasks: [{ id: "repair-add", name: "Repair addition", prompt: "Fix add.mjs so add(a,b) adds its arguments. Inspect the file and use a tool to edit it.", required_tools: ["read"] }] };
  },
  async schedule({ tasks, policy }) {
    return { task_ids: tasks.map(task => task.id), concurrency: policy.concurrency };
  },
  async setup({ root }) {
    await writeFile(join(root, "add.mjs"), "export const add = (a, b) => a - b;\n", { flag: "wx" });
    return {};
  },
  async score({ root }) {
    // The grader stays outside the candidate workspace. No hidden answer file is
    // placed beside the implementation. This intentionally simple text scorer is
    // an example policy, not proof of semantic correctness for arbitrary code.
    const source = await readFile(join(root, "add.mjs"), "utf8");
    const passed = /=>\s*a\s*\+\s*b\s*;?/.test(source);
    return { passed, reason: passed ? "addition expression observed" : "expected addition expression absent" };
  },
  async accept({ baseline, candidate }) {
    const allPass = [...baseline.results, ...candidate.results].every(result => result.success);
    const turns = (report: typeof baseline) => report.results.reduce((sum, result) => sum + result.turns, 0);
    return { decision: allPass && turns(candidate) < turns(baseline) ? "accept" : "reject", reason: "Predeclared policy: every task passes and candidate uses fewer model turns." };
  },
};
let input = "";
for await (const chunk of process.stdin) input += chunk;
try {
  process.stdout.write(JSON.stringify(await dispatchExperimentController(controller, JSON.parse(input))));
} catch (error) {
  console.error(error);
  process.exitCode = 1;
}
