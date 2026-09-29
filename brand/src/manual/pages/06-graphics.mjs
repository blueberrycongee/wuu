// Graphic language: icons, illustration, imagery, large-format crops.
import { page, title, ball, lockup, appIcon, C, G, T, staticBall, agentBall, AGENTS } from "../kit.mjs";
// Product icon artwork is the single source for UI icons; the manual draws from it.
import { iconArtwork } from "../../../../desktop/src/shared/iconArtwork.ts";

function ico(name, px = 24, color = "currentColor", sw = 1.75) {
  const nodes = iconArtwork[name].map(([tag, a]) => `<${tag} ${Object.entries(a).map(([k, v]) => `${k.replace(/[A-Z]/g, (m) => "-" + m.toLowerCase())}="${v}"`).join(" ")}/>`).join("");
  return `<svg width="${px}" height="${px}" viewBox="0 0 24 24" fill="none" stroke="${color}" stroke-width="${sw}" stroke-linecap="round" stroke-linejoin="round" style="color:${color}">${nodes}</svg>`;
}

const SET = ["FileDiff", "Terminal", "Globe", "FolderOpen", "GitBranch", "Search", "Puzzle", "ShieldCheck", "CalendarDays", "Users", "Settings", "Check", "MessageSquarePlus", "Paperclip", "Pencil", "Layers", "Cpu", "Bell"];

function iconGrid(name, px) {
  const g = Array.from({ length: 25 }, (_, i) => `<line x1="${i}" y1="0" x2="${i}" y2="24" stroke="#E4C9C3" stroke-width="${i % 4 === 0 ? 0.06 : 0.03}"/><line x1="0" y1="${i}" x2="24" y2="${i}" stroke="#E4C9C3" stroke-width="${i % 4 === 0 ? 0.06 : 0.03}"/>`).join("");
  const nodes = iconArtwork[name].map(([tag, a]) => `<${tag} ${Object.entries(a).map(([k, v]) => `${k}="${v}"`).join(" ")}/>`).join("");
  return `<svg width="${px}" height="${px}" viewBox="0 0 24 24">${g}<rect x="2" y="2" width="20" height="20" fill="none" stroke="#C2412F" stroke-width=".05" stroke-dasharray=".3 .3"/><g fill="none" stroke="${C.brand.ink}" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" style="color:${C.brand.ink}">${nodes}</g></svg>`;
}

export const icons = page({
  chapter: 5, id: "icons",
  body: `
  <div class="grid">
    <div class="span-4">${title("图标", "Icons")}
      <p class="lead">界面图标只用产品已有的 Wuu 图标：24 单位网格、1.75 描边、圆头圆角、开放轮廓。官网和文档也用这一套，不另画一种风格。</p>
      <div class="rule-list" style="margin-top:32px;--rl:1fr">
        <div class="body"><strong>实心胶囊是唯一的“面”。</strong>终端光标这类小元素用实心胶囊，它和小球的眼睛是同一种形状，让图标站在小球旁边也协调。</div>
        <div class="body"><strong>小球不进图标。</strong>不把小球画成功能图标，也不给图标加眼睛。</div>
        <div class="body"><strong>单色。</strong>图标跟随文字颜色；状态图标用对应状态色，不做双色或渐变。</div>
        <div class="body"><strong>放大时保持比例。</strong>大于 32 px 时整体缩放，描边随之变粗，不改成细线。</div>
      </div>
      <p class="cap" style="margin-top:16px">来源：<span class="mono">desktop/src/shared/iconArtwork.ts</span>，本页直接读取该文件绘制。</p>
    </div>
    <div class="span-7 start-6">
      <div class="plate" style="padding:32px 36px">
        <div class="grid" style="grid-template-columns:repeat(9,1fr);row-gap:26px">
          ${SET.map((n) => `<div style="text-align:center">${ico(n, 24, C.brand.ink)}<div class="cap" style="margin-top:6px;font-size:10.5px;font-weight:400">${n}</div></div>`).join("")}
        </div>
      </div>
      <div class="row" style="--g:24px;margin-top:24px;--a:stretch">
        <div class="plate" style="padding:24px;flex:0 0 auto">${iconGrid("Terminal", 216)}<div class="cap" style="margin-top:10px">Terminal · 24 网格，2 单位留边</div></div>
        <div class="plate stack" style="padding:24px 28px;flex:1;--s:18px">
          <div class="h4">三种“图标”不要混用</div>
          <div class="row" style="--g:18px">${appIcon(56)}<div><div class="small" style="color:var(--text)">应用图标</div><div class="cap">程序坞、安装包、网站下载按钮</div></div></div>
          <div class="row" style="--g:18px"><div style="width:56px" class="center">${ball(32)}</div><div><div class="small" style="color:var(--text)">小球</div><div class="cap">品牌符号、头像、网站图标</div></div></div>
          <div class="row" style="--g:18px"><div style="width:56px" class="center">${ico("Terminal", 24, C.brand.ink)}</div><div><div class="small" style="color:var(--text)">界面图标</div><div class="cap">按钮、菜单、状态；来自 iconArtwork</div></div></div>
          <div class="row" style="--g:10px;padding-top:12px;border-top:1px solid var(--line-1)">${ico("CircleCheck", 18, C.status.success.light)}<span class="small" style="color:${C.status.success.light}">测试通过</span>${ico("TriangleAlert", 18, C.status.warning.light)}<span class="small" style="color:${C.status.warning.light}">需要确认</span>${ico("CircleAlert", 18, C.status.danger.light)}<span class="small" style="color:${C.status.danger.light}">命令失败</span></div>
        </div>
      </div>
      <div class="plate" style="margin-top:24px;padding:26px 30px;display:grid;grid-template-columns:repeat(3,1fr);column-gap:28px">
        ${[["FileDiff", "检查每一处改动", "文件、diff 和测试结果都在应用里。"], ["Terminal", "在本地运行命令", "在权限模式内执行，输出留在会话里。"], ["Puzzle", "用插件补上所需", "工具、技能、视图和主题。"]].map(([n, h, t]) => `<div>${ico(n, 22, C.brand.ink)}<div class="h4" style="margin-top:12px">${h}</div><div class="small" style="margin-top:4px">${t}</div></div>`).join("")}
      </div>
      <div class="cap" style="margin-top:8px">官网功能列表：图标与标题左对齐，22 px</div>
    </div>
  </div>`,
});

// Illustration: flat balls on a ground line, with simple work fragments.
const card = (x, y, w, h, fill = C.light.surface, extra = "") => `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${Math.min(12, h / 4)}" fill="${fill}" ${extra}/>`;
const bars = (x, y, n, w = 120) => Array.from({ length: n }, (_, i) => `<rect x="${x}" y="${y + i * 16}" width="${w - (i % 2) * 34}" height="7" rx="3.5" fill="${C.light["line-2"]}"/>`).join("");
const pose = (state, dx = 0, dy = 0) => ({ ...G.WuuBall.pose("display", state), gazeX: G.WuuBall.pose("display", state).gazeX + dx, gazeY: G.WuuBall.pose("display", state).gazeY + dy });
const B = (cx, cy, d, body, eye, p) => { const r = G.WuuBall.eyeRects("display", p || G.WuuBall.pose("display", "rest")); const eyes = r.map((e) => { const w = e.width, h = Math.max(e.height, w * 0.9); return `<rect x="${-w / 2}" y="${-h / 2}" width="${w}" height="${h}" rx="${w / 2}" transform="translate(${e.cx} ${e.cy}) rotate(${e.rotate})"/>`; }).join(""); return `<g transform="translate(${cx - d / 2} ${cy - d / 2}) scale(${d / 100})"><circle cx="50" cy="50" r="50" fill="${body}"/><g fill="${eye}">${eyes}</g></g>`; };

export function illoTeam(w = 560, h = 300) {
  const ground = h - 44;
  return `<svg width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">
    ${card(248, 40, 270, 150)}${bars(272, 70, 5, 200)}
    ${B(120, ground - 60, 120, C.brand.ink, C.brand.paper, pose("rest", 4, 0))}
    ${B(264, ground - 32, 64, C.agent.sky.light, C.brand.ink, pose("work", 0, -10))}
    ${B(346, ground - 32, 64, C.agent.peach.light, C.brand.ink, pose("work"))}
    ${B(428, ground - 32, 64, C.agent.leaf.light, C.brand.ink, pose("think"))}
    <line x1="24" x2="${w - 24}" y1="${ground}" y2="${ground}" stroke="${C.light["line-2"]}" stroke-width="2" stroke-linecap="round"/>
  </svg>`;
}
export function illoEmpty(w = 400, h = 300) {
  const ground = h - 44;
  return `<svg width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">
    ${card(170, 70, 190, 130, C.light.surface, `stroke="${C.light["line-2"]}" stroke-width="2" stroke-dasharray="6 6"`)}
    ${B(110, ground - 50, 100, C.brand.ink, C.brand.paper, pose("rest", 2, -4))}
    <line x1="24" x2="${w - 24}" y1="${ground}" y2="${ground}" stroke="${C.light["line-2"]}" stroke-width="2" stroke-linecap="round"/>
  </svg>`;
}
export function illoPlugins(w = 400, h = 300) {
  const ground = h - 44;
  const blk = (x, y, c) => `<rect x="${x}" y="${y}" width="58" height="58" rx="14" fill="${c}"/>`;
  return `<svg width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">
    ${blk(200, ground - 58, C.light["surface-3"])}${blk(262, ground - 58, C.light["surface-3"])}${blk(231, ground - 120, C.light.surface)}
    <rect x="231" y="${ground - 120}" width="58" height="58" rx="14" fill="none" stroke="${C.light["line-2"]}" stroke-width="2"/>
    ${B(116, ground - 45, 90, C.brand.ink, C.brand.paper, pose("rest", 6, -6))}
    <line x1="24" x2="${w - 24}" y1="${ground}" y2="${ground}" stroke="${C.light["line-2"]}" stroke-width="2" stroke-linecap="round"/>
  </svg>`;
}

export const illustration = page({
  chapter: 5, id: "illustration",
  body: `
  <div class="grid">
    <div class="span-4">${title("插画：一群小球", "Illustration")}
      <p class="lead">插画只讲一件事：谁在做什么。角色只有小球，道具只有简单的卡片、线条和方块，它们代表界面里的内容。</p>
      <div class="rule-list" style="margin-top:32px;--rl:1fr">
        <div class="body"><strong>一只墨色小球，至多五只 agent 小球。</strong>Wuu 永远最大、最靠前；agent 按 agent 色区分。</div>
        <div class="body"><strong>平面。</strong>没有描边、渐变、投影和高光；一条地平线就够了。</div>
        <div class="body"><strong>眼睛用状态姿态。</strong>工作中的小球看向卡片，思考的小球看向上方，不摆夸张表情。</div>
        <div class="body"><strong>用在需要解释的地方。</strong>空状态、引导、文档章节首图、发布说明。设置页和密集界面里不放插画。</div>
        <div class="body"><strong>配饰属于产品。</strong>模型头像的帽子、耳机是产品功能，不进品牌插画。</div>
      </div>
    </div>
    <div class="span-7 start-6">
      <div class="plate center" style="height:340px">${illoTeam()}</div>
      <div class="cap" style="margin-top:8px">多个 agent 协作：Wuu 看着整体，三个子 agent 各看各的任务</div>
      <div class="grid" style="grid-template-columns:1fr 1fr;column-gap:24px;margin-top:24px">
        <div><div class="plate center" style="height:300px">${illoEmpty(380, 280)}</div><div class="cap" style="margin-top:8px">空状态：还没有会话</div></div>
        <div><div class="plate center" style="height:300px">${illoPlugins(380, 280)}</div><div class="cap" style="margin-top:8px">插件：新增的能力是一块块积木</div></div>
      </div>
    </div>
  </div>`,
});

export const imagery = page({
  chapter: 5, id: "imagery",
  body: `
  <div class="grid">
    <div class="span-4">${title("截图与影像", "Screenshots and imagery")}
      <p class="lead">Wuu 最好的图像是它自己的界面。截图是主要影像；照片不属于品牌识别的一部分。</p>
      <div class="rule-list" style="margin-top:32px;--rl:1fr">
        <div class="body"><strong>裁到要说明的地方。</strong>只保留和这句话有关的面板，放大到文字能读。</div>
        <div class="body"><strong>平放，不加设备外壳。</strong>不倾斜、不加透视、不放进笔记本电脑模型。圆角 12 px，1 px 分隔线描边。</div>
        <div class="body"><strong>用虚构数据。</strong>不出现真实账号、路径、密钥和私有代码；标注“示例数据”。</div>
        <div class="body"><strong>亮暗主题分开。</strong>同一页里所有截图用同一主题。</div>
        <div class="body"><strong>照片只做编辑内容。</strong>博客文章可以用照片，但不放在首页首屏、图标或标识旁边。当前官网的风景照建议退出首屏。</div>
      </div>
    </div>
    <div class="span-7 start-6">
      <div class="plate" style="padding:40px;background:var(--surface-2);position:relative"><span class="doy">✓</span>
        <div style="width:565px;height:420px;border-radius:12px;box-shadow:0 0 0 1px var(--line-2);background:#fff url(reference/app-light.png) no-repeat;background-size:1736px;background-position:-403px -64px;margin:0 auto"></div>
        <div class="cap" style="margin-top:14px;text-align:center">只裁会话区，平放，保留可读的正文（开发预览，示例数据）</div>
      </div>
      <div class="grid" style="grid-template-columns:1fr 1fr;column-gap:24px;margin-top:24px">
        <div class="plate" style="height:250px;overflow:hidden;position:relative;background:linear-gradient(135deg,#2a2d3a,#15161c)"><span class="dontx">✕</span>
          <div style="position:absolute;left:70px;top:40px;width:300px;height:190px;border-radius:12px;background:#fff url(reference/app-light.png) no-repeat 0 0/300px;transform:perspective(700px) rotateY(-22deg) rotateX(8deg);box-shadow:0 30px 60px rgba(0,0,0,.5)"></div>
          <div class="cap" style="position:absolute;left:16px;bottom:12px;color:#aaa">透视、暗色渐变背景、整窗缩小到无法阅读</div></div>
        <div class="plate" style="height:250px;overflow:hidden;position:relative;background:url(reference/landing-home.jpg) 4% 96%/300%"><span class="dontx">✕</span>
          <div class="abs" style="left:50%;top:48%;transform:translate(-50%,-50%)">${lockup(20, { ink: "#fff", eye: "#1E2230" })}</div>
          <div class="cap" style="position:absolute;left:16px;bottom:12px;color:#ddd">把标识放在风景照上当主视觉</div></div>
      </div>
    </div>
  </div>`,
});

export const crop = page({
  chapter: 5, id: "large-crop",
  body: `
  <div class="grid">
    <div class="span-4">${title("大尺寸裁切构图", "The large-format crop")}
      <p class="lead">借用自方向 B 的唯一规则：在封面、首屏和社交图这种大幅面上，小球可以大到被画面边缘裁掉一部分。</p>
      <table class="spec" style="margin-top:32px">
        <thead><tr><th style="width:64px">条件</th><th>要求</th></tr></thead>
        <tbody>
          <tr><td>幅面</td><td>短边 ≥ 600 px 的画面；标识、头像、图标不适用</td></tr>
          <tr><td>尺寸</td><td>球的直径 ≥ 画面短边的 60%</td></tr>
          <tr><td>眼睛</td><td>两只眼完整可见，离裁切边至少一个眼宽</td></tr>
          <tr><td>裁切</td><td>只被一到两条边裁切；一个画面只放一只裁切球</td></tr>
          <tr><td>文字</td><td>不压在球上；放在球对面的留白里</td></tr>
          <tr><td>方向</td><td>品牌姿态的眼睛在右上，所以通常裁左边和下边；裁右边时只裁掉很少一部分</td></tr>
        </tbody>
      </table>
      <p class="body" style="margin-top:18px">应用图标有自己已批准的构图，不适用这条规则，也不作为它的依据，见第 08 章。</p>
    </div>
    <div class="span-7 start-6">
      <div class="grid" style="grid-template-columns:1.6fr 1fr;column-gap:24px">
        <div><div class="plate" style="height:320px;overflow:hidden;position:relative;background:var(--canvas);box-shadow:inset 0 0 0 1px var(--line-2)"><span class="doy">✓</span>
          <div class="abs" style="right:-34px;bottom:-120px">${ball(340)}</div>
          <div class="abs" style="left:28px;top:56px;font:700 30px/1.2 var(--brand)">每一处改动，<br>都留给你检查。</div></div><div class="cap" style="margin-top:8px">右、下两边裁切；眼睛完整，文字在左上留白</div></div>
        <div><div class="plate" style="height:320px;overflow:hidden;position:relative;background:${C.brand.ink}"><span class="doy">✓</span>
          <div class="abs" style="left:-40px;bottom:-190px">${ball(380, { body: C.dark.ball, eye: C.dark["ball-eye"] })}</div>
          <div class="abs" style="left:26px;top:52px">${lockup(12, { ink: C.dark.ball, eye: C.dark["ball-eye"] })}</div></div><div class="cap" style="margin-top:8px">竖幅：只裁底边</div></div>
      </div>
      <div class="grid" style="grid-template-columns:repeat(3,1fr);column-gap:24px;margin-top:24px">
        <div><div class="plate" style="height:190px;overflow:hidden;position:relative;background:var(--surface)"><span class="dontx">✕</span><div class="abs" style="right:-120px;top:-20px">${ball(260)}</div></div><div class="cap" style="margin-top:8px">眼睛被裁掉</div></div>
        <div><div class="plate" style="height:190px;overflow:hidden;position:relative;background:var(--surface)"><span class="dontx">✕</span><div class="abs" style="right:-40px;bottom:-60px">${ball(170)}</div><div class="abs" style="left:-50px;top:-60px">${ball(150)}</div></div><div class="cap" style="margin-top:8px">一个画面里两只裁切球</div></div>
        <div><div class="plate" style="height:190px;overflow:hidden;position:relative;background:var(--surface)"><span class="dontx">✕</span><div class="abs" style="right:-60px;bottom:-110px">${ball(300)}</div><div class="abs" style="left:24px;bottom:40px;font:700 26px/1.2 var(--brand);color:#fff;mix-blend-mode:difference">文字压在球上</div></div><div class="cap" style="margin-top:8px">文字压在球上</div></div>
      </div>
    </div>
  </div>`,
});

export default [icons, illustration, imagery, crop];
