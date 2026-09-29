import { page, title, ball, lockup, C, G, staticBall, agentBall, CHAPTERS } from "../kit.mjs";

const REF = "reference";
// Crop a reference capture (2× pixels) to a CSS box. x, y, w, h are in 1× capture px.
const crop = (file, x, y, w, h, scale = 1, extra = "") =>
  `<div style="width:${w * scale}px;height:${h * scale}px;border-radius:8px;box-shadow:0 0 0 1px var(--line-2);background:url(${REF}/${file}) no-repeat;background-size:var(--bw);background-position:${-x * scale}px ${-y * scale}px;${extra}"></div>`;

export const cover = page({
  id: "cover", chrome: false,
  body: `
  <div class="abs" style="left:80px;top:72px">${lockup(26)}</div>
  <div class="abs" style="left:80px;top:360px">
    <div style="font:600 96px/1 var(--brand);letter-spacing:-.025em">品牌手册</div>
    <div style="font:500 30px/1.2 var(--brand);color:var(--text-3);margin-top:22px;letter-spacing:-.005em">Brand guidelines</div>
  </div>
  <div class="abs body" style="left:80px;bottom:72px;width:560px">
    <div class="row" style="--g:40px;--a:flex-start">
      <div><div class="label">版本</div><div class="h4" style="margin-top:6px">2026.9 · 第 1 版</div></div>
      <div><div class="label">范围</div><div class="h4" style="margin-top:6px;font-weight:500">标识、色彩、字体、图形、动效、应用与交付</div></div>
    </div>
  </div>
  <div class="abs" style="right:-150px;bottom:-230px">${ball(820)}</div>`,
});

export const contents = page({
  id: "contents", chrome: false,
  body: `
  <div class="abs" style="left:80px;top:72px">${lockup(12)}</div>
  <div class="grid" style="margin-top:40px">
    <div class="span-5">
      ${title("目录", "Contents")}
      <div class="rule-list" style="margin-top:44px;--rl:44px 1fr 40px">
        ${CHAPTERS.map((c) => `<div style="padding:12px 0"><span class="cap num">${c[0]}</span><span class="h4" style="font-weight:500">${c[1]} <span style="color:var(--text-3);font-weight:400">${c[2]}</span></span><span class="cap num" style="text-align:right" data-toc="${c[0]}"></span></div>`).join("")}
      </div>
    </div>
    <div class="span-6 start-7 stack" style="--s:28px;padding-top:6px">
      <div class="label">怎么用这本手册</div>
      <div class="rule-list" style="--rl:120px 1fr">
        <div><span class="h4">设计师</span><span class="body">先读第 01、03 章，理解小球的职责和标识规则；做页面前查第 04、05 章的角色和比例；交付前对照第 08 章的示例和误用页。</span></div>
        <div><span class="h4">工程师</span><span class="body">第 09 章列出变量、资产路径和生成命令。数值只来自 <code>brand/tokens/tokens.json</code>，不要从图片里量取。</span></div>
        <div><span class="h4">贡献者</span><span class="body">写文案前读“语言与语气”。在中文和英文里都写 Wuu；命令写 <code>wuu</code>。</span></div>
      </div>
      <div class="label" style="margin-top:44px">规则的强度</div>
      <div class="rule-list" style="--rl:120px 1fr">
        <div><span class="h4">必须</span><span class="body">标识构造、最小尺寸、色彩角色与对比度、状态色与品牌色的分工。违反会损害识别或可用性。</span></div>
        <div><span class="h4">建议</span><span class="body">版式、比例、插画构图。可以按内容调整，但要能说出理由。</span></div>
      </div>
      <p class="cap" style="max-width:560px">第 08 章中的产品界面是品牌体系的提案示意，不代表当前应用的样子；产品界面改造需要另行评审。</p>
    </div>
  </div>`,
});

export const facts = page({
  chapter: 0, id: "facts",
  body: `
  <div class="grid">
    <div class="span-4">${title("先弄清事实", "What we know, and what we assume")}
      <p class="lead">品牌判断建立在产品已经做到的事情上。中列来自代码和文档；右列是本手册采用的假设，还没有用户研究支持，需要后续验证。</p>
      <div class="label" style="margin-top:56px">本手册不包括</div>
      <div class="rule-list" style="margin-top:12px;--rl:1fr">
        <div class="body">产品界面的改造。第 08 章的界面是提案示意。</div>
        <div class="body">商标检索与注册。“Wuu”与小球图形尚未做任何商标检索。</div>
        <div class="body">用户研究、市场定位和商业主张。</div>
      </div>
    </div>
    <div class="span-4 start-6">
      <div class="row" style="--g:10px;margin-bottom:14px"><span class="tag fact">已确认</span><span class="cap">来源在括号中</span></div>
      <div class="rule-list" style="--rl:1fr">
        <div class="body">开源桌面应用：选择本地项目，让 agent 读代码、改文件、运行命令，并在应用里检查文件、diff 与结果。<span class="cap">（README）</span></div>
        <div class="body">模型服务由用户连接和选择；提示词与上下文发送给所选服务。<span class="cap">（README）</span></div>
        <div class="body">会话可以继续；插件增加工具与桌面功能；操作受权限模式约束。<span class="cap">（README，安全模型）</span></div>
        <div class="body">中文与英文界面、文档并行，文档站默认中文。<span class="cap">（docs/site.json）</span></div>
        <div class="body">小球是会话中的助手形象：15 种活动状态，只用眼睛表达；按服务商着色，按模型佩戴配饰。<span class="cap">（wuu-mascot-spec.ts）</span></div>
        <div class="body">MIT 许可；头像几何来自 blobatar（MIT）。<span class="cap">（README）</span></div>
      </div>
    </div>
    <div class="span-3 start-10">
      <div class="row" style="--g:10px;margin-bottom:14px"><span class="tag assume">假设</span><span class="cap">待验证</span></div>
      <div class="rule-list" style="--rl:1fr">
        <div class="body">主要用户是会阅读 diff、愿意审查 agent 改动的开发者。</div>
        <div class="body">用户每天在应用里停留很久，所以界面的耐看比第一眼的刺激更重要。</div>
        <div class="body">同一个人会同时使用多个模型和 agent，区分它们是长期需求。</div>
        <div class="body">中文与英文用户同等重要。</div>
        <div class="body">品牌主要通过产品本身、GitHub、文档站和官网被看到；暂无付费投放和实体物料计划。</div>
      </div>
    </div>
  </div>`,
});

export const audit = page({
  chapter: 0, id: "audit",
  body: `
  <div class="grid">
    <div class="span-4">${title("现在的品牌", "The current brand, audited")}
      <p class="lead">小球这条线索是清楚的，但它在每个触点上是不同的东西。问题不在某一个设计，而在于没有规则决定它们的关系。</p>
      <div class="rule-list" style="margin-top:36px;--rl:28px 1fr">
        <div><span class="h4 num">1</span><span class="body"><strong>三种身份色。</strong>官网是粉色 <span class="mono">#FFBDC2</span>，图标和首页是炭灰 <span class="mono">#353637</span>，会话头像随服务商变色。没有规定哪一个才是 Wuu。</span></div>
        <div><span class="h4 num">2</span><span class="body"><strong>名字有两种写法。</strong>README、官网和站点标题写 “wuu”，应用和文档正文写 “Wuu”。</span></div>
        <div><span class="h4 num">3</span><span class="body"><strong>没有文字标识。</strong>官网用衬线体排 “wuu”，侧栏用系统粗体排 “Wuu”。</span></div>
        <div><span class="h4 num">4</span><span class="body"><strong>两套语气。</strong>官网是暖米色、风景照和衬线体；应用是冷白、系统字体、朱红强调色和蓝色热力图。</span></div>
        <div><span class="h4 num">5</span><span class="body"><strong>居中的竖眼读成“暂停”。</strong>小尺寸头像上，两条居中的竖胶囊和媒体暂停键几乎一样。</span></div>
      </div>
    </div>
    <div class="span-7 start-6" style="position:relative;height:780px">
      <div class="abs" style="left:0;top:0">${crop("landing-home.jpg", 0, 0, 600, 375, 1, "background-size:600px")}<div class="cap" style="margin-top:8px">官网首页 · 1 2 3 4</div></div>
      <div class="abs" style="left:630px;top:0"><img src="../../assets/app-icon.png" width="200" height="200" style="display:block;border-radius:8px;box-shadow:0 0 0 1px var(--line-2)"><div class="cap" style="margin-top:8px">当前应用图标 · 1</div>
        <div class="row" style="--g:14px;margin-top:28px;--a:flex-end"><img src="../../assets/app-icon.png" width="32" height="32" style="box-shadow:0 0 0 1px var(--line-2)"><img src="../../assets/app-icon.png" width="16" height="16" style="box-shadow:0 0 0 1px var(--line-2)"></div><div class="cap" style="margin-top:6px;width:200px">32 / 16 px：三道动势线变成噪点</div></div>
      <div class="abs" style="left:0;top:450px">${crop("app-light.png", 0, 0, 252, 190, 1, "background-size:542.5px")}<div class="cap" style="margin-top:8px">应用侧栏 · 2 3</div></div>
      <div class="abs" style="left:276px;top:450px">${crop("app-empty.png", 30, 150, 260, 190, 1, "background-size:353px")}<div class="cap" style="margin-top:8px">首页问候 · 1 4</div></div>
      <div class="abs" style="left:560px;top:450px">
        <div class="plate" style="width:270px;height:190px;display:flex;align-items:center;justify-content:center;gap:20px;box-shadow:0 0 0 1px var(--line-2)">
          ${pauseSample(40)}${pauseSample(24)}${pauseSample(16)}<span style="width:1px;height:40px;background:var(--line-2)"></span>${pauseIcon(24)}
        </div>
        <div class="cap" style="margin-top:8px">左：居中竖眼的头像；右：暂停图标 · 5</div></div>
    </div>
  </div>`,
});

// Current avatar face (centred vertical capsules) next to a media pause glyph.
function pauseSample(px) {
  return `<svg width="${px}" height="${px}" viewBox="0 0 100 100"><circle cx="50" cy="50" r="50" fill="#353637"/><rect x="31" y="30" width="13" height="36" rx="6.5" fill="#fff"/><rect x="56" y="30" width="13" height="36" rx="6.5" fill="#fff"/></svg>`;
}
function pauseIcon(px) {
  return `<svg width="${px}" height="${px}" viewBox="0 0 24 24"><circle cx="12" cy="12" r="12" fill="#141411"/><rect x="8" y="7" width="2.6" height="10" rx="1.3" fill="#fff"/><rect x="13.4" y="7" width="2.6" height="10" rx="1.3" fill="#fff"/></svg>`;
}

export const ballToday = page({
  chapter: 0, id: "ball-today",
  body: `
  <div class="grid">
    <div class="span-4">${title("小球现在做什么", "What the ball already does")}
      <p class="lead">在产品里，小球不是装饰。它是会话里的助手，用眼睛告诉你它在做什么。这决定了品牌应该怎样对待它。</p>
      <div class="stack" style="--s:26px;margin-top:40px">
        <div><div class="h4">只用眼睛表达</div><p class="body" style="margin-top:6px">15 种活动状态都没有嘴。眼睛的高度、倾斜和视线方向承担全部信息。<strong>所以标识的核心是眼睛，不是圆。</strong></p></div>
        <div><div class="h4">身体颜色区分 agent</div><p class="body" style="margin-top:6px">头像按服务商取色，按模型换配饰。<strong>所以彩色的球属于其他 agent；Wuu 自己需要一个固定的颜色。</strong></p></div>
        <div><div class="h4">在过程行里变形</div><p class="body" style="margin-top:6px">执行命令、编辑、搜索时，小球会变成对应的活动图形。<strong>这是产品功能表达，不是品牌标识；两者要分开管理。</strong></p></div>
      </div>
    </div>
    <div class="span-7 start-6">
      <div style="width:720px;height:443px;border-radius:8px;box-shadow:0 0 0 1px var(--line-2);background:#fff url(${REF}/activity-states.png) no-repeat center/100%"></div>
      <div class="cap" style="margin-top:8px">当前产品的活动状态：主头像、32 px 与过程行尺寸（开发预览，示例数据）</div>
      <div style="margin-top:32px;width:720px;height:183px;border-radius:8px;box-shadow:0 0 0 1px var(--line-2);background:#fff url(${REF}/avatars.png) no-repeat 0 0/720px"></div>
      <div class="cap" style="margin-top:8px">按模型佩戴的配饰与 agent 形体（开发预览）</div>
    </div>
  </div>`,
});

export const positioning = page({
  chapter: 0, id: "positioning",
  body: `
  <div class="grid">
    <div class="span-4">${title("定位与性格", "Positioning and character")}</div>
    <div class="span-7 start-6">
      <p style="font:500 26px/1.5 var(--brand);letter-spacing:-.005em">Wuu 是在本地项目里和 AI agent 一起工作的桌面应用。</p>
      <p style="font:500 26px/1.5 var(--brand);letter-spacing:-.005em;color:var(--text-3)">品牌要让人一眼看清 agent 正在做什么，并且愿意每天长时间使用它。</p>
    </div>
  </div>
  <div class="grid" style="margin-top:64px">
    ${[
      ["专注", "Attentive", "小球的视线总是朝向工作：在标识里看向文字，在会话里看向正在处理的内容。", "不让小球盯着用户卖萌或求关注。",
        `<div class="row" style="--g:16px">${staticBall(64, "rest")}<div class="stack" style="--s:8px"><div style="width:150px;height:9px;border-radius:5px;background:var(--line-2)"></div><div style="width:110px;height:9px;border-radius:5px;background:var(--line-2)"></div></div></div>`],
      ["直白", "Plain", "文案先说结果和下一步；版式靠位置、留白和字重建立层级，只有一个角色元素。", "不用口号、感叹号和堆叠的寒暄。",
        `<div class="plate" style="padding:14px 16px;width:300px;box-shadow:0 0 0 1px var(--line-1)"><div class="small" style="color:var(--text)">已修改 3 个文件，测试通过。</div><div class="cap" style="margin-top:4px">查看改动　继续</div></div>`],
      ["轻松", "Light", "个性来自小球短而少的动作：一次眨眼，一次回落。动作不挡住任何操作。", "不加 3D 质感、渐变、贴纸式插画或随机彩蛋。",
        `<div class="row" style="--g:12px;--a:flex-end">${[1, 0.55, 0.1, 0.55, 1].map((k) => G.WuuBall.staticSVG({ px: 44, pose: { ...G.WuuBall.pose("display", "rest"), scaleY: k }, body: C.brand.ink, eye: C.brand.paper })).join("")}<span class="cap" style="margin-left:6px">190 ms</span></div>`],
    ].map(([zh, en, does, not, fig]) => `
    <div class="span-4">
      <div style="height:84px;display:flex;align-items:center">${fig}</div>
      <div class="row" style="--g:10px;--a:baseline;margin-top:22px"><span style="font:600 24px/1 var(--brand)">${zh}</span><span class="cap">${en}</span></div>
      <p class="body" style="margin-top:14px">${does}</p>
      <p class="body" style="margin-top:10px;color:var(--text-3)">不做：${not}</p>
    </div>`).join("")}
  </div>
  <div class="grid abs" style="left:80px;bottom:112px">
    <div class="span-3"><div class="label">做设计判断时问三个问题</div></div>
    ${[
      ["它能帮人看清 agent 在做什么吗？", "看不清的状态、含糊的文案和抢眼的装饰都会让答案变成否。"],
      ["连续看八小时还舒服吗？", "高饱和的大面积色块、持续循环的动画和过小的灰字都通不过。"],
      ["去掉小球后，这个设计还成立吗？", "小球是点睛，不是支撑。版面需要靠自己的层级成立。"],
    ].map(([q, a]) => `<div class="span-3"><div class="h3">${q}</div><p class="body" style="margin-top:8px">${a}</p></div>`).join("")}
  </div>`,
});

export const voice = page({
  chapter: 0, id: "voice",
  body: `
  <div class="grid">
    <div class="span-4">${title("语言与语气", "Voice and tone")}
      <p class="lead">Wuu 说话像一位靠谱的同事：先讲结果，再讲原因，最后给出下一步。</p>
      <div class="rule-list" style="margin-top:36px;--rl:1fr">
        <div class="body"><strong>动作用动词。</strong>读取、修改、运行、等待你确认。不用“智能”“赋能”“一键搞定”。</div>
        <div class="body"><strong>失败要说影响和出路。</strong>什么没有完成，现有内容是否保留，怎么恢复。</div>
        <div class="body"><strong>“我”只属于对话。</strong>助手的回复和问候可以用第一人称；按钮、设置和错误提示不用。</div>
        <div class="body"><strong>不用感叹号、表情符号和流行语。</strong>轻松来自简短，而不是语气词。</div>
        <div class="body"><strong>中英文之间、数字与单位之间留一个空格。</strong>例如 “在 Wuu 里”“20 MB”。</div>
      </div>
    </div>
    <div class="span-7 start-6">
      <table class="spec">
        <thead><tr><th style="width:92px">场景</th><th style="width:190px">现在</th><th style="width:250px">改写</th><th>为什么</th></tr></thead>
        <tbody>
          <tr><td>输入框占位</td><td>即刻开始<br><span class="cap">Get started</span></td><td>描述要做的事<br><span class="cap">Describe the task</span></td><td>占位文字告诉人该输入什么，而不是催促。</td></tr>
          <tr><td>保存失败</td><td>保存失败<br><span class="cap">Failed to save</span></td><td>设置没有保存，请重试。<br><span class="cap">Settings weren’t saved. Try again.</span></td><td>说清是什么没完成，并给出下一步。</td></tr>
          <tr><td>复制失败</td><td>复制失败</td><td>无法写入剪贴板</td><td>指出失败的具体环节。</td></tr>
          <tr><td>深夜问候</td><td>夜深了，还要继续吗？<br><span class="cap">It’s late. Want to keep going?</span></td><td style="color:var(--success)">保留</td><td>简短、有人味，不推用户多做。</td></tr>
          <tr><td>官网首屏</td><td>给想法留一点空间</td><td>在本地项目里，和 agent 一起把事做完。<br><span class="cap">Work with agents on your own projects.</span></td><td>第一句话要说明产品是什么。</td></tr>
          <tr><td>官网副标题</td><td>Wuu 是开源的 coding agent。选好模型，从一个想法开始，一起做点什么吧。</td><td>读代码、改文件、运行命令；每一处改动都留给你检查。</td><td>用产品事实代替邀请语。</td></tr>
        </tbody>
      </table>
      <p class="cap" style="margin-top:16px">“现在”一列摘自 <span class="mono">desktop/src/renderer/i18n/resources</span> 与 <span class="mono">landing/zh.html</span>。改写是写法示例，落地前要按实际行为核对，例如失败后原设置是否保留。</p>
    </div>
  </div>`,
});

export default [cover, contents, facts, audit, ballToday, positioning, voice];
