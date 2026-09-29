// Handoff: tokens, product mapping, assets, licences and open items.
import { page, title, ball, lockup, C, G, T, staticBall } from "../kit.mjs";

export const tokensPage = page({
  chapter: 8, id: "tokens",
  body: `
  <div class="grid">
    <div class="span-4">${title("设计变量", "Tokens")}
      <p class="lead">所有数值只定义一次：<span class="mono">brand/tokens/tokens.json</span>。CSS、标识、图标、手册页面都从它生成。</p>
      <div class="plate two codeblock" style="padding:18px 20px;margin-top:28px;font-size:12px">@import "brand/tokens/wuu-brand.css";<br><br>.hero {<br>&nbsp;&nbsp;background: var(--wuu-brand-canvas);<br>&nbsp;&nbsp;color: var(--wuu-brand-text);<br>&nbsp;&nbsp;font: var(--wuu-brand-type-display-weight)<br>&nbsp;&nbsp;&nbsp;&nbsp;var(--wuu-brand-type-display-size)/1.05<br>&nbsp;&nbsp;&nbsp;&nbsp;var(--wuu-brand-font-brand);<br>}<br><span style="color:var(--text-3)">/* 暗色：在祖先元素上加 .wuu-brand-dark */</span></div>
      <p class="body" style="margin-top:18px">变量名带 <span class="mono">--wuu-brand-</span> 前缀，官网和物料可以直接使用，不会和产品内部变量冲突。</p>
    </div>
    <div class="span-7 start-6">
      <div class="label">采用到产品时的对应关系（建议）</div>
      <table class="spec" style="margin-top:12px">
        <thead><tr><th style="width:200px">品牌变量</th><th style="width:200px">产品现有角色</th><th style="width:90px">亮</th><th>说明</th></tr></thead>
        <tbody>
          ${[
            ["canvas", "--paper", "底色"], ["surface", "--surface-1", "卡片、输入框"], ["surface-2", "--surface-2", "次级区域"], ["surface-3 / selection", "--selection-surface", "选中"],
            ["text", "--ink-strong", "标题与正文"], ["text-2", "--ink-soft", "次要正文"], ["text-3", "--ink-tertiary", "说明、元信息"], ["text-4", "--ink-faint", "占位、禁用"],
            ["line-1", "--hairline-soft", "表面内分隔"], ["line-2", "--hairline", "区块边界"], ["boundary", "--control-boundary", "控件边界 3 : 1"],
            ["focus", "--focus-ring", "键盘焦点"], ["success … info", "--success … --info", "状态"], ["ink", "--wuu-accent", "现为朱红，建议改为墨色"],
          ].map(([b, p, n]) => { const k = b.split(" ")[0]; const hex = C.light[k] ?? (k === "focus" ? C.interaction.focus.light : k === "ink" ? C.brand.ink : k === "success" ? C.status.success.light : ""); return `<tr><td class="mono" style="font-size:12px">${b}</td><td class="mono" style="font-size:12px">${p}</td><td><div class="row" style="--g:6px"><span class="sw" style="width:14px;height:14px;border-radius:4px;background:${hex}"></span><span class="mono" style="font-size:11px">${hex}</span></div></td><td>${n}</td></tr>`; }).join("")}
        </tbody>
      </table>
      <p class="cap" style="margin-top:12px">产品角色名来自 <span class="mono">desktop/src/renderer/styles/base.css</span>。本次交付不修改产品样式；是否采用、何时采用需要单独评审，并同步更新设计系统文档与看板。</p>
    </div>
  </div>`,
});

export const assets = page({
  chapter: 8, id: "assets",
  body: `
  <div class="grid">
    <div class="span-4">${title("素材清单", "Assets")}
      <p class="lead">所有素材都有生成器。改动 <span class="mono">tokens.json</span> 或页面源文件后，重新运行生成命令，不要手改导出文件。</p>
      <div class="plate two codeblock" style="padding:18px 20px;margin-top:28px;font-size:12px">npm ci --prefix brand<br>npm --prefix brand run build<br><span style="color:var(--text-3)"># 标识、图标、CSS 变量、手册 HTML</span><br><br>npm ci --prefix desktop<br>npm --prefix brand run render<br><span style="color:var(--text-3)"># 分享图、安装背景、文档预览图、PDF</span></div>
      <p class="body" style="margin-top:18px">渲染需要本机安装思源黑体（Source Han Sans SC 或 Noto Sans CJK SC）；缺少时脚本会停止并提示。</p>
    </div>
    <div class="span-7 start-6">
      <table class="spec">
        <thead><tr><th style="width:280px">路径</th><th>内容</th><th style="width:150px">格式</th></tr></thead>
        <tbody>
          ${[
            ["assets/logo/wuu-symbol[-small|-micro]-*.svg", "小球：三种光学尺寸 × 墨 / 纸 / 黑 / 白", "SVG"],
            ["assets/logo/wuu-wordmark-*.svg", "文字标识", "SVG"],
            ["assets/logo/wuu-lockup-horizontal[-small|-micro]-*.svg", "横式组合；X ≤ 28 px 用 small，X ≤ 14 px 用 micro", "SVG"],
            ["assets/logo/wuu-lockup-stacked-*.svg", "竖式组合", "SVG"],
            ["/assets/app-icon*", "应用图标沿用仓库现有的已批准原稿与成品；本次未改动", "SVG · PNG"],
            ["assets/favicon/", "网站图标（随系统明暗）、16 / 32 px；Apple Touch 180 px 由现有应用图标缩放", "SVG · PNG"],
            ["assets/social/", "分享图（中 / 英）、README 横幅（亮 / 暗）、发布卡片、头像", "PNG"],
            ["assets/installer/", "macOS 安装窗口背景 720 × 420 与 @2x", "PNG"],
            ["assets/motion/", "动效参考实现与交互演示", "JS · HTML"],
            ["tokens/", "设计变量源文件与生成的 CSS", "JSON · CSS"],
            ["fonts/", "Hanken Grotesk、Fragment Mono 与授权文件", "TTF · OFL"],
            ["manual/index.html", "本手册；浏览器中打开即可阅读", "HTML"],
          ].map(([p, d, f]) => `<tr><td class="mono" style="font-size:11.5px">${p.startsWith("/") ? p.slice(1) : `brand/${p}`}</td><td>${d}</td><td>${f}</td></tr>`).join("")}
        </tbody>
      </table>
    </div>
  </div>`,
});

export const open = page({
  chapter: 8, id: "licences-open-items",
  body: `
  <div class="grid">
    <div class="span-5">${title("授权与待验证事项", "Licences and open items")}
      <table class="spec" style="margin-top:32px">
        <thead><tr><th style="width:160px">素材</th><th>来源与授权</th></tr></thead>
        <tbody>
          <tr><td>小球与字标</td><td>本手册原创几何，由 <span class="mono">src/geometry.mjs</span> 生成，不是任何字体的轮廓。随仓库分发，适用仓库许可证（MIT）；MIT 不授予商标权，品牌资产的使用条款需要维护者另行确认。</td></tr>
          <tr><td>Hanken Grotesk</td><td>SIL OFL 1.1，The Hanken Grotesk Project Authors；随仓库分发，附 OFL.txt</td></tr>
          <tr><td>Fragment Mono</td><td>SIL OFL 1.1，The Fragment-Mono Project Authors；随仓库分发，附 OFL.txt</td></tr>
          <tr><td>思源黑体</td><td>SIL OFL 1.1，Adobe；不随仓库分发，从官方发布获取</td></tr>
          <tr><td>界面图标</td><td>产品自有图形 <span class="mono">desktop/src/shared/iconArtwork.ts</span></td></tr>
          <tr><td>现状截图</td><td>开发预览与仓库内的现有资产（提交 ef1713674），示例数据；仅用于现状对比</td></tr>
        </tbody>
      </table>
      <p class="cap" style="margin-top:12px">产品中现有的头像几何来自 blobatar（MIT）；本手册的小球是新绘制的，不依赖该库。</p>
    </div>
    <div class="span-6 start-7">
      <div class="label">尚未验证</div>
      <div class="rule-list" style="margin-top:10px;--rl:1fr">
        <div class="body"><strong>商标。</strong>“Wuu”名称与小球图形没有做商标检索。</div>
        <div class="body"><strong>用户研究。</strong>第 01 章的假设没有访谈或数据支持。</div>
        <div class="body"><strong>图标小尺寸。</strong>保留的应用图标在 32 px 以下动势线较密；是否需要小尺寸变体尚未评估。</div>
        <div class="body"><strong>印刷。</strong>只定义了屏幕色值，没有 CMYK 或专色值，也没有打样。</div>
        <div class="body"><strong>跨平台字体。</strong>手册在 Linux 的 Chromium 中配合 Noto Sans CJK SC 渲染检查；Windows 与 macOS 上的效果未检查。</div>
        <div class="body"><strong>色觉差异。</strong>七个 agent 色明度相同，灰度下无法区分；规则要求配名字，但没有做色觉模拟测试。</div>
        <div class="body"><strong>产品采用。</strong>界面示意没有进入产品代码，也没有做可用性测试。</div>
        <div class="body"><strong>名称读音与中文名。</strong>没有定义，本手册也不做推断。</div>
      </div>
    </div>
  </div>`,
});

export const back = page({
  id: "back", chrome: false,
  body: `
  <div class="abs" style="left:50%;top:50%;transform:translate(-50%,-50%);text-align:center">
    ${ball(120)}
    <div class="cap" style="margin-top:28px">Wuu 品牌手册 · 2026.9 · 第 1 版</div>
    <div class="cap" style="margin-top:4px">源文件：brand/ · 规则摘要：docs/zh-cn/project/brand.md</div>
  </div>`,
});

export default [tokensPage, assets, open, back];
