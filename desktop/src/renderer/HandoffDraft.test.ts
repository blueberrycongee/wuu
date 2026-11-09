import { describe, expect, it } from "vitest";

import {
  canSubmitHandoffDraft,
  handoffPromptFromIntent,
  handoffPromptFromSelection,
  parseHandoffArgs,
  parseRequestedHandoffIntent,
  reduceHandoffDraft,
} from "./HandoffDraft";

const catalog = {
  providers: [
    {
      id: "openai",
      label: "OpenAI",
      models: [{ id: "gpt-5.4", label: "GPT-5.4" }, { id: "gpt-5.5", label: "GPT-5.5" }],
      allowCustomModel: true,
    },
    {
      id: "anthropic",
      label: "Anthropic",
      models: [{ id: "claude-opus-4.1", label: "Claude Opus 4.1" }],
    },
  ],
};

describe("handoff draft", () => {
  it("keeps incomplete quoted input as a draft instead of an error", () => {
    expect(parseHandoffArgs(`model "open`)).toEqual({
      providerPrefix: "open",
      modelPrefix: "",
      intent: "",
      complete: false,
      hasSlash: false,
      variant: "",
    });
  });

  it("parses provider/model and intent without asking a model", () => {
    const draft = reduceHandoffDraft("openai/gpt-5.5 -- keep the verified fix", catalog, 3);
    expect(draft).toMatchObject({
      providerId: "openai",
      modelId: "gpt-5.5",
      intent: "keep the verified fix",
      status: "resolved",
      pickerView: "models",
      revision: 3,
    });
    expect(canSubmitHandoffDraft(draft)).toBe(true);
  });

  it("round trips selected effort and model ids containing separators with multiline intent", () => {
    const intent = "Implement this.\nKeep notes -- and whitespace. ";
    const prompt = handoffPromptFromSelection("openai", "vendor/model:latest", intent, "high");
    const draft = reduceHandoffDraft(prompt.slice("/handoff ".length), catalog, 1);
    expect(draft).toMatchObject({ providerId: "openai", modelId: "vendor/model:latest", variant: "high", intent });
    expect(canSubmitHandoffDraft(draft)).toBe(true);
  });

  it("shows the runtime summary until the user starts choosing a provider", () => {
    const draft = reduceHandoffDraft("", catalog, 1);
    expect(draft.pickerView).toBe("summary");
    expect(draft.status).toBe("pending");
    expect(canSubmitHandoffDraft(draft)).toBe(false);
  });

  it("opens the provider list from a provider prefix", () => {
    const draft = reduceHandoffDraft("open", catalog, 1);
    expect(draft.pickerView).toBe("providers");
    expect(draft.filterQuery).toBe("open");
    expect(draft.status).toBe("pending");
    expect(canSubmitHandoffDraft(draft)).toBe(false);
  });

  it("opens the model list after a unique provider is chosen", () => {
    const draft = reduceHandoffDraft("openai/", catalog, 1);
    expect(draft.providerId).toBe("openai");
    expect(draft.pickerView).toBe("models");
    expect(canSubmitHandoffDraft(draft)).toBe(false);
  });

  it("marks custom model ids as unverified instead of inventing a provider", () => {
    const draft = reduceHandoffDraft("openai/my-local-gate/vision", catalog, 1);
    expect(draft.status).toBe("unverified");
    expect(draft.modelId).toBe("my-local-gate/vision");
    expect(draft.pickerView).toBe("models");
    expect(canSubmitHandoffDraft(draft)).toBe(true);
  });

  it("opens a configuration draft from an agent request without selecting a model", () => {
    const intent = "Keep the verified fix; check recovery before committing.";
    const prompt = handoffPromptFromIntent(intent);
    const draft = reduceHandoffDraft(prompt.slice("/handoff ".length), catalog, 1);
    expect(draft.intent).toBe(intent);
    expect(draft.providerId).toBe("");
    expect(canSubmitHandoffDraft(draft)).toBe(false);
    expect(parseRequestedHandoffIntent(`{"awaiting_user_configuration":true,"intent":"keep the verified fix"}`)).toBe("keep the verified fix");
    expect(handoffPromptFromIntent("keep the verified fix")).toBe("/handoff -- keep the verified fix");
    expect(handoffPromptFromSelection("openai", "gpt-5.5", "keep going")).toBe("/handoff openai:gpt-5.5: -- keep going");
    expect(parseRequestedHandoffIntent(`{"intent":"keep going"}`)).toBeNull();
  });
});
