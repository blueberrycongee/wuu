// Shared page furniture for the manual. Pages are plain template functions.
import * as G from "../geometry.mjs";

export { G };
export const T = G.tokens;
export const C = T.color;

export const CHAPTERS = [
  ["01", "理解 Wuu", "Understanding Wuu"],
  ["02", "方向探索", "Exploration"],
  ["03", "标识", "Identity"],
  ["04", "色彩", "Colour"],
  ["05", "字体与排版", "Typography"],
  ["06", "图形语言", "Graphic language"],
  ["07", "动效", "Motion"],
  ["08", "应用", "Applications"],
  ["09", "交付", "Handoff"],
];

/** A manual page. chapter is an index into CHAPTERS or null for cover pages. */
export function page({ chapter = null, id, dark = false, cls = "", body, chrome = true }) {
  return (n) => {
    const ch = chapter == null ? null : CHAPTERS[chapter];
    const head = chrome && ch
      ? `<div class="chrome"><span>${ch[0]} · ${ch[1]}<span class="en">${ch[2]}</span></span><span>Wuu 品牌手册</span></div>`
      : "";
    const folio = chrome ? `<div class="folio">${String(n).padStart(2, "0")}</div>` : "";
    return `<section class="page ${dark ? "dark" : ""} ${cls}" id="${id}" data-chapter="${ch ? ch[0] : ""}">${head}${body}${folio}</section>`;
  };
}

export const title = (zh, en) => `<h2 class="title">${zh}${en ? `<span class="en">${en}</span>` : ""}</h2>`;

/** Ball helpers */
export const ball = (px, o = {}) => G.ballSVG({ px, ...o });
export const staticBall = (px, state, o = {}) => G.WuuBall.staticSVG({ px, state, body: o.body ?? C.brand.ink, eye: o.eye ?? C.brand.paper, size: o.size });
export const agentBall = (px, name, dark = false, state = "rest") => G.WuuBall.staticSVG({ px, state, body: C.agent[name][dark ? "dark" : "light"], eye: C.brand.ink });
export const lockup = (xh, o = {}) => G.lockupSVG({ xh, ...o });
export const wordmark = (xh, o = {}) => G.wordmarkSVG({ xh, ...o });
// The approved app icon (repository assets/), referenced rather than redrawn.
export const appIcon = (px, extra = "") => `<img src="../../assets/app-icon.png" width="${px}" height="${px}" alt="Wuu" style="display:block;${extra}">`;

export const AGENTS = Object.keys(C.agent).filter((k) => !k.startsWith("$"));

export function swatch({ hex, name, role, w = 140, h = 96, dark = false, extra = "" }) {
  return `<div style="width:${w}px"><div class="sw" style="height:${h}px;background:${hex}"></div>
    <div style="margin-top:10px" class="h4">${name}</div>
    <div class="cap mono" style="margin-top:2px">${hex}</div>${role ? `<div class="cap" style="margin-top:4px;color:${dark ? "var(--d-text-2)" : "var(--text-2)"};font-weight:400">${role}</div>` : ""}${extra}</div>`;
}


// WCAG 2 contrast, used to print real ratios next to colour pairs.
export function contrast(a, b) {
  const lum = (hx) => {
    const n = parseInt(hx.slice(1, 7), 16);
    return [n >> 16, (n >> 8) & 255, n & 255]
      .map((v) => { v /= 255; return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; })
      .reduce((s, v, i) => s + v * [0.2126, 0.7152, 0.0722][i], 0);
  };
  const x = lum(a), y = lum(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}
export const cr = (a, b) => contrast(a, b).toFixed(1) + ":1";

export const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;");

export const pngURI = (buf) => `data:image/png;base64,${buf.toString("base64")}`;
