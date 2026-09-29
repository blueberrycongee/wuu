// Three directions compared on the same touchpoints, a stress test, and the decision.
import { page, title, ball, lockup, appIcon, C, G, staticBall, agentBall, AGENTS } from "../kit.mjs";

// Direction A's icon study: the ball seated on the bottom edge. Not adopted; the app keeps
// its approved icon.
const DOME = {
  inset: 100, body: 824, radius: 185, ballDiameter: 660, ballCenter: [512, 650],
  variants: {
    light: { ground: "#F7F7F4", ball: C.brand.ink, eye: C.brand.paper, edge: "rgba(20,20,17,.08)" },
    dark: { ground: "#262523", ball: C.dark.ball, eye: C.dark["ball-eye"] },
  },
};
const icon = (px, variant = "light") => G.appIconSVG(DOME, { px, variant });

const bp = (size = "display") => G.ballPaths(size, {});
const ballG = (D, cx, cy, body, eye, size = "display") => { const p = bp(size); return `<g transform="translate(${cx - D / 2} ${cy - D / 2}) scale(${D / 100})"><path d="${p.body}" fill="${body}"/><path d="${p.eyes}" fill="${eye}"/></g>`; };
const squircle = G.squirclePath(100, 100, 824, 185);
let uid = 0;

// Direction C: a shaded, glossy ball with a round-terminal wordmark.
function softBall(px, hue = "#FF9FAA") {
  const id = `soft${uid++}`;
  return `<svg width="${px}" height="${px}" viewBox="0 0 100 100"><defs><radialGradient id="${id}g" cx="36%" cy="30%" r="75%"><stop offset="0" stop-color="#FFE3E6"/><stop offset=".45" stop-color="${hue}"/><stop offset="1" stop-color="#E86A7C"/></radialGradient><radialGradient id="${id}s"><stop offset="0" stop-color="#7a3440" stop-opacity=".28"/><stop offset="1" stop-color="#7a3440" stop-opacity="0"/></radialGradient></defs><ellipse cx="50" cy="95" rx="34" ry="5" fill="url(#${id}s)"/><circle cx="50" cy="48" r="44" fill="url(#${id}g)"/><ellipse cx="36" cy="28" rx="10" ry="6" fill="#fff" opacity=".55" transform="rotate(-25 36 28)"/><g fill="#2A1618"><rect x="47" y="30" width="9" height="20" rx="4.5" transform="rotate(6 51 40)"/><rect x="63" y="32" width="8.5" height="20" rx="4.25" transform="rotate(6 67 42)"/></g></svg>`;
}
function roundWordmark(h, color = "#3A2227") {
  const t = 22, u = 70, rr = (u * 0.8 - t) / 2, x0 = 4 * rr + t + 14;
  const bowl = (x) => `M${x + t / 2} 0V${100 - t / 2 - (u / 2 - t / 2)}A${u / 2 - t / 2} ${u / 2 - t / 2} 0 0 0 ${x + u - t / 2} ${100 - t / 2 - (u / 2 - t / 2)}V0M${x + u - t / 2} 0V${100 - t / 2}`;
  const w = `M${t / 2} 0V${100 - t / 2 - rr}A${rr} ${rr} 0 0 0 ${t / 2 + 2 * rr} ${100 - t / 2 - rr}V30M${t / 2 + 2 * rr} ${100 - t / 2 - rr}A${rr} ${rr} 0 0 0 ${t / 2 + 4 * rr} ${100 - t / 2 - rr}V0`;
  const W = x0 + 2 * u + 14;
  return `<svg height="${h}" viewBox="${-t / 2} ${-t / 2} ${W + t} ${100 + t}"><path d="${w}${bowl(x0)}${bowl(x0 + u + 14)}" fill="none" stroke="${color}" stroke-width="${t}" stroke-linecap="round" stroke-linejoin="round"/></svg>`;
}
const softLockup = (x) => `<div class="row" style="--g:${x * 0.3}px">${softBall(x * 1.6)}${roundWordmark(x * 1.1)}</div>`;
const softIcon = (px) => { const id = `si${uid++}`; return `<svg width="${px}" height="${px}" viewBox="0 0 1024 1024"><defs><linearGradient id="${id}" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#FFF4EC"/><stop offset="1" stop-color="#FFD9DE"/></linearGradient></defs><path d="${squircle}" fill="url(#${id})"/><g transform="translate(212 200) scale(6)">${softBall(100).replace(/<svg[^>]*>|<\/svg>/g, "")}</g></svg>`; };

// Direction B: the ball entering from the frame edge, on a vermilion field.
const V = "#FF4A1C";
const frameIcon = (px) => { const id = `fi${uid++}`; return `<svg width="${px}" height="${px}" viewBox="0 0 1024 1024"><defs><clipPath id="${id}"><path d="${squircle}"/></clipPath></defs><path d="${squircle}" fill="${V}"/><g clip-path="url(#${id})">${ballG(900, 720, 760, "#111111", V)}</g></svg>`; };
const frameLockup = (x) => G.lockupSVG({ xh: x, ink: "#111111", eye: "#FFFFFF" });

export const DIRS = {
  A: {
    key: "A", zh: "墨点", en: "Ink", paper: "#F7F7F4", ink: C.brand.ink, soft: "#6E6D6A", line: "#E3E2DE", card: "#FFFFFF",
    lockup: (x) => lockup(x), icon: (px) => icon(px), avatar: (px) => staticBall(px, "rest"),
    bet: "把小球当作一个排版元素：平面墨色、完整的圆、眼睛偏向右上。颜色留给其他 agent，品牌本身几乎只有墨与纸。",
    pros: ["从 16 px 到海报都是同一个图形", "放进密集界面不抢内容", "单色输出无需改稿"],
    cons: ["色彩存在感弱，需要靠构图和 agent 色补足", "如果排版平庸，整体会显得寡淡"],
    swatches: ["#F7F7F4", "#E8E8E5", "#D7D6D3", "#908F8C", "#4D4C4A", "#141411", "|", ...AGENTS.map((a) => C.agent[a].light)],
    hero: () => `<div class="abs" style="left:28px;top:24px">${lockup(10)}</div>
      <div class="abs" style="left:28px;bottom:30px;font:600 30px/1.18 var(--brand);letter-spacing:-.01em;color:${C.brand.ink}">在本地项目里，<br>和 agent 一起把事做完。</div>
      <div class="abs" style="right:44px;top:44px">${ball(132)}</div>`,
    heroBg: "#F7F7F4",
  },
  B: {
    key: "B", zh: "出框", en: "Frame", paper: "#FFFFFF", ink: "#111111", soft: "#666", line: "#E6E6E6", card: "#F4F4F4",
    lockup: frameLockup, icon: frameIcon, avatar: (px) => G.ballSVG({ px, body: "#111111", eye: "#FFFFFF" }),
    bet: "让小球从画面边缘探进来，尺度夸张，配一块高饱和的朱红色场。识别靠冲击力和裁切构图。",
    pros: ["海报、首屏和社交图的冲击力最强", "朱红色场在程序坞和信息流中醒目"],
    cons: ["缩小后裁掉了眼睛，16–32 px 只剩色块", "头像必须是完整的球，裁切无法进入产品", "朱红与错误状态色同一色相，会误读"],
    swatches: [V, "#111111", "#FFFFFF", "#F4F4F4", "#8A8A8A"],
    hero: () => `<div class="abs" style="left:28px;top:24px">${G.lockupSVG({ xh: 10, ink: "#111", eye: V })}</div>
      <svg class="abs" style="inset:0" width="100%" height="100%" viewBox="0 0 468 300" preserveAspectRatio="xMidYMid slice">${ballG(400, 420, 330, "#111111", V)}</svg>
      <div class="abs" style="left:28px;bottom:28px;font:800 34px/1.02 var(--brand);letter-spacing:-.02em;color:#111">和 agent<br>一起做完。</div>`,
    heroBg: V,
  },
  C: {
    key: "C", zh: "软物", en: "Soft", paper: "#FBF4EE", ink: "#3A2227", soft: "#8A6C70", line: "#EEDFD6", card: "#FFFFFF",
    lockup: softLockup, icon: softIcon, avatar: (px) => softBall(px),
    bet: "把小球做成一个有质感的软玩具：粉色、高光、投影，圆头字标，渐变背景。识别靠角色本身的可爱。",
    pros: ["第一眼最亲切", "适合贴纸、周边和轻松的社交内容"],
    cons: ["渐变与高光在 16 px 糊成一团，无法单色输出", "粉色选中态与错误提示的浅红底相混", "玩具感与“检查代码改动”的场景不符，容易过时"],
    swatches: ["#FBF4EE", "#FFE3E6", "#FF9FAA", "#E86A7C", "#D9CCF5", "#3A2227"],
    hero: () => `<div class="abs" style="left:28px;top:24px">${softLockup(9)}</div>
      <div class="abs" style="right:36px;top:40px">${softBall(170)}</div>
      <div class="abs" style="left:28px;bottom:30px;font:700 28px/1.2 var(--brand);color:#3A2227">嗨，我是 Wuu。<br>一起写点代码吧！</div>`,
    heroBg: "linear-gradient(160deg,#FFF1EA,#FFE0E4 60%,#EDE3FA)",
  },
};

function board(d) {
  const sel = d.key === "B" ? "#111111" : d.key === "C" ? "#FFE3E6" : "#E8E8E5";
  const selInk = d.key === "B" ? "#fff" : d.ink;
  const send = d.key === "A" ? d.ink : d.key === "B" ? V : "#FF8595";
  const sw = d.swatches.map((c) => c === "|" ? `<div style="width:12px"></div>` : `<div class="sw" style="width:52px;height:52px;background:${c}"></div>`).join("");
  return `<div style="width:952px;display:grid;grid-template-columns:460px 476px;grid-template-rows:208px 312px 96px;gap:16px;color:${d.ink}">
    <div class="plate" style="background:${d.paper};box-shadow:inset 0 0 0 1px ${d.line};display:flex;align-items:center;justify-content:center">${d.lockup(38)}<span class="cap abs" style="left:16px;top:12px">组合标识</span></div>
    <div class="plate" style="background:${d.paper};box-shadow:inset 0 0 0 1px ${d.line};display:flex;align-items:center;justify-content:center;gap:24px">${d.icon(150)}${d.icon(64)}${d.icon(32)}${d.icon(16)}<span class="cap abs" style="left:16px;top:12px">应用图标 150 / 64 / 32 / 16</span></div>
    <div class="plate" style="background:${d.heroBg};overflow:hidden;box-shadow:inset 0 0 0 1px ${d.line}">${d.hero()}</div>
    <div class="plate" style="background:${d.card};box-shadow:inset 0 0 0 1px ${d.line};overflow:hidden;display:grid;grid-template-columns:150px 1fr;font:400 12.5px/1.55 var(--brand)">
      <div style="background:${d.paper};border-right:1px solid ${d.line};padding:16px 10px">
        <div style="margin:2px 6px 16px">${d.key === "C" ? softLockup(7) : d.lockup(7)}</div>
        <div style="padding:5px 8px">新对话</div><div style="padding:5px 8px">搜索会话</div>
        <div style="padding:12px 8px 4px;color:${d.soft};font-size:11px">工作区</div>
        <div style="padding:5px 8px;border-radius:7px;background:${sel};color:${selInk}">整理登录流程</div><div style="padding:5px 8px">修复导航重复</div>
      </div>
      <div style="padding:20px 18px;display:flex;flex-direction:column;justify-content:space-between">
        <div class="row" style="--g:10px;--a:flex-start">${d.avatar(22)}<div><div style="font-size:11px;color:${d.soft}">已编辑 3 个文件 · 42 秒</div><div style="margin-top:4px">登录超时是因为 token 刷新早于写入。已调整顺序，并补上一次重试。</div>
          <div style="margin-top:10px;border:1px solid ${d.line};border-radius:7px;padding:6px 10px;font-size:11px"><span style="color:#057D38">+12</span> <span style="color:#C63732">−4</span> <span class="mono" style="color:${d.soft}">src/auth/session.ts</span></div></div></div>
        <div style="border:1px solid ${d.line};border-radius:14px;padding:10px 12px;display:flex;justify-content:space-between;align-items:center;color:${d.soft}">描述要做的事<span style="width:22px;height:22px;border-radius:50%;background:${send};color:#fff;display:grid;place-items:center;font-size:11px">↑</span></div>
      </div>
    </div>
    <div class="plate" style="grid-column:span 2;background:${d.paper};box-shadow:inset 0 0 0 1px ${d.line};display:flex;align-items:center;gap:6px;padding:0 20px"><span class="cap" style="width:70px">色彩</span>${sw}</div>
  </div>`;
}

const dirPage = (k) => {
  const d = DIRS[k];
  return page({
    chapter: 1, id: `direction-${k.toLowerCase()}`,
    body: `
    <div class="grid">
      <div class="span-3">
        <div class="label">方向 ${k}</div>
        <div style="font:600 40px/1.15 var(--brand);margin-top:10px">${d.zh}<span style="font-weight:500;color:var(--text-3);font-size:24px;margin-left:12px">${d.en}</span></div>
        <p class="body" style="margin-top:20px">${d.bet}</p>
        <div class="label" style="margin-top:36px">成立的地方</div>
        <div class="rule-list" style="margin-top:10px;--rl:1fr">${d.pros.map((t) => `<div class="body">${t}</div>`).join("")}</div>
        <div class="label" style="margin-top:28px">问题</div>
        <div class="rule-list" style="margin-top:10px;--rl:1fr">${d.cons.map((t) => `<div class="body">${t}</div>`).join("")}</div>
      </div>
      <div class="span-8 start-5">${board(d)}</div>
    </div>`,
  });
};

export const overview = page({
  chapter: 1, id: "directions",
  body: `
  <div class="grid">
    <div class="span-4">${title("三个方向", "Three directions, one question")}
      <p class="lead">三个方向回答同一个问题：小球在品牌里是什么？它们的差别在材料、尺度和颜色的职责，而不只是色板。</p>
      <p class="body" style="margin-top:24px">每个方向都放进同一组触点：组合标识、应用图标的四个尺寸、官网首屏、产品侧栏与会话、色彩。之后用同一组压力测试比较。</p>
    </div>
    <div class="span-7 start-6 stack" style="--s:22px">
      ${["A", "B", "C"].map((k) => { const d = DIRS[k]; return `
      <div class="row" style="--g:28px;--a:center;padding-bottom:22px;border-bottom:1px solid var(--line-1)">
        <div class="plate center" style="width:230px;height:150px;background:${d.paper};box-shadow:inset 0 0 0 1px ${d.line}">${d.lockup(24)}</div>
        <div class="center" style="width:110px">${d.icon(96)}</div>
        <div style="flex:1"><div class="row" style="--g:10px;--a:baseline"><span class="h3">${k} · ${d.zh}</span><span class="cap">${d.en}</span></div><p class="body" style="margin-top:8px">${d.bet}</p></div>
      </div>`; }).join("")}
    </div>
  </div>`,
});

// Stress tests: identical conditions for all three.
function test16(d) {
  const tab = (bg, fg) => `<div style="display:flex;align-items:center;gap:6px;background:${bg};color:${fg};border-radius:7px;padding:6px 9px;font:500 11px var(--brand);width:128px">${d.key === "A" ? ball(16) : d.key === "B" ? frameIcon(16) : softBall(16)}<span>Wuu 文档</span></div>`;
  return `<div class="stack" style="--s:6px">${tab("#FFFFFF", "#333")}${tab("#2B2B2A", "#ddd")}</div>`;
}
function testMono(d) {
  if (d.key === "A") return G.lockupSVG({ xh: 16, ink: "#000", mode: "knockout" });
  if (d.key === "B") return `<div class="row" style="--g:8px">${G.lockupSVG({ xh: 16, ink: "#000", mode: "knockout" })}<span class="cap">色场消失，<br>裁切失去依据</span></div>`;
  return `<div class="row" style="--g:8px;filter:grayscale(1) contrast(1.4)">${softLockup(14)}</div>`;
}
function testDark(d) {
  const av = d.key === "A" ? G.ballSVG({ px: 26, body: C.dark.ball, eye: C.dark["ball-eye"] }) : d.key === "B" ? G.ballSVG({ px: 26, body: "#111", eye: "#fff" }) : softBall(26);
  return `<div style="background:#1C1C1A;border-radius:8px;padding:12px;display:flex;gap:10px;align-items:center;color:#ddd;font:12px var(--brand);width:190px">${av}<span>正在读取 4 个文件</span></div>`;
}
function testStatus(d) {
  const brandSel = d.key === "A" ? "#E8E8E5" : d.key === "B" ? V : "#FFE3E6";
  const fg = d.key === "B" ? "#fff" : "#141411";
  return `<div class="stack" style="--s:6px;width:190px;font:12px var(--brand)"><div style="background:${brandSel};color:${fg};border-radius:6px;padding:6px 9px">当前会话（选中）</div><div style="background:#FFEAE7;color:#A42A26;border-radius:6px;padding:6px 9px">命令失败 · 退出码 1</div></div>`;
}
function testCrowd(d) {
  if (d.key === "A") return `<div class="row" style="--g:6px">${staticBall(30, "rest")}${["sky", "peach", "leaf", "iris"].map((a) => agentBall(24, a)).join("")}</div>`;
  if (d.key === "B") return `<div class="row" style="--g:6px">${G.ballSVG({ px: 30, body: "#111", eye: "#fff" })}${[V, "#FF7A52", "#E23A12", "#FF9B7A"].map((c) => G.ballSVG({ px: 24, body: c, eye: "#111" })).join("")}<span class="cap" style="margin-left:4px">只有一种<br>颜色可分</span></div>`;
  return `<div class="row" style="--g:6px">${softBall(30)}${["#9FD3FF", "#FFC08F", "#B7E3A0", "#CDB9FF"].map((h) => softBall(24, h)).join("")}</div>`;
}

const verdict = (v, t) => `<div class="verdict ${v}" style="margin-top:10px">${v === "yes" ? "✓" : v === "no" ? "✕" : "△"} <span style="font-weight:500;color:var(--text-2)">${t}</span></div>`;

export const stress = page({
  chapter: 1, id: "stress-test",
  body: `
  <div class="grid">
    <div class="span-3">${title("压力测试", "Stress test")}
      <p class="lead">漂亮的封面不能说明问题。五项测试都来自真实用途：浏览器标签、单色输出、暗色界面、与状态色并置、多个 agent 同屏。</p>
    </div>
    <div class="span-9 start-4">
      <table class="spec" style="table-layout:fixed">
        <thead><tr><th style="width:150px">测试</th><th>A · 墨点</th><th>B · 出框</th><th>C · 软物</th></tr></thead>
        <tbody>
        ${[
          ["16 px 标签页图标", test16, [["yes", "眼睛仍可辨认"], ["no", "只剩色块"], ["no", "渐变糊成一团"]]],
          ["单色输出", testMono, [["yes", "镂空版直接可用"], ["mid", "可用，但失去色场"], ["no", "需要另画一套"]]],
          ["暗色界面头像", testDark, [["yes", "反白后对比清楚"], ["mid", "墨色球在暗底上消失"], ["mid", "可读，但像贴纸"]]],
          ["与错误状态并置", testStatus, [["yes", "选中与错误互不混淆"], ["no", "朱红选中像报错"], ["no", "浅粉与错误浅红相近"]]],
          ["多个 agent 同屏", testCrowd, [["yes", "Wuu 墨色，其他 agent 取色"], ["no", "色相单一，难区分"], ["mid", "可区分，但画面嘈杂"]]],
        ].map(([name, fn, v]) => `<tr><td style="padding:18px 12px 18px 0"><span class="h4">${name}</span></td>${["A", "B", "C"].map((k, i) => `<td style="padding:18px 12px 18px 0"><div style="min-height:62px;display:flex;align-items:center">${fn(DIRS[k])}</div>${verdict(v[i][0], v[i][1])}</td>`).join("")}</tr>`).join("")}
        </tbody>
      </table>
    </div>
  </div>`,
});

export const decision = page({
  chapter: 1, id: "decision",
  body: `
  <div class="grid">
    <div class="span-4">${title("选择 A，<br>借用 B 的一条规则", "Direction A, with one rule from B")}
      <p class="lead">墨点在所有测试中都成立，缺点是色彩弱。我们不给它加一个品牌强调色，而是把颜色交给产品里本来就存在的东西：其他 agent。</p>
    </div>
    <div class="span-7 start-6">
      <div class="rule-list" style="--rl:150px 1fr">
        <div><span class="h4">保留自 A</span><span class="body">平面墨色的完整小球；偏向右上的眼睛；纸与墨的中性色；自绘的 “wuu” 字标。</span></div>
        <div><span class="h4">借用自 B</span><span class="body">大尺寸裁切构图：只用于封面、官网首屏、社交图等大幅面，且两只眼睛必须完整留在画面内。<strong>头像、标识和小尺寸永远用完整的球。</strong></span></div>
        <div><span class="h4">放弃 B 的</span><span class="body">朱红色场。它和错误状态同色相，放进产品就会误读。</span></div>
        <div><span class="h4">放弃 C 的</span><span class="body">渐变、高光、投影和圆头字标。它们无法缩小、无法单色，也不适合审查代码的场景。</span></div>
        <div><span class="h4">应用图标</span><span class="body">保留现有的已批准图标，不重画。方向 A 中“小球坐在底边”的图标方案经评审未采用。</span></div>
        <div><span class="h4">新增</span><span class="body">agent 色：七个明度相同的浅色，只用在其他 agent 的球身和插画里。Wuu 自己永远是墨色。</span></div>
      </div>
    </div>
  </div>
  <div class="abs" style="left:80px;right:80px;bottom:96px">
    <div class="label" style="margin-bottom:14px">体系一览</div>
    <div style="display:grid;grid-template-columns:400px 400px 280px 1fr;gap:16px;height:210px">
      <div class="plate center">${lockup(34)}</div>
      <div class="plate ink center">${lockup(34, { ink: C.dark.ball, eye: C.dark["ball-eye"] })}</div>
      <div class="plate two center">${appIcon(150)}</div>
      <div class="plate center" style="gap:10px">${staticBall(52, "rest")}${["sky", "peach", "leaf", "iris", "butter"].map((a) => agentBall(38, a)).join("")}</div>
    </div>
  </div>`,
});

export default [overview, dirPage("A"), dirPage("B"), dirPage("C"), stress, decision];
