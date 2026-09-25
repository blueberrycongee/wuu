import { beforeEach, describe, expect, it } from "vitest";
import type { EngineListResult } from "../shared/protocol";
import {
  lastEffortForEngineModel,
  readDraftEngineMemory,
  rememberedEngineRuntime,
  resolveDraftEngineMemory,
  writeDraftEngineMemory,
} from "./DraftEngineMemory";

const MEMORY_KEY = "wuu.desktop.lastDraftEngine";

beforeEach(() => {
  window.localStorage.clear();
});

function inventory(overrides?: Partial<EngineListResult>): EngineListResult {
  return {
    engines: [
      {
        id: "codex",
        enabled: true,
        binary_ok: true,
        models: [
          {
            id: "gpt-5-codex",
            is_default: true,
            supported_efforts: ["low", "medium", "high"],
          },
          { id: "gpt-5", supported_efforts: ["medium"] },
        ],
      },
      {
        id: "claude",
        enabled: true,
        binary_ok: true,
        models: [{ id: "claude-opus-5", supported_efforts: ["medium"] }],
      },
    ],
    settings: { default_engine: "wuu" },
    ...overrides,
  };
}

describe("draft engine memory", () => {
  it("uses the live inventory for protocol engines and drops stale model overrides when the agent owns its catalog", () => {
    const live = inventory({ engines: [{ id: "devin", enabled: true, binary_ok: true }] });
    writeDraftEngineMemory({ engine: "devin", model: "stale-model", effort: "high" });
    expect(resolveDraftEngineMemory(live)).toEqual({ engine: "devin", model: "", effort: "" });
    expect(rememberedEngineRuntime("devin", live)).toEqual({ model: "", effort: "" });
    live.engines[0].enabled = false;
    expect(resolveDraftEngineMemory(live)).toBeUndefined();
    live.engines[0].enabled = true;
    expect(resolveDraftEngineMemory(live)?.engine).toBe("devin");
  });

  it("restores the engine, model and effort last picked in the composer", () => {
    writeDraftEngineMemory({
      engine: "codex",
      model: "gpt-5-codex",
      effort: "high",
    });

    expect(resolveDraftEngineMemory(inventory())).toEqual({
      engine: "codex",
      model: "gpt-5-codex",
      effort: "high",
    });
    expect(lastEffortForEngineModel("codex", "gpt-5-codex")).toBe("high");
    expect(lastEffortForEngineModel("codex", "gpt-5")).toBeUndefined();
  });

  it("applies an explicit wuu pick before the inventory arrives", () => {
    writeDraftEngineMemory({ engine: "wuu", model: "", effort: "" });

    expect(resolveDraftEngineMemory(undefined)).toEqual({
      engine: "wuu",
      model: "",
      effort: "",
    });
  });

  it("waits for the inventory before applying an external engine", () => {
    writeDraftEngineMemory({ engine: "codex", model: "", effort: "" });

    expect(resolveDraftEngineMemory(undefined)).toBeUndefined();
  });

  it("keeps the stored preference when the engine is not currently available", () => {
    writeDraftEngineMemory({ engine: "codex", model: "", effort: "" });
    const missingBinary = inventory({
      engines: [{ id: "codex", enabled: true, binary_ok: false }],
    });

    expect(resolveDraftEngineMemory(missingBinary)).toBeUndefined();
    // A reinstall / late PATH detection recovers the pick instead of losing it.
    expect(readDraftEngineMemory()?.engine).toBe("codex");
    expect(resolveDraftEngineMemory(inventory())).toEqual({
      engine: "codex",
      model: "",
      effort: "",
    });
  });

  it("drops a model the engine no longer reports so the default applies", () => {
    writeDraftEngineMemory({
      engine: "codex",
      model: "retired-model",
      effort: "high",
    });

    expect(resolveDraftEngineMemory(inventory())).toEqual({
      engine: "codex",
      model: "",
      effort: "",
    });
  });

  it("drops an effort the remembered model does not support", () => {
    writeDraftEngineMemory({ engine: "codex", model: "gpt-5", effort: "high" });

    expect(resolveDraftEngineMemory(inventory())).toEqual({
      engine: "codex",
      model: "gpt-5",
      effort: "",
    });
  });

  it("keeps the remembered runtime when the engine reports no catalog", () => {
    writeDraftEngineMemory({
      engine: "codex",
      model: "gpt-5-codex",
      effort: "high",
    });
    const noCatalog = inventory({
      engines: [
        {
          id: "codex",
          enabled: true,
          binary_ok: true,
          models_error: "model list unavailable",
        },
      ],
    });

    expect(resolveDraftEngineMemory(noCatalog)).toEqual({
      engine: "codex",
      model: "gpt-5-codex",
      effort: "high",
    });
  });

  it("ignores an engine id the composer would not offer", () => {
    window.localStorage.setItem(
      MEMORY_KEY,
      JSON.stringify({ engine: "some-other-agent", model: "", effort: "" }),
    );

    expect(resolveDraftEngineMemory(inventory())).toBeUndefined();
  });

  it("keeps each engine's model and effort while the other one is in use", () => {
    writeDraftEngineMemory({ engine: "codex", model: "gpt-5-codex", effort: "high" });
    writeDraftEngineMemory({ engine: "claude", model: "claude-opus-5", effort: "medium" });

    expect(rememberedEngineRuntime("codex", inventory())).toEqual({
      model: "gpt-5-codex",
      effort: "high",
    });
    expect(rememberedEngineRuntime("claude", inventory())).toEqual({
      model: "claude-opus-5",
      effort: "medium",
    });
    expect(lastEffortForEngineModel("codex", "gpt-5-codex")).toBe("high");
    // The built-in engine has its own provider/model memory.
    expect(rememberedEngineRuntime("wuu", inventory())).toBeUndefined();
    expect(readDraftEngineMemory()).toEqual({
      engine: "claude",
      model: "claude-opus-5",
      effort: "medium",
    });
  });

  it("leaves the engine default in charge when its remembered model is gone", () => {
    writeDraftEngineMemory({ engine: "codex", model: "retired-model", effort: "high" });

    expect(rememberedEngineRuntime("codex", inventory())).toBeUndefined();
  });

  it("reads the single-selection payload written by an older build", () => {
    window.localStorage.setItem(
      MEMORY_KEY,
      JSON.stringify({ engine: "codex", model: "gpt-5-codex", effort: "high" }),
    );

    expect(readDraftEngineMemory()).toEqual({
      engine: "codex",
      model: "gpt-5-codex",
      effort: "high",
    });
    expect(rememberedEngineRuntime("codex", inventory())).toEqual({
      model: "gpt-5-codex",
      effort: "high",
    });
  });

  it("treats corrupted storage as no remembered engine", () => {
    window.localStorage.setItem(MEMORY_KEY, "not json");
    expect(readDraftEngineMemory()).toBeUndefined();

    window.localStorage.setItem(MEMORY_KEY, JSON.stringify({ engine: "   " }));
    expect(readDraftEngineMemory()).toBeUndefined();

    window.localStorage.setItem(MEMORY_KEY, JSON.stringify(["codex"]));
    expect(readDraftEngineMemory()).toBeUndefined();
  });

  it("clears the memory when written without an engine", () => {
    writeDraftEngineMemory({ engine: "codex", model: "", effort: "" });
    writeDraftEngineMemory({ engine: "", model: "", effort: "" });

    expect(window.localStorage.getItem(MEMORY_KEY)).toBeNull();
  });
});
