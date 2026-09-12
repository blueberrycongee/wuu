import type { ReactNode } from "react";

/** These IDs also appear in saved agent identities. */
export const WUU_MASCOT_ACCESSORIES = [
  "none", "cap", "beanie", "top-hat", "sprout", "crown", "headphones",
  "scarf", "beret", "party-hat", "wizard-hat", "chef-hat", "flower", "halo",
  "bow-tie", "graduation-cap", "cowboy-hat", "propeller-cap", "mushroom-cap",
  "bunny-ears", "cat-ears", "ribbon", "necktie",
] as const;

export type WuuMascotAccessory = typeof WUU_MASCOT_ACCESSORIES[number];

function Bow(): JSX.Element {
  return <>
    <path className="wuu-mascot-fill" d="M-2 0Q-16-11-16-2Q-16 9-2 3ZM2 0Q16-11 16-2Q16 9 2 3Z" />
    <rect className="wuu-mascot-band" x="-3" y="-3" width="6" height="8" rx="2" />
  </>;
}

function Hat({ children }: { children: ReactNode }): JSX.Element {
  return <g transform="translate(0 -32)">{children}</g>;
}

function AccessoryArt({ accessory, rear }: { accessory: Exclude<WuuMascotAccessory, "none">; rear: boolean }): JSX.Element | null {
  if (rear) {
    switch (accessory) {
      case "headphones": return <path className="wuu-mascot-line wuu-mascot-headphone-band" d="M-31 5V-6C-31-44 31-44 31-6V5" />;
      case "scarf": return <path className="wuu-mascot-fill" d="M9 23Q22 28 17 43L8 40Q14 30 5 27Z" />;
      case "bunny-ears": return <Hat><path className="wuu-mascot-fill" d="M-18 8C-27-10-22-23-15-21C-8-19-9-4-8 8M8 8C9-4 8-19 15-21C22-23 27-10 18 8" /><path className="wuu-mascot-detail" d="M-17-14L-14 0M17-14L14 0" /></Hat>;
      case "cat-ears": return <Hat><path className="wuu-mascot-fill" d="M-25 12L-23-9Q-22-13-18-9L-5 5M5 5L18-9Q22-13 23-9L25 12" /><path className="wuu-mascot-detail" d="M-20-3L-13 4M20-3L13 4" /></Hat>;
      default: return null;
    }
  }
  switch (accessory) {
    case "cap": return <Hat>
      <path className="wuu-mascot-fill" d="M-21 7Q-20-10-3-12Q15-13 19 6Z" />
      <path className="wuu-mascot-detail" d="M-3-11Q4-5 3 5" />
      <path className="wuu-mascot-fill" d="M-22 6Q0 2 27 9Q26 14 13 13L-22 11Z" />
    </Hat>;
    case "beanie": return <Hat>
      <circle className="wuu-mascot-fill" cy="-14" r="4" />
      <path className="wuu-mascot-fill" d="M-21 7Q-22-11 0-12Q22-11 21 7Z" />
      <path className="wuu-mascot-detail" d="M-11-5L-12 3M0-8V2M11-5L12 3" />
      <rect className="wuu-mascot-fill" x="-23" y="4" width="46" height="8" rx="3" />
      <rect className="wuu-mascot-band" x="10" y="5" width="5" height="6" rx="1" />
    </Hat>;
    case "top-hat": return <Hat>
      <path className="wuu-mascot-fill" d="M-15 9L-17-13Q0-18 17-13L15 9Z" />
      <path className="wuu-mascot-band" d="M-16 1Q0 4 16 1L15 7H-15Z" />
      <ellipse className="wuu-mascot-fill" cy="10" rx="25" ry="4" />
    </Hat>;
    case "sprout": return <Hat>
      <path className="wuu-mascot-line" d="M0 4Q-2-7 3-13" />
      <path className="wuu-mascot-fill" d="M0-6C-13-3-18-11-16-15C-7-16-1-13 0-6ZM2-10C5-20 15-20 18-17C15-8 8-7 2-10Z" />
    </Hat>;
    case "crown": return <Hat>
      <path className="wuu-mascot-fill" d="M-22-5L-12 1L0-12L12 1L22-5L19 11Q0 15-19 11Z" />
      <path className="wuu-mascot-detail" d="M-16 7Q0 10 16 7" />
      <circle className="wuu-mascot-band" cy="2" r="2.5" />
    </Hat>;
    case "headphones": return <>
      <rect className="wuu-mascot-fill" x="-36" y="-9" width="10" height="23" rx="5" />
      <rect className="wuu-mascot-fill" x="26" y="-9" width="10" height="23" rx="5" />
      <path className="wuu-mascot-detail" d="M-30-3V8M30-3V8" />
    </>;
    case "scarf": return <>
      <path className="wuu-mascot-fill" d="M-25 18Q0 27 25 18L22 27Q0 35-22 27Z" />
      <path className="wuu-mascot-detail" d="M-18 24Q-5 29 6 27" />
      <path className="wuu-mascot-band" d="M10 22L17 20L15 29L9 30Z" />
    </>;
    case "beret": return <Hat><path className="wuu-mascot-fill" d="M-24 6C-28-3-10-15 8-13C20-12 29-5 22 3L15 10L-19 11Z" /><path className="wuu-mascot-line" d="M2-13L4-17M-19 8Q0 6 17 7" /></Hat>;
    case "party-hat": return <Hat><path className="wuu-mascot-fill" d="M-18 10L2-18L19 10Q0 15-18 10Z" /><path className="wuu-mascot-detail" d="M-9-2L11 2M-14 5L15 8" /><circle className="wuu-mascot-band" cx="2" cy="-18" r="3" /></Hat>;
    case "wizard-hat": return <Hat><path className="wuu-mascot-fill" d="M-17 8Q-6-5 0-19Q9-22 16-15L7-14L19 8Z" /><ellipse className="wuu-mascot-fill" cy="10" rx="25" ry="4" /><path className="wuu-mascot-band" d="M1-8L3-4L7-3L3-1L2 3L0-1L-4-2L0-4Z" /></Hat>;
    case "chef-hat": return <Hat><path className="wuu-mascot-fill" d="M-19 3C-32-8-21-20-10-15C-8-24 9-24 11-15C24-21 32-7 19 3V12H-19Z" /><path className="wuu-mascot-detail" d="M-18 5H18M-9-5L-7 0M9-5L7 0" /></Hat>;
    case "flower": return <g transform="translate(-23 -28) rotate(-12)"><path className="wuu-mascot-fill" d="M0-5C-8-15-15-3-6 1C-16 7-5 17 0 7C6 17 17 7 6 1C15-3 8-15 0-5Z" /><circle className="wuu-mascot-band" cy="2" r="3.5" /></g>;
    case "halo": return <Hat><ellipse className="wuu-mascot-halo" cy="-9" rx="21" ry="5" /></Hat>;
    case "bow-tie": return <g transform="translate(0 25)"><Bow /></g>;
    case "graduation-cap": return <Hat><path className="wuu-mascot-fill" d="M-15 0V10Q0 16 15 10V0M-27-2L0-14L27-2L0 9Z" /><path className="wuu-mascot-line" d="M0-2L22 2V15" /><path className="wuu-mascot-band" d="M22 12L25 19H19Z" /></Hat>;
    case "cowboy-hat": return <Hat><path className="wuu-mascot-fill" d="M-17 8L-13-10Q-10-14 0-9Q10-14 13-10L17 8Z" /><path className="wuu-mascot-band" d="M-16 1Q0 5 16 1L17 7H-17Z" /><path className="wuu-mascot-fill" d="M-29 3Q-19 13 0 8Q19 13 29 3Q27 18 0 14Q-27 18-29 3Z" /></Hat>;
    case "propeller-cap": return <Hat><path className="wuu-mascot-fill" d="M-21 10Q-21-9 0-10Q21-9 21 10Z" /><path className="wuu-mascot-detail" d="M0-9V8" /><path className="wuu-mascot-line" d="M0-10V-17" /><ellipse className="wuu-mascot-fill" cy="-18" rx="16" ry="3" /><circle className="wuu-mascot-band" cy="-18" r="2" /></Hat>;
    case "mushroom-cap": return <Hat><path className="wuu-mascot-fill" d="M-27 8Q-25-15 0-16Q25-15 27 8Q0 17-27 8Z" /><ellipse className="wuu-mascot-band" cx="-10" cy="-4" rx="5" ry="3" /><ellipse className="wuu-mascot-band" cx="11" cy="1" rx="6" ry="4" /></Hat>;
    case "ribbon": return <g transform="translate(23 -27) rotate(25) scale(.8)"><Bow /></g>;
    case "necktie": return <g transform="translate(0 24)"><path className="wuu-mascot-fill" d="M0 1L-6 14L0 19L6 14ZM-5-4H5L3 2H-3Z" /></g>;
    case "bunny-ears":
    case "cat-ears": return null;
  }
}

export function MascotAccessory({ accessory, layer, body }: {
  accessory: Exclude<WuuMascotAccessory, "none">;
  layer: "rear" | "front";
  body: { cx: number; cy: number; rx: number; ry: number };
}): JSX.Element {
  // Artwork uses a radius of 32; fit both paint planes to the actual identity,
  // including non-round agent shapes, rather than the default Wuu body.
  return <g className={`wuu-mascot-accessory wuu-mascot-accessory-${accessory}`} aria-hidden="true">
    <g transform={`translate(${body.cx} ${body.cy}) scale(${body.rx / 32} ${body.ry / 32})`}>
      <AccessoryArt accessory={accessory} rear={layer === "rear"} />
    </g>
  </g>;
}
