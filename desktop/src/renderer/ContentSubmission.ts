import type { RendererSubmissionV1 } from "../shared/workbench";

export function parseContentSubmission(input: unknown): RendererSubmissionV1 {
  if (!input || typeof input !== "object" || Array.isArray(input)) throw new Error("Content submission must be an object");
  const value = input as Record<string, unknown>;
  if (Object.keys(value).some(key => key !== "text" && key !== "context")
    || typeof value.text !== "string" || !value.text.trim() || value.text.length > 8_000) {
    throw new Error("Content submission requires text of at most 8,000 characters");
  }
  if (value.context === undefined) return { text: value.text.trim() };
  const json = JSON.stringify(value.context, (_key, item: unknown) => {
    if (item === undefined || typeof item === "function" || typeof item === "symbol"
      || typeof item === "bigint" || (typeof item === "number" && !Number.isFinite(item))) {
      throw new Error("Content submission context must be JSON");
    }
    return item;
  });
  if (json.length > 16_384) throw new Error("Content submission context exceeds 16,384 characters");
  return { text: value.text.trim(), context: JSON.parse(json) as unknown };
}
