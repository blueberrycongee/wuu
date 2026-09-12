import type { CSSProperties } from "react";
import {
  AGENT_AVATAR_ACCESSORIES,
  AGENT_AVATAR_SHAPES,
  AgentAvatarMark,
  agentAvatarConfig,
  serializeAgentAvatarConfig,
  type AgentAvatarConfig,
} from "./AgentAvatarMark";
import { useI18n } from "./i18n";

const SHAPE_LABEL_KEYS = {
  round: "channels.avatarShapeRound",
  organic: "channels.avatarShapeOrganic",
  boxy: "channels.avatarShapeBoxy",
  nub: "channels.avatarShapeNub",
  cloud: "channels.avatarShapeCloud",
  sun: "channels.avatarShapeSun",
} as const;

const ACCESSORY_LABEL_KEYS = {
  none: "channels.avatarAccessoryNone",
  cap: "channels.avatarAccessoryCap",
  beanie: "channels.avatarAccessoryBeanie",
  "top-hat": "channels.avatarAccessoryTopHat",
  sprout: "channels.avatarAccessorySprout",
  crown: "channels.avatarAccessoryCrown",
  headphones: "channels.avatarAccessoryHeadphones",
  scarf: "channels.avatarAccessoryScarf",
  beret: "channels.avatarAccessoryBeret",
  "party-hat": "channels.avatarAccessoryPartyHat",
  "wizard-hat": "channels.avatarAccessoryWizardHat",
  "chef-hat": "channels.avatarAccessoryChefHat",
  flower: "channels.avatarAccessoryFlower",
  halo: "channels.avatarAccessoryHalo",
  "bow-tie": "channels.avatarAccessoryBowTie",
  "graduation-cap": "channels.avatarAccessoryGraduationCap",
  "cowboy-hat": "channels.avatarAccessoryCowboyHat",
  "propeller-cap": "channels.avatarAccessoryPropellerCap",
  "mushroom-cap": "channels.avatarAccessoryMushroomCap",
  "bunny-ears": "channels.avatarAccessoryBunnyEars",
  "cat-ears": "channels.avatarAccessoryCatEars",
  ribbon: "channels.avatarAccessoryRibbon",
  necktie: "channels.avatarAccessoryNecktie",
} as const;

export function AgentAvatarCreator({
  seed,
  avatarKey,
  avatarImage,
  showShapes = true,
  onChange,
}: {
  seed: string;
  avatarKey: string;
  avatarImage?: string;
  showShapes?: boolean;
  onChange: (avatarKey: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const config = agentAvatarConfig(avatarKey);

  function update(patch: Partial<AgentAvatarConfig>): void {
    onChange(serializeAgentAvatarConfig({ ...config, ...patch }));
  }

  return (
    <div className="agent-avatar-creator">
      {showShapes ? <fieldset className="channel-avatar-picker agent-avatar-shape-picker">
        <legend>{t("channels.avatarShape")}</legend>
        <div>
          {AGENT_AVATAR_SHAPES.map((shape) => {
            const nextKey = serializeAgentAvatarConfig({ ...config, shape: shape.id });
            const active = !avatarImage && config.shape === shape.id;
            return (
              <button
                className={active ? "active" : ""}
                type="button"
                key={shape.id}
                title={t(SHAPE_LABEL_KEYS[shape.id])}
                aria-label={t(SHAPE_LABEL_KEYS[shape.id])}
                aria-pressed={active}
                onClick={() => update({ shape: shape.id })}
              >
                <AgentAvatarMark seed={seed} avatarKey={nextKey} />
              </button>
            );
          })}
        </div>
      </fieldset> : null}

      <fieldset className="channel-avatar-picker agent-avatar-color-picker">
        <legend>{t("channels.avatarColor")}</legend>
        <div
          className="agent-avatar-color-control"
          style={{ "--agent-avatar-hue": config.hue } as CSSProperties}
        >
          <span className="agent-avatar-color-preview" aria-hidden="true" />
          <input
            type="range"
            min="0"
            max="359"
            value={config.hue}
            aria-label={t("channels.avatarColor")}
            onChange={(event) => update({ hue: Number(event.currentTarget.value) })}
          />
          <output>{config.hue}°</output>
        </div>
      </fieldset>

      <fieldset className="channel-avatar-picker agent-avatar-accessory-picker">
        <legend>{t("channels.avatarAccessory")}</legend>
        <div>
          {AGENT_AVATAR_ACCESSORIES.map((accessory) => {
            const nextKey = serializeAgentAvatarConfig({ ...config, accessory });
            const active = !avatarImage && config.accessory === accessory;
            return (
              <button
                className={active ? "active" : ""}
                type="button"
                key={accessory}
                title={t(ACCESSORY_LABEL_KEYS[accessory])}
                aria-label={t(ACCESSORY_LABEL_KEYS[accessory])}
                aria-pressed={active}
                onClick={() => update({ accessory })}
              >
                <AgentAvatarMark seed={seed} avatarKey={nextKey} />
              </button>
            );
          })}
        </div>
      </fieldset>
    </div>
  );
}
