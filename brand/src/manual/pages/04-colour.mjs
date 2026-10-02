// Colour: roles, neutral ramps, agent colours, status colours, proportions.
import { page, title, ball, lockup, C, G, T, staticBall, agentBall, AGENTS, cr, contrast } from "../kit.mjs";

const L = C.light, D = C.dark;
const chip = (hex, w = 64, h = 64, r = 8) => `<div class="sw" style="width:${w}px;height:${h}px;border-radius:${r}px;background:${hex}"></div>`;

export const roles = page({
  chapter: 3, id: "colour-roles",
  body: `
  <div class="grid">
    <div class="span-4">${title("色彩用途", "Colour roles")}
      <p class="lead">品牌、中性色、agent、状态与交互。</p>

    </div>
    <div class="span-7 start-6">
      <table class="spec">
        <thead><tr><th style="width:190px">组</th><th style="width:220px">颜色</th><th>可以</th><th>不可以</th></tr></thead>
        <tbody>
          <tr><td><div class="h4">品牌 · 墨与纸</div></td><td><div class="row" style="--g:6px">${chip(C.brand.ink, 40, 40)}${chip(C.brand.paper, 40, 40)}</div></td><td>标识、Wuu 的小球、主按钮、正文</td><td>不换成其他颜色，也不做渐变</td></tr>
          <tr><td><div class="h4">中性色阶</div></td><td><div class="row" style="--g:4px">${["canvas", "surface-2", "surface-3", "line-2", "text-3", "text-2"].map((k) => chip(L[k], 26, 40, 6)).join("")}</div></td><td>底色、表面、分隔线、次要文字、选中态</td><td>不用低对比灰字承载必要信息</td></tr>
          <tr><td><div class="h4">agent 色</div></td><td><div class="row" style="--g:4px">${AGENTS.map((a) => chip(C.agent[a].light, 26, 40, 13)).join("")}</div></td><td>其他 agent 的球身、插画、按 agent 区分的图表</td><td>不上文字、按钮、状态和 Wuu 自己</td></tr>
          <tr><td><div class="h4">功能状态色</div></td><td><div class="row" style="--g:4px">${["success", "warning", "danger", "info"].map((k) => chip(C.status[k].light, 40, 40, 6)).join("")}</div></td><td>成功、警告、错误、信息；总是配文字或图标</td><td>不做品牌装饰，不单独用颜色表达</td></tr>
          <tr><td><div class="h4">交互</div></td><td><div class="row" style="--g:6px">${chip(C.interaction.primary.light, 40, 40)}<div class="sw" style="width:40px;height:40px;border-radius:8px;box-shadow:0 0 0 2px ${C.interaction.focus.light};background:#fff"></div></div></td><td>主按钮用墨色；键盘焦点用 2 px 蓝色环</td><td>不为每个选中项加彩色</td></tr>
        </tbody>
      </table>

    </div>
  </div>
  <div class="abs" style="left:80px;right:80px;bottom:96px">
    <div class="label" style="margin-bottom:12px">一个界面里的五组颜色（示意）</div>
    <div class="plate" style="height:250px;display:grid;grid-template-columns:230px 1fr 280px;overflow:hidden;box-shadow:0 0 0 1px var(--line-1);font:400 13px/1.5 var(--brand)">
      <div style="background:var(--canvas);border-right:1px solid var(--line-1);padding:18px 14px;position:relative">${lockup(10)}<span class="abs tag" style="left:120px;top:16px">1</span>
        <div class="small" style="margin-top:22px">新对话</div><div class="small" style="margin-top:6px;background:var(--surface-3);border-radius:6px;padding:4px 8px;margin-left:-8px">整理登录流程</div><div class="small" style="margin-top:6px;color:var(--text-3)">修复导航重复</div><span class="abs tag" style="left:200px;top:82px">2</span></div>
      <div style="padding:20px 24px;position:relative">
        <div class="row" style="--g:10px;--a:flex-start">${staticBall(24, "rest")}<div class="small" style="color:var(--text)">已修改 3 个文件。子 agent 发现 1 个测试失败。</div></div>
        <div class="row" style="--g:8px;margin-top:14px;margin-left:34px">${agentBall(18, "peach", false, "failed")}<span class="tag">3</span><span class="small">运行测试</span><span class="small" style="color:${C.status.danger.light};font-weight:600;margin-left:8px">✕ 失败</span><span class="tag">4</span></div>
        <div class="abs" style="left:24px;right:24px;bottom:18px;border:1px solid var(--line-2);border-radius:14px;padding:10px 14px;display:flex;justify-content:space-between;align-items:center;color:var(--text-4);box-shadow:0 0 0 2px var(--canvas),0 0 0 4px ${C.interaction.focus.light}">描述要做的事<span style="width:24px;height:24px;border-radius:50%;background:var(--text);color:var(--canvas);display:grid;place-items:center;font-size:12px">↑</span></div><span class="abs tag" style="right:30px;bottom:66px">5</span>
      </div>
      <div style="border-left:1px solid var(--line-1);padding:18px 20px" class="stack">
        ${[["1", "品牌：标识与 Wuu 的小球"], ["2", "中性色：底色、选中、次要文字"], ["3", "agent 色：子 agent 的球身"], ["4", "状态色：失败，配文字与图标"], ["5", "交互：墨色主按钮、蓝色焦点环"]].map(([n, t]) => `<div class="row" style="--g:10px;margin-top:8px"><span class="tag">${n}</span><span class="small">${t}</span></div>`).join("")}
      </div>
    </div>
  </div>`,
});

function ramp(set, dark) {
  const rows = [
    ["canvas", "底色", "页面与窗口底色"], ["surface", "表面", "卡片、输入框、弹出层"], ["surface-2", "表面 2", "分组、次级区域"], ["surface-3", "表面 3 · 选中", "选中行、按下态"],
    ["line-1", "分隔线", "同一表面内的分隔"], ["line-2", "边框", "独立区块的边界"], ["boundary", "控件边界", "输入框等需要 3 : 1 的边界"],
    ["text-4", "文字 4", "占位文字、禁用"], ["text-3", "文字 3", "说明、元信息"], ["text-2", "文字 2", "次要正文"], ["text", "文字", "标题与正文"],
  ];
  const bg = set.canvas;
  return `<div class="grid" style="grid-template-columns:repeat(11,1fr);column-gap:10px">
    ${rows.map(([k, zh, role]) => { const r = contrast(set[k], k.startsWith("surface") || k === "canvas" ? set.text : bg); return `<div>
      <div class="sw" style="height:150px;background:${set[k]};box-shadow:inset 0 0 0 1px ${dark ? "rgba(255,255,255,.07)" : "rgba(20,20,17,.07)"}"></div>
      <div class="h4" style="margin-top:10px">${zh}</div>
      <div class="cap mono" style="margin-top:2px">${set[k]}</div>
      <div class="cap num" style="margin-top:2px">${k.startsWith("surface") || k === "canvas" ? `文字 ${r.toFixed(1)}:1` : `${r.toFixed(2)}:1`}</div>
      <div class="cap" style="margin-top:6px;font-weight:400;line-height:1.45">${role}</div>
      <div class="cap mono" style="margin-top:6px;opacity:.7;font-size:10.5px">${k}</div></div>`; }).join("")}
  </div>`;
}

const hueCompare = () => {
  const sets = [["偏暖 · 米色", "#F5F1E8", "#E9E3D5", "#1C1A15"], ["石色 · 采用", L.canvas, L["surface-3"], L.text], ["偏冷 · 蓝灰", "#F5F7FA", "#E4E7EC", "#14171C"]];
  return `<div class="row" style="--g:16px">${sets.map(([n, bg, s, ink]) => `<div><div style="width:150px;height:96px;border-radius:10px;background:${bg};box-shadow:inset 0 0 0 1px rgba(20,20,17,.07);position:relative;overflow:hidden"><div class="abs" style="left:14px;top:14px">${G.ballSVG({ px: 28, body: ink, eye: bg })}</div><div class="abs" style="left:14px;right:14px;bottom:14px;height:26px;border-radius:6px;background:${s}"></div></div><div class="cap" style="margin-top:8px;${n.includes("采用") ? "color:var(--text);font-weight:600" : ""}">${n}</div></div>`).join("")}</div>`;
};

export const neutralsLight = page({
  chapter: 3, id: "neutrals-light",
  body: `
  <div class="grid">
    <div class="span-5">${title("中性色 · 亮色", "Neutrals, light")}
      <p class="lead">石色：色相 100°，彩度约 0.004。各级对比度见下表。</p>
    </div>
    <div class="span-6 start-7" style="padding-top:8px">
      <div class="label">底色比较</div>

      <div style="margin-top:16px">${hueCompare()}</div>
    </div>
  </div>
  <div style="margin-top:44px">${ramp(L, false)}</div>
  <p class="cap" style="margin-top:22px">对比度按 WCAG 2 计算：表面一栏为正文文字在其上的对比度，其余为与底色 <span class="mono">${L.canvas}</span> 的对比度。正文至少 4.5 : 1，大字与控件边界至少 3 : 1。</p>`,
});

export const neutralsDark = page({
  chapter: 3, id: "neutrals-dark", dark: true,
  body: `
  <div class="grid">
    <div class="span-5">${title("中性色 · 暗色", "Neutrals, dark")}
      <p class="lead">底色接近墨色，表面逐级提亮；正文不用纯白。</p>
    </div>
    <div class="span-6 start-7" style="padding-top:8px">
      <div class="label">在暗色里</div>
      <div class="rule-list" style="margin-top:10px;--rl:1fr">
        <div class="body">Wuu 的小球换成纸色 <span class="mono">${D.ball}</span>，眼睛用底色。</div>
        <div class="body">表面层级靠明度区分，不靠阴影；阴影只留给弹出层和对话框。</div>
      </div>
      <div class="row" style="--g:18px;margin-top:18px">${G.ballSVG({ px: 48, body: D.ball, eye: D["ball-eye"] })}${lockup(16, { ink: D.ball, eye: D["ball-eye"] })}</div>
    </div>
  </div>
  <div style="margin-top:44px">${ramp(D, true)}</div>
  <p class="cap" style="margin-top:22px">表面一栏为正文文字在其上的对比度，其余为与底色 <span class="mono">${D.canvas}</span> 的对比度。</p>`,
});

export const agents = page({
  chapter: 3, id: "agent-colours",
  body: `
  <div class="grid">
    <div class="span-4">${title("agent 色", "Agent colours")}
      <p class="lead">七种浅色，OKLCH 明度 0.84；眼睛用墨色。</p>
      <div class="rule-list" style="margin-top:32px;--rl:1fr">
        <div class="body"><strong>稳定分配。</strong>同一个服务商或 agent 在所有会话里颜色不变；按标识符散列到七色之一。</div>
        <div class="body"><strong>Wuu 不参与分配。</strong>亮色主题用墨色，暗色主题用纸色。</div>
        <div class="body"><strong>只在球和插画上。</strong>不做文字色、按钮色、选中色或状态色。需要标注 agent 名字时，文字用中性色。</div>
        <div class="body"><strong>超过七个时</strong>，靠形体与名字区分，不再增加颜色。</div>
      </div>
    </div>
    <div class="span-7 start-6">
      <div class="grid" style="grid-template-columns:repeat(7,1fr);column-gap:14px">
        ${AGENTS.map((a) => `<div>
          <div class="center" style="height:110px">${agentBall(84, a)}</div>
          <div class="sw" style="height:60px;background:${C.agent[a].light};margin-top:8px"></div>
          <div style="background:${D.canvas};border-radius:10px;padding:6px;margin-top:6px"><div class="sw" style="height:48px;background:${C.agent[a].dark};box-shadow:none;border-radius:6px"></div></div>
          <div class="h4" style="margin-top:10px;text-transform:capitalize">${a}</div>
          <div class="cap mono" style="margin-top:2px">${C.agent[a].light}</div>
          <div class="cap mono">${C.agent[a].dark} 暗</div>
          <div class="cap num" style="margin-top:4px">眼 ${cr(C.brand.ink, C.agent[a].light)}</div></div>`).join("")}
      </div>
      <div class="plate ink" style="margin-top:32px;padding:26px 30px;display:flex;align-items:center;gap:16px">
        ${G.ballSVG({ px: 56, body: D.ball, eye: D["ball-eye"] })}${AGENTS.map((a) => agentBall(44, a, true)).join("")}
        <span class="cap" style="margin-left:auto;color:var(--d-text-2)">暗色主题使用略暗的一组，保持与底色的明度差</span>
      </div>
    </div>
  </div>`,
});

export const status = page({
  chapter: 3, id: "status-colours",
  body: `
  <div class="grid">
    <div class="span-4">${title("状态色与品牌色", "Status next to brand")}
      <p class="lead">状态用深色、小面积，并配文字或图标。</p>
      <div class="rule-list" style="margin-top:32px;--rl:1fr">
        <div class="body"><strong>失败时球不变色。</strong>失败由眼睛姿态、红色文字和图标表示。</div>
        <div class="body"><strong>不只靠颜色区分状态。</strong>始终配文字或图标。</div>
        <div class="body"><strong>浅底只做提示条。</strong>浅色状态底配同色相深色文字，对比度不低于 4.5 : 1。</div>
      </div>
    </div>
    <div class="span-7 start-6">
      <div class="grid" style="grid-template-columns:repeat(4,1fr);column-gap:16px">
        ${[["success", "成功"], ["warning", "警告"], ["danger", "错误"], ["info", "信息"]].map(([k, zh]) => `<div>
          <div class="sw" style="height:72px;background:${C.status[k].light}"></div>
          <div class="sw" style="height:44px;background:${C.status[k].soft};margin-top:6px;display:flex;align-items:center;padding:0 12px"><span class="small" style="color:${C.status[k].light};font-weight:600">${zh}</span></div>
          <div class="h4" style="margin-top:10px">${zh} <span class="cap mono">${k}</span></div>
          <div class="cap mono" style="margin-top:2px">${C.status[k].light} · ${cr(C.status[k].light, L.canvas)}</div>
          <div class="cap mono">浅底文字 ${cr(C.status[k].light, C.status[k].soft)}</div>
          <div class="cap mono">暗色 ${C.status[k].dark} · ${cr(C.status[k].dark, D.canvas)}</div></div>`).join("")}
      </div>
      <div class="plate" style="margin-top:32px;padding:26px 30px;box-shadow:0 0 0 1px var(--line-1)">
        <div class="label" style="margin-bottom:16px">示例 · 一轮任务中的失败</div>
        ${[
          [staticBall(24, "work"), "Wuu", "正在把任务分给 2 个子 agent", "var(--text-3)", ""],
          [agentBall(22, "sky", false, "rest"), "检查类型", "完成，没有发现问题", "var(--text-2)", `<span style="color:${C.status.success.light}">✓ 通过</span>`],
          [agentBall(22, "peach", false, "failed"), "运行测试", "2 个测试失败：<span class=\"mono\">session.test.ts</span>", "var(--text-2)", `<span style="color:${C.status.danger.light}">✕ 失败 · 退出码 1</span>`],
        ].map(([b, who, what, c, st]) => `<div class="row" style="--g:12px;padding:10px 0;border-top:1px solid var(--line-1)">${b}<span class="h4" style="width:84px">${who}</span><span class="small" style="color:${c};flex:1">${what}</span><span class="small" style="font-weight:600">${st}</span></div>`).join("")}
        <div style="margin-top:12px;background:${C.status.danger.soft};color:${C.status.danger.light};border-radius:8px;padding:10px 14px" class="small"><strong>测试没有通过。</strong>改动已保留，可以查看失败输出，或让 Wuu 修复后重跑。</div>
      </div>
    </div>
  </div>`,
});

function bar(parts) {
  return `<div style="display:flex;height:28px;border-radius:6px;overflow:hidden;border:1px solid var(--line-2)">${parts.map(([w, c]) => `<div style="width:${w}%;background:${c};box-shadow:inset -1px 0 0 rgba(20,20,17,.12)"></div>`).join("")}</div>`;
}

export const proportions = page({
  chapter: 3, id: "colour-proportions",
  body: `
  <div class="grid">
    <div class="span-4">${title("用量比例", "Proportions")}
      <p class="lead">比例可按内容调整。大面积彩色背景仅用于插画和社交图。</p>
    </div>
    <div class="span-7 start-6">
      ${[
        ["产品界面", "内容密集，长时间使用", [[78, L.canvas], [12, L.surface], [7, L.text], [2, C.agent.sky.light], [1, C.status.danger.light]], "中性 90 · 墨 7 · agent 与状态 ≤ 3"],
        ["文档站", "阅读优先", [[82, L.canvas], [8, L["surface-2"]], [9, L.text], [1, C.status.info.light]], "中性 90 · 墨 9 · 链接与提示 1"],
        ["官网首页", "需要被记住", [[64, L.canvas], [22, C.brand.ink], [8, L["surface-2"]], [6, C.agent.peach.light]], "纸 64 · 墨 22 · agent 色 ≤ 6"],
        ["社交图与插画", "允许一块 agent 色做底", [[50, C.agent.mint.light], [30, L.canvas], [20, C.brand.ink]], "一种 agent 色 ≤ 50 · 墨 20"],
      ].map(([zh, note, parts, rule]) => `<div class="row" style="--g:28px;padding:22px 0;border-bottom:1px solid var(--line-1)">
        <div style="width:170px"><div class="h4">${zh}</div><div class="cap">${note}</div></div>
        <div style="flex:1">${bar(parts)}<div class="cap num" style="margin-top:8px">${rule}</div></div></div>`).join("")}
      <div class="grid" style="grid-template-columns:repeat(2,1fr);column-gap:24px;margin-top:32px">
        <div class="plate" style="height:190px;position:relative;overflow:hidden;background:${C.agent.mint.light}"><span class="doy">✓</span>
          <div class="abs" style="right:36px;top:40px">${ball(110)}</div><div class="abs" style="left:28px;bottom:26px;font:600 22px/1.2 var(--brand)">一块 agent 色做底，<br>墨色的 Wuu 在上面</div></div>
        <div class="plate" style="height:190px;position:relative;overflow:hidden;background:linear-gradient(90deg,${C.agent.peach.light},${C.agent.iris.light},${C.agent.mint.light})"><span class="dontx">✕</span>
          <div class="abs" style="right:36px;top:40px">${ball(110)}</div><div class="abs" style="left:28px;bottom:26px;font:600 22px/1.2 var(--brand)">多种 agent 色拼成<br>渐变背景</div></div>
      </div>
    </div>
  </div>`,
});

export default [roles, neutralsLight, neutralsDark, agents, status, proportions];
