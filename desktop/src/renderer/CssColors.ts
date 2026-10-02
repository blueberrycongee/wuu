/**
 * Resolves CSS color expressions (tokens, var(), color-mix()) to hex for the
 * surfaces that paint outside the cascade: Monaco themes and the xterm
 * palette. A probe inside `scope` resolves each expression exactly as the
 * stylesheet would, so theme attributes and extension overrides apply.
 */
export function resolveCssColors<K extends string>(
  colors: Readonly<Record<K, string>>,
  scope: Element = document.documentElement,
): Record<K, string> {
  const probe = document.createElement("span");
  probe.style.display = "none";
  scope.append(probe);
  try {
    const resolved = {} as Record<K, string>;
    for (const key of Object.keys(colors) as K[]) {
      // Mixing in sRGB normalizes every color syntax (hex, rgb(), oklch()...)
      // to one serialization, which is all hexColor has to read.
      probe.style.color = `color-mix(in srgb, ${colors[key]} 100%, transparent)`;
      resolved[key] = hexColor(getComputedStyle(probe).color);
    }
    return resolved;
  } finally {
    probe.remove();
  }
}

const SRGB_COLOR = /^color\(srgb ([-\d.e]+) ([-\d.e]+) ([-\d.e]+)(?: \/ ([-\d.e]+))?\)$/;
const RGB_COLOR = /^rgba?\(([\d.]+), ([\d.]+), ([\d.]+)(?:, ([\d.]+))?\)$/;

function hexColor(computed: string): string {
  const srgb = SRGB_COLOR.exec(computed);
  const rgb = srgb ? undefined : RGB_COLOR.exec(computed);
  const match = srgb ?? rgb;
  if (!match) {
    throw new Error(`Unsupported computed color: ${computed}`);
  }
  const scale = srgb ? 255 : 1;
  const channels = [match[1], match[2], match[3]].map((value) => Number(value) * scale);
  const alpha = match[4] === undefined ? 1 : Number(match[4]);
  if (alpha < 1) {
    channels.push(alpha * 255);
  }
  return `#${channels
    .map((value) => Math.round(Math.min(255, Math.max(0, value))).toString(16).padStart(2, "0"))
    .join("")}`;
}
