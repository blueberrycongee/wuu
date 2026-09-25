import { describe, expect, it } from "vitest";
import type { ProviderModelSummary, ProviderSummary } from "../shared/protocol";
import {
  orderedEffortOptions,
  providerModelContextWindow,
  providerModelReasoningMode,
  providerModelVariantOptions
} from "./RuntimeHelpers";

function providerWithModel(model: ProviderModelSummary | undefined): ProviderSummary | undefined {
  if (!model) {
    return undefined;
  }
  return {
    name: "fake",
    type: "openai-compatible",
    model: model.id,
    models: [model]
  };
}

describe("providerModelContextWindow", () => {
  it("resolves the exact model instead of reusing another model's ceiling", () => {
    const initialized = {
      providers: [
        {
          name: "kimi-for-coding",
          type: "anthropic",
          model: "k3",
          models: [
            {
              id: "k3",
              capabilities: {
                context_window: 1_048_576,
                chat: true,
                tools: true,
                structured_output: true,
                streaming: true,
                system_role: true,
                reasoning: true,
              },
            },
            {
              id: "k3-256k",
              capabilities: {
                context_window: 262_144,
                chat: true,
                tools: true,
                structured_output: true,
                streaming: true,
                system_role: true,
                reasoning: true,
              },
            },
          ],
        },
      ],
    } as Parameters<typeof providerModelContextWindow>[0];

    expect(
      providerModelContextWindow(initialized, "kimi-for-coding", "k3"),
    ).toBe(1_048_576);
    expect(
      providerModelContextWindow(initialized, "kimi-for-coding", "k3-256k"),
    ).toBe(262_144);
  });

  it("uses the smaller model input limit when one is published", () => {
    const initialized = {
      providers: [
        {
          name: "provider",
          type: "openai-compatible",
          model: "model",
          models: [
            {
              id: "model",
              capabilities: {
                context_window: 1_048_576,
                input_limit: 272_000,
              },
            },
          ],
        },
      ],
    } as Parameters<typeof providerModelContextWindow>[0];

    expect(providerModelContextWindow(initialized, "provider", "model")).toBe(
      272_000,
    );
  });
});

describe("providerModelVariantOptions", () => {
  it("returns only the default option when the model exposes no levels", () => {
    const provider = providerWithModel({
      id: "no-levels",
      supported_efforts: [],
      capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true }
    });
    expect(providerModelVariantOptions(provider, "no-levels", "")).toEqual(["", "none"]);
  });

  it("does not append the 'none' toggle when the model cannot reason", () => {
    const provider = providerWithModel({
      id: "no-reasoning",
      supported_efforts: [],
      capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: false }
    });
    expect(providerModelVariantOptions(provider, "no-reasoning", "")).toEqual([""]);
  });

  it("includes every supported effort in the option list", () => {
    const provider = providerWithModel({
      id: "with-levels",
      supported_efforts: ["low", "medium", "high"],
      capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true }
    });
    expect(providerModelVariantOptions(provider, "with-levels", "")).toEqual(["", "low", "medium", "high"]);
  });

  it("orders descending effort lists from weakest to strongest", () => {
    const provider = providerWithModel({
      id: "grok-4.6",
      variants: [{ id: "xhigh" }, { id: "high" }, { id: "medium" }, { id: "low" }],
      supported_efforts: ["xhigh", "high", "medium", "low"],
      capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true }
    });
    expect(providerModelVariantOptions(provider, "grok-4.6", "xhigh")).toEqual([
      "",
      "low",
      "medium",
      "high",
      "xhigh",
    ]);
    expect(orderedEffortOptions(["xhigh", "high", "medium", "low"])).toEqual([
      "low",
      "medium",
      "high",
      "xhigh",
    ]);
  });

  it("prefers explicit variants over supported_efforts", () => {
    const provider = providerWithModel({
      id: "with-variants",
      variants: [{ id: "fast" }, { id: "thorough" }],
      supported_efforts: ["low", "medium"],
      capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true }
    });
    expect(providerModelVariantOptions(provider, "with-variants", "")).toEqual(["", "fast", "thorough"]);
  });

  it("does not offer an incompatible inherited variant", () => {
    const provider = providerWithModel({
      id: "with-levels",
      supported_efforts: ["low", "medium"],
      capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true }
    });
    expect(providerModelVariantOptions(provider, "with-levels", "xhigh")).toEqual([
      "",
      "low",
      "medium",
    ]);
  });

  it("returns just the default option when no provider is configured", () => {
    expect(providerModelVariantOptions(undefined, "anything", "")).toEqual([""]);
  });
});

describe("providerModelReasoningMode", () => {
  it("reports 'off' when the model has no reasoning capability", () => {
    const provider = providerWithModel({
      id: "no-reasoning",
      supported_efforts: [],
      capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: false }
    });
    expect(providerModelReasoningMode(provider, "no-reasoning")).toBe("off");
  });

  it("reports 'toggle' when reasoning is on but no levels are exposed", () => {
    const provider = providerWithModel({
      id: "toggle-only",
      supported_efforts: [],
      capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true }
    });
    expect(providerModelReasoningMode(provider, "toggle-only")).toBe("toggle");
  });

  it("reports 'levels' when the model exposes adjustable efforts", () => {
    const provider = providerWithModel({
      id: "with-levels",
      supported_efforts: ["low", "medium", "high"],
      capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true }
    });
    expect(providerModelReasoningMode(provider, "with-levels")).toBe("levels");
  });

  it("reports 'levels' when the model exposes named variants", () => {
    const provider = providerWithModel({
      id: "with-variants",
      variants: [{ id: "fast" }, { id: "thorough" }],
      capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true }
    });
    expect(providerModelReasoningMode(provider, "with-variants")).toBe("levels");
  });

  it("reports 'off' when no provider is configured", () => {
    expect(providerModelReasoningMode(undefined, "anything")).toBe("off");
  });
});
