import { _layout, palette, type Animate } from "blobatar";
import { idle as idleExpression, type Expression } from "blobatar/expression";
import { Blobatar } from "blobatar/react";
import "blobatar/motion.css";
import {
  createContext,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
  type CSSProperties,
  type ReactNode,
  type SVGProps,
} from "react";
import { createPortal } from "react-dom";
import { AVATAR_HUES } from "./DefaultAvatar";
import "./styles/wuu-mascot.css";

import {
  WUU_MASCOT_ACTIVITY_PERSPECTIVES,
  WUU_MASCOT_BRAND_COLORS,
  WUU_MASCOT_DEFAULT_HUE,
  WUU_MASCOT_NAME,
  WUU_MASCOT_TRAITS,
} from "./wuu-mascot-spec";

const WUU_MASCOT_LAYOUT = _layout(WUU_MASCOT_NAME, {
  traits: WUU_MASCOT_TRAITS,
});

type MascotEyeStyle = {
  /** Shared eye width, about each eye's own centre. */
  esx: number;
  /** Shared eye height, about each eye's own centre. */
  esy: number;
  /** Shared tilt, mirrored per side (left −tilt, right +tilt). */
  tilt?: number;
  /** Extra width on the right eye only. */
  esx2?: number;
  /** Extra height on the right eye only. */
  esy2?: number;
  /** Extra tilt on the right eye only, before the per-side mirroring. */
  tilt2?: number;
};

/** Keep every activity on the approved icon's filled capsule eye vocabulary. */
export function wuuMascotExpression(eyes: Partial<MascotEyeStyle> = {}): Expression {
  return {
    ...idleExpression,
    p: {
      ...idleExpression.p,
      edx: 0,
      esx: eyes.esx ?? 1,
      esy: eyes.esy ?? 1,
      esx2: eyes.esx2 ?? 0,
      esy2: eyes.esy2 ?? 0,
      tilt: eyes.tilt ?? 0,
      tilt2: eyes.tilt2 ?? 0,
      lock: 1,
    },
  };
}

export type WuuMascotAccessory =
  | "none"
  | "cap"
  | "beanie"
  | "top-hat"
  | "sprout"
  | "crown"
  | "headphones"
  | "scarf"
  | "beret"
  | "party-hat"
  | "wizard-hat"
  | "chef-hat"
  | "flower"
  | "halo"
  | "bow-tie"
  | "graduation-cap"
  | "cowboy-hat"
  | "propeller-cap"
  | "mushroom-cap"
  | "bunny-ears"
  | "cat-ears"
  | "ribbon"
  | "necktie";

export type { WuuMascotActivity } from "./wuu-mascot-spec";
import type { WuuMascotActivity } from "./wuu-mascot-spec";

export const WUU_MASCOT_ACTIVITY_EXPRESSIONS: Readonly<
  Record<WuuMascotActivity, Expression | undefined>
> = {
  idle: wuuMascotExpression(),
  compose: wuuMascotExpression({ esy: 1.04 }),
  thinking: wuuMascotExpression({ esy: 0.94, tilt: 2 }),
  compact: wuuMascotExpression({ esy: 0.9 }),
  search: wuuMascotExpression({ esy: 1.08 }),
  edit: wuuMascotExpression({ esy: 0.97, tilt: 1 }),
  command: wuuMascotExpression({ esy: 0.94 }),
  read: wuuMascotExpression({ esy: 0.94, tilt: -1 }),
  tool: wuuMascotExpression({ esy: 1.02 }),
};

type WuuMascotRuntime = {
  provider?: string;
  providers?: readonly string[];
  model?: string;
};

const WuuMascotRuntimeContext = createContext<WuuMascotRuntime>({});

// Spread early assignments across the colour wheel, then use the remaining
// shared avatar hues before any provider colour is reused.
const PROVIDER_HUES = [
  14, 202, 96, 288, 52, 222, 150, 322, 33, 250, 182, 350,
] as const satisfies readonly (typeof AVATAR_HUES[number])[];

const MODEL_ACCESSORY_BUCKETS: readonly WuuMascotAccessory[] = [
  "sprout",
  "cap",
  "beanie",
  "crown",
  "top-hat",
  "none",
  "headphones",
  "scarf",
  "beret",
  "party-hat",
  "flower",
  "halo",
  "bow-tie",
  "wizard-hat",
  "chef-hat",
  "headphones",
  "graduation-cap",
  "cowboy-hat",
  "bunny-ears",
  "cat-ears",
  "propeller-cap",
  "mushroom-cap",
  "ribbon",
  "necktie",
  "beret",
  "party-hat",
  "flower",
  "halo",
  "bow-tie",
  "wizard-hat",
  "chef-hat",
  "headphones",
];

export function WuuMascotRuntimeProvider({
  provider,
  providers,
  model,
  children,
}: WuuMascotRuntime & { children: ReactNode }): JSX.Element {
  const value = useMemo(() => ({ provider, providers, model }), [provider, providers, model]);
  return (
    <WuuMascotRuntimeContext.Provider value={value}>
      {children}
    </WuuMascotRuntimeContext.Provider>
  );
}

function normalizedProviderIdentity(provider: string | undefined): string {
  return provider?.trim().toLocaleLowerCase() ?? "";
}

export function providerMascotHue(
  provider: string | undefined,
  providers?: readonly string[],
): number {
  const identity = normalizedProviderIdentity(provider);
  if (!identity) return WUU_MASCOT_DEFAULT_HUE;

  if (providers) {
    const identities = [...new Set(providers.map(normalizedProviderIdentity).filter(Boolean))];
    const index = identities.indexOf(identity);
    const allocationIndex = index >= 0 ? index : identities.length;
    return PROVIDER_HUES[allocationIndex % PROVIDER_HUES.length];
  }

  return PROVIDER_HUES[stableHash(identity) % PROVIDER_HUES.length];
}

export function modelMascotAccessory(model: string | undefined): WuuMascotAccessory {
  const identity = model?.trim().toLocaleLowerCase();
  if (!identity) return "none";
  return MODEL_ACCESSORY_BUCKETS[stableHash(identity) % MODEL_ACCESSORY_BUCKETS.length];
}

type WuuMascotProps = Omit<
  SVGProps<SVGSVGElement>,
  "children" | "dangerouslySetInnerHTML" | "viewBox"
> & {
  size?: number;
  provider?: string;
  model?: string;
  accessory?: WuuMascotAccessory;
  activity?: WuuMascotActivity;
  /** Override the face while retaining the mascot's authored identity. */
  expression?: Expression;
  showActivityProp?: boolean;
  followPointer?: boolean;
  /** Pin hero appearances to the app icon, independent of provider identity. */
  brand?: boolean;
  identityName?: string;
  identityHue?: number;
  identityTraits?: Readonly<Record<string, number>>;
  /** Blobatar idle-motion gate. Defaults to "always" so standalone hero
   *  mascots keep their ambient loop; multi-avatar surfaces pass "hover"
   *  so only working avatars move. */
  animate?: Animate;
};

export function WuuMascot({
  provider,
  model,
  accessory,
  activity = "idle",
  expression,
  showActivityProp = true,
  followPointer = false,
  brand = false,
  identityName = WUU_MASCOT_NAME,
  identityHue,
  identityTraits = WUU_MASCOT_TRAITS,
  animate = "always",
  style,
  ...svgProps
}: WuuMascotProps): JSX.Element {
  const runtime = useContext(WuuMascotRuntimeContext);
  const effectiveProvider = provider ?? runtime.provider;
  const effectiveModel = model ?? runtime.model;
  const hue = identityHue ?? providerMascotHue(effectiveProvider, runtime.providers);
  const colors = brand || (identityHue === undefined && !normalizedProviderIdentity(effectiveProvider))
    ? WUU_MASCOT_BRAND_COLORS
    : palette(hue);
  const selectedAccessory = accessory ?? modelMascotAccessory(effectiveModel);
  const identityTraitsSignature = Object.entries(identityTraits)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => `${key}:${value}`)
    .join("|");
  const [svg, setSVG] = useState<SVGSVGElement | null>(null);
  const [mascotLayers, setMascotLayers] = useState<{
    rear: SVGGElement;
    front: SVGGElement;
  } | null>(null);

  useLayoutEffect(() => {
    const bodyLayer =
      svg?.querySelector<SVGGElement>(".mo-bob > g:not(.mo-eyes)") ?? null;
    const eyesLayer = svg?.querySelector<SVGGElement>(".mo-eyes") ?? null;
    if (!bodyLayer || !eyesLayer) {
      setMascotLayers(null);
      return undefined;
    }

    // Accessories need two paint planes to look worn rather than pasted on.
    // The rear plane lives inside the body group so the core silhouette
    // occludes it. The front plane must render above both the body and the
    // eyes, so it is inserted as a sibling after .mo-eyes in DOM order.
    const rear = document.createElementNS("http://www.w3.org/2000/svg", "g");
    const front = document.createElementNS("http://www.w3.org/2000/svg", "g");
    rear.classList.add("wuu-mascot-layer", "wuu-mascot-layer-rear");
    front.classList.add("wuu-mascot-layer", "wuu-mascot-layer-front");
    bodyLayer.insertBefore(rear, bodyLayer.firstChild);
    eyesLayer.parentElement?.insertBefore(front, eyesLayer.nextSibling);
    setMascotLayers({ rear, front });

    return () => {
      rear.remove();
      front.remove();
    };
  // Only identity changes replace the tree. Camera and expression changes
  // update its paths in place, preserving these accessory portal targets.
  }, [identityName, identityTraitsSignature, svg]);

  useEffect(() => {
    if (!svg || !followPointer) return;

    const reducedMotion = typeof window.matchMedia === "function"
      ? window.matchMedia("(prefers-reduced-motion: reduce)")
      : null;
    let pointer: { x: number; y: number } | null = null;
    let animationFrame: number | null = null;

    const renderGaze = () => {
      animationFrame = null;
      const rect = svg.getBoundingClientRect();
      if (!pointer || reducedMotion?.matches || rect.width === 0 || rect.height === 0) {
        svg.style.setProperty("--mo-pointer-yaw", "0");
        svg.style.setProperty("--mo-pointer-pitch", "0");
        return;
      }

      const dx = pointer.x - (rect.left + rect.width / 2);
      const dy = pointer.y - (rect.top + rect.height / 2);
      const distance = Math.hypot(dx, dy);
      const reach = Math.max(80, Math.min(window.innerWidth, window.innerHeight) * 0.18);
      const strength = Math.min(1, distance / reach);
      const directionX = distance > 0 ? dx / distance : 0;
      const directionY = distance > 0 ? dy / distance : 0;

      // Pointer attention rotates the same surface as the activity camera.
      // Angles are size-independent, so small and large instances look alike.
      svg.style.setProperty(
        "--mo-pointer-yaw",
        `${(directionX * strength * 7).toFixed(2)}`,
      );
      svg.style.setProperty(
        "--mo-pointer-pitch",
        `${(-directionY * strength * 5).toFixed(2)}`,
      );
    };

    const scheduleGaze = () => {
      if (animationFrame !== null) return;
      animationFrame = window.requestAnimationFrame(renderGaze);
    };
    const handlePointerMove = (event: PointerEvent) => {
      if (event.pointerType === "touch") return;
      pointer = { x: event.clientX, y: event.clientY };
      scheduleGaze();
    };
    const resetGaze = () => {
      pointer = null;
      scheduleGaze();
    };

    window.addEventListener("pointermove", handlePointerMove, { passive: true });
    window.addEventListener("blur", resetGaze);
    document.documentElement.addEventListener("mouseleave", resetGaze);
    reducedMotion?.addEventListener("change", resetGaze);

    return () => {
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("blur", resetGaze);
      document.documentElement.removeEventListener("mouseleave", resetGaze);
      reducedMotion?.removeEventListener("change", resetGaze);
      if (animationFrame !== null) window.cancelAnimationFrame(animationFrame);
      svg.style.removeProperty("--mo-pointer-yaw");
      svg.style.removeProperty("--mo-pointer-pitch");
    };
  }, [followPointer, svg]);

  // Keep Blobatar's own hue fixed so provider changes only update inherited
  // colour variables. The SVG subtree and its seeded animation phases survive;
  // the existing fill transitions carry the mascot into the new palette.
  const mascotStyle = {
    "--mo-head": colors.head ?? WUU_MASCOT_LAYOUT.palette.head,
    "--mo-eye": colors.eye ?? WUU_MASCOT_LAYOUT.palette.eye,
    ...(followPointer ? { "--mo-look-x": 0, "--mo-look-y": 0 } : {}),
    ...style,
  } as CSSProperties;

  return (
    <>
      <Blobatar
        {...svgProps}
        ref={setSVG}
        name={identityName}
        hue={identityHue ?? WUU_MASCOT_DEFAULT_HUE}
        background={false}
        traits={identityTraits}
        perspective={WUU_MASCOT_ACTIVITY_PERSPECTIVES[activity]}
        animate={animate}
        expression={expression ?? WUU_MASCOT_ACTIVITY_EXPRESSIONS[activity]}
        focusable={false}
        pointerEvents="none"
        style={mascotStyle}
        data-wuu-mascot-provider-hue={hue}
        data-wuu-mascot-accessory={selectedAccessory}
        data-wuu-mascot-activity={activity}
        data-wuu-mascot-follows-pointer={followPointer ? "" : undefined}
      />
      {mascotLayers && selectedAccessory !== "none"
        ? <>
            {createPortal(
              <MascotAccessory
                key={`${selectedAccessory}-rear`}
                accessory={selectedAccessory}
                layer="rear"
              />,
              mascotLayers.rear,
            )}
            {createPortal(
              <MascotAccessory
                key={`${selectedAccessory}-front`}
                accessory={selectedAccessory}
                layer="front"
              />,
              mascotLayers.front,
            )}
          </>
        : null}
      {showActivityProp && mascotLayers && activity !== "idle" && activity !== "compact"
        ? createPortal(
            <MascotActivityProp key={activity} activity={activity} />,
            mascotLayers.front,
          )
        : null}
    </>
  );
}

/**
 * Where each status prop sits on the 100×100 canvas.
 *
 * Artwork is authored around the original centres (`ox`, `oy`). The process
 * row draws this canvas at 28px, where a full-size question mark or
 * magnifier lands on the eyes. Each layout recenters a slightly smaller
 * copy onto the rim so the face stays readable and the prop still names
 * the state.
 */
export const WUU_MASCOT_ACTIVITY_PROP_LAYOUT = {
  thinking: { ox: 79, oy: 24, x: 87, y: 13, s: 0.78 },
  search: { ox: 78, oy: 53, x: 86, y: 57, s: 0.88 },
  edit: { ox: 80, oy: 64, x: 89, y: 71, s: 0.8 },
  command: { ox: 81, oy: 62.5, x: 86, y: 66, s: 0.8 },
  read: { ox: 83.5, oy: 70, x: 87, y: 74, s: 0.88 },
  tool: { ox: 82, oy: 61, x: 88, y: 63.5, s: 0.84 },
} as const;

type ActivityWithProp = keyof typeof WUU_MASCOT_ACTIVITY_PROP_LAYOUT;

function activityPropPlacement(
  activity: Exclude<WuuMascotActivity, "idle">,
): string | undefined {
  if (!(activity in WUU_MASCOT_ACTIVITY_PROP_LAYOUT)) return undefined;
  const { x, y, s, ox, oy } = WUU_MASCOT_ACTIVITY_PROP_LAYOUT[activity as ActivityWithProp];
  return `translate(${x} ${y}) scale(${s}) translate(${-ox} ${-oy})`;
}

function MascotActivityProp({
  activity,
}: {
  activity: Exclude<WuuMascotActivity, "idle">;
}): JSX.Element {
  return (
    <g
      className={`wuu-mascot-activity-prop wuu-mascot-activity-prop-${activity}`}
      aria-hidden="true"
    >
      <g className="wuu-mascot-activity-motion">
        <g transform={activityPropPlacement(activity)}>
          {activity === "thinking" ? (
            <>
              <circle
                className="wuu-mascot-thinking-bubble"
                cx="79"
                cy="24"
                r="14"
              />
              <path
                className="wuu-mascot-activity-line"
                d="M 73 19 C 73.5 12 84.5 12 85.5 18.5 C 86.5 24 79 24 79 29"
              />
              <circle
                className="wuu-mascot-activity-solid"
                cx="79"
                cy="34.5"
                r="3"
              />
            </>
          ) : null}
          {activity === "search" ? (
            <>
              <circle
                className="wuu-mascot-activity-fill"
                cx="78"
                cy="53"
                r="10"
              />
              <path
                className="wuu-mascot-activity-line"
                d="M 85 61 L 95 72"
              />
            </>
          ) : null}
          {activity === "edit" ? (
            <>
              <path
                className="wuu-mascot-activity-fill"
                d="M 68 71 L 87 50 L 94 57 L 74 77 L 66 79 Z"
              />
              <path
                className="wuu-mascot-activity-line"
                d="M 84 54 L 91 61 M 68 72 L 74 77"
              />
            </>
          ) : null}
          {activity === "command" ? (
            <>
              <rect
                className="wuu-mascot-activity-fill"
                x="67"
                y="51"
                width="28"
                height="23"
                rx="6"
              />
              <path
                className="wuu-mascot-activity-line"
                d="M 73 58 L 78 62.5 L 73 67 M 82 67 L 89 67"
              />
            </>
          ) : null}
          {activity === "read" ? (
            <>
              <path
                className="wuu-mascot-activity-fill"
                d="M 72 61 Q 79.5 58 83.5 62 Q 87.5 58 95 61 L 95 80 Q 87.5 77 83.5 82 Q 79.5 77 72 80 Z"
              />
              <path
                className="wuu-mascot-activity-line"
                d="M 83.5 62 L 83.5 82"
              />
            </>
          ) : null}
          {activity === "tool" ? (
            <>
              <path
                className="wuu-mascot-activity-line"
                d="M 82 49 L 82 73 M 70 61 L 94 61 M 73.5 52.5 L 90.5 69.5 M 90.5 52.5 L 73.5 69.5"
              />
              <circle
                className="wuu-mascot-activity-fill"
                cx="82"
                cy="61"
                r="5.5"
              />
            </>
          ) : null}
        </g>
      </g>
    </g>
  );
}

function MascotAccessory({
  accessory,
  layer,
}: {
  accessory: Exclude<WuuMascotAccessory, "none">;
  layer: "rear" | "front";
}): JSX.Element {
  const { body } = WUU_MASCOT_LAYOUT;
  const top = body.cy - body.ry;
  const bottom = body.cy + body.ry;
  const centerX = body.cx;
  const lowerAnchorY = body.cy + body.ry * 0.62;

  if (layer === "rear") {
    return (
      <g
        className={`wuu-mascot-accessory wuu-mascot-accessory-rear wuu-mascot-accessory-${accessory}`}
        aria-hidden="true"
      >
        {accessory === "headphones" ? (
          <path className="wuu-mascot-line wuu-mascot-headphone-band" d={`M ${body.cx - body.rx * 0.86} ${body.cy + 5} Q ${body.cx - body.rx * 0.9} ${top - 5} ${centerX} ${top - 8} Q ${body.cx + body.rx * 0.9} ${top - 5} ${body.cx + body.rx * 0.86} ${body.cy + 5}`} />
        ) : null}
        {accessory === "scarf" ? (
          <path className="wuu-mascot-fill" d={`M ${centerX + 7} ${lowerAnchorY + 1} Q ${centerX + 23} ${bottom + 2} ${centerX + 15} ${bottom + 18} L ${centerX + 4} ${bottom + 10} Q ${centerX + 10} ${bottom - 1} ${centerX + 1} ${lowerAnchorY + 4} Z`} />
        ) : null}
        {accessory === "bunny-ears" ? (
          <>
            <path className="wuu-mascot-fill" d={`M ${centerX - 18} ${top + 7} Q ${centerX - 29} ${top - 13} ${centerX - 17} ${top - 17} Q ${centerX - 6} ${top - 12} ${centerX - 10} ${top + 8} Z`} />
            <path className="wuu-mascot-fill" d={`M ${centerX + 10} ${top + 8} Q ${centerX + 6} ${top - 12} ${centerX + 17} ${top - 17} Q ${centerX + 29} ${top - 13} ${centerX + 18} ${top + 7} Z`} />
          </>
        ) : null}
        {accessory === "cat-ears" ? (
          <>
            <path className="wuu-mascot-fill" d={`M ${centerX - 25} ${top + 10} L ${centerX - 20} ${top - 11} Q ${centerX - 8} ${top - 4} ${centerX - 4} ${top + 9} Z`} />
            <path className="wuu-mascot-fill" d={`M ${centerX + 4} ${top + 9} Q ${centerX + 8} ${top - 4} ${centerX + 20} ${top - 11} L ${centerX + 25} ${top + 10} Z`} />
          </>
        ) : null}
        {accessory === "ribbon" ? (
          <g transform={`translate(${body.cx + body.rx * 0.78} ${top})`}>
            <path className="wuu-mascot-fill" d="M -2 0 Q -15 -11 -17 1 Q -14 12 -2 4 Z" />
            <path className="wuu-mascot-fill" d="M 2 0 Q 15 -11 17 1 Q 14 12 2 4 Z" />
          </g>
        ) : null}
      </g>
    );
  }

  return (
    <g
      className={`wuu-mascot-accessory wuu-mascot-accessory-${accessory}`}
      aria-hidden="true"
    >
      {accessory === "cap" ? (
        <>
          <path className="wuu-mascot-fill" d={`M ${centerX - 22} ${top + 8} Q ${centerX - 18} ${top - 8} ${centerX + 2} ${top - 9} Q ${centerX + 20} ${top - 8} ${centerX + 23} ${top + 9} Z`} />
          <path className="wuu-mascot-fill" d={`M ${centerX - 24} ${top + 8} Q ${centerX + 1} ${top + 4} ${centerX + 29} ${top + 10} Q ${centerX + 17} ${top + 17} ${centerX - 22} ${top + 13} Z`} />
        </>
      ) : null}
      {accessory === "beanie" ? (
        <>
          <circle className="wuu-mascot-fill" cx={centerX} cy={top - 10} r="4.8" />
          <path className="wuu-mascot-fill" d={`M ${centerX - 22} ${top + 7} Q ${centerX - 19} ${top - 10} ${centerX} ${top - 11} Q ${centerX + 20} ${top - 10} ${centerX + 23} ${top + 7} Z`} />
          <rect className="wuu-mascot-fill" x={centerX - 24} y={top + 4} width="48" height="10" rx="5" />
        </>
      ) : null}
      {accessory === "top-hat" ? (
        <>
          <rect className="wuu-mascot-fill" x={centerX - 16} y={top - 14} width="32" height="27" rx="4.5" />
          <rect className="wuu-mascot-band" x={centerX - 16} y={top + 5} width="32" height="7" rx="2" />
          <rect className="wuu-mascot-fill" x={centerX - 25} y={top + 10} width="50" height="8" rx="4" />
        </>
      ) : null}
      {accessory === "sprout" ? (
        <>
          <path className="wuu-mascot-line" d={`M ${centerX} ${top + 3} Q ${centerX - 2} ${top - 6} ${centerX + 1} ${top - 13}`} />
          <path className="wuu-mascot-fill" d={`M ${centerX} ${top - 9} Q ${centerX - 13} ${top - 17} ${centerX - 16} ${top - 7} Q ${centerX - 9} ${top - 2} ${centerX} ${top - 9} Z`} />
          <path className="wuu-mascot-fill" d={`M ${centerX + 1} ${top - 11} Q ${centerX + 12} ${top - 16} ${centerX + 17} ${top - 9} Q ${centerX + 12} ${top - 3} ${centerX + 1} ${top - 11} Z`} />
        </>
      ) : null}
      {accessory === "crown" ? (
        <path className="wuu-mascot-fill" d={`M ${centerX - 23} ${top + 11} L ${centerX - 20} ${top - 7} L ${centerX - 8} ${top + 1} L ${centerX} ${top - 12} L ${centerX + 9} ${top + 1} L ${centerX + 21} ${top - 7} L ${centerX + 24} ${top + 11} Q ${centerX} ${top + 16} ${centerX - 23} ${top + 11} Z`} />
      ) : null}
      {accessory === "headphones" ? (
        <>
          <rect className="wuu-mascot-fill" x={body.cx - body.rx - 4} y={body.cy - 10} width="12" height="26" rx="6" />
          <rect className="wuu-mascot-fill" x={body.cx + body.rx - 8} y={body.cy - 10} width="12" height="26" rx="6" />
        </>
      ) : null}
      {accessory === "scarf" ? (
        <>
          <path className="wuu-mascot-fill" d={`M ${body.cx - body.rx * 0.76} ${lowerAnchorY - 6} Q ${centerX} ${lowerAnchorY + 2} ${body.cx + body.rx * 0.76} ${lowerAnchorY - 6} L ${body.cx + body.rx * 0.69} ${lowerAnchorY + 6} Q ${centerX} ${lowerAnchorY + 12} ${body.cx - body.rx * 0.69} ${lowerAnchorY + 6} Z`} />
          <circle className="wuu-mascot-band" cx={centerX + 10} cy={lowerAnchorY + 5} r="4.5" />
        </>
      ) : null}
      {accessory === "beret" ? (
        <path className="wuu-mascot-fill" d={`M ${centerX - 25} ${top + 8} Q ${centerX - 22} ${top - 5} ${centerX - 7} ${top - 10} Q ${centerX + 13} ${top - 14} ${centerX + 24} ${top + 1} Q ${centerX + 12} ${top + 12} ${centerX - 25} ${top + 8} Z`} />
      ) : null}
      {accessory === "party-hat" ? (
        <>
          <path className="wuu-mascot-fill" d={`M ${centerX - 20} ${top + 11} L ${centerX + 3} ${top - 13} L ${centerX + 20} ${top + 11} Q ${centerX} ${top + 16} ${centerX - 20} ${top + 11} Z`} />
          <circle className="wuu-mascot-fill" cx={centerX + 3} cy={top - 13} r="4" />
        </>
      ) : null}
      {accessory === "wizard-hat" ? (
        <>
          <path className="wuu-mascot-fill" d={`M ${centerX - 18} ${top + 11} Q ${centerX - 3} ${top - 3} ${centerX + 4} ${top - 16} Q ${centerX + 10} ${top - 4} ${centerX + 23} ${top + 10} Q ${centerX + 4} ${top + 15} ${centerX - 18} ${top + 11} Z`} />
          <path className="wuu-mascot-fill" d={`M ${centerX - 25} ${top + 10} Q ${centerX} ${top + 5} ${centerX + 27} ${top + 11} Q ${centerX + 8} ${top + 19} ${centerX - 25} ${top + 14} Z`} />
        </>
      ) : null}
      {accessory === "chef-hat" ? (
        <path className="wuu-mascot-fill" d={`M ${centerX - 23} ${top + 13} L ${centerX - 23} ${top + 4} Q ${centerX - 26} ${top - 5} ${centerX - 15} ${top - 7} Q ${centerX - 10} ${top - 16} ${centerX} ${top - 12} Q ${centerX + 10} ${top - 17} ${centerX + 16} ${top - 7} Q ${centerX + 27} ${top - 5} ${centerX + 23} ${top + 4} L ${centerX + 23} ${top + 13} Q ${centerX} ${top + 17} ${centerX - 23} ${top + 13} Z`} />
      ) : null}
      {accessory === "flower" ? (
        <g className="wuu-mascot-flower" transform={`translate(${body.cx - body.rx * 0.75} ${top})`}>
          <circle className="wuu-mascot-petal" cx="0" cy="-6" r="5" />
          <circle className="wuu-mascot-petal" cx="6" cy="0" r="5" />
          <circle className="wuu-mascot-petal" cx="0" cy="6" r="5" />
          <circle className="wuu-mascot-petal" cx="-6" cy="0" r="5" />
          <circle className="wuu-mascot-flower-center" r="4" />
        </g>
      ) : null}
      {accessory === "halo" ? (
        <ellipse className="wuu-mascot-halo" cx={centerX} cy={top - 8} rx="22" ry="5.5" />
      ) : null}
      {accessory === "bow-tie" ? (
        <>
          <path className="wuu-mascot-fill" d={`M ${centerX - 3} ${lowerAnchorY - 2} Q ${centerX - 13} ${lowerAnchorY - 11} ${centerX - 19} ${lowerAnchorY - 3} Q ${centerX - 16} ${lowerAnchorY + 7} ${centerX - 3} ${lowerAnchorY + 2} Z`} />
          <path className="wuu-mascot-fill" d={`M ${centerX + 3} ${lowerAnchorY - 2} Q ${centerX + 13} ${lowerAnchorY - 11} ${centerX + 19} ${lowerAnchorY - 3} Q ${centerX + 16} ${lowerAnchorY + 7} ${centerX + 3} ${lowerAnchorY + 2} Z`} />
          <circle className="wuu-mascot-band" cx={centerX} cy={lowerAnchorY} r="4.2" />
        </>
      ) : null}
      {accessory === "graduation-cap" ? (
        <>
          <path className="wuu-mascot-fill" d={`M ${centerX - 28} ${top - 1} L ${centerX} ${top - 13} L ${centerX + 28} ${top - 1} L ${centerX} ${top + 10} Z`} />
          <path className="wuu-mascot-line" d={`M ${centerX + 20} ${top + 2} L ${centerX + 22} ${top + 14}`} />
          <circle className="wuu-mascot-band" cx={centerX + 22} cy={top + 16} r="3" />
        </>
      ) : null}
      {accessory === "cowboy-hat" ? (
        <>
          <path className="wuu-mascot-fill" d={`M ${centerX - 17} ${top + 9} Q ${centerX - 14} ${top - 9} ${centerX} ${top - 8} Q ${centerX + 15} ${top - 9} ${centerX + 18} ${top + 9} Z`} />
          <path className="wuu-mascot-fill" d={`M ${centerX - 30} ${top + 8} Q ${centerX - 17} ${top + 16} ${centerX} ${top + 10} Q ${centerX + 18} ${top + 16} ${centerX + 30} ${top + 8} Q ${centerX + 16} ${top + 22} ${centerX} ${top + 15} Q ${centerX - 17} ${top + 22} ${centerX - 30} ${top + 8} Z`} />
        </>
      ) : null}
      {accessory === "propeller-cap" ? (
        <>
          <path className="wuu-mascot-fill" d={`M ${centerX - 23} ${top + 10} Q ${centerX - 18} ${top - 8} ${centerX} ${top - 9} Q ${centerX + 19} ${top - 8} ${centerX + 23} ${top + 10} Z`} />
          <path className="wuu-mascot-line" d={`M ${centerX} ${top - 9} L ${centerX} ${top - 16}`} />
          <path className="wuu-mascot-fill" d={`M ${centerX} ${top - 16} Q ${centerX - 11} ${top - 18} ${centerX - 15} ${top - 12} Q ${centerX - 8} ${top - 8} ${centerX} ${top - 16} Z`} />
          <path className="wuu-mascot-fill" d={`M ${centerX} ${top - 16} Q ${centerX + 11} ${top - 18} ${centerX + 15} ${top - 12} Q ${centerX + 8} ${top - 8} ${centerX} ${top - 16} Z`} />
        </>
      ) : null}
      {accessory === "mushroom-cap" ? (
        <path className="wuu-mascot-fill" d={`M ${centerX - 29} ${top + 12} Q ${centerX - 23} ${top - 12} ${centerX} ${top - 14} Q ${centerX + 23} ${top - 12} ${centerX + 29} ${top + 12} Q ${centerX + 11} ${top + 5} ${centerX} ${top + 12} Q ${centerX - 11} ${top + 5} ${centerX - 29} ${top + 12} Z`} />
      ) : null}
      {accessory === "ribbon" ? (
        <g transform={`translate(${body.cx + body.rx * 0.78} ${top})`}>
          <circle className="wuu-mascot-band" cy="2" r="4.5" />
        </g>
      ) : null}
      {accessory === "necktie" ? (
        <>
          <path className="wuu-mascot-line" d={`M ${centerX - 20} ${lowerAnchorY - 7} L ${centerX - 6} ${lowerAnchorY} M ${centerX + 20} ${lowerAnchorY - 7} L ${centerX + 6} ${lowerAnchorY}`} />
          <path className="wuu-mascot-fill" d={`M ${centerX} ${lowerAnchorY - 5} L ${centerX + 6} ${lowerAnchorY} L ${centerX} ${lowerAnchorY + 6} L ${centerX - 6} ${lowerAnchorY} Z`} />
          <path className="wuu-mascot-fill" d={`M ${centerX} ${lowerAnchorY + 6} L ${centerX - 7} ${bottom + 2} L ${centerX} ${bottom + 8} L ${centerX + 7} ${bottom + 2} Z`} />
        </>
      ) : null}
    </g>
  );
}

function stableHash(value: string): number {
  let hash = 0x811c9dc5;
  for (let index = 0; index < value.length; index++) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193);
  }
  return hash >>> 0;
}
