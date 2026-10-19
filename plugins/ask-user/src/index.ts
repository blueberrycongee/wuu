import { offerUserQuestions, USER_QUESTION_OFFER_SERVICE, type RuntimePlugin, type UserQuestion } from "@wuu/plugin-sdk";

function object(value: unknown): Record<string, unknown> {
  if (value === null || value === undefined) return {};
  if (typeof value !== "object" || Array.isArray(value)) throw new Error("invalid tool arguments: expected an object");
  return value as Record<string, unknown>;
}

function string(value: unknown): string {
  if (value == null) return "";
  if (typeof value !== "string") throw new Error("invalid tool arguments: expected a string");
  return value;
}

function boolean(value: unknown): boolean {
  if (value == null) return false;
  if (typeof value !== "boolean") throw new Error("invalid tool arguments: expected a boolean");
  return value;
}

function array(value: unknown): unknown[] {
  if (value == null) return [];
  if (!Array.isArray(value)) throw new Error("invalid tool arguments: expected an array");
  return value;
}

const plugin: RuntimePlugin = {
  initialize() {
    return {
      required_services: [{ name: USER_QUESTION_OFFER_SERVICE, major_version: 1, required: true }],
      tools: [{
        id: "ask_user", direct_only: true,
        description: "Offer the user one or more focused questions without pausing the current turn. Prefer clear choices. A later user reply, if any, arrives as ordinary steering input rather than this tool result.",
        input_schema: {
          type: "object",
          properties: {
            questions: {
              type: "array", minItems: 1,
              items: {
                type: "object",
                properties: {
                  id: { type: "string", description: "Stable ID unique within this call." },
                  question: { type: "string", description: "The focused question to answer." },
                  header: { type: "string", description: "Optional compact heading." },
                  detail: { type: "string", description: "Optional context needed to decide." },
                  options: {
                    type: "array",
                    items: {
                      type: "object",
                      properties: {
                        label: { type: "string", description: "Choice label shown to the user." },
                        description: { type: "string", description: "Optional short explanation of this choice." },
                      },
                      required: ["label"], additionalProperties: false,
                    },
                  },
                  multi_select: { type: "boolean", description: "Allow choosing more than one option." },
                  allow_custom: { type: "boolean", description: "Allow a free-text answer." },
                },
                required: ["id", "question"], additionalProperties: false,
              },
            },
          },
          required: ["questions"], additionalProperties: false,
        },
        display: { kind: "ask-user", text: "Asked the user", capability: "interaction" },
      }],
    };
  },
  async executeTool(call, host) {
    if (call.tool_id !== "ask_user") throw new Error(`unknown ask user tool ${JSON.stringify(call.tool_id)}`);
    const input = object(call.arguments);
    const questions: UserQuestion[] = array(input.questions).map((value) => {
      const question = object(value);
      const options = array(question.options).map((value) => {
        const option = object(value);
        const description = string(option.description);
        return { label: string(option.label), ...(description ? { description } : {}) };
      });
      const header = string(question.header);
      const detail = string(question.detail);
      const multiSelect = boolean(question.multi_select);
      const allowCustom = boolean(question.allow_custom);
      return {
        id: string(question.id), question: string(question.question),
        ...(header ? { header } : {}), ...(detail ? { detail } : {}),
        ...(options.length ? { options } : {}),
        ...(multiSelect ? { multi_select: true } : {}), ...(allowCustom ? { allow_custom: true } : {}),
      };
    });
    const offer = await offerUserQuestions(host, questions);
    return { result: { content: [{ type: "text", text: JSON.stringify(offer) }] } };
  },
};

export default plugin;
