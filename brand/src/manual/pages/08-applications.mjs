// Applications: app icon, product UI (light/dark), website, docs, social, installer.
import { page, title, ball, lockup, icon, C, G, T, staticBall, agentBall, pngURI } from "../kit.mjs";
import { iconArtwork } from "../../../../desktop/src/shared/iconArtwork.ts";
import { ogImage, readmeBanner, releaseCard, socialAvatar, dmgBackground } from "../../templates.mjs";

const ico = (name, px = 16, color = "currentColor") => `<svg width="${px}" height="${px}" viewBox="0 0 24 24" fill="none" stroke="${color}" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" style="color:${color};flex:none">${iconArtwork[name].map(([tag, a]) => `<${tag} ${Object.entries(a).map(([k, v]) => `${k}="${v}"`).join(" ")}/>`).join("")}</svg>`;
const scaled = (w, h, s, inner) => `<div style="width:${w * s}px;height:${h * s}px;overflow:hidden;border-radius:12px;box-shadow:0 0 0 1px rgba(128,128,120,.28)"><div style="width:${w}px;height:${h}px;transform:scale(${s});transform-origin:0 0">${inner}</div></div>`;
const tpl = (t, s, extra = "") => `<div style="width:${t.width * s}px;height:${t.height * s}px;overflow:hidden;border-radius:10px;box-shadow:0 0 0 1px var(--line-2);${extra}"><div style="width:${t.width}px;height:${t.height}px;transform:scale(${s});transform-origin:0 0">${t.html}</div></div>`;

// ---------------------------------------------------------------------------
export const appIcon = page({
  chapter: 7, id: "app-icon",
  body: `
  <div class="grid">
    <div class="span-4">${title("应用图标", "App icon")}
      <p class="lead">一只墨色的小球坐在图标底边上，只露出上半身和眼睛。它在程序坞里是一个穹顶形，和圆形、方形的图标都不一样。</p>
      <table class="spec" style="margin-top:32px">
        <thead><tr><th>参数</th><th>值（1024 网格）</th></tr></thead>
        <tbody>
          <tr><td>图标主体</td><td class="num">${T.appIcon.body} × ${T.appIcon.body}，内缩 ${T.appIcon.inset}，连续圆角 ${T.appIcon.radius}</td></tr>
          <tr><td>小球</td><td class="num">直径 ${T.appIcon.ballDiameter}，中心 (${T.appIcon.ballCenter.join(", ")})，被底边裁去 ${Math.round(T.appIcon.ballCenter[1] + T.appIcon.ballDiameter / 2 - T.appIcon.inset - T.appIcon.body)}</td></tr>
          <tr><td>眼睛</td><td>品牌姿态；按渲染尺寸选择光学版本</td></tr>
          <tr><td>变体</td><td>亮色（默认）· 暗色 · 单色</td></tr>
        </tbody>
      </table>
      <p class="body" style="margin-top:18px">分层源文件（底板、小球、眼睛）在 <span class="mono">brand/assets/app-icon/</span>，可以导入 Icon Composer 等工具。替换产品里的图标需要另行评审。</p>
    </div>
    <div class="span-7 start-6">
      <div class="row" style="--g:24px;--a:stretch">
        <div class="plate center" style="padding:24px">${icon(270, "light", { grid: true })}</div>
        <div class="stack" style="--s:24px">
          <div class="plate center" style="padding:20px 24px;gap:20px">${icon(104)}${icon(104, "dark")}${icon(104, "mono")}</div>
          <div class="plate" style="padding:20px 24px;display:flex;gap:22px;align-items:flex-end">${[128, 64, 48, 32, 16].map((px) => `<div style="text-align:center"><img src="${pngURI(G.appIconPNG({ px: px * 2 }))}" width="${px}" height="${px}"><div class="cap num" style="margin-top:8px">${px}</div></div>`).join("")}</div>
        </div>
      </div>
      <div class="row" style="--g:24px;margin-top:24px">
        ${[["linear-gradient(135deg,#dfe6ee,#c7d1dc)", "light"], ["linear-gradient(135deg,#1c2330,#2b2a33)", "dark"]].map(([bg, v]) => `<div style="flex:1;height:170px;border-radius:14px;background:${bg};position:relative;overflow:hidden">
          <div class="abs" style="left:50%;bottom:16px;transform:translateX(-50%);display:flex;gap:14px;padding:10px 14px;border-radius:22px;background:${v === "light" ? "rgba(255,255,255,.45)" : "rgba(30,30,30,.45)"};backdrop-filter:blur(8px);box-shadow:inset 0 0 0 1px rgba(255,255,255,.25)">
            ${["#3478F6", "#F2F2F2", "icon", "#1E1E1E", "#34C759"].map((c) => c === "icon" ? `<div>${icon(64, v)}</div>` : `<div style="width:64px;height:64px;padding:6px"><div style="width:52px;height:52px;border-radius:12px;background:${c}"></div></div>`).join("")}
          </div></div>`).join("")}
      </div>
      <div class="cap" style="margin-top:8px">程序坞中的亮色与暗色外观；两侧为占位图标</div>
    </div>
  </div>`,
});

// ---------------------------------------------------------------------------
// A proposal window: sidebar, conversation, workspace panel. theme: light | dark
function appWindow(theme = "light", mode = "session") {
  const d = theme === "dark", P = d ? C.dark : C.light;
  const ink = d ? C.dark.ball : C.brand.ink, eye = d ? C.dark["ball-eye"] : C.brand.paper;
  const wuu = (px, state) => G.WuuBall.staticSVG({ px, state, body: ink, eye });
  const ag = (px, a, state) => G.WuuBall.staticSVG({ px, state, body: C.agent[a][d ? "dark" : "light"], eye: C.brand.ink });
  const S = C.status;
  const txt = (c = P.text, w = 400, sz = 13.5) => `color:${c};font:${w} ${sz}px/1.5 var(--brand)`;
  const row = (label, sel = false, extra = "") => `<div style="display:flex;align-items:center;gap:8px;padding:6px 10px;border-radius:8px;${sel ? `background:${P["surface-3"]};` : ""}${txt(sel ? P.text : P["text-2"], sel ? 500 : 400)}">${label}${extra}</div>`;
  const sidebar = `<div style="background:${P.canvas};border-right:1px solid ${P["line-1"]};padding:14px 12px;display:flex;flex-direction:column">
      <div style="display:flex;gap:7px;padding:2px 6px 18px">${["#FF5F57", "#FEBC2E", "#28C840"].map((c) => `<span style="width:12px;height:12px;border-radius:6px;background:${c}"></span>`).join("")}</div>
      <div style="padding:0 10px 18px">${lockup(10, { ink, eye })}</div>
      ${row(`${ico("MessageSquarePlus", 16, P["text-2"])}新对话`)}${row(`${ico("Search", 16, P["text-2"])}搜索会话`)}
      <div style="${txt(P["text-3"], 500, 12)};padding:18px 10px 6px">工作区 · wuu</div>
      ${row("整理登录流程", mode === "session", `<span style="margin-left:auto">${wuu(14, "work")}</span>`)}${row("修复导航重复")}${row("升级测试依赖")}${row("检查长标题与运行状态")}
      <div style="margin-top:auto">${row(`${ico("Settings", 16, P["text-2"])}设置`)}</div></div>`;
  const conv = mode === "session" ? `
      <div style="padding:0 40px;${txt(P.text, 600, 14)};height:48px;display:flex;align-items:center;border-bottom:1px solid ${P["line-1"]}">整理登录流程</div>
      <div style="padding:26px 40px 0;flex:1;overflow:hidden">
        <div style="margin-left:auto;width:fit-content;max-width:420px;background:${P["surface-2"]};border-radius:16px;padding:10px 14px;${txt()}">登录偶尔超时，帮我找原因并修好，跑一遍测试。</div>
        <div style="display:flex;gap:12px;margin-top:26px">${wuu(26, "work")}<div style="flex:1">
          <div style="${txt(P["text-3"], 400, 12.5)}">正在处理 · 1 分 12 秒</div>
          <div style="${txt()};margin-top:6px;line-height:1.7">超时来自 token 刷新：新 token 在写入存储之前就被读取了。我调整了写入顺序，并在读取失败时重试一次。</div>
          <div style="margin-top:14px;border:1px solid ${P["line-1"]};border-radius:12px;overflow:hidden">
            ${[["FileText", "读取 src/auth/session.ts", "text-3"], ["Pencil", "修改 2 个文件 · +12 −4", "text-2"], ["Terminal", "npm test", "text-2"]].map(([n, t, c], i) => `<div style="display:flex;align-items:center;gap:10px;padding:9px 14px;${i ? `border-top:1px solid ${P["line-1"]};` : ""}${txt(P[c], 400, 13)}">${ico(n, 16, P["text-3"])}<span class="mono" style="font-size:12.5px">${t}</span>${i === 2 ? `<span style="margin-left:auto;display:flex;gap:6px;align-items:center;${txt(S.danger[d ? "dark" : "light"], 600, 12.5)}">${ico("CircleAlert", 14, S.danger[d ? "dark" : "light"])}2 个失败</span>` : ""}</div>`).join("")}
          </div>
          <div style="display:flex;gap:10px;margin-top:16px;align-items:center">${ag(20, "sky", "work")}<span style="${txt(P["text-2"], 400, 13)}">子 agent · 检查类型</span><span style="${txt(P["text-3"], 400, 12.5)}">进行中</span></div>
          <div style="display:flex;gap:10px;margin-top:8px;align-items:center">${ag(20, "peach", "failed")}<span style="${txt(P["text-2"], 400, 13)}">子 agent · 修复失败的测试</span><span style="${txt(P["text-3"], 400, 12.5)}">等待重跑</span></div>
        </div></div></div>
      <div style="margin:0 32px 24px;border:1px solid ${P["line-2"]};background:${P.surface};border-radius:18px;padding:14px 16px;display:flex;flex-direction:column;gap:12px">
        <span style="${txt(P["text-4"])}">描述要做的事</span>
        <div style="display:flex;align-items:center;gap:10px">${ico("Plus", 18, P["text-3"])}<span style="${txt(P["text-3"], 500, 12.5)};margin-left:auto">Wuu · 示例模型</span><span style="width:28px;height:28px;border-radius:14px;background:${P.text};display:grid;place-items:center">${ico("ArrowUp", 16, P.canvas)}</span></div></div>`
    : `<div style="flex:1;display:flex;flex-direction:column;justify-content:center;padding:0 120px">
        <div style="display:flex;gap:18px;align-items:center">${wuu(56, "rest")}<div style="${txt(P.text, 700, 26)};line-height:1.3">晚上好，今天还想处理什么？</div></div>
        <div style="margin-top:28px;border:1px solid ${P["line-1"]};border-radius:14px;padding:18px 20px;display:flex;gap:40px">
          ${[["会话", "128"], ["Token", "114.1M"], ["活跃天数", "250"]].map(([k, v]) => `<div><div style="${txt(P["text-3"], 400, 12.5)}">${k}</div><div class="num" style="${txt(P.text, 500, 22)}">${v}</div></div>`).join("")}
          <div style="margin-left:auto;display:grid;grid-template-columns:repeat(18,9px);gap:3px">${Array.from({ length: 126 }, (_, i) => { const v = (Math.sin(i * 1.7) + Math.cos(i * 0.9)) * 0.5 + 0.5; return `<span style="width:9px;height:9px;border-radius:2px;background:${v > 0.75 ? P.text : v > 0.5 ? P["text-3"] : v > 0.3 ? P["line-2"] : P["surface-3"]}"></span>`; }).join("")}</div>
        </div></div>
      <div style="margin:0 120px 32px;border:1px solid ${P["line-2"]};background:${P.surface};border-radius:18px;padding:14px 16px;display:flex;justify-content:space-between;align-items:center"><span style="${txt(P["text-4"])}">描述要做的事</span><span style="width:28px;height:28px;border-radius:14px;background:${P.text};display:grid;place-items:center">${ico("ArrowUp", 16, P.canvas)}</span></div>`;
  const panel = `<div style="background:${P.canvas};border-left:1px solid ${P["line-1"]};display:flex;flex-direction:column">
      <div style="height:48px;display:flex;align-items:center;gap:18px;padding:0 18px;border-bottom:1px solid ${P["line-1"]};${txt(P["text-2"], 500, 13)}"><span style="color:${P.text}">改动</span><span>文件</span><span>终端</span></div>
      <div style="padding:14px 18px">${[["src/auth/session.ts", "+9 −3"], ["src/auth/store.ts", "+3 −1"]].map(([f, n], i) => `<div style="display:flex;gap:8px;align-items:center;padding:7px 8px;border-radius:7px;${i === 0 ? `background:${P["surface-3"]};` : ""}${txt(P.text, 400, 12.5)}">${ico("FileDiff", 15, P["text-3"])}<span class="mono" style="font-size:12px">${f}</span><span class="mono num" style="margin-left:auto;font-size:11.5px;color:${P["text-3"]}">${n}</span></div>`).join("")}</div>
      <div class="mono" style="margin:0 18px;border:1px solid ${P["line-1"]};border-radius:10px;overflow:hidden;font-size:11.5px;line-height:1.75;font-variant-ligatures:none">
        ${[[" ", "  const token = await refresh();"], ["-", "  const next = read(KEY);"], ["-", "  store.write(KEY, token);"], ["+", "  await store.write(KEY, token);"], ["+", "  const next = read(KEY) ?? retry();"], [" ", "  return next;"]].map(([m, l]) => `<div style="padding:0 10px;white-space:pre;background:${m === "+" ? (d ? "#1B2A1E" : "#E5F5E7") : m === "-" ? (d ? "#321C1A" : "#FFEAE7") : "transparent"};color:${m === "+" ? S.success[d ? "dark" : "light"] : m === "-" ? S.danger[d ? "dark" : "light"] : P["text-2"]}">${m} ${l}</div>`).join("")}
      </div></div>`;
  return `<div style="width:1280px;height:800px;display:grid;grid-template-columns:${mode === "home" ? "240px 1fr" : "240px 1fr 330px"};background:${P.surface};font-family:var(--brand);border-radius:12px;overflow:hidden;box-shadow:0 0 0 1px ${d ? "rgba(255,255,255,.08)" : "rgba(20,20,17,.1)"},0 20px 60px rgba(20,20,17,${d ? 0.35 : 0.12})">
    ${sidebar}<div style="display:flex;flex-direction:column;min-width:0">${conv}</div>${mode === "home" ? "" : panel}</div>`;
}

export const productLight = page({
  chapter: 7, id: "product-light",
  body: `
  <div class="grid">
    <div class="span-3">${title("产品界面 · 亮色", "Product, light")}
      <p class="lead">品牌在产品里只出现在三处：侧栏顶部的标识、会话中 Wuu 的头像、子 agent 的彩色小球。其余全部交给中性色和排版。</p>
      <div class="rule-list" style="margin-top:28px;--rl:1fr">
        <div class="body">侧栏中运行中的会话用 14 px 的工作姿态小球，代替通用的加载圈。</div>
        <div class="body">失败只出现在出错的那一步：红色文字与图标，小球颜色不变。</div>
        <div class="body">主按钮是墨色；彩色只在 agent 小球和 diff 上。</div>
      </div>
      <p class="cap" style="margin-top:20px">提案示意，非当前产品界面；示例数据。</p>
    </div>
    <div class="span-9 start-4">${scaled(1280, 800, 0.839, appWindow("light"))}</div>
  </div>`,
});

export const productDark = page({
  chapter: 7, id: "product-dark", dark: true,
  body: `
  <div class="grid">
    <div class="span-3">${title("产品界面 · 暗色", "Product, dark")}
      <p class="lead">暗色主题里 Wuu 的小球换成纸色，agent 小球用暗色一组；层级靠表面明度，而不是阴影。</p>
      <p class="body" style="margin-top:24px">diff 的增删底色使用状态色的暗色浅底，文字保持 4.5 : 1 以上。</p>
      <p class="cap" style="margin-top:20px">提案示意，非当前产品界面；示例数据。</p>
    </div>
    <div class="span-9 start-4">${scaled(1280, 800, 0.839, appWindow("dark"))}</div>
  </div>`,
});

export const productHome = page({
  chapter: 7, id: "product-home",
  body: `
  <div class="grid">
    <div class="span-3">${title("首页空状态", "Empty home")}
      <p class="lead">没有打开会话时，小球和问候并排出现。这是产品里小球最大的地方，但它仍然安静：只在空闲时偶尔眨眼。</p>
      <div class="rule-list" style="margin-top:28px;--rl:1fr">
        <div class="body">问候用标题字号与 Bold，不用衬线体。</div>
        <div class="body">用量热力图只用中性色阶：从表面 3 到墨色，不再使用蓝色。</div>
        <div class="body">热力图的小游戏属于产品彩蛋，由产品决定是否保留；品牌不要求它。</div>
      </div>
      <p class="cap" style="margin-top:20px">提案示意，非当前产品界面；示例数据。</p>
    </div>
    <div class="span-9 start-4">${scaled(1280, 800, 0.839, appWindow("light", "home"))}</div>
  </div>`,
});

// ---------------------------------------------------------------------------
function website() {
  const P = C.light;
  const nav = `<div style="display:flex;align-items:center;gap:28px;padding:26px 72px">${lockup(13)}<span style="margin-left:auto;font:500 14px var(--brand);color:${P["text-2"]}">产品</span><span style="font:500 14px var(--brand);color:${P["text-2"]}">文档</span><span style="font:500 14px var(--brand);color:${P["text-2"]}">博客</span><span style="font:500 14px var(--brand);color:${P["text-2"]}">GitHub</span><span style="font:500 14px var(--brand);background:${P.text};color:${P.canvas};padding:9px 18px;border-radius:999px">下载 macOS 版</span></div>`;
  const hero = `<div style="position:relative;height:520px;overflow:hidden">
    <div class="abs" style="left:72px;top:86px;width:760px">
      <div style="font:700 60px/1.14 var(--brand);color:${P.text}">在本地项目里，<br>和 agent 一起把事做完。</div>
      <div style="font:400 20px/1.6 var(--brand);color:${P["text-2"]};margin-top:24px;width:520px">读代码、改文件、运行命令；每一处改动都留给你检查。模型服务由你选择，开源、MIT 许可。</div>
      <div style="display:flex;gap:12px;margin-top:34px"><span style="font:500 16px var(--brand);background:${P.text};color:${P.canvas};padding:13px 24px;border-radius:999px">下载 macOS 版</span><span style="font:500 16px var(--brand);box-shadow:inset 0 0 0 1px ${P.boundary};padding:13px 24px;border-radius:999px">阅读文档</span></div>
      <div style="font:400 13px var(--brand);color:${P["text-3"]};margin-top:16px">Apple silicon · 未签名预览版</div>
    </div>
    <div class="abs" style="right:-60px;bottom:-230px">${ball(640)}</div></div>`;
  const features = `<div style="display:grid;grid-template-columns:repeat(3,1fr);gap:48px;padding:56px 72px;border-top:1px solid ${P["line-1"]}">
    ${[["FileDiff", "工作就在一处", "对话、文件、改动和终端在同一个窗口里。从提问到检查结果，不必来回切换。"], ["Cpu", "模型由你选择", "连接自己的模型服务，按任务选择合适的模型。"], ["Puzzle", "用插件补上所需", "加入工具、技能、视图和主题，让 Wuu 适应你的工作方式。"]].map(([n, h, t]) => `<div>${ico(n, 22, P.text)}<div style="font:700 20px/1.3 var(--brand);margin-top:16px">${h}</div><div style="font:400 15.5px/1.7 var(--brand);color:${P["text-2"]};margin-top:8px">${t}</div></div>`).join("")}</div>`;
  const agents = `<div style="margin:0 72px;background:${P.surface};border-radius:20px;padding:44px 48px;display:flex;align-items:center;gap:48px">
    <div style="width:420px"><div style="font:700 30px/1.25 var(--brand)">一个任务，几个 agent 一起做</div><div style="font:400 15.5px/1.7 var(--brand);color:${P["text-2"]};margin-top:12px">Wuu 把任务分给子 agent，并把每一步的结果放回同一个会话里。</div></div>
    <div style="margin-left:auto">${illoRow()}</div></div>`;
  return `<div style="width:1440px;height:1320px;background:${P.canvas};font-family:var(--brand);color:${P.text}">${nav}${hero}${features}${agents}</div>`;
}
function illoRow() {
  return `<div style="display:flex;align-items:flex-end;gap:14px;padding-bottom:4px;border-bottom:2px solid ${C.light["line-2"]}">${staticBall(110, "rest")}${[["sky", "work"], ["peach", "work"], ["leaf", "think"], ["iris", "work"]].map(([a, s]) => agentBall(60, a, false, s)).join("")}</div>`;
}

export const web = page({
  chapter: 7, id: "website",
  body: `
  <div class="grid">
    <div class="span-3">${title("官网首页", "Website")}
      <p class="lead">首屏只做三件事：说清产品是什么、给出下载、让人记住小球。大尺寸裁切的小球占据右下角，文字在左侧留白里。</p>
      <div class="rule-list" style="margin-top:28px;--rl:1fr">
        <div class="body">风景照退出首屏；功能区用产品图标和截图。</div>
        <div class="body">一整页只有一只品牌小球；agent 小球群只出现在讲协作的段落里。</div>
        <div class="body">字体从本地加载，不请求外部字体服务。</div>
      </div>
      <p class="cap" style="margin-top:20px">提案示意。文案基于当前产品事实，发布前需与官网内容负责人核对。</p>
    </div>
    <div class="span-9 start-4">
      <div style="border-radius:12px;overflow:hidden;box-shadow:0 0 0 1px var(--line-2),0 20px 60px rgba(20,20,17,.1)">
        <div style="height:30px;background:#E9E9E6;display:flex;align-items:center;gap:7px;padding:0 12px">${["#FF5F57", "#FEBC2E", "#28C840"].map((c) => `<span style="width:10px;height:10px;border-radius:5px;background:${c}"></span>`).join("")}<span style="margin-left:14px;background:#fff;border-radius:6px;padding:3px 10px;font:500 11px var(--brand);color:#555;display:flex;gap:6px;align-items:center">${ball(12)}wuu · 在本地项目里和 agent 一起工作</span></div>
        ${scaled(1440, 1320, 0.7458, website()).replace("border-radius:12px", "border-radius:0").replace(/height:[\d.]+px;overflow/, "height:760px;overflow")}
      </div>
    </div>
  </div>`,
});

// ---------------------------------------------------------------------------
function docsPage() {
  const P = C.light;
  const nav = ["开始使用", "安装", "连接模型服务", "完成第一个任务", "工作区", "会话", "子 agent", "插件", "权限模式"];
  return `<div style="width:1280px;height:760px;background:${P.canvas};font-family:var(--brand);display:grid;grid-template-rows:60px 1fr">
    <div style="display:flex;align-items:center;gap:24px;padding:0 32px;border-bottom:1px solid ${P["line-1"]}">${lockup(11)}<span style="font:500 13px var(--brand);color:${P["text-3"]}">文档</span><div style="margin-left:auto;width:260px;border-radius:9px;background:${P["surface-2"]};padding:8px 12px;font:400 13px var(--brand);color:${P["text-4"]};display:flex;gap:8px;align-items:center">${ico("Search", 15, P["text-3"])}搜索文档</div><span style="font:500 13px var(--brand);color:${P["text-2"]}">English</span></div>
    <div style="display:grid;grid-template-columns:260px 1fr 220px">
      <div style="padding:26px 22px;border-right:1px solid ${P["line-1"]}">${nav.map((t, i) => `<div style="padding:6px 10px;border-radius:7px;font:${i === 3 ? 500 : 400} 13.5px/1.5 var(--brand);color:${i === 3 ? P.text : P["text-2"]};${i === 3 ? `background:${P["surface-3"]}` : ""}">${t}</div>`).join("")}</div>
      <div style="padding:40px 64px"><div style="font:500 13px var(--brand);color:${P["text-3"]}">开始使用</div><div style="font:700 34px/1.25 var(--brand);margin-top:8px">完成第一个任务</div>
        <p style="font:400 16px/1.75 var(--brand);color:${P["text-2"]};margin-top:18px;max-width:600px">先连接一个模型服务，再选择一个本地文件夹作为工作区。Wuu 会在当前权限模式内读取文件、修改代码和运行命令，并把每一处改动留给你检查。</p>
        <div style="margin-top:18px;border-radius:12px;background:${P.surface};padding:18px 20px;display:flex;gap:16px;align-items:center;max-width:600px">${illoMini()}<div style="font:400 14px/1.6 var(--brand);color:${P["text-2"]}"><strong style="color:${P.text}">提示</strong>　第一次可以用只读模式试试，Wuu 不会修改任何文件。</div></div>
        <div style="font:700 21px/1.4 var(--brand);margin-top:30px">1. 连接模型服务</div>
        <p style="font:400 16px/1.75 var(--brand);color:${P["text-2"]};margin-top:8px;max-width:600px">打开“设置 → 模型服务”，选择服务商并填入凭据。</p></div>
      <div style="padding:40px 22px;border-left:1px solid ${P["line-1"]}"><div style="font:600 12px var(--brand);color:${P["text-3"]};letter-spacing:.06em">本页内容</div>${["连接模型服务", "选择工作区", "描述任务", "检查改动"].map((t, i) => `<div style="font:400 13px/1.5 var(--brand);color:${i ? P["text-3"] : P.text};margin-top:10px">${t}</div>`).join("")}</div>
    </div></div>`;
}
const illoMini = () => staticBall(40, "rest");

export const docs = page({
  chapter: 7, id: "docs-readme",
  body: `
  <div class="grid">
    <div class="span-3">${title("文档站与 README", "Docs and README")}
      <p class="lead">文档是阅读场景：标识小，正文宽松，提示框用表面色而不是彩色。README 顶部用横幅，并提供亮暗两张图。</p>
      <div class="plate two codeblock" style="padding:16px 18px;margin-top:28px;font-size:11.5px">&lt;picture&gt;<br>&nbsp;&lt;source media="(prefers-color-scheme: dark)"<br>&nbsp;&nbsp;srcset="brand/assets/social/readme-banner-dark.png"&gt;<br>&nbsp;&lt;img alt="Wuu"<br>&nbsp;&nbsp;src="brand/assets/social/readme-banner-light.png"&gt;<br>&lt;/picture&gt;</div>
      <p class="cap" style="margin-top:14px">提案示意；是否替换 README 与文档站主题需另行决定。</p>
    </div>
    <div class="span-9 start-4">
      ${scaled(1280, 760, 0.75, docsPage()).replace("border-radius:12px", "border-radius:12px;box-shadow:0 0 0 1px var(--line-2)")}
      <div class="row" style="--g:24px;margin-top:24px;flex-wrap:wrap">
        ${tpl(readmeBanner("light"), 0.4)}${tpl(readmeBanner("dark"), 0.4)}
      </div>
      <div class="cap" style="margin-top:8px">README 横幅 1280 × 320 · 亮 / 暗</div>
    </div>
  </div>`,
});

export const social = page({
  chapter: 7, id: "social",
  body: `
  <div class="grid">
    <div class="span-3">${title("社交与发布", "Social and releases")}
      <p class="lead">分享图和发布卡片用同一套版式：左上标识，左侧大标题，右下一只小球。发布卡片用“完成”姿态。</p>
      <table class="spec" style="margin-top:28px">
        <thead><tr><th>素材</th><th>尺寸</th></tr></thead>
        <tbody><tr><td>分享图（中 / 英）</td><td class="num">1200 × 630</td></tr><tr><td>发布卡片</td><td class="num">1200 × 675</td></tr><tr><td>社交头像</td><td class="num">400 × 400</td></tr></tbody>
      </table>
      <p class="body" style="margin-top:16px">模板在 <span class="mono">src/templates.mjs</span>，改动文字后重新渲染即可导出 PNG。</p>
    </div>
    <div class="span-9 start-4">
      <div class="row" style="--g:24px;--a:flex-start">${tpl(ogImage("zh"), 0.4375)}${tpl(ogImage("en"), 0.4375)}</div>
      <div class="row" style="--g:24px;--a:flex-end;margin-top:24px">${tpl(releaseCard(), 0.4375)}
        <div><div style="width:150px;height:150px;border-radius:75px;overflow:hidden;box-shadow:0 0 0 1px var(--line-2)"><div style="transform:scale(.375);transform-origin:0 0;width:400px;height:400px">${socialAvatar().html}</div></div><div class="cap" style="margin-top:10px">社交头像 · 圆形裁切</div></div>
      </div>
    </div>
  </div>`,
});

export const installer = page({
  chapter: 7, id: "installer",
  body: `
  <div class="grid">
    <div class="span-3">${title("安装窗口与幻灯片", "Installer and slides")}
      <p class="lead">安装窗口只说一件事：把 Wuu 拖进“应用程序”。应用图标本身就是小球，所以背景里只有一条路径和一行提示，不再放第二只小球。</p>
      <p class="body" style="margin-top:20px">演示文稿的封面沿用封面页版式；内页用手册的 12 栏网格与字号层级，不加页眉装饰。</p>
      <p class="cap" style="margin-top:20px">安装背景为提案；替换 <span class="mono">desktop/build/dmg-background*.png</span> 需另行评审。</p>
    </div>
    <div class="span-9 start-4">
      <div style="width:760px;border-radius:12px;overflow:hidden;box-shadow:0 0 0 1px var(--line-2),0 20px 60px rgba(20,20,17,.12)">
        <div style="height:28px;background:#ECECEA;display:flex;align-items:center;gap:7px;padding:0 12px;border-bottom:1px solid #DADAD6">${["#FF5F57", "#FEBC2E", "#28C840"].map((c) => `<span style="width:10px;height:10px;border-radius:5px;background:${c}"></span>`).join("")}<span style="margin:0 auto;font:500 12px var(--brand);color:#555">Wuu</span></div>
        <div style="position:relative;width:720px;height:420px;margin:0 20px">${dmgBackground().html}
          <div class="abs" style="left:116px;top:186px;text-align:center">${icon(128)}<div style="font:400 13px var(--brand);margin-top:2px">Wuu</div></div>
          <div class="abs" style="left:476px;top:186px;text-align:center"><svg width="128" height="128" viewBox="0 0 128 128"><path d="M14 34a8 8 0 0 1 8-8h28l10 10h46a8 8 0 0 1 8 8v54a8 8 0 0 1-8 8H22a8 8 0 0 1-8-8Z" fill="#8FBDEB"/><path d="M14 48a8 8 0 0 1 8-8h84a8 8 0 0 1 8 8v50a8 8 0 0 1-8 8H22a8 8 0 0 1-8-8Z" fill="#A9CDF1"/></svg><div style="font:400 13px var(--brand);margin-top:2px">Applications</div></div>
        </div>
      </div>
      <div class="row" style="--g:24px;margin-top:28px">
        ${[["cover", "封面"], ["body", "内页"]].map(([k, t]) => `<div><div style="width:368px;height:207px;border-radius:8px;overflow:hidden;position:relative;background:${C.brand.paper};box-shadow:0 0 0 1px var(--line-2)">${k === "cover"
          ? `<div class="abs" style="left:18px;top:16px">${lockup(6)}</div><div class="abs" style="left:18px;top:76px;font:700 24px/1.15 var(--brand)">2026 路线图</div><div class="abs" style="left:18px;top:108px;font:400 10px var(--brand);color:var(--text-3)">Wuu 贡献者会议 · 示例</div><div class="abs" style="right:-24px;bottom:-78px">${ball(200)}</div>`
          : `<div class="abs" style="left:18px;top:18px;font:600 9px var(--brand);color:var(--text-3)">02 · 插件</div><div class="abs" style="left:18px;top:40px;font:700 17px/1.2 var(--brand)">插件与核心的边界</div><div class="abs" style="left:18px;top:74px;width:140px;font:400 8.5px/1.6 var(--brand);color:var(--text-2)">核心负责生命周期、持久化和权限；插件负责业务状态、提示词与工具。</div><div class="abs" style="left:184px;top:40px;right:18px;height:140px;border-radius:6px;background:var(--surface);display:grid;grid-template-columns:1fr 1fr;gap:8px;padding:12px"><div style="border-radius:4px;background:var(--surface-3);font:600 8px var(--brand);padding:6px">核心</div><div style="border-radius:4px;box-shadow:inset 0 0 0 1px var(--line-2);font:600 8px var(--brand);padding:6px;display:flex;flex-direction:column;gap:4px">插件<span style="display:flex;gap:3px">${agentBall(12, "sky")}${agentBall(12, "leaf")}</span></div></div>`}</div><div class="cap" style="margin-top:8px">演示文稿 · ${t}</div></div>`).join("")}
      </div>
    </div>
  </div>`,
});

export default [appIcon, productLight, productDark, productHome, web, docs, social, installer];
