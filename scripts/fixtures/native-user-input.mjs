#!/usr/bin/env node
// A deterministic local native protocol peer for the user-input E2E journey.
// No credentials, external services, or inference are used.
import { appendFileSync, existsSync, readFileSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { createInterface } from "node:readline";
const turns = new Map(), requests = new Map(), threads = new Map();
const historyPath = process.env.WUU_INPUT_NATIVE_LOG + ".history.json";
const nativeHistory = existsSync(historyPath) ? JSON.parse(readFileSync(historyPath, "utf8")) : {};
const saveHistory = () => writeFileSync(historyPath, JSON.stringify(nativeHistory));
const configured = () => process.env.WUU_INPUT_NATIVE_CONFIG && existsSync(process.env.WUU_INPUT_NATIVE_CONFIG)
  ? JSON.parse(readFileSync(process.env.WUU_INPUT_NATIVE_CONFIG, "utf8")) : {};
function loadThread(threadID, overrides) {
  const config = configured();
  const mode = overrides?.["features.default_mode_request_user_input"] ?? config.feature ?? false;
  threads.set(threadID, { defaultMode: mode, toolEnabled: config.tool !== false });
  log({ loaded: threadID, defaultMode: mode, toolEnabled: config.tool !== false, overrides });
}
let threadIndex = 0, turnIndex = 0;
const write = value => process.stdout.write(JSON.stringify(value) + "\n");
const log = value => appendFileSync(process.env.WUU_INPUT_NATIVE_LOG, JSON.stringify({ pid: process.pid, ...value }) + "\n");
const reply = (id, result) => write({ id, result });
function finish(turnID, status = "completed") {
  const turn = turns.get(turnID);
  if (!turn || turn.finished) return;
  turn.finished = true;
  nativeHistory[turn.threadID].find(entry => entry.id === turnID).status = status;
  saveHistory();
  const text = "Question journey completed: " + turn.scenario;
  write({ method: "item/completed", params: { threadId: turn.threadID, turnId: turnID, item: { id: "answer-" + turnID, type: "agentMessage", text, phase: "final_answer" } } });
  write({ method: "turn/completed", params: { threadId: turn.threadID, turn: { id: turnID, status } } });
}
const control = createServer(async (req, res) => {
  let raw = ""; for await (const part of req) raw += part;
  const body = JSON.parse(raw || "{}");
  if (body.action === "finish" || body.action === "finish-disconnect") finish(body.turnID);
  res.end("ok", () => {
    if (body.action === "disconnect" || body.action === "finish-disconnect") {
      if (body.action === "disconnect") {
        // The fixture persists a stopped native state without delivering its
        // notification. The replacement must read this proof before new input.
        const turn = turns.get(body.turnID);
        nativeHistory[turn.threadID].find(entry => entry.id === body.turnID).status = "interrupted";
        saveHistory();
      }
      process.stdout.write("", () => { control.close(); process.exit(0); });
    }
  });
});
await new Promise(resolve => control.listen(0, "127.0.0.1", resolve));
log({ control: control.address().port });
createInterface({ input: process.stdin }).on("line", line => {
  const message = JSON.parse(line), p = message.params ?? {};
  log({ message });
  if (!message.method) {
    const turnID = requests.get(message.id);
    if (turnID) {
      const turn = turns.get(turnID);
      log({ response: message, turnID, scenario: turn.scenario });
      if (turn.deferredStart !== undefined) {
        log({ startResponse: turnID });
        reply(turn.deferredStart, { turn: { id: turnID } });
      }
      finish(turnID);
    }
    return;
  }
  switch (message.method) {
    case "initialize": reply(message.id, { userAgent: "synthetic-user-input", codexHome: process.env.HOME }); break;
    case "model/list": reply(message.id, { data: [{ id: "fixture", model: "fixture", displayName: "Fixture", isDefault: true, defaultReasoningEffort: "high", supportedReasoningEfforts: [{ reasoningEffort: "high" }] }], nextCursor: null }); break;
    case "config/read": {
      const config = configured();
      reply(message.id, { config: { model: "fixture", model_reasoning_effort: "high", ...(config.feature === undefined ? {} : { features: { default_mode_request_user_input: config.feature } }) } }); break;
    }
    case "thread/start": {
      const id = `native-thread-${process.pid}-${++threadIndex}`;
      loadThread(id, p.config); reply(message.id, { thread: { id }, model: "fixture" }); break;
    }
    case "thread/resume": {
      // A subscribed warm thread retains its loaded feature state. A new native
      // process loads configuration on cold resume, just as on thread/start.
      if (!threads.has(p.threadId)) loadThread(p.threadId, p.config);
      else log({ warmResume: p.threadId, retained: threads.get(p.threadId) });
      reply(message.id, { thread: { id: p.threadId }, model: "fixture" }); break;
    }
    case "thread/read": reply(message.id, { thread: { id: p.threadId, turns: nativeHistory[p.threadId] ?? [] } }); break;
    case "turn/start": {
      const id = `native-turn-${process.pid}-${++turnIndex}`;
      const scenario = p.input.map(v => v.text ?? "").join(" ").trim();
      turns.set(id, { threadID: p.threadId, scenario });
      (nativeHistory[p.threadId] ??= []).push({ id, status: "inProgress", items: [] });
      saveHistory();
      const availability = threads.get(p.threadId);
      if (!availability?.defaultMode || !availability?.toolEnabled) {
        log({ started: id, threadID: p.threadId, scenario, gated: true, availability });
        reply(message.id, { turn: { id } }); finish(id); break;
      }
      const question = { id: "approval.command_execution", header: "Approach", question: "Which approach should this native turn use?", isOther: !scenario.includes("closed"), isSecret: scenario.includes("secret"), options: [{ label: "Blue", description: "Use the blue approach" }, { label: "Green", description: "Use the green approach" }] };
      const questions = [question];
      if (scenario.includes("blocking") || scenario.includes("early")) questions.push({ id: "details", header: "Details", question: "What should we preserve?", options: null, isOther: false, isSecret: false });
      if (scenario.includes("text-only")) { question.options = null; question.isOther = false; }
      if (scenario.includes("duplicate")) questions.push({ ...question });
      const requestID = `question-${id}`;
      requests.set(requestID, id);
      const params = { threadId: scenario.includes("wrong-thread") ? "another-thread" : p.threadId, turnId: scenario.includes("stale") ? "stale-turn" : id, itemId: requestID, questions };
      if (!scenario.includes("legacy")) params.isBlocking = !scenario.includes("optional");
      const request = { id: requestID, method: "item/tool/requestUserInput", params };
      log({ started: id, threadID: p.threadId, scenario, request });
      if (scenario === "early-wait-for-answer") {
        turns.get(id).deferredStart = message.id;
        write({ method: "turn/started", params: { threadId: p.threadId, turn: { id } } });
        write(request);
        break;
      }
      if (scenario.includes("early")) write(request);
      reply(message.id, { turn: { id } });
      if (!scenario.includes("early")) write(request);
      break;
    }
    case "turn/interrupt": {
      const id = p.turnId || [...turns].find(([, t]) => t.threadID === p.threadId && !t.finished)?.[0];
      finish(id, "interrupted"); reply(message.id, {}); break;
    }
    case "initialized": break;
    default: if (message.id !== undefined) reply(message.id, {});
  }
});
process.stdin.on("end", () => { control.close(); process.exit(0); });
