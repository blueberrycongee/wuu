import {
  MODEL_REQUEST_SERVICE,
  requireKernelService,
  requireModelRequestService,
  type ModelRequestResult,
  type RuntimePlugin,
} from "@wuu/plugin-sdk";

const storageKey = "next-message.settings.v1";
const maxInputBytes = 8192;
const maxRememberedTurns = 512;
const prompt = "Read the recent conversation and predict what the user is most likely to say next. "
  + "Write as the user, matching their language, tone, and intent. Make it a natural, concise message they could send directly. "
  + "Keep it on one line within 100 Unicode characters. Return only JSON: {\"suggestion\":\"the user's next message\"}.";

type Settings =
  | { model_alias: string; provider?: never; model?: never; variant?: never }
  | { model_alias?: never; provider: string; model: string; variant?: string }
  | { model_alias?: never; provider?: never; model?: never; variant?: never };
interface VisibleTurn { id: string; userText: string; assistantText: string; imageCount: number; fileCount: number }
interface PredictionInput { threadId: string; turnId: string; turns: VisibleTurn[] }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseSettings(value: unknown): Settings {
  if (!isRecord(value) || Object.keys(value).some(key => !["model_alias", "provider", "model", "variant"].includes(key))
    || ["model_alias", "provider", "model", "variant"].some(key => Object.hasOwn(value, key)
      && (typeof value[key] !== "string" || value[key].length > 256
        || /[\u0000-\u001f\u007f-\u009f\u2028\u2029]/u.test(value[key])))) {
    throw new Error("Invalid next-message settings");
  }
  const model_alias = typeof value.model_alias === "string" ? value.model_alias.trim() : "";
  const provider = typeof value.provider === "string" ? value.provider.trim() : "";
  const model = typeof value.model === "string" ? value.model.trim() : "";
  const variant = typeof value.variant === "string" ? value.variant.trim() : "";
  if ((provider === "") !== (model === "") || (model_alias && provider)
    || (Object.hasOwn(value, "variant") && !provider)) throw new Error("Invalid next-message model selection");
  return model_alias ? { model_alias } : provider
    ? { provider, model, ...(Object.hasOwn(value, "variant") ? { variant } : {}) } : {};
}

function parsePrediction(value: unknown): PredictionInput {
  const fields = ["threadId", "turnId", "turns"] as const;
  if (!isRecord(value) || Object.keys(value).length !== fields.length
    || typeof value.threadId !== "string" || !value.threadId.trim()
    || typeof value.turnId !== "string" || !value.turnId.trim()
    || !Array.isArray(value.turns) || value.turns.length < 1 || value.turns.length > 21) {
    throw new Error("Expected a completed visible turn for next-message prediction");
  }
  const input = value as unknown as PredictionInput;
  for (const id of [input.threadId, input.turnId]) {
    if (id.length > 256 || id !== id.trim() || /[\u0000-\u001f\u007f-\u009f]/u.test(id)) {
      throw new Error("Invalid next-message turn identity");
    }
  }
  for (const turn of input.turns) {
    if (!isRecord(turn) || Object.keys(turn).sort().join(",") !== "assistantText,fileCount,id,imageCount,userText"
      || typeof turn.id !== "string" || !turn.id.trim() || turn.id.length > 256
      || typeof turn.userText !== "string" || typeof turn.assistantText !== "string" || !turn.assistantText.trim()
      || !Number.isSafeInteger(turn.imageCount) || turn.imageCount < 0 || turn.imageCount > 100
      || !Number.isSafeInteger(turn.fileCount) || turn.fileCount < 0 || turn.fileCount > 100
      || !turn.userText.trim()) {
      throw new Error("Invalid visible conversation turn");
    }
  }
  if (input.turns.at(-1)?.id !== input.turnId) throw new Error("Prediction source turn changed");
  return input;
}

export function createNextMessagePlugin(): RuntimePlugin {
  let settings: Settings = {};
  let settingsWrites = Promise.resolve();
  // Only identities are retained. The frontend never replays historical turns;
  // bounding recent identities keeps long-running plugin generations small.
  const attempted = new Set<string>();
  return {
    concurrentCapabilities: ["plugin.client.request"],
    async initialize(_params, host) {
      settings = {};
      settingsWrites = Promise.resolve();
      attempted.clear();
      const stored = await host.call("host.storage.get", { scope: "workspace", key: storageKey });
      if (stored.value !== null) {
        let value: unknown;
        try { value = JSON.parse(stored.value); }
        catch { throw new Error("Invalid stored next-message settings"); }
        settings = parseSettings(value);
      }
      return {
        capabilities: [{ id: "plugin.client.request", kind: "decision", version: 1 }],
        required_services: [requireKernelService("host.storage.get"), requireKernelService("host.storage.set"), requireModelRequestService()],
      };
    },
    async invokeCapability(call, host, execution) {
      if (call.capability !== "plugin.client.request") throw new Error("Unknown next-message capability");
      const request = call.input;
      if (!isRecord(request) || typeof request.method !== "string") throw new Error("Invalid next-message client request");
      execution.signal.throwIfAborted();
      switch (request.method) {
        case "next-message.get": {
          await settingsWrites;
          execution.signal.throwIfAborted();
          return { output: { result: { settings: { ...settings } } } };
        }
        case "next-message.update": {
          const next = parseSettings(request.input);
          if (!next.model_alias && !next.provider) throw new Error("Select a model for next-message suggestions");
          // Serialize persistence, not inference: overlapping saves must commit
          // in request order and publish only successfully stored selections.
          const saved = settingsWrites.then(async () => {
            execution.signal.throwIfAborted();
            await host.call("host.storage.set", { scope: "workspace", key: storageKey, value: JSON.stringify(next) }, { signal: execution.signal });
            settings = next;
            return { output: { result: { settings: { ...settings } } } };
          });
          settingsWrites = saved.then(() => undefined, () => undefined);
          return saved;
        }
        case "next-message.predict": {
          const input = parsePrediction(request.input);
          await settingsWrites;
          execution.signal.throwIfAborted();
          const modelSettings = settings;
          const selected = [...input.turns];
          const encode = () => JSON.stringify({ turns: selected.map(turn => ({
            user: turn.userText, assistant: turn.assistantText,
            ...(turn.imageCount ? { images: turn.imageCount } : {}),
            ...(turn.fileCount ? { files: turn.fileCount } : {}),
          })) });
          while (selected.length > 1 && Buffer.byteLength(prompt) + Buffer.byteLength(encode()) > maxInputBytes) selected.shift();
          const content = encode();
          if (Buffer.byteLength(prompt) + Buffer.byteLength(content) > maxInputBytes) {
            throw new Error("Next-message input exceeds the byte budget");
          }
          const key = JSON.stringify([input.threadId, input.turnId]);
          const abstain = { output: { result: { suggestion: null } } };
          if (!modelSettings.model_alias && !modelSettings.provider) return abstain;
          if (attempted.has(key)) return abstain;
          attempted.add(key);
          if (attempted.size > maxRememberedTurns) attempted.delete(attempted.values().next().value!);
          let result: ModelRequestResult;
          try {
            // The handler-scoped host binds this tools-free request to the
            // caller's execution; never retain an initialize/activate host.
            result = await host.call("host.service.call", {
              service: MODEL_REQUEST_SERVICE,
              method: "call",
              params: {
                messages: [{ role: "system", content: prompt }, { role: "user", content }],
                ...modelSettings,
                ...(modelSettings.provider && modelSettings.variant === undefined ? { preferred_effort: "low" } : {}),
                max_output_tokens: 128,
                allow_unbounded_output: true,
                timeout_ms: 30000,
              },
            }, { signal: execution.signal }) as ModelRequestResult;
          } catch {
            // Cancellation is an execution outcome, not a successful abstention.
            execution.signal.throwIfAborted();
            return abstain;
          }
          execution.signal.throwIfAborted();
          if (result.truncated || Buffer.byteLength(result.text) > 4096) return abstain;
          // A single-property grammar also rejects duplicate JSON keys, which
          // JSON.parse alone silently accepts. Invalid JSON still abstains.
          if (!/^\s*\{\s*"suggestion"\s*:\s*(null|"(?:[^"\\]|\\.)*")\s*\}\s*$/u.test(result.text)) return abstain;
          let parsed: { suggestion: unknown };
          try { parsed = JSON.parse(result.text) as { suggestion: unknown }; }
          catch { return abstain; }
          if (typeof parsed.suggestion !== "string") return abstain;
          const suggestion = parsed.suggestion.trim();
          if (!suggestion || [...suggestion].length > 100
            || /[\u0000-\u001f\u007f-\u009f\u2028\u2029\ud800-\udfff]/u.test(parsed.suggestion)) return abstain;
          return { output: { result: { suggestion } } };
        }
        default: throw new Error("Unknown next-message client method");
      }
    },
  };
}

export default createNextMessagePlugin();
