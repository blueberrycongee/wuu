import { beforeEach, describe, expect, it } from "vitest";
import { connectionDisplayPreferences } from "./ConnectionDisplayPreferences";
import { subscriptionSources } from "./SubscriptionSources";

beforeEach(() => localStorage.clear());

describe("local connection display preferences", () => {
  it("persists duplicate aliases independently without changing connection identities", () => {
    connectionDisplayPreferences.setAlias("provider:work", "Personal");
    connectionDisplayPreferences.setAlias("engine:codex", "Personal");
    expect(connectionDisplayPreferences.label("provider:work", "work")).toBe("Personal");
    expect(connectionDisplayPreferences.label("engine:codex", "Codex")).toBe("Personal");
    connectionDisplayPreferences.setAlias("provider:work", "");
    expect(connectionDisplayPreferences.label("provider:work", "work")).toBe("work");
    expect(connectionDisplayPreferences.label("engine:codex", "Codex")).toBe("Personal");
  });

  it("orders current model IDs, appends new models, and never invents missing models", () => {
    connectionDisplayPreferences.setModelOrder("engine:codex", ["b", "gone", "a", "b"]);
    const models = [{ id: "a" }, { id: "b" }, { id: "new" }];
    expect(connectionDisplayPreferences.orderModels("engine:codex", models).map(m => m.id)).toEqual(["b", "a", "new"]);
    expect(models.map(m => m.id)).toEqual(["a", "b", "new"]);
    connectionDisplayPreferences.moveModel("engine:codex", models, "new", -1);
    expect(connectionDisplayPreferences.orderModels("engine:codex", models).map(m => m.id)).toEqual(["b", "new", "a"]);
  });

  it("keeps subscription catalog order independent of the configured model", () => {
    const source = subscriptionSources(undefined, [{ name: "work", type: "openai-codex", model: "b", models: [{ id: "a" }, { id: "b" }] }])[0];
    expect(source.models.map(model => model.id)).toEqual(["a", "b"]);
    expect(source.selectedModel).toBe("b");
    connectionDisplayPreferences.setModelOrder("provider:work", ["b", "a"]);
    const reordered = subscriptionSources(undefined, [source.provider!])[0];
    expect(reordered.models.map(model => model.id)).toEqual(["b", "a"]);
    expect(reordered.selectedModel).toBe("b");
  });

  it("keeps an active hidden model available and does not change subscription defaults", () => {
    const key = "engine:codex";
    connectionDisplayPreferences.setModelOrder(key, ["b", "a"]);
    connectionDisplayPreferences.setModelVisible(key, "a", false);
    const models = [{ id: "a", is_default: true }, { id: "b" }];
    expect(connectionDisplayPreferences.visibleModels(key, models).map(m => m.id)).toEqual(["b"]);
    expect(connectionDisplayPreferences.visibleModels(key, models, ["a"]).map(m => m.id)).toEqual(["b", "a"]);
    const source = subscriptionSources({ engines: [{ id: "codex", enabled: true, binary_ok: true, models }] }, [])[0];
    expect(source.selectedModel).toBe("a");
    expect(source.models.map(m => m.id)).toEqual(["b", "a"]);
  });
});
