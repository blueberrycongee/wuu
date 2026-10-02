// Communication templates rendered to PNG by scripts/render.cjs and shown in the manual.
// Each template returns { width, height, html } at native size, styled by manual.css.
import * as G from "./geometry.mjs";

const C = G.tokens.color;
const ball = (px, o = {}) => G.ballSVG({ px, ...o });
const lockup = (xh, o = {}) => G.lockupSVG({ xh, ...o });
const agent = (px, name, state = "rest", dark = false) => G.WuuBall.staticSVG({ px, state, body: C.agent[name][dark ? "dark" : "light"], eye: C.brand.ink });
const box = (w, h, bg, inner, extra = "") => `<div style="position:relative;width:${w}px;height:${h}px;overflow:hidden;background:${bg};${extra}">${inner}</div>`;

export function ogImage(lang = "zh") {
  const t = lang === "zh"
    ? ["在本地项目里，", "和 agent 一起把事做完。", "读代码、改文件、运行命令；每一处改动都留给你检查。"]
    : ["Work with agents", "on your own projects.", "Read code, edit files and run commands. Every change stays reviewable."];
  return { width: 1200, height: 630, html: box(1200, 630, C.brand.paper, `
    <div class="abs" style="left:72px;top:64px">${lockup(22)}</div>
    <div class="abs" style="left:72px;top:200px;width:760px;font:700 ${lang === "zh" ? 56 : 64}px/1.14 var(--brand);letter-spacing:${lang === "zh" ? 0 : -0.02}em;color:${C.brand.ink}">${t[0]}<br>${t[1]}</div>
    <div class="abs" style="left:72px;bottom:72px;width:560px;font:400 22px/1.55 var(--brand);color:${C.light["text-2"]}">${t[2]}</div>
    <div class="abs" style="right:-70px;bottom:-190px">${ball(560)}</div>`) };
}

export function readmeBanner(theme = "light") {
  const dark = theme === "dark";
  const bg = dark ? C.dark.canvas : C.brand.paper, ink = dark ? C.dark.ball : C.brand.ink, eye = dark ? C.dark["ball-eye"] : C.brand.paper;
  const sub = dark ? C.dark["text-2"] : C.light["text-2"];
  const line = dark ? C.dark["line-2"] : C.light["line-2"];
  const wuu = G.WuuBall.staticSVG({ px: 104, state: "rest", body: ink, eye });
  return { width: 1280, height: 320, html: box(1280, 320, bg, `
    <div class="abs" style="left:80px;top:96px">${G.wordmarkSVG({ xh: 56, color: ink })}</div>
    <div class="abs" style="left:82px;top:200px;font:500 22px/1.45 var(--brand);color:${sub}">Work with AI agents on your local projects.</div>
    <div class="abs" style="right:80px;bottom:74px;display:flex;align-items:flex-end;gap:14px">${wuu}${[["sky", "work"], ["peach", "work"], ["leaf", "think"]].map(([a, s]) => agent(60, a, s, dark)).join("")}</div>
    <div class="abs" style="right:64px;bottom:72px;width:420px;height:2px;border-radius:1px;background:${line}"></div>`) };
}

export function releaseCard(version = "2026.9.29") {
  const items = ["可以为会话选择执行环境", "应用缩放快捷键，以及居中的缩放提示", "重新绘制 macOS 安装窗口背景"];
  return { width: 1200, height: 675, html: box(1200, 675, C.brand.paper, `
    <div class="abs" style="left:72px;top:64px">${lockup(18)}</div>
    <div class="abs" style="left:72px;top:170px;font:500 22px/1 var(--brand);color:${C.light["text-3"]}">发布 · Release</div>
    <div class="abs" style="left:72px;top:206px;font:700 88px/1 var(--brand);letter-spacing:-.03em;color:${C.brand.ink}">${version}</div>
    <div class="abs" style="left:72px;top:360px;width:680px">${items.map((t) => `<div style="display:flex;gap:16px;align-items:baseline;padding:14px 0;border-top:1px solid ${C.light["line-2"]};font:400 22px/1.45 var(--brand);color:${C.light.text}"><span style="width:8px;height:8px;border-radius:4px;background:${C.brand.ink};flex:none;transform:translateY(-3px)"></span>${t}</div>`).join("")}</div>
    <div class="abs" style="right:96px;top:250px">${G.WuuBall.staticSVG({ px: 240, state: "done", body: C.brand.ink, eye: C.brand.paper })}</div>
    <div class="abs" style="right:96px;bottom:64px;font:400 16px var(--brand);color:${C.light["text-3"]}">示例内容</div>`) };
}

export function socialAvatar() {
  return { width: 400, height: 400, html: box(400, 400, C.brand.paper, `<div class="abs" style="left:80px;top:80px">${ball(240)}</div>`) };
}

export function dmgBackground() {
  // Finder places the app icon at (180, 250) and Applications at (540, 250), 128 px icons.
  // The app icon is already the brand ball, so the background carries only the path.
  const dots = Array.from({ length: 9 }, (_, i) => `<circle cx="${282 + i * 20}" cy="250" r="${i === 8 ? 0 : 2.5}" fill="${C.light["line-2"]}"/>`).join("");
  return { width: 720, height: 420, html: box(720, 420, C.brand.paper, `
    <svg class="abs" style="left:0;top:0" width="720" height="420" viewBox="0 0 720 420">${dots}<path d="M446 242l10 8-10 8" fill="none" stroke="${C.light["line-2"]}" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/></svg>
    <div class="abs" style="left:0;right:0;bottom:24px;text-align:center;font:500 12px var(--brand);color:${C.light["text-3"]}">Drag Wuu to Applications · 拖到“应用程序”</div>`) };
}

export const TEMPLATES = {
  "social/og-image-zh": () => ogImage("zh"),
  "social/og-image-en": () => ogImage("en"),
  "social/readme-banner-light": () => readmeBanner("light"),
  "social/readme-banner-dark": () => readmeBanner("dark"),
  "social/release-card": () => releaseCard(),
  "social/avatar": () => socialAvatar(),
  "installer/dmg-background": () => dmgBackground(),
};
