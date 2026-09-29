// Motion: character, states, identity vs feedback, reduced motion.
import { page, title, ball, lockup, icon, C, G, T, staticBall, agentBall } from "../kit.mjs";

const W = G.WuuBall;
const M = T.motion;
const frame = (px, o = {}) => W.staticSVG({ px, body: o.body ?? C.brand.ink, eye: o.eye ?? C.brand.paper, ...o });

function curve(bez, label, dur) {
  const [x1, y1, x2, y2] = bez.match(/[\d.]+/g).map(Number);
  const S = 150;
  return `<div><svg width="${S + 20}" height="${S + 20}" viewBox="-10 -10 ${S + 20} ${S + 20}"><rect x="0" y="0" width="${S}" height="${S}" fill="none" stroke="${C.light["line-2"]}"/>
    <line x1="0" y1="${S}" x2="${x1 * S}" y2="${S - y1 * S}" stroke="${C.light["text-4"]}" stroke-width="1"/><line x1="${S}" y1="0" x2="${x2 * S}" y2="${S - y2 * S}" stroke="${C.light["text-4"]}" stroke-width="1"/>
    <path d="M0 ${S}C${x1 * S} ${S - y1 * S} ${x2 * S} ${S - y2 * S} ${S} 0" fill="none" stroke="${C.brand.ink}" stroke-width="2.5"/></svg>
    <div class="h4" style="margin-top:6px">${label}</div><div class="cap mono" style="margin-top:2px">${bez}</div><div class="cap" style="margin-top:2px">${dur}</div></div>`;
}

export const character = page({
  chapter: 6, id: "motion-character",
  body: `
  <div class="grid">
    <div class="span-4">${title("运动性格", "How the ball moves")}
      <p class="lead">小球像一个有分量、很专注的东西：眼睛先动，身体几乎不动。它的动作短、少，而且每一个动作都对应一件真实发生的事。</p>
      <table class="spec" style="margin-top:32px">
        <thead><tr><th>动作</th><th>时长</th><th>什么时候</th></tr></thead>
        <tbody>
          <tr><td>视线转移</td><td class="num">${M.duration.base} ms · ease-out</td><td>状态改变</td></tr>
          <tr><td>眨眼</td><td class="num">${M.ball.blink.close} + ${M.ball.blink.hold} + ${M.ball.blink.open} ms</td><td>空闲、聆听、等你确认时；间隔 ${M.ball.blink.intervalMin / 1000}–${M.ball.blink.intervalMax / 1000} s 随机</td></tr>
          <tr><td>回落</td><td class="num">${M.duration.slow} ms · 压扁 ≤ ${M.ball.settle.squash * 100}%</td><td>一轮任务完成，只发生一次</td></tr>
          <tr><td>工作中微动</td><td class="num">周期 ${M.ball.maxLoop} ms</td><td>思考与执行期间；隐藏或完成即停止</td></tr>
        </tbody>
      </table>
      <p class="body" style="margin-top:18px">时长与进入、离开缓动沿用产品现有的 <span class="mono">--motion-*</span>、<span class="mono">--ease-out</span> 与 <span class="mono">--ease-in</span>；只新增一条用于回落的 ease-in-out。</p>
    </div>
    <div class="span-7 start-6">
      <div class="plate" style="padding:32px 36px">
        <div class="label">实时示例 · 浏览器中播放；PDF 中为第一帧</div>
        <div class="row" style="--g:40px;margin-top:22px;--a:flex-end">
          <div data-wuu-live="rest,think,work,done,rest,wait" data-px="120" data-every="2200"></div>
          <div data-wuu-live="work,done,rest" data-px="40" data-every="2000"></div>
          <div data-wuu-live="rest,failed,rest" data-px="20" data-every="2400"></div>
          <div class="cap" style="margin-left:auto;width:200px">120 px 展示版在六个状态间循环；40 px 与 20 px 自动使用小尺寸眼睛。</div>
        </div>
      </div>
      <div class="plate" style="padding:28px 36px;margin-top:24px">
        <div class="label">缓动</div>
        <div class="row" style="--g:40px;margin-top:18px;--a:flex-start">
          ${curve(M.easing.out, "进入 · ease-out", `视线、出现 · ${M.duration.base} ms`)}
          ${curve(M.easing.in, "离开 · ease-in", `消失，比进入短 · ${M.duration.fast} ms`)}
          ${curve(M.easing.inOut, "位置 · ease-in-out", `回落与结构移动 · ${M.duration.slow} ms`)}
        </div>
      </div>
    </div>
  </div>`,
});

const MAP = [
  ["rest", "空闲", "idle · responding", "面向右上，偶尔眨眼", "空闲、回复正在输出"],
  ["listen", "聆听", "compose · sending", "看向下方的输入框", "你正在输入"],
  ["think", "思考", "thinking · compact", "看向左上，缓慢漂移", "推理与整理上下文"],
  ["work", "工作", "search · edit · command · read · tool", "看向下方的步骤，左右扫视", "执行工具与命令"],
  ["wait", "等你确认", "waiting", "转向你，眼睛略微睁大", "需要授权或回答"],
  ["done", "完成", "一轮结束", "一次眨眼 + 一次回落", "一轮任务完成"],
  ["failed", "失败", "failed", "眼睛半闭、低头，不晃动", "任务失败"],
  ["paused", "暂停", "interrupted · queued", "眼睛几乎闭上", "被中断或排队中"],
];

export const states = page({
  chapter: 6, id: "motion-states",
  body: `
  <div class="grid">
    <div class="span-3">${title("状态", "States")}
      <p class="lead">产品现有的 15 种活动归为 8 个状态姿态。每个状态只改变眼睛；颜色和形体不变。</p>
      <p class="body" style="margin-top:20px">产品里的活动图形（过程行中的终端提示符、铅笔、弧线）是功能反馈，由产品维护，不在品牌资产里重画。</p>
    </div>
    <div class="span-9 start-4">
      <table class="spec" style="table-layout:fixed">
        <thead><tr><th style="width:170px">姿态</th><th style="width:110px">状态</th><th style="width:210px">对应产品活动</th><th>眼睛怎么动</th><th style="width:150px">含义</th></tr></thead>
        <tbody>
          ${MAP.map(([k, zh, product, how, meaning]) => `<tr><td style="padding:10px 12px 10px 0"><div class="row" style="--g:12px">${frame(56, { state: k })}${frame(28, { state: k })}${frame(16, { state: k })}</div></td><td><span class="h4">${zh}</span><div class="cap mono">${k}</div></td><td class="mono" style="font-size:12px">${product}</td><td>${how}</td><td>${meaning}</td></tr>`).join("")}
        </tbody>
      </table>
    </div>
  </div>`,
});

// Storyboards sample the same functions the runtime uses.
const blinkFrames = [0, 30, 60, 100, 130, 190].map((t) => [t, frame(64, { pose: { ...W.pose("display", "rest"), scaleY: W.blink(t) } })]);
const settleFrames = [0, 70, 140, 210, 280].map((t) => [t, frame(64, { state: "done", squash: W.settle(t) })]);
const failFrames = [0, 60, 120, 180].map((t) => { const k = 1 - Math.pow(1 - t / 180, 3); const a = W.pose("display", "work"), b = W.pose("display", "failed"); return [t, frame(64, { pose: { gazeX: a.gazeX + (b.gazeX - a.gazeX) * k, gazeY: a.gazeY + (b.gazeY - a.gazeY) * k, scaleY: a.scaleY + (b.scaleY - a.scaleY) * k, roll: a.roll + (b.roll - a.roll) * k } })]; });
const strip = (frames) => `<div class="row" style="--g:22px;--a:flex-end">${frames.map(([t, svg]) => `<div style="text-align:center">${svg}<div class="cap num" style="margin-top:8px">${t} ms</div></div>`).join("")}</div>`;

export const boundaries = page({
  chapter: 6, id: "motion-boundaries",
  body: `
  <div class="grid">
    <div class="span-4">${title("识别与反馈的边界", "Identity or feedback")}
      <p class="lead">小球在两种情况下出现：作为品牌符号，它几乎不动；作为工作中的 Wuu，它用眼睛报告状态。两种用法不能互换。</p>
      <table class="spec" style="margin-top:32px">
        <thead><tr><th></th><th>品牌符号</th><th>工作中的 Wuu</th></tr></thead>
        <tbody>
          <tr><td>位置</td><td>标识、图标、导航、文档</td><td>会话头像、首页问候</td></tr>
          <tr><td>动作</td><td>静止；官网首屏可以进场一次（一次眨眼 + 回落）</td><td>随状态改变眼睛</td></tr>
          <tr><td>跟随指针</td><td>只在官网首屏，只动眼睛</td><td>不跟随</td></tr>
          <tr><td>循环</td><td>无</td><td>仅思考与工作期间，≤ ${M.ball.maxLoop} ms 周期</td></tr>
        </tbody>
      </table>
      <p class="body" style="margin-top:18px"><strong>不要</strong>把小球当作下载、保存等通用操作的加载图标，也不要在按钮悬停时让它跳动。</p>
    </div>
    <div class="span-7 start-6 stack" style="--s:24px">
      <div class="plate" style="padding:26px 32px"><div class="row" style="--g:10px;--a:baseline"><span class="h4">眨眼</span><span class="cap">${M.ball.blink.close} 闭 · ${M.ball.blink.hold} 停 · ${M.ball.blink.open} 开</span></div><div style="margin-top:16px">${strip(blinkFrames)}</div></div>
      <div class="plate" style="padding:26px 32px"><div class="row" style="--g:10px;--a:baseline"><span class="h4">完成时的回落</span><span class="cap">以底部为锚点，宽 +${M.ball.settle.squash * 100}%、高 −${M.ball.settle.squash * 100}%，只发生一次</span></div><div style="margin-top:16px">${strip(settleFrames)}</div></div>
      <div class="plate" style="padding:26px 32px"><div class="row" style="--g:10px;--a:baseline"><span class="h4">从工作到失败</span><span class="cap">${M.duration.base} ms 转到失败姿态；不抖动、不变红</span></div><div style="margin-top:16px">${strip(failFrames)}</div></div>
    </div>
  </div>`,
});

export const reduced = page({
  chapter: 6, id: "motion-reduced",
  body: `
  <div class="grid">
    <div class="span-4">${title("减少动态效果", "Reduced motion")}
      <p class="lead">系统的“减少动态效果”和应用内的动效设置效果相同：状态直接切换到最终姿态，没有眨眼、回落和循环。</p>
      <div class="rule-list" style="margin-top:32px;--rl:1fr">
        <div class="body"><strong>姿态保留。</strong>静止的眼睛仍然表达状态，所以信息不会丢失。</div>
        <div class="body"><strong>文字始终在场。</strong>状态旁边总有文字，例如“正在运行测试”，不依赖动画。</div>
        <div class="body"><strong>不做替代动画。</strong>不改成闪烁、淡入淡出循环或颜色变化。</div>
      </div>
      <div class="plate two codeblock" style="padding:18px 20px;margin-top:28px">WuuBall.mount(el, {<br>&nbsp;&nbsp;px: 28, state: "work",<br>&nbsp;&nbsp;reducedMotion: settings.motion === "reduce"<br>});<br><span style="color:var(--text-3)">// 未传入时跟随 prefers-reduced-motion</span></div>
    </div>
    <div class="span-7 start-6">
      <table class="spec" style="table-layout:fixed">
        <thead><tr><th style="width:120px">事件</th><th>默认</th><th style="width:200px">减少动态效果</th></tr></thead>
        <tbody>
          ${[
            ["开始思考", [0, 60, 120, 180].map((t) => { const k = 1 - Math.pow(1 - t / 180, 3); const a = W.pose("display", "rest"), b = W.pose("display", "think"); return frame(48, { pose: { gazeX: a.gazeX + (b.gazeX - a.gazeX) * k, gazeY: a.gazeY + (b.gazeY - a.gazeY) * k, scaleY: a.scaleY + (b.scaleY - a.scaleY) * k } }); }), frame(48, { state: "think" })],
            ["完成", settleFrames.slice(0, 4).map(([, s]) => s.replace(/width="64" height="64"/, 'width="48" height="48"')), frame(48, { state: "done" })],
            ["空闲", blinkFrames.slice(0, 4).map(([, s]) => s.replace(/width="64" height="64"/, 'width="48" height="48"')), frame(48, { state: "rest" })],
          ].map(([ev, seq, still]) => `<tr><td style="padding:16px 12px 16px 0"><span class="h4">${ev}</span></td><td><div class="row" style="--g:16px">${seq.join('<span class="cap">→</span>')}</div></td><td><div class="row" style="--g:12px">${still}<span class="cap">直接切换</span></div></td></tr>`).join("")}
        </tbody>
      </table>
      <div class="plate" style="margin-top:28px;padding:22px 26px;display:flex;gap:28px;align-items:center">
        <div data-wuu-live="rest,think,work,done" data-px="64" data-every="1800" data-reduced="1"></div>
        <div class="small" style="flex:1">这是一只开启了减少动态效果的实时小球：它每 1.8 秒切换一次状态，但没有过渡、眨眼和循环。交互式演示见 <span class="mono">brand/assets/motion/index.html</span>。</div>
      </div>
    </div>
  </div>`,
});

export default [character, states, boundaries, reduced];
