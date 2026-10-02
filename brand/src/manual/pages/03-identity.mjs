// Identity: the ball, the wordmark, lockups and their rules.
import { page, title, ball, lockup, wordmark, C, G, T, staticBall, agentBall, cr, pngURI } from "../kit.mjs";

const pct = (n) => `${+(n).toFixed(1)}%`;
const f2 = (n) => String(+n.toFixed(2));

// Construction drawing of the display ball: D = 100 units, drawn at px size.
function constructionBall(px) {
  const eyes = G.eyeRects("display", {});
  const o = T.ball.optical.display;
  const F = [50 + o.gazeX, 50 + o.gazeY];
  const p = G.ballPaths("display", {});
  const red = "#C2412F";
  const t = (x, y, s, anchor = "start") => `<text x="${x}" y="${y}" font-size="2.4" fill="${red}" font-family="Hanken Grotesk, Noto Sans CJK SC" font-weight="500" text-anchor="${anchor}">${s}</text>`;
  const L = (x1, y1, x2, y2, dash = "") => `<line x1="${x1}" y1="${y1}" x2="${x2}" y2="${y2}" stroke="${red}" stroke-width=".22" ${dash ? `stroke-dasharray="${dash}"` : ""}/>`;
  const [e1, e2] = eyes;
  const axis = (o.lean * Math.PI) / 180;
  const ax = (d) => [F[0] + d * Math.cos(axis), F[1] + d * Math.sin(axis)];
  return `<svg width="${px}" height="${px * 1.12}" viewBox="-12 -8 124 112" overflow="visible">
    <path d="${p.body}" fill="#E8E8E5"/><path d="${p.eyes}" fill="${C.brand.ink}"/>
    ${L(50, -4, 50, 101, "0.8 0.8")}${L(-4, 50, 104, 50, "0.8 0.8")}
    <circle cx="50" cy="50" r=".7" fill="${red}"/><circle cx="${F[0]}" cy="${F[1]}" r=".7" fill="${red}"/>
    ${L(50, 56, F[0], 56)}${L(F[0], 54.8, F[0], 57.2)}${t(F[0] + 1.2, 57, `视线 +${o.gazeX}`)}
    ${L(44, 50, 44, F[1])}${L(42.8, F[1], 45.2, F[1])}${t(43, 44.6, `${o.gazeY}`, "end")}
    ${L(...ax(-20), ...ax(24), "0.6 0.6")}${t(ax(24)[0] + 1, ax(24)[1] + 0.8, `眼轴 ${o.lean}°`)}
    ${L(e1.cx, 21, e2.cx, 21 + (e2.cx - e1.cx) * Math.tan(axis))}${t((e1.cx + e2.cx) / 2, 19, `眼距 ${o.gap}`, "middle")}
    ${L(e1.cx - e1.width / 2, 64, e1.cx + e1.width / 2, 64)}${t(e1.cx, 68, `眼宽 ${o.eyeWidth}`, "middle")}
    ${L(e2.cx - e2.width / 2, 64, e2.cx + e2.width / 2, 64)}${t(e2.cx + 2, 68, `外侧 ${f2(e2.width)}`, "middle")}
    ${L(82, e2.cy - o.eyeHeight / 2, 82, e2.cy + o.eyeHeight / 2)}${t(83.4, e2.cy - 7, `眼高 ${o.eyeHeight}`)}
    ${L(0, 103, 100, 103)}${L(0, 101.8, 0, 104.2)}${L(100, 101.8, 100, 104.2)}${t(50, 107, "D = 100", "middle")}
  </svg>`;
}

export const roles = page({
  chapter: 2, id: "ball-roles",
  body: `
  <div class="grid">
    <div class="span-4">${title("小球的三种用法", "Ball roles")}
      <p class="lead">品牌符号、Wuu 头像、其他 agent。</p>
      <div class="label" style="margin-top:48px">使用限制</div>
      <div class="rule-list" style="margin-top:10px;--rl:1fr">
        <div class="body">不加戏服或台词，不用于与 agent 无关的营销场景。</div>
        <div class="body">下载、保存使用产品的进度控件。</div>
        <div class="body">一个版面最多一只品牌小球；agent 小球群除外。</div>
      </div>
    </div>
    <div class="span-7 start-6">
      ${[
        ["品牌符号", "Brand symbol", "标识、网站、文档和物料中的 Wuu。形状、颜色、眼睛姿态全部固定。应用图标沿用自己的已批准原稿，见第 08 章。", `${ball(88)}<div style="margin-left:18px">${lockup(20)}</div>`, "只用第 03 章的资产，不重画、不做表情。"],
        ["会话中的 Wuu", "Wuu at work", "对话里 Wuu 自己的头像。颜色固定为墨色（暗色主题为纸色），眼睛随活动状态变化。", `<div class="row" style="--g:14px">${["rest", "think", "work", "wait", "failed"].map((s) => staticBall(56, s)).join("")}</div>`, "状态只由眼睛表达，见第 07 章。"],
        ["其他 agent", "Other agents", "子 agent、项目 agent、外部模型与服务商。同一张脸，换 agent 色；可以换形体与配饰。", `<div class="row" style="--g:12px">${["sky", "peach", "leaf", "iris", "rose"].map((a) => agentBall(52, a)).join("")}</div>`, "Wuu 自己永远不用 agent 色。"],
      ].map(([zh, en, d, fig, rule]) => `
      <div class="row" style="--g:32px;--a:center;padding:30px 0;border-bottom:1px solid var(--line-1)">
        <div style="width:360px;display:flex;align-items:center">${fig}</div>
        <div style="flex:1"><div class="row" style="--g:10px;--a:baseline"><span class="h3">${zh}</span><span class="cap">${en}</span></div>
          <p class="body" style="margin-top:8px">${d}</p><p class="small" style="margin-top:6px;color:var(--text)">${rule}</p></div>
      </div>`).join("")}
      <div class="plate" style="margin-top:36px;height:236px;display:grid;grid-template-columns:200px 1fr 220px;overflow:hidden;box-shadow:0 0 0 1px var(--line-1);font:400 12.5px/1.5 var(--brand)">
        <div style="background:var(--canvas);border-right:1px solid var(--line-1);padding:18px 14px;position:relative">${lockup(9)}<div style="margin-top:18px" class="small">新对话</div><div class="small" style="margin-top:6px;background:var(--surface-3);border-radius:6px;padding:4px 8px;margin-left:-8px">整理登录流程</div><span class="abs tag" style="left:130px;top:14px">1</span></div>
        <div style="padding:22px 22px;position:relative"><div class="row" style="--g:10px;--a:flex-start">${staticBall(24, "work")}<div><div class="cap">正在运行测试</div><div class="small" style="margin-top:4px;color:var(--text)">已修改 2 个文件，等待 <span class="mono">npm test</span> 完成。</div></div></div><span class="abs tag" style="right:18px;top:18px">2</span></div>
        <div style="border-left:1px solid var(--line-1);padding:18px 16px;position:relative"><div class="cap">子 agent</div>${[["sky", "检查类型"], ["peach", "更新文档"], ["leaf", "写测试"]].map(([a, t]) => `<div class="row" style="--g:8px;margin-top:10px">${agentBall(18, a, false, "work")}<span class="small">${t}</span></div>`).join("")}<span class="abs tag" style="right:14px;top:14px">3</span></div>
      </div>
      <p class="cap" style="margin-top:8px">同一屏中的三种用法：1 品牌符号 · 2 会话中的 Wuu · 3 其他 agent（示意）</p>
    </div>
  </div>`,
});

export const construction = page({
  chapter: 2, id: "ball-construction",
  body: `
  <div class="grid">
    <div class="span-4">${title("小球的构造", "Construction")}
      <p class="lead">使用生成资产，保留眼睛的位置与比例。</p>
      <table class="spec" style="margin-top:36px">
        <thead><tr><th>参数</th><th>值（D = 100）</th><th>说明</th></tr></thead>
        <tbody>
          <tr><td>眼宽 × 眼高</td><td class="num">${T.ball.optical.display.eyeWidth} × ${T.ball.optical.display.eyeHeight}</td><td>胶囊比例约 1 : 2.2</td></tr>
          <tr><td>眼距（中心）</td><td class="num">${T.ball.optical.display.gap}</td><td>两眼中心距离</td></tr>
          <tr><td>视线中心</td><td class="num">+${T.ball.optical.display.gazeX}, ${T.ball.optical.display.gazeY}</td><td>视线偏右上</td></tr>
          <tr><td>眼轴倾斜</td><td class="num">${T.ball.optical.display.lean}°</td><td>眼轴顺时针倾斜</td></tr>
          <tr><td>外侧眼收窄</td><td class="num">${T.ball.optical.display.foreshorten} × 偏离度</td><td>外侧眼透视缩放</td></tr>
        </tbody>
      </table>
      <p class="cap" style="margin-top:14px">数值定义在 <span class="mono">tokens.json → ball.optical</span>；图形由 <span class="mono">src/geometry.mjs</span> 生成。</p>
    </div>
    <div class="span-7 start-6 center" style="height:800px;gap:48px">${constructionBall(560)}<div class="stack" style="--s:20px;align-self:flex-end;margin-bottom:60px">${ball(96)}${ball(40)}${ball(20)}</div></div>
  </div>`,
});

function pauseFace(px) {
  return `<svg width="${px}" height="${px}" viewBox="0 0 100 100"><circle cx="50" cy="50" r="50" fill="${C.brand.ink}"/><rect x="33" y="30" width="12" height="38" rx="6" fill="${C.brand.paper}"/><rect x="55" y="30" width="12" height="38" rx="6" fill="${C.brand.paper}"/></svg>`;
}

export const optical = page({
  chapter: 2, id: "ball-optical",
  body: `
  <div class="grid">
    <div class="span-4">${title("三种光学尺寸", "Optical sizes")}
      <p class="lead">按最终显示直径选用版本；小尺寸版加大眼睛。</p>
      <table class="spec" style="margin-top:36px">
        <thead><tr><th>版本</th><th>渲染直径</th><th>眼宽 × 高</th><th>视线</th><th>倾斜</th></tr></thead>
        <tbody>${["display", "small", "micro"].map((k) => { const o = T.ball.optical[k]; const r = k === "display" ? `> ${T.ball.opticalBreakpoints.small} px` : k === "small" ? `${T.ball.opticalBreakpoints.micro + 1}–${T.ball.opticalBreakpoints.small} px` : `≤ ${T.ball.opticalBreakpoints.micro} px`; return `<tr><td>${{ display: "展示 Display", small: "小 Small", micro: "微 Micro" }[k]}</td><td class="num">${r}</td><td class="num">${o.eyeWidth} × ${o.eyeHeight}</td><td class="num">+${o.gazeX}, ${o.gazeY}</td><td class="num">${o.lean}°</td></tr>`; }).join("")}</tbody>
      </table>
      <p class="body" style="margin-top:20px">导出资产时按<strong>最终渲染直径</strong>选版本；<span class="mono">ballSVG({ px })</span> 会自动选择。不要把 Display 版缩小使用。</p>
    </div>
    <div class="span-7 start-6">
      <div class="plate" style="padding:40px 44px">
        <div class="row" style="--g:44px;--a:flex-end">
          ${[["display", 160], ["display", 64], ["small", 40], ["small", 28], ["micro", 20], ["micro", 16], ["micro", 12]].map(([s, px]) => `<div style="text-align:center">${G.ballSVG({ px, size: s })}<div class="cap num" style="margin-top:12px">${px}</div></div>`).join("")}
        </div>
      </div>
      <div class="plate" style="padding:28px 44px;margin-top:24px;display:flex;gap:56px;align-items:center">
        ${[["display", "Display 版缩到 16 px"], ["micro", "Micro 版 16 px"]].map(([k, t]) => `<div class="row" style="--g:20px"><img src="${pngURI(G.ballPNG({ px: 16, size: k }))}" width="128" height="128" style="image-rendering:pixelated;box-shadow:0 0 0 1px var(--line-1)"><div><img src="${pngURI(G.ballPNG({ px: 16, size: k }))}" width="16" height="16"><div class="small" style="margin-top:10px;color:var(--text)">${t}</div><div class="cap" style="margin-top:2px">${k === "display" ? "眼睛不足 2 px，边缘发灰" : "每只眼 3 × 5 px，落在整数像素上"}</div></div></div>`).join("")}
      </div>
      <div class="grid" style="grid-template-columns:repeat(2,1fr);margin-top:28px;column-gap:24px">
        <div class="plate" style="padding:26px 28px">
          <div class="h4">为什么眼睛不居中</div>
          <div class="row" style="--g:18px;margin-top:18px">${pauseFace(56)}${pauseFace(24)}<span class="cap" style="width:120px">居中、竖直的两只眼，就是暂停按钮。</span></div>
        </div>
        <div class="plate" style="padding:26px 28px">
          <div class="h4">偏移之后</div>
          <div class="row" style="--g:18px;margin-top:18px">${ball(56)}${ball(24)}<span class="cap" style="width:140px">视线偏向一侧，就只能读成“在看”。</span></div>
        </div>
      </div>
    </div>
  </div>`,
});

export const gaze = page({
  chapter: 2, id: "ball-gaze",
  body: `
  <div class="grid">
    <div class="span-4">${title("视线朝向内容", "The ball looks at the work")}
      <p class="lead">品牌符号的眼睛固定看向右上，也就是看向文字标识。版面里放小球时，让它看向这一页真正的内容。</p>
      <div class="rule-list" style="margin-top:36px;--rl:1fr">
        <div class="body"><strong>标识不镜像。</strong>标识、图标、头像都用固定的品牌姿态，眼睛永远偏右上。</div>
        <div class="body"><strong>构图可以借用状态姿态。</strong>插画和大幅面构图需要让小球看向左侧或下方时，从第 07 章的状态中选，不要自己摆眼睛。</div>
        <div class="body"><strong>不看镜头。</strong>正面直视用户的姿态只属于“需要你确认”这一个产品状态。</div>
      </div>
    </div>
    <div class="span-7 start-6">
      <div class="grid" style="grid-template-columns:repeat(2,1fr);column-gap:24px;row-gap:24px">
        <div class="plate" style="height:300px;padding:36px;position:relative"><span class="doy">✓</span>
          <div class="abs" style="left:48px;top:120px">${ball(84)}</div>
          <div class="abs" style="left:160px;top:118px;width:230px"><div class="h3">工作区已准备好</div><p class="small" style="margin-top:6px">连接模型服务后即可开始。</p></div>
          <div class="cap abs" style="left:24px;bottom:18px">小球在文字左侧，品牌姿态自然看向文字</div></div>
        <div class="plate" style="height:300px;padding:36px;position:relative"><span class="dontx">✕</span>
          <div class="abs" style="left:48px;top:118px;width:230px;text-align:right"><div class="h3">工作区已准备好</div><p class="small" style="margin-top:6px">连接模型服务后即可开始。</p></div>
          <div class="abs" style="left:300px;top:120px">${ball(84)}</div>
          <div class="cap abs" style="left:24px;bottom:18px">小球在右侧还看向右上：它背对内容</div></div>
        <div class="plate" style="height:300px;padding:36px;position:relative"><span class="doy">✓</span>
          <div class="abs" style="left:48px;top:118px;width:230px;text-align:right"><div class="h3">工作区已准备好</div><p class="small" style="margin-top:6px">连接模型服务后即可开始。</p></div>
          <div class="abs" style="left:300px;top:120px">${G.WuuBall.staticSVG({ px: 84, pose: { ...G.WuuBall.pose("display", "rest"), gazeX: -12 }, body: C.brand.ink, eye: C.brand.paper })}</div>
          <div class="cap abs" style="left:24px;bottom:18px">构图需要时：让小球看向左侧的内容</div></div>
        <div class="plate" style="height:300px;padding:36px;position:relative"><span class="doy">✓</span>
          <div class="abs" style="left:48px;top:56px">${staticBall(72, "work")}</div>
          <div class="abs" style="left:48px;top:150px;right:40px" class="stack">
            <div style="height:30px;border-radius:7px;background:var(--surface-2);display:flex;align-items:center;padding:0 12px" class="cap">读取 src/auth/session.ts</div>
            <div style="height:30px;border-radius:7px;background:var(--surface-2);display:flex;align-items:center;padding:0 12px;margin-top:8px" class="cap">运行 npm test</div></div>
          <div class="cap abs" style="left:24px;bottom:18px">工作中：小球看向下方正在进行的步骤</div></div>
      </div>
    </div>
  </div>`,
});

// Wordmark construction with metrics in x-height units.
function wordmarkConstruction(xh) {
  const wm = G.wordmarkPath();
  const W = T.wordmark, s = xh / 1000;
  const h = (wm.bottom - wm.top) * s, w = wm.width * s;
  const red = "#C2412F";
  const d = G.wordmarkSVG({ xh }).match(/d="([^"]+)"/)[1];
  const x = (u) => u * s, pad = 70;
  const stemEnd = W.stem, b0 = W.stem + W.wCounter, b1 = b0 + W.wMiddleStem, c1 = b1 + W.wCounter + W.stem;
  const u1 = c1 + W.wuSpace, u2 = u1 + W.uWidth + W.uuSpace;
  const lab = (px, py, t, a = "middle") => `<text x="${px}" y="${py}" fill="${red}" font-size="12" font-family="Hanken Grotesk, Noto Sans CJK SC" font-weight="500" text-anchor="${a}">${t}</text>`;
  const hl = (y, t) => `<line x1="${-pad + 20}" x2="${w + pad - 20}" y1="${y}" y2="${y}" stroke="${red}" stroke-width="1" stroke-dasharray="3 3"/>${lab(-pad + 20, y - 6, t, "start")}`;
  const dimH = (a, b, y, t) => `<line x1="${x(a)}" x2="${x(b)}" y1="${y}" y2="${y}" stroke="${red}"/><line x1="${x(a)}" x2="${x(a)}" y1="${y - 5}" y2="${y + 5}" stroke="${red}"/><line x1="${x(b)}" x2="${x(b)}" y1="${y - 5}" y2="${y + 5}" stroke="${red}"/>${lab((x(a) + x(b)) / 2, y + 18, t)}`;
  return `<svg width="${w + pad * 2}" height="${h + 160}" viewBox="${-pad} -60 ${w + pad * 2} ${h + 160}">
    <path d="${d}" transform="scale(${s})" fill="${C.brand.ink}"/>
    ${hl(0, "x 高度 X")}${hl(xh, "基线")}${hl(xh + W.overshoot * s, "")}
    <rect x="${x(u1)}" y="0" width="${x(W.uWidth)}" height="${xh + W.overshoot * s}" fill="none" stroke="${red}" stroke-width="1"/>
    ${lab(x(u1 + W.uWidth / 2), -14, "碗形模块 0.892 X")}
    ${dimH(0, stemEnd, xh + 40, "竖笔 0.243 X")}
    ${dimH(b0, b1, xh + 40, "中笔 0.211 X")}
    ${dimH(c1, u1, xh + 40, "0.13 X")}
    ${dimH(u1 + W.uWidth, u2, xh + 40, "0.12 X")}
    <line x1="${x(b0) - 8}" x2="${x(b1) + 8}" y1="${x(1000 * W.wMiddleCut)}" y2="${x(1000 * W.wMiddleCut)}" stroke="${red}"/>${lab(x(b1) + 14, x(1000 * W.wMiddleCut) + 4, "中笔截短 0.24 X", "start")}
  </svg>`;
}

export const wordmarkPage = page({
  chapter: 2, id: "wordmark",
  body: `
  <div class="grid">
    <div class="span-4">${title("文字标识", "The wordmark")}
      <p class="lead">使用自绘 “wuu” 字标，不用字体重新排。</p>
      <div class="rule-list" style="margin-top:36px;--rl:1fr">
        <div class="body"><strong>全小写。</strong>标识里只有 “wuu” 这一种写法；正文里的名称见“名称的写法”。</div>
        <div class="body"><strong>碗底。</strong>w 与 u 共用曲线。</div>
        <div class="body"><strong>笔端。</strong>平切，不改为圆头。</div>
        <div class="body"><strong>自绘，不是字体。</strong>不要用任何字体重新排 “wuu”；需要文字时用 Hanken Grotesk 写 “Wuu”。</div>
      </div>
    </div>
    <div class="span-8 start-5" style="padding-top:40px">
      ${wordmarkConstruction(250)}
      <div class="row" style="--g:56px;margin-top:64px;--a:flex-end">
        ${wordmark(64)}${wordmark(28)}${wordmark(14)}${wordmark(8)}
      </div>
    </div>
  </div>`,
});

function lockupDiagram(kind, xh) {
  const g = G.lockupGeometry(kind);
  const s = xh / g.X, red = "#C2412F";
  const svg = G.lockupSVG({ kind, xh });
  const W = g.width * s, H = (g.bottom - g.top) * s;
  const lab = (x, y, t, a = "middle") => `<text x="${x}" y="${y}" fill="${red}" font-size="11" font-family="Hanken Grotesk, Noto Sans CJK SC" font-weight="500" text-anchor="${a}">${t}</text>`;
  const L = T.lockup[kind];
  const ov = kind === "horizontal"
    ? `<line x1="${g.D * s}" x2="${(g.D + L.gap * g.X) * s}" y1="${H + 16}" y2="${H + 16}" stroke="${red}"/>${lab((g.D + L.gap * g.X / 2) * s, H + 32, `${L.gap} X`)}
       <line x1="-14" x2="-14" y1="0" y2="${H}" stroke="${red}"/>${lab(-20, H / 2 + 4, `球 ${L.ball} X`, "end")}
       <line x1="${g.word.x * s}" x2="${W}" y1="${(-g.X - g.top) * s}" y2="${(-g.X - g.top) * s}" stroke="${red}" stroke-dasharray="3 3"/><line x1="${g.word.x * s}" x2="${W}" y1="${(-g.top) * s}" y2="${(-g.top) * s}" stroke="${red}" stroke-dasharray="3 3"/>`
    : `<line x1="${W + 16}" x2="${W + 16}" y1="${g.D * s}" y2="${(g.D + L.gap * g.X) * s}" stroke="${red}"/>${lab(W + 22, (g.D + L.gap * g.X / 2) * s + 4, `${L.gap} X`, "start")}
       <line x1="${g.ball.x * s}" x2="${(g.ball.x + g.D) * s}" y1="-14" y2="-14" stroke="${red}"/>${lab(W / 2, -20, `球 ${L.ball} X`)}`;
  return `<div style="position:relative;display:inline-block">${svg}<svg class="abs" style="left:0;top:0;overflow:visible" width="${W}" height="${H}">${ov}</svg></div>`;
}

export const lockups = page({
  chapter: 2, id: "lockups",
  body: `
  <div class="grid">
    <div class="span-4">${title("组合方式", "Lockups")}
      <p class="lead">X 为字标 x 高度；小球与其垂直居中。</p>
      <table class="spec" style="margin-top:36px">
        <thead><tr><th>组合</th><th>用在</th></tr></thead>
        <tbody>
          <tr><td>横式</td><td>默认。网站导航、文档站、README、页脚、演示文稿。</td></tr>
          <tr><td>竖式</td><td>接近正方形的版面：启动画面、活动海报、贴纸。</td></tr>
          <tr><td>只用小球</td><td>名字已经出现在旁边时：应用内、浏览器标签、社交头像。</td></tr>
          <tr><td>只用字标</td><td>很少用。小球已经在同一视野里时，例如图标旁的应用名。</td></tr>
        </tbody>
      </table>
    </div>
    <div class="span-7 start-6">
      <div class="plate" style="height:330px;display:flex;align-items:center;justify-content:center">${lockupDiagram("horizontal", 72)}</div>
      <div class="grid" style="grid-template-columns:1.25fr 1fr 1fr;column-gap:24px;margin-top:24px">
        <div class="plate" style="height:330px;display:flex;align-items:center;justify-content:center">${lockupDiagram("stacked", 46)}</div>
        <div class="plate" style="height:330px;display:flex;align-items:center;justify-content:center">${ball(140)}</div>
        <div class="plate" style="height:330px;display:flex;align-items:center;justify-content:center">${wordmark(40)}</div>
      </div>
    </div>
  </div>`,
});

function clearDiagram() {
  const xh = 44, g = G.lockupGeometry("horizontal"), s = xh / g.X;
  const W = g.width * s, H = (g.bottom - g.top) * s, c = xh;
  return `<div style="position:relative;padding:${c}px;background:repeating-linear-gradient(45deg,rgba(194,65,47,.08) 0 6px,transparent 6px 12px);box-shadow:inset 0 0 0 1px rgba(194,65,47,.5)">
    <div style="background:var(--surface)">${G.lockupSVG({ xh })}</div>
    <div class="abs cap" style="left:${c + W / 2 - 40}px;top:${c / 2 - 8}px;color:#C2412F;width:80px;text-align:center">1 X</div>
    <div class="abs cap" style="left:${c / 2 - 12}px;top:${c + H / 2 - 8}px;color:#C2412F">1 X</div></div>`;
}

export const clearSpace = page({
  chapter: 2, id: "clear-space",
  body: `
  <div class="grid">
    <div class="span-4">${title("安全空间与最小尺寸", "Clear space and minimum sizes")}
      <p class="lead">安全空间里不放文字、图片边缘或其他标识。最小尺寸之下，改用只有小球的版本。</p>
      <table class="spec" style="margin-top:36px">
        <thead><tr><th>版本</th><th>安全空间</th><th>屏幕最小</th><th>印刷最小</th></tr></thead>
        <tbody>
          <tr><td>横式</td><td>1 X</td><td class="num">X ≥ ${T.lockup.horizontal.minXHeightPx} px（宽约 36 px）</td><td class="num">宽 ${T.lockup.horizontal.minWidthMm} mm</td></tr>
          <tr><td>竖式</td><td>1 X</td><td class="num">X ≥ ${T.lockup.stacked.minXHeightPx} px</td><td class="num">宽 ${T.lockup.stacked.minWidthMm} mm</td></tr>
          <tr><td>小球</td><td>0.25 D</td><td class="num">${T.lockup.symbol.minPx} px</td><td class="num">${T.lockup.symbol.minMm} mm</td></tr>
        </tbody>
      </table>
      <p class="body" style="margin-top:20px">16 px 的网站图标使用 Micro 版小球，并且不带背景色块。</p>
    </div>
    <div class="span-7 start-6">
      <div class="row" style="--g:48px;--a:flex-start">
        ${clearDiagram()}
        <div style="position:relative;padding:22px;background:repeating-linear-gradient(45deg,rgba(194,65,47,.08) 0 6px,transparent 6px 12px);box-shadow:inset 0 0 0 1px rgba(194,65,47,.5)"><div style="background:var(--surface)">${ball(88)}</div><div class="cap" style="color:#C2412F;margin-top:4px;position:absolute;left:0;right:0;bottom:-24px;text-align:center">0.25 D</div></div>
      </div>
      <div class="label" style="margin-top:72px">实际尺寸</div>
      <div class="plate" style="margin-top:14px;padding:32px 36px;display:flex;gap:56px;align-items:flex-end">
        <div>${lockup(7)}<div class="cap" style="margin-top:10px">横式最小 · X 7 px</div></div>
        <div>${lockup(12)}<div class="cap" style="margin-top:10px">导航常用 · X 12 px</div></div>
        <div>${G.lockupSVG({ kind: "stacked", xh: 9 })}<div class="cap" style="margin-top:10px">竖式最小</div></div>
        <div>${ball(12)}<div class="cap" style="margin-top:10px">小球最小 · 12 px</div></div>
        <div>${ball(16)}<div class="cap" style="margin-top:10px">网站图标 · 16 px</div></div>
      </div>
      <div class="label" style="margin-top:44px">在真实位置</div>
      <div class="row" style="--g:20px;margin-top:14px;--a:stretch">
        <div class="plate" style="padding:14px;background:#DEDEDA;width:230px"><div style="background:#fff;border-radius:8px 8px 0 0;padding:8px 10px;display:flex;gap:8px;align-items:center;font:500 12px var(--brand)">${ball(16)}Wuu 文档<span style="margin-left:auto;color:var(--text-4)">✕</span></div><div class="cap" style="margin-top:10px">浏览器标签 · 小球 16 px</div></div>
        <div class="plate" style="padding:0;width:420px;overflow:hidden;box-shadow:0 0 0 1px var(--line-1)"><div style="display:flex;align-items:center;gap:22px;padding:16px 20px;border-bottom:1px solid var(--line-1)">${lockup(12)}<span class="small" style="margin-left:auto">文档</span><span class="small">博客</span><span class="small" style="background:var(--text);color:var(--canvas);padding:5px 12px;border-radius:999px">下载</span></div><div class="cap" style="padding:10px 20px">网站导航 · 横式 X 12 px</div></div>
        <div class="plate" style="padding:14px 18px;width:190px;box-shadow:0 0 0 1px var(--line-1);display:flex;gap:12px;align-items:center"><div style="width:48px;height:48px;border-radius:50%;background:${C.brand.paper};box-shadow:0 0 0 1px var(--line-2);display:grid;place-items:center">${ball(36)}</div><div class="cap">社交头像<br>圆形裁切内留 0.25 D</div></div>
      </div>
    </div>
  </div>`,
});

export const colourVersions = page({
  chapter: 2, id: "logo-colour",
  body: `
  <div class="grid">
    <div class="span-3">${title("色彩版本", "Colour versions")}
      <p class="lead">标识只有墨与纸两种颜色。单色输出用镂空版：眼睛是透出的背景。</p>
      <p class="body" style="margin-top:20px">标识与背景的对比至少 <strong>3 : 1</strong>。放在照片上时，只放在平静、明度均匀的区域；做不到就先加一块纸色或墨色的底。</p>
    </div>
    <div class="span-9 start-4">
      <div class="grid" style="grid-template-columns:repeat(3,1fr);column-gap:20px;row-gap:20px">
        ${[
          ["主版本 · 墨色在纸色上", "var(--canvas)", lockup(26), `墨 ${C.brand.ink} · 纸 ${C.brand.paper} · ${cr(C.brand.ink, C.brand.paper)}`, "line"],
          ["反白 · 纸色在墨色上", C.dark.canvas, lockup(26, { ink: C.dark.ball, eye: C.dark["ball-eye"] }), `${C.dark.ball} on ${C.dark.canvas} · ${cr(C.dark.ball, C.dark.canvas)}`, ""],
          ["单色黑 · 镂空", "#FFFFFF", lockup(26, { ink: "#000", mode: "knockout" }), "打印、刻印、传真；眼睛透出底色", "line"],
          ["单色白 · 镂空", "#3B6FD8", lockup(26, { ink: "#FFFFFF", mode: "knockout" }), "放在合作方或深色品牌色上", ""],
          ["在 agent 色上", C.agent.mint.light, lockup(26), `墨色标识 · ${cr(C.brand.ink, C.agent.mint.light)}`, ""],
          ["在照片上", "url(reference/landing-home.jpg) 8% 92%/300%", `<div style="background:${C.brand.paper};padding:14px 18px;border-radius:10px">${lockup(20)}</div>`, "纹理复杂时，先放一块纸色底", ""],
        ].map(([t, bg, art, note, line]) => `<div><div class="plate center" style="height:220px;background:${bg};${line ? "box-shadow:inset 0 0 0 1px var(--line-2)" : ""}">${art}</div><div class="h4" style="margin-top:12px">${t}</div><div class="cap mono" style="margin-top:3px">${note}</div></div>`).join("")}
      </div>
    </div>
  </div>`,
});

function misuse(n, label, art, bg = "var(--surface)") {
  return `<div><div class="plate center" style="height:176px;background:${bg};position:relative;overflow:hidden"><span class="dontx">✕</span>${art}</div><div class="small" style="margin-top:10px"><span class="num" style="color:var(--text-3);margin-right:6px">${n}</span>${label}</div></div>`;
}
const wmOnly = (xh, o = {}) => wordmark(xh, o);

export const misuses = page({
  chapter: 2, id: "misuse",
  body: `
  <div class="grid">
    <div class="span-3">${title("误用", "Misuse")}
      <p class="lead">以下为禁用示例。</p>
      <p class="body" style="margin-top:20px">从现有资产、光学尺寸和状态姿态中选择。</p>
    </div>
    <div class="span-9 start-4">
      <div class="grid" style="grid-template-columns:repeat(4,1fr);column-gap:20px;row-gap:24px">
        ${misuse("01", "眼睛居中竖直，读成暂停键", pauseFace(84))}
        ${misuse("02", "旋转或倾斜标识", `<div style="transform:rotate(-14deg)">${lockup(18)}</div>`)}
        ${misuse("03", "加渐变、高光或投影", `<div style="filter:drop-shadow(0 10px 10px rgba(0,0,0,.28))"><svg width="84" height="84" viewBox="0 0 100 100"><defs><radialGradient id="mg" cx="35%" cy="30%"><stop offset="0" stop-color="#6d6d6a"/><stop offset="1" stop-color="#141411"/></radialGradient></defs><circle cx="50" cy="50" r="50" fill="url(#mg)"/><path d="${G.ballPaths("display").eyes}" fill="#fff"/></svg></div>`)}
        ${misuse("04", "给 Wuu 标识换成 agent 色", lockup(18, { ink: C.agent.sky.light, eye: C.brand.ink }))}
        ${misuse("05", "拉伸或压扁", `<div style="transform:scaleX(1.45)">${ball(70)}</div>`)}
        ${misuse("06", "在小尺寸或标识中裁掉眼睛", `<div style="width:84px;height:84px;overflow:hidden;border-radius:16px;background:var(--surface-2);position:relative"><div class="abs" style="left:-30px;top:34px">${ball(110)}</div></div>`)}
        ${misuse("07", "加嘴、腮红、手脚或台词", `<svg width="84" height="84" viewBox="0 0 100 100"><path d="${G.ballPaths("display").body}" fill="${C.brand.ink}"/><path d="${G.ballPaths("display").eyes}" fill="#fff"/><path d="M52 64 Q60 72 70 64" stroke="#fff" stroke-width="4" fill="none" stroke-linecap="round"/><circle cx="42" cy="58" r="6" fill="#FF8A9B" opacity=".8"/><circle cx="84" cy="58" r="6" fill="#FF8A9B" opacity=".8"/></svg>`)}
        ${misuse("08", "用字体重排字标或首字母大写", `<div class="row" style="--g:10px">${ball(34)}<span style="font:700 34px/1 Georgia, serif">Wuu</span></div>`)}
        ${misuse("09", "改变球与字标的比例或间距", `<div class="row" style="--g:4px">${ball(20)}${wmOnly(20)}</div>`)}
        ${misuse("10", "描边或线框化", `<svg width="84" height="84" viewBox="0 0 100 100"><circle cx="50" cy="50" r="48" fill="none" stroke="${C.brand.ink}" stroke-width="3"/><path d="${G.ballPaths("display").eyes}" fill="none" stroke="${C.brand.ink}" stroke-width="3"/></svg>`)}
        ${misuse("11", "放在对比不足的背景上", lockup(18, { ink: "#8B8B86", eye: "#B9B9B4" }), "#A3A39E")}
        ${misuse("12", "给品牌符号戴产品配饰", `<div style="position:relative">${ball(80)}<svg class="abs" style="left:6px;top:-18px" width="70" height="40" viewBox="0 0 70 40"><path d="M6 34 Q8 6 35 4 Q62 6 64 34 Z" fill="#8FD5FD" stroke="#141411" stroke-width="3"/><rect x="2" y="28" width="66" height="10" rx="4" fill="#BDE6FF" stroke="#141411" stroke-width="3"/></svg></div>`)}
      </div>
    </div>
  </div>`,
});

export const naming = page({
  chapter: 2, id: "naming",
  body: `
  <div class="grid">
    <div class="span-4">${title("名称的写法", "Writing the name")}
      <p class="lead">正文写 Wuu；命令、包名和字标写 <code>wuu</code>。</p>
    </div>
    <div class="span-7 start-6">
      <table class="spec">
        <thead><tr><th style="width:170px">场景</th><th style="width:220px">写法</th><th>示例</th></tr></thead>
        <tbody>
          <tr><td>中文与英文正文、标题、界面</td><td><strong style="color:var(--text)">Wuu</strong></td><td>在 Wuu 里打开项目。<br>Open a project in Wuu.</td></tr>
          <tr><td>命令、包名、路径</td><td><code>wuu</code>（等宽字体）</td><td>运行 <code>wuu exec</code>；配置在 <code>~/.wuu</code>。</td></tr>
          <tr><td>标识</td><td>自绘字标 “wuu”</td><td>只使用第 03 章的资产。</td></tr>
          <tr><td>英文所有格</td><td>Wuu’s</td><td>Wuu’s permission modes</td></tr>
          <tr><td>应用名、窗口标题</td><td>Wuu</td><td>macOS 菜单栏与 Dock 中显示 “Wuu”。</td></tr>
        </tbody>
      </table>
      <div class="label" style="margin-top:44px">不要这样写</div>
      <div class="row" style="--g:14px;margin-top:14px;flex-wrap:wrap">
        ${["WUU", "wuu（正文中）", "Wuu AI", "悟 / 呜（音译）", "W.U.U."].map((t) => `<span class="plate" style="padding:10px 16px;box-shadow:inset 0 0 0 1px var(--line-2)"><span class="small" style="text-decoration:line-through;text-decoration-color:var(--danger)">${t}</span></span>`).join("")}
      </div>
      <div class="grid" style="grid-template-columns:1fr 1fr;column-gap:24px;margin-top:40px">
        <div class="plate" style="padding:24px 26px"><div class="label">示例 · 中文</div><p class="body" style="margin-top:10px;color:var(--text)">Wuu 是在本地项目里和 AI agent 一起工作的桌面应用。安装后，在终端里也可以运行 <code>wuu exec</code>，会话保存在 <code>~/.wuu</code>。</p></div>
        <div class="plate" style="padding:24px 26px"><div class="label">Example · English</div><p class="body" style="margin-top:10px;color:var(--text)">Wuu is a desktop app for working with AI agents on local projects. After installing, you can also run <code>wuu exec</code> in a terminal; sessions live in <code>~/.wuu</code>.</p></div>
      </div>
      <p class="cap" style="margin-top:28px">当前仓库的 README、官网和站点标题用的是小写 “wuu”，采用本手册后需要统一修改；这不在本次交付范围内。名称没有官方中文译名，也不要自行音译。</p>
    </div>
  </div>`,
});

export default [roles, construction, optical, gaze, wordmarkPage, lockups, clearSpace, colourVersions, misuses, naming];
