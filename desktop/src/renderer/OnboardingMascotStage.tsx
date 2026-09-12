import type { CSSProperties, ReactNode } from "react";
import { ENGINE_ICON_PATHS } from "./EngineIcons";
import { WuuMascot } from "./WuuMascot";

import { ONBOARDING_PLUGIN_ORDER } from "./onboardingCatalog";

export type OnboardingPluginID = (typeof ONBOARDING_PLUGIN_ORDER)[number];

const COMPANIONS = ["wuu", "blue", "sage"] as const;

export function OnboardingMascotStage({
  pluginIDs,
  engineID,
}: {
  pluginIDs?: readonly string[];
  engineID?: string;
}): JSX.Element {
  const worn = new Set(pluginIDs ?? []);
  const split = worn.has("subagent");
  const engineMark = engineID && engineID !== "wuu" ? ENGINE_ICON_PATHS[engineID] : undefined;

  return (
    <div
      className={`onboarding-plugin-mascot-stage${split ? " is-split" : ""}`}
      data-testid="onboarding-mascot-stage"
      data-onboarding-split={split ? "" : undefined}
      data-onboarding-engine={engineID || undefined}
      aria-hidden="true"
    >
      <div className="onboarding-mascot-pack">
        {COMPANIONS.map((color, index) => (
          <div
            key={color}
            className={`onboarding-mascot-clone onboarding-mascot-clone-${index}`}
            data-onboarding-companion={color}
            hidden={index !== 0 && !split}
          >
            <WuuMascot
              className="onboarding-mascot"
              size={200}
              brand
              activity="idle"
              ambient={index === 0}
              followPointer
              equipment={index === 0 ? <CompanionEquipment worn={worn} engineMark={engineMark} engineID={engineID} /> : undefined}
              style={index === 0 ? undefined : { "--mo-head": `var(--companion-${color})`, "--mo-eye": "var(--equipment-ink)" } as CSSProperties}
            />
          </div>
        ))}
      </div>
    </div>
  );
}

function CompanionEquipment({
  worn,
  engineMark,
  engineID,
}: {
  worn: ReadonlySet<string>;
  engineMark?: string;
  engineID?: string;
}): JSX.Element {
  const hasBelt = ["todo", "automation", "memory", "dream", "note-compaction"].some((id) => worn.has(id));
  const hasPocket = worn.has("memory") || worn.has("dream");
  function capability(id: OnboardingPluginID, content: ReactNode): ReactNode {
    if (!worn.has(id)) return null;
    return (
      <g key={id} className="onboarding-equipment-module" data-onboarding-capability={id}>
        {content}
      </g>
    );
  }

  return (
    <g className="onboarding-mascot-equipment">
        {engineMark ? (
          <g className="onboarding-equipment-module" data-onboarding-engine-mark={engineID}>
            <g className="onboarding-engine-mark" transform="translate(68 18) scale(0.72)">
              <path d={engineMark} />
            </g>
          </g>
        ) : null}
        {hasBelt ? (
          <g className="onboarding-equipment-belt">
            <path className="equipment-edge" d="M14 65 Q49 84 86 64 L83 74 Q51 94 18 76 Z" />
            <path className="equipment-fabric" d="M14 63 Q49 82 86 62 L84 71 Q51 91 17 73 Z" />
            <path className="equipment-seam equipment-stitch" d="M20 72 Q48 85 78 73" />
          </g>
        ) : null}
        {capability("ask-user", <g className="equipment-headset">
          {/* The ear hook hugs the silhouette; the boom stays above the watch. */}
          <path className="equipment-edge" d="M11 47 Q7 36 18 32 L20 36 Q13 38 15 47 Z" />
          <rect className="equipment-edge" x="7" y="42" width="12" height="17" rx="6" transform="rotate(8 13 50)" />
          <rect className="equipment-shell" x="8.5" y="43.5" width="8" height="13" rx="4" transform="rotate(8 13 50)" />
          <path className="equipment-detail equipment-mic-boom" d="M14 55 Q19 62 29 58" />
          <rect className="equipment-edge" x="26" y="55" width="9" height="5" rx="2.5" transform="rotate(-12 30 57.5)" />
          <circle className="equipment-bookmark" cx="12.5" cy="47.5" r="1.5" />
          <path className="equipment-detail" d="M11.5 51 L11.5 53" />
        </g>)}
        {capability("todo", <>
          <path className="equipment-edge" d="M28 72 Q40 76 51 76 L51 84 Q38 83 26 79 Z" />
          <path className="equipment-progress-done" d="M31 76 L34 77" />
          <path className="equipment-progress-current" d="M39 78 L42 78.5" />
          <path className="equipment-progress-next" d="M47 79 L49 79" />
        </>)}
        {capability("goal", <>
          <path className="equipment-headband" d="M80 25 Q89 20 96 24 L93 28 L98 31 Q87 32 79 30 Z" />
          <path className="equipment-headband" d="M81 28 Q91 32 94 42 L88 39 L85 42 Q86 33 78 31 Z" />
          <path className="equipment-seam equipment-stitch" d="M84 26 L92 26 M83 31 Q88 34 90 38" />
          <path className="equipment-headband" d="M18 24 Q49 14 81 23 L86 33 Q50 24 14 35 Z" />
          <path className="equipment-seam equipment-stitch" d="M19 32 Q50 22 83 30" />
          <path className="equipment-bookmark" d="M46 20 L52 20 L51 28 L45 28 Z" />
          <path className="equipment-paper" d="M78 23 Q82 21 85 25 L85 30 Q82 33 78 30 Z" />
          <path className="equipment-seam" d="M81 25 L82 29" />
        </>)}
        {capability("automation", <>
          <circle className="equipment-edge" cx="22" cy="70" r="7.5" />
          <circle className="equipment-shell" cx="22" cy="69" r="6" />
          <path className="equipment-seam" d="M22 64 V65 M27 69 H26 M22 74 V73 M17 69 H18" />
          <path className="equipment-detail equipment-clock-hand" d="M22 65.5 V69 L24.5 70" />
        </>)}
        {hasPocket ? <path className="equipment-edge" d="M65 61 L82 59 Q87 59 86 65 L84 79 Q82 84 70 84 Q65 83 65 79 Z" /> : null}
        {capability("memory", <g className="equipment-notebook">
          <rect className="equipment-binding" x="66" y="55" width="15" height="20" rx="2.5" transform="rotate(-7 73 65)" />
          <path className="equipment-paper" d="M69 55 L79 54 V72 L69 74 Z" />
          <path className="equipment-seam" d="M72 59 L77 58.5 M72 62 L77 61.5" />
          <path className="equipment-bookmark" d="M74 54 L77 53.5 V59 L75.5 58 L74 59 Z" />
        </g>)}
        {hasPocket ? <>
          <path className="equipment-fabric" d="M63 66 Q75 70 86 64 L84 78 Q82 83 70 82 Q64 82 64 77 Z" />
          <path className="equipment-seam equipment-stitch" d="M68 77 Q75 80 81 77" />
        </> : null}
        {capability("dream", <>
          <g className="equipment-sort-sheet equipment-sort-sheet-back"><rect className="equipment-paper" x="77" y="58" width="7" height="10" rx="1.5" /></g>
          <g className="equipment-sort-sheet"><rect className="equipment-shell" x="77" y="59" width="7" height="10" rx="1.5" /></g>
          <path className="equipment-detail" d="M80 62 V68 Q80 71 82 70 L83 69" />
        </>)}
        {capability("note-compaction", <g className="equipment-note-press">
          {/* A belt-mounted paper press: stacked sheets held by one clasp. */}
          <path className="equipment-binding" d="M46 77 L51 77 L51 83 L46 83 Z M56 77 L61 76 L61 82 L56 83 Z" />
          <rect className="equipment-edge" x="41" y="80" width="23" height="13" rx="3" />
          <rect className="equipment-binding" x="42.5" y="81.5" width="20" height="10" rx="2" />
          <g className="equipment-folded-note">
            <rect className="equipment-shell" x="44" y="79.5" width="16" height="9" rx="1.5" />
            <rect className="equipment-paper" x="45" y="78" width="14" height="8" rx="1.5" />
            <path className="equipment-seam" d="M46 86.5 H59 M46 89 H59" />
            <path className="equipment-detail" d="M47 81 H51 M47 83 H50" />
          </g>
          <rect className="equipment-edge" x="53" y="79" width="6" height="13" rx="1.5" />
          <rect className="equipment-bookmark" x="53.5" y="82.5" width="5" height="6" rx="1" />
          <path className="equipment-detail" d="M55 85.5 H57" />
        </g>)}
    </g>
  );
}
