// Writes the brand assets under brand/assets and tokens/wuu-brand.css from tokens.json.
// Run: npm --prefix brand run build (templates such as social images are rendered by scripts/render.cjs).
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import * as G from "./geometry.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const T = G.tokens, C = T.color;
const written = [];
function out(rel, data) {
  const file = path.join(root, rel);
  mkdirSync(path.dirname(file), { recursive: true });
  writeFileSync(file, data);
  written.push(rel);
}
// Standalone files drop the inline pixel size and carry a <title> for assistive tech.
const svgFile = (s) => s.replace(/ width="[\d.]+" height="[\d.]+"/, "").replace(' role="img" aria-label="Wuu">', ' role="img"><title>Wuu</title>');

const COLOURWAYS = {
  ink: { ink: C.brand.ink, eye: C.brand.paper, mode: "two-tone" },
  paper: { ink: C.dark.ball, eye: C.dark["ball-eye"], mode: "two-tone" },
  black: { ink: "#000000", mode: "knockout" },
  white: { ink: "#FFFFFF", mode: "knockout" },
};

// Logo: symbol at each optical size, wordmark, lockups.
for (const [name, cw] of Object.entries(COLOURWAYS)) {
  for (const size of ["display", "small", "micro"]) {
    const suffix = size === "display" ? "" : `-${size}`;
    out(`assets/logo/wuu-symbol${suffix}-${name}.svg`, svgFile(G.ballSVG({ px: 100, size, body: cw.ink, eye: cw.eye, mode: cw.mode })));
  }
  out(`assets/logo/wuu-wordmark-${name}.svg`, svgFile(G.wordmarkSVG({ xh: 100, color: cw.ink })));
  out(`assets/logo/wuu-lockup-horizontal-${name}.svg`, svgFile(G.lockupSVG({ kind: "horizontal", xh: 100, ink: cw.ink, eye: cw.eye, mode: cw.mode })));
  // Lockups used at small x-heights carry the matching optical eyes (ball = 1.4 X).
  out(`assets/logo/wuu-lockup-horizontal-small-${name}.svg`, svgFile(G.lockupSVG({ kind: "horizontal", xh: 20, ink: cw.ink, eye: cw.eye, mode: cw.mode })));
  out(`assets/logo/wuu-lockup-horizontal-micro-${name}.svg`, svgFile(G.lockupSVG({ kind: "horizontal", xh: 10, ink: cw.ink, eye: cw.eye, mode: cw.mode })));
  out(`assets/logo/wuu-lockup-stacked-${name}.svg`, svgFile(G.lockupSVG({ kind: "stacked", xh: 100, ink: cw.ink, eye: cw.eye, mode: cw.mode })));
}
for (const px of [512, 256, 128, 64]) out(`assets/logo/png/wuu-symbol-ink-${px}.png`, G.ballPNG({ px }));

// App icon: three appearances, a PNG ladder, and separate layers for icon tools.
for (const variant of Object.keys(T.appIcon.variants)) out(`assets/app-icon/wuu-app-icon-${variant}.svg`, svgFile(G.appIconSVG({ variant })));
for (const px of [1024, 512, 256, 128, 64, 32, 16]) out(`assets/app-icon/png/wuu-app-icon-light-${px}.png`, G.appIconPNG({ px }));
for (const variant of ["dark", "mono"]) out(`assets/app-icon/png/wuu-app-icon-${variant}-1024.png`, G.appIconPNG({ variant, px: 1024 }));
{
  const I = T.appIcon, D = I.ballDiameter, bx = I.ballCenter[0] - D / 2, by = I.ballCenter[1] - D / 2, k = D / 100;
  const body = G.squirclePath(I.inset, I.inset, I.body, I.radius);
  const p = G.ballPaths("display", T.ball.brandPose);
  const wrap = (inner) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"><defs><clipPath id="body"><path d="${body}"/></clipPath></defs>${inner}</svg>`;
  out("assets/app-icon/layers/1-ground.svg", wrap(`<path d="${body}" fill="${I.variants.light.ground}"/>`));
  out("assets/app-icon/layers/2-ball.svg", wrap(`<g clip-path="url(#body)"><path transform="translate(${bx} ${by}) scale(${k})" d="${p.body}" fill="${I.variants.light.ball}"/></g>`));
  out("assets/app-icon/layers/3-eyes.svg", wrap(`<path transform="translate(${bx} ${by}) scale(${k})" d="${p.eyes}" fill="${I.variants.light.eye}"/>`));
}

// Favicon: micro optical size, no ground, follows the browser colour scheme.
{
  const p = G.ballPaths("micro", T.ball.brandPose);
  out("assets/favicon/favicon.svg", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><style>.b{fill:${C.light.ball}}.e{fill:${C.light["ball-eye"]}}@media (prefers-color-scheme:dark){.b{fill:${C.dark.ball}}.e{fill:${C.dark["ball-eye"]}}}</style><path class="b" d="${p.body}"/><path class="e" d="${p.eyes}"/></svg>`);
  out("assets/favicon/favicon-16.png", G.ballPNG({ px: 16 }));
  out("assets/favicon/favicon-32.png", G.ballPNG({ px: 32 }));
  out("assets/favicon/apple-touch-icon-180.png", G.appIconPNG({ px: 180 }));
}

// CSS custom properties. Brand-scoped names so they never collide with product roles.
{
  const lines = ["/* Generated from brand/tokens/tokens.json by brand/src/build-assets.mjs. Do not edit. */", ":root {"];
  const add = (k, v) => lines.push(`  --wuu-brand-${k}: ${v};`);
  add("ink", C.brand.ink); add("paper", C.brand.paper);
  for (const [k, v] of Object.entries(C.light)) add(k, v);
  for (const [a, v] of Object.entries(C.agent)) if (!a.startsWith("$")) add(`agent-${a}`, v.light);
  for (const [s, v] of Object.entries(C.status)) { add(s, v.light); add(`${s}-soft`, v.soft); }
  add("focus", C.interaction.focus.light);
  add("font-brand", T.type.family.brand); add("font-mono", T.type.family.mono);
  for (const [k, v] of Object.entries(T.type.scale)) { add(`type-${k}-size`, `${v.size}px`); add(`type-${k}-line`, v.line); add(`type-${k}-weight`, v.weight); add(`type-${k}-tracking`, `${v.tracking}em`); }
  for (const [k, v] of Object.entries(T.motion.duration)) add(`motion-${k}`, `${v}ms`);
  for (const [k, v] of Object.entries(T.motion.easing)) add(`ease-${k.replace(/[A-Z]/g, (m) => "-" + m.toLowerCase())}`, v);
  lines.push("}", "", "[data-theme=\"dark\"], .wuu-brand-dark {");
  for (const [k, v] of Object.entries(C.dark)) add(k, v);
  for (const [a, v] of Object.entries(C.agent)) if (!a.startsWith("$")) add(`agent-${a}`, v.dark);
  for (const [s, v] of Object.entries(C.status)) { add(s, v.dark); add(`${s}-soft`, v["soft-dark"]); }
  add("focus", C.interaction.focus.dark);
  lines.push("}", "");
  out("tokens/wuu-brand.css", lines.join("\n"));
}

console.log(`${written.length} files written`);
