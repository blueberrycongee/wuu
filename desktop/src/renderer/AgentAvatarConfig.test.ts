import { describe, expect, it } from "vitest";
import { AGENT_AVATAR_SHAPES, agentAvatarConfig, parseAgentAvatarConfig, serializeAgentAvatarConfig } from "./AgentAvatarMark";

describe("saved avatar accessories", () => {
  it("can edit every current and retired shape without losing its color or headwear", () => {
    for (const shape of [...AGENT_AVATAR_SHAPES.map(item => item.id), "organic", "boxy", "nub", "cloud", "sun"]) {
      const config = parseAgentAvatarConfig(`mascot-v1:${shape}:headset:202`)!;
      expect(config).not.toBeNull();
      expect(AGENT_AVATAR_SHAPES.some(item => item.id === config.shape)).toBe(true);
      expect(config.accessory).toBe("headset");
      expect(config.hue).toBe(202);
      expect(parseAgentAvatarConfig(serializeAgentAvatarConfig(config))).toEqual(config);
    }
  });
  it("discards retired artwork without losing a saved shape or colour", () => {
    const config = agentAvatarConfig("mascot-v1:cloud:bandana:202");
    expect(config).toEqual({ shape: "capsule", hue: 202, accessory: "none" });
    expect(parseAgentAvatarConfig(serializeAgentAvatarConfig({ ...config, accessory: "hard-hat" })))
      .toEqual({ shape: "capsule", hue: 202, accessory: "hard-hat" });
  });

  it("still rejects malformed identity fields when the accessory is unknown", () => {
    for (const value of ["mascot-v1:cloud:crown:360", "mascot-v1:missing:crown:202", "mascot-v1:cloud::202", "mascot-v1:cloud:crown:202:extra"]) {
      expect(parseAgentAvatarConfig(value)).toBeNull();
    }
  });
});
